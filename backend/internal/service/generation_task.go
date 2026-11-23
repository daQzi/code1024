package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/shopspring/decimal"
)

const (
	GenerationQueued          = generation.Queued
	GenerationSubmitting      = generation.Submitting
	GenerationUncertain       = generation.Uncertain
	GenerationRunning         = generation.Running
	GenerationSucceeded       = generation.Succeeded
	GenerationFailed          = generation.Failed
	GenerationDeliveryPending = generation.DeliveryPending
	GenerationDeliveryReady   = generation.DeliveryReady
	GenerationBillingHeld     = generation.BillingHeld
	GenerationBillingPending  = generation.BillingPending
	GenerationBillingSettled  = generation.BillingSettled
	GenerationBillingReview   = generation.BillingReview
)

var ErrGenerationTaskNotFound = infraerrors.New(http.StatusNotFound, "GENERATION_TASK_NOT_FOUND", "generation task not found").WithCause(generation.ErrTaskNotFound)
var ErrGenerationIdempotencyConflict = infraerrors.New(http.StatusConflict, "GENERATION_IDEMPOTENCY_CONFLICT", "idempotency key was used for a different request")
var ErrGenerationLeaseLost = generation.ErrLeaseLost
var ErrGenerationCapacity = infraerrors.New(http.StatusTooManyRequests, "GENERATION_CAPACITY", "active video task limit reached")

type GenerationTask = generation.Task
type GenerationTaskReadView = generation.TaskReadView
type GenerationTaskLyric = generation.TaskLyric
type GenerationConnection = generation.Connection
type GenerationVideoFields = generation.VideoFields

func generationVideoFields(body, decoded json.RawMessage) GenerationVideoFields {
	return generation.VideoFieldsFromRequest(body, decoded)
}

func NewGenerationTaskID() (string, error) { return generation.NewTaskID() }

// Preserve the host's public error contract when a domain snapshot is rejected.
func ValidateGenerationTaskSnapshot(task *GenerationTask) error {
	err := task.Validate()
	if errors.Is(err, generation.ErrOrigin) {
		return ErrGenerationOrigin
	}
	return err
}

type GenerationTaskRepository interface {
	generation.Store
	CreateHeld(context.Context, *GenerationTask, *UsageBillingCommand) (*GenerationTask, error)
	GetOwned(context.Context, string, int64, int64) (*GenerationTask, error)
	FindIdempotent(context.Context, int64, int64, string) (*GenerationTask, error)
	ListOwned(context.Context, int64, int64, string, int, string) ([]GenerationTask, error)
	ListReview(context.Context, int) ([]GenerationTask, error)
	Reconcile(context.Context, string, int64, string, string, string, *decimal.Decimal) error
}

// Upload checkpoints keep the current task lease. Cleanup has its own bounded
// claim path after settlement, so a storage outage cannot block a user refund.
type GenerationPreparationRepository interface {
	CheckpointPreparation(context.Context, *GenerationTask) error
	ClaimPreparationCleanup(context.Context, string, time.Duration) (*GenerationTask, error)
	FinishPreparationCleanup(context.Context, *GenerationTask, bool, time.Duration) error
}
