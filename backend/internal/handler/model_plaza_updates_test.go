//go:build unit

package handler

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type plazaUpdateSettings struct {
	service.SettingRepository
	disabled atomic.Bool
}

func (r *plazaUpdateSettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{service.SettingKeyModelPlazaEnabled: fmt.Sprint(!r.disabled.Load())}, nil
}
func (*plazaUpdateSettings) GetValue(context.Context, string) (string, error) { return "false", nil }

type plazaUpdateMetrics struct {
	service.ModelPlazaMetricsRepository
	version atomic.Int64
	calls   atomic.Int64
}

func (r *plazaUpdateMetrics) ReadModelPerformanceVersion(_ context.Context, scopes []service.PlazaMetricsScope) (string, error) {
	r.calls.Add(1)
	return fmt.Sprint(r.version.Load()), nil
}
func newPlazaUpdates(t *testing.T) (*ModelPlazaHandler, *plazaUpdateMetrics, *plazaUpdateSettings, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	metrics := &plazaUpdateMetrics{}
	settings := &plazaUpdateSettings{}
	svc := service.ProvideModelPlazaService(
		plazaVaryChannelRepo{channels: []service.Channel{{Status: service.StatusActive, GroupIDs: []int64{1, 2}, ModelMapping: map[string]map[string]string{"openai": {"model": "model"}}}}},
		plazaVaryGroupRepo{groups: []service.Group{{ID: 1, Platform: "openai"}, {ID: 2, Platform: "openai", IsExclusive: true}}}, nil, nil, nil, metrics, nil, nil, nil)
	h := NewModelPlazaHandler(svc, nil, service.NewSettingService(settings, &config.Config{}))
	t.Cleanup(h.CloseNotifications)
	router := gin.New()
	router.GET("/updates", h.WatchPerformance)
	return h, metrics, settings, router
}
func plazaUpdateURL(group int) string {
	return fmt.Sprintf("/updates?group_id=%d&ids=%s", group, plazaModelID("openai", "model"))
}
func readPlazaFrame(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	var lines []string
	for {
		line, err := reader.ReadString('\n')
		require.NoError(t, err)
		lines = append(lines, line)
		if line == "\n" {
			return strings.Join(lines, "")
		}
	}
}
func TestModelPlazaUpdatesRejectsHiddenScopeAndOverloadBeforeVersionQuery(t *testing.T) {
	h, metrics, _, router := newPlazaUpdates(t)
	for _, path := range []string{plazaUpdateURL(2), plazaUpdateURL(99), "/updates?group_id=1&ids=invalid"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		require.Contains(t, []int{400, 404}, response.Code)
	}
	require.Zero(t, metrics.calls.Load())
	for i := 0; i < 16; i++ {
		h.notificationSlots <- struct{}{}
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", plazaUpdateURL(1), nil))
	require.Equal(t, 503, response.Code)
	require.Equal(t, "15", response.Header().Get("Retry-After"))
	require.Zero(t, metrics.calls.Load())
}
func TestModelPlazaUpdatesRealHTTPRefreshRevocationAndCancellation(t *testing.T) {
	h, metrics, settings, router := newPlazaUpdates(t)
	server := httptest.NewServer(router)
	defer server.Close()
	client := &http.Client{Timeout: 8 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+plazaUpdateURL(1), nil)
	require.NoError(t, err)
	reply, err := client.Do(request)
	require.NoError(t, err)
	defer reply.Body.Close()
	require.Equal(t, 200, reply.StatusCode)
	require.Equal(t, "private, no-store", reply.Header.Get("Cache-Control"))
	reader := bufio.NewReader(reply.Body)
	require.Contains(t, readPlazaFrame(t, reader), "event: ready")
	metrics.version.Add(1)
	require.Contains(t, readPlazaFrame(t, reader), "event: refresh")
	settings.disabled.Store(true)
	require.Contains(t, readPlazaFrame(t, reader), "event: scope_changed")
	require.Eventually(t, func() bool { return len(h.notificationSlots) == 0 }, time.Second, time.Millisecond)
	settings.disabled.Store(false)
	reply2, err := client.Get(server.URL + plazaUpdateURL(1))
	require.NoError(t, err)
	require.Contains(t, readPlazaFrame(t, bufio.NewReader(reply2.Body)), "event: ready")
	h.CloseNotifications()
	require.Eventually(t, func() bool { return len(h.notificationSlots) == 0 }, time.Second, time.Millisecond)
	reply2.Body.Close()
}
func TestModelPlazaUpdatesClientCancelReleasesSlot(t *testing.T) {
	h, _, _, router := newPlazaUpdates(t)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+plazaUpdateURL(1), nil)
	require.NoError(t, err)
	reply, err := server.Client().Do(request)
	require.NoError(t, err)
	require.Contains(t, readPlazaFrame(t, bufio.NewReader(reply.Body)), "event: ready")
	cancel()
	reply.Body.Close()
	require.Eventually(t, func() bool { return len(h.notificationSlots) == 0 }, time.Second, time.Millisecond)
}

func TestModelPlazaUpdatesRequiresCancellableTransportAndStopsAdmission(t *testing.T) {
	h, metrics, _, router := newPlazaUpdates(t)
	// A writer without deadlines is not admitted as a long-lived stream.
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", plazaUpdateURL(1), nil))
	require.Equal(t, 503, recorder.Code)
	require.EqualValues(t, 1, metrics.calls.Load())
	require.Empty(t, h.notificationSlots)
	h.CloseNotifications()
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", plazaUpdateURL(1), nil))
	require.Equal(t, 503, recorder.Code)
	require.Empty(t, h.notificationSlots)
}

func TestModelPlazaUpdatesRevalidatesTokenBeforeScopeQuery(t *testing.T) {
	h, metrics, _, _ := newPlazaUpdates(t)
	cfg := &config.Config{}
	cfg.JWT.Secret = "isolated-notification-fixture"
	h.authService = service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	h.apiKeyService = &service.APIKeyService{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Next()
	})
	router.GET("/updates", h.WatchPerformance)
	request := httptest.NewRequest("GET", plazaUpdateURL(1), nil)
	request.Header.Set("Authorization", "bearer invalid-token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Zero(t, metrics.calls.Load())
	require.Empty(t, h.notificationSlots)
}
