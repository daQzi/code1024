package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPluginCredentialHTTPAdmissionAndSessionBinding(t *testing.T) {
	for _, scenario := range []struct {
		name, method, id, body string
		auth, session          bool
		budget                 int64
		status                 int
	}{
		{"bad-id", "GET", "bad", "", true, true, 64 << 20, 400},
		{"unauthenticated", "GET", "17", "", false, false, 64 << 20, 401},
		{"login-api-key-only", "POST", "17", `{"action":"login"}`, true, false, 64 << 20, 400},
		{"continue-api-key-only", "POST", "17", `{"action":"continue"}`, true, false, 64 << 20, 400},
		{"negative-revision", "POST", "17", `{"action":"login","revision":-1}`, true, true, 64 << 20, 400},
		{"memory", "GET", "17", "", true, true, 1, 503},
		{"no-service", "GET", "17", "", true, true, 64 << 20, 400},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			r := gin.New()
			r.Use(middleware.RequestBodyLimit(2<<20, pkghttputil.NewBodyMemoryBudget(scenario.budget)))
			r.Use(func(c *gin.Context) {
				if scenario.auth {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
				}
				if scenario.session {
					c.Set(middleware.ContextKeySessionID, "private-session-canary")
				}
				c.Next()
			})
			h := NewNewAPIPluginHandler(nil, nil)
			r.Handle(scenario.method, "/accounts/:id/credential", h.PluginCredential)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(scenario.method, "/accounts/"+scenario.id+"/credential", strings.NewReader(scenario.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			require.Equal(t, scenario.status, w.Code)
			require.NotContains(t, w.Body.String(), "private-session-canary")
			if scenario.id == "17" {
				require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
			}
			if scenario.budget == 1 {
				require.Equal(t, "1", w.Header().Get("Retry-After"))
			}
		})
	}
}

func TestPluginCredentialHTTPCleanupAuthenticationAndNoCache(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			if authenticated {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
			}
			c.Next()
		})
		h := NewNewAPIPluginHandler(nil, nil)
		r.GET("/credential-cleanup", h.CredentialRevocations)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/credential-cleanup", nil))
		if authenticated {
			require.Equal(t, 503, w.Code)
		} else {
			require.Equal(t, 401, w.Code)
		}
		require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
	}
}
