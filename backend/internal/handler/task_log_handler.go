package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type TaskLogHandler struct{ generations *service.GenerationService }

func NewTaskLogHandler(generations *service.GenerationService) *TaskLogHandler {
	return &TaskLogHandler{generations: generations}
}

func (h *TaskLogHandler) List(c *gin.Context)         { h.list(c, false) }
func (h *TaskLogHandler) AdminList(c *gin.Context)    { h.list(c, true) }
func (h *TaskLogHandler) Get(c *gin.Context)          { h.get(c, false) }
func (h *TaskLogHandler) AdminGet(c *gin.Context)     { h.get(c, true) }
func (h *TaskLogHandler) Content(c *gin.Context)      { h.content(c, false) }
func (h *TaskLogHandler) AdminContent(c *gin.Context) { h.content(c, true) }

func (h *TaskLogHandler) requireGenerations(c *gin.Context) bool {
	if h == nil || h.generations == nil {
		response.Error(c, http.StatusServiceUnavailable, "Task log module is unavailable")
		return false
	}
	return true
}

func (h *TaskLogHandler) AdminMaintainNative(c *gin.Context) {
	if !h.requireGenerations(c) {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return
	}
	var input struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "Invalid native video review action")
		return
	}
	middleware.SetAuditAction(c, "admin.native_video.maintenance")
	if err := h.generations.MaintainNativeVideo(c.Request.Context(), c.Param("task_id"), subject.UserID, input.Action, input.Reason); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"updated": true})
}

func (h *TaskLogHandler) list(c *gin.Context, admin bool) {
	if !h.requireGenerations(c) {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return
	}
	page, size := response.ParsePagination(c)
	f := service.TaskLogFilter{Page: page, PageSize: min(size, 100), TaskID: c.Query("task_id"), Model: c.Query("model"), PluginKey: c.Query("plugin"), Status: c.Query("status"), BillingStatus: c.Query("billing_status"), ReviewOnly: c.Query("review") == "true"}
	if f.Page > 100000 || len(f.TaskID) > 512 || len(f.Model) > 256 || len(f.PluginKey) > 256 || len(f.Status) > 32 || len(f.BillingStatus) > 32 {
		response.BadRequest(c, "Invalid task log filter")
		return
	}
	for key, dest := range map[string]*int64{"group_id": &f.GroupID, "account_id": &f.AccountID, "user_id": &f.UserID} {
		if raw := c.Query(key); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value <= 0 {
				response.BadRequest(c, "Invalid task log filter")
				return
			}
			*dest = value
		}
	}
	if !admin || c.Query("scope") == "mine" {
		f.UserID = subject.UserID
	}
	if !admin {
		f.AccountID = 0
	}
	for key, dest := range map[string]**time.Time{"start": &f.Start, "end": &f.End} {
		if raw := c.Query(key); raw != "" {
			value, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				response.BadRequest(c, "Invalid task log time range")
				return
			}
			*dest = &value
		}
	}
	if f.Start != nil && f.End != nil && !f.End.After(*f.Start) {
		response.BadRequest(c, "Invalid task log time range")
		return
	}
	c.Header("Cache-Control", "private, no-store")
	items, total, err := h.generations.TaskLogs(c.Request.Context(), f, admin)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, f.Page, f.PageSize)
}

func (h *TaskLogHandler) get(c *gin.Context, admin bool) {
	if !h.requireGenerations(c) {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return
	}
	owner := subject.UserID
	if admin {
		owner = 0
	}
	c.Header("Cache-Control", "private, no-store")
	item, err := h.generations.TaskLog(c.Request.Context(), c.Param("task_id"), owner, admin)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *TaskLogHandler) content(c *gin.Context, admin bool) {
	if !h.requireGenerations(c) {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return
	}
	owner := subject.UserID
	if admin {
		owner = 0
	}
	resp, release, err := h.generations.TaskLogContent(c.Request.Context(), c.Param("task_id"), owner, admin, c.Request.Header)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer release()
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		c.Header("Content-Range", resp.Header.Get("Content-Range"))
		response.Error(c, http.StatusRequestedRangeNotSatisfiable, "Requested task content range is not available")
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		response.Error(c, http.StatusBadGateway, "Task content is temporarily unavailable")
		return
	}
	c.Header("Content-Disposition", "attachment; filename=video.mp4")
	streamGenerationContent(c, resp)
}
