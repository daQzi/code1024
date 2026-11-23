package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
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

func TestGenerationEmbeddedContentUsesDescriptorAndExactSignature(t *testing.T) {
	s, account, upstream := generationPluginFixture(t, "sora")
	version := s.repo.(*generationPluginFixtureRepo).version
	version.Source = strings.Replace(version.Source, "export function buildContentRequest(ctx)", "function originalContentRequest(ctx)", 1) + `
export function buildContentRequest(ctx){return {url:ctx.baseUrl+"/operations:fetch",method:"POST",body:{operationName:ctx.upstreamTaskId},responseType:"json",contentPath:["response","videos",0,"bytesBase64Encoded"],mimeType:"video/webm"};}
export function signRequest(ctx,req){return {headers:{"X-Content-Digest":req.bodySha256}};}`
	task := &GenerationTask{ID: "video_" + strings.Repeat("a", 40), PluginVersionID: 1, UpstreamID: "fixed-upstream-operation", Model: "sora-2", UpstreamModel: "sora-2", Action: "text_to_video", State: json.RawMessage(`{}`), Data: json.RawMessage(`{}`)}
	payload := bytes.Repeat([]byte("simulated video bytes"), 100000)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		upstream.response = `{"done":true,"response":{"videos":[{"bytesBase64Encoded":"` + base64.StdEncoding.EncodeToString(payload) + `"}]}}`
		response, err := s.OpenTaskContent(context.Background(), account, task, method, "video", http.Header{"Range": {"bytes=0-99"}, "If-None-Match": {"private-validator"}, "Authorization": {"client-secret"}})
		require.NoError(t, err)
		require.Equal(t, "video/webm", response.Header.Get("Content-Type"))
		require.EqualValues(t, len(payload), response.ContentLength)
		request := upstream.requests[len(upstream.requests)-1]
		require.Equal(t, http.MethodPost, request.Method, "HEAD still fetches the operation using its declared method")
		require.Equal(t, "/operations:fetch", request.URL.Path)
		require.Empty(t, request.Header.Get("Range"))
		require.Empty(t, request.Header.Get("If-None-Match"))
		require.NotEqual(t, "client-secret", request.Header.Get("Authorization"))
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"operationName":"fixed-upstream-operation"}`, string(body))
		digest := sha256.Sum256(body)
		require.Equal(t, hex.EncodeToString(digest[:]), request.Header.Get("X-Content-Digest"))
		got, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		if method == http.MethodHead {
			require.Empty(t, got)
		} else {
			require.Equal(t, payload, got)
			require.Len(t, generationEmbeddedSlots, 1)
		}
		require.NoError(t, response.Body.Close())
		require.NoError(t, response.Body.Close())
		require.Empty(t, generationEmbeddedSlots)
	}
	for _, tc := range []struct {
		body string
		want error
	}{{`{"done":false}`, pluginTask.ErrEmbeddedArtifactNotReady}, {`{"done":true,"response":{}}`, pluginTask.ErrEmbeddedArtifactGone}} {
		upstream.response = tc.body
		_, err := s.OpenTaskContent(context.Background(), account, task, http.MethodGet, "video", nil)
		require.ErrorIs(t, err, tc.want)
		require.Empty(t, generationEmbeddedSlots)
	}
	upstream.response = `{"done":true,"response":{"videos":[{"bytesBase64Encoded":"eA=="}]}}`
	budgetCtx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), pkghttputil.NewBodyMemoryBudget(1024))
	defer release()
	_, err := s.OpenTaskContent(budgetCtx, account, task, http.MethodGet, "video", nil)
	require.ErrorIs(t, err, pkghttputil.ErrRequestBodyMemoryBudget)
	require.Empty(t, generationEmbeddedSlots)
}

type generationArtifactTrackedBody struct {
	io.Reader
	closed bool
}

func (b *generationArtifactTrackedBody) Close() error { b.closed = true; return nil }

func TestGenerationEmbeddedContentBoundsCancellationAndRelease(t *testing.T) {
	descriptor := &pluginTask.Request{ContentPath: []any{"data"}}
	newResponse := func(body string, length int64) (*http.Response, *generationArtifactTrackedBody) {
		tracked := &generationArtifactTrackedBody{Reader: strings.NewReader(body)}
		return &http.Response{StatusCode: 200, Body: tracked, ContentLength: length}, tracked
	}
	for _, tc := range []struct {
		body   string
		length int64
	}{
		{body: `{"done":true,"data":"!!"}`, length: -1},
		{body: `{"done":true,"data":"eA=="}`, length: pluginTask.MaxEmbeddedJSONBytes + 1},
		{body: `{"done":true,"data":"eA=="`, length: -1},
	} {
		upstream, tracked := newResponse(tc.body, tc.length)
		_, err := generationEmbeddedResponse(context.Background(), upstream, descriptor, http.MethodGet)
		require.Error(t, err)
		require.True(t, tracked.closed)
		require.Empty(t, generationEmbeddedSlots)
	}
	for range cap(generationEmbeddedSlots) {
		generationEmbeddedSlots <- struct{}{}
	}
	upstream, tracked := newResponse(`{"done":true,"data":"eA=="}`, -1)
	_, err := generationEmbeddedResponse(context.Background(), upstream, descriptor, http.MethodGet)
	for range cap(generationEmbeddedSlots) {
		<-generationEmbeddedSlots
	}
	require.ErrorIs(t, err, ErrGenerationArtifactBusy)
	require.True(t, tracked.closed)
	budget := pluginTask.MaxEmbeddedJSONBytes*5 + 1<<20
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), pkghttputil.NewBodyMemoryBudget(budget))
	defer release()
	ctx, cancel := context.WithCancel(ctx)
	upstream, tracked = newResponse(`{"done":true,"data":"eA=="}`, -1)
	response, err := generationEmbeddedResponse(ctx, upstream, descriptor, http.MethodGet)
	require.NoError(t, err)
	require.True(t, tracked.closed, "the upstream envelope closes before downstream delivery")
	require.Equal(t, "video/mp4", response.Header.Get("Content-Type"))
	require.Len(t, generationEmbeddedSlots, 1)
	require.ErrorIs(t, pkghttputil.ReserveRequestBodyMemory(ctx, 1), pkghttputil.ErrRequestBodyMemoryBudget)
	cancel()
	_, err = response.Body.Read(make([]byte, 1))
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, response.Body.Close())
	require.NoError(t, response.Body.Close())
	require.Empty(t, generationEmbeddedSlots)
	require.NoError(t, pkghttputil.ReserveRequestBodyMemory(ctx, budget), "Close releases the full nested memory reservation exactly once")
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
	account.Credentials[newAPIPluginVideoModelsKey] = []string{"video-*"}
	require.True(t, account.SupportsOpenAIEndpointCapability("plugin_task"), "wildcard task families remain eligible even without a concrete public catalog entry")
	require.True(t, account.SupportsNewAPIVideoModel("video-1"))
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
