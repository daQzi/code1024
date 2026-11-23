//go:build unit

package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNewAPIModuleDisabledNativeGatewayPassesThroughAndPluginEndpointsDegrade503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		RunMode:      config.RunModeSimple,
		NewAPIEngine: config.NewAPIEngineConfig{Enabled: false},
	}
	gateway := service.NewOpenAIGatewayService(openAIImagesFailoverAccountRepo{}, nil, nil, nil, nil, nil, &grokMediaSlotBindings{}, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	openAI := NewOpenAIGatewayHandler(gateway, nil, nil, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	// Simulate New API module disabled: generations == nil, plugins == nil.
	openAI.generations = nil
	reqPolicy := NewRequestPluginHandler(nil, nil)
	registry := NewPluginRegistryHandler(nil)
	taskLogs := NewTaskLogHandler(nil)
	adminNewAPI := adminhandler.NewNewAPIPluginHandler(nil, nil)

	group := &service.Group{ID: 88, Platform: service.PlatformOpenAI, RateMultiplier: 1, AllowImageGeneration: true}
	apiKey := &service.APIKey{ID: 88, UserID: 88, User: &service.User{ID: 88}, GroupID: &group.ID, Group: group}
	authSubject := middleware.AuthSubject{UserID: 88, Concurrency: 5}

	t.Run("native gateway middleware and try-hooks pass through with zero hijack", func(t *testing.T) {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware.ContextKeyUser), authSubject)
			c.Next()
		})
		r.Use(openAI.GenerationMultipartIngress, openAI.GenerationRemixIngress, reqPolicy.Ingress)
		r.POST("/v1/videos", func(c *gin.Context) {
			require.False(t, openAI.TryPluginVideo(c, service.PlatformOpenAI))
			c.JSON(http.StatusOK, gin.H{"native_video": true})
		})
		r.POST("/v1/chat/completions", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"native_chat": true})
		})
		r.POST("/v1/images/generations", func(c *gin.Context) {
			require.False(t, openAI.TryPluginTaskImage(c, apiKey, authSubject, "gpt-image-1", "/v1/images/generations", []byte(`{"model":"gpt-image-1"}`), nil))
			c.JSON(http.StatusOK, gin.H{"native_image": true})
		})
		r.POST("/v1beta/models/*modelAction", func(c *gin.Context) {
			require.False(t, openAI.TryNativeTaskAliasSubmit(c))
			require.False(t, openAI.TryProviderGemini(c))
			c.JSON(http.StatusOK, gin.H{"native_gemini": true})
		})

		// Multipart POST /v1/videos passes straight through to native handler without panic.
		mpBody, mpCT := buildR4MultipartRequest(t, map[string]string{"model": "grok-video", "prompt": "wave"}, "input_reference", "ref.png", append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("z"), 32)...))
		reqMP := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(mpBody.Bytes()))
		reqMP.Header.Set("Content-Type", mpCT)
		wMP := httptest.NewRecorder()
		r.ServeHTTP(wMP, reqMP)
		require.Equal(t, http.StatusOK, wMP.Code, wMP.Body.String())
		require.Contains(t, wMP.Body.String(), `"native_video":true`)

		// Native Chat, Images, Gemini :predictLongRunning & :generateContent all pass through untouched.
		for _, tc := range []struct {
			path, body, want string
		}{
			{"/v1/chat/completions", `{"model":"gpt-4o","messages":[]}`, `"native_chat":true`},
			{"/v1/images/generations", `{"model":"gpt-image-1","prompt":"sun"}`, `"native_image":true`},
			{"/v1beta/models/veo-2.0:predictLongRunning", `{"instances":[{"prompt":"sun"}]}`, `"native_gemini":true`},
			{"/v1beta/models/gemini-2.5-pro:generateContent", `{"contents":[]}`, `"native_gemini":true`},
		} {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, tc.path+": "+w.Body.String())
			require.Contains(t, w.Body.String(), tc.want)
		}

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/vendor/custom", nil)
		require.False(t, openAI.MayDispatchNativeTaskAlias(c))
	})

	t.Run("New API gateway, admin, registry, policy, and task-log endpoints degrade cleanly to 503", func(t *testing.T) {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware.ContextKeyUser), authSubject)
			c.Next()
		})
		// Gateway New API endpoints
		r.POST("/v1/tasks/:key", openAI.GenerationTaskSubmitIngress, openAI.GenerationTaskSubmit)
		r.GET("/v1/tasks/:key", openAI.GenerationTaskGet)
		r.DELETE("/v1/videos/:request_id", openAI.GenerationVideoDelete)
		r.POST("/v1/videos/:request_id/remix", openAI.GenerationRemixIngress)
		r.POST("/v1/video-files", openAI.GenerationFileUpload)
		// Admin New API Plugin endpoints
		r.GET("/admin/plugins/js", adminNewAPI.ListNewAPI)
		r.GET("/admin/plugins/js/tasks/review", adminNewAPI.GenerationReview)
		r.PUT("/admin/plugins/js/tasks/:task_id/reconcile", adminNewAPI.ReconcileGeneration)
		r.GET("/admin/plugins/js/:key/versions", adminNewAPI.NewAPIVersions)
		r.POST("/admin/plugins/js", adminNewAPI.InstallNewAPI)
		r.GET("/admin/plugins/js/marketplace/sources", adminNewAPI.MarketplaceSources)
		r.PUT("/admin/plugins/js/marketplace/sources", adminNewAPI.SaveMarketplaceSources)
		r.POST("/admin/plugins/js/:key/restore", adminNewAPI.RestoreFactory)
		r.PUT("/admin/plugins/js/:id/activation", adminNewAPI.ActivateNewAPI)
		r.GET("/admin/plugins/js/versions/:id/source", adminNewAPI.NewAPISource)
		r.GET("/admin/plugins/js/factories/:key/source", adminNewAPI.NewAPIFactorySource)
		r.GET("/admin/plugins/js/provider-tool-prices", adminNewAPI.ProviderToolPrices)
		r.PUT("/admin/plugins/js/provider-tool-prices", adminNewAPI.SaveProviderToolPrices)
		// Request Plugin endpoints
		r.GET("/admin/request-plugins", reqPolicy.List)
		r.POST("/admin/request-plugins", reqPolicy.Save)
		r.GET("/admin/request-plugins/:id/config", reqPolicy.Config)
		r.GET("/admin/request-plugins/runs", reqPolicy.Runs)
		r.POST("/admin/request-plugins/dry-run", reqPolicy.DryRun)
		r.GET("/admin/request-plugins/model-modifiers", reqPolicy.ModelModifiers)
		r.PUT("/admin/request-plugins/model-modifiers", reqPolicy.SaveModelModifiers)
		// Community Registry endpoints
		r.GET("/plugin-registry/mine", registry.Mine)
		r.GET("/plugin-registry/market", registry.Market)
		r.GET("/plugin-registry/:id", registry.Get)
		r.POST("/plugin-registry", registry.Save)
		r.POST("/plugin-registry/:id/transition", registry.Transition)
		r.POST("/plugin-registry/:id/install", registry.Install)
		r.POST("/plugin-registry/dry-run", registry.DryRun)
		// Task Log endpoints
		r.GET("/task-logs", taskLogs.List)
		r.GET("/task-logs/:task_id", taskLogs.Get)
		r.GET("/task-logs/:task_id/content", taskLogs.Content)
		r.POST("/admin/task-logs/:task_id/native-maintain", taskLogs.AdminMaintainNative)

		cases := []struct{ method, path, body string }{
			{http.MethodPost, "/v1/tasks/sora", `{"model":"sora-2"}`},
			{http.MethodGet, "/v1/tasks/video_123", ""},
			{http.MethodDelete, "/v1/videos/video_123", ""},
			{http.MethodPost, "/v1/videos/video_123/remix", `{"prompt":"new"}`},
			{http.MethodPost, "/v1/video-files", "raw"},
			{http.MethodGet, "/admin/plugins/js", ""},
			{http.MethodGet, "/admin/plugins/js/tasks/review", ""},
			{http.MethodPut, "/admin/plugins/js/tasks/video_123/reconcile", `{}`},
			{http.MethodGet, "/admin/plugins/js/sora/versions", ""},
			{http.MethodPost, "/admin/plugins/js", `{}`},
			{http.MethodGet, "/admin/plugins/js/marketplace/sources", ""},
			{http.MethodPut, "/admin/plugins/js/marketplace/sources", `{}`},
			{http.MethodPost, "/admin/plugins/js/sora/restore", `{}`},
			{http.MethodPut, "/admin/plugins/js/1/activation", `{"enabled":true}`},
			{http.MethodGet, "/admin/plugins/js/versions/1/source", ""},
			{http.MethodGet, "/admin/plugins/js/factories/sora/source", ""},
			{http.MethodGet, "/admin/plugins/js/provider-tool-prices", ""},
			{http.MethodPut, "/admin/plugins/js/provider-tool-prices", `{}`},
			{http.MethodGet, "/admin/request-plugins", ""},
			{http.MethodPost, "/admin/request-plugins", `{}`},
			{http.MethodGet, "/admin/request-plugins/1/config", ""},
			{http.MethodGet, "/admin/request-plugins/runs", ""},
			{http.MethodPost, "/admin/request-plugins/dry-run", `{}`},
			{http.MethodGet, "/admin/request-plugins/model-modifiers", ""},
			{http.MethodPut, "/admin/request-plugins/model-modifiers", `{}`},
			{http.MethodGet, "/plugin-registry/mine", ""},
			{http.MethodGet, "/plugin-registry/market", ""},
			{http.MethodGet, "/plugin-registry/1", ""},
			{http.MethodPost, "/plugin-registry", `{}`},
			{http.MethodPost, "/plugin-registry/1/transition", `{}`},
			{http.MethodPost, "/plugin-registry/1/install", `{}`},
			{http.MethodPost, "/plugin-registry/dry-run", `{}`},
			{http.MethodGet, "/task-logs", ""},
			{http.MethodGet, "/task-logs/video_123", ""},
			{http.MethodGet, "/task-logs/video_123/content", ""},
			{http.MethodPost, "/admin/task-logs/video_123/native-maintain", `{}`},
		}
		for _, tc := range cases {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusServiceUnavailable, w.Code, "%s %s returned %d: %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	})
}
