package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *RequestPluginHandler) ModelModifiers(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	if !h.requirePlugins(c) {
		return
	}
	value, err := h.plugins.ModelModifierSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}

func (h *RequestPluginHandler) SaveModelModifiers(c *gin.Context) {
	if !h.requirePlugins(c) {
		return
	}
	if _, ok := pluginRegistryActor(c); !ok {
		return
	}
	var input service.ModelModifierSettings
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "Invalid model name exemption settings")
		return
	}
	value, err := h.plugins.SaveModelModifierSettings(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}
