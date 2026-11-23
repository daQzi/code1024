package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *NewAPIPluginHandler) PluginCredential(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid account ID")
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Authentication required")
		return
	}
	actor, auth := strconv.FormatInt(subject.UserID, 10), ""
	if session := c.GetString(middleware.ContextKeySessionID); session != "" {
		sum := sha256.Sum256([]byte(session))
		auth = hex.EncodeToString(sum[:])
	}
	var view *service.PluginCredentialView
	if c.Request.Method == http.MethodGet {
		view, err = h.newAPI.PluginCredentialStatus(c.Request.Context(), id, actor, auth)
	} else {
		var input struct {
			Revision int64                    `json:"revision"`
			Action   string                   `json:"action"`
			Input    service.PluginLoginInput `json:"input"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Revision < 0 {
			response.BadRequest(c, "invalid credential operation")
			return
		}
		if (input.Action == "login" || input.Action == "continue") && auth == "" {
			response.BadRequest(c, "plugin login requires a signed-in administrator session")
			return
		}
		view, err = h.newAPI.ManagePluginCredential(c.Request.Context(), id, input.Revision, actor, auth, input.Action, input.Input)
	}
	if err != nil {
		if middleware.RejectRequestBodyMemoryOverload(c, err) {
			return
		}
		if errors.Is(err, service.ErrPluginCredentialConflict) || errors.Is(err, service.ErrPluginCredentialIdentity) {
			response.Error(c, http.StatusConflict, "credential state changed or maintenance is in progress; refresh and retry")
		} else {
			response.BadRequest(c, "plugin credential operation failed; refresh status and check the login step, plugin and connection")
		}
		return
	}
	response.Success(c, view)
}

func (h *NewAPIPluginHandler) CredentialRevocations(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	if _, ok := middleware.GetAuthSubjectFromContext(c); !ok {
		response.Unauthorized(c, "Authentication required")
		return
	}
	if h.newAPI == nil {
		response.Error(c, http.StatusServiceUnavailable, "plugin credential cleanup is unavailable")
		return
	}
	if c.Request.Method == http.MethodGet {
		jobs, err := h.newAPI.ListCredentialRevocations(c.Request.Context())
		if err != nil {
			response.Error(c, http.StatusServiceUnavailable, "plugin credential cleanup is unavailable")
			return
		}
		response.Success(c, jobs)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid credential cleanup ID")
		return
	}
	if err = h.newAPI.RetryCredentialRevocation(c.Request.Context(), id); err != nil {
		response.Error(c, http.StatusConflict, "credential cleanup changed or is in progress; refresh and retry")
		return
	}
	response.Success(c, gin.H{"queued": true})
}
