package routes

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// NativeTaskAliases offers declared paths only after Gin has ruled out a
// static route. Its private router is never reachable outside this middleware.
func NativeTaskAliases(h *handler.Handlers, apiKeyAuth middleware.APIKeyAuthMiddleware, ops *service.OpsService, cfg *config.Config) gin.HandlerFunc {
	inner := gin.New()
	inner.RedirectTrailingSlash = false
	if err := inner.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		_ = inner.SetTrustedProxies(nil)
	}
	inner.Use(gin.Recovery(), middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize, pkghttputil.NewBodyMemoryBudget(cfg.Gateway.RequestBodyMemoryBudgetBytes)))
	inner.Use(middleware.ClientRequestID(), handler.OpsErrorLoggerMiddleware(ops, h.ModelPlaza), handler.InboundEndpointMiddleware(), func(c *gin.Context) {
		middleware.MarkNativeTaskAliasLookup(c)
		c.Next()
	}, gin.HandlerFunc(apiKeyAuth))
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		inner.Handle(method, "/*nativePath", h.OpenAIGateway.GenerationTaskNativeAlias)
	}
	return func(c *gin.Context) {
		if c.FullPath() != "" || c.Request.URL.RawPath != "" || strings.Contains(c.Request.URL.EscapedPath(), "%") || !h.OpenAIGateway.MayDispatchNativeTaskAlias(c) {
			c.Next()
			return
		}
		inner.ServeHTTP(c.Writer, c.Request)
		c.Abort()
	}
}
