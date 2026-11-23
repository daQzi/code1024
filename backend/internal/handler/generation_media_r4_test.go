//go:build unit

package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type r4MediaTaskFileStore struct {
	*nativeAliasTaskStore
	mu          sync.Mutex
	files       map[string]r4StoredFile
	storedCount int
	deletedIDs  []string
	failCreate  error
}

type r4StoredFile struct {
	file   service.GenerationFile
	userID int64
	keyID  int64
	group  int64
	bound  bool
}

func newR4MediaTaskFileStore() *r4MediaTaskFileStore {
	return &r4MediaTaskFileStore{
		nativeAliasTaskStore: newNativeAliasTaskStore(),
		files:                map[string]r4StoredFile{},
	}
}

func (s *r4MediaTaskFileStore) StoreGenerationFile(_ context.Context, user, key, group int64, filename, kind string, source io.ReadSeeker) (*service.GenerationFile, error) {
	data, err := io.ReadAll(source)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	id, err := service.NewGenerationFileID()
	if err != nil {
		return nil, err
	}
	f := service.GenerationFile{
		ID:       id,
		Object:   "video.file",
		Filename: filename,
		MimeType: kind,
		Size:     int64(len(data)),
		SHA256:   hex.EncodeToString(sum[:]),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storedCount++
	s.files[id] = r4StoredFile{file: f, userID: user, keyID: key, group: group}
	cp := f
	return &cp, nil
}

func (s *r4MediaTaskFileStore) GetGenerationFile(_ context.Context, id string, user, key, group int64) (*service.GenerationFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.files[id]
	if !ok || entry.userID != user || entry.keyID != key || entry.group != group {
		return nil, service.ErrGenerationFileNotFound
	}
	cp := entry.file
	return &cp, nil
}

func (s *r4MediaTaskFileStore) OpenGenerationFile(context.Context, string, int64, int64, int64) (io.ReadCloser, error) {
	return nil, service.ErrGenerationFileNotFound
}

func (s *r4MediaTaskFileStore) DeleteGenerationFile(_ context.Context, id string, user, key, group int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.files[id]
	if !ok || entry.userID != user || entry.keyID != key || entry.group != group {
		return service.ErrGenerationFileNotFound
	}
	if entry.bound {
		return service.ErrGenerationFileInUse
	}
	delete(s.files, id)
	s.deletedIDs = append(s.deletedIDs, id)
	return nil
}

func (s *r4MediaTaskFileStore) CleanupGenerationFiles(context.Context, int) (int64, error) {
	return 0, nil
}

func (s *r4MediaTaskFileStore) CreateHeld(ctx context.Context, task *service.GenerationTask, cmd *service.UsageBillingCommand) (*service.GenerationTask, error) {
	s.mu.Lock()
	if s.failCreate != nil {
		err := s.failCreate
		s.mu.Unlock()
		return nil, err
	}
	for _, f := range task.Files {
		if entry, ok := s.files[f.ID]; ok {
			entry.bound = true
			s.files[f.ID] = entry
		}
	}
	s.mu.Unlock()
	return s.nativeAliasTaskStore.CreateHeld(ctx, task, cmd)
}

func buildR4MultipartRequest(t *testing.T, fields map[string]string, fileField, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		require.NoError(t, w.WriteField(k, v))
	}
	if fileField != "" {
		part, err := w.CreateFormFile(fileField, filename)
		require.NoError(t, err)
		_, err = part.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return &buf, w.FormDataContentType()
}

func TestGenerationNativeRouteMultipartAndFileReferencesR4(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pngBytes := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("A"), 256)...)

	pluginSrc := `export const meta={apiVersion:1,key:"media-native",name:"Media Native",version:"1.0.0",author:{name:"Fixture"},models:["render-v1"],fetchMode:"per_task",
routes:[
 {method:"POST",path:"/vendor/render",type:"submit",decode:"decodeRender",render:"renderTask",models:["render-v1"]}
]};
export function buildSubmitRequest(){return {url:"/submit",method:"POST"};}
export function parseSubmitResponse(){return {taskId:"up-1"};}
export function buildQueryRequest(){return {url:"/fetch",method:"GET"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
export const native = {
 decodeRender(req){
  const body = req.body || {};
  if (body.kind === "multipart") {
   const files = body.files || [];
   if (files.length !== 1) throw Error("expected exactly 1 file");
   const f = files[0];
   if (!f.ref || !f.ref.startsWith("request_file:")) throw Error("missing opaque file ref");
   if (f.path || f.localPath || f.bytes || f.data || f.content) throw Error("leaked local path or raw bytes");
   const model = (body.fields && body.fields.model && body.fields.model[0]) || "";
   const prompt = (body.fields && body.fields.prompt && body.fields.prompt[0]) || "";
   return {kind:"submit",model,action:"RENDER",requestBody:{model,prompt,fileRef:f.ref,mime:f.mimeType,size:f.size}};
  }
  const v = body.value || {};
  return {kind:"submit",model:v.model||"",action:"RENDER",requestBody:v};
 },
 renderTask(ctx,task){return {task_id:task.task_id,status:task.status,action:task.action};}
};`

	policySrc := `export const meta={apiVersion:1,key:"native-media-policy",name:"Native Media Policy",version:"1.0.0",author:{name:"Fixture"}};
export function ingress(ctx,req){
 const attachments = utils.attachments();
 if (attachments.length !== 1 || attachments[0].mime !== "image/png" || attachments[0].rewritable) {
  throw Error("unexpected attachment projection");
 }
 if (req.body.prompt === "policy-deny") return {action:"reject",code:"blocked",message:"blocked by policy"};
 if (req.body.prompt === "patch-me") {
  return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/prompt",value:"patched-prompt"}]}]};
 }
 return {action:"continue"};
}`

	vTask := compileTaskVersion(t, 1, pluginSrc)
	vPolicy := &service.NewAPIPluginVersion{ID: 91, Source: policySrc, Enabled: true}
	group := &service.Group{ID: 55, Platform: service.PlatformOpenAI, RateMultiplier: 1, AllowImageGeneration: true}

	account := service.Account{
		ID: 31, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 2,
		GroupIDs: []int64{group.ID},
		Credentials: map[string]any{
			service.NewAPIPluginRevisionKey: int64(1),
			"base_url":                      "https://upstream.example",
			"api_key":                       "secret-fixture-key",
			"model_mapping":                 map[string]any{"render-v1": "render-v1"},
			service.NewAPIVideoPricingKey:   map[string]any{"*": map[string]any{"mode": "fixed", "unit": "request", "unit_price": "0.10"}},
		},
	}

	pluginRepo := &multiPluginRepo{
		versions: map[int64]*service.NewAPIPluginVersion{1: vTask, 91: vPolicy},
		instances: []service.RequestPluginInstance{{
			ID: 7, Name: "Native Media Policy", PluginVersionID: 91, Version: vPolicy,
			Formats: []string{"task"}, Enabled: true, Scope: "global", Stages: []string{"ingress"}, ConfigEncrypted: "fixture:{}",
		}},
	}
	accountRepo := openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}
	slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
	concurrency := service.NewConcurrencyService(slots)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	upstreamCalls := 0
	upstream := &grokMediaSlotUpstream{call: func(*http.Request, int64) (*http.Response, error) {
		upstreamCalls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}}
	gateway := service.NewOpenAIGatewayService(accountRepo, nil, nil, nil, nil, nil, &grokMediaSlotBindings{}, cfg, nil, concurrency, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	plugins, err := service.ProvideNewAPIPluginService(pluginRepo, requestHTTPEncryptor{}, gateway, nil, nil, nil, nil)
	require.NoError(t, err)
	defer plugins.Stop()
	require.NoError(t, plugins.NormalizeAccountBinding(context.Background(), &accountRepo.accounts[0], map[string]any{}))

	store := newR4MediaTaskFileStore()
	generations := service.NewGenerationService(store, plugins, accountRepo, gateway, nil, nil)
	defer generations.Stop()
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer billing.Stop()

	h := NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	h.generations = generations

	apiKey := &service.APIKey{ID: 21, UserID: 11, User: &service.User{ID: 11}, GroupID: &group.ID, Group: group}
	preloaded, err := store.StoreGenerationFile(context.Background(), apiKey.UserID, apiKey.ID, group.ID, "ref.png", "image/png", bytes.NewReader(pngBytes))
	require.NoError(t, err)
	foreign, err := store.StoreGenerationFile(context.Background(), 999, 888, group.ID, "secret.png", "image/png", bytes.NewReader(pngBytes))
	require.NoError(t, err)

	newRouter := func() *gin.Engine {
		r := gin.New()
		r.Use(middleware.RequestBodyLimit(129<<20, pkghttputil.NewBodyMemoryBudget(64<<20)))
		r.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 5})
			c.Next()
		})
		r.POST("/v1/tasks/:key/native/*subpath", h.GenerationTaskNativeSubmit)
		r.POST("/vendor/render", h.GenerationTaskNativeAlias)
		return r
	}

	t.Run("prefixed native route multipart uploads opaque file ref and patches prompt", func(t *testing.T) {
		body, ct := buildR4MultipartRequest(t, map[string]string{"model": "render-v1", "prompt": "patch-me"}, "reference_image", "frame.png", pngBytes)
		req := httptest.NewRequest(http.MethodPost, "/v1/tasks/media-native/native/vendor/render", bytes.NewReader(body.Bytes()))
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		newRouter().ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		taskID := gjson.GetBytes(w.Body.Bytes(), "task_id").String()
		require.NotEmpty(t, taskID)
		saved := store.tasks[taskID]
		require.NotNil(t, saved)
		require.Len(t, saved.Files, 1)
		require.Equal(t, "request_file:reference_image", saved.Files[0].Ref)
		require.Equal(t, "image/png", saved.Files[0].MimeType)
		require.Equal(t, "patched-prompt", gjson.GetBytes(saved.Request, "prompt").String())
		require.Equal(t, "request_file:reference_image", gjson.GetBytes(saved.Request, "fileRef").String())
		slots.assertReleased(t)
	})

	t.Run("root alias native route multipart succeeds", func(t *testing.T) {
		body, ct := buildR4MultipartRequest(t, map[string]string{"model": "render-v1", "prompt": "ok"}, "reference_image", "frame.png", pngBytes)
		req := httptest.NewRequest(http.MethodPost, "/vendor/render", bytes.NewReader(body.Bytes()))
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		newRouter().ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		taskID := gjson.GetBytes(w.Body.Bytes(), "task_id").String()
		require.NotEmpty(t, taskID)
		require.Len(t, store.tasks[taskID].Files, 1)
		slots.assertReleased(t)
	})

	t.Run("multipart rejection and orphan cleanup leave zero leaked files", func(t *testing.T) {
		beforeStored := store.storedCount

		// 1. Compressed Content-Encoding rejected 415.
		body, ct := buildR4MultipartRequest(t, map[string]string{"model": "render-v1", "prompt": "ok"}, "reference_image", "frame.png", pngBytes)
		req := httptest.NewRequest(http.MethodPost, "/v1/tasks/media-native/native/vendor/render", bytes.NewReader(body.Bytes()))
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Content-Encoding", "gzip")
		w := httptest.NewRecorder()
		newRouter().ServeHTTP(w, req)
		require.Equal(t, http.StatusUnsupportedMediaType, w.Code, w.Body.String())

		// 2. Oversized declared Content-Length rejected 413.
		req2 := httptest.NewRequest(http.MethodPost, "/v1/tasks/media-native/native/vendor/render", bytes.NewReader(body.Bytes()))
		req2.Header.Set("Content-Type", ct)
		req2.ContentLength = service.GenerationMultipartMaxBytes + 1
		w2 := httptest.NewRecorder()
		newRouter().ServeHTTP(w2, req2)
		require.Equal(t, http.StatusRequestEntityTooLarge, w2.Code, w2.Body.String())

		// 3. Request plugin rejects multipart before storage persistence.
		denyBody, denyCT := buildR4MultipartRequest(t, map[string]string{"model": "render-v1", "prompt": "policy-deny"}, "reference_image", "frame.png", pngBytes)
		req3 := httptest.NewRequest(http.MethodPost, "/v1/tasks/media-native/native/vendor/render", bytes.NewReader(denyBody.Bytes()))
		req3.Header.Set("Content-Type", denyCT)
		w3 := httptest.NewRecorder()
		newRouter().ServeHTTP(w3, req3)
		require.Equal(t, http.StatusForbidden, w3.Code, w3.Body.String())
		require.Equal(t, beforeStored, store.storedCount, "rejected requests must never persist uploaded files")

		// 4. CreateHeld failure cleans up persisted multipart file immediately on defer cleanup().
		store.failCreate = service.ErrGenerationDecode
		defer func() { store.failCreate = nil }()
		beforeDeleted := len(store.deletedIDs)
		req4 := httptest.NewRequest(http.MethodPost, "/v1/tasks/media-native/native/vendor/render", bytes.NewReader(body.Bytes()))
		req4.Header.Set("Content-Type", ct)
		w4 := httptest.NewRecorder()
		newRouter().ServeHTTP(w4, req4)
		require.Equal(t, http.StatusBadRequest, w4.Code, w4.Body.String())
		require.Equal(t, beforeDeleted+1, len(store.deletedIDs), "failed task admission must delete orphan persisted file")
	})

	t.Run("native route JSON file_references enforces ownership fail-closed and projects to policy", func(t *testing.T) {
		beforeRuns := len(pluginRepo.runs)
		foreignPayload := `{"model":"render-v1","prompt":"ok","file_references":[{"file_id":"` + foreign.ID + `","field":"reference_image"}]}`
		reqForeign := httptest.NewRequest(http.MethodPost, "/v1/tasks/media-native/native/vendor/render", strings.NewReader(foreignPayload))
		reqForeign.Header.Set("Content-Type", "application/json")
		wForeign := httptest.NewRecorder()
		newRouter().ServeHTTP(wForeign, reqForeign)
		require.Equal(t, http.StatusNotFound, wForeign.Code, wForeign.Body.String())
		require.Equal(t, beforeRuns, len(pluginRepo.runs), "foreign file_reference must fail before request plugins run")
		require.Zero(t, upstreamCalls)

		ownedPayload := `{"model":"render-v1","prompt":"patch-me","file_references":[{"file_id":"` + preloaded.ID + `","field":"reference_image"}]}`
		reqOwned := httptest.NewRequest(http.MethodPost, "/v1/tasks/media-native/native/vendor/render", strings.NewReader(ownedPayload))
		reqOwned.Header.Set("Content-Type", "application/json")
		wOwned := httptest.NewRecorder()
		newRouter().ServeHTTP(wOwned, reqOwned)
		require.Equal(t, http.StatusOK, wOwned.Code, wOwned.Body.String())
		require.Equal(t, beforeRuns+1, len(pluginRepo.runs))
		taskID := gjson.GetBytes(wOwned.Body.Bytes(), "task_id").String()
		require.NotEmpty(t, taskID)
		saved := store.tasks[taskID]
		require.Len(t, saved.Files, 1)
		require.Equal(t, preloaded.ID, saved.Files[0].ID)
		require.Equal(t, "patched-prompt", gjson.GetBytes(saved.Request, "prompt").String())
		slots.assertReleased(t)
	})
}

func TestGenerationTaskSubmitMultipartAndSunoFailClosedR4(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pngBytes := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("B"), 256)...)

	genericSrc := `export const meta={apiVersion:1,key:"generic-media",name:"Generic Media",version:"1.0.0",author:{name:"Fixture"},models:["gen-media-1"],fetchMode:"per_task",
routes:[{method:"POST",path:"/v1/gen",type:"submit",decode:"decodeGen",render:"renderTask",models:["gen-media-1"]}]};
export function buildSubmitRequest(){return {url:"/submit",method:"POST"};}
export function parseSubmitResponse(){return {taskId:"up-g1"};}
export function buildQueryRequest(){return {url:"/fetch",method:"GET"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
export const native = {
 decodeGen(req){
  const b = req.body || {};
  if (b.kind !== "multipart" || !b.files || b.files.length !== 1) throw Error("expected multipart file");
  const f = b.files[0];
  if (!f.ref.startsWith("request_file:")) throw Error("expected opaque ref");
  return {kind:"submit",model:b.fields.model[0],action:"GENERATE",requestBody:{model:b.fields.model[0],prompt:b.fields.prompt[0],fileRef:f.ref}};
 },
 renderTask(ctx,task){return {task_id:task.task_id};}
};`

	policySrc := `export const meta={apiVersion:1,key:"task-submit-policy",name:"Task Submit Policy",version:"1.0.0",author:{name:"Fixture"}};
export function ingress(ctx,req){
 const a = utils.attachments();
 if (a.length !== 1 || a[0].mime !== "image/png" || a[0].rewritable) throw Error("missing read-only attachment");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/prompt",value:"policy-approved"}]}]};
}`

	vTask := compileTaskVersion(t, 2, genericSrc)
	vPolicy := &service.NewAPIPluginVersion{ID: 92, Source: policySrc, Enabled: true}
	group := &service.Group{ID: 56, Platform: service.PlatformOpenAI, RateMultiplier: 1, AllowImageGeneration: true}
	account := service.Account{
		ID: 32, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 2,
		GroupIDs: []int64{group.ID},
		Credentials: map[string]any{
			service.NewAPIPluginRevisionKey: int64(2),
			"base_url":                      "https://upstream.example",
			"api_key":                       "secret-fixture-key",
			"model_mapping":                 map[string]any{"gen-media-1": "gen-media-1"},
			service.NewAPIVideoPricingKey:   map[string]any{"*": map[string]any{"mode": "fixed", "unit": "request", "unit_price": "0.10"}},
		},
	}

	pluginRepo := &multiPluginRepo{
		versions: map[int64]*service.NewAPIPluginVersion{2: vTask, 92: vPolicy},
		instances: []service.RequestPluginInstance{{
			ID: 8, Name: "Task Submit Policy", PluginVersionID: 92, Version: vPolicy,
			Formats: []string{"task"}, Enabled: true, Scope: "global", Stages: []string{"ingress"}, ConfigEncrypted: "fixture:{}",
		}},
	}
	accountRepo := openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}
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
	require.NoError(t, plugins.NormalizeAccountBinding(context.Background(), &accountRepo.accounts[0], map[string]any{}))

	store := newR4MediaTaskFileStore()
	store.candidates = []service.GenerationVideoDecodeCandidate{{
		VersionID: 2,
		Mapping:   map[string]any{"gen-media-1": "gen-media-1"},
	}}
	generations := service.NewGenerationService(store, plugins, accountRepo, gateway, nil, nil)
	defer generations.Stop()
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer billing.Stop()

	h := NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	h.generations = generations
	policy := NewRequestPluginHandler(plugins, generations)

	apiKey := &service.APIKey{ID: 22, UserID: 12, User: &service.User{ID: 12}, GroupID: &group.ID, Group: group}
	router := gin.New()
	router.Use(middleware.RequestBodyLimit(129<<20, pkghttputil.NewBodyMemoryBudget(64<<20)))
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 5})
		c.Next()
	})
	router.POST(service.GenerationTaskSubmitRoute, h.GenerationTaskSubmitIngress, policy.Ingress, middleware.GroupModelAllowlist(), h.GenerationTaskSubmit)

	t.Run("generic task submit accepts multipart upload, enforces policy, and binds persisted file", func(t *testing.T) {
		body, ct := buildR4MultipartRequest(t, map[string]string{"model": "gen-media-1", "prompt": "orig"}, "reference_image", "ref.png", pngBytes)
		req := httptest.NewRequest(http.MethodPost, "/v1/tasks/generic-media", bytes.NewReader(body.Bytes()))
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		taskID := gjson.GetBytes(w.Body.Bytes(), "id").String()
		require.NotEmpty(t, taskID)
		saved := store.tasks[taskID]
		require.NotNil(t, saved)
		require.Len(t, saved.Files, 1)
		require.Equal(t, "request_file:reference_image", saved.Files[0].Ref)
		require.Equal(t, "policy-approved", gjson.GetBytes(saved.Request, "prompt").String())
		slots.assertReleased(t)
	})

	t.Run("sunoapi rejects multipart upload fail-closed with 415", func(t *testing.T) {
		before := store.storedCount
		body, ct := buildR4MultipartRequest(t, map[string]string{"model": "suno_music", "prompt": "song"}, "reference_image", "ref.png", pngBytes)
		req := httptest.NewRequest(http.MethodPost, "/v1/tasks/sunoapi", bytes.NewReader(body.Bytes()))
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnsupportedMediaType, w.Code, w.Body.String())
		require.Equal(t, before, store.storedCount)
	})

	t.Run("compressed multipart task submit rejected fail-closed with 415", func(t *testing.T) {
		body, ct := buildR4MultipartRequest(t, map[string]string{"model": "gen-media-1", "prompt": "orig"}, "reference_image", "ref.png", pngBytes)
		req := httptest.NewRequest(http.MethodPost, "/v1/tasks/generic-media", bytes.NewReader(body.Bytes()))
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Content-Encoding", "gzip")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnsupportedMediaType, w.Code, w.Body.String())
	})
}

func TestProviderAudioMultipartCompressedEncodingRejectedR4(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
	concurrency := service.NewConcurrencyService(slots)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer billing.Stop()
	gateway := service.NewOpenAIGatewayService(openAIImagesFailoverAccountRepo{}, nil, nil, nil, nil, nil, &grokMediaSlotBindings{}, cfg, nil, concurrency, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)

	group := &service.Group{ID: 57, Platform: service.PlatformOpenAI, RateMultiplier: 1}
	apiKey := &service.APIKey{ID: 23, UserID: 13, User: &service.User{ID: 13}, GroupID: &group.ID, Group: group}

	body, ct := buildR4MultipartRequest(t, map[string]string{"model": "whisper-1"}, "file", "clip.mp3", []byte("ID3audio"))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", ct)
	c.Request.Header.Set("Content-Encoding", "gzip")
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 5})

	h.ProviderAudio(c, "/v1/audio/transcriptions")
	require.Equal(t, http.StatusUnsupportedMediaType, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "unsupported_media_type")
}
