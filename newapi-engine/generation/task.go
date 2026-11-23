package generation

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/shopspring/decimal"
)

const (
	Queued          = "queued"
	Submitting      = "submitting"
	Uncertain       = "submission_uncertain"
	Running         = "running"
	Succeeded       = "succeeded"
	Failed          = "failed"
	DeliveryPending = "pending"
	DeliveryReady   = "ready"
	BillingHeld     = "held"
	BillingPending  = "pending"
	BillingSettled  = "settled"
	BillingReview   = "review"
)

type Task struct {
	ID                      string            `json:"id"`
	UserID                  int64             `json:"-"`
	APIKeyID                int64             `json:"-"`
	SubscriptionID          *int64            `json:"-"`
	GroupID                 int64             `json:"-"`
	AccountID               int64             `json:"-"`
	PluginVersionID         int64             `json:"-"`
	IdempotencyKey          string            `json:"-"`
	Fingerprint             string            `json:"-"`
	Model                   string            `json:"model"`
	UpstreamModel           string            `json:"-"`
	Platform                string            `json:"-"`
	Protocol                string            `json:"-"`
	Action                  string            `json:"-"`
	Request                 json.RawMessage   `json:"-"`
	PublicFields            VideoFields       `json:"-"`
	Files                   []FileSnapshot    `json:"-"`
	Origins                 []OriginTask      `json:"-"`
	ConfigEncrypted         string            `json:"-"`
	RequestPluginsEncrypted string            `json:"-"`
	RequestReportEncrypted  string            `json:"-"`
	PreparationStatus       string            `json:"-"`
	PreparationEncrypted    string            `json:"-"`
	Connection              Connection        `json:"-"`
	CredentialBinding       CredentialBinding `json:"-"`
	UpstreamID              string            `json:"-"`
	State                   json.RawMessage   `json:"-"`
	Data                    json.RawMessage   `json:"-"`
	Usage                   json.RawMessage   `json:"-"`
	Price                   PriceSnapshot     `json:"-"`
	Status                  string            `json:"status"`
	DeliveryStatus          string            `json:"delivery_status"`
	BillingStatus           string            `json:"billing_status"`
	Progress                int               `json:"progress"`
	ErrorCode               string            `json:"error_code,omitempty"`
	ErrorMessage            string            `json:"error_message,omitempty"`
	CreatedAt               time.Time         `json:"created_at"`
	UpdatedAt               time.Time         `json:"updated_at"`
	FinishedAt              *time.Time        `json:"finished_at,omitempty"`
	PublicDeletedAt         *time.Time        `json:"-"`
	NextRunAt               time.Time         `json:"-"`
	LeaseToken              string            `json:"-"`
	Attempts                int               `json:"-"`
	ReconciledAmount        *decimal.Decimal  `json:"-"`
}

// Connection pins the destination while permitting credential rotation on the
// same account. Editing an account URL cannot move an already accepted task.
type Connection struct {
	BaseURL string `json:"base_url"`
	Kind    string `json:"kind"`
}

// VideoFields preserves only public protocol fields, separately from
// the plugin's rewritten request, opaque state and upstream credentials.
type VideoFields struct {
	Seconds             string `json:"seconds,omitempty"`
	Size                string `json:"size,omitempty"`
	Prompt              string `json:"prompt,omitempty"`
	ResponsesBackground bool   `json:"responses_background,omitempty"`
}

func VideoFieldsFromRequest(body, decoded json.RawMessage) VideoFields {
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
	fields := VideoFields{Seconds: text(input["seconds"], 24), Size: text(input["size"], 32), Prompt: text(input["prompt"], 8192)}
	if fields.Seconds == "" {
		fields.Seconds = text(upstream["duration"], 24)
	}
	return fields
}

// VideoView is shared by create, list and get. A plugin may add supported
// presentation fields, while host identity and lifecycle always win.
func (t *Task) VideoView() map[string]any {
	status := "queued"
	switch t.Status {
	case Submitting, Running, Uncertain:
		status = "in_progress"
	case Succeeded:
		status = "completed"
	case Failed:
		status = "failed"
	}
	view := map[string]any{"id": t.ID, "object": "video", "model": t.Model, "status": status, "progress": t.Progress, "created_at": t.CreatedAt.Unix(), "seconds": t.PublicFields.Seconds, "size": t.PublicFields.Size, "prompt": nil, "completed_at": nil, "expires_at": nil, "error": nil, "remixed_from_video_id": nil}
	if len(t.Origins) == 1 {
		view["remixed_from_video_id"] = t.Origins[0].TaskID
	}
	view["billing_status"] = t.BillingStatus
	view["delivery_status"] = t.DeliveryStatus
	if t.PublicFields.Prompt != "" {
		view["prompt"] = t.PublicFields.Prompt
	}
	if t.FinishedAt != nil {
		view["completed_at"] = t.FinishedAt.Unix()
	}
	if t.Status == Failed {
		view["error"] = map[string]any{"code": t.ErrorCode, "message": t.ErrorMessage}
	}
	return view
}

func NewTaskID() (string, error) {
	var bytes [20]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "video_" + hex.EncodeToString(bytes[:]), nil
}

func (t *Task) Validate() error {
	if t == nil || len(t.ID) != 46 || t.UserID <= 0 || t.APIKeyID <= 0 || t.GroupID <= 0 || t.AccountID <= 0 || t.PluginVersionID <= 0 || t.Model == "" || t.UpstreamModel == "" || t.Platform == "" || len(t.Fingerprint) != 64 || len(t.IdempotencyKey) > 200 || len(t.Request) > 1<<20 || !json.Valid(t.Request) {
		return errors.New("invalid generation task snapshot")
	}
	if len(t.Model) > 100 || len(t.UpstreamModel) > 100 || t.IdempotencyKey == "" {
		return errors.New("model names must fit the existing usage ledger and an idempotency identity is required")
	}
	if len(t.RequestPluginsEncrypted) > MaxRequestPolicyEncrypted {
		return errors.New("task request policy exceeds snapshot limit")
	}
	if len(t.RequestReportEncrypted) > MaxReportEncrypted {
		return errors.New("task report exceeds snapshot limit")
	}
	if t.PreparationStatus != "" && t.PreparationStatus != "pending" || t.PreparationEncrypted != "" {
		return errors.New("new tasks cannot supply prepared upstream objects")
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
	if err := t.CredentialBinding.Validate(); err != nil {
		return err
	}
	if t.Price.Rule.Mode == "expression" {
		if t.Price.Expression == nil || t.Price.Expression.SHA256 != HashPayload([]byte(t.Price.Rule.Expression)) || t.Price.GroupMultiplier.IsNegative() || !t.Price.Hold.Equal(t.Price.Rule.Maximum.Mul(t.Price.GroupMultiplier).Round(MonetaryScale)) {
			return errors.New("invalid frozen task expression price")
		}
		if raw, err := json.Marshal(t.Price.Expression); err != nil || len(raw) > 64<<10 {
			return errors.New("task expression snapshot exceeds 64 KiB")
		}
	}
	if err := ValidateOrigins(t.Origins); err != nil {
		return err
	}
	if len(t.Files) > pluginTask.MaxTaskFiles {
		return errors.New("too many task files")
	}
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, file := range t.Files {
		if !fileIDPattern.MatchString(file.ID) || len(file.SHA256) != 64 || !pluginTask.ValidVideoFileField(file.Field) || file.Ref != FileRef(file.Field, counts[file.Field]) || file.Size <= 0 || file.Size > pluginTask.MaxFileBytes || seen[file.Ref] {
			return errors.New("invalid task file snapshot")
		}
		seen[file.Ref] = true
		counts[file.Field]++
		if _, _, err := ValidateFilename(file.Filename, file.MimeType); err != nil {
			return err
		}
	}
	return nil
}
