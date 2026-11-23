//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type multiPluginRepo struct {
	service.NewAPIPluginRepository
	service.RequestPluginRepository
	versions  map[int64]*service.NewAPIPluginVersion
	instances []service.RequestPluginInstance
	runs      []service.RequestPluginRun
}

func (r *multiPluginRepo) Get(_ context.Context, id int64) (*service.NewAPIPluginVersion, error) {
	v, ok := r.versions[id]
	if !ok {
		return nil, service.ErrGenerationTaskNotFound
	}
	cp := *v
	return &cp, nil
}

func (r *multiPluginRepo) List(context.Context) ([]service.NewAPIPluginVersion, error) {
	out := make([]service.NewAPIPluginVersion, 0, len(r.versions))
	for _, v := range r.versions {
		out = append(out, *v)
	}
	return out, nil
}

func (r *multiPluginRepo) Install(_ context.Context, v *service.NewAPIPluginVersion, _ *int64) (*service.NewAPIPluginVersion, error) {
	return v, nil
}

func (r *multiPluginRepo) ListRequestPluginInstances(context.Context, bool) ([]service.RequestPluginInstance, error) {
	return r.instances, nil
}

func (r *multiPluginRepo) RecordRequestPluginRun(_ context.Context, item service.RequestPluginRun) error {
	r.runs = append(r.runs, item)
	return nil
}

type nativeAliasTaskStore struct {
	service.GenerationTaskRepository
	tasks      map[string]*service.GenerationTask
	byIdem     map[string]*service.GenerationTask
	candidates []service.GenerationVideoDecodeCandidate
	holds      int
}

func newNativeAliasTaskStore() *nativeAliasTaskStore {
	return &nativeAliasTaskStore{
		tasks:  map[string]*service.GenerationTask{},
		byIdem: map[string]*service.GenerationTask{},
	}
}

func (r *nativeAliasTaskStore) GetOwned(_ context.Context, id string, user, key int64) (*service.GenerationTask, error) {
	t, ok := r.tasks[id]
	if !ok || t.UserID != user || t.APIKeyID != key {
		return nil, service.ErrGenerationTaskNotFound
	}
	cp := *t
	return &cp, nil
}

func (r *nativeAliasTaskStore) GetVideoOrigin(_ context.Context, id string, user, key, group int64) (*service.GenerationTask, error) {
	t, ok := r.tasks[id]
	if !ok || t.UserID != user || t.APIKeyID != key || t.GroupID != group {
		return nil, service.ErrGenerationTaskNotFound
	}
	cp := *t
	return &cp, nil
}

func (r *nativeAliasTaskStore) ListVideoDecodeCandidates(context.Context, int64, int64, int64, string, string) ([]service.GenerationVideoDecodeCandidate, error) {
	return r.candidates, nil
}

func (r *nativeAliasTaskStore) FindIdempotent(_ context.Context, user, key int64, idempotency string) (*service.GenerationTask, error) {
	t, ok := r.byIdem[idempotency]
	if !ok || t.UserID != user || t.APIKeyID != key {
		return nil, service.ErrGenerationTaskNotFound
	}
	cp := *t
	return &cp, nil
}

func (r *nativeAliasTaskStore) Claim(context.Context, string, time.Duration) (*service.GenerationTask, error) {
	return nil, service.ErrGenerationTaskNotFound
}

func (r *nativeAliasTaskStore) CreateHeld(_ context.Context, task *service.GenerationTask, _ *service.UsageBillingCommand) (*service.GenerationTask, error) {
	r.holds++
	task.Status = service.GenerationQueued
	task.CreatedAt = time.Now().UTC()
	cp := *task
	r.tasks[task.ID] = &cp
	if task.IdempotencyKey != "" {
		r.byIdem[task.IdempotencyKey] = &cp
	}
	return &cp, nil
}

func compileTaskVersion(t *testing.T, id int64, source string) *service.NewAPIPluginVersion {
	t.Helper()
	loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	return &service.NewAPIPluginVersion{
		ID:           id,
		Source:       source,
		Meta:         loaded.Meta,
		Enabled:      true,
		Capabilities: runner.Descriptor{Task: true},
	}
}

func TestGenerationNativeAliasConflictDisambiguationAllowlistPolicyAndAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	alphaSrc := `export const meta={apiVersion:1,key:"vendor-alpha",name:"Alpha",version:"1.0.0",author:{name:"Fixture"},models:["alpha-v1"],fetchMode:"per_task",
routes:[
 {method:"POST",path:"/shared/submit",type:"submit",decode:"decodeReq",render:"renderTask",models:["alpha-v1"]},
 {method:"PUT",path:"/alpha/items/:id",type:"submit",decode:"decodeMutate",render:"renderTask"},
 {method:"PATCH",path:"/alpha/items/:id",type:"submit",decode:"decodeMutate",render:"renderTask"},
 {method:"DELETE",path:"/alpha/items/:id",type:"submit",decode:"decodeMutate",render:"renderTask"}
]};
export function buildSubmitRequest(){return {url:"/submit",method:"POST"};}
export function parseSubmitResponse(){return {taskId:"x"};}
export function buildQueryRequest(){return {url:"/fetch",method:"GET"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
export const native = {
 decodeReq(req){const b=req.body||{};return {kind:"submit",model:b.model||"alpha-v1",action:"GENERATE",prompt:b.gpt_description_prompt||b.prompt||"",requestBody:b};},
 decodeMutate(req){const b=req.body||{};return {kind:"submit",model:b.model||"alpha-v1",action:req.method,originTaskIds:[req.params.id],requestBody:b};},
 renderTask(ctx,task){return {plugin:"vendor-alpha",task_id:task.task_id,action:task.action,prompt:task.prompt||""};}
};`

	betaSrc := `export const meta={apiVersion:1,key:"vendor-beta",name:"Beta",version:"1.0.0",author:{name:"Fixture"},models:["beta-v1"],fetchMode:"per_task",
routes:[
 {method:"POST",path:"/shared/submit",type:"submit",decode:"decodeReq",render:"renderTask",models:["beta-v1"]}
]};
export function buildSubmitRequest(){return {url:"/submit",method:"POST"};}
export function parseSubmitResponse(){return {taskId:"x"};}
export function buildQueryRequest(){return {url:"/fetch",method:"GET"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
export const native = {
 decodeReq(req){const b=req.body||{};return {kind:"submit",model:b.model||"beta-v1",action:"GENERATE",requestBody:b};},
 renderTask(ctx,task){return {plugin:"vendor-beta",task_id:task.task_id};}
};`

	reqPolicySrc := `export const meta={apiVersion:1,key:"native-task-policy",name:"Policy",version:"1.0.0",author:{name:"Fixture"}};
export function ingress(ctx,req){
 if(req.body.gpt_description_prompt==="policy-deny"){return {action:"reject",code:"blocked_by_policy",message:"blocked by request plugin"};}
 if(req.body.gpt_description_prompt==="policy-patch"){return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/gpt_description_prompt",value:"patched-prompt"}]}]};}
 return {action:"continue"};
}`

	vAlpha := compileTaskVersion(t, 1, alphaSrc)
	vBeta := compileTaskVersion(t, 2, betaSrc)
	vPolicy := &service.NewAPIPluginVersion{ID: 90, Source: reqPolicySrc, Enabled: true}
	group := &service.Group{ID: 44, Platform: service.PlatformOpenAI, RateMultiplier: 1, AllowImageGeneration: true}

	makeAccount := func(id, versionID int64, model string) service.Account {
		return service.Account{
			ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 2,
			GroupIDs: []int64{group.ID},
			Credentials: map[string]any{
				service.NewAPIPluginRevisionKey: versionID,
				"base_url":                      "https://upstream.example",
				"api_key":                       "secret-fixture-key",
				"model_mapping":                 map[string]any{model: model},
				service.NewAPIVideoPricingKey:   map[string]any{"*": map[string]any{"mode": "fixed", "unit": "request", "unit_price": "0.10"}},
			},
		}
	}

	pluginRepo := &multiPluginRepo{
		versions: map[int64]*service.NewAPIPluginVersion{1: vAlpha, 2: vBeta, 90: vPolicy},
		instances: []service.RequestPluginInstance{{
			ID: 5, Name: "Native Task Ingress Policy", PluginVersionID: 90, Version: vPolicy,
			Formats: []string{"task"}, Enabled: true, Scope: "global", Stages: []string{"ingress"}, ConfigEncrypted: "fixture:{}",
		}},
	}
	accountRepo := openAIImagesFailoverAccountRepo{
		accounts: []service.Account{makeAccount(11, 1, "alpha-v1"), makeAccount(12, 2, "beta-v1")},
	}
	slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
	concurrency := service.NewConcurrencyService(slots)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	upstream := &grokMediaSlotUpstream{call: func(*http.Request, int64) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}}
	gateway := service.NewOpenAIGatewayService(accountRepo, nil, nil, nil, nil, nil, &grokMediaSlotBindings{}, cfg, nil, concurrency, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	plugins, err := service.ProvideNewAPIPluginService(pluginRepo, requestHTTPEncryptor{}, gateway, nil, nil, nil, nil)
	require.NoError(t, err)
	defer plugins.Stop()
	for i := range accountRepo.accounts {
		require.NoError(t, plugins.NormalizeAccountBinding(context.Background(), &accountRepo.accounts[i], map[string]any{}))
	}

	tasks := newNativeAliasTaskStore()
	generations := service.NewGenerationService(tasks, plugins, accountRepo, gateway, nil, nil)
	defer generations.Stop()
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer billing.Stop()

	promptEngine := &handlerPromptEngine{
		mode:     securityaudit.ModeBlocking,
		decision: &securityaudit.PromptDecision{Kind: securityaudit.DecisionAllow, AllowNextStage: true},
	}
	h := NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	h.generations = generations
	h.securityAuditCoordinator = securityaudit.NewCoordinator(nil, promptEngine)

	runReq := func(method, path, body, idem string, grp *service.Group) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		if idem != "" {
			c.Request.Header.Set("Idempotency-Key", idem)
		}
		if grp == nil {
			grp = group
		}
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 20, UserID: 10, User: &service.User{ID: 10}, GroupID: &grp.ID, Group: grp})
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 5})
		h.GenerationTaskNativeAlias(c)
		return w
	}

	t.Run("ambiguous shared root alias without model returns 409", func(t *testing.T) {
		w := runReq(http.MethodPost, "/shared/submit", `{"gpt_description_prompt":"hello"}`, "", nil)
		require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "ambiguous_task_route")
	})

	t.Run("model disambiguates shared root alias cleanly", func(t *testing.T) {
		w := runReq(http.MethodPost, "/shared/submit", `{"model":"beta-v1","prompt":"hello"}`, "", nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Equal(t, "vendor-beta", gjson.GetBytes(w.Body.Bytes(), "plugin").String())
		slots.assertReleased(t)
	})

	t.Run("group model allowlist rejects disallowed model with 404", func(t *testing.T) {
		restricted := *group
		restricted.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"beta-v1"}}
		w := runReq(http.MethodPost, "/shared/submit", `{"model":"alpha-v1","prompt":"hello"}`, "", &restricted)
		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "not available for this group")
	})

	t.Run("request plugin early ingress rejects and audits", func(t *testing.T) {
		beforeRuns := len(pluginRepo.runs)
		w := runReq(http.MethodPost, "/shared/submit", `{"model":"alpha-v1","gpt_description_prompt":"policy-deny"}`, "", nil)
		require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "request_processing_rejected")
		require.Equal(t, beforeRuns+1, len(pluginRepo.runs))
	})

	t.Run("request plugin early ingress patches vendor body before audit and submit", func(t *testing.T) {
		promptEngine.decision = &securityaudit.PromptDecision{Kind: securityaudit.DecisionAllow, AllowNextStage: true}
		w := runReq(http.MethodPost, "/shared/submit", `{"model":"alpha-v1","gpt_description_prompt":"policy-patch"}`, "", nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		_, _, auditReqs := promptEngine.snapshot()
		require.NotEmpty(t, auditReqs)
		last := auditReqs[len(auditReqs)-1]
		require.Contains(t, string(last.Body), "patched-prompt")
		slots.assertReleased(t)
	})

	t.Run("prompt audit inspects vendor prompt fields and blocks forbidden content", func(t *testing.T) {
		promptEngine.decision = &securityaudit.PromptDecision{
			Kind:           securityaudit.DecisionBlock,
			ErrorCode:      securityaudit.ErrorCodeBlocked,
			AllowNextStage: false,
		}
		defer func() {
			promptEngine.decision = &securityaudit.PromptDecision{Kind: securityaudit.DecisionAllow, AllowNextStage: true}
		}()
		for _, payload := range []string{
			`{"model":"alpha-v1","gpt_description_prompt":"forbidden-suno-lyrics"}`,
			`{"model":"alpha-v1","instances":[{"prompt":"forbidden-veo-prompt"}]}`,
			`{"model":"alpha-v1","content":[{"type":"text","text":"forbidden-ark-text"}]}`,
		} {
			w := runReq(http.MethodPost, "/shared/submit", payload, "", nil)
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), securityaudit.ErrorCodeBlocked)
			slots.assertReleased(t)
		}
	})

	t.Run("PUT PATCH DELETE native routes support originTaskIds and differentiate idempotency fingerprints", func(t *testing.T) {
		parentID := "video_" + strings.Repeat("a", 40)
		tasks.tasks[parentID] = &service.GenerationTask{
			ID: parentID, UserID: 10, APIKeyID: 20, GroupID: group.ID, AccountID: 11, PluginVersionID: 1,
			Platform: service.PlatformOpenAI, Model: "alpha-v1", UpstreamModel: "alpha-v1",
			Status: service.GenerationSucceeded, UpstreamID: "private-upstream-parent",
			Data: json.RawMessage(`{}`), Connection: service.GenerationConnection{BaseURL: "https://upstream.example", Kind: "vendor"},
			ConfigEncrypted: accountRepo.accounts[0].GetCredential("newapi_plugin_config_encrypted"),
		}
		tasks.candidates = []service.GenerationVideoDecodeCandidate{{VersionID: 1, Mapping: map[string]any{"alpha-v1": "alpha-v1"}}}

		// PUT succeeds with idempotency key "mut-key".
		wPut := runReq(http.MethodPut, "/alpha/items/"+parentID, `{"model":"alpha-v1"}`, "mut-key", nil)
		require.Equal(t, http.StatusOK, wPut.Code, wPut.Body.String())
		require.Equal(t, "PUT", gjson.GetBytes(wPut.Body.Bytes(), "action").String())

		// PATCH with the SAME idempotency key & body MUST conflict (409) because HTTP method is part of the fingerprint!
		wPatchConflict := runReq(http.MethodPatch, "/alpha/items/"+parentID, `{"model":"alpha-v1"}`, "mut-key", nil)
		require.Equal(t, http.StatusConflict, wPatchConflict.Code, wPatchConflict.Body.String())
		require.Contains(t, wPatchConflict.Body.String(), "idempotency_conflict")

		// PATCH and DELETE with distinct idempotency keys succeed cleanly.
		wPatch := runReq(http.MethodPatch, "/alpha/items/"+parentID, `{"model":"alpha-v1"}`, "mut-patch", nil)
		require.Equal(t, http.StatusOK, wPatch.Code, wPatch.Body.String())
		require.Equal(t, "PATCH", gjson.GetBytes(wPatch.Body.Bytes(), "action").String())

		wDel := runReq(http.MethodDelete, "/alpha/items/"+parentID, `{"model":"alpha-v1"}`, "mut-del", nil)
		require.Equal(t, http.StatusOK, wDel.Code, wDel.Body.String())
		require.Equal(t, "DELETE", gjson.GetBytes(wDel.Body.Bytes(), "action").String())
	})
}

func TestGenerationNativeAliasHistoricalQueriesAfterUnbindAndXAIHandoffBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sunoSrc, err := builtin.Source("sunoapi")
	require.NoError(t, err)
	hailuoSrc, err := builtin.Source("hailuo")
	require.NoError(t, err)
	xaiSrc, err := builtin.Source("xai")
	require.NoError(t, err)

	vSuno := compileTaskVersion(t, 10, sunoSrc)
	vHailuo := compileTaskVersion(t, 11, hailuoSrc)
	vXAI := compileTaskVersion(t, 12, xaiSrc)
	// Disable all plugin versions AND bind ZERO accounts to verify historical tasks stay readable!
	vSuno.Enabled = false
	vHailuo.Enabled = false
	vXAI.Enabled = false

	pluginRepo := &multiPluginRepo{
		versions: map[int64]*service.NewAPIPluginVersion{10: vSuno, 11: vHailuo, 12: vXAI},
	}
	group := &service.Group{ID: 61, Platform: service.PlatformOpenAI, RateMultiplier: 1}
	emptyAccounts := openAIImagesFailoverAccountRepo{accounts: nil}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gateway := service.NewOpenAIGatewayService(emptyAccounts, nil, nil, nil, nil, nil, &grokMediaSlotBindings{}, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	plugins, err := service.ProvideNewAPIPluginService(pluginRepo, requestHTTPEncryptor{}, gateway, nil, nil, nil, nil)
	require.NoError(t, err)
	defer plugins.Stop()

	sunoID := "video_" + strings.Repeat("b", 40)
	hailuoID := "video_" + strings.Repeat("c", 40)
	xaiNativeID := "video_" + strings.Repeat("d", 40)
	xaiCanonicalID := "video_" + strings.Repeat("e", 40)

	tasks := newNativeAliasTaskStore()
	tasks.tasks[sunoID] = &service.GenerationTask{
		ID: sunoID, UserID: 10, APIKeyID: 20, GroupID: group.ID, PluginVersionID: 10,
		Protocol: service.GenerationProtocolNativePrefix + "sunoapi", Action: "MUSIC",
		Status: service.GenerationSucceeded, Model: "chirp-v4", UpstreamID: "private-suno-id",
		Data: json.RawMessage(`[{"id":"clip-1","title":"Stars"}]`), CreatedAt: time.Now().UTC(),
	}
	tasks.tasks[hailuoID] = &service.GenerationTask{
		ID: hailuoID, UserID: 10, APIKeyID: 20, GroupID: group.ID, PluginVersionID: 11,
		Protocol: service.GenerationProtocolNativePrefix + "hailuo", Action: "text_to_video",
		Status: service.GenerationRunning, Model: "MiniMax-Hailuo-2.3", UpstreamID: "private-hailuo-id",
		Data: json.RawMessage(`{}`), CreatedAt: time.Now().UTC(),
	}
	tasks.tasks[xaiNativeID] = &service.GenerationTask{
		ID: xaiNativeID, UserID: 10, APIKeyID: 20, GroupID: group.ID, PluginVersionID: 12,
		Protocol: service.GenerationProtocolNativePrefix + "xai", Action: "text_to_video",
		Status: service.GenerationRunning, Model: "grok-imagine-video", UpstreamID: "private-xai-1",
		Data: json.RawMessage(`{}`), CreatedAt: time.Now().UTC(),
	}
	tasks.tasks[xaiCanonicalID] = &service.GenerationTask{
		ID: xaiCanonicalID, UserID: 10, APIKeyID: 20, GroupID: group.ID, PluginVersionID: 12,
		Protocol: service.GenerationProtocolOpenAI, Action: "text_to_video",
		Status: service.GenerationRunning, Model: "grok-imagine-video", UpstreamID: "private-xai-2",
		Data: json.RawMessage(`{}`), CreatedAt: time.Now().UTC(),
	}

	generations := service.NewGenerationService(tasks, plugins, emptyAccounts, gateway, nil, nil)
	defer generations.Stop()
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer billing.Stop()

	h := NewOpenAIGatewayHandler(gateway, nil, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	h.generations = generations
	apiKey := &service.APIKey{ID: 20, UserID: 10, User: &service.User{ID: 10}, GroupID: &group.ID, Group: group}

	t.Run("historical path-param GET stays readable after unbind and disable", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/suno/fetch/"+sunoID, nil)
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		h.GenerationTaskNativeAlias(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), sunoID)
		require.NotContains(t, w.Body.String(), "private-suno-id")
	})

	t.Run("historical query-param GET (?task_id=) stays readable after unbind and disable", func(t *testing.T) {
		require.True(t, generations.MayDispatchNativeTaskAlias(context.Background(), http.MethodGet, "/v1/query/video_generation", map[string][]string{"task_id": {hailuoID}}))
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/query/video_generation?task_id="+hailuoID, nil)
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		h.GenerationTaskNativeAlias(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), hailuoID)
		require.NotContains(t, w.Body.String(), "private-hailuo-id")
	})

	t.Run("historical dynamic POST batch query (/suno/fetch) stays readable after unbind and disable", func(t *testing.T) {
		require.True(t, generations.MayDispatchNativeTaskAlias(context.Background(), http.MethodPost, "/suno/fetch", nil))
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/suno/fetch", strings.NewReader(`{"ids":["`+sunoID+`"]}`))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		h.GenerationTaskNativeAlias(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), sunoID)
		require.NotContains(t, w.Body.String(), "private-suno-id")
	})

	t.Run("TryNativeTaskQueryAlias hands off native xAI task but preserves canonical OpenAI Videos task", func(t *testing.T) {
		// Native xAI task -> handled in native wire format.
		wNative := httptest.NewRecorder()
		cNative, _ := gin.CreateTestContext(wNative)
		cNative.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/"+xaiNativeID, nil)
		cNative.Set(string(middleware.ContextKeyAPIKey), apiKey)
		require.True(t, h.TryNativeTaskQueryAlias(cNative))
		require.Equal(t, http.StatusAccepted, wNative.Code, wNative.Body.String())
		require.Contains(t, wNative.Body.String(), `"status":"pending"`)
		require.NotContains(t, wNative.Body.String(), "private-xai-1")

		// Canonical POST /v1/videos task -> NOT hijacked by native alias handoff!
		wCanonical := httptest.NewRecorder()
		cCanonical, _ := gin.CreateTestContext(wCanonical)
		cCanonical.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/"+xaiCanonicalID, nil)
		cCanonical.Set(string(middleware.ContextKeyAPIKey), apiKey)
		require.False(t, h.TryNativeTaskQueryAlias(cCanonical))
		require.Zero(t, wCanonical.Body.Len())
	})
}
