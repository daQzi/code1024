package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/Wei-Shaw/sub2api/newapi-engine/taskprotocol"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

var pluginImageObserveTimeout = 5 * time.Minute

// TryPluginTaskImage bridges POST /v1/images/generations and /v1/images/edits
// to explicitly bound Task plugins declaring the openai_image protocol.
func (h *OpenAIGatewayHandler) TryPluginTaskImage(
	c *gin.Context,
	apiKey *service.APIKey,
	subject middleware.AuthSubject,
	rawModel string,
	endpointPath string,
	body []byte,
	reqLog *zap.Logger,
) bool {
	if h.generations == nil || apiKey == nil || apiKey.Group == nil || strings.TrimSpace(rawModel) == "" {
		return false
	}
	ensureCompositeTargetPlatform(c, apiKey, rawModel)
	platform := effectivePluginPlatform(c, apiKey)
	if !h.generations.HasNewAPIImageTaskModel(c.Request.Context(), apiKey.GroupID, platform, rawModel) {
		return false
	}
	if !compositeTargetPlatformAllowed(c, apiKey, rawModel, service.PlatformOpenAI) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Model is not supported by this OpenAI-compatible endpoint for composite groups")
		return true
	}
	if !service.GroupAllowsImageGeneration(apiKey.Group) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
		return true
	}
	idempotency := c.GetHeader("Idempotency-Key")
	if len(idempotency) > 200 || strings.TrimSpace(idempotency) != idempotency {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Idempotency-Key must contain at most 200 characters without surrounding whitespace")
		return true
	}

	ctx := service.WithNativeVideoPluginRouting(service.WithOpenAIProfitControlSuppressed(c.Request.Context()), service.GenerationProtocolOpenAIImage)
	ctx = service.WithGenerationRequestHeaders(ctx, c.Request.Header)
	ctx = service.WithGenerationIngressPath(ctx, endpointPath)

	var (
		jsonBody       json.RawMessage
		responseFormat string
	)
	contentType := c.GetHeader("Content-Type")
	if isMultipartImagesContentType(contentType) {
		spoolCtx, normalized, cleanup, err := h.generations.ReadVideoMultipart(ctx, apiKey, contentType, bytes.NewReader(body))
		if err != nil {
			var pathError *os.PathError
			switch {
			case errors.Is(err, service.ErrGenerationFileBusy), errors.Is(err, service.ErrGenerationFileTooLarge):
				h.generationFileError(c, err)
			case errors.Is(err, pluginTask.ErrVideoMultipartTooLarge):
				h.errorResponse(c, http.StatusRequestEntityTooLarge, "request_too_large", "Image multipart text exceeds limit")
			case errors.As(err, &pathError):
				h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Image upload storage is unavailable")
			default:
				h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid multipart image request")
			}
			return true
		}
		defer cleanup()
		ctx = service.WithGenerationMultipartInput(spoolCtx)
		jsonBody = normalized
		responseFormat = strings.TrimSpace(gjson.GetBytes(jsonBody, "response_format").String())
	} else {
		if len(body) > service.MaxRequestPluginBody {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "request_too_large", "Image task request exceeds 1 MiB")
			return true
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(body, &obj) != nil || obj == nil {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body must be a JSON object")
			return true
		}
		jsonBody = body
		responseFormat = strings.TrimSpace(gjson.GetBytes(jsonBody, "response_format").String())
	}

	if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIImages, rawModel, jsonBody); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return true
	}

	var task *service.GenerationTask
	if idempotency != "" {
		existing, replayErr := h.generations.ReplayBoundTask(ctx, apiKey, idempotency, jsonBody, service.GenerationProtocolOpenAIImage)
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
		ctx, routingNeeded, err = h.generations.VideoPluginRoutingNeeded(ctx, apiKey, platform, rawModel, service.GenerationProtocolOpenAIImage)
		if err == nil && routingNeeded {
			ctx, err = h.generations.PrepareVideoRouting(ctx, apiKey, platform, rawModel, jsonBody, service.GenerationProtocolOpenAIImage)
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
			rawModel,
			nil,
			service.OpenAIUpstreamTransportHTTPSSE,
			service.OpenAIEndpointCapabilityPluginImageTask,
			false,
			false,
			false,
			platform,
		)
		if err != nil || selection == nil || selection.Account == nil {
			h.errorResponse(c, http.StatusServiceUnavailable, "image_channel_unavailable", "No compatible plugin channel is available for this image model")
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
		task, err = h.generations.CreateVideo(ctx, apiKey, subscription, selection.Account, rawModel, jsonBody, idempotency, service.GenerationProtocolOpenAIImage)
		if selection.Acquired && releaseAccount != nil {
			releaseAccount()
			releaseAccount = nil
		}
		if err != nil {
			h.handleGenerationCreateError(c, reqLog, err)
			return true
		}
	}

	obsCtx, cancel := context.WithTimeout(c.Request.Context(), pluginImageObserveTimeout)
	defer cancel()
	finalTask, waitErr := h.generations.AwaitTask(obsCtx, task, 0, nil)
	if c.Request.Context().Err() != nil {
		return true
	}
	if waitErr != nil {
		h.errorResponse(c, http.StatusGatewayTimeout, "timeout_error", "Image generation task timed out; inspect /v1/tasks/"+task.ID)
		return true
	}
	if finalTask.Status != service.GenerationSucceeded {
		msg := finalTask.PublicTaskReadView().FailReason
		if strings.TrimSpace(msg) == "" {
			msg = "Image generation task failed"
		}
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", msg)
		return true
	}
	raw, err := h.generations.RenderTaskImage(c.Request.Context(), finalTask)
	if err != nil {
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Failed to render image task response")
		return true
	}
	resp, err := taskprotocol.NormalizeImageResponse(raw, finalTask.CreatedAt.Unix())
	if err != nil {
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Image plugin returned an invalid OpenAI image response")
		return true
	}
	if strings.EqualFold(responseFormat, "b64_json") {
		h.generations.InlineTaskImageBase64(c.Request.Context(), finalTask, resp)
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, resp)
	return true
}
