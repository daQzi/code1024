package service

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type generationOriginFixture struct {
	generationReplayFixture
	candidates []GenerationVideoDecodeCandidate
	parents    map[string]*GenerationTask
	decodeErr  error
}

func (r *generationOriginFixture) ListVideoDecodeCandidates(context.Context, int64, int64, int64, string, string) ([]GenerationVideoDecodeCandidate, error) {
	return r.candidates, r.decodeErr
}

func (r *generationOriginFixture) GetOwned(_ context.Context, id string, user, key int64) (*GenerationTask, error) {
	parent := r.parents[id]
	if parent == nil || parent.UserID != user || parent.APIKeyID != key {
		return nil, ErrGenerationTaskNotFound
	}
	return parent, nil
}

func (r *generationOriginFixture) GetVideoOrigin(ctx context.Context, id string, user, key, group int64) (*GenerationTask, error) {
	parent, err := r.GetOwned(ctx, id, user, key)
	if err != nil || parent.GroupID != group {
		return nil, ErrGenerationTaskNotFound
	}
	return parent, nil
}

func generationOriginsFixture(t *testing.T) (*GenerationService, *APIKey, *Account, *GenerationTask, json.RawMessage, *generationFixtureHTTP) {
	t.Helper()
	plugins, account, upstream := generationPluginFixture(t, "doubao")
	version := plugins.repo.(*generationPluginFixtureRepo).version
	account.Status, account.Schedulable = StatusActive, true
	account.Credentials[newAPIPluginVideoModelsKey] = pluginVideoModels(version.Meta)
	account.Credentials[newAPIPluginNativeVideosKey] = pluginNativeVideoBinding(version.Meta)
	account.Credentials[NewAPIVideoPricingKey] = map[string]any{"*": map[string]any{"mode": "fixed", "unit": "request", "unit_price": "0.16"}}
	group := int64(3)
	key := &APIKey{ID: 2, UserID: 1, GroupID: &group, User: &User{ID: 1}, Group: &Group{ID: group, RateMultiplier: 1}}
	parent := &GenerationTask{ID: "video_" + strings.Repeat("1", 40), UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: account.ID, PluginVersionID: 1, Model: "doubao-seedance-2-5-260628", UpstreamModel: "doubao-seedance-2-5-260628", Platform: PlatformOpenAI, Action: "image_to_video", Status: GenerationSucceeded, UpstreamID: "private-parent-id", Data: json.RawMessage(`{"private":"parent-data","count":1}`), Connection: generationAccountConnection(account)}
	body := json.RawMessage(`{"model":"doubao-seedance-2-5-260628","content":[{"type":"draft_task","draft_task":{"id":"` + parent.ID + `"}},{"type":"text","text":"continue"}],"duration":8,"resolution":"720p"}`)
	repo := &generationOriginFixture{candidates: []GenerationVideoDecodeCandidate{{VersionID: 1}}, parents: map[string]*GenerationTask{parent.ID: parent}}
	return &GenerationService{repo: repo, plugins: plugins}, key, account, parent, body, upstream
}

func TestGenerationOriginAdmissionPinsConnectionAndRewritesUpstreamID(t *testing.T) {
	s, key, account, parent, body, upstream := generationOriginsFixture(t)
	ctx := WithNativeVideoPluginRouting(context.Background(), GenerationProtocolArk)
	ctx, err := s.PrepareVideoRouting(ctx, key, PlatformOpenAI, parent.Model, body, GenerationProtocolArk)
	require.NoError(t, err)
	require.True(t, GenerationVideoRoutingPrepared(ctx))
	require.True(t, account.IsSchedulableForModelWithContext(ctx, parent.Model))
	other := *account
	other.ID++
	require.False(t, other.IsSchedulableForModelWithContext(ctx, parent.Model), "a higher-priority account cannot claim a derived task")
	other = *account
	other.Credentials = maps.Clone(account.Credentials)
	other.Credentials["base_url"] = "https://other.example"
	require.False(t, other.IsSchedulableForModelWithContext(ctx, parent.Model))
	task, err := s.CreateVideo(ctx, key, nil, account, parent.Model, body, "derived-once", GenerationProtocolArk)
	require.NoError(t, err)
	require.Equal(t, "0.16", task.Price.Hold.String())
	require.Len(t, task.Origins, 1)
	require.Equal(t, parent.UpstreamID, task.Origins[0].UpstreamTaskID)
	require.Empty(t, upstream.requests, "preflight, decoding, estimation and admission do not call the upstream")
	upstream.response = `{"id":"private-child"}`
	require.NoError(t, s.plugins.SubmitTask(context.Background(), account, task))
	require.Len(t, upstream.requests, 1)
	sent, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Contains(t, string(sent), parent.UpstreamID)
	require.NotContains(t, string(sent), parent.ID)
	public, err := json.Marshal(task)
	require.NoError(t, err)
	require.NotContains(t, string(public), "private-parent")
	require.NotContains(t, string(public), "parent-data")
	view, err := json.Marshal(task.VideoView())
	require.NoError(t, err)
	require.Contains(t, string(view), parent.ID)
	require.NotContains(t, string(view), "private-parent")
	// An accepted request replays before any plugin, origin or price lookup.
	s.repo.(*generationOriginFixture).task = task
	s.plugins = nil
	replayed, err := s.CreateVideo(ctx, key, nil, &Account{}, parent.Model, body, "derived-once", GenerationProtocolArk)
	require.NoError(t, err)
	require.Same(t, task, replayed)
}

func TestGenerationOriginRejectsForeignOrConflictingParents(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*GenerationTask)
	}{
		{"foreign user", func(p *GenerationTask) { p.UserID++ }},
		{"foreign key", func(p *GenerationTask) { p.APIKeyID++ }},
		{"foreign group", func(p *GenerationTask) { p.GroupID++ }},
		{"foreign platform", func(p *GenerationTask) { p.Platform = PlatformGrok }},
		{"not submitted", func(p *GenerationTask) { p.UpstreamID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, key, _, parent, body, upstream := generationOriginsFixture(t)
			tc.mutate(parent)
			_, err := s.PrepareVideoRouting(context.Background(), key, PlatformOpenAI, parent.Model, body, GenerationProtocolArk)
			require.ErrorIs(t, err, ErrGenerationOrigin)
			require.Nil(t, s.repo.(*generationOriginFixture).held)
			require.Empty(t, upstream.requests)
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*GenerationTask)
	}{
		{"different account", func(p *GenerationTask) { p.AccountID++ }},
		{"different version", func(p *GenerationTask) { p.PluginVersionID++ }},
		{"different address", func(p *GenerationTask) { p.Connection.BaseURL = "https://other.example" }},
		{"different kind", func(p *GenerationTask) { p.Connection.Kind = "new_api" }},
		{"different config", func(p *GenerationTask) { p.ConfigEncrypted = "changed" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, key, _, parent, body, upstream := generationOriginsFixture(t)
			second := *parent
			second.ID = "video_" + strings.Repeat("2", 40)
			tc.mutate(&second)
			s.repo.(*generationOriginFixture).parents[second.ID] = &second
			body = json.RawMessage(strings.Replace(string(body), `{"type":"text","text":"continue"}`, `{"type":"draft_task","draft_task":{"id":"`+second.ID+`"}}`, 1))
			_, err := s.PrepareVideoRouting(context.Background(), key, PlatformOpenAI, parent.Model, body, GenerationProtocolArk)
			require.ErrorIs(t, err, ErrGenerationOrigin)
			require.Empty(t, upstream.requests)
		})
	}
}

func TestGenerationOriginPlanRejectsChangesBeforeAndAfterLateIngress(t *testing.T) {
	s, key, account, parent, body, upstream := generationOriginsFixture(t)
	ctx, err := s.PrepareVideoRouting(context.Background(), key, PlatformOpenAI, parent.Model, body, GenerationProtocolArk)
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		mutate func(*Account)
	}{
		{"account", func(a *Account) { a.ID++ }},
		{"version", func(a *Account) { a.Credentials[NewAPIPluginRevisionKey] = 2 }},
		{"address", func(a *Account) { a.Credentials["base_url"] = "https://other.example" }},
		{"kind", func(a *Account) { a.Credentials["newapi_upstream_kind"] = "new_api" }},
		{"configuration", func(a *Account) { a.Credentials[newAPIPluginConfigEncryptedKey] = "changed" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *account
			copy.Credentials = maps.Clone(account.Credentials)
			tc.mutate(&copy)
			_, err := s.CreateVideo(ctx, key, nil, &copy, parent.Model, body, "unaccepted", GenerationProtocolArk)
			require.ErrorIs(t, err, ErrGenerationOrigin)
			require.Nil(t, s.repo.(*generationOriginFixture).held)
		})
	}
	changed := json.RawMessage(strings.Replace(string(body), parent.ID, "video_"+strings.Repeat("2", 40), 1))
	_, _, err = s.plugins.DecodeVideoProtocol(ctx, account, 1, parent.Model, changed, GenerationProtocolArk)
	require.ErrorIs(t, err, ErrGenerationOrigin)
	removed := json.RawMessage(`{"model":"` + parent.Model + `","content":[{"type":"text","text":"continue"}],"duration":8}`)
	_, _, err = s.plugins.DecodeVideoProtocol(ctx, account, 1, parent.Model, removed, GenerationProtocolArk)
	require.ErrorIs(t, err, ErrGenerationOrigin)
	plain, err := s.PrepareVideoRouting(context.Background(), key, PlatformOpenAI, parent.Model, removed, GenerationProtocolArk)
	require.NoError(t, err)
	_, _, err = s.plugins.DecodeVideoProtocol(plain, account, 1, parent.Model, body, GenerationProtocolArk)
	require.ErrorIs(t, err, ErrGenerationOrigin, "late ingress cannot introduce a parent")
	require.Empty(t, upstream.requests)
}

type generationOriginVersionRepo struct {
	NewAPIPluginRepository
	versions map[int64]*NewAPIPluginVersion
}

func (r *generationOriginVersionRepo) Get(_ context.Context, id int64) (*NewAPIPluginVersion, error) {
	return r.versions[id], nil
}

func TestGenerationOriginDecoderAmbiguityDisabledAndNetworkIsolation(t *testing.T) {
	t.Run("ambiguous parents", func(t *testing.T) {
		s, key, _, parent, body, upstream := generationOriginsFixture(t)
		first := s.plugins.repo.(*generationPluginFixtureRepo).version
		second := *first
		second.ID = 2
		second.Source = strings.ReplaceAll(first.Source, "if (originTaskIds.length) intent.originTaskIds = originTaskIds;", "")
		require.NotEqual(t, first.Source, second.Source)
		s.plugins.repo = &generationOriginVersionRepo{versions: map[int64]*NewAPIPluginVersion{1: first, 2: &second}}
		s.repo.(*generationOriginFixture).candidates = []GenerationVideoDecodeCandidate{{VersionID: 1}, {VersionID: 2}}
		_, err := s.PrepareVideoRouting(context.Background(), key, PlatformOpenAI, parent.Model, body, GenerationProtocolArk)
		require.ErrorIs(t, err, ErrGenerationDecode)
		require.Empty(t, upstream.requests)
	})
	t.Run("disabled plugin executes nothing", func(t *testing.T) {
		s, key, _, parent, body, upstream := generationOriginsFixture(t)
		s.plugins.repo.(*generationPluginFixtureRepo).version.Enabled = false
		_, err := s.PrepareVideoRouting(context.Background(), key, PlatformOpenAI, parent.Model, body, GenerationProtocolArk)
		require.ErrorIs(t, err, ErrGenerationDecode)
		require.Empty(t, upstream.requests)
	})
	t.Run("decode cannot fetch", func(t *testing.T) {
		s, key, _, parent, body, upstream := generationOriginsFixture(t)
		version := s.plugins.repo.(*generationPluginFixtureRepo).version
		before := version.Source
		version.Source = strings.ReplaceAll(version.Source, `const originTaskIds = draftTaskIds(body.content);`, `fetch("https://api.example.com/side-effect"); const originTaskIds = draftTaskIds(body.content);`)
		require.NotEqual(t, before, version.Source)
		_, err := s.PrepareVideoRouting(context.Background(), key, PlatformOpenAI, parent.Model, body, GenerationProtocolArk)
		require.ErrorIs(t, err, ErrGenerationDecode)
		require.Empty(t, upstream.requests)
	})
}

func TestGenerationOriginPreviewDoesNotPersistMultipart(t *testing.T) {
	s, key, _, _, _, upstream := generationOriginsFixture(t)
	kind, body := multiFileRequest(t, []string{"input_reference"}, []string{"fixture"})
	files := &generationMultiFileFixture{}
	s.repo = &generationFileTaskRepo{GenerationFileRepository: files}
	ctx, normalized, cleanup, err := s.ReadVideoMultipart(context.Background(), key, kind, strings.NewReader(string(body)))
	require.NoError(t, err)
	defer cleanup()
	preview, refs, err := s.previewVideoDecodeFiles(ctx, key, normalized, GenerationProtocolOpenAI)
	require.NoError(t, err)
	require.NotEmpty(t, preview)
	require.Len(t, refs, 1)
	require.Equal(t, "request_file:input_reference", refs[0].Ref)
	require.Empty(t, refs[0].ID)
	require.Zero(t, files.stored)
	require.Empty(t, upstream.requests)
}
