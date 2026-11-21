package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/builtin"
	"github.com/Wei-Shaw/sub2api/internal/newapi/jsplugin"
	"github.com/Wei-Shaw/sub2api/internal/newapi/runner"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestGenerationPluginWorkerHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != "generation-plugin-test-worker" {
		return
	}
	if runner.Serve(context.Background(), os.Stdin, os.Stdout) != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

type generationPluginFixtureRepo struct {
	NewAPIPluginRepository
	version *NewAPIPluginVersion
}

func (r *generationPluginFixtureRepo) Get(context.Context, int64) (*NewAPIPluginVersion, error) {
	return r.version, nil
}

type generationFixtureHTTP struct {
	HTTPUpstream
	response    string
	err         error
	requests    []*http.Request
	contentType string
	status      int
}

func (h *generationFixtureHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	h.requests = append(h.requests, req)
	if h.err != nil {
		return nil, h.err
	}
	status, kind := h.status, h.contentType
	if status == 0 {
		status = 200
	}
	if kind == "" {
		kind = "application/json"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{kind}}, Body: io.NopCloser(strings.NewReader(h.response))}, nil
}
func (h *generationFixtureHTTP) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return h.Do(req, proxy, id, concurrency)
}

func generationPluginFixture(t *testing.T, key string) (*NewAPIPluginService, *Account, *generationFixtureHTTP) {
	t.Helper()
	source, err := builtin.Source(key)
	require.NoError(t, err)
	loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	executable, err := os.Executable()
	require.NoError(t, err)
	pool := runner.NewPool(executable, []string{"-test.run=^TestGenerationPluginWorkerHelper$", "--", "generation-plugin-test-worker"})
	t.Cleanup(pool.Close)
	httpClient := &generationFixtureHTTP{}
	version := &NewAPIPluginVersion{ID: 1, Enabled: true, Source: source, Meta: loaded.Meta, Capabilities: runner.Descriptor{Task: true}}
	s := &NewAPIPluginService{repo: &generationPluginFixtureRepo{version: version}, pool: pool, gateway: &OpenAIGatewayService{httpUpstream: httpClient}}
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.example.com", "api_key": "fixture-key", NewAPIPluginRevisionKey: 1}}
	return s, account, httpClient
}

func TestGenerationPluginXAICompleteAndContent(t *testing.T) {
	s, account, httpClient := generationPluginFixture(t, "xai")
	ctx := context.Background()
	decoded, action, err := s.DecodeVideo(ctx, account, 1, "grok-imagine-video", json.RawMessage(`{"model":"grok-imagine-video","prompt":"A quiet ocean","seconds":"8","size":"1280x720"}`))
	require.NoError(t, err)
	task := &GenerationTask{ID: "video_" + strings.Repeat("1", 40), PluginVersionID: 1, Model: "grok-imagine-video", UpstreamModel: "grok-imagine-video", Action: action, Request: decoded, CreatedAt: time.Now().UTC(), State: json.RawMessage(`{}`), Data: json.RawMessage(`{}`)}
	task.Usage, err = s.EstimateTask(ctx, account, task)
	require.NoError(t, err)
	seconds, ok := generationQuantity(task.Usage, "second")
	require.True(t, ok)
	require.Equal(t, "8", seconds.String())
	httpClient.response = `{"request_id":"fixture-upstream-id"}`
	require.NoError(t, s.SubmitTask(ctx, account, task))
	require.Equal(t, "fixture-upstream-id", task.UpstreamID)
	httpClient.response = `{"status":"done","video":{"url":"https://vidgen.x.ai/fixture.mp4","duration":8,"respect_moderation":true}}`
	require.NoError(t, s.PollTask(ctx, account, task))
	require.Equal(t, GenerationSucceeded, task.Status)
	require.Equal(t, GenerationDeliveryReady, task.DeliveryStatus)
	view, err := s.RenderVideo(ctx, account, task)
	require.NoError(t, err)
	require.Equal(t, task.ID, view["id"])
	require.Equal(t, "completed", view["status"])
	resp, err := s.OpenTaskContent(ctx, account, task, "GET", "video", http.Header{"Range": []string{"bytes=0-99"}, "Authorization": []string{"Bearer downstream-secret"}})
	require.NoError(t, err)
	resp.Body.Close()
	request := httpClient.requests[len(httpClient.requests)-1]
	require.Equal(t, "bytes=0-99", request.Header.Get("Range"))
	require.Empty(t, request.Header.Get("Authorization"))
}

func TestGenerationPluginDoubaoFinalTokenFacts(t *testing.T) {
	s, account, httpClient := generationPluginFixture(t, "doubao")
	ctx := context.Background()
	model := "doubao-seedance-2-5-260628"
	decoded, action, err := s.DecodeVideo(ctx, account, 1, model, json.RawMessage(`{"model":"doubao-seedance-2-5-260628","prompt":"Ocean","seconds":-1,"metadata":{"resolution":"720p"}}`))
	require.NoError(t, err)
	task := &GenerationTask{ID: "video_" + strings.Repeat("2", 40), PluginVersionID: 1, Model: model, UpstreamModel: model, Action: action, Request: decoded, State: json.RawMessage(`{}`), Data: json.RawMessage(`{}`)}
	task.Usage, err = s.EstimateTask(ctx, account, task)
	require.NoError(t, err)
	estimate, ok := generationQuantity(task.Usage, "token")
	require.True(t, ok)
	require.True(t, estimate.IsPositive())
	httpClient.response = `{"id":"fixture-ark-id"}`
	require.NoError(t, s.SubmitTask(ctx, account, task))
	httpClient.response = `{"status":"succeeded","content":{"video_url":"https://cdn.example.com/fixture.mp4"},"usage":{"completion_tokens":12345}}`
	require.NoError(t, s.PollTask(ctx, account, task))
	actual, ok := generationQuantity(task.Usage, "token")
	require.True(t, ok)
	require.Equal(t, "12345", actual.String())
	require.Equal(t, GenerationDeliveryReady, task.DeliveryStatus)
	resp, err := s.OpenTaskContent(ctx, account, task, "HEAD", "video", nil)
	require.NoError(t, err)
	resp.Body.Close()
}

func TestGenerationPluginSoraContentUsesPinnedUpstreamIdentity(t *testing.T) {
	s, account, upstream := generationPluginFixture(t, "sora")
	ctx := context.Background()
	decoded, action, err := s.DecodeVideo(ctx, account, 1, "sora-2", json.RawMessage(`{"model":"sora-2","prompt":"Ocean","seconds":"8","size":"1280x720"}`))
	require.NoError(t, err)
	task := &GenerationTask{ID: "video_" + strings.Repeat("3", 40), PluginVersionID: 1, Model: "sora-2", UpstreamModel: "sora-2", Action: action, Request: decoded}
	upstream.response = `{"id":"sora-upstream-identity"}`
	require.NoError(t, s.SubmitTask(ctx, account, task))
	upstream.response = `{"id":"sora-upstream-identity","status":"completed","seconds":"8","size":"1280x720"}`
	require.NoError(t, s.PollTask(ctx, account, task))
	require.Equal(t, GenerationDeliveryReady, task.DeliveryStatus)
	response, err := s.OpenTaskContent(ctx, account, task, http.MethodHead, "video", nil)
	require.NoError(t, err)
	response.Body.Close()
	request := upstream.requests[len(upstream.requests)-1]
	require.Equal(t, "/v1/videos/sora-upstream-identity/content", request.URL.Path)
	require.Equal(t, "Bearer fixture-key", request.Header.Get("Authorization"))
}

func TestGenerationPluginArkEndpointMappingRetainsDeclaredPublicModel(t *testing.T) {
	s, account, upstream := generationPluginFixture(t, "doubao")
	model := "doubao-seedance-2-5-260628"
	account.Credentials[newAPIPluginVideoModelsKey] = []string{model}
	account.Credentials["model_mapping"] = map[string]any{model: "ep-fixture-ark-endpoint"}
	require.True(t, account.SupportsNewAPIVideoModel(model))
	require.Equal(t, []string{model}, account.NewAPIVideoModels())
	decoded, action, err := s.DecodeVideo(context.Background(), account, 1, model, json.RawMessage(`{"model":"doubao-seedance-2-5-260628","prompt":"Ocean","seconds":"8","size":"1280x720"}`))
	require.NoError(t, err)
	task := &GenerationTask{PluginVersionID: 1, Model: model, UpstreamModel: account.GetMappedModel(model), Action: action, Request: decoded}
	upstream.response = `{"id":"ark-endpoint-task"}`
	require.NoError(t, s.SubmitTask(context.Background(), account, task))
	request := upstream.requests[len(upstream.requests)-1]
	body, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	var sent map[string]any
	require.NoError(t, json.Unmarshal(body, &sent))
	require.Equal(t, "ep-fixture-ark-endpoint", sent["model"])
}

func TestGenerationPluginSubmitTransportIsUncertain(t *testing.T) {
	s, account, httpClient := generationPluginFixture(t, "xai")
	httpClient.err = errors.New("fixture disconnected after write")
	task := &GenerationTask{PluginVersionID: 1, Model: "grok-imagine-video", UpstreamModel: "grok-imagine-video", Action: "text_to_video", Request: json.RawMessage(`{"prompt":"ocean","duration":8}`)}
	err := s.SubmitTask(context.Background(), account, task)
	var submit *GenerationSubmissionError
	require.ErrorAs(t, err, &submit)
	require.True(t, submit.Uncertain)
	httpClient.requests = nil
	task.Request = json.RawMessage(`{}`)
	err = s.SubmitTask(context.Background(), account, task)
	require.ErrorAs(t, err, &submit)
	require.False(t, submit.Uncertain)
	require.Empty(t, httpClient.requests)
}

func TestGenerationPluginAccountsCannotUseNativeScheduler(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{NewAPIPluginRevisionKey: 1, newAPIPluginVideoModelsKey: []string{"video"}}}
	require.False(t, account.IsSchedulableForModel("video"))
	require.True(t, account.IsSchedulableForModelWithContext(WithNewAPIPluginRouting(context.Background()), "video"))
	require.False(t, account.IsSchedulableForModelWithContext(WithNewAPIPluginRouting(context.Background()), "gpt-not-a-video-model"))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	require.False(t, account.SupportsOpenAIEndpointCapability(""))
	require.True(t, account.SupportsOpenAIEndpointCapability("plugin_task"))
}

func TestGenerationPluginSigningAndConnectionSnapshot(t *testing.T) {
	s, account, upstream := generationPluginFixture(t, "xai")
	repo := s.repo.(*generationPluginFixtureRepo)
	repo.version.Source += `
export function exchangeKey(ctx) { return {token: {value: "exchanged-fixture"}}; }
export function signRequest(ctx, req) {
  if (!ctx.token || ctx.token.value !== "exchanged-fixture") throw new Error("exchange was not applied");
  return {headers: {"X-Body-Digest": req.bodySha256}, query: {signature: "fixture-signature"}};
}`
	// A later account edit cannot move an accepted task to another upstream.
	account.Credentials["base_url"] = "https://changed.example.com"
	task := &GenerationTask{PluginVersionID: 1, Model: "grok-imagine-video", UpstreamModel: "grok-imagine-video", Action: "text_to_video", Request: json.RawMessage(`{"prompt":"ocean","duration":8}`), Connection: GenerationConnection{BaseURL: "https://pinned.example.com", Kind: "vendor"}}
	upstream.response = `{"request_id":"signed-upstream-id"}`
	require.NoError(t, s.SubmitTask(context.Background(), account, task))
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "pinned.example.com", upstream.requests[0].URL.Host)
	require.Equal(t, "fixture-signature", upstream.requests[0].URL.Query().Get("signature"))
	require.Len(t, upstream.requests[0].Header.Get("X-Body-Digest"), 64)
}

func TestGenerationPluginSigningFailureIsNotAnAttempt(t *testing.T) {
	s, account, upstream := generationPluginFixture(t, "xai")
	s.repo.(*generationPluginFixtureRepo).version.Source += `
export function signRequest() { throw new Error("fixture signing failed"); }
`
	task := &GenerationTask{PluginVersionID: 1, Model: "grok-imagine-video", UpstreamModel: "grok-imagine-video", Action: "text_to_video", Request: json.RawMessage(`{"prompt":"ocean","duration":8}`)}
	err := s.SubmitTask(context.Background(), account, task)
	var submission *GenerationSubmissionError
	require.ErrorAs(t, err, &submission)
	require.False(t, submission.Uncertain)
	require.Empty(t, upstream.requests)
}

func TestGenerationPluginSSESubmitAndTruncation(t *testing.T) {
	s, account, upstream := generationPluginFixture(t, "xai")
	version := s.repo.(*generationPluginFixtureRepo).version
	version.Source = strings.Replace(version.Source, "export function buildSubmitRequest(", "function buildJSONSubmitRequest(", 1) + `
meta.submitResponseTypes = ["json", "sse"];
export function buildSubmitRequest(ctx) { const req = buildJSONSubmitRequest(ctx); req.responseType = "sse"; return req; }
export function parseSubmitEvent(ctx, event, state) {
  return {state: {request_id: "stream-fixture"}, done: event.data === "done"};
}`
	upstream.contentType = "text/event-stream"
	upstream.response = "data: done\n\n"
	task := &GenerationTask{PluginVersionID: 1, Model: "grok-imagine-video", UpstreamModel: "grok-imagine-video", Action: "text_to_video", Request: json.RawMessage(`{"prompt":"ocean","duration":8}`)}
	require.NoError(t, s.SubmitTask(context.Background(), account, task))
	require.Equal(t, "stream-fixture", task.UpstreamID)
	upstream.response = "data: partial\n\n"
	task.UpstreamID = ""
	err := s.SubmitTask(context.Background(), account, task)
	var submission *GenerationSubmissionError
	require.ErrorAs(t, err, &submission)
	require.True(t, submission.Uncertain, "a truncated accepted stream must never be resubmitted automatically")
	require.Empty(t, task.UpstreamID)
}

func TestGenerationPluginHTTPRejectionDoesNotNeedJSON(t *testing.T) {
	s, account, upstream := generationPluginFixture(t, "xai")
	upstream.status, upstream.contentType, upstream.response = 401, "text/plain", "Unauthorized"
	task := &GenerationTask{PluginVersionID: 1, Model: "grok-imagine-video", UpstreamModel: "grok-imagine-video", Action: "text_to_video", Request: json.RawMessage(`{"prompt":"ocean","duration":8}`)}
	var submission *GenerationSubmissionError
	require.ErrorAs(t, s.SubmitTask(context.Background(), account, task), &submission)
	require.False(t, submission.Uncertain)
	upstream.status = 409
	require.ErrorAs(t, s.SubmitTask(context.Background(), account, task), &submission)
	require.True(t, submission.Uncertain, "a duplicate submission response can name existing billable work")
}

func TestGenerationPluginModelProjection(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Credentials: map[string]any{NewAPIPluginRevisionKey: 1, newAPIPluginVideoModelsKey: []string{"vendor-video", "other-video"}, "model_mapping": map[string]any{"my-video": "vendor-video"}}}
	require.Equal(t, []string{"my-video"}, account.NewAPIVideoModels())
	require.False(t, account.SupportsNewAPIVideoModel("grok-4"))
	require.False(t, account.SupportsNewAPIVideoModel("vendor-video"))
	account.Credentials["model_mapping"] = map[string]any{"*": "vendor-video"}
	require.Equal(t, []string{"other-video", "vendor-video"}, account.NewAPIVideoModels())
	require.True(t, account.SupportsNewAPIVideoModel("custom-alias"))
	// The plugin catalog contributes only declared models, without supplementing
	// native Grok/OpenAI text defaults from an empty platform mapping.
	account.Platform = PlatformOpenAI
	account.Credentials["model_mapping"] = map[string]any{}
	require.Equal(t, []string{"other-video", "vendor-video"}, account.NewAPIVideoModels())
	require.Equal(t, []string{"vendor-video"}, supplementUnmappedOpenAIModels([]Account{*account}, []string{"vendor-video"}))
}
