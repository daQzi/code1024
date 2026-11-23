//go:build unit

package routes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pluginSourceRouteRepo struct {
	service.NewAPIPluginRepository
	version *service.NewAPIPluginVersion
	reads   int
}

func (r *pluginSourceRouteRepo) Get(context.Context, int64) (*service.NewAPIPluginVersion, error) {
	r.reads++
	return r.version, nil
}

func TestPluginSourceRegisteredRoutesRetainAdminAuthorization(t *testing.T) {
	for _, mode := range []string{"full", "limited", "unauthenticated"} {
		t.Run(mode, func(t *testing.T) {
			keys := &scopedRouteKeys{}
			settings := service.NewSettingService(nil, nil)
			settings.SetScopedAdminKeyRepository(keys)
			policy := adminaccess.Policy{GlobalPermissions: []string{"users.read"}}
			if mode == "full" {
				policy = adminaccess.Policy{FullAccess: true}
			}
			key, _, err := settings.CreateScopedAdminKey(context.Background(), service.ScopedAdminKeyInput{Name: "source inspection", Enabled: true, ConfirmFullAccess: mode == "full", Policy: policy}, 9)
			require.NoError(t, err)
			source := "throw Error('source only');"
			hash := sha256.Sum256([]byte(source))
			repo := &pluginSourceRouteRepo{version: &service.NewAPIPluginVersion{ID: 7, Key: "source-fixture", Version: "1.0.0", SHA256: hex.EncodeToString(hash[:]), Source: source}}
			plugins, err := service.NewNewAPIPluginService(repo, nil)
			require.NoError(t, err)
			defer plugins.Stop()
			auth := middleware.NewAdminAuthMiddleware(nil, service.NewUserService(&scopedRouteUsers{}, nil, nil, nil), settings, nil)
			r := gin.New()
			stepUps := 0
			RegisterAdminRoutes(r.Group("/api/v1"), &handler.Handlers{Admin: &handler.AdminHandlers{NewAPIPlugin: adminhandler.NewNewAPIPluginHandler(plugins, nil)}}, auth, middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }), middleware.StepUpAuthMiddleware(func(c *gin.Context) { stepUps++; c.AbortWithStatus(403) }), nil, nil)
			for _, path := range []string{"/api/v1/admin/newapi-plugins/versions/7/info", "/api/v1/admin/newapi-plugins/versions/7/source", "/api/v1/admin/newapi-plugins/factories/" + builtin.Keys()[0] + "/source"} {
				req := httptest.NewRequest("GET", path, nil)
				if mode != "unauthenticated" {
					req.Header.Set("x-api-key", key)
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				expected := 200
				if mode == "limited" {
					expected = 403
				} else if mode == "unauthenticated" {
					expected = 401
				}
				require.Equal(t, expected, w.Code, w.Body.String())
				if mode == "full" {
					require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
				}
			}
			if mode != "full" {
				for _, method := range []string{"GET", "PUT"} {
					for _, path := range []string{"/api/v1/admin/request-processing/model-modifiers", "/api/v1/admin/newapi-plugins/provider-tool-prices"} {
						req := httptest.NewRequest(method, path, nil)
						if mode != "unauthenticated" {
							req.Header.Set("x-api-key", key)
						}
						w := httptest.NewRecorder()
						r.ServeHTTP(w, req)
						expected := 403
						if mode == "unauthenticated" {
							expected = 401
						}
						require.Equal(t, expected, w.Code, w.Body.String())
					}
				}
			}
			for _, action := range []string{"balance", "models", "credential"} {
				req := httptest.NewRequest("POST", "/api/v1/admin/newapi-plugins/accounts/17/"+action, nil)
				if mode != "unauthenticated" {
					req.Header.Set("x-api-key", key)
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if mode == "unauthenticated" {
					require.Equal(t, 401, w.Code)
				} else {
					require.Equal(t, 403, w.Code)
				}
			}
			if mode == "full" {
				req := httptest.NewRequest("PUT", "/api/v1/admin/newapi-plugins/provider-tool-prices", nil)
				req.Header.Set("x-api-key", key)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, 403, w.Code)
				require.Equal(t, 4, stepUps, "management requires step-up before service access")
			} else {
				require.Zero(t, stepUps, "authorization precedes step-up and upstream calls")
			}
			if mode == "full" {
				require.Equal(t, 2, repo.reads)
			} else {
				require.Zero(t, repo.reads, "authorization must precede artifact reads")
			}
		})
	}
}
