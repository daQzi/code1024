package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNewAPIPluginBalanceHandlerAdmission(t *testing.T) {
	for _, scenario := range []struct {
		id     string
		budget int64
		status int
	}{
		{"bad", newAPIPluginBalanceHTTPMemory, 400}, {"-1", newAPIPluginBalanceHTTPMemory, 400},
		{"17", 1, 503}, {"17", newAPIPluginBalanceHTTPMemory, 503},
	} {
		t.Run(scenario.id+"/"+http.StatusText(scenario.status), func(t *testing.T) {
			r := gin.New()
			r.Use(middleware.RequestBodyLimit(2<<20, pkghttputil.NewBodyMemoryBudget(scenario.budget)))
			h := NewNewAPIPluginHandler(nil, nil)
			r.POST("/accounts/:id/balance", h.QueryNewAPIPluginBalance)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", "/accounts/"+scenario.id+"/balance", nil))
			require.Equal(t, scenario.status, w.Code)
			if scenario.id == "17" {
				require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
				require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
			}
			if scenario.budget == 1 {
				require.Equal(t, "1", w.Header().Get("Retry-After"))
			}
		})
	}
}

const newAPIPluginBalanceHTTPMemory = 16 << 20

func TestNewAPIPluginModelsHTTPAdmission(t *testing.T) {
	for _, scenario := range []struct {
		id     string
		budget int64
		status int
	}{{"bad", 16 << 20, 400}, {"-1", 16 << 20, 400}, {"17", 1, 503}, {"17", 16 << 20, 503}} {
		t.Run(scenario.id+"/"+http.StatusText(scenario.status), func(t *testing.T) {
			r := gin.New()
			r.Use(middleware.RequestBodyLimit(2<<20, pkghttputil.NewBodyMemoryBudget(scenario.budget)))
			h := NewNewAPIPluginHandler(nil, nil)
			r.POST("/accounts/:id/models", h.QueryNewAPIPluginModels)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", "/accounts/"+scenario.id+"/models", nil))
			require.Equal(t, scenario.status, w.Code)
			if scenario.id == "17" {
				require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
				require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
			}
			if scenario.budget == 1 {
				require.Equal(t, "1", w.Header().Get("Retry-After"))
			}
		})
	}
}

func TestNewAPIPluginVersionInfoHTTP(t *testing.T) {
	for _, scenario := range []string{"saved", "invalid-id", "capacity", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			repo := &pluginSourceHTTPRepo{version: &service.NewAPIPluginVersion{ID: 91, Key: "historical-fixture", Version: "0.9.0", Source: "throw Error('metadata never executes');"}}
			if scenario == "missing" {
				repo.version = nil
			}
			s, err := service.NewNewAPIPluginService(repo, nil)
			require.NoError(t, err)
			defer s.Stop()
			h := NewNewAPIPluginHandler(s, nil)
			r := gin.New()
			budget := int64(newAPIPluginBalanceHTTPMemory)
			if scenario == "capacity" {
				budget = 1
			}
			r.Use(middleware.RequestBodyLimit(2<<20, pkghttputil.NewBodyMemoryBudget(budget)))
			r.GET("/versions/:id/info", h.NewAPIVersionInfo)
			path := "/versions/91/info"
			if scenario == "invalid-id" {
				path = "/versions/bad/info"
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			switch scenario {
			case "saved":
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), `"id":91`)
				require.Contains(t, w.Body.String(), `"compatibility":`)
				require.NotContains(t, w.Body.String(), "metadata never executes")
				require.NotContains(t, w.Body.String(), `"source":`)
				require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
				require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
			case "invalid-id":
				require.Equal(t, 400, w.Code)
				require.Zero(t, repo.reads)
			case "capacity":
				require.Equal(t, 503, w.Code)
				require.Zero(t, repo.reads)
				require.Equal(t, "1", w.Header().Get("Retry-After"))
			case "missing":
				require.Equal(t, 404, w.Code)
			}
		})
	}
}
