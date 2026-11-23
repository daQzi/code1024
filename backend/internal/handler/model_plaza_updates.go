package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const modelPlazaNotificationInterval = 2 * time.Second
const modelPlazaNotificationLifetime = 20 * time.Second

func (h *ModelPlazaHandler) CloseNotifications() {
	if h != nil && h.notificationCancel != nil {
		h.notificationCancel()
	}
}

func plazaNotificationIDs(raw string) (map[string]bool, error) {
	ids := strings.Split(raw, ",")
	if len(ids) > 24 {
		return nil, errors.New("at most 24 model IDs are allowed")
	}
	result := make(map[string]bool, len(ids))
	for _, id := range ids {
		if len(id) != 36 || !strings.HasPrefix(id, "mdl_") || strings.Trim(id[4:], "0123456789abcdef") != "" {
			return nil, errors.New("invalid model ID")
		}
		result[id] = true
	}
	return result, nil
}

// Notifications contain no metric values, names or private task facts. Each
// connection is short-lived, re-checks visibility every tick, and only advises
// clients to call the ordinary authenticated metrics endpoints again.
func (h *ModelPlazaHandler) WatchPerformance(c *gin.Context) {
	ids, err := plazaNotificationIDs(c.Query("ids"))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	rawGroup := c.Query("group_id")
	if rawGroup == "" {
		response.BadRequest(c, "A group ID is required")
		return
	}
	subject, authed := middleware.GetAuthSubjectFromContext(c)
	var userID int64
	if authed {
		userID = subject.UserID
	}
	role, _ := middleware.GetUserRoleFromContext(c)
	var token string
	if parts := strings.Fields(c.GetHeader("Authorization")); len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		token = parts[1]
	}
	load := func(ctx context.Context) ([]service.PlazaMetricsScope, error) {
		if authed {
			if h.authService == nil || h.apiKeyService == nil {
				return nil, infraerrors.Unauthorized("SESSION_UNAVAILABLE", "Session cannot be verified")
			}
			claims, err := h.authService.ValidateToken(token)
			if err != nil || claims.UserID != userID {
				return nil, infraerrors.Unauthorized("SESSION_CHANGED", "Session is no longer valid")
			}
			role, err = h.apiKeyService.ModelPlazaSessionRole(ctx, userID, claims.TokenVersion)
			if err != nil {
				return nil, err
			}
		}
		if h.settingService != nil && h.settingService.IsBackendModeEnabled(ctx) && role != "admin" {
			return nil, infraerrors.New(http.StatusForbidden, "BACKEND_MODE", "Model plaza is not available")
		}
		_, visible, err := h.visibleScope(ctx, userID, rawGroup, "")
		if err != nil {
			return nil, err
		}
		scopes := plazaSummaryScopes(visible, ids)
		if len(scopes) != len(ids) {
			return nil, infraerrors.NotFound("MODEL_NOT_FOUND", "Model not found")
		}
		return scopes, nil
	}
	// Bound the entire stream (including reads/checks) and reject excess streams
	// without creating a waiting goroutine or queue.
	ctx, cancel := context.WithTimeout(c.Request.Context(), modelPlazaNotificationLifetime)
	defer cancel()
	if h.notificationContext == nil || h.notificationContext.Err() != nil {
		response.Error(c, http.StatusServiceUnavailable, "Performance notifications are stopping")
		return
	}
	unregister := context.AfterFunc(h.notificationContext, cancel)
	defer unregister()
	select {
	case h.notificationSlots <- struct{}{}:
		defer func() { <-h.notificationSlots }()
	default:
		c.Header("Retry-After", "15")
		response.Error(c, http.StatusServiceUnavailable, "Performance notifications are busy")
		return
	}
	initialCtx, initialCancel := context.WithTimeout(ctx, 2*time.Second)
	scopes, err := load(initialCtx)
	initialCancel()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	previous, err := h.plazaService.PerformanceVersion(ctx, scopes)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Performance notifications are unavailable")
		return
	}
	controller := http.NewResponseController(c.Writer)
	if err := controller.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Performance notifications require write deadlines")
		return
	}
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	write := func(frame string) bool {
		if ctx.Err() != nil {
			return false
		}
		if err := controller.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
			return false
		}
		if _, err := io.WriteString(c.Writer, frame); err != nil {
			return false
		}
		return controller.Flush() == nil
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Accel-Buffering", "no")
	if !write("event: ready\ndata: {}\n\n") {
		return
	}
	ticker := time.NewTicker(modelPlazaNotificationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkCtx, checkCancel := context.WithTimeout(ctx, 2*time.Second)
			scopes, err = load(checkCtx)
			var current string
			if err == nil {
				current, err = h.plazaService.PerformanceVersion(checkCtx, scopes)
			}
			checkCancel()
			if err != nil {
				write("event: scope_changed\ndata: {}\n\n")
				return
			}
			if current != previous {
				previous = current
				if !write("event: refresh\ndata: {}\n\n") {
					return
				}
			} else if !write(": keepalive\n\n") {
				return
			}
		}
	}
}
