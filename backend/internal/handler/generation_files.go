package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/gin-gonic/gin"
)

// Video files are a documented Sub2API extension, registered before body
// inspection middleware so uploads stream without JSON/model buffering.
func (h *OpenAIGatewayHandler) GenerationFileUpload(c *gin.Context) {
	key, ok := h.generationFileIdentity(c)
	if !ok {
		return
	}
	if c.Request.ContentLength > pluginTask.MaxFileBytes {
		h.errorResponse(c, 413, "video_file_too_large", "Video files must be at most 32 MiB")
		return
	}
	if err := pkghttputil.ReserveRequestBodyMemory(c.Request.Context(), 256<<10); err != nil {
		middleware.RejectRequestBodyMemoryOverload(c, err)
		return
	}
	if _, _, err := service.ValidateGenerationFilename(c.GetHeader("X-Filename"), c.GetHeader("Content-Type")); err != nil {
		h.errorResponse(c, 400, "invalid_request_error", err.Error())
		return
	}
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetReadDeadline(time.Now().Add(60 * time.Second))
	defer controller.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	file, err := h.generations.UploadGenerationFile(ctx, key, c.GetHeader("X-Filename"), c.GetHeader("Content-Type"), c.Request.Body)
	if err != nil {
		if middleware.RejectRequestBodyReadError(c, "handler.video_file", err) {
			return
		}
		h.generationFileError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, file)
}

func (h *OpenAIGatewayHandler) GenerationFileGet(c *gin.Context) {
	key, ok := h.generationFileIdentity(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	file, err := h.generations.GetVideoFile(ctx, key, c.Param("file_id"))
	if err != nil {
		h.generationFileError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, file)
}

func (h *OpenAIGatewayHandler) GenerationFileDelete(c *gin.Context) {
	key, ok := h.generationFileIdentity(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := h.generations.DeleteVideoFile(ctx, key, c.Param("file_id")); err != nil {
		h.generationFileError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, gin.H{"id": c.Param("file_id"), "object": "video.file.deleted", "deleted": true})
}

func (h *OpenAIGatewayHandler) generationFileIdentity(c *gin.Context) (*service.APIKey, bool) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key.Group == nil || key.GroupID == nil {
		h.errorResponse(c, 403, "permission_error", "A video-enabled group is required")
		return nil, false
	}
	if !service.GroupAllowsImageGeneration(key.Group) {
		h.errorResponse(c, 403, "permission_error", service.ImageGenerationPermissionMessage())
		return nil, false
	}
	if h.generations == nil || !h.generations.HasVideoFileStorage() {
		h.errorResponse(c, 503, "api_error", "Video file service unavailable")
		return nil, false
	}
	return key, true
}

func (h *OpenAIGatewayHandler) generationFileError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrGenerationFileNotFound):
		h.errorResponse(c, 404, "video_file_not_found", "Video file not found or expired")
	case errors.Is(err, service.ErrGenerationFileQuota):
		c.Header("Retry-After", "60")
		h.errorResponse(c, 429, "video_file_quota", "Video file storage limit reached; delete unused files or retry after expiry")
	case errors.Is(err, service.ErrGenerationFileTooLarge):
		h.errorResponse(c, 413, "video_file_too_large", "Video files must contain 1 byte to 32 MiB")
	case errors.Is(err, service.ErrGenerationFileBusy):
		c.Header("Retry-After", "5")
		h.errorResponse(c, 503, "video_file_busy", "Video upload capacity is temporarily full; retry later")
	case errors.Is(err, service.ErrGenerationFileInUse):
		h.errorResponse(c, 409, "video_file_in_use", "Video file is retained by an accepted task")
	default:
		h.errorResponse(c, 503, "api_error", "Video file operation failed")
	}
}
