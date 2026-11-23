package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GenerationRemixIngress runs after authentication and before generic model
// readers. Standard remix bodies contain only a prompt: the owned parent fixes
// the model. Accepted replays happen before any parent, plugin or price lookup.
func (h *OpenAIGatewayHandler) GenerationRemixIngress(c *gin.Context) {
	origin, remix := service.GenerationVideoRemixOrigin(c.Request.URL.Path)
	if c.Request.Method != http.MethodPost || !remix {
		c.Next()
		return
	}
	if h.generations == nil {
		h.errorResponse(c, 503, "api_error", "Video task service is unavailable")
		c.Abort()
		return
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.Group == nil {
		h.errorResponse(c, 401, "authentication_error", "Invalid API key")
		c.Abort()
		return
	}
	contentType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || contentType != "application/json" {
		h.errorResponse(c, 415, "unsupported_media_type", "Video remix requires application/json with a prompt")
		c.Abort()
		return
	}
	idempotency := c.GetHeader("Idempotency-Key")
	if len(idempotency) > 200 || strings.TrimSpace(idempotency) != idempotency {
		h.errorResponse(c, 400, "invalid_request_error", "Invalid Idempotency-Key")
		c.Abort()
		return
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if !middleware.RejectRequestBodyReadError(c, "handler.video_remix", err) {
			h.errorResponse(c, 400, "invalid_request_error", "Cannot read video remix request")
		}
		c.Abort()
		return
	}
	if len(body) > 1<<20 {
		h.errorResponse(c, 413, "request_too_large", "Video remix requests must be at most 1 MiB")
		c.Abort()
		return
	}
	if trimmed := bytes.TrimSpace(body); !utf8.Valid(body) || !json.Valid(body) || len(trimmed) == 0 || trimmed[0] != '{' {
		h.errorResponse(c, 400, "invalid_request_error", "Video remix requires a JSON object")
		c.Abort()
		return
	}
	ctx := service.WithGenerationVideoRemix(c.Request.Context(), origin)
	existing, err := h.generations.ReplayVideo(ctx, key.UserID, key.ID, idempotency, body, service.GenerationProtocolOpenAI)
	if err == nil && existing != nil {
		writeGenerationCreated(c, existing)
		c.Abort()
		return
	}
	if err == nil {
		// The parent row includes private JSON snapshots; admit those before
		// reading it, even though only its public selectors leave the service.
		err = pkghttputil.ReserveRequestBodyMemory(ctx, 32<<20)
	}
	if err == nil {
		ctx, body, err = h.generations.PrepareVideoRemix(ctx, key, body)
	}
	if err != nil {
		if middleware.RejectRequestBodyMemoryOverload(c, err) {
			return
		}
		status, code, message := 503, "api_error", "Video remix is temporarily unavailable"
		switch {
		case errors.Is(err, service.ErrGenerationIdempotencyConflict):
			status, code, message = 409, "idempotency_conflict", "Idempotency-Key was already used for another request"
		case errors.Is(err, service.ErrGenerationTaskNotFound), errors.Is(err, service.ErrGenerationOrigin):
			status, code, message = 404, "video_not_found", "The origin video is unavailable"
		case errors.Is(err, service.ErrGenerationRemixUnsupported):
			status, code, message = 400, "video_remix_unsupported", "The origin video's plugin does not support remix"
		case errors.Is(err, service.ErrGenerationDecode):
			status, code, message = 400, "invalid_request_error", "Video remix accepts only a non-empty prompt of at most 8192 UTF-8 bytes"
		}
		h.errorResponse(c, status, code, message)
		c.Abort()
		return
	}
	c.Request = c.Request.WithContext(ctx)
	requestmodel.ResetRequestBody(c.Request, body)
	c.Next()
}
