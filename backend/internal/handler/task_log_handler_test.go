package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type taskLogFixture struct {
	service.GenerationTaskRepository
	filter service.TaskLogFilter
	owner  int64
}

func (r *taskLogFixture) Claim(context.Context, string, time.Duration) (*service.GenerationTask, error) {
	return nil, service.ErrGenerationTaskNotFound
}

func (r *taskLogFixture) ListTaskLogs(_ context.Context, f service.TaskLogFilter) ([]service.TaskLog, int64, error) {
	r.filter = f
	return []service.TaskLog{{ID: "video_fixture", UserID: 7, APIKeyID: 8, AccountID: 9, UpstreamID: "private-upstream"}}, 1, nil
}

func (r *taskLogFixture) GetTaskLog(_ context.Context, id string, owner int64) (*service.TaskLogDetail, error) {
	r.owner = owner
	if owner != 0 && owner != 7 {
		return nil, service.ErrGenerationTaskNotFound
	}
	return &service.TaskLogDetail{TaskLog: service.TaskLog{ID: id, UserID: 7, APIKeyID: 8, AccountID: 9, UpstreamID: "private-upstream"}, Events: []service.TaskLogEvent{}}, nil
}

func TestTaskLogConsoleOwnershipAndPublicProjection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &taskLogFixture{}
	svc := service.NewGenerationService(repo, nil, nil, nil, nil, nil)
	defer svc.Stop()
	h := NewTaskLogHandler(svc)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7}) })
	r.GET("/tasks", h.List)
	r.GET("/tasks/:task_id", h.Get)
	r.GET("/admin/tasks", h.AdminList)
	for _, tc := range []struct {
		path  string
		code  int
		owner int64
	}{
		{"/tasks?user_id=99&account_id=9", 200, 7},
		{"/tasks?group_id=bad", 400, 0},
		{"/tasks?start=invalid", 400, 0},
		{"/tasks?start=2026-11-22T00:00:00Z&end=2026-11-21T00:00:00Z", 400, 0},
		{"/admin/tasks?scope=mine", 200, 7},
		{"/admin/tasks", 200, 0},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		require.Equal(t, tc.code, w.Code, w.Body.String())
		if tc.code == 200 {
			require.Equal(t, tc.owner, repo.filter.UserID)
			if strings.HasPrefix(tc.path, "/tasks") {
				require.Zero(t, repo.filter.AccountID)
				require.NotContains(t, w.Body.String(), "upstream_id")
				require.NotContains(t, w.Body.String(), "account_id")
			}
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/video_fixture", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.EqualValues(t, 7, repo.owner)
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	for _, key := range []string{"user_id", "api_key_id", "account_id", "upstream_id", "state", "request", "config_encrypted"} {
		require.NotContains(t, envelope.Data, key)
	}
	r.PUT("/admin/tasks/:task_id/native-maintenance", h.AdminMaintainNative)
	for _, body := range []string{`{"action":"resubmit","reason":"verified"}`, `{"action":"resume_poll","reason":""}`, `invalid`} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/admin/tasks/native_video_fixture/native-maintenance", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	}
	_, err := svc.TaskLog(context.Background(), "video_fixture", 99, false)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	_, err = svc.TaskLog(context.Background(), "video_fixture", 0, false)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
}
