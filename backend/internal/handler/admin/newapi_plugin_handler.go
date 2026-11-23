package admin

import (
	"encoding/json"
	"net/http"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// NewAPIPluginHandler has no access to Sub's native PluginManager. Keeping
// separate handlers prevents a JS lifecycle action from changing a native
// installation, signature policy, runtime or UI session.
type NewAPIPluginHandler struct {
	newAPI      *service.NewAPIPluginService
	generations *service.GenerationService
}

func NewNewAPIPluginHandler(plugins *service.NewAPIPluginService, generations *service.GenerationService) *NewAPIPluginHandler {
	return &NewAPIPluginHandler{newAPI: plugins, generations: generations}
}

func (h *NewAPIPluginHandler) requireNewAPI(c *gin.Context) bool {
	if h == nil || h.newAPI == nil {
		response.Error(c, http.StatusServiceUnavailable, "New API plugin module is disabled")
		return false
	}
	return true
}

func (h *NewAPIPluginHandler) requireGenerations(c *gin.Context) bool {
	if h == nil || h.generations == nil {
		response.Error(c, http.StatusServiceUnavailable, "New API task module is disabled")
		return false
	}
	return true
}

func (h *NewAPIPluginHandler) ListNewAPI(c *gin.Context) {
	if !h.requireNewAPI(c) {
		return
	}
	items, err := h.newAPI.List(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	factories, err := service.PluginFactories()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"plugins": items, "builtins": builtin.Keys(), "factories": factories, "baseline": service.NewAPIPluginBaseline})
}

func (h *NewAPIPluginHandler) GenerationReview(c *gin.Context) {
	if !h.requireGenerations(c) {
		return
	}
	tasks, err := h.generations.ListReview(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items := make([]gin.H, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, gin.H{"id": task.ID, "model": task.Model, "account_id": task.AccountID, "user_id": task.UserID, "status": task.Status, "billing_status": task.BillingStatus, "hold_amount": task.Price.Hold, "upstream_id": task.UpstreamID, "error_code": task.ErrorCode, "created_at": task.CreatedAt})
	}
	response.Success(c, items)
}
func (h *NewAPIPluginHandler) ReconcileGeneration(c *gin.Context) {
	if !h.requireGenerations(c) {
		return
	}
	var input struct {
		Action     string           `json:"action"`
		UpstreamID string           `json:"upstream_id"`
		Reason     string           `json:"reason"`
		Amount     *decimal.Decimal `json:"amount"`
	}
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "对账参数无效")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Authentication required")
		return
	}
	if err := h.generations.Reconcile(c.Request.Context(), c.Param("task_id"), subject.UserID, input.Action, input.UpstreamID, input.Reason, input.Amount); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"reconciled": true})
}
func (h *NewAPIPluginHandler) NewAPIVersions(c *gin.Context) {
	if !h.requireNewAPI(c) {
		return
	}
	items, err := h.newAPI.Versions(c.Request.Context(), c.Param("key"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}
func (h *NewAPIPluginHandler) InstallNewAPI(c *gin.Context) {
	if !h.requireNewAPI(c) {
		return
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if middleware.RejectRequestBodyMemoryOverload(c, err) {
			return
		}
		response.BadRequest(c, "无法读取插件源码")
		return
	}
	// Account for JSON decoding, source and IPC representations before copying.
	if err = pkghttputil.ReserveRequestBodyMemory(c.Request.Context(), int64(len(body))*4); err != nil {
		middleware.RejectRequestBodyMemoryOverload(c, err)
		return
	}
	var input struct {
		Source               string                        `json:"source"`
		Builtin              string                        `json:"builtin"`
		AllowBuiltinOverride bool                          `json:"allow_builtin_override"`
		Marketplace          *service.PluginMarketplacePin `json:"marketplace"`
	}
	if json.Unmarshal(body, &input) != nil {
		response.BadRequest(c, "安装参数无效")
		return
	}
	var actor *int64
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		actor = &subject.UserID
	}
	var item *service.NewAPIPluginVersion
	if input.Marketplace != nil {
		if input.Builtin != "" {
			response.BadRequest(c, "市场源码与内置恢复不能同时选择")
			return
		}
		item, err = h.newAPI.InstallMarketplace(c.Request.Context(), input.Source, *input.Marketplace, actor, input.AllowBuiltinOverride)
	} else {
		item, err = h.newAPI.Install(c.Request.Context(), input.Source, input.Builtin, actor, service.PluginInstallOptions{AllowBuiltinOverride: input.AllowBuiltinOverride})
	}
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Created(c, item)
}

func (h *NewAPIPluginHandler) MarketplaceSources(c *gin.Context) {
	if !h.requireNewAPI(c) {
		return
	}
	items, err := h.newAPI.MarketplaceSources(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *NewAPIPluginHandler) SaveMarketplaceSources(c *gin.Context) {
	if !h.requireNewAPI(c) {
		return
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if !middleware.RejectRequestBodyMemoryOverload(c, err) {
			response.BadRequest(c, "无法读取市场来源")
		}
		return
	}
	if len(body) > 64<<10 {
		response.BadRequest(c, "市场来源配置最多 64 KiB")
		return
	}
	if err = pkghttputil.ReserveRequestBodyMemory(c.Request.Context(), int64(len(body))*4); err != nil {
		middleware.RejectRequestBodyMemoryOverload(c, err)
		return
	}
	var input struct {
		Sources []service.PluginMarketplaceSource `json:"sources"`
	}
	if json.Unmarshal(body, &input) != nil || input.Sources == nil {
		response.BadRequest(c, "市场来源参数无效")
		return
	}
	items, err := h.newAPI.SaveMarketplaceSources(c.Request.Context(), input.Sources)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, items)
}

func (h *NewAPIPluginHandler) RestoreFactory(c *gin.Context) {
	if !h.requireNewAPI(c) {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Authentication required")
		return
	}
	item, err := h.newAPI.RestoreFactory(c.Request.Context(), c.Param("key"), &subject.UserID)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, item)
}
func (h *NewAPIPluginHandler) ActivateNewAPI(c *gin.Context) {
	if !h.requireNewAPI(c) {
		return
	}
	id, ok := pluginIDParam(c)
	if !ok {
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Enabled == nil {
		response.BadRequest(c, "请选择启用或停用")
		return
	}
	if err := h.newAPI.Activate(c.Request.Context(), id, *input.Enabled); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"id": id, "enabled": *input.Enabled})
}
