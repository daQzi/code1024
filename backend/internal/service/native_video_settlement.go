package service

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"
)

// NativeVideoFinalUsage contains only normalized final units, never a URL,
// credential or an upstream response. Its first durable observation wins.
type NativeVideoFinalUsage struct {
	VideoCount      int       `json:"video_count,omitempty"`
	DurationSeconds int       `json:"duration_seconds,omitempty"`
	OutputTokens    int       `json:"output_tokens,omitempty"`
	ObservedAt      time.Time `json:"observed_at"`
}

func NativeVideoUsageObservation(r *OpenAIForwardResult, at time.Time) *NativeVideoFinalUsage {
	if r == nil || r.VideoCount <= 0 && r.Usage.OutputTokens <= 0 {
		return nil
	}
	return &NativeVideoFinalUsage{VideoCount: r.VideoCount, DurationSeconds: r.VideoDurationSeconds, OutputTokens: r.Usage.OutputTokens, ObservedAt: at.UTC()}
}

func (u NativeVideoFinalUsage) Normalize(p *GrokVideoPendingBilling) (*NativeVideoFinalUsage, error) {
	if p == nil || p.Financial == nil || p.ValidateNativeVideoSnapshot() != nil || u.ObservedAt.IsZero() {
		return nil, errors.New("native video final usage context unavailable")
	}
	if p.Financial.Protocol == GenerationProtocolXAI {
		if u.VideoCount != 1 || u.OutputTokens != 0 || u.DurationSeconds < 0 {
			return nil, errors.New("invalid native video final seconds")
		}
		if u.DurationSeconds == 0 {
			u.DurationSeconds = p.VideoDurationSeconds
		}
		u.DurationSeconds = NormalizeVideoBillingDurationSecondsOrDefault(u.DurationSeconds)
	} else if u.VideoCount != 0 || u.DurationSeconds != 0 || u.OutputTokens <= 0 || u.OutputTokens > math.MaxInt32 {
		return nil, errors.New("invalid native video final tokens")
	}
	return &u, nil
}

func (u *NativeVideoFinalUsage) Result(p *NativeVideoBillingSnapshot, created time.Time) *OpenAIForwardResult {
	r := &OpenAIForwardResult{ResponseID: p.TaskID, NativeVideoBilling: p, Model: p.Model, BillingModel: p.Model, UpstreamModel: p.UpstreamModel,
		VideoCount: u.VideoCount, VideoDurationSeconds: u.DurationSeconds, VideoResolution: p.Resolution, Duration: u.ObservedAt.Sub(created)}
	r.Usage.OutputTokens = u.OutputTokens
	return r
}

type NativeVideoFinalUsageRepository interface {
	ResolveNativeVideoFinalUsage(context.Context, *NativeVideoBillingSnapshot, *NativeVideoFinalUsage) (*NativeVideoFinalUsage, error)
}

type NativeVideoSettlement struct {
	Observation NativeVideoObservation
	Pending     *GrokVideoPendingBilling
	Usage       *NativeVideoFinalUsage
	LeaseToken  string
	Failures    int
}

// ValidateNativeVideoFunding checks the recorded owner again on recovery. A
// well-formed snapshot is not evidence that it belongs to this particular job.
func ValidateNativeVideoFunding(o NativeVideoObservation, p *GrokVideoPendingBilling) error {
	if o.Validate() != nil || o.CreatedAt.IsZero() || p == nil || p.Financial == nil || p.ValidateNativeVideoSnapshot() != nil {
		return errors.New("invalid native video settlement evidence")
	}
	f := p.Financial
	if f.UserID != o.UserID || f.APIKeyID != o.APIKeyID || f.GroupID != o.GroupID || f.AccountID != o.AccountID || f.Protocol != o.Protocol || f.TaskID != o.UpstreamID || o.Status == GenerationFailed {
		return errors.New("native video settlement ownership mismatch")
	}
	return nil
}

type NativeVideoSettlementRepository interface {
	ClaimNativeVideoSettlement(context.Context, string, time.Duration) (*NativeVideoSettlement, error)
	SaveNativeVideoSettlement(context.Context, *NativeVideoSettlement, string, string, time.Duration) error
}

func (s *NativeVideoWorkers) workNativeVideoSettlements(ctx context.Context, repo NativeVideoSettlementRepository) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		task, err := repo.ClaimNativeVideoSettlement(callCtx, uuid.NewString(), 2*time.Minute)
		if err == nil {
			err = s.processNativeVideoSettlement(callCtx, repo, task)
		}
		cancel()
		if err != nil && !errors.Is(err, ErrGenerationTaskNotFound) && !errors.Is(err, context.Canceled) {
			slog.Warn("native_video_settlement", "code", "settlement_persistence_failed")
		}
	}
}

func (s *NativeVideoWorkers) processNativeVideoSettlement(ctx context.Context, repo NativeVideoSettlementRepository, task *NativeVideoSettlement) error {
	if task == nil {
		return errors.New("native video settlement missing")
	}
	p := task.Pending
	status, code := "settled", ""
	if ValidateNativeVideoFunding(task.Observation, p) != nil || task.Usage == nil {
		status, code = "review", "native_billing_evidence_invalid"
	} else {
		u, err := task.Usage.Normalize(p)
		if err != nil {
			status, code = "review", "native_billing_evidence_invalid"
		} else {
			f := p.Financial
			// Funding is captured; no current price, subscription or account
			// credential lookup may change who pays this already accepted task.
			group := f.GroupID
			input := &OpenAIRecordUsageInput{Result: u.Result(f, task.Observation.CreatedAt),
				User: &User{ID: f.UserID}, APIKey: &APIKey{ID: f.APIKeyID, UserID: f.UserID, GroupID: &group},
				Account: &Account{ID: f.AccountID, Type: AccountTypeAPIKey}, APIKeyService: s.keys}
			if err := s.gateway.RecordUsage(ctx, input); err != nil {
				status, code = "ready", "native_billing_retrying"
				if errors.Is(err, ErrUsageBillingRequestConflict) || task.Failures >= 9 {
					status, code = "review", "native_billing_review"
				}
			} else if f.Mode != "standard" {
				status = "logged"
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return repo.SaveNativeVideoSettlement(ctx, task, status, code, 15*time.Second*time.Duration(1<<min(task.Failures, 4)))
}
