package pelican

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestManagementRoutesRequireAdministrator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	jwt := middleware.JWTAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})
	admin := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.AbortWithStatus(http.StatusForbidden)
	})
	audit := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	RegisterRoutes(r.Group("/api/v1"), &Service{}, jwt, admin, audit, nil, nil)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/config"}, {"PUT", "/config"}, {"GET", "/options"}, {"POST", "/run"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			for _, auth := range []string{"", "user-token"} {
				req := httptest.NewRequest(tc.method, "/api/v1/pelican"+tc.path, nil)
				req.Header.Set("Authorization", auth)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				want := http.StatusForbidden
				if auth == "" {
					want = http.StatusUnauthorized
				}
				require.Equal(t, want, w.Code)
			}
		})
	}
	for _, path := range []string{"/overview", "/status"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/pelican"+path, nil))
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
}
