package handler

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// TryPluginVideo dispatches the canonical create endpoint and the existing xAI
// generation alias. Unbound accounts retain their original native handler.
func (h *OpenAIGatewayHandler) TryPluginVideo(c *gin.Context, platform string) bool {
	protocol := service.GenerationProtocolOpenAI
	if strings.HasSuffix(c.Request.URL.Path, "/videos/generations") {
		protocol = service.GenerationProtocolXAI
	}
	return h.TryPluginNativeVideo(c, platform, protocol)
}

// TryPluginNativeVideo shares admission, idempotency and the durable executor
// with canonical Videos. Only a matching, explicitly bound plugin can claim it.
func (h *OpenAIGatewayHandler) TryPluginNativeVideo(c *gin.Context, platform, protocol string) bool {
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
	ctx := service.WithNativeVideoPluginRouting(service.WithOpenAIProfitControlSuppressed(c.Request.Context()), protocol)
	ctx = service.WithGenerationRequestHeaders(ctx, c.Request.Header)
	taskSubmit := service.GenerationTaskSubmitPlugin(ctx) != ""
	if kind, _, _ := mime.ParseMediaType(c.GetHeader("Content-Type")); kind == "multipart/form-data" {
		ctx = service.WithGenerationMultipartInput(ctx)
	}
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
	if idempotency != "" && !taskSubmit {
		if !prepare() {
			return true
		}
		existing, replayErr := h.generations.ReplayVideo(ctx, key.UserID, key.ID, idempotency, jsonBody, protocol)
		if errors.Is(replayErr, service.ErrGenerationIdempotencyConflict) {
			h.errorResponse(c, 409, "idempotency_conflict", replayErr.Error())
			return true
		}
		if replayErr != nil {
			h.errorResponse(c, 503, "api_error", "Video task service is unavailable")
			return true
		}
		if existing != nil {
			writeGenerationCreated(c, existing)
			return true
		}
	}
	model := requestmodel.FromBodyForRoute(c.FullPath(), c.GetHeader("Content-Type"), body)
	if taskSubmit {
		model, err = service.GenerationTaskSubmitModel(body)
		if err != nil {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid task request model")
			return true
		}
	}
	if model == "" {
		if service.HasGenerationMultipartFile(ctx) {
			h.errorResponse(c, 400, "invalid_video_request", "A model is required for video uploads")
			return true
		}
		return false
	}
	var routingNeeded bool
	ctx, routingNeeded, err = h.generations.VideoPluginRoutingNeeded(ctx, key, platform, model, protocol)
	if err == nil && routingNeeded {
		if !prepare() {
			return true
		}
		ctx, err = h.generations.PrepareVideoRouting(ctx, key, platform, model, jsonBody, protocol)
	}
	if err != nil {
		if middleware.RejectRequestBodyMemoryOverload(c, err) {
			return true
		}
		if errors.Is(err, service.ErrGenerationCapacity) {
			c.Header("Retry-After", "1")
			h.errorResponse(c, 429, "generation_capacity", "Task protocol admission is temporarily at capacity")
		} else if errors.Is(err, service.ErrGenerationFileQuota) || errors.Is(err, service.ErrGenerationFileBusy) || errors.Is(err, service.ErrGenerationFileNotFound) || errors.Is(err, service.ErrGenerationFileTooLarge) {
			h.generationFileError(c, err)
		} else if errors.Is(err, service.ErrGenerationDecode) || errors.Is(err, service.ErrGenerationOrigin) {
			h.errorResponse(c, 400, "invalid_task_intent", "The task request or its origin connection is unavailable")
		} else {
			h.errorResponse(c, 503, "api_error", "Task protocol admission is temporarily unavailable")
		}
		return true
	}
	if taskSubmit && !service.GenerationVideoRoutingPrepared(ctx) {
		return false
	}
	capability := service.GenerationTaskCapabilityForProtocol(protocol)
	selection, _, err := h.gatewayService.SelectAccountWithSchedulerForCapability(ctx, key.GroupID, "", "", model, nil, service.OpenAIUpstreamTransportHTTPSSE, capability, false, false, false, platform)
	if errors.Is(err, service.ErrNoAvailableAccounts) {
		if service.HasGenerationMultipartFile(ctx) || service.GenerationVideoRoutingPrepared(ctx) {
			h.errorResponse(c, 503, "video_channel_unavailable", "No compatible plugin channel is available for this task")
			return true
		}
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
	auditBody := jsonBody
	if protocol == service.GenerationProtocolSunoTask {
		// The Suno decoder accepts text in input or metadata; the media
		// audit's existing prompt collector must see all of those fields.
		var texts []string
		for _, field := range []string{"input", "prompt", "metadata.prompt", "metadata.gpt_description_prompt", "metadata.title", "metadata.tags"} {
			if value := gjson.GetBytes(jsonBody, field); value.Type == gjson.String && value.Str != "" {
				texts = append(texts, value.Str)
			}
		}
		auditBody, _ = json.Marshal(map[string]string{"prompt": strings.Join(texts, "\n")})
	}
	if decision := h.checkSecurityAudit(c, log, key, subject, service.ContentModerationProtocolOpenAIImages, model, auditBody); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return true
	}
	task, err := h.generations.CreateVideo(ctx, key, subscription, selection.Account, model, jsonBody, idempotency, protocol)
	if err != nil {
		var policy *service.RequestPluginFailure
		if errors.As(err, &policy) {
			status, code := http.StatusServiceUnavailable, "request_processing_failed"
			if policy.Rejected {
				status, code = http.StatusForbidden, "request_processing_rejected"
			}
			if policy.Class == "capacity" {
				c.Header("Retry-After", "1")
			}
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
			requestPluginAbort(c, status, code, policy.Error())
			return true
		}
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
		if errors.Is(err, service.ErrGenerationFileQuota) || errors.Is(err, service.ErrGenerationFileBusy) || errors.Is(err, service.ErrGenerationFileNotFound) || errors.Is(err, service.ErrGenerationFileTooLarge) {
			h.generationFileError(c, err)
			return true
		}
		log.Warn("video_create_failed")
		h.errorResponse(c, status, code, message)
		return true
	}
	// Creation always returns a durable public identity, even if a later render
	// hook fails. A client can safely retry it using the same idempotency key.
	writeGenerationCreated(c, task)
	return true
}

func writeGenerationCreated(c *gin.Context, task *service.GenerationTask) {
	if service.GenerationTaskSubmitPlugin(c.Request.Context()) != "" {
		c.Header("Location", "/v1/tasks/"+task.ID)
		c.Header("Cache-Control", "private, no-store")
		c.JSON(http.StatusOK, gin.H{"id": task.ID, "task_id": task.ID, "status": task.VideoView()["status"], "model": task.Model, "created_at": task.CreatedAt.Unix()})
		return
	}
	location := "/v1/videos/" + task.ID
	if task.Protocol == service.GenerationProtocolArk {
		// Registered Ark aliases share this canonical query address.
		location = "/api/v3/contents/generations/tasks/" + task.ID
	}
	c.Header("Location", location)
	c.Header("Cache-Control", "private, no-store")
	if task.Protocol != "" && task.Protocol != service.GenerationProtocolOpenAI {
		status, view := task.NativeVideoView(true)
		c.JSON(status, view)
		return
	}
	c.JSON(http.StatusOK, generationHostView(task))
}

func generationHostView(t *service.GenerationTask) map[string]any {
	return t.VideoView()
}

func (h *OpenAIGatewayHandler) GenerationVideoGet(c *gin.Context) {
	task, ok := h.ownedGeneration(c)
	if !ok {
		return
	}
	if !service.IsVideoGenerationProtocol(task.Protocol) {
		h.errorResponse(c, 404, "not_found_error", "Video not found")
		return
	}
	if task.Protocol != "" && task.Protocol != service.GenerationProtocolOpenAI {
		status, view := task.NativeVideoView(false)
		c.Header("Cache-Control", "private, no-store")
		c.JSON(status, view)
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
	id := c.Param("request_id")
	if id == "" {
		id = c.Param("task_id")
	}
	task, err := h.generations.Get(c.Request.Context(), id, key.UserID, key.ID)
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
	if !service.IsVideoGenerationProtocol(task.Protocol) {
		h.errorResponse(c, 404, "not_found_error", "Video not found")
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
	h.writeGenerationContent(c, task, artifact, false)
}

func (h *OpenAIGatewayHandler) writeGenerationContent(c *gin.Context, task *service.GenerationTask, artifact string, declaredOnly bool) {
	open := h.generations.Content
	if declaredOnly {
		open = h.generations.TaskArtifactContent
	}
	resp, release, err := open(c.Request.Context(), task, c.Request.Method, artifact, c.Request.Header)
	if err != nil {
		if errors.Is(err, service.ErrGenerationArtifactNotFound) {
			h.errorResponse(c, 404, "artifact_not_found", "Task or artifact not found")
			return
		}
		if errors.Is(err, service.ErrGenerationArtifactBusy) {
			c.Header("Retry-After", "5")
			h.errorResponse(c, 503, "video_content_busy", "Video content capacity is temporarily full; retry later")
			return
		}
		if middleware.RejectRequestBodyMemoryOverload(c, err) {
			return
		}
		if errors.Is(err, pluginTask.ErrEmbeddedArtifactNotReady) {
			h.errorResponse(c, 409, "artifact_not_ready", "Task artifacts are not ready")
			return
		}
		if errors.Is(err, pluginTask.ErrEmbeddedArtifactGone) {
			h.errorResponse(c, 410, "artifact_gone", "Artifact content is no longer available")
			return
		}
		h.errorResponse(c, 502, "upstream_error", "Video content is temporarily unavailable")
		return
	}
	defer release()
	defer func() { _ = resp.Body.Close() }()
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
	streamGenerationContent(c, resp)
}

func streamGenerationContent(c *gin.Context, resp *http.Response) {
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if value := resp.Header.Get(name); value != "" {
			c.Header(name, value)
		}
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("Content-Security-Policy", "sandbox; default-src 'none'")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(resp.StatusCode)
	if c.Request.Method == http.MethodHead {
		return
	}
	controller := http.NewResponseController(c.Writer)
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, err := c.Writer.Write(buffer[:n]); err != nil {
				return
			}
		}
		if readErr != nil {
			return
		}
	}
}
