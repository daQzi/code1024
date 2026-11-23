package admin

import (
	"context"
	"strconv"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// Registry sources are administrator-only, with the same authorization as
// plugin version history. Reserve for DB/source + worst-case JSON escaping.
const pluginSourceResponseMemory = 16 << 20

func reservePluginSourceResponse(c *gin.Context) bool {
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	if err := pkghttputil.ReserveRequestBodyMemory(c.Request.Context(), pluginSourceResponseMemory); err != nil {
		middleware.RejectRequestBodyMemoryOverload(c, err)
		return false
	}
	return true
}

func (h *NewAPIPluginHandler) NewAPISource(c *gin.Context) {
	if !h.requireNewAPI(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid plugin version ID")
		return
	}
	if !reservePluginSourceResponse(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	source, err := h.newAPI.PluginSource(ctx, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, source)
}

func (h *NewAPIPluginHandler) NewAPIFactorySource(c *gin.Context) {
	if !h.requireNewAPI(c) {
		return
	}
	if !reservePluginSourceResponse(c) {
		return
	}
	source, err := h.newAPI.FactorySource(c.Param("key"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, source)
}
