package handler

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"os"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/gin-gonic/gin"
)

// readTaskMultipart spools a bounded multipart/form-data upload into
// immediately-unlinked temporary files under the shared upload slot semaphore,
// 60s read deadline, 129 MiB / 4-file / 32 MiB-per-file caps, and 32 MiB
// ingress memory reservation. The caller must defer the returned cleanup func.
func (h *OpenAIGatewayHandler) readTaskMultipart(c *gin.Context, key *service.APIKey) (context.Context, []byte, func(), bool) {
	if h == nil || h.generations == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
		return nil, nil, nil, false
	}
	if encoding := c.GetHeader("Content-Encoding"); encoding != "" && encoding != "identity" {
		h.errorResponse(c, http.StatusUnsupportedMediaType, "unsupported_media_type", "Task multipart uploads require an uncompressed request body")
		return nil, nil, nil, false
	}
	if c.Request.ContentLength > service.GenerationMultipartMaxBytes {
		h.errorResponse(c, http.StatusRequestEntityTooLarge, "request_too_large", "Task multipart requests must be at most 129 MiB with at most four 32 MiB files")
		return nil, nil, nil, false
	}
	// Includes bounded JSON copies and the standard library's MIME header
	// parser (10 MiB maximum). File data itself is spooled in 32 KiB chunks;
	// release this transient parsing scratch once spooling finishes so later
	// policy and routing stages do not double-charge freed MIME buffers.
	scratchCtx, releaseScratch := pkghttputil.WithRequestBodyMemoryScope(c.Request.Context())
	defer releaseScratch()
	if err := pkghttputil.ReserveRequestBodyMemory(scratchCtx, 32<<20); err != nil {
		middleware.RejectRequestBodyMemoryOverload(c, err)
		return nil, nil, nil, false
	}
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetReadDeadline(time.Now().Add(60 * time.Second))
	defer controller.SetReadDeadline(time.Time{})
	readCtx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer c.Request.Body.Close()
	ctx, body, cleanup, err := h.generations.ReadVideoMultipart(readCtx, key, c.GetHeader("Content-Type"), c.Request.Body)
	_ = controller.SetReadDeadline(time.Time{})
	if err != nil {
		cancel()
		if !middleware.RejectRequestBodyReadError(c, "handler.video_multipart", err) {
			var pathError *os.PathError
			switch {
			case errors.Is(err, service.ErrGenerationFileBusy), errors.Is(err, service.ErrGenerationFileTooLarge):
				h.generationFileError(c, err)
			case errors.Is(err, pluginTask.ErrVideoMultipartTooLarge):
				h.errorResponse(c, http.StatusRequestEntityTooLarge, "request_too_large", "Task multipart text must be at most 1 MiB total and 512 KiB per field")
			case errors.As(err, &pathError):
				h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task upload storage is unavailable")
			default:
				h.errorResponse(c, http.StatusBadRequest, "invalid_video_request", "Invalid task multipart; use unique text fields and at most four supported media files; input_reference requires PNG, JPEG or WebP")
			}
		}
		return nil, nil, nil, false
	}
	return ctx, body, func() {
		cleanup()
		cancel()
	}, true
}

// GenerationMultipartIngress runs after authentication and before any generic
// model/body reader. Only the standard Videos endpoint accepts this envelope;
// existing vendor-native multipart paths keep their own contract.
func (h *OpenAIGatewayHandler) GenerationMultipartIngress(c *gin.Context) {
	if h == nil || h.generations == nil || c.Request.Method != http.MethodPost || c.Request.URL.Path != "/v1/videos" && c.Request.URL.Path != "/videos" {
		c.Next()
		return
	}
	kind, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || kind != "multipart/form-data" {
		c.Next()
		return
	}
	key, ok := h.generationFileIdentity(c)
	if !ok {
		c.Abort()
		return
	}
	ctx, body, cleanup, ok := h.readTaskMultipart(c, key)
	if !ok {
		c.Abort()
		return
	}
	defer cleanup()
	c.Request = c.Request.WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	requestmodel.ResetRequestBody(c.Request, body)
	c.Next()
}
