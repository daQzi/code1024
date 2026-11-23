package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrNativeVideoMaintenanceConflict = infraerrors.New(http.StatusConflict, "NATIVE_VIDEO_MAINTENANCE_CONFLICT", "Task state or original connection does not allow this action; refresh and verify the upstream task")

type NativeVideoMaintenance struct {
	Observation      NativeVideoObservation
	Pending          *GrokVideoPendingBilling
	Usage            *NativeVideoFinalUsage
	ConnectionHash   string
	MonitorStatus    string
	SettlementStatus string
}

type NativeVideoMaintenanceRepository interface {
	GetNativeVideoMaintenance(context.Context, string) (*NativeVideoMaintenance, error)
	ResumeNativeVideo(context.Context, string, int64, string, string, string) error
}

func (t *NativeVideoMaintenance) AllowedActions() []string {
	if t == nil || t.Observation.Validate() != nil || t.Observation.Status == GenerationFailed {
		return nil
	}
	var actions []string
	validFunding := ValidateNativeVideoFunding(t.Observation, t.Pending) == nil
	if t.MonitorStatus == NativeVideoMonitorReview && t.ConnectionHash != "" && (!nativeVideoPollDone(t.Observation.Status, t.Observation.DeliveryStatus) || t.SettlementStatus == "awaiting_usage") && (t.SettlementStatus != "awaiting_usage" || validFunding) {
		actions = append(actions, "resume_poll")
	}
	if t.SettlementStatus == "review" && validFunding && t.Usage != nil {
		if _, err := t.Usage.Normalize(t.Pending); err == nil {
			actions = append(actions, "retry_settlement")
		}
	}
	return actions
}

// MaintainNativeVideo can only retry existing GET/settlement work. It cannot
// replace an upstream ID, connection, captured price, funding or final units.
func (s *GenerationService) MaintainNativeVideo(ctx context.Context, id string, actor int64, action, reason string) error {
	if actor <= 0 || !strings.HasPrefix(id, "native_video_") || len(id) != 77 || action != "resume_poll" && action != "retry_settlement" || len(strings.TrimSpace(reason)) < 4 || len(reason) > 2000 || strings.ContainsRune(reason, 0) {
		return infraerrors.New(http.StatusBadRequest, "INVALID_NATIVE_VIDEO_ACTION", "A valid native task, action and review reason (4–2000 characters) are required")
	}
	repo, ok := s.repo.(NativeVideoMaintenanceRepository)
	if !ok {
		return errors.New("native video maintenance unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	task, err := repo.GetNativeVideoMaintenance(ctx, id)
	if err != nil {
		return err
	}
	if task == nil || task.Observation.Validate() != nil || NativeVideoTaskID(task.Observation) != id || task.Observation.Status == GenerationFailed {
		return ErrNativeVideoMaintenanceConflict
	}
	if action == "resume_poll" {
		if task.MonitorStatus != NativeVideoMonitorReview || task.ConnectionHash == "" || nativeVideoPollDone(task.Observation.Status, task.Observation.DeliveryStatus) && task.SettlementStatus != "awaiting_usage" || s.accounts == nil || s.gateway == nil {
			return ErrNativeVideoMaintenanceConflict
		}
		account, err := s.accounts.GetByID(ctx, task.Observation.AccountID)
		if err != nil {
			return err
		}
		if account == nil || !account.IsActive() {
			return ErrNativeVideoMaintenanceConflict
		}
		hash, err := s.gateway.NativeVideoPollConnectionHash(account, task.Observation.Protocol, task.Observation.UpstreamID)
		if err != nil || hash != task.ConnectionHash {
			return ErrNativeVideoMaintenanceConflict
		}
		if task.SettlementStatus == "awaiting_usage" && ValidateNativeVideoFunding(task.Observation, task.Pending) != nil {
			return ErrNativeVideoMaintenanceConflict
		}
	} else {
		if task.SettlementStatus != "review" || task.Usage == nil || ValidateNativeVideoFunding(task.Observation, task.Pending) != nil {
			return ErrNativeVideoMaintenanceConflict
		}
		if _, err := task.Usage.Normalize(task.Pending); err != nil {
			return ErrNativeVideoMaintenanceConflict
		}
	}
	return repo.ResumeNativeVideo(ctx, id, actor, action, strings.TrimSpace(reason), task.ConnectionHash)
}
