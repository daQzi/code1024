//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/require"
)

const credentialTaskSource = `
export const meta={apiVersion:1,key:"credential-task",name:"Credential task",version:"1.0.0",author:{name:"Fixture"},models:["vendor-video"],fetchMode:"per_task",protocols:["openai_video"],
 channelConfig:{type:"object",properties:{label:{type:"string",title:"Label",description:"Fixture label",default:"saved"}}},usageSchema:{seconds:{type:"number",unit:"second"}}};
export const protocols={openai_video:{decodeRequest(ctx){return {kind:"submit",model:ctx.body.value.model,action:"text_to_video",requestBody:ctx.body.value};},render(ctx,task){return {id:task.task_id};}}};
function taskAuth(ctx){if(ctx.apiKey!=="" || !ctx.credential?.token || ctx.authHeader!=="Bearer "+ctx.credential.token)throw Error("invalid private task auth");}
export function extractUsage(ctx){taskAuth(ctx);return {seconds:ctx.responseBody?.seconds ?? Number(ctx.requestBody?.seconds || 4)};}
export function buildSubmitRequest(ctx){taskAuth(ctx);return {url:ctx.baseUrl+"/submit",method:"POST",headers:{"X-Seen-Auth":ctx.authHeader},body:{prompt:ctx.requestBody.prompt}};}
export function parseSubmitResponse(ctx,resp){taskAuth(ctx);return {taskId:resp.body.id};}
export function buildQueryRequest(ctx){taskAuth(ctx);return {url:ctx.baseUrl+"/query/"+ctx.taskId,method:"GET"};}
export function parseTaskResult(ctx,resp){taskAuth(ctx);return {status:"SUCCESS",url:"https://api.example.com/video.mp4"};}
export function listArtifacts(task){return [{key:"video",type:"video",mimeType:"video/mp4"}];}
export function buildContentRequest(ctx){taskAuth(ctx);return {url:ctx.baseUrl+"/content",method:"GET",credentialless:ctx.clientRequest.method==="HEAD"};}
export function signRequest(ctx,req){if(ctx.apiKey!=="" || !ctx.credential?.token)throw Error("wrong signature identity");return {headers:{"X-Signed-Grant":ctx.credential.token}};}
export function exchangeKey(){throw Error("a login must never be exchanged as a static key");}
`

func credentialTaskFixture(t *testing.T) (*NewAPIPluginService, *Account, *credentialFixtureRepo) {
	t.Helper()
	s, account, repo := lifecycleFixture(t, credentialTaskSource+lifecycleHooks)
	delete(account.Credentials, "api_key")
	account.Credentials["model_mapping"] = map[string]any{"public-video": "vendor-video"}
	s.gateway.accountRepo = &stubOpenAIAccountRepo{accounts: []Account{*account}}
	return s, account, repo
}

func credentialFixtureTask(t *testing.T, account *Account) *GenerationTask {
	t.Helper()
	id, err := NewGenerationTaskID()
	require.NoError(t, err)
	return &GenerationTask{ID: id, UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: account.ID, PluginVersionID: 1, Model: "public-video", UpstreamModel: "vendor-video", Platform: account.Platform, Protocol: GenerationProtocolOpenAI, Action: "text_to_video", Connection: generationAccountConnection(account), ConfigEncrypted: account.GetCredential(newAPIPluginConfigEncryptedKey), Request: json.RawMessage(`{"model":"public-video","prompt":"Ocean","seconds":4}`), Usage: json.RawMessage(`{}`)}
}

func TestGenerationCredentialCreateLoginAndTaskOperations(t *testing.T) {
	s, account, repo := credentialTaskFixture(t)
	ctx := context.Background()
	// A Task-only plugin has the same administrator login card and persistent
	// lifecycle as a Provider, despite having no relay route at all.
	require.Empty(t, pluginProviderBinding(repo.versions[1].Meta, repo.versions[1].Capabilities).Routes)
	view, err := s.PluginCredentialStatus(ctx, account.ID, "admin-1", "session-digest")
	require.NoError(t, err)
	require.Equal(t, "login_required", view.Status)
	_, err = s.prepareTaskCredential(ctx, account, credentialFixtureTask(t, account), true)
	require.Error(t, err)
	view = fixtureLogin(t, s, account)
	require.Len(t, view.TaskBinding, 64)
	task := credentialFixtureTask(t, account)
	resolved, err := s.prepareTaskCredential(ctx, account, task, true)
	require.NoError(t, err)
	require.Equal(t, "login", task.CredentialBinding.Mode)
	require.Equal(t, view.TaskBinding, task.CredentialBinding.Identity)
	raw, err := json.Marshal(task.CredentialBinding)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "grant-1")
	require.NotContains(t, string(raw), "refresh-secret")
	var methods []string
	s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		methods = append(methods, req.Method+" "+req.URL.Path)
		if req.URL.Path == "/prefix/revoke" {
			return chainResponse(503, `{}`), nil
		}
		if req.Method != http.MethodHead {
			require.Equal(t, "Bearer grant-1", req.Header.Get("Authorization"))
			require.Equal(t, "grant-1", req.URL.Query().Get("grant"))
			require.Equal(t, "grant-1", req.Header.Get("X-Signed-Grant"))
		} else {
			require.Empty(t, req.Header.Get("Authorization"))
			require.Empty(t, req.Header.Get("X-Signed-Grant"))
			require.Empty(t, req.URL.Query().Get("grant"))
		}
		switch req.URL.Path {
		case "/prefix/submit":
			require.Equal(t, "Bearer grant-1", req.Header.Get("X-Seen-Auth"))
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"prompt":"Ocean"}`, string(body))
			return chainResponse(200, `{"id":"upstream-task"}`), nil
		case "/prefix/query/upstream-task":
			return chainResponse(200, `{"seconds":4}`), nil
		case "/prefix/content":
			return chainResponse(200, `video-bytes`), nil
		}
		t.Fatalf("unexpected task path %s", req.URL.Path)
		return nil, nil
	}}
	facts, err := s.EstimateTask(ctx, resolved, task)
	require.NoError(t, err)
	require.JSONEq(t, `{"seconds":4}`, string(facts))
	require.NoError(t, s.SubmitTask(ctx, account, task))
	require.Equal(t, "upstream-task", task.UpstreamID)
	// Recreating the per-operation session is equivalent to the background
	// worker reloading a task. No token is retained in task state.
	require.NoError(t, s.PollTask(ctx, account, task))
	require.Equal(t, GenerationSucceeded, task.Status)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		resp, err := s.OpenTaskContent(ctx, account, task, method, "video", nil)
		require.NoError(t, err)
		resp.Body.Close()
	}
	require.Len(t, methods, 4)
	view, err = s.ManagePluginCredential(ctx, account.ID, 0, "admin-1", "session-digest", "revoke", PluginLoginInput{})
	require.NoError(t, err)
	require.Equal(t, "revoke_pending", view.Status)
	before := len(methods)
	require.Error(t, s.SubmitTask(ctx, account, task))
	require.Len(t, methods, before, "a pending remote revoke already denies local task use")
}

func TestGenerationCredentialRefreshIdentityAndNoSubmissionReplay(t *testing.T) {
	s, account, repo := credentialTaskFixture(t)
	fixtureLogin(t, s, account)
	ctx := context.Background()
	task := credentialFixtureTask(t, account)
	_, err := s.prepareTaskCredential(ctx, account, task, true)
	require.NoError(t, err)
	want := task.CredentialBinding.Identity
	state, err := repo.ClaimPluginCredential(ctx, repo.state, 0)
	require.NoError(t, err)
	payload, err := s.readCredentialPayload(state)
	require.NoError(t, err)
	payload.Credential.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	require.NoError(t, s.saveCredentialPayload(ctx, state, &payload))
	refreshes, submissions := 0, 0
	s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/prefix/refresh":
			refreshes++
			return chainResponse(200, `{"credential":{"token":"grant-2","refresh":"refresh-secret"},"headers":{"Authorization":"Bearer grant-2"},"query":{"grant":"grant-2"},"accountId":"upstream-identity"}`), nil
		case "/prefix/submit":
			submissions++
			require.Equal(t, "Bearer grant-2", req.Header.Get("Authorization"))
			return chainResponse(401, `{}`), nil
		}
		t.Fatalf("unexpected path %s", req.URL.Path)
		return nil, nil
	}}
	err = s.SubmitTask(ctx, account, task)
	var rejected *GenerationSubmissionError
	require.ErrorAs(t, err, &rejected)
	require.False(t, rejected.Uncertain)
	require.Equal(t, 1, refreshes)
	require.Equal(t, 1, submissions, "even credential rejection never replays a paid submission")
	require.Equal(t, want, task.CredentialBinding.Identity)
	state, err = repo.ReadPluginCredential(ctx, account.ID)
	require.NoError(t, err)
	state, err = repo.ClaimPluginCredential(ctx, state, state.Revision)
	require.NoError(t, err)
	payload, err = s.readCredentialPayload(state)
	require.NoError(t, err)
	payload.TaskBinding = strings.Repeat("f", 64)
	require.NoError(t, s.saveCredentialPayload(ctx, state, &payload))
	_, err = s.prepareTaskCredential(ctx, account, task, false)
	require.ErrorIs(t, err, ErrGenerationCredential)
	require.Equal(t, 1, submissions)
}

func TestGenerationCredentialAnonymousIdentityAndLegacyBoundaries(t *testing.T) {
	payload := &pluginCredentialPayload{}
	grant := jsplugin.CredentialResult{Credential: json.RawMessage(`{"token":"one"}`)}
	require.NoError(t, setPluginCredentialResult(payload, "fixture", grant, false))
	first := payload.TaskBinding
	require.Len(t, first, 64)
	grant.Credential = json.RawMessage(`{"token":"two"}`)
	require.NoError(t, setPluginCredentialResult(payload, "fixture", grant, true))
	require.Equal(t, first, payload.TaskBinding)
	require.NoError(t, setPluginCredentialResult(payload, "fixture", grant, false))
	require.NotEqual(t, first, payload.TaskBinding, "a different anonymous login cannot inherit old tasks")
	require.True(t, SameGenerationCredential(GenerationCredentialBinding{}, GenerationCredentialBinding{Mode: "static"}))
	require.False(t, SameGenerationCredential(GenerationCredentialBinding{}, GenerationCredentialBinding{Mode: "login", Identity: first}))
	s, account, _ := credentialTaskFixture(t)
	fixtureLogin(t, s, account)
	legacy := credentialFixtureTask(t, account)
	_, err := s.prepareTaskCredential(context.Background(), account, legacy, false)
	require.ErrorIs(t, err, ErrGenerationCredential, "an unbound old task cannot adopt a newly installed login")
	_, err = s.prepareTaskCredential(context.Background(), account, legacy, true)
	require.NoError(t, err)
	account.Credentials["base_url"] = "https://replacement.example.invalid"
	_, err = s.prepareTaskCredential(context.Background(), account, legacy, false)
	require.ErrorIs(t, err, ErrGenerationCredential)
}

func TestGenerationCredentialAdmissionBindsBeforeHolding(t *testing.T) {
	plugins, account, _ := credentialTaskFixture(t)
	account.Credentials[NewAPIVideoPricingKey] = map[string]any{"*": map[string]any{"mode": "fixed", "unit": "request", "unit_price": "1"}}
	repo := &generationReplayFixture{}
	svc := &GenerationService{repo: repo, plugins: plugins}
	key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, Group: &Group{ID: 3, RateMultiplier: 1}}
	body := json.RawMessage(`{"model":"public-video","prompt":"Ocean","seconds":4}`)
	_, err := svc.Create(context.Background(), key, nil, account, "public-video", body, "login-task")
	require.Error(t, err)
	require.Nil(t, repo.held, "missing login cannot freeze user funds")
	fixtureLogin(t, plugins, account)
	task, err := svc.Create(context.Background(), key, nil, account, "public-video", body, "login-task")
	require.NoError(t, err)
	require.Same(t, task, repo.held)
	require.Equal(t, "login", task.CredentialBinding.Mode)
	require.Len(t, task.CredentialBinding.Identity, 64)
	require.Equal(t, "1", task.Price.Hold.String())
	require.NoError(t, task.Validate())
}
