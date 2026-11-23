package service

import (
	"context"
	"errors"
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/query"
)

type TaskLog = query.Log
type TaskLogEvent = query.Event
type TaskLogBillingSummary = query.BillingSummary
type TaskLogDetail = query.Detail
type TaskLogFilter = query.Filter
type TaskLogRepository = query.Repository

func taskLogError(err error) error {
	if errors.Is(err, generation.ErrTaskNotFound) {
		return ErrGenerationTaskNotFound
	}
	return err
}

func (s *GenerationService) TaskLogs(ctx context.Context, filter TaskLogFilter, admin bool) ([]TaskLog, int64, error) {
	repo, _ := s.repo.(TaskLogRepository)
	items, total, err := query.NewService(repo).List(ctx, query.Access{UserID: filter.UserID, Administrator: admin}, filter)
	return items, total, taskLogError(err)
}

func (s *GenerationService) TaskLog(ctx context.Context, id string, owner int64, admin bool) (*TaskLogDetail, error) {
	repo, _ := s.repo.(TaskLogRepository)
	item, err := query.NewService(repo).Get(ctx, query.Access{UserID: owner, Administrator: admin}, id)
	if err != nil {
		return nil, taskLogError(err)
	}
	if admin && item.Source == "native" {
		if maintenance, ok := s.repo.(NativeVideoMaintenanceRepository); ok {
			task, err := maintenance.GetNativeVideoMaintenance(ctx, id)
			if err != nil {
				if !errors.Is(err, ErrNativeVideoMaintenanceConflict) {
					return nil, err
				}
			} else {
				item.NativeActions = task.AllowedActions()
			}
		}
	}
	return item, nil
}

func (s *GenerationService) TaskLogContent(ctx context.Context, id string, owner int64, admin bool, headers http.Header) (*http.Response, context.CancelFunc, error) {
	repo, ok := s.repo.(TaskLogRepository)
	if !ok || !admin && owner <= 0 {
		return nil, nil, ErrGenerationTaskNotFound
	}
	item, err := repo.GetTaskLog(ctx, id, owner)
	if err != nil {
		return nil, nil, err
	}
	if item.Status != GenerationSucceeded || item.DeliveryStatus != GenerationDeliveryReady {
		return nil, nil, infraerrors.New(http.StatusConflict, "TASK_CONTENT_NOT_READY", "Task content is not available")
	}
	if item.Source == "native" {
		return nil, nil, infraerrors.New(http.StatusConflict, "NATIVE_TASK_CONTENT_UNAVAILABLE", "Use the original native video content endpoint for this task")
	}
	task, err := s.repo.GetOwned(ctx, id, item.UserID, item.APIKeyID)
	if err != nil {
		return nil, nil, err
	}
	if !IsVideoGenerationProtocol(task.Protocol) {
		return nil, nil, infraerrors.New(http.StatusConflict, "TASK_CONTENT_NOT_VIDEO", "Use /v1/tasks/:key/artifacts to read non-video task artifacts")
	}
	return s.Content(ctx, task, http.MethodGet, "video", headers)
}
