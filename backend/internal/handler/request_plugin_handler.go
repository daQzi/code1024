package handler

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type RequestPluginHandler struct {
	plugins     *service.NewAPIPluginService
	generations *service.GenerationService
}

func NewRequestPluginHandler(plugins *service.NewAPIPluginService, generations *service.GenerationService) *RequestPluginHandler {
	return &RequestPluginHandler{plugins: plugins, generations: generations}
}

func (h *RequestPluginHandler) requirePlugins(c *gin.Context) bool {
	if h == nil || h.plugins == nil {
		response.Error(c, http.StatusServiceUnavailable, "New API request processing module is disabled")
		return false
	}
	return true
}

func (h *RequestPluginHandler) List(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	if !h.requirePlugins(c) {
		return
	}
	items, err := h.plugins.RequestPluginInstances(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "stages": []string{"ingress", "request", "outbound", "egress", "reply", "report"}, "stage_formats": gin.H{"ingress": []string{"openai", "openai_responses", "claude", "gemini", "task"}, "request": []string{"openai", "openai_responses", "claude", "gemini"}, "outbound": []string{"openai", "openai_responses", "claude", "gemini"}, "egress": []string{"task", "openai", "openai_responses", "claude", "gemini"}, "reply": []string{"openai", "openai_responses", "claude", "gemini"}, "report": []string{"task", "openai", "openai_responses", "claude", "gemini"}}, "attempt_scope": "provider_account", "formats": []string{"openai", "openai_responses", "claude", "gemini", "task"}, "max_body_bytes": service.MaxRequestPluginBody, "network": true, "network_stages": []string{"ingress", "egress"}, "network_opt_in": true, "dry_run_network": false, "reply_network": true, "reply_network_opt_in": true, "report_network": true, "report_stats": h.plugins.TaskReportStats(), "reply_audit_failures": h.plugins.ReplyAuditFailures()})
}
func (h *RequestPluginHandler) Save(c *gin.Context) {
	if !h.requirePlugins(c) {
		return
	}
	actor, ok := pluginRegistryActor(c)
	if !ok {
		return
	}
	var input service.RequestPluginInstance
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "Invalid request processing instance")
		return
	}
	item, err := h.plugins.SaveRequestPluginInstance(c.Request.Context(), input, actor)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}
func (h *RequestPluginHandler) Config(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	if !h.requirePlugins(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid instance ID")
		return
	}
	config, err := h.plugins.RequestPluginConfig(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"config": config})
}
func (h *RequestPluginHandler) Runs(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	if !h.requirePlugins(c) {
		return
	}
	page, size := response.ParsePagination(c)
	items, total, err := h.plugins.RequestPluginRuns(c.Request.Context(), c.Query("request_id"), page, min(size, 100))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": min(size, 100)})
}
func (h *RequestPluginHandler) DryRun(c *gin.Context) {
	if !h.requirePlugins(c) {
		return
	}
	actor, ok := pluginRegistryActor(c)
	if !ok {
		return
	}
	var input struct {
		VersionID int64           `json:"plugin_version_id"`
		Config    json.RawMessage `json:"config"`
		Body      json.RawMessage `json:"body"`
		Format    string          `json:"format"`
		Stage     string          `json:"stage"`
	}
	if c.ShouldBindJSON(&input) != nil || len(input.Body) > service.MaxRequestPluginBody {
		response.BadRequest(c, "Invalid dry run input")
		return
	}
	switch input.Format {
	case "openai", "openai_responses", "claude", "gemini", "task":
	default:
		response.BadRequest(c, "Unsupported dry run format")
		return
	}
	call := service.RequestPluginCall{RequestID: uuid.NewString(), UserID: actor, Model: requestmodel.FromJSON(input.Body), Format: input.Format, Path: "/dry-run"}
	if input.Stage == "" {
		input.Stage = "ingress"
	}
	out, err := h.plugins.DryRunRequestPluginStage(c.Request.Context(), input.VersionID, input.Config, input.Body, call, input.Stage)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err = h.plugins.RecordRequestPluginRun(c.Request.Context(), call, out, true); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

// Ingress runs after auth and before allowlists, routing, moderation and
// billing. It changes the original body, so all existing consumers see the
// same transformed bytes. There is one invocation per HTTP request, not retry.
func (h *RequestPluginHandler) Ingress(c *gin.Context) {
	if h != nil && h.plugins != nil {
		if err := h.plugins.EnsureModelModifierPolicy(c.Request.Context()); err != nil {
			requestPluginAbort(c, http.StatusServiceUnavailable, "model_policy_unavailable", "Model name policy is unavailable")
			return
		}
	}
	format := requestPluginIngressFormat(c)
	if h == nil || h.plugins == nil || format == "" {
		c.Next()
		return
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.GroupID == nil {
		c.Next()
		return
	}
	ctx := c.Request.Context()
	applicable, err := h.plugins.RequestPluginsMayApply(ctx, *key.GroupID)
	if err != nil {
		requestPluginAbort(c, http.StatusServiceUnavailable, "request_processing_unavailable", "Request-processing policy is unavailable")
		return
	}
	if !applicable {
		c.Request = c.Request.WithContext(service.WithRequestPluginPlan(ctx, nil))
		c.Next()
		return
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if !middleware.RejectRequestBodyReadError(c, "handler.request_plugin", err) {
			middleware.AbortWithRequestBodyReadFailure(c, "handler.request_plugin", err)
		}
		return
	}
	requestmodel.ResetRequestBody(c.Request, body)
	model := requestmodel.FromBodyForRoute(c.FullPath(), c.GetHeader("Content-Type"), body)
	if service.GenerationTaskSubmitPlugin(ctx) != "" {
		model, _ = service.GenerationTaskSubmitModel(body) // Pinned JSON ingress has already validated the shape.
	}
	if value := c.Param("modelAction"); value != "" {
		model = strings.TrimPrefix(value, "/")
		if colon := strings.LastIndex(model, ":"); colon >= 0 {
			model = model[:colon]
		}
	}
	call := service.RequestPluginCall{RequestID: uuid.NewString(), UserID: key.UserID, TokenID: key.ID, TokenName: key.Name, GroupID: *key.GroupID, Group: strconv.FormatInt(*key.GroupID, 10), Model: model, Format: format, Path: c.Request.URL.Path}
	call.ModelCandidates = requestmodel.FromBodyCandidates(c.FullPath(), c.GetHeader("Content-Type"), body)
	if strings.HasSuffix(c.FullPath(), "/images/generations") || strings.HasSuffix(c.FullPath(), "/images/edits") {
		// The image handler reads top-level model and supplies its existing
		// default. A stray session.model must not hide that model's policies.
		contentType, _, _ := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if contentType != "multipart/form-data" {
			call.Model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
		}
		if call.Model == "" {
			call.Model = service.DefaultOpenAIImagesModel
		}
		call.ModelCandidates = append(call.ModelCandidates, call.Model)
	}
	if defaultModel := requestPluginReportOnlyDefaultModel(c.FullPath()); defaultModel != "" && call.Model == "" {
		call.Model = defaultModel
		call.ModelCandidates = append(call.ModelCandidates, defaultModel)
	}
	if format == "gemini" {
		call.ModelCandidates = []string{model}
	}
	if id, ok := ctx.Value(ctxkey.ClientRequestID).(string); ok && id != "" {
		call.RequestID = id
	}
	plan, err := h.plugins.NewRequestPluginPlan(ctx, call)
	if err != nil {
		requestPluginAbort(c, http.StatusServiceUnavailable, "request_processing_unavailable", "Request-processing policy is unavailable")
		return
	}
	ctx = service.WithRequestPluginPlan(ctx, plan)
	c.Request = c.Request.WithContext(ctx)
	if plan == nil {
		c.Next()
		return
	}
	username := ""
	if key.User != nil {
		username = key.User.Username
	}
	var report *service.RequestPluginHTTPReport
	if report = h.plugins.BeginRequestPluginHTTPReport(ctx, c.Writer, username); report != nil {
		originalWriter := c.Writer
		c.Writer = report
		defer func() { c.Writer = originalWriter; report.Finish(body) }()
	}
	contentType, _, _ := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if isRequestPluginReportOnlyRoute(c.FullPath()) {
		if report != nil && contentType == "multipart/form-data" {
			if mpForm, mpErr := service.ParseRequestPluginMultipart(c.GetHeader("Content-Type"), body); mpErr == nil {
				report.SetMultipart(mpForm)
			}
		}
		c.Next()
		return
	}
	isMultipartImagesEdits := strings.HasSuffix(c.FullPath(), "/images/edits") && contentType == "multipart/form-data"
	var mpForm *service.RequestPluginMultipart
	var mpErr error
	if isMultipartImagesEdits && (report != nil || plan.HasEarlyIngress()) {
		mpForm, mpErr = service.ParseRequestPluginMultipart(c.GetHeader("Content-Type"), body)
		if report != nil && mpErr == nil {
			report.SetMultipart(mpForm)
		}
	}
	if !plan.HasEarlyIngress() {
		// Egress-only rules see the JS-built Task JSON in the durable worker;
		// they do not impose ingress's JSON/attachment restrictions here.
		c.Next()
		return
	}
	if isMultipartImagesEdits {
		if mpErr != nil {
			if errors.Is(mpErr, service.ErrRequestPluginMultipartTooLarge) {
				requestPluginAbort(c, http.StatusRequestEntityTooLarge, "request_processing_body_too_large", "Request-processing body exceeds 1 MiB")
				return
			}
			requestPluginAbort(c, http.StatusBadRequest, "invalid_request_error", "Invalid multipart images request")
			return
		}
		if err = pkghttputil.ReserveRequestBodyMemory(ctx, int64(len(mpForm.View))*6+(8<<20)); err != nil {
			middleware.RejectRequestBodyMemoryOverload(c, err)
			return
		}
		mpCtx := service.WithRequestPluginMultipart(ctx, mpForm)
		out, runErr := plan.Ingress(mpCtx, mpForm.View)
		auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		err = h.plugins.RecordRequestPluginRun(auditCtx, call, out, false)
		cancel()
		if err != nil {
			logger.LegacyPrintf("handler.request_plugin", "unable to persist request processing audit")
			requestPluginAbort(c, http.StatusServiceUnavailable, "request_processing_audit_unavailable", "Request-processing audit is unavailable")
			return
		}
		if runErr != nil {
			var failure *service.RequestPluginFailure
			if errors.As(runErr, &failure) {
				if failure.Class == "capacity" {
					c.Header("Retry-After", "1")
				}
				if failure.Rejected {
					service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
					requestPluginAbort(c, http.StatusForbidden, "request_processing_rejected", failure.Error())
					return
				}
			}
			requestPluginAbort(c, http.StatusServiceUnavailable, "request_processing_failed", "Request-processing policy could not be applied")
			return
		}
		rebuilt, rebuildErr := mpForm.Rebuild(out.Body)
		if rebuildErr != nil {
			requestPluginAbort(c, http.StatusServiceUnavailable, "request_processing_failed", "Request-processing policy could not be applied")
			return
		}
		requestmodel.ResetRequestBody(c.Request, rebuilt)
		body = rebuilt
		if report != nil {
			report.SetRequestProjection(out.Body, mpForm.ReportMedia())
		}
		c.Next()
		return
	}
	if len(body) > service.MaxRequestPluginBody {
		requestPluginAbort(c, http.StatusRequestEntityTooLarge, "request_processing_body_too_large", "Request-processing body exceeds 1 MiB")
		return
	}
	protocol, video := service.GenerationVideoCreateProtocol(c.Request.URL.Path)
	if service.GenerationTaskSubmitPlugin(ctx) != "" {
		protocol, video = service.GenerationTaskSubmitProtocol(ctx), true
	}
	if video {
		original, normalizeErr := pluginTask.NormalizeVideoRequest(c.GetHeader("Content-Type"), body)
		if normalizeErr != nil {
			requestPluginAbort(c, http.StatusBadRequest, "invalid_video_request", "Invalid video request")
			return
		}
		ctx, err = service.WithGenerationClientRequest(ctx, original)
		if err != nil {
			requestPluginAbort(c, http.StatusBadRequest, "invalid_video_request", "Invalid video request")
			return
		}
		c.Request = c.Request.WithContext(ctx)
		idempotency := c.GetHeader("Idempotency-Key")
		if len(idempotency) > 200 || strings.TrimSpace(idempotency) != idempotency {
			requestPluginAbort(c, http.StatusBadRequest, "invalid_request_error", "Invalid Idempotency-Key")
			return
		}
		if idempotency != "" && h.generations != nil && service.GenerationTaskSubmitPlugin(ctx) == "" {
			existing, replayErr := h.generations.ReplayVideo(ctx, key.UserID, key.ID, idempotency, original, protocol)
			if errors.Is(replayErr, service.ErrGenerationIdempotencyConflict) {
				requestPluginAbort(c, http.StatusConflict, "idempotency_conflict", replayErr.Error())
				return
			}
			if replayErr != nil {
				requestPluginAbort(c, http.StatusServiceUnavailable, "video_service_unavailable", "Video task service is unavailable")
				return
			}
			if existing != nil {
				writeGenerationCreated(c, existing)
				c.Abort()
				return
			}
		}
		if h.generations != nil {
			ctx, err = h.generations.PrepareVideoPolicyFiles(ctx, key, original, protocol)
			if err != nil {
				status := http.StatusBadRequest
				if errors.Is(err, service.ErrGenerationFileNotFound) {
					status = http.StatusNotFound
				}
				requestPluginAbort(c, status, "video_file_unavailable", "Video file references are invalid or unavailable")
				return
			}
			c.Request = c.Request.WithContext(ctx)
		}
	}
	if contentType != "" && contentType != "application/json" {
		requestPluginAbort(c, http.StatusUnsupportedMediaType, "request_processing_body_unsupported", "Enabled request-processing policy currently requires a JSON body without file attachments")
		return
	}
	if err = pkghttputil.ReserveRequestBodyMemory(ctx, int64(len(body))*6+(8<<20)); err != nil {
		middleware.RejectRequestBodyMemoryOverload(c, err)
		return
	}
	out, runErr := plan.Ingress(ctx, body)
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	err = h.plugins.RecordRequestPluginRun(auditCtx, call, out, false)
	cancel()
	if err != nil {
		logger.LegacyPrintf("handler.request_plugin", "unable to persist request processing audit")
		requestPluginAbort(c, http.StatusServiceUnavailable, "request_processing_audit_unavailable", "Request-processing audit is unavailable")
		return
	}
	if runErr != nil {
		var failure *service.RequestPluginFailure
		if errors.As(runErr, &failure) {
			if failure.Class == "capacity" {
				c.Header("Retry-After", "1")
			}
			if failure.Rejected {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
				requestPluginAbort(c, http.StatusForbidden, "request_processing_rejected", failure.Error())
				return
			}
		}
		requestPluginAbort(c, http.StatusServiceUnavailable, "request_processing_failed", "Request-processing policy could not be applied")
		return
	}
	requestmodel.ResetRequestBody(c.Request, out.Body)
	body = out.Body
	c.Next()
}

func requestPluginAbort(c *gin.Context, status int, code, message string) {
	// These failures happen before an upstream model is called and must not
	// turn into provider failure samples in the model plaza.
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	// Reuse the allowlist's protocol selection, including Gemini aliases.
	path := c.FullPath()
	switch {
	case strings.Contains(path, "/v1beta/"):
		middleware.GoogleErrorWriter(c, status, message)
	case strings.HasSuffix(path, "/messages"):
		middleware.AnthropicErrorWriter(c, status, message)
	default:
		c.JSON(status, gin.H{"error": gin.H{"type": "request_processing_error", "code": code, "message": message}})
	}
	c.Abort()
}

func requestPluginIngressFormat(c *gin.Context) string {
	if c.Request == nil || c.Request.Method != http.MethodPost {
		return ""
	}
	path := c.FullPath()
	switch {
	case path == service.GenerationTaskSubmitRoute:
		return "task"
	case strings.HasSuffix(path, "/chat/completions"), strings.HasSuffix(path, "/embeddings"), strings.HasSuffix(path, "/images/generations"), strings.HasSuffix(path, "/images/edits"),
		strings.HasSuffix(path, "/tts"), strings.HasSuffix(path, "/stt"), strings.HasSuffix(path, "/web_search"), strings.HasSuffix(path, "/x_search"), strings.HasSuffix(path, "/alpha/search"):
		return "openai"
	case strings.HasSuffix(path, "/responses"):
		return "openai_responses"
	case strings.HasSuffix(path, "/messages"):
		return "claude"
	case strings.Contains(path, "/v1beta/models/") && (strings.HasSuffix(c.Param("modelAction"), ":generateContent") || strings.HasSuffix(c.Param("modelAction"), ":streamGenerateContent")):
		return "gemini"
	case isRequestPluginVideoCreate(c.Request.URL.Path):
		return "task"
	default:
		return ""
	}
}

func isRequestPluginVideoCreate(path string) bool {
	_, ok := service.GenerationVideoCreateProtocol(path)
	return ok
}

func isRequestPluginReportOnlyRoute(path string) bool {
	return strings.HasSuffix(path, "/tts") ||
		strings.HasSuffix(path, "/stt") ||
		strings.HasSuffix(path, "/web_search") ||
		strings.HasSuffix(path, "/x_search") ||
		strings.HasSuffix(path, "/alpha/search")
}

func requestPluginReportOnlyDefaultModel(path string) string {
	switch {
	case strings.HasSuffix(path, "/tts"):
		return "tts"
	case strings.HasSuffix(path, "/stt"):
		return "stt"
	case strings.HasSuffix(path, "/web_search"):
		return "grok-web-search"
	case strings.HasSuffix(path, "/x_search"):
		return "grok-x-search"
	default:
		return ""
	}
}
