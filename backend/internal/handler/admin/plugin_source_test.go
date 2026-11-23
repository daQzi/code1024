package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pluginSourceHTTPRepo struct {
	service.NewAPIPluginRepository
	reads    int
	version  *service.NewAPIPluginVersion
	block    bool
	started  chan struct{}
	deadline time.Time
}

func (r *pluginSourceHTTPRepo) Get(ctx context.Context, _ int64) (*service.NewAPIPluginVersion, error) {
	r.reads++
	if r.block {
		r.deadline, _ = ctx.Deadline()
		close(r.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return r.version, nil
}

func TestPluginSourceHTTPReadOnlyAndAdmission(t *testing.T) {
	for _, scenario := range []string{"source", "invalid-id", "capacity"} {
		t.Run(scenario, func(t *testing.T) {
			source := "\ufeffthrow Error('must never execute');\r\n"
			digest := sha256.Sum256([]byte(source))
			repo := &pluginSourceHTTPRepo{version: &service.NewAPIPluginVersion{ID: 7, Key: "source-fixture", Version: "1.0.0", SHA256: hex.EncodeToString(digest[:]), Source: source}}
			s, err := service.NewNewAPIPluginService(repo, nil)
			require.NoError(t, err)
			defer s.Stop()
			h := NewNewAPIPluginHandler(s, nil)
			r := gin.New()
			budget := int64(pluginSourceResponseMemory)
			if scenario == "capacity" {
				budget = 1
			}
			r.Use(middleware.RequestBodyLimit(2<<20, pkghttputil.NewBodyMemoryBudget(budget)))
			r.GET("/versions/:id/source", h.NewAPISource)
			path := "/versions/7/source"
			if scenario == "invalid-id" {
				path = "/versions/no/source"
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			switch scenario {
			case "source":
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
				require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
				require.Contains(t, w.Body.String(), "must never execute")
				require.NotContains(t, w.Body.String(), "capabilities")
				require.Equal(t, 1, repo.reads)
			case "invalid-id":
				require.Equal(t, 400, w.Code)
				require.Zero(t, repo.reads)
			case "capacity":
				require.Equal(t, 503, w.Code)
				require.Zero(t, repo.reads)
			}
		})
	}
}

func TestPluginSourceHTTPCancellationReleasesSharedAdmission(t *testing.T) {
	repo := &pluginSourceHTTPRepo{block: true, started: make(chan struct{})}
	s, err := service.NewNewAPIPluginService(repo, nil)
	require.NoError(t, err)
	defer s.Stop()
	h := NewNewAPIPluginHandler(s, nil)
	budget := pkghttputil.NewBodyMemoryBudget(pluginSourceResponseMemory)
	r := gin.New()
	r.Use(middleware.RequestBodyLimit(2<<20, budget))
	r.GET("/versions/:id/source", h.NewAPISource)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/versions/7/source", nil).WithContext(ctx))
	}()
	select {
	case <-repo.started:
	case <-time.After(time.Second):
		t.Fatal("artifact query did not start")
	}
	require.WithinDuration(t, time.Now().Add(5*time.Second), repo.deadline, time.Second)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled artifact query did not stop")
	}
	check, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
	defer release()
	require.NoError(t, pkghttputil.ReserveRequestBodyMemory(check, pluginSourceResponseMemory))
}
