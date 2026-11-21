package admin

import (
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/newapi/builtin"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func (h *PluginHandler) ListNewAPI(c *gin.Context) {
	items, err := h.newAPI.List(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"plugins": items, "builtins": builtin.Keys(), "baseline": "48f9df96ea7c68eae04a0f3687869f232f48929f"})
}

func (h *PluginHandler) GenerationReview(c *gin.Context) {
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
func (h *PluginHandler) ReconcileGeneration(c *gin.Context) {
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
func (h *PluginHandler) NewAPIVersions(c *gin.Context) {
	items, err := h.newAPI.Versions(c.Request.Context(), c.Param("key"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}
func (h *PluginHandler) InstallNewAPI(c *gin.Context) {
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
		Source  string `json:"source"`
		Builtin string `json:"builtin"`
	}
	if json.Unmarshal(body, &input) != nil {
		response.BadRequest(c, "安装参数无效")
		return
	}
	var actor *int64
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		actor = &subject.UserID
	}
	item, err := h.newAPI.Install(c.Request.Context(), input.Source, input.Builtin, actor)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Created(c, item)
}
func (h *PluginHandler) ActivateNewAPI(c *gin.Context) {
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
