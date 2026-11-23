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

type registryHandlerFixture struct {
	service.NewAPIPluginRepository
	service.PluginRegistryRepository
	filter      service.PluginRegistryFilter
	transitions int
}

func (r *registryHandlerFixture) ListPluginSubmissions(_ context.Context, f service.PluginRegistryFilter) ([]service.PluginSubmission, int64, error) {
	r.filter = f
	return []service.PluginSubmission{}, 0, nil
}
func (r *registryHandlerFixture) GetPluginSubmission(context.Context, int64) (*service.PluginSubmission, error) {
	return &service.PluginSubmission{AuthorID: 99, Status: "draft", Source: "private-source"}, nil
}
func (r *registryHandlerFixture) TransitionPluginSubmission(context.Context, int64, int64, int64, string, string, bool) error {
	r.transitions++
	return nil
}

func TestPluginRegistryRoutesEnforceOwnerAndPublishingRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &registryHandlerFixture{}
	svc, err := service.NewNewAPIPluginService(repo, nil)
	require.NoError(t, err)
	defer svc.Stop()
	h := NewPluginRegistryHandler(svc)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7}) })
	r.GET("/registry", h.Mine)
	r.GET("/market", h.Market)
	r.GET("/registry/:id", h.Get)
	r.GET("/templates", h.Templates)
	r.POST("/dry-run", h.DryRun)
	r.POST("/registry/:id/transition", h.Transition)
	for _, tc := range []struct {
		path   string
		owner  int64
		status string
	}{{"/registry?user_id=99&status=published", 7, ""}, {"/market?user_id=99&status=draft", 0, "published"}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Equal(t, tc.owner, repo.filter.Owner)
		require.Equal(t, tc.status, repo.filter.Status)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/registry/1", nil))
	require.Equal(t, 404, w.Code)
	require.NotContains(t, w.Body.String(), "private-source")
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/registry/1/transition", strings.NewReader(`{"action":"publish","revision":1}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, 403, w.Code)
	require.Zero(t, repo.transitions, "user routes must never invoke the administrative publication path")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/templates", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	require.Contains(t, w.Body.String(), "community-7-example")
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/dry-run", strings.NewReader(`{"source":"fixture","hook":"refresh","args":[]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, 400, w.Code, w.Body.String())
	require.Zero(t, repo.transitions, "offline validation cannot publish or install code")
}
