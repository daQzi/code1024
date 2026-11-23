package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type nativeAliasRoutePluginRepo struct {
	service.NewAPIPluginRepository
	versions []service.NewAPIPluginVersion
}

func (r *nativeAliasRoutePluginRepo) List(context.Context) ([]service.NewAPIPluginVersion, error) {
	return r.versions, nil
}

func (r *nativeAliasRoutePluginRepo) Get(_ context.Context, id int64) (*service.NewAPIPluginVersion, error) {
	for i := range r.versions {
		if r.versions[i].ID == id {
			v := r.versions[i]
			return &v, nil
		}
	}
	return nil, service.ErrGenerationTaskNotFound
}

type nativeAliasRouteAccountRepo struct {
	service.AccountRepository
	accounts []service.Account
}

func (r *nativeAliasRouteAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, platform string) ([]service.Account, error) {
	var out []service.Account
	for _, a := range r.accounts {
		if a.Platform != platform {
			continue
		}
		for _, gid := range a.GroupIDs {
			if gid == groupID {
				out = append(out, a)
				break
			}
		}
	}
	return out, nil
}

type nativeAliasRouteEncryptor struct{}

func (nativeAliasRouteEncryptor) Encrypt(v string) (string, error) { return v, nil }
func (nativeAliasRouteEncryptor) Decrypt(v string) (string, error) { return v, nil }

func TestNativeTaskAliasPathAllowed(t *testing.T) {
	blocked := []string{
		"",
		"/",
		"suno/submit/music",
		"/health",
		"/readyz",
		"/metrics",
		"/setup",
		"/setup/init",
		"/admin/users",
		"/auth/login",
		"/oauth/token",
		"/assets/app.js",
		"/static/logo.png",
		"/debug/pprof",
		"/swagger/index.html",
		"/.well-known/openid-configuration",
		"/backend-api/codex/responses",
		"/antigravity/v1/messages",
		"/api/v1/admin/plugins",
		"/api/v2/something",
		"/v1/tasks",
		"/v1/tasks/video_123",
		"/v1/tasks/suno/native/submit",
		"/suno/../admin/users",
		"/suno/./fetch",
		"/suno//fetch",
		"/suno/fetch%2f123",
		"/suno/fetch\\bad",
		"/suno/fetch\x00bad",
	}
	for _, p := range blocked {
		require.False(t, service.NativeTaskAliasPathAllowed(p), "reserved/unsafe path %q must be blocked", p)
	}

	allowed := []string{
		"/suno/submit/music",
		"/suno/fetch",
		"/suno/fetch/video_123",
		"/api/v3/contents/generations/tasks",
		"/api/common/v3/submit_task_v2",
		"/api/common/v3/get_result_v2",
		"/v1beta/models/veo-3.1-generate-preview:predictLongRunning",
		"/v1beta/models/veo-3.1-generate-preview/operations/video_123",
		"/v1/video_generation",
		"/v1/query/video_generation",
		"/v1/files/retrieve",
		"/tasks",
		"/ent/v2/text2video",
	}
	for _, p := range allowed {
		require.True(t, service.NativeTaskAliasPathAllowed(p), "vendor path %q must be allowed", p)
	}
}

func TestNativeTaskAliasesMiddlewareAuthGuardsAndPredictLongRunningFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sunoSrc, err := builtin.Source("sunoapi")
	require.NoError(t, err)
	sunoLoaded, err := jsplugin.CompilePlugin(sunoSrc, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	googleSrc, err := builtin.Source("google")
	require.NoError(t, err)
	googleLoaded, err := jsplugin.CompilePlugin(googleSrc, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	conflictSrc := `export const meta={apiVersion:1,key:"google-dup",name:"Google dup",version:"1.0.0",author:{name:"Fixture"},models:["veo-3.1-generate-preview"],fetchMode:"per_task",
routes:[{method:"POST",path:"/v1beta/models/:model",type:"submit",decode:"d",render:"r"}]};
export function buildSubmitRequest(){return {url:"/submit",method:"POST"};}
export function parseSubmitResponse(){return {taskId:"x"};}
export function buildQueryRequest(){return {url:"/fetch",method:"GET"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
export const native = { d(){return {kind:"submit",model:"veo-3.1-generate-preview"};}, r(){return {status:200,body:{}};} };`
	conflictLoaded, err := jsplugin.CompilePlugin(conflictSrc, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)

	pluginRepo := &nativeAliasRoutePluginRepo{
		versions: []service.NewAPIPluginVersion{
			{ID: 1, Source: sunoSrc, Meta: sunoLoaded.Meta, Enabled: true, Capabilities: runner.Descriptor{Task: true}},
			{ID: 2, Source: googleSrc, Meta: googleLoaded.Meta, Enabled: true, Capabilities: runner.Descriptor{Task: true}},
			{ID: 3, Source: conflictSrc, Meta: conflictLoaded.Meta, Enabled: true, Capabilities: runner.Descriptor{Task: true}},
		},
	}
	plugins, err := service.NewNewAPIPluginService(pluginRepo, nativeAliasRouteEncryptor{})
	require.NoError(t, err)
	t.Cleanup(plugins.Stop)

	conflictGroupID := int64(55)
	nativeGeminiGroupID := int64(56)
	accountRepo := &nativeAliasRouteAccountRepo{
		accounts: []service.Account{
			{
				ID: 101, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
				GroupIDs: []int64{conflictGroupID},
				Credentials: map[string]any{
					service.NewAPIPluginRevisionKey: 2,
					"newapi_plugin_native_routes": map[string]any{
						"plugin": "google",
						"models": []string{"veo-3.1-generate-preview"},
						"routes": []map[string]any{{"method": "POST", "path": "/v1beta/models/:model", "type": "submit"}},
					},
				},
			},
			{
				ID: 102, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
				GroupIDs: []int64{conflictGroupID},
				Credentials: map[string]any{
					service.NewAPIPluginRevisionKey: 3,
					"newapi_plugin_native_routes": map[string]any{
						"plugin": "google-dup",
						"models": []string{"veo-3.1-generate-preview"},
						"routes": []map[string]any{{"method": "POST", "path": "/v1beta/models/:model", "type": "submit"}},
					},
				},
			},
		},
	}

	cfg := &config.Config{
		RunMode: config.RunModeSimple,
		Gateway: config.GatewayConfig{
			MaxBodySize:     1 << 20,
			TextMaxBodySize: 1 << 20,
		},
	}
	gateway := service.NewOpenAIGatewayService(accountRepo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	generations := service.NewGenerationService(nil, plugins, accountRepo, gateway, nil, nil)
	t.Cleanup(generations.Stop)

	openAIHandler := handler.ProvideOpenAIGatewayHandler(gateway, generations, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil)
	handlers := &handler.Handlers{
		Gateway:       &handler.GatewayHandler{},
		OpenAIGateway: openAIHandler,
		AsyncImage:    handler.NewAsyncImageHandler(nil, nil),
	}

	var authCalls int
	apiKeyAuth := servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		authCalls++
		token := c.GetHeader("Authorization")
		switch token {
		case "Bearer conflict-key":
			c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
				ID: 1, UserID: 1, GroupID: &conflictGroupID,
				Group: &service.Group{ID: conflictGroupID, Platform: service.PlatformOpenAI},
			})
			c.Next()
		case "Bearer gemini-key":
			c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
				ID: 2, UserID: 2, GroupID: &nativeGeminiGroupID,
				Group: &service.Group{ID: nativeGeminiGroupID, Platform: service.PlatformGemini},
			})
			c.Next()
		default:
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"type": "authentication_error", "message": "API key is required"}})
		}
	})

	router := gin.New()
	router.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "healthy") })
	router.NoRoute(NativeTaskAliases(handlers, apiKeyAuth, nil, cfg))
	RegisterGatewayRoutes(router, handlers, apiKeyAuth, nil, nil, nil, nil, nil, cfg)

	t.Run("static host routes are never intercepted by alias middleware", func(t *testing.T) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "healthy", w.Body.String())
	})

	t.Run("unauthenticated native alias request is blocked by API key auth", func(t *testing.T) {
		before := authCalls
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/suno/submit/music", strings.NewReader(`{"prompt":"ocean"}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
		require.Equal(t, before+1, authCalls)
	})

	t.Run("percent-encoded path never dispatches to native alias middleware", func(t *testing.T) {
		before := authCalls
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/suno/submit%2fmusic", strings.NewReader(`{}`))
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code)
		require.Equal(t, before, authCalls)
	})

	t.Run("reserved /api/v1 path never dispatches to native alias middleware", func(t *testing.T) {
		before := authCalls
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/nonexistent", strings.NewReader(`{}`))
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code)
		require.Equal(t, before, authCalls)
	})

	t.Run("predictLongRunning conflicts return 409 on ambiguous group, fall through on unbound native Gemini group", func(t *testing.T) {
		// Direct test of TryNativeTaskAliasSubmit on the two groups:
		// 1. Ambiguous group with two plugins claiming /v1beta/models/:model -> 409 ambiguous_task_route.
		w1 := httptest.NewRecorder()
		c1, _ := gin.CreateTestContext(w1)
		c1.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/veo-3.1-generate-preview:predictLongRunning", strings.NewReader(`{"instances":[{"prompt":"sea"}]}`))
		c1.Request.Header.Set("Content-Type", "application/json")
		c1.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
			ID: 1, UserID: 1, GroupID: &conflictGroupID,
			Group: &service.Group{ID: conflictGroupID, Platform: service.PlatformOpenAI},
		})
		require.True(t, openAIHandler.TryNativeTaskAliasSubmit(c1))
		require.Equal(t, http.StatusConflict, w1.Code, w1.Body.String())
		require.Contains(t, w1.Body.String(), "ambiguous_task_route")

		// 2. Native Gemini group without Veo task plugin bound -> returns false so native GeminiV1BetaModels handles it!
		w2 := httptest.NewRecorder()
		c2, _ := gin.CreateTestContext(w2)
		c2.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/veo-3.1-generate-preview:predictLongRunning", strings.NewReader(`{"instances":[{"prompt":"sea"}]}`))
		c2.Request.Header.Set("Content-Type", "application/json")
		c2.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
			ID: 2, UserID: 2, GroupID: &nativeGeminiGroupID,
			Group: &service.Group{ID: nativeGeminiGroupID, Platform: service.PlatformGemini},
		})
		require.False(t, openAIHandler.TryNativeTaskAliasSubmit(c2))
		require.Zero(t, w2.Body.Len())
	})
}
