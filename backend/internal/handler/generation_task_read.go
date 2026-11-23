package handler

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) ownedPluginTask(c *gin.Context) (*service.GenerationTask, bool) {
	c.Header("Cache-Control", "private, no-store")
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || h.generations == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
		return nil, false
	}
	task, err := h.generations.GetPluginTask(c.Request.Context(), c.Param("key"), key)
	if errors.Is(err, service.ErrGenerationTaskNotFound) {
		h.errorResponse(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
		return nil, false
	}
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
		return nil, false
	}
	return task, true
}

func (h *OpenAIGatewayHandler) GenerationTaskGet(c *gin.Context) {
	if task, ok := h.ownedPluginTask(c); ok {
		if lyrics := task.SunoLyrics(); len(lyrics) > 0 {
			c.JSON(http.StatusOK, struct {
				service.GenerationTaskReadView
				Lyrics []service.GenerationTaskLyric `json:"lyrics"`
			}{task.PublicTaskReadView(), lyrics})
			return
		}
		c.JSON(http.StatusOK, task.PublicTaskReadView())
	}
}

// GenerationTaskCancel cancels an unclaimed queued task locally, or invokes
// an active task plugin's optional upstream cancel hook.
func (h *OpenAIGatewayHandler) GenerationTaskCancel(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || h.generations == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
		return
	}
	task, confirmed, err := h.generations.CancelPluginTask(c.Request.Context(), c.Param("key"), key)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrGenerationTaskNotFound):
			h.errorResponse(c, http.StatusNotFound, "task_not_found", "Task not found")
		case errors.Is(err, service.ErrGenerationTaskCannotCancel):
			h.errorResponse(c, http.StatusConflict, "task_cannot_cancel", "Task cannot be canceled in its current state or plugin has no cancel hook")
		default:
			h.errorResponse(c, http.StatusServiceUnavailable, "task_cancel_unavailable", "Task cancellation is temporarily unavailable")
		}
		return
	}
	if !confirmed {
		c.JSON(http.StatusAccepted, gin.H{"task_id": task.ID, "status": task.PublicTaskReadView().Status, "cancel_requested": true, "billing_status": task.BillingStatus})
		return
	}
	c.JSON(http.StatusOK, gin.H{"task_id": task.ID, "status": "canceled", "billing_status": task.BillingStatus})
}

type generationTaskArtifactView struct {
	Key        string `json:"key"`
	Type       string `json:"type"`
	MIMEType   string `json:"mime_type,omitempty"`
	ContentURL string `json:"content_url"`
}

func (h *OpenAIGatewayHandler) GenerationTaskDeleteResult(c *gin.Context) {
	h.deletePluginTaskResult(c, c.Param("key"), false)
}

func (h *OpenAIGatewayHandler) GenerationVideoDelete(c *gin.Context) {
	h.deletePluginTaskResult(c, c.Param("request_id"), true)
}

func (h *OpenAIGatewayHandler) deletePluginTaskResult(c *gin.Context, id string, videoOnly bool) {
	c.Header("Cache-Control", "private, no-store")
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || h.generations == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Task service is unavailable")
		return
	}
	err := h.generations.DeletePluginTaskResult(c.Request.Context(), id, key, videoOnly)
	switch {
	case errors.Is(err, service.ErrGenerationTaskNotFound):
		h.errorResponse(c, http.StatusNotFound, "task_not_found", "Task not found")
	case errors.Is(err, service.ErrGenerationTaskResultNotDeletable):
		h.errorResponse(c, http.StatusConflict, "task_result_in_use", err.Error())
	case err != nil:
		h.errorResponse(c, http.StatusServiceUnavailable, "task_delete_unavailable", "Task result deletion is temporarily unavailable")
	default:
		if videoOnly {
			c.JSON(http.StatusOK, gin.H{"id": id, "object": "video.deleted", "deleted": true})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func (h *OpenAIGatewayHandler) GenerationTaskArtifacts(c *gin.Context) {
	task, ok := h.ownedPluginTask(c)
	if !ok {
		return
	}
	artifacts, err := h.generations.ListTaskArtifacts(c.Request.Context(), task)
	if err != nil {
		if middleware.RejectRequestBodyMemoryOverload(c, err) {
			return
		}
		h.errorResponse(c, http.StatusServiceUnavailable, "artifact_plugin_unavailable", "Task artifacts are temporarily unavailable")
		return
	}
	items := make([]generationTaskArtifactView, 0, len(artifacts))
	for _, artifact := range artifacts {
		path := "/v1/tasks/" + url.PathEscape(task.ID) + "/artifacts/" + url.PathEscape(artifact.Key) + "/content"
		items = append(items, generationTaskArtifactView{Key: artifact.Key, Type: artifact.Type, MIMEType: artifact.MimeType, ContentURL: path})
	}
	c.JSON(http.StatusOK, gin.H{"task_id": task.ID, "artifacts": items})
}

func (h *OpenAIGatewayHandler) GenerationTaskArtifactContent(c *gin.Context) {
	task, ok := h.ownedPluginTask(c)
	if !ok {
		return
	}
	artifact := c.Param("artifact_key")
	if !pluginTask.ValidArtifactKey(artifact) {
		h.errorResponse(c, http.StatusNotFound, "artifact_not_found", "Task or artifact not found")
		return
	}
	if task.Status != service.GenerationSucceeded {
		h.errorResponse(c, http.StatusConflict, "artifact_not_ready", "Task artifacts are not ready")
		return
	}
	h.writeGenerationContent(c, task, artifact, true)
}
