package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/taskprotocol"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

var (
	pluginResponsesHeartbeatInterval = 15 * time.Second
	pluginResponsesObserveTimeout    = 5 * time.Minute
)

func effectivePluginPlatform(c *gin.Context, apiKey *service.APIKey) string {
	if apiKey == nil || apiKey.Group == nil {
		return service.PlatformOpenAI
	}
	if apiKey.Group.Platform == service.PlatformComposite {
		if resolved, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context()); ok && resolved != "" {
			return resolved
		}
	}
	return apiKey.Group.Platform
}

func (h *OpenAIGatewayHandler) handleGenerationRoutingError(c *gin.Context, err error) {
	if middleware.RejectRequestBodyMemoryOverload(c, err) {
		return
	}
	switch {
	case errors.Is(err, service.ErrGenerationCapacity):
		c.Header("Retry-After", "1")
		h.errorResponse(c, http.StatusTooManyRequests, "generation_capacity", "Task protocol admission is temporarily at capacity")
	case errors.Is(err, service.ErrGenerationFileQuota),
		errors.Is(err, service.ErrGenerationFileBusy),
		errors.Is(err, service.ErrGenerationFileNotFound),
		errors.Is(err, service.ErrGenerationFileTooLarge):
		h.generationFileError(c, err)
	case errors.Is(err, service.ErrGenerationDecode), errors.Is(err, service.ErrGenerationOrigin):
		h.errorResponse(c, http.StatusBadRequest, "invalid_task_intent", "The task request or its origin connection is unavailable")
	default:
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task protocol admission is temporarily unavailable")
	}
}

func (h *OpenAIGatewayHandler) handleGenerationCreateError(c *gin.Context, log *zap.Logger, err error) {
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
		return
	}
	if errors.Is(err, service.ErrGenerationFileQuota) ||
		errors.Is(err, service.ErrGenerationFileBusy) ||
		errors.Is(err, service.ErrGenerationFileNotFound) ||
		errors.Is(err, service.ErrGenerationFileTooLarge) {
		h.generationFileError(c, err)
		return
	}
	status := http.StatusBadRequest
	code := "invalid_task_request"
	message := "Task request is invalid or its pricing is not configured."
	switch {
	case errors.Is(err, service.ErrGenerationIdempotencyConflict):
		status = http.StatusConflict
		code = "idempotency_conflict"
		message = err.Error()
	case errors.Is(err, service.ErrBatchImageInsufficientBalance):
		status = http.StatusPaymentRequired
		code = "insufficient_balance"
		message = "Insufficient balance for the task reservation."
	case errors.Is(err, service.ErrGenerationCapacity):
		status = http.StatusTooManyRequests
		code = "generation_capacity"
		message = "Active task limit reached; retry after a task completes."
		c.Header("Retry-After", "10")
	}
	if log != nil {
		log.Warn("plugin_task_create_failed", zap.Error(err))
	}
	h.errorResponse(c, status, code, message)
}

// TryPluginTaskResponses bridges POST /v1/responses to explicitly bound Task
// plugins declaring the openai_responses protocol. It supports background,
// sync, and stream modes using the host-owned PluginResponsesMachine.
func (h *OpenAIGatewayHandler) TryPluginTaskResponses(
	c *gin.Context,
	apiKey *service.APIKey,
	subject middleware.AuthSubject,
	reqModel string,
	reqStream bool,
	body []byte,
	reqLog *zap.Logger,
) bool {
	if h.generations == nil || apiKey == nil || apiKey.Group == nil {
		return false
	}
	platform := effectivePluginPlatform(c, apiKey)
	matched, supports := h.generations.HasNewAPIResponsesTaskModel(c.Request.Context(), apiKey.GroupID, platform, reqModel)
	if !matched {
		return false
	}

	bgField := gjson.GetBytes(body, "background")
	if bgField.Exists() && bgField.Type != gjson.True && bgField.Type != gjson.False && bgField.Type != gjson.Null {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "background must be a boolean")
		return true
	}
	background := bgField.Bool()
	if background && reqStream {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "background and stream cannot both be true")
		return true
	}
	mode := "sync"
	if background {
		mode = "background"
	} else if reqStream {
		mode = "stream"
	}
	if !slices.Contains(supports, mode) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Model %q does not support %s mode", reqModel, mode))
		return true
	}
	if len(body) > service.MaxRequestPluginBody {
		h.errorResponse(c, http.StatusRequestEntityTooLarge, "request_too_large", "Task request exceeds 1 MiB")
		return true
	}
	idempotency := c.GetHeader("Idempotency-Key")
	if len(idempotency) > 200 || strings.TrimSpace(idempotency) != idempotency {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Idempotency-Key must contain at most 200 characters without surrounding whitespace")
		return true
	}

	ctx := service.WithNativeVideoPluginRouting(service.WithOpenAIProfitControlSuppressed(c.Request.Context()), service.GenerationProtocolOpenAIResponses)
	ctx = service.WithGenerationResponsesMode(ctx, mode)
	ctx = service.WithGenerationRequestHeaders(ctx, c.Request.Header)
	ctx = service.WithGenerationIngressPath(ctx, "/v1/responses")

	var task *service.GenerationTask
	if idempotency != "" {
		existing, replayErr := h.generations.ReplayBoundTask(ctx, apiKey, idempotency, body, service.GenerationProtocolOpenAIResponses)
		if errors.Is(replayErr, service.ErrGenerationIdempotencyConflict) {
			h.errorResponse(c, http.StatusConflict, "idempotency_conflict", replayErr.Error())
			return true
		}
		if replayErr != nil {
			h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
			return true
		}
		task = existing
	}

	if task == nil {
		var routingNeeded bool
		var err error
		ctx, routingNeeded, err = h.generations.VideoPluginRoutingNeeded(ctx, apiKey, platform, reqModel, service.GenerationProtocolOpenAIResponses)
		if err == nil && routingNeeded {
			ctx, err = h.generations.PrepareVideoRouting(ctx, apiKey, platform, reqModel, body, service.GenerationProtocolOpenAIResponses)
		}
		if err != nil {
			h.handleGenerationRoutingError(c, err)
			return true
		}
		selection, _, err := h.gatewayService.SelectAccountWithSchedulerForCapability(
			ctx,
			apiKey.GroupID,
			"",
			"",
			reqModel,
			nil,
			service.OpenAIUpstreamTransportHTTPSSE,
			service.OpenAIEndpointCapabilityPluginResponsesTask,
			false,
			false,
			false,
			platform,
		)
		if err != nil || selection == nil || selection.Account == nil {
			h.errorResponse(c, http.StatusServiceUnavailable, "task_channel_unavailable", "No compatible plugin channel is available for this model and mode")
			return true
		}
		releaseAccount := selection.ReleaseFunc
		if selection.Acquired && releaseAccount != nil {
			defer func() {
				if releaseAccount != nil {
					releaseAccount()
				}
			}()
		}
		if !selection.Account.SupportsNewAPIResponsesTaskMode(mode) {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Selected channel does not support %s mode", mode))
			return true
		}
		subscription, _ := middleware.GetSubscriptionFromContext(c)
		if err := h.billingCacheService.CheckBillingEligibility(ctx, apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(ctx, apiKey)); err != nil {
			status, code, message, _ := billingErrorDetails(err)
			h.errorResponse(c, status, code, message)
			return true
		}
		streamStarted := false
		release, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
		if !acquired {
			return true
		}
		if release != nil {
			defer release()
		}
		task, err = h.generations.CreateVideo(ctx, apiKey, subscription, selection.Account, reqModel, body, idempotency, service.GenerationProtocolOpenAIResponses)
		if selection.Acquired && releaseAccount != nil {
			releaseAccount()
			releaseAccount = nil
		}
		if err != nil {
			h.handleGenerationCreateError(c, reqLog, err)
			return true
		}
	}

	machine := taskprotocol.NewPluginResponsesMachine(task.ID, task.Model, task.CreatedAt.Unix(), taskprotocol.DefaultPluginProtocolLimits())
	c.Header("Cache-Control", "private, no-store")

	switch mode {
	case "background":
		machine.SetBackground(true)
		c.JSON(http.StatusOK, machine.PendingResponse(generation.PublicTaskStatus(task.Status)))
	case "stream":
		h.streamPluginResponsesTask(c, task, machine)
	default:
		h.awaitPluginResponsesSync(c, task, machine)
	}
	return true
}

func (h *OpenAIGatewayHandler) awaitPluginResponsesSync(c *gin.Context, task *service.GenerationTask, machine *taskprotocol.PluginResponsesMachine) {
	obsCtx, cancel := context.WithTimeout(c.Request.Context(), pluginResponsesObserveTimeout)
	defer cancel()
	finalTask, waitErr := h.generations.AwaitTask(obsCtx, task, 0, nil)
	if c.Request.Context().Err() != nil {
		return
	}
	if waitErr != nil {
		lastStatus := generation.PublicTaskStatus(task.Status)
		if finalTask != nil {
			lastStatus = generation.PublicTaskStatus(finalTask.Status)
		}
		resp, err := machine.TimeoutResponse(lastStatus)
		if err != nil {
			h.errorResponse(c, http.StatusInternalServerError, "task_protocol_error", "Task protocol request failed")
			return
		}
		c.JSON(http.StatusOK, resp)
		return
	}
	h.writePluginResponsesTerminal(c, finalTask, machine, true)
}

func (h *OpenAIGatewayHandler) streamPluginResponsesTask(c *gin.Context, task *service.GenerationTask, machine *taskprotocol.PluginResponsesMachine) {
	created, err := machine.CreatedEvent()
	if err != nil {
		h.errorResponse(c, http.StatusInternalServerError, "task_protocol_error", "Task protocol request failed")
		return
	}
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	if err := writePluginResponsesSSE(c, created); err != nil {
		return
	}

	obsCtx, cancel := context.WithTimeout(c.Request.Context(), pluginResponsesObserveTimeout)
	defer cancel()

	var prevState taskprotocol.ProtocolState
	var tickErr error
	lastStatus := generation.PublicTaskStatus(task.Status)

	finalTask, waitErr := h.generations.AwaitTask(obsCtx, task, pluginResponsesHeartbeatInterval, func(current *service.GenerationTask, isHeartbeat bool) error {
		if isHeartbeat {
			if _, err := c.Writer.Write([]byte(": PING\n\n")); err != nil {
				return err
			}
			c.Writer.Flush()
			return nil
		}
		if machine.Terminal() {
			return nil
		}
		lastStatus = generation.PublicTaskStatus(current.Status)
		evResult, err := h.generations.RenderResponsesEvents(obsCtx, current, prevState)
		if err != nil {
			tickErr = err
			return err
		}
		prevState = evResult.State
		events, err := machine.ApplyTick(evResult, lastStatus)
		if err != nil {
			tickErr = err
			return err
		}
		for _, ev := range events {
			if err := writePluginResponsesSSE(c, ev); err != nil {
				return err
			}
		}
		return nil
	})

	if machine.Terminal() || c.Request.Context().Err() != nil {
		return
	}
	if finalTask != nil {
		lastStatus = generation.PublicTaskStatus(finalTask.Status)
	}
	if tickErr == nil && errors.Is(waitErr, context.DeadlineExceeded) {
		if ev, err := machine.TimeoutEvent(lastStatus); err == nil {
			_ = writePluginResponsesSSE(c, ev)
		}
		return
	}
	if ev, err := machine.FailureEvent(lastStatus); err == nil {
		_ = writePluginResponsesSSE(c, ev)
	}
}

func writePluginResponsesSSE(c *gin.Context, ev taskprotocol.PluginResponsesStreamEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", ev.Type, payload); err != nil {
		return err
	}
	c.Writer.Flush()
	return nil
}

func (h *OpenAIGatewayHandler) writePluginResponsesTerminal(c *gin.Context, task *service.GenerationTask, machine *taskprotocol.PluginResponsesMachine, hasRenderFinal bool) {
	publicStatus := generation.PublicTaskStatus(task.Status)
	if task.Status == service.GenerationFailed || task.Status == service.GenerationUncertain {
		resp, err := machine.FinalResponse(nil, "FAILURE")
		if err != nil {
			h.errorResponse(c, http.StatusInternalServerError, "task_protocol_error", "Task protocol request failed")
			return
		}
		c.JSON(http.StatusOK, resp)
		return
	}
	var (
		resp map[string]any
		err  error
	)
	if hasRenderFinal {
		var raw json.RawMessage
		raw, err = h.generations.RenderResponsesFinal(c.Request.Context(), task)
		if err == nil {
			resp, err = machine.FinalResponse(raw, "SUCCESS")
		}
	} else {
		var evRes taskprotocol.ProtocolEventResult
		evRes, err = h.generations.RenderResponsesEvents(c.Request.Context(), task, taskprotocol.ProtocolState{})
		if err == nil {
			resp, err = machine.FinalFromEvents(evRes, "SUCCESS")
		}
	}
	if err != nil {
		failResp, failErr := machine.FailureResponse(publicStatus)
		if failErr != nil {
			h.errorResponse(c, http.StatusInternalServerError, "task_protocol_error", "Task protocol request failed")
			return
		}
		c.JSON(http.StatusOK, failResp)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// GenerationResponsesGet retrieves an owned durable Task plugin response via
// GET /v1/responses/:response_id (and /responses/:response_id).
func (h *OpenAIGatewayHandler) GenerationResponsesGet(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || h.generations == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
		return
	}
	taskID, ok := taskprotocol.TaskIDFromResponseID(c.Param("response_id"))
	if !ok {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Response not found")
		return
	}
	task, err := h.generations.GetPluginTask(c.Request.Context(), taskID, apiKey)
	if errors.Is(err, service.ErrGenerationTaskNotFound) {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Response not found")
		return
	}
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
		return
	}
	if task.Protocol != service.GenerationProtocolOpenAIResponses {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Response not found")
		return
	}
	info, err := h.generations.PluginTaskResponsesInfo(c.Request.Context(), task.PluginVersionID)
	if err != nil {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Response not found")
		return
	}
	machine := taskprotocol.NewPluginResponsesMachine(task.ID, task.Model, task.CreatedAt.Unix(), taskprotocol.DefaultPluginProtocolLimits())
	machine.SetBackground(task.PublicFields.ResponsesBackground)
	publicStatus := generation.PublicTaskStatus(task.Status)
	if task.Status != service.GenerationSucceeded && task.Status != service.GenerationFailed && task.Status != service.GenerationUncertain {
		c.JSON(http.StatusOK, machine.PendingResponse(publicStatus))
		return
	}
	h.writePluginResponsesTerminal(c, task, machine, info.HasRenderFinal)
}
