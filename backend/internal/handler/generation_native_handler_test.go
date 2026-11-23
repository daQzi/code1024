package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type generationNativeHTTPRepo struct{ generationGatewayReplayFixture }

func (r *generationNativeHTTPRepo) GetOwned(_ context.Context, id string, user, key int64) (*service.GenerationTask, error) {
	task := r.task
	if task.ID != id || task.UserID != user || task.APIKeyID != key {
		return nil, service.ErrGenerationTaskNotFound
	}
	return task, nil
}

func TestGenerationNativeHTTPReadsNeverUseLegacyGatewayOrLedger(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, protocol, method, path string
		user, key                    int64
		status                       int
		contains                     string
	}{
		{"xai pending", service.GenerationProtocolXAI, http.MethodGet, "/v1/videos/video_public", 1, 2, 202, `"status":"pending"`},
		{"ark pending", service.GenerationProtocolArk, http.MethodGet, "/api/v3/contents/generations/tasks/video_public", 1, 2, 200, `"status":"running"`},
		{"other user", service.GenerationProtocolArk, http.MethodGet, "/api/v3/contents/generations/tasks/video_public", 9, 2, 404, `Video not found`},
		{"other key", service.GenerationProtocolArk, http.MethodGet, "/api/v3/contents/generations/tasks/video_public", 1, 9, 404, `Video not found`},
		{"wrong native protocol", service.GenerationProtocolXAI, http.MethodGet, "/api/v3/contents/generations/tasks/video_public", 1, 2, 404, `Video not found`},
		{"unsupported cancel", service.GenerationProtocolArk, http.MethodDelete, "/api/v3/contents/generations/tasks/video_public", 1, 2, 405, `cancellation is not supported`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &service.GenerationTask{ID: "video_public", UserID: 1, APIKeyID: 2, Protocol: tc.protocol, Status: service.GenerationRunning, Model: "public-model", UpstreamID: "private-upstream"}
			repo := &generationNativeHTTPRepo{generationGatewayReplayFixture{task: task}}
			generations := service.NewGenerationService(repo, nil, nil, nil, nil, nil)
			defer generations.Stop()
			h := &OpenAIGatewayHandler{generations: generations}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: tc.key, UserID: tc.user, Group: &service.Group{Platform: service.PlatformOpenAI}})
			})
			router.GET("/v1/videos/:request_id", h.GenerationVideoGet)
			router.GET("/api/v3/contents/generations/tasks/:task_id", h.SeedanceTasks)
			router.DELETE("/api/v3/contents/generations/tasks/:task_id", h.SeedanceTasks)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), tc.contains)
			require.NotContains(t, w.Body.String(), "private-upstream")
			if tc.status < 300 {
				require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestGenerationNativeHTTPReplayKeepsNativeShapeAndChecksProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"model":"grok-imagine-video","prompt":"ocean","duration":8}`
	// The fixture represents a previously persisted native create snapshot.
	fingerprint := service.HashUsageRequestPayload([]byte(service.GenerationProtocolXAI + "\x00" + service.HashUsageRequestPayload([]byte(`{"duration":8,"model":"grok-imagine-video","prompt":"ocean"}`))))
	task := &service.GenerationTask{ID: "video_public", UserID: 1, APIKeyID: 2, Protocol: service.GenerationProtocolXAI, IdempotencyKey: "same", Fingerprint: fingerprint}
	repo := &generationNativeHTTPRepo{generationGatewayReplayFixture{task: task}}
	gen := service.NewGenerationService(repo, nil, nil, nil, nil, nil)
	defer gen.Stop()
	h := &OpenAIGatewayHandler{generations: gen}
	for _, tc := range []struct {
		protocol string
		status   int
	}{{service.GenerationProtocolXAI, 200}, {service.GenerationProtocolXAIEdit, 409}} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("Idempotency-Key", "same")
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, Group: &service.Group{ID: 3}})
		require.True(t, h.TryPluginNativeVideo(c, service.PlatformGrok, tc.protocol))
		require.Equal(t, tc.status, w.Code)
		if tc.status == 200 {
			require.JSONEq(t, `{"request_id":"video_public"}`, w.Body.String())
		}
	}
}
