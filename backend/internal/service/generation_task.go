package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

const (
	GenerationQueued          = "queued"
	GenerationSubmitting      = "submitting"
	GenerationUncertain       = "submission_uncertain"
	GenerationRunning         = "running"
	GenerationSucceeded       = "succeeded"
	GenerationFailed          = "failed"
	GenerationDeliveryPending = "pending"
	GenerationDeliveryReady   = "ready"
	GenerationBillingHeld     = "held"
	GenerationBillingPending  = "pending"
	GenerationBillingSettled  = "settled"
	GenerationBillingReview   = "review"
)

var ErrGenerationTaskNotFound = infraerrors.New(http.StatusNotFound, "GENERATION_TASK_NOT_FOUND", "generation task not found")
var ErrGenerationIdempotencyConflict = infraerrors.New(http.StatusConflict, "GENERATION_IDEMPOTENCY_CONFLICT", "idempotency key was used for a different request")
var ErrGenerationLeaseLost = errors.New("generation task lease lost")
var ErrGenerationCapacity = infraerrors.New(http.StatusTooManyRequests, "GENERATION_CAPACITY", "active video task limit reached")

type GenerationTask struct {
	ID               string                  `json:"id"`
	UserID           int64                   `json:"-"`
	APIKeyID         int64                   `json:"-"`
	SubscriptionID   *int64                  `json:"-"`
	GroupID          int64                   `json:"-"`
	AccountID        int64                   `json:"-"`
	PluginVersionID  int64                   `json:"-"`
	IdempotencyKey   string                  `json:"-"`
	Fingerprint      string                  `json:"-"`
	Model            string                  `json:"model"`
	UpstreamModel    string                  `json:"-"`
	Platform         string                  `json:"-"`
	Protocol         string                  `json:"-"`
	Action           string                  `json:"-"`
	Request          json.RawMessage         `json:"-"`
	PublicFields     GenerationVideoFields   `json:"-"`
	ConfigEncrypted  string                  `json:"-"`
	Connection       GenerationConnection    `json:"-"`
	UpstreamID       string                  `json:"-"`
	State            json.RawMessage         `json:"-"`
	Data             json.RawMessage         `json:"-"`
	Usage            json.RawMessage         `json:"-"`
	Price            GenerationPriceSnapshot `json:"-"`
	Status           string                  `json:"status"`
	DeliveryStatus   string                  `json:"delivery_status"`
	BillingStatus    string                  `json:"billing_status"`
	Progress         int                     `json:"progress"`
	ErrorCode        string                  `json:"error_code,omitempty"`
	ErrorMessage     string                  `json:"error_message,omitempty"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
	FinishedAt       *time.Time              `json:"finished_at,omitempty"`
	NextRunAt        time.Time               `json:"-"`
	LeaseToken       string                  `json:"-"`
	Attempts         int                     `json:"-"`
	ReconciledAmount *decimal.Decimal        `json:"-"`
}

// Connection pins the destination while permitting credential rotation on the
// same account. Editing an account URL cannot move an already accepted task.
type GenerationConnection struct {
	BaseURL string `json:"base_url"`
	Kind    string `json:"kind"`
}

// GenerationVideoFields preserves only public protocol fields, separately from
// the plugin's rewritten request, opaque state and upstream credentials.
type GenerationVideoFields struct {
	Seconds string `json:"seconds,omitempty"`
	Size    string `json:"size,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

func generationVideoFields(body, decoded json.RawMessage) GenerationVideoFields {
	var input, upstream map[string]json.RawMessage
	_ = json.Unmarshal(body, &input)
	_ = json.Unmarshal(decoded, &upstream)
	text := func(raw json.RawMessage, maximum int) string {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			var number json.Number
			if json.Unmarshal(raw, &number) == nil {
				value = number.String()
			}
		}
		if len(value) > maximum {
			return ""
		}
		return value
	}
	fields := GenerationVideoFields{Seconds: text(input["seconds"], 24), Size: text(input["size"], 32), Prompt: text(input["prompt"], 8192)}
	if fields.Seconds == "" {
		fields.Seconds = text(upstream["duration"], 24)
	}
	return fields
}

// VideoView is shared by create, list and get. A plugin may add supported
// presentation fields, while host identity and lifecycle always win.
func (t *GenerationTask) VideoView() map[string]any {
	status := "queued"
	switch t.Status {
	case GenerationSubmitting, GenerationRunning, GenerationUncertain:
		status = "in_progress"
	case GenerationSucceeded:
		status = "completed"
	case GenerationFailed:
		status = "failed"
	}
	view := map[string]any{"id": t.ID, "object": "video", "model": t.Model, "status": status, "progress": t.Progress, "created_at": t.CreatedAt.Unix(), "seconds": t.PublicFields.Seconds, "size": t.PublicFields.Size, "prompt": nil, "completed_at": nil, "expires_at": nil, "error": nil, "remixed_from_video_id": nil}
	view["billing_status"] = t.BillingStatus
	view["delivery_status"] = t.DeliveryStatus
	if t.PublicFields.Prompt != "" {
		view["prompt"] = t.PublicFields.Prompt
	}
	if t.FinishedAt != nil {
		view["completed_at"] = t.FinishedAt.Unix()
	}
	if t.Status == GenerationFailed {
		view["error"] = map[string]any{"code": t.ErrorCode, "message": t.ErrorMessage}
	}
	return view
}

func NewGenerationTaskID() (string, error) {
	var bytes [20]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "video_" + hex.EncodeToString(bytes[:]), nil
}

func (t *GenerationTask) Validate() error {
	if t == nil || len(t.ID) != 46 || t.UserID <= 0 || t.APIKeyID <= 0 || t.GroupID <= 0 || t.AccountID <= 0 || t.PluginVersionID <= 0 || t.Model == "" || t.UpstreamModel == "" || t.Platform == "" || len(t.Fingerprint) != 64 || len(t.IdempotencyKey) > 200 || len(t.Request) > 1<<20 || !json.Valid(t.Request) {
		return errors.New("invalid generation task snapshot")
	}
	if len(t.Model) > 100 || len(t.UpstreamModel) > 100 || t.IdempotencyKey == "" {
		return errors.New("model names must fit the existing usage ledger and an idempotency identity is required")
	}
	u, err := url.Parse(t.Connection.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (t.Connection.Kind != "vendor" && t.Connection.Kind != "new_api") {
		return errors.New("invalid generation connection snapshot")
	}
	if err := t.Price.Rule.Validate(); err != nil {
		return err
	}
	if t.Price.Hold.IsNegative() {
		return errors.New("invalid generation hold")
	}
	return nil
}

type GenerationTaskRepository interface {
	CreateHeld(context.Context, *GenerationTask, *UsageBillingCommand) (*GenerationTask, error)
	GetOwned(context.Context, string, int64, int64) (*GenerationTask, error)
	FindIdempotent(context.Context, int64, int64, string) (*GenerationTask, error)
	ListOwned(context.Context, int64, int64, string, int, string) ([]GenerationTask, error)
	Claim(context.Context, string, time.Duration) (*GenerationTask, error)
	SaveProgress(context.Context, *GenerationTask, time.Duration) error
	Settle(context.Context, *GenerationTask, decimal.Decimal, *GenerationCost) error
	ListReview(context.Context, int) ([]GenerationTask, error)
	Reconcile(context.Context, string, int64, string, string, string, *decimal.Decimal) error
}
