//go:build unit

package routes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "plugin" && os.Args[2] == "worker" {
		if err := runner.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type pluginMgmtRouteRepo struct {
	service.NewAPIPluginRepository
	version *service.NewAPIPluginVersion
	deleted int64
}

func (r *pluginMgmtRouteRepo) Get(context.Context, int64) (*service.NewAPIPluginVersion, error) {
	return r.version, nil
}

func (r *pluginMgmtRouteRepo) DeleteVersion(_ context.Context, id int64, _ func(service.NewAPIPluginVersion) bool) (*service.PluginDeleteResult, error) {
	r.deleted = id
	return &service.PluginDeleteResult{DeletedID: id, Key: r.version.Key, Version: r.version.Version, RemovedPlugin: true}, nil
}

func (r *pluginMgmtRouteRepo) ListPluginAccountPricing(_ context.Context, _ string) ([]service.PluginAccountPricingSummary, error) {
	unitPrice := decimal.RequireFromString("0.20")
	return []service.PluginAccountPricingSummary{{
		AccountID:    12,
		AccountName:  "Demo Account",
		Status:       "active",
		Schedulable:  true,
		VersionID:    7,
		Version:      "1.0.0",
		PricingRules: map[string]service.GenerationPriceRule{"demo-model": {Mode: "fixed", Unit: "request", UnitPrice: &unitPrice}},
	}}, nil
}

func TestNewAPIPluginDeleteDryRunAndPricingRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := &scopedRouteKeys{}
	settings := service.NewSettingService(nil, nil)
	settings.SetScopedAdminKeyRepository(keys)
	key, _, err := settings.CreateScopedAdminKey(context.Background(), service.ScopedAdminKeyInput{
		Name:              "full-admin",
		Enabled:           true,
		ConfirmFullAccess: true,
		Policy:            adminaccess.Policy{FullAccess: true},
	}, 9)
	require.NoError(t, err)

	src := `export const meta = { apiVersion: 1, key: "demo-custom", name: "Demo", author: { name: "Test" }, version: "1.0.0" };
export function ingress(req) { return { count: req.n }; }`
	sum := sha256.Sum256([]byte(src))
	repo := &pluginMgmtRouteRepo{
		version: &service.NewAPIPluginVersion{
			ID: 7, Key: "demo-custom", Version: "1.0.0", Origin: "upload",
			SHA256: hex.EncodeToString(sum[:]), Source: src,
		},
	}
	plugins, err := service.NewNewAPIPluginService(repo, nil)
	require.NoError(t, err)
	defer plugins.Stop()

	auth := middleware.NewAdminAuthMiddleware(nil, service.NewUserService(&scopedRouteUsers{}, nil, nil, nil), settings, nil)
	allowStepUp := true
	stepUps := 0
	r := gin.New()
	RegisterAdminRoutes(
		r.Group("/api/v1"),
		&handler.Handlers{Admin: &handler.AdminHandlers{NewAPIPlugin: adminhandler.NewNewAPIPluginHandler(plugins, nil)}},
		auth,
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		middleware.StepUpAuthMiddleware(func(c *gin.Context) {
			stepUps++
			if !allowStepUp {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
		}),
		nil,
		nil,
	)

	t.Run("GET /:key/pricing returns bound account pricing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/newapi-plugins/demo-custom/pricing", nil)
		req.Header.Set("x-api-key", key)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "Demo Account")
	})

	t.Run("POST /versions/:id/dry-run routes to handler and validates hook", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/newapi-plugins/versions/7/dry-run", bytes.NewBufferString(`{"hook":"unknownHook","args":[{}]}`))
		req.Header.Set("x-api-key", key)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "not available in the offline playground")
	})

	t.Run("POST /versions/:id/dry-run executes retained source offline", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/newapi-plugins/versions/7/dry-run", bytes.NewBufferString(`{"hook":"ingress","args":[{"n":8}]}`))
		req.Header.Set("x-api-key", key)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), `"offline":true`)
		require.Contains(t, w.Body.String(), `"count":8`)
	})

	t.Run("POST /versions/:id/dry-run preserves missing-version status", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/newapi-plugins/versions/99/dry-run", bytes.NewBufferString(`{"hook":"extractUsage","args":[{}]}`))
		req.Header.Set("x-api-key", key)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	})

	t.Run("DELETE /versions/:id requires step-up auth and deletes custom version", func(t *testing.T) {
		allowStepUp = false
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/newapi-plugins/versions/7", nil)
		req.Header.Set("x-api-key", key)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code)
		require.Zero(t, repo.deleted)

		allowStepUp = true
		req = httptest.NewRequest(http.MethodDelete, "/api/v1/admin/newapi-plugins/versions/7", nil)
		req.Header.Set("x-api-key", key)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.EqualValues(t, 7, repo.deleted)
		require.Equal(t, 2, stepUps)
	})
}
