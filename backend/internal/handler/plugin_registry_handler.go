package handler

import (
	"net/http"
	"strconv"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type PluginRegistryHandler struct{ plugins *service.NewAPIPluginService }

func NewPluginRegistryHandler(plugins *service.NewAPIPluginService) *PluginRegistryHandler {
	return &PluginRegistryHandler{plugins: plugins}
}

func pluginRegistryActor(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return 0, false
	}
	c.Header("Cache-Control", "private, no-store")
	return subject.UserID, true
}
func pluginSubmissionID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid plugin submission ID")
		return 0, false
	}
	return id, true
}

func (h *PluginRegistryHandler) Mine(c *gin.Context)      { h.list(c, "mine") }
func (h *PluginRegistryHandler) Market(c *gin.Context)    { h.list(c, "market") }
func (h *PluginRegistryHandler) AdminList(c *gin.Context) { h.list(c, "review") }

func (h *PluginRegistryHandler) Templates(c *gin.Context) {
	actor, ok := pluginRegistryActor(c)
	if !ok {
		return
	}
	items, err := service.PluginDeveloperTemplates(actor)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *PluginRegistryHandler) requirePlugins(c *gin.Context) bool {
	if h == nil || h.plugins == nil {
		response.Error(c, http.StatusServiceUnavailable, "New API plugin registry module is disabled")
		return false
	}
	return true
}

func (h *PluginRegistryHandler) DryRun(c *gin.Context) {
	if !h.requirePlugins(c) {
		return
	}
	if _, ok := pluginRegistryActor(c); !ok {
		return
	}
	// Reserve decoded source, fixture copies, IPC and result before binding.
	if err := pkghttputil.ReserveRequestBodyMemory(c.Request.Context(), 8<<20); err != nil {
		middleware.RejectRequestBodyMemoryOverload(c, err)
		return
	}
	var input service.PluginPlaygroundInput
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "Invalid offline plugin fixture")
		return
	}
	result, err := h.plugins.DryRunPlugin(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
func (h *PluginRegistryHandler) list(c *gin.Context, scope string) {
	if !h.requirePlugins(c) {
		return
	}
	actor, ok := pluginRegistryActor(c)
	if !ok {
		return
	}
	page, size := response.ParsePagination(c)
	filter := service.PluginRegistryFilter{Page: page, PageSize: min(size, 100)}
	switch scope {
	case "mine":
		filter.Owner = actor
	case "market":
		filter.Status = "published"
	default:
		filter.Status = c.DefaultQuery("status", "submitted")
		if filter.Status != "submitted" && filter.Status != "published" && filter.Status != "withdrawn" && filter.Status != "rejected" {
			response.BadRequest(c, "Invalid review status")
			return
		}
	}
	items, total, err := h.plugins.ListSubmissions(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": filter.PageSize, "pages": (total + int64(filter.PageSize) - 1) / int64(filter.PageSize), "namespace": service.PluginAuthorNamespace(actor)})
}

func (h *PluginRegistryHandler) Get(c *gin.Context)      { h.get(c, false) }
func (h *PluginRegistryHandler) AdminGet(c *gin.Context) { h.get(c, true) }
func (h *PluginRegistryHandler) get(c *gin.Context, admin bool) {
	if !h.requirePlugins(c) {
		return
	}
	actor, ok := pluginRegistryActor(c)
	if !ok {
		return
	}
	id, ok := pluginSubmissionID(c)
	if !ok {
		return
	}
	item, err := h.plugins.Submission(c.Request.Context(), id, actor, admin)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *PluginRegistryHandler) Save(c *gin.Context) {
	if !h.requirePlugins(c) {
		return
	}
	actor, ok := pluginRegistryActor(c)
	if !ok {
		return
	}
	var input struct {
		ID       int64  `json:"id"`
		Revision int64  `json:"revision"`
		Source   string `json:"source"`
		Readme   string `json:"readme"`
	}
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "Invalid plugin draft")
		return
	}
	if err := pkghttputil.ReserveRequestBodyMemory(c.Request.Context(), int64(len(input.Source))*4); err != nil {
		middleware.RejectRequestBodyMemoryOverload(c, err)
		return
	}
	item, err := h.plugins.SaveSubmission(c.Request.Context(), actor, input.ID, input.Revision, input.Source, input.Readme)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, item)
}

func (h *PluginRegistryHandler) Transition(c *gin.Context)      { h.transition(c, false) }
func (h *PluginRegistryHandler) AdminTransition(c *gin.Context) { h.transition(c, true) }
func (h *PluginRegistryHandler) transition(c *gin.Context, admin bool) {
	if !h.requirePlugins(c) {
		return
	}
	actor, ok := pluginRegistryActor(c)
	if !ok {
		return
	}
	id, ok := pluginSubmissionID(c)
	if !ok {
		return
	}
	var input struct {
		Revision int64  `json:"revision"`
		Action   string `json:"action"`
		Reason   string `json:"reason"`
	}
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "Invalid plugin transition")
		return
	}
	if !admin && input.Action != "submit" && input.Action != "withdraw" {
		response.Error(c, http.StatusForbidden, "Review requires an administrator")
		return
	}
	if err := h.plugins.TransitionSubmission(c.Request.Context(), id, actor, input.Revision, input.Action, input.Reason, admin); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"updated": true})
}

func (h *PluginRegistryHandler) Install(c *gin.Context) {
	if !h.requirePlugins(c) {
		return
	}
	actor, ok := pluginRegistryActor(c)
	if !ok {
		return
	}
	id, ok := pluginSubmissionID(c)
	if !ok {
		return
	}
	var input struct {
		SHA256 string `json:"sha256"`
	}
	if c.ShouldBindJSON(&input) != nil || len(input.SHA256) != 64 {
		response.BadRequest(c, "Exact source SHA256 is required")
		return
	}
	version, err := h.plugins.InstallSubmission(c.Request.Context(), id, actor, input.SHA256)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, version)
}
