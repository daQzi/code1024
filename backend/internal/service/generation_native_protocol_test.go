package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGenerationNativeVideoDecodeSubmitAndPoll(t *testing.T) {
	for _, tc := range []struct{ name, plugin, protocol, model, body, submit, done, path, action string }{
		{"xai generation", "xai", GenerationProtocolXAI, "grok-imagine-video", `{"model":"grok-imagine-video","prompt":"ocean","duration":8,"resolution":"720p"}`, `{"request_id":"native-upstream"}`, `{"status":"done","video":{"url":"https://media.example/video.mp4","duration":8}}`, "/v1/videos/generations", "text_to_video"},
		{"xai edit", "xai", GenerationProtocolXAIEdit, "grok-imagine-video", `{"model":"grok-imagine-video","prompt":"ocean","video":{"url":"https://media.example/input.mp4","duration":8},"resolution":"720p"}`, `{"request_id":"native-upstream"}`, `{"status":"done","video":{"url":"https://media.example/video.mp4","duration":8}}`, "/v1/videos/edits", "video_edit"},
		{"xai extend", "xai", GenerationProtocolXAIExtend, "grok-imagine-video", `{"model":"grok-imagine-video","prompt":"ocean","video":{"url":"https://media.example/input.mp4","duration":8},"duration":6,"resolution":"480p"}`, `{"request_id":"native-upstream"}`, `{"status":"done","video":{"url":"https://media.example/video.mp4","duration":14}}`, "/v1/videos/extensions", "video_extension"},
		{"ark multimodal", "doubao", GenerationProtocolArk, "doubao-seedance-2-5-260628", `{"model":"doubao-seedance-2-5-260628","content":[{"type":"text","text":"ocean"},{"type":"image_url","image_url":{"url":"https://media.example/input.png"},"role":"first_frame"}],"duration":8,"resolution":"720p","generate_audio":false}`, `{"id":"native-upstream"}`, `{"status":"succeeded","content":{"video_url":"https://media.example/video.mp4"},"usage":{"completion_tokens":12345}}`, "/api/v3/contents/generations/tasks", "image_to_video"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plugins, account, upstream := generationPluginFixture(t, tc.plugin)
			body, action, err := plugins.DecodeVideoProtocol(context.Background(), account, 1, tc.model, json.RawMessage(tc.body), tc.protocol)
			require.NoError(t, err)
			require.Equal(t, tc.action, action)
			task := &GenerationTask{ID: "video_" + strings.Repeat("1", 40), PluginVersionID: 1, Protocol: tc.protocol, Model: tc.model, UpstreamModel: "ep-public-mapping", Action: action, Request: body, CreatedAt: time.Now().UTC()}
			task.Usage, err = plugins.EstimateTask(context.Background(), account, task)
			require.NoError(t, err)
			upstream.response = tc.submit
			require.NoError(t, plugins.SubmitTask(context.Background(), account, task))
			require.Equal(t, "native-upstream", task.UpstreamID)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, tc.path, upstream.requests[0].URL.Path)
			sent, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.Contains(t, string(sent), `"model":"ep-public-mapping"`)
			if tc.protocol == GenerationProtocolArk {
				require.Contains(t, string(sent), `"role":"first_frame"`)
				require.Contains(t, string(sent), `"generate_audio":false`)
			}
			upstream.response = tc.done
			require.NoError(t, plugins.PollTask(context.Background(), account, task))
			require.Equal(t, GenerationSucceeded, task.Status)
			require.Equal(t, GenerationDeliveryReady, task.DeliveryStatus)
			require.Contains(t, string(task.Usage), "_host_final_usage")
			status, view := task.NativeVideoView(false)
			require.Equal(t, http.StatusOK, status)
			encoded, err := json.Marshal(view)
			require.NoError(t, err)
			require.Contains(t, string(encoded), "/v1/videos/"+task.ID+"/content")
			require.NotContains(t, string(encoded), "native-upstream")
			require.NotContains(t, string(encoded), "https://media.example")
		})
	}
}

func TestGenerationNativeVideoRejectsUnsupportedAndDerivedRequests(t *testing.T) {
	for _, tc := range []struct{ plugin, protocol, model, body string }{
		{"sora", GenerationProtocolArk, "sora-2", `{"model":"sora-2","content":[{"type":"text","text":"ocean"}]}`},
		{"xai", GenerationProtocolXAIEdit, "grok-imagine-video-1.5", `{"model":"grok-imagine-video-1.5","prompt":"ocean","video":{"url":"https://media.example/input.mp4"}}`},
		{"doubao", GenerationProtocolArk, "doubao-seedance-2-5-260628", `{"model":"doubao-seedance-2-5-260628","callback_url":"https://callback.example/task","content":[{"type":"text","text":"ocean"}]}`},
		{"doubao", GenerationProtocolArk, "doubao-seedance-2-5-260628", `{"model":"doubao-seedance-2-5-260628","content":[{"type":"draft_task","draft_task":{"id":"another-users-task"}}]}`},
	} {
		t.Run(tc.plugin+tc.protocol+tc.body, func(t *testing.T) {
			plugins, account, upstream := generationPluginFixture(t, tc.plugin)
			_, _, err := plugins.DecodeVideoProtocol(context.Background(), account, 1, tc.model, json.RawMessage(tc.body), tc.protocol)
			require.Error(t, err)
			require.Empty(t, upstream.requests)
		})
	}
}

func TestGenerationNativeVideoIdempotencySeparatesWireOperations(t *testing.T) {
	plugins, account, upstream := generationPluginFixture(t, "xai")
	account.Credentials[NewAPIVideoPricingKey] = map[string]any{"*": map[string]any{"mode": "fixed", "unit": "second", "unit_price": "0.02"}}
	body := json.RawMessage(`{"model":"grok-imagine-video","prompt":"ocean","duration":8}`)
	repo := &generationReplayFixture{}
	svc := &GenerationService{repo: repo, plugins: plugins}
	key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, Group: &Group{ID: 3, RateMultiplier: 1}}
	task, err := svc.CreateVideo(context.Background(), key, nil, account, "grok-imagine-video", body, "same", GenerationProtocolXAI)
	require.NoError(t, err)
	require.Equal(t, GenerationProtocolXAI, task.Protocol)
	require.Equal(t, "0.16", task.Price.Hold.String())
	repo.task = task
	// No live account/plugin/price required for accepted requests.
	svc.plugins = nil
	replayed, err := svc.CreateVideo(context.Background(), key, nil, &Account{}, task.Model, body, "same", GenerationProtocolXAI)
	require.NoError(t, err)
	require.Same(t, task, replayed)
	for _, protocol := range []string{GenerationProtocolOpenAI, GenerationProtocolXAIEdit, GenerationProtocolXAIExtend, GenerationProtocolArk} {
		_, err := svc.ReplayVideo(context.Background(), 1, 2, "same", body, protocol)
		require.ErrorIs(t, err, ErrGenerationIdempotencyConflict)
	}
	require.Empty(t, upstream.requests, "admission never submits to the upstream")
}

func TestGenerationNativeVideoRoutingUsesDeclaredOperation(t *testing.T) {
	plugins, account, _ := generationPluginFixture(t, "xai")
	version, err := plugins.repo.Get(context.Background(), 1)
	require.NoError(t, err)
	account.Platform, account.Status, account.Schedulable = PlatformGrok, StatusActive, true
	account.Credentials[newAPIPluginVideoModelsKey] = pluginVideoModels(version.Meta)
	account.Credentials[newAPIPluginNativeVideosKey] = pluginNativeVideoBinding(version.Meta)
	for _, tc := range []struct {
		protocol, model string
		want            bool
	}{
		{GenerationProtocolXAI, "grok-imagine-video", true},
		{GenerationProtocolXAIEdit, "grok-imagine-video", true},
		{GenerationProtocolXAIEdit, "grok-imagine-video-1.5", false},
		{GenerationProtocolArk, "grok-imagine-video", false},
	} {
		require.Equal(t, tc.want, account.IsSchedulableForModelWithContext(WithNativeVideoPluginRouting(context.Background(), tc.protocol), tc.model))
	}
	require.False(t, account.IsSchedulableForModel("grok-imagine-video"))
}

func TestGenerationNativeVideoRetainsPreviouslyAcceptedCanonicalAlias(t *testing.T) {
	body := json.RawMessage(`{"model":"grok-imagine-video","prompt":"ocean","duration":8}`)
	fingerprint, err := generationRequestFingerprint(body)
	require.NoError(t, err)
	task := &GenerationTask{ID: "video_original", Protocol: GenerationProtocolOpenAI, UserID: 1, APIKeyID: 2, IdempotencyKey: "old-key", Fingerprint: fingerprint}
	svc := &GenerationService{repo: &generationReplayFixture{task: task}}
	replayed, err := svc.ReplayVideo(context.Background(), 1, 2, "old-key", body, GenerationProtocolXAI)
	require.NoError(t, err)
	require.Same(t, task, replayed)
	_, err = svc.ReplayVideo(context.Background(), 1, 2, "old-key", json.RawMessage(`{"model":"grok-imagine-video","prompt":"changed","duration":8}`), GenerationProtocolXAI)
	require.ErrorIs(t, err, ErrGenerationIdempotencyConflict)
	_, err = svc.ReplayVideo(context.Background(), 1, 2, "old-key", body, GenerationProtocolXAIEdit)
	require.ErrorIs(t, err, ErrGenerationIdempotencyConflict)
}

func TestGenerationNativeVideoViewKeepsPrivateSnapshotsPrivate(t *testing.T) {
	task := &GenerationTask{ID: "video_public", Protocol: GenerationProtocolArk, Model: "public-model", Status: GenerationSucceeded, DeliveryStatus: GenerationDeliveryReady,
		Data: json.RawMessage(`{"id":"private-task","model":"ep-private","api_key":"secret","content":{"video_url":"https://storage.example/file?sig=secret"},"usage":{"completion_tokens":0,"total_tokens":123,"private":"secret"}}`)}
	_, view := task.NativeVideoView(false)
	raw, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret")
	require.NotContains(t, string(raw), "private")
	require.Equal(t, map[string]any{"completion_tokens": int64(0), "total_tokens": int64(123)}, view["usage"])
	task.Status = GenerationRunning
	_, view = task.NativeVideoView(false)
	require.NotContains(t, view, "usage", "pending tasks must not publish final usage")
	task.Protocol = GenerationProtocolXAI
	status, _ := task.NativeVideoView(false)
	require.Equal(t, http.StatusAccepted, status)
}

func TestGenerationNativeVideoViewPreservesChargedFailureAndModeration(t *testing.T) {
	task := &GenerationTask{ID: "video_public", Protocol: GenerationProtocolXAI, Status: GenerationSucceeded, DeliveryStatus: GenerationDeliveryPending, Data: json.RawMessage(`{"status":"done","video":{"respect_moderation":false,"duration":8},"usage":{"cost_in_usd_ticks":2000}}`)}
	_, view := task.NativeVideoView(false)
	require.Equal(t, "done", view["status"])
	require.Equal(t, false, view["video"].(map[string]any)["respect_moderation"])
	require.Equal(t, map[string]any{"cost_in_usd_ticks": int64(2000)}, view["usage"])
	task.Data = json.RawMessage(`{"status":"failed","error":{"message":"private vendor text"},"usage":{"cost_in_usd_ticks":2000}}`)
	_, view = task.NativeVideoView(false)
	require.Equal(t, "failed", view["status"])
	require.NotContains(t, view, "video")
	raw, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private vendor text")
	require.Contains(t, view, "usage", "a charged failure must retain its numeric vendor bill")
}

func TestGenerationNativeVideoPublicFieldsRetainNativePromptAndAutoDuration(t *testing.T) {
	fields := generationNativePublicFields(json.RawMessage(`{"model":"public-model","duration":8,"content":[{"type":"text","text":"ocean"},{"type":"image_url","image_url":{"url":"https://media.example/input.png"}}]}`), json.RawMessage(`{"vendorBody":{"duration":8}}`), GenerationProtocolArk)
	require.Equal(t, "ocean", fields.Prompt)
	require.Equal(t, "8", fields.Seconds)
	fields = generationNativePublicFields(json.RawMessage(`{"model":"public-model","duration":-1,"content":[{"type":"text","text":"ocean"}]}`), json.RawMessage(`{"duration":-1}`), GenerationProtocolArk)
	require.Empty(t, fields.Seconds, "automatic duration is not a negative video length")
}
