package service

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func generationRemixFixture(t *testing.T) (*GenerationService, *APIKey, *Account, *GenerationTask, *generationFixtureHTTP) {
	t.Helper()
	plugins, account, upstream := generationPluginFixture(t, "sora")
	version := plugins.repo.(*generationPluginFixtureRepo).version
	account.Status, account.Schedulable = StatusActive, true
	account.Credentials[newAPIPluginVideoModelsKey] = pluginVideoModels(version.Meta)
	account.Credentials[NewAPIVideoPricingKey] = map[string]any{"*": map[string]any{"mode": "fixed", "unit": "second", "unit_price": "0.2"}}
	group := int64(3)
	key := &APIKey{ID: 2, UserID: 1, GroupID: &group, User: &User{ID: 1}, Group: &Group{ID: group, RateMultiplier: 1}}
	parent := &GenerationTask{ID: "video_" + strings.Repeat("1", 40), UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: account.ID, PluginVersionID: 1, Model: "sora-2", UpstreamModel: "sora-2", Platform: PlatformOpenAI, Protocol: GenerationProtocolOpenAI, Action: "text_to_video", Status: GenerationSucceeded, UpstreamID: "private-parent", Data: json.RawMessage(`{"secret":"private-data"}`), PublicFields: GenerationVideoFields{Seconds: "8", Size: "1280x720"}, Connection: generationAccountConnection(account)}
	repo := &generationOriginFixture{candidates: []GenerationVideoDecodeCandidate{{VersionID: 1}}, parents: map[string]*GenerationTask{parent.ID: parent}}
	return &GenerationService{repo: repo, plugins: plugins, gateway: plugins.gateway}, key, account, parent, upstream
}

func TestGenerationRemixUsesOriginalDriverConnectionAndSelectors(t *testing.T) {
	s, key, account, parent, upstream := generationRemixFixture(t)
	raw := json.RawMessage(`{"prompt":"Make the ocean blue"}`)
	ctx, prepared, err := s.PrepareVideoRemix(WithGenerationVideoRemix(context.Background(), parent.ID), key, raw)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"sora-2","prompt":"Make the ocean blue"}`, string(prepared))
	ctx = WithNativeVideoPluginRouting(ctx, GenerationProtocolOpenAI)
	ctx, err = s.PrepareVideoRouting(ctx, key, PlatformOpenAI, parent.Model, prepared, GenerationProtocolOpenAI)
	require.NoError(t, err)
	require.True(t, GenerationVideoRoutingPrepared(ctx))
	other := *account
	other.ID++
	require.False(t, other.IsSchedulableForModelWithContext(ctx, parent.Model))
	task, err := s.CreateVideo(ctx, key, nil, account, parent.Model, prepared, "remix-once", GenerationProtocolOpenAI)
	require.NoError(t, err)
	require.Equal(t, "remix", task.Action)
	require.Equal(t, "1.6", task.Price.Hold.String())
	require.Equal(t, parent.PublicFields.Seconds, task.PublicFields.Seconds)
	require.Equal(t, parent.PublicFields.Size, task.PublicFields.Size)
	require.Len(t, task.Origins, 1)
	require.Equal(t, parent.UpstreamID, task.Origins[0].UpstreamTaskID)
	require.Equal(t, parent.ID, task.VideoView()["remixed_from_video_id"])
	require.Empty(t, upstream.requests)
	upstream.response = `{"id":"private-child","status":"queued"}`
	require.NoError(t, s.plugins.SubmitTask(context.Background(), account, task))
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/videos/private-parent/remix", upstream.requests[0].URL.Path)
	wire, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.JSONEq(t, string(prepared), string(wire), "inherited selectors authorize billing but are not extra upstream remix fields")
	require.NotContains(t, string(wire), parent.ID)
	require.False(t, gjson.GetBytes(wire, "seconds").Exists())
	public, err := json.Marshal(task.VideoView())
	require.NoError(t, err)
	require.NotContains(t, string(public), parent.UpstreamID)
	require.NotContains(t, string(public), "private-data")
	s.repo.(*generationOriginFixture).task = task
	s.plugins = nil
	replayContext := WithGenerationVideoRemix(context.Background(), parent.ID)
	replayed, err := s.ReplayVideo(replayContext, key.UserID, key.ID, "remix-once", raw, GenerationProtocolOpenAI)
	require.NoError(t, err)
	require.Same(t, task, replayed)
	for _, replay := range []struct {
		ctx  context.Context
		body json.RawMessage
	}{
		{WithGenerationVideoRemix(context.Background(), "video_"+strings.Repeat("2", 40)), raw},
		{context.Background(), raw},
		{replayContext, json.RawMessage(`{"prompt":"changed"}`)},
	} {
		_, err := s.ReplayVideo(replay.ctx, key.UserID, key.ID, "remix-once", replay.body, GenerationProtocolOpenAI)
		require.ErrorIs(t, err, ErrGenerationIdempotencyConflict)
	}
}

func TestGenerationRemixRejectsUnownedUnsupportedAndUnboundedInputs(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*APIKey, *GenerationTask, *NewAPIPluginVersion)
	}{
		{"foreign user", func(_ *APIKey, p *GenerationTask, _ *NewAPIPluginVersion) { p.UserID++ }},
		{"foreign key", func(_ *APIKey, p *GenerationTask, _ *NewAPIPluginVersion) { p.APIKeyID++ }},
		{"foreign group", func(_ *APIKey, p *GenerationTask, _ *NewAPIPluginVersion) { p.GroupID++ }},
		{"unfinished", func(_ *APIKey, p *GenerationTask, _ *NewAPIPluginVersion) { p.Status = GenerationRunning }},
		{"not submitted", func(_ *APIKey, p *GenerationTask, _ *NewAPIPluginVersion) { p.UpstreamID = "" }},
		{"disabled", func(_ *APIKey, _ *GenerationTask, v *NewAPIPluginVersion) { v.Enabled = false }},
		{"modified driver", func(_ *APIKey, _ *GenerationTask, v *NewAPIPluginVersion) { v.Source += "\n" }},
		{"other driver", func(_ *APIKey, _ *GenerationTask, v *NewAPIPluginVersion) { v.Meta.Key = "other" }},
		{"duration too large", func(_ *APIKey, p *GenerationTask, _ *NewAPIPluginVersion) { p.PublicFields.Seconds = "3601" }},
		{"duration not finite", func(_ *APIKey, p *GenerationTask, _ *NewAPIPluginVersion) { p.PublicFields.Seconds = "NaN" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, key, _, parent, upstream := generationRemixFixture(t)
			test.mutate(key, parent, s.plugins.repo.(*generationPluginFixtureRepo).version)
			_, _, err := s.PrepareVideoRemix(WithGenerationVideoRemix(context.Background(), parent.ID), key, json.RawMessage(`{"prompt":"Ocean"}`))
			require.Error(t, err)
			require.Empty(t, upstream.requests)
			require.Nil(t, s.repo.(*generationOriginFixture).held)
		})
	}
	s, key, account, parent, upstream := generationRemixFixture(t)
	ctx := WithGenerationVideoRemix(context.Background(), parent.ID)
	for _, body := range []string{`{}`, `[]`, `{"prompt":null}`, `{"prompt":" "}`, `{"prompt":"a","prompt":"b"}`, `{"Prompt":"a"}`, `{"prompt":"a","model":"other"}`, `{"prompt":"a","seconds":4}`, `{"prompt":"` + strings.Repeat("a", 8193) + `"}`} {
		_, _, err := s.PrepareVideoRemix(ctx, key, json.RawMessage(body))
		require.ErrorIs(t, err, ErrGenerationDecode)
	}
	parent.PublicFields.Seconds = ""
	preparedCtx, prepared, err := s.PrepareVideoRemix(ctx, key, json.RawMessage(`{"prompt":"Ocean"}`))
	require.NoError(t, err)
	preparedCtx, err = s.PrepareVideoRouting(preparedCtx, key, PlatformOpenAI, parent.Model, prepared, GenerationProtocolOpenAI)
	require.NoError(t, err)
	_, err = s.CreateVideo(preparedCtx, key, nil, account, parent.Model, prepared, "no-default", GenerationProtocolOpenAI)
	require.ErrorContains(t, err, "estimated second usage")
	require.Nil(t, s.repo.(*generationOriginFixture).held)
	require.Empty(t, upstream.requests)
	facts := generationRemixEstimatedFacts(generationRemix{})
	require.False(t, gjson.GetBytes(facts, "seconds").Exists(), "unknown historical duration is not guessed")
}

func TestGenerationVideoRemixRoutes(t *testing.T) {
	for _, path := range []string{"/v1/videos/video_123/remix", "/videos/video_123/remix"} {
		id, ok := GenerationVideoRemixOrigin(path)
		require.True(t, ok)
		require.Equal(t, "video_123", id)
		protocol, ok := GenerationVideoCreateProtocol(path)
		require.True(t, ok)
		require.Equal(t, GenerationProtocolOpenAI, protocol)
	}
	for _, path := range []string{"/v11/videos/a/remix", "/v1/videos//remix", "/v1/videos/a/b/remix", "/v1/videos/a/remix/", "/else/videos/a/remix"} {
		_, ok := GenerationVideoRemixOrigin(path)
		require.False(t, ok, path)
	}
	s, key, _, _, _ := generationRemixFixture(t)
	_, _, err := s.PrepareVideoRemix(WithGenerationVideoRemix(context.Background(), "video_"+strings.Repeat("g", 40)), key, json.RawMessage(`{"prompt":"ocean"}`))
	require.ErrorIs(t, err, ErrGenerationOrigin)
}
