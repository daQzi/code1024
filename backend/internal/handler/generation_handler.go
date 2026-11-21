package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	pluginTask "github.com/Wei-Shaw/sub2api/internal/newapi/task"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// TryPluginVideo handles the canonical protocol when a matching plugin account
// is configured. The native routes remain available for existing integrations.
func (h *OpenAIGatewayHandler) TryPluginVideo(c *gin.Context, platform string) bool {
	if h.generations == nil {
		return false
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key.Group == nil {
		return false
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if !middleware.RejectRequestBodyReadError(c, "handler.generation", err) {
			h.errorResponse(c, 400, "invalid_request_error", "Cannot read video request")
		}
		return true
	}
	requestmodel.ResetRequestBody(c.Request, body)
	idempotency := c.GetHeader("Idempotency-Key")
	if len(idempotency) > 200 || strings.TrimSpace(idempotency) != idempotency {
		h.errorResponse(c, 400, "invalid_request_error", "Idempotency-Key must contain at most 200 characters without surrounding whitespace")
		return true
	}
	ctx := service.WithNewAPIPluginRouting(service.WithOpenAIProfitControlSuppressed(c.Request.Context()))
	var jsonBody json.RawMessage
	prepare := func() bool {
		if jsonBody != nil {
			return true
		}
		if len(body) > 1<<20 {
			h.errorResponse(c, 413, "request_too_large", "Video requests must be at most 1 MiB; use image reference URLs for larger inputs")
			return false
		}
		if err = pkghttputil.ReserveRequestBodyMemory(ctx, int64(len(body))*12+1<<20); err != nil {
			middleware.RejectRequestBodyMemoryOverload(c, err)
			return false
		}
		jsonBody, err = pluginTask.NormalizeVideoRequest(c.GetHeader("Content-Type"), body)
		if err != nil {
			h.errorResponse(c, 400, "invalid_request_error", err.Error())
			return false
		}
		return true
	}
	if idempotency != "" {
		if !prepare() {
			return true
		}
		existing, replayErr := h.generations.Replay(ctx, key.UserID, key.ID, idempotency, jsonBody)
		if errors.Is(replayErr, service.ErrGenerationIdempotencyConflict) {
			h.errorResponse(c, 409, "idempotency_conflict", replayErr.Error())
			return true
		}
		if replayErr != nil {
			h.errorResponse(c, 503, "api_error", "Video task service is unavailable")
			return true
		}
		if existing != nil {
			c.Header("Location", "/v1/videos/"+existing.ID)
			c.Header("Cache-Control", "private, no-store")
			c.JSON(http.StatusOK, generationHostView(existing))
			return true
		}
	}
	model := requestmodel.FromBodyForRoute(c.FullPath(), c.GetHeader("Content-Type"), body)
	if model == "" {
		return false
	}
	selection, _, err := h.gatewayService.SelectAccountWithSchedulerForCapability(ctx, key.GroupID, "", "", model, nil, service.OpenAIUpstreamTransportHTTPSSE, service.OpenAIEndpointCapability("plugin_task"), false, false, false, platform)
	if errors.Is(err, service.ErrNoAvailableAccounts) {
		return false
	}
	if err != nil || selection == nil || selection.Account == nil {
		h.errorResponse(c, 503, "video_channel_unavailable", "Video channel is temporarily unavailable")
		return true
	}
	if selection.Acquired && selection.ReleaseFunc != nil {
		defer selection.ReleaseFunc()
	}
	// All copies, JSON normalization, and IPC snapshots share ingress admission.
	if !prepare() {
		return true
	}
	if !service.GroupAllowsImageGeneration(key.Group) {
		h.errorResponse(c, 403, "permission_error", service.ImageGenerationPermissionMessage())
		return true
	}
	subscription, _ := middleware.GetSubscriptionFromContext(c)
	if err = h.billingCacheService.CheckBillingEligibility(ctx, key.User, key, key.Group, subscription, service.QuotaPlatform(ctx, key)); err != nil {
		status, code, message, _ := billingErrorDetails(err)
		h.errorResponse(c, status, code, message)
		return true
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, 401, "authentication_error", "Invalid API key")
		return true
	}
	streamStarted := false
	log := requestLogger(c, "handler.generation")
	release, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, log)
	if !acquired {
		return true
	}
	if release != nil {
		defer release()
	}
	if decision := h.checkSecurityAudit(c, log, key, subject, service.ContentModerationProtocolOpenAIImages, model, jsonBody); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return true
	}
	task, err := h.generations.Create(ctx, key, subscription, selection.Account, model, jsonBody, idempotency)
	if err != nil {
		status := http.StatusBadRequest
		code := "invalid_video_request"
		message := "Video request is invalid or its pricing is not configured."
		if errors.Is(err, service.ErrGenerationIdempotencyConflict) {
			status = 409
			code = "idempotency_conflict"
			message = err.Error()
		}
		if errors.Is(err, service.ErrBatchImageInsufficientBalance) {
			status = 402
			code = "insufficient_balance"
			message = "Insufficient balance for the video reservation."
		}
		if errors.Is(err, service.ErrGenerationCapacity) {
			status, code, message = 429, "generation_capacity", "Active video task limit reached; retry after a task completes."
			c.Header("Retry-After", "10")
		}
		log.Warn("video_create_failed")
		h.errorResponse(c, status, code, message)
		return true
	}
	// Creation always returns a durable public identity, even if a later render
	// hook fails. A client can safely retry it using the same idempotency key.
	c.Header("Location", "/v1/videos/"+task.ID)
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, generationHostView(task))
	return true
}

func generationHostView(t *service.GenerationTask) map[string]any {
	return t.VideoView()
}

func (h *OpenAIGatewayHandler) GenerationVideoGet(c *gin.Context) {
	task, ok := h.ownedGeneration(c)
	if !ok {
		return
	}
	view, err := h.generations.Render(c.Request.Context(), task)
	if err != nil {
		view = generationHostView(task)
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, view)
}
func (h *OpenAIGatewayHandler) GenerationVideoList(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || h.generations == nil {
		h.errorResponse(c, 503, "api_error", "Video task service is unavailable")
		return
	}
	limit := 20
	if text := c.Query("limit"); text != "" {
		var err error
		limit, err = strconv.Atoi(text)
		if err != nil || limit < 1 || limit > 100 {
			h.errorResponse(c, 400, "invalid_request_error", "limit must be between 1 and 100")
			return
		}
	}
	if len(c.Query("after")) > 46 {
		h.errorResponse(c, 400, "invalid_request_error", "invalid video cursor")
		return
	}
	order := c.DefaultQuery("order", "desc")
	if order != "asc" && order != "desc" {
		h.errorResponse(c, 400, "invalid_request_error", "order must be asc or desc")
		return
	}
	tasks, err := h.generations.List(c.Request.Context(), key.UserID, key.ID, c.Query("after"), limit+1, order)
	if err != nil {
		h.errorResponse(c, 503, "api_error", "Cannot list video tasks")
		return
	}
	hasMore := len(tasks) > limit
	if hasMore {
		tasks = tasks[:limit]
	}
	items := make([]map[string]any, 0, len(tasks))
	for i := range tasks {
		items = append(items, generationHostView(&tasks[i]))
	}
	result := gin.H{"object": "list", "data": items, "has_more": hasMore}
	if len(tasks) > 0 {
		result["first_id"] = tasks[0].ID
		result["last_id"] = tasks[len(tasks)-1].ID
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(200, result)
}
func (h *OpenAIGatewayHandler) ownedGeneration(c *gin.Context) (*service.GenerationTask, bool) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || h.generations == nil {
		h.errorResponse(c, 503, "api_error", "Video task service is unavailable")
		return nil, false
	}
	task, err := h.generations.Get(c.Request.Context(), c.Param("request_id"), key.UserID, key.ID)
	if err != nil {
		if errors.Is(err, service.ErrGenerationTaskNotFound) {
			h.errorResponse(c, 404, "not_found_error", "Video not found")
		} else {
			h.errorResponse(c, 503, "api_error", "Video task service is unavailable")
		}
		return nil, false
	}
	return task, true
}
func (h *OpenAIGatewayHandler) GenerationVideoContent(c *gin.Context) {
	task, ok := h.ownedGeneration(c)
	if !ok {
		return
	}
	if task.Status != service.GenerationSucceeded || task.DeliveryStatus != service.GenerationDeliveryReady {
		h.errorResponse(c, 409, "video_not_ready", "Video content is not available")
		return
	}
	artifact := c.DefaultQuery("variant", "video")
	if artifact != "video" && artifact != "thumbnail" && artifact != "spritesheet" {
		h.errorResponse(c, 400, "invalid_request_error", "Unsupported content variant")
		return
	}
	resp, release, err := h.generations.Content(c.Request.Context(), task, c.Request.Method, artifact, c.Request.Header)
	if err != nil {
		h.errorResponse(c, 502, "upstream_error", "Video content is temporarily unavailable")
		return
	}
	defer release()
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		if value := resp.Header.Get("Content-Range"); len(value) <= 256 {
			c.Header("Content-Range", value)
		}
		h.errorResponse(c, 416, "range_not_satisfiable", "Requested video range is not available")
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		h.errorResponse(c, 502, "upstream_error", "Video content is temporarily unavailable")
		return
	}
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if value := resp.Header.Get(name); value != "" {
			c.Header(name, value)
		}
	}
	c.Header("Cache-Control", "private, no-store")
	c.Status(resp.StatusCode)
	if c.Request.Method == http.MethodHead {
		return
	}
	controller := http.NewResponseController(c.Writer)
	defer controller.SetWriteDeadline(time.Time{})
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, err = c.Writer.Write(buffer[:n]); err != nil {
				return
			}
		}
		if readErr != nil {
			return
		}
	}
}
