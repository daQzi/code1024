package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func (h *NewAPIPluginHandler) QueryNewAPIPluginBalance(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "账号 ID 无效")
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	result, err := h.newAPI.QueryNewAPIPluginBalance(c.Request.Context(), id)
	if err != nil {
		if !middleware.RejectRequestBodyMemoryOverload(c, err) {
			response.ErrorFrom(c, err)
		}
		return
	}
	response.Success(c, result)
}

func (h *NewAPIPluginHandler) NewAPIVersionInfo(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "插件版本 ID 无效")
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	info, err := h.newAPI.VersionInfo(c.Request.Context(), id)
	if err != nil {
		if !middleware.RejectRequestBodyMemoryOverload(c, err) {
			response.ErrorFrom(c, err)
		}
		return
	}
	response.Success(c, info)
}

func (h *NewAPIPluginHandler) QueryNewAPIPluginModels(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "账号 ID 无效")
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	result, err := h.newAPI.QueryNewAPIPluginModels(c.Request.Context(), id)
	if err != nil {
		if !middleware.RejectRequestBodyMemoryOverload(c, err) {
			response.ErrorFrom(c, err)
		}
		return
	}
	response.Success(c, result)
}
