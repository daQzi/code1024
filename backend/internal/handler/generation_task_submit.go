package handler

import (
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Pin the public model, entry and plugin identity before request transforms.
// A retry of an accepted job never depends on current plugin or price state.
func (h *OpenAIGatewayHandler) GenerationTaskSubmitIngress(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.Group == nil || key.GroupID == nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "message": "A task group is required"}})
		return
	}
	abort := func(status int, code, message string) {
		h.errorResponse(c, status, code, message)
		c.Abort()
	}
	if h.generations == nil {
		abort(http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
		return
	}
	plugin := c.Param("key")
	if !service.ValidGenerationTaskSubmitPlugin(plugin) {
		abort(http.StatusBadRequest, "invalid_request_error", "Invalid task plugin key")
		return
	}
	contentType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	isMultipart := c.GetHeader("Content-Type") != "" && err == nil && contentType == "multipart/form-data"
	if c.GetHeader("Content-Type") != "" && !isMultipart && (err != nil || contentType != "application/json") {
		abort(http.StatusUnsupportedMediaType, "unsupported_media_type", "Task submission requires JSON or multipart/form-data")
		return
	}
	idempotency := c.GetHeader("Idempotency-Key")
	if len(idempotency) > 200 || strings.TrimSpace(idempotency) != idempotency {
		abort(http.StatusBadRequest, "invalid_request_error", "Invalid Idempotency-Key")
		return
	}
	var body []byte
	if isMultipart {
		if plugin == "sunoapi" {
			abort(http.StatusUnsupportedMediaType, "unsupported_media_type", "This task plugin does not accept multipart file uploads")
			return
		}
		spoolCtx, normalized, cleanup, ok := h.readTaskMultipart(c, key)
		if !ok {
			c.Abort()
			return
		}
		defer cleanup()
		c.Request = c.Request.WithContext(service.WithGenerationMultipartInput(spoolCtx))
		body = normalized
	} else {
		if c.Request.ContentLength > service.MaxRequestPluginBody && c.GetHeader("Content-Encoding") == "" {
			abort(http.StatusRequestEntityTooLarge, "request_too_large", "Task request exceeds 1 MiB")
			return
		}
		body, err = httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			if !middleware.RejectRequestBodyReadError(c, "handler.task_submit", err) {
				middleware.AbortWithRequestBodyReadFailure(c, "handler.task_submit", err)
			}
			return
		}
		if len(body) > service.MaxRequestPluginBody {
			abort(http.StatusRequestEntityTooLarge, "request_too_large", "Task request exceeds 1 MiB")
			return
		}
	}
	ctx, err := service.PrepareGenerationTaskSubmit(c.Request.Context(), plugin, body)
	if err != nil {
		if !middleware.RejectRequestBodyMemoryOverload(c, err) {
			abort(http.StatusBadRequest, "invalid_request_error", "Task submission requires a valid model and a non-streaming JSON object")
		}
		return
	}
	c.Request = c.Request.WithContext(ctx)
	requestmodel.ResetRequestBody(c.Request, body)
	if idempotency != "" {
		task, err := h.generations.ReplayVideo(ctx, key.UserID, key.ID, idempotency, body, service.GenerationTaskSubmitProtocol(ctx))
		if errors.Is(err, service.ErrGenerationIdempotencyConflict) || task != nil && task.GroupID != *key.GroupID {
			abort(http.StatusConflict, "idempotency_conflict", service.ErrGenerationIdempotencyConflict.Error())
			return
		}
		if err != nil {
			abort(http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
			return
		}
		if task != nil {
			writeGenerationCreated(c, task)
			c.Abort()
			return
		}
	}
	if key.Group.Platform != service.NormalizeOpenAICompatiblePlatform(key.Group.Platform) {
		abort(http.StatusBadRequest, "invalid_request_error", "Select a supported upstream platform group for task submission")
		return
	}
	c.Request.Header.Set("Content-Type", "application/json")
	c.Next()
}

func (h *OpenAIGatewayHandler) GenerationTaskSubmit(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.Group == nil || service.GenerationTaskSubmitPlugin(c.Request.Context()) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Task submission was not prepared")
		return
	}
	protocol := service.GenerationTaskSubmitProtocol(c.Request.Context())
	if h.TryPluginNativeVideo(c, key.Group.Platform, protocol) {
		return
	}
	if protocol == service.GenerationProtocolOpenAI && h.TryPluginNativeVideo(c, key.Group.Platform, service.GenerationProtocolGenericTask) {
		return
	}
	h.errorResponse(c, http.StatusServiceUnavailable, "task_channel_unavailable", "No compatible account is bound to this task plugin and model")
}
