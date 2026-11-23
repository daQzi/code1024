package service

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
)

var ErrGenerationTaskCannotCancel = errors.New("only an unclaimed queued task can be canceled")
var ErrGenerationCancelUnavailable = errors.New("task cancellation is unavailable")

const GenerationCanceledBeforeSubmit = generation.CanceledBeforeSubmit
const GenerationCanceledUpstream = generation.CanceledUpstream

type GenerationCancelRepository interface {
	CancelQueuedTask(context.Context, string, int64, int64, int64) (*GenerationTask, error)
}

type GenerationUpstreamCancelRepository interface {
	ConfirmUpstreamCancelTask(ctx context.Context, id string, user, key, group int64) (*GenerationTask, error)
}

// CancelQueuedPluginTask never contacts a provider. The store serializes
// cancellation with worker claim; the original executor releases the hold.
func (s *GenerationService) CancelQueuedPluginTask(ctx context.Context, id string, key *APIKey) (*GenerationTask, error) {
	// Identity is also checked again inside the write transaction.
	if !generation.IsPublicTaskID(id) || key == nil || key.GroupID == nil {
		return nil, ErrGenerationTaskNotFound
	}
	repo, ok := s.repo.(GenerationCancelRepository)
	if !ok {
		return nil, ErrGenerationCancelUnavailable
	}
	return repo.CancelQueuedTask(ctx, id, key.UserID, key.ID, *key.GroupID)
}

// CancelPluginTask cancels a queued task locally or invokes an active plugin
// task's optional buildCancelRequest/parseCancelResponse hooks. confirmed is
// true only when cancellation is durably committed as terminal; unconfirmed
// upstream cancel requests return (task, false, nil) without refunding.
func (s *GenerationService) CancelPluginTask(ctx context.Context, id string, key *APIKey) (*GenerationTask, bool, error) {
	task, err := s.CancelQueuedPluginTask(ctx, id, key)
	if err == nil {
		return task, true, nil
	}
	if !errors.Is(err, ErrGenerationTaskCannotCancel) {
		return nil, false, err
	}
	current, err := s.GetPluginTask(ctx, id, key)
	if err != nil {
		return nil, false, err
	}
	if current.Status == GenerationFailed && (current.ErrorCode == GenerationCanceledBeforeSubmit || current.ErrorCode == GenerationCanceledUpstream) {
		return current, true, nil
	}
	if (current.Status != GenerationRunning && current.Status != GenerationSubmitting) || current.UpstreamID == "" || current.BillingStatus != GenerationBillingHeld {
		return nil, false, ErrGenerationTaskCannotCancel
	}
	upstreamRepo, ok := s.repo.(GenerationUpstreamCancelRepository)
	if !ok || s.plugins == nil || s.accounts == nil {
		return nil, false, ErrGenerationTaskCannotCancel
	}
	account, err := s.accounts.GetByID(ctx, current.AccountID)
	if err != nil || account == nil {
		return nil, false, ErrGenerationCancelUnavailable
	}
	outcome, supported, err := s.plugins.CancelTask(ctx, account, current)
	if err != nil {
		return nil, false, err
	}
	if !supported {
		return nil, false, ErrGenerationTaskCannotCancel
	}
	if !outcome.Confirmed {
		return current, false, nil
	}
	canceled, err := upstreamRepo.ConfirmUpstreamCancelTask(ctx, id, key.UserID, key.ID, *key.GroupID)
	if err != nil {
		return nil, false, err
	}
	return canceled, true, nil
}
