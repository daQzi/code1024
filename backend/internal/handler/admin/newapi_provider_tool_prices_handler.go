package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *NewAPIPluginHandler) ProviderToolPrices(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	if !h.requireNewAPI(c) {
		return
	}
	value, err := h.newAPI.ProviderToolPrices(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}

func (h *NewAPIPluginHandler) SaveProviderToolPrices(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	if !h.requireNewAPI(c) {
		return
	}
	var input service.ProviderToolPriceSettings
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "工具售价配置无效")
		return
	}
	value, err := h.newAPI.SaveProviderToolPrices(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}
