package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGenerationTaskReadHTTPGroupAndKeyBoundaries(t *testing.T) {
	id := "video_" + strings.Repeat("1", 40)
	base := "/v1/tasks/" + id
	for _, tc := range []struct {
		name, path, state, expected string
		user, key, group, version   int64
		code                        int
	}{
		{"status", base, service.GenerationRunning, `"status":"IN_PROGRESS"`, 1, 2, 3, 7, 200},
		{"unknown submission", base, service.GenerationUncertain, `"status":"UNKNOWN"`, 1, 2, 3, 7, 200},
		{"failed", base, service.GenerationFailed, `"fail_reason":"Task failed"`, 1, 2, 3, 7, 200},
		{"pending list", base + "/artifacts", service.GenerationQueued, `"artifacts":[]`, 1, 2, 3, 7, 200},
		{"pending content", base + "/artifacts/video/content", service.GenerationQueued, "artifact_not_ready", 1, 2, 3, 7, 409},
		{"foreign user", base, service.GenerationRunning, "artifact_not_found", 99, 2, 3, 7, 404},
		{"foreign key", base + "/artifacts", service.GenerationRunning, "artifact_not_found", 1, 99, 3, 7, 404},
		{"key moved to another group", base + "/artifacts/video/content", service.GenerationSucceeded, "artifact_not_found", 1, 2, 99, 7, 404},
		{"native history stays on native route", base, service.GenerationRunning, "artifact_not_found", 1, 2, 3, 0, 404},
		{"invalid artifact", base + "/artifacts/..x/content", service.GenerationRunning, "artifact_not_found", 1, 2, 3, 7, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &service.GenerationTask{ID: id, UserID: 1, APIKeyID: 2, GroupID: 3, PluginVersionID: tc.version, Status: tc.state, Platform: service.PlatformOpenAI, Progress: 42, UpstreamID: "private-upstream", ErrorMessage: "private error"}
			repo := &generationNativeHTTPRepo{generationGatewayReplayFixture{task: task}}
			gen := service.NewGenerationService(repo, nil, nil, nil, nil, nil)
			defer gen.Stop()
			h := &OpenAIGatewayHandler{generations: gen}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: tc.key, UserID: tc.user, GroupID: &tc.group})
			})
			router.GET("/v1/tasks/:key", h.GenerationTaskGet)
			router.GET("/v1/tasks/:key/artifacts", h.GenerationTaskArtifacts)
			router.GET("/v1/tasks/:key/artifacts/:artifact_key/content", h.GenerationTaskArtifactContent)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			require.Equal(t, tc.code, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), tc.expected)
			require.NotContains(t, w.Body.String(), "private-upstream")
			require.NotContains(t, w.Body.String(), "private error")
			require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
		})
	}
}

func TestGenerationTaskReadSunoLyricsAndVideoEndpointExclusion(t *testing.T) {
	id := "video_" + strings.Repeat("2", 40)
	group := int64(3)
	task := &service.GenerationTask{
		ID: id, UserID: 1, APIKeyID: 2, GroupID: group, PluginVersionID: 9,
		Protocol: service.GenerationProtocolSunoTask, Action: "LYRICS",
		Status: service.GenerationSucceeded, DeliveryStatus: service.GenerationDeliveryPending,
		Platform: service.PlatformOpenAI, UpstreamID: "private-suno",
		Data: json.RawMessage(`[{"id":"song-1","title":"Night","text":"Stars above","audio_url":"https://private.example/secret.mp3"}]`),
	}
	repo := &generationNativeHTTPRepo{generationGatewayReplayFixture{task: task}}
	gen := service.NewGenerationService(repo, nil, nil, nil, nil, nil)
	defer gen.Stop()
	h := &OpenAIGatewayHandler{generations: gen}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group})
	})
	router.GET("/v1/tasks/:key", h.GenerationTaskGet)
	router.GET("/v1/videos/:task_id", h.GenerationVideoGet)
	router.GET("/v1/videos/:task_id/content", h.GenerationVideoContent)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/tasks/"+id, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"lyrics":[{"title":"Night","text":"Stars above"}]`)
	require.NotContains(t, w.Body.String(), "private-suno")
	require.NotContains(t, w.Body.String(), "secret.mp3")
	for _, path := range []string{"/v1/videos/" + id, "/v1/videos/" + id + "/content"} {
		w = httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	}
}

type generationDeleteHTTPRepo struct {
	generationNativeHTTPRepo
	deleted bool
}

func (r *generationDeleteHTTPRepo) DeleteGenerationResult(_ context.Context, id string, user, key, group int64, videoOnly bool) error {
	task := r.task
	if task == nil || task.ID != id || task.UserID != user || task.APIKeyID != key || task.GroupID != group || task.PluginVersionID <= 0 {
		return service.ErrGenerationTaskNotFound
	}
	if videoOnly && !service.IsVideoGenerationProtocol(task.Protocol) {
		return service.ErrGenerationTaskNotFound
	}
	if r.deleted {
		return nil
	}
	if (task.Status != service.GenerationSucceeded && task.Status != service.GenerationFailed) || task.BillingStatus != service.GenerationBillingSettled {
		return service.ErrGenerationTaskResultNotDeletable
	}
	r.deleted = true
	return nil
}

func TestGenerationResultDeleteHTTPContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := "video_" + strings.Repeat("3", 40)
	for _, tc := range []struct {
		name      string
		path      string
		protocol  string
		status    string
		billing   string
		user      int64
		key       int64
		group     int64
		wantCode  int
		wantBody  string
		wantExact string
	}{
		{
			name:      "video delete returns OpenAI video.deleted JSON",
			path:      "/v1/videos/" + id,
			protocol:  service.GenerationProtocolOpenAI,
			status:    service.GenerationSucceeded,
			billing:   service.GenerationBillingSettled,
			user:      1,
			key:       2,
			group:     3,
			wantCode:  http.StatusOK,
			wantExact: `{"id":"` + id + `","object":"video.deleted","deleted":true}`,
		},
		{
			name:     "task result delete returns 204 No Content",
			path:     "/v1/tasks/" + id + "/result",
			protocol: service.GenerationProtocolSunoTask,
			status:   service.GenerationFailed,
			billing:  service.GenerationBillingSettled,
			user:     1,
			key:      2,
			group:    3,
			wantCode: http.StatusNoContent,
		},
		{
			name:     "video delete rejects non-video task with 404",
			path:     "/v1/videos/" + id,
			protocol: service.GenerationProtocolSunoTask,
			status:   service.GenerationSucceeded,
			billing:  service.GenerationBillingSettled,
			user:     1,
			key:      2,
			group:    3,
			wantCode: http.StatusNotFound,
			wantBody: "task_not_found",
		},
		{
			name:     "active task cannot be deleted (409)",
			path:     "/v1/videos/" + id,
			protocol: service.GenerationProtocolOpenAI,
			status:   service.GenerationRunning,
			billing:  service.GenerationBillingPending,
			user:     1,
			key:      2,
			group:    3,
			wantCode: http.StatusConflict,
			wantBody: "task_result_in_use",
		},
		{
			name:     "unsettled terminal task cannot be deleted (409)",
			path:     "/v1/tasks/" + id + "/result",
			protocol: service.GenerationProtocolSunoTask,
			status:   service.GenerationSucceeded,
			billing:  service.GenerationBillingPending,
			user:     1,
			key:      2,
			group:    3,
			wantCode: http.StatusConflict,
			wantBody: "task_result_in_use",
		},
		{
			name:     "foreign user cannot delete result (404)",
			path:     "/v1/tasks/" + id + "/result",
			protocol: service.GenerationProtocolOpenAI,
			status:   service.GenerationSucceeded,
			billing:  service.GenerationBillingSettled,
			user:     99,
			key:      2,
			group:    3,
			wantCode: http.StatusNotFound,
			wantBody: "task_not_found",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &service.GenerationTask{
				ID: id, UserID: 1, APIKeyID: 2, GroupID: 3, PluginVersionID: 7,
				Protocol: tc.protocol, Status: tc.status, BillingStatus: tc.billing,
			}
			repo := &generationDeleteHTTPRepo{generationNativeHTTPRepo: generationNativeHTTPRepo{generationGatewayReplayFixture{task: task}}}
			gen := service.NewGenerationService(repo, nil, nil, nil, nil, nil)
			defer gen.Stop()
			h := &OpenAIGatewayHandler{generations: gen}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: tc.key, UserID: tc.user, GroupID: &tc.group})
			})
			router.DELETE("/v1/videos/:request_id", h.GenerationVideoDelete)
			router.DELETE("/v1/tasks/:key/result", h.GenerationTaskDeleteResult)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, tc.path, nil))
			require.Equal(t, tc.wantCode, w.Code, w.Body.String())
			require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
			if tc.wantExact != "" {
				require.JSONEq(t, tc.wantExact, w.Body.String())
			}
			if tc.wantBody != "" {
				require.Contains(t, w.Body.String(), tc.wantBody)
			}
		})
	}
}
