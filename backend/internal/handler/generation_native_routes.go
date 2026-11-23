package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/taskprotocol"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const maxNativeBatchQueryTaskIDs = 100

func writeNativeReply(c *gin.Context, reply taskprotocol.NativeReply) {
	c.Header("Cache-Control", "private, no-store")
	for name, value := range reply.Headers {
		c.Header(name, value)
	}
	if !reply.HasBody {
		c.Status(reply.Status)
		return
	}
	c.JSON(reply.Status, reply.Body)
}

func (h *OpenAIGatewayHandler) MayDispatchNativeTaskAlias(c *gin.Context) bool {
	return h != nil && h.generations != nil && h.generations.MayDispatchNativeTaskAlias(c.Request.Context(), c.Request.Method, c.Request.URL.Path, c.Request.URL.Query())
}

func (h *OpenAIGatewayHandler) GenerationTaskNativeAlias(c *gin.Context) {
	if c.Request.Method == http.MethodGet {
		h.generationTaskNativeQuery(c, "", c.Request.URL.Path, true)
		return
	}
	h.generationTaskNativeSubmit(c, "", c.Request.URL.Path, true)
}

// TryNativeTaskAliasSubmit checks whether the authenticated key's group binds a
// Task plugin claiming this submit alias (such as Gemini :predictLongRunning).
// Returns false when no plugin in the group claims the route so static host
// handlers (e.g. native Gemini relay) keep working unchanged.
func (h *OpenAIGatewayHandler) TryNativeTaskAliasSubmit(c *gin.Context) bool {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil || apiKey.GroupID == nil || h == nil || h.generations == nil {
		return false
	}
	subpath := c.Request.URL.Path
	publicModel := nativeAliasPathModel(subpath)
	if mapped, ok := service.ResolvedUpstreamModelFromContext(c.Request.Context()); ok && mapped != "" {
		publicModel = mapped
	}
	_, matched, err := h.generations.ResolveNativeTaskAlias(c.Request.Context(), apiKey.GroupID, effectivePluginPlatform(c, apiKey), c.Request.Method, subpath, publicModel)
	if errors.Is(err, service.ErrGenerationDecode) {
		h.errorResponse(c, http.StatusConflict, "ambiguous_task_route", "Multiple plugins declare this route; use the plugin-prefixed path")
		return true
	}
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Native route selection is unavailable")
		return true
	}
	if !matched {
		return false
	}
	h.generationTaskNativeSubmit(c, "", subpath, true)
	return true
}

func nativeAliasPathModel(subpath string) string {
	const prefix = "/v1beta/models/"
	if strings.HasPrefix(subpath, prefix) && strings.HasSuffix(subpath, ":predictLongRunning") {
		candidate := strings.TrimSuffix(strings.TrimPrefix(subpath, prefix), ":predictLongRunning")
		if candidate != "" && !strings.Contains(candidate, "/") {
			return candidate
		}
	}
	return ""
}

func normalizeNativeSubpath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "/"
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}
	return trimmed
}

// buildNativeTaskAuditBody collects prompt/text/image fields across supported
// vendor native task schemas so Prompt Audit sees every user-supplied prompt.
func buildNativeTaskAuditBody(body []byte) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	var texts []string
	appendText := func(raw gjson.Result) {
		if raw.Type == gjson.String {
			if s := strings.TrimSpace(raw.Str); s != "" {
				texts = append(texts, s)
			}
		}
	}
	for _, field := range []string{
		"prompt", "negative_prompt", "gpt_description_prompt", "title", "tags", "lyrics", "text",
		"input", "input.prompt", "input.negative_prompt", "input.text",
		"metadata.prompt", "metadata.gpt_description_prompt", "metadata.title", "metadata.tags",
	} {
		appendText(gjson.GetBytes(body, field))
	}
	for _, item := range gjson.GetBytes(body, "instances").Array() {
		appendText(item.Get("prompt"))
		appendText(item.Get("text"))
	}
	for _, item := range gjson.GetBytes(body, "content").Array() {
		appendText(item.Get("text"))
		appendText(item.Get("prompt"))
	}
	if len(texts) == 0 {
		return body
	}
	payload := map[string]any{"prompt": strings.Join(texts, "\n")}
	if imgs := gjson.GetBytes(body, "images"); imgs.Exists() {
		payload["images"] = json.RawMessage(imgs.Raw)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

// runNativeTaskRequestPlugins builds the task RequestPluginPlan and executes
// any matching early ingress rules after model resolution and idempotency replay.
func (h *OpenAIGatewayHandler) runNativeTaskRequestPlugins(
	ctx context.Context,
	c *gin.Context,
	apiKey *service.APIKey,
	model, subpath string,
	body []byte,
) (context.Context, []byte, bool, bool) {
	plugins := h.generations.Plugins()
	if plugins == nil || apiKey == nil || apiKey.GroupID == nil {
		return ctx, body, false, true
	}
	applicable, err := plugins.RequestPluginsMayApply(ctx, *apiKey.GroupID)
	if err != nil {
		requestPluginAbort(c, http.StatusServiceUnavailable, "request_processing_unavailable", "Request-processing policy is unavailable")
		return ctx, body, false, false
	}
	if !applicable {
		return service.WithRequestPluginPlan(ctx, nil), body, false, true
	}
	requestID := uuid.NewString()
	if id, ok := ctx.Value(ctxkey.ClientRequestID).(string); ok && id != "" {
		requestID = id
	}
	call := service.RequestPluginCall{
		RequestID:       requestID,
		UserID:          apiKey.UserID,
		TokenID:         apiKey.ID,
		TokenName:       apiKey.Name,
		GroupID:         *apiKey.GroupID,
		Group:           strconv.FormatInt(*apiKey.GroupID, 10),
		Model:           model,
		ModelCandidates: []string{model},
		Format:          "task",
		Path:            subpath,
	}
	plan, err := plugins.NewRequestPluginPlan(ctx, call)
	if err != nil {
		requestPluginAbort(c, http.StatusServiceUnavailable, "request_processing_unavailable", "Request-processing policy is unavailable")
		return ctx, body, false, false
	}
	ctx = service.WithRequestPluginPlan(ctx, plan)
	if plan == nil || !plan.HasEarlyIngress() {
		return ctx, body, false, true
	}
	if err = pkghttputil.ReserveRequestBodyMemory(ctx, int64(len(body))*6+(8<<20)); err != nil {
		middleware.RejectRequestBodyMemoryOverload(c, err)
		return ctx, body, false, false
	}
	out, runErr := plan.Ingress(ctx, body)
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	auditErr := plugins.RecordRequestPluginRun(auditCtx, call, out, false)
	cancel()
	if auditErr != nil {
		logger.LegacyPrintf("handler.request_plugin", "unable to persist request processing audit")
		requestPluginAbort(c, http.StatusServiceUnavailable, "request_processing_audit_unavailable", "Request-processing audit is unavailable")
		return ctx, body, false, false
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
				return ctx, body, false, false
			}
		}
		requestPluginAbort(c, http.StatusServiceUnavailable, "request_processing_failed", "Request-processing policy could not be applied")
		return ctx, body, false, false
	}
	return ctx, out.Body, true, true
}

// GenerationTaskNativeSubmit handles POST/PUT/PATCH/DELETE /v1/tasks/:key/native/*subpath for
// Task plugins declaring meta.routes (submit or dynamic batch-query routes).
func (h *OpenAIGatewayHandler) GenerationTaskNativeSubmit(c *gin.Context) {
	h.generationTaskNativeSubmit(c, c.Param("key"), normalizeNativeSubpath(c.Param("subpath")), false)
}

func (h *OpenAIGatewayHandler) generationTaskNativeSubmit(c *gin.Context, pluginKey, subpath string, alias bool) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil || apiKey.GroupID == nil {
		h.errorResponse(c, http.StatusForbidden, "permission_error", "A task group is required")
		return
	}
	if h.generations == nil || h.gatewayService == nil || h.billingCacheService == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
		return
	}
	if !alias && !service.ValidGenerationTaskSubmitPlugin(pluginKey) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid task plugin key")
		return
	}
	platform := effectivePluginPlatform(c, apiKey)
	var body []byte
	var err error
	isMultipart := false
	if rawCT := c.GetHeader("Content-Type"); rawCT != "" {
		mediaType, _, parseErr := mime.ParseMediaType(rawCT)
		if parseErr != nil || (mediaType != "application/json" && mediaType != "multipart/form-data") {
			h.errorResponse(c, http.StatusUnsupportedMediaType, "unsupported_media_type", "Native route submission requires JSON or multipart/form-data")
			return
		}
		isMultipart = mediaType == "multipart/form-data"
	}
	if isMultipart {
		spoolCtx, normalized, cleanup, spooledOK := h.readTaskMultipart(c, apiKey)
		if !spooledOK {
			return
		}
		defer cleanup()
		c.Request = c.Request.WithContext(service.WithGenerationMultipartInput(spoolCtx))
		body = normalized
	} else {
		if c.Request.ContentLength > service.MaxRequestPluginBody && c.GetHeader("Content-Encoding") == "" {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "request_too_large", "Task request exceeds 1 MiB")
			return
		}
		body, err = pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			if !middleware.RejectRequestBodyReadError(c, "handler.native_route", err) {
				middleware.AbortWithRequestBodyReadFailure(c, "handler.native_route", err)
			}
			return
		}
		if len(body) > service.MaxRequestPluginBody {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "request_too_large", "Task request exceeds 1 MiB")
			return
		}
		if len(body) > 0 {
			var obj map[string]json.RawMessage
			if json.Unmarshal(body, &obj) != nil || obj == nil {
				h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body must be a JSON object")
				return
			}
		}
	}
	var versionID int64
	var route jsplugin.Route
	var params map[string]string
	var matched bool
	historicalQueryOnly := false
	if alias {
		var match service.NativeTaskAliasMatch
		publicModel := requestmodel.FromJSON(body)
		if publicModel == "" {
			publicModel = nativeAliasPathModel(subpath)
		}
		match, matched, err = h.generations.ResolveNativeTaskAlias(c.Request.Context(), apiKey.GroupID, platform, c.Request.Method, subpath, publicModel)
		if errors.Is(err, service.ErrGenerationDecode) {
			h.errorResponse(c, http.StatusConflict, "ambiguous_task_route", "Multiple plugins declare this route; use the plugin-prefixed path")
			return
		}
		if err != nil {
			h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Native route selection is unavailable")
			return
		}
		pluginKey, versionID, route, params = match.PluginKey, match.VersionID, match.Route, match.Params
	} else {
		versionID, route, params, matched = h.generations.FindNewAPINativeRoute(c.Request.Context(), apiKey.GroupID, platform, pluginKey, c.Request.Method, subpath)
	}
	if !matched {
		if match, ok, _ := h.generations.ResolveNativeTaskHistoricalBatchQuery(c.Request.Context(), apiKey, pluginKey, c.Request.Method, subpath, body); ok {
			pluginKey, versionID, route, params, matched, historicalQueryOnly = match.PluginKey, match.VersionID, match.Route, match.Params, true, true
		}
	}
	if !matched {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Native route not found for this task plugin")
		return
	}

	var decoded *service.NativeRouteDecodeResult
	if historicalQueryOnly {
		decoded, err = h.generations.DecodeNativeRoutePinned(c.Request.Context(), versionID, route, params, subpath, body)
	} else {
		decoded, err = h.generations.DecodeNativeRoute(c.Request.Context(), versionID, route, params, subpath, body)
	}
	if err != nil || decoded == nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid native route request")
		return
	}

	switch decoded.Kind {
	case "query":
		if route.Type != jsplugin.RouteTypeDynamic {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Declared submit route cannot return a query")
			return
		}
		if len(decoded.TaskIDs) > maxNativeBatchQueryTaskIDs {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Too many task IDs (max %d)", maxNativeBatchQueryTaskIDs))
			return
		}
		owned := make([]*service.GenerationTask, 0, len(decoded.TaskIDs))
		for _, id := range decoded.TaskIDs {
			id = strings.TrimSpace(id)
			if !generation.IsPublicTaskID(id) {
				h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid task ID in native query")
				return
			}
			task, err := h.generations.GetPluginTask(c.Request.Context(), id, apiKey)
			if errors.Is(err, service.ErrGenerationTaskNotFound) {
				continue
			}
			if err != nil {
				h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
				return
			}
			ver, err := h.generations.GetPluginVersionMeta(c.Request.Context(), task.PluginVersionID)
			if err != nil || ver == nil || ver.Meta.Key != pluginKey {
				continue
			}
			owned = append(owned, task)
		}
		reply, err := h.generations.RenderNativeTaskList(c.Request.Context(), versionID, owned, route, params)
		if err != nil {
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Failed to render native batch query response")
			return
		}
		writeNativeReply(c, reply)
		return

	case "submit":
		if historicalQueryOnly {
			h.errorResponse(c, http.StatusNotFound, "not_found_error", "Native route not found for this task plugin")
			return
		}
		if len(body) == 0 {
			body = []byte(`{}`)
		}
		model := strings.TrimSpace(decoded.Model)
		if model == "" {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Native route decoder did not resolve a model")
			return
		}
		if apiKey.Group.ModelAllowlistEnabled() && !apiKey.Group.ModelAllowlist.Allows(model) {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalModelConfiguration)
			middleware.MarkIngressRejected(c, middleware.IngressRejectModelNotAllowed)
			h.errorResponse(c, http.StatusNotFound, "not_found_error", fmt.Sprintf("Model %q is not available for this group", model))
			return
		}
		idempotency := c.GetHeader("Idempotency-Key")
		if len(idempotency) > 200 || strings.TrimSpace(idempotency) != idempotency {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid Idempotency-Key")
			return
		}
		protocol := service.GenerationProtocolNativePrefix + pluginKey
		ctx := service.WithNativeVideoPluginRouting(service.WithOpenAIProfitControlSuppressed(c.Request.Context()), protocol)
		ctx = service.WithGenerationRequestHeaders(ctx, c.Request.Header)
		ctx = service.WithGenerationRouteMatch(ctx, route, params, subpath, versionID)
		ctx = service.WithGenerationIngressPath(ctx, subpath)

		var task *service.GenerationTask
		if idempotency != "" {
			existing, replayErr := h.generations.ReplayBoundTask(ctx, apiKey, idempotency, body, protocol)
			if errors.Is(replayErr, service.ErrGenerationIdempotencyConflict) {
				h.errorResponse(c, http.StatusConflict, "idempotency_conflict", replayErr.Error())
				return
			}
			if replayErr != nil {
				h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
				return
			}
			task = existing
		}
		if task == nil {
			var clientReqErr error
			ctx, clientReqErr = service.WithGenerationClientRequest(ctx, body)
			if clientReqErr != nil {
				h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid native route request")
				return
			}
			var policyFileErr error
			ctx, policyFileErr = h.generations.PrepareVideoPolicyFiles(ctx, apiKey, body, protocol)
			if policyFileErr != nil {
				status := http.StatusBadRequest
				if errors.Is(policyFileErr, service.ErrGenerationFileNotFound) {
					status = http.StatusNotFound
				}
				h.errorResponse(c, status, "video_file_unavailable", "Task file references are invalid or unavailable")
				return
			}
			var policyMutated, policyAllowed bool
			ctx, body, policyMutated, policyAllowed = h.runNativeTaskRequestPlugins(ctx, c, apiKey, model, subpath, body)
			if !policyAllowed {
				return
			}
			if policyMutated {
				reDecoded, reDecodeErr := h.generations.DecodeNativeRoute(ctx, versionID, route, params, subpath, body)
				if reDecodeErr != nil || reDecoded == nil || reDecoded.Kind != "submit" || strings.TrimSpace(reDecoded.Model) != model {
					requestPluginAbort(c, http.StatusServiceUnavailable, "request_processing_failed", "Request-processing policy produced an invalid native task payload")
					return
				}
			}
			var routingNeeded bool
			ctx, routingNeeded, err = h.generations.VideoPluginRoutingNeeded(ctx, apiKey, platform, model, protocol)
			if err == nil && routingNeeded {
				ctx, err = h.generations.PrepareVideoRouting(ctx, apiKey, platform, model, body, protocol)
			}
			if err != nil {
				h.handleGenerationRoutingError(c, err)
				return
			}
			selection, _, err := h.gatewayService.SelectAccountWithSchedulerForCapability(
				ctx,
				apiKey.GroupID,
				"",
				"",
				model,
				nil,
				service.OpenAIUpstreamTransportHTTPSSE,
				service.OpenAIEndpointCapabilityPluginNativeRoutes,
				false,
				false,
				false,
				platform,
			)
			if err != nil || selection == nil || selection.Account == nil {
				h.errorResponse(c, http.StatusServiceUnavailable, "task_channel_unavailable", "No compatible plugin channel is available for this native route")
				return
			}
			if selection.Acquired && selection.ReleaseFunc != nil {
				defer selection.ReleaseFunc()
			}
			subscription, _ := middleware.GetSubscriptionFromContext(c)
			if err := h.billingCacheService.CheckBillingEligibility(ctx, apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(ctx, apiKey)); err != nil {
				status, code, message, _ := billingErrorDetails(err)
				h.errorResponse(c, status, code, message)
				return
			}
			subject, ok := middleware.GetAuthSubjectFromContext(c)
			if !ok {
				h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
				return
			}
			reqLog := requestLogger(c, "handler.native_route")
			streamStarted := false
			release, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
			if !acquired {
				return
			}
			if release != nil {
				defer release()
			}
			if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIImages, model, buildNativeTaskAuditBody(body)); decision != nil && !decision.AllowNextStage {
				h.openAISecurityAuditError(c, decision)
				return
			}
			task, err = h.generations.CreateVideo(ctx, apiKey, subscription, selection.Account, model, body, idempotency, protocol)
			if err != nil {
				h.handleGenerationCreateError(c, reqLog, err)
				return
			}
		}

		reply, renderErr := h.generations.RenderNativeTask(ctx, task, route, params, "create")
		if renderErr != nil {
			c.Header("Location", "/v1/tasks/"+task.ID)
			c.Header("Cache-Control", "private, no-store")
			c.JSON(http.StatusOK, gin.H{"id": task.ID, "task_id": task.ID, "status": task.PublicTaskReadView().Status, "model": task.Model, "created_at": task.CreatedAt.Unix()})
			return
		}
		if _, hasLocation := reply.Headers["Location"]; !hasLocation {
			c.Header("Location", "/v1/tasks/"+task.ID)
		}
		writeNativeReply(c, reply)
		return

	default:
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Unsupported native route intent")
	}
}

// TryNativeTaskQueryAlias is the host's explicit handoff for a video query
// path also declared by the task's own plugin, matching New API's
// handOffNativeTaskQuery: only tasks submitted natively use the native wire
// format, while canonical OpenAI Videos tasks keep the OpenAI video response.
func (h *OpenAIGatewayHandler) TryNativeTaskQueryAlias(c *gin.Context) bool {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || h.generations == nil || !service.NativeTaskAliasPathAllowed(c.Request.URL.Path) {
		return false
	}
	task, match, matched, err := h.generations.ResolveNativeTaskQuery(c.Request.Context(), key, "", c.Request.URL.Path, c.Request.URL.Query())
	if err != nil || !matched || task == nil || task.Protocol == "" || task.Protocol == service.GenerationProtocolOpenAI {
		return false
	}
	reply, err := h.generations.RenderNativeTask(c.Request.Context(), task, match.Route, match.Params, "query")
	if err != nil {
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Failed to render native task response")
	} else {
		writeNativeReply(c, reply)
	}
	return true
}

// GenerationTaskNativeQuery handles GET /v1/tasks/:key/native/*subpath for
// Task plugins declaring meta.routes query routes.
func (h *OpenAIGatewayHandler) GenerationTaskNativeQuery(c *gin.Context) {
	h.generationTaskNativeQuery(c, c.Param("key"), normalizeNativeSubpath(c.Param("subpath")), false)
}

func (h *OpenAIGatewayHandler) generationTaskNativeQuery(c *gin.Context, pluginKey, subpath string, alias bool) {
	c.Header("Cache-Control", "private, no-store")
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil || h.generations == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
		return
	}
	if !alias && !service.ValidGenerationTaskSubmitPlugin(pluginKey) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid task plugin key")
		return
	}
	task, match, matched, err := h.generations.ResolveNativeTaskQuery(c.Request.Context(), apiKey, pluginKey, subpath, c.Request.URL.Query())
	if errors.Is(err, service.ErrGenerationTaskNotFound) || err == nil && !matched {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Task not found")
		return
	}
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
		return
	}
	reply, err := h.generations.RenderNativeTask(c.Request.Context(), task, match.Route, match.Params, "query")
	if err != nil {
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Failed to render native task response")
		return
	}
	writeNativeReply(c, reply)
}
