package generation

import (
	"context"
	"errors"
	"time"
)

// Executor owns only the new durable task state machine. It cannot discover
// or claim the host's historical native video records.
type Executor struct {
	store                Store
	resolve              ResolveConnection
	afterSettlement      AfterSettlement
	billingPendingWindow time.Duration
	now                  func() time.Time
}

func NewExecutor(options ExecutorOptions) *Executor {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	window := options.BillingPendingWindow
	if window <= 0 {
		window = DefaultBillingPendingWindow
	}
	return &Executor{store: options.Store, resolve: options.Resolve, afterSettlement: options.AfterSettlement, billingPendingWindow: window, now: now}
}

func (e *Executor) Process(ctx context.Context, task *Task) error {
	if e.store == nil {
		return errors.New("generation task store unavailable")
	}
	if task == nil {
		return errors.New("generation task is unavailable")
	}
	if task.Status == Uncertain {
		return e.store.SaveProgress(ctx, task, time.Hour)
	}
	awaitingBill := BillingPendingFor(task)
	if (task.Status == Succeeded || task.Status == Failed) && !awaitingBill {
		return e.Settle(ctx, task)
	}
	if awaitingBill {
		pendingSince := task.CreatedAt
		if task.FinishedAt != nil && !task.FinishedAt.IsZero() {
			pendingSince = *task.FinishedAt
		} else if !task.UpdatedAt.IsZero() {
			pendingSince = task.UpdatedAt
		}
		if e.now().Sub(pendingSince) > e.billingPendingWindow {
			if task.Price.DeterministicWithoutFinalResponse() {
				return e.Settle(ctx, task)
			}
			task.BillingStatus = BillingReview
			task.ErrorCode = "upstream_billing_timeout"
			task.ErrorMessage = "The video is complete but the upstream final bill requires administrator review."
			return e.store.SaveProgress(ctx, task, time.Hour)
		}
	}
	if !awaitingBill && e.now().Sub(task.CreatedAt) > time.Hour {
		if task.Status == Submitting && task.UpstreamID == "" {
			task.Status = Failed
			task.BillingStatus = BillingPending
			now := e.now().UTC()
			task.FinishedAt = &now
			task.ErrorCode = "queue_expired"
			task.ErrorMessage = "The task expired before submission."
			if task.PreparationStatus != "" {
				task.PreparationStatus = "cleanup"
			}
		} else {
			task.BillingStatus = BillingReview
			task.ErrorCode = "generation_timeout"
			task.ErrorMessage = "The upstream task is taking too long and requires reconciliation."
		}
		return e.store.SaveProgress(ctx, task, time.Hour)
	}
	if e.resolve == nil {
		return errors.New("generation connection resolver unavailable")
	}
	connection, err := e.resolve(ctx, task)
	if err != nil || connection == nil {
		if task.PreparationStatus == "pending" {
			task.Status, task.BillingStatus, task.PreparationStatus = Failed, BillingPending, "cleanup"
			task.ErrorCode, task.ErrorMessage = "upload_account_unavailable", "The upstream reference connection is unavailable. The reservation will be released."
			now := e.now().UTC()
			task.FinishedAt = &now
			return e.store.SaveProgress(ctx, task, time.Second)
		}
		task.BillingStatus = BillingReview
		task.ErrorCode = "account_unavailable"
		task.ErrorMessage = "The upstream connection requires administrator review."
		return e.store.SaveProgress(ctx, task, time.Hour)
	}
	release, acquired, err := connection.Acquire(ctx)
	if errors.Is(err, ErrConcurrencyUnavailable) {
		return err
	}
	if err != nil || !acquired {
		if task.Status == Submitting {
			task.Status = Queued
		}
		return e.store.SaveProgress(ctx, task, 5*time.Second)
	}
	if release == nil {
		return errors.New("generation acquired slot has no release")
	}
	defer release()
	if task.Status == Submitting {
		if !connection.Schedulable() {
			task.Status = Queued
			return e.store.SaveProgress(ctx, task, time.Minute)
		}
		if task.PreparationStatus == "pending" {
			err = connection.Prepare(ctx, task)
			if err == nil {
				task.Status, task.PreparationStatus = Queued, "ready"
				task.ErrorCode, task.ErrorMessage = "", ""
				return e.store.SaveProgress(ctx, task, time.Second)
			}
			var preparation *PreparationError
			permanent := errors.As(err, &preparation) && preparation.Permanent
			if !permanent && e.now().Sub(task.CreatedAt) < 15*time.Minute {
				task.Status = Queued
				task.ErrorCode, task.ErrorMessage = "upload_retrying", "Reference upload is temporarily unavailable; it will resume without submitting another generation."
				delay := 5 * time.Second
				if preparation != nil && preparation.RetryAfter > delay {
					delay = min(preparation.RetryAfter, time.Hour)
				}
				return e.store.SaveProgress(ctx, task, delay)
			}
			task.Status, task.BillingStatus, task.PreparationStatus = Failed, BillingPending, "cleanup"
			task.ErrorCode, task.ErrorMessage = "reference_upload_failed", "Reference upload could not be completed. The reservation will be released."
			now := e.now().UTC()
			task.FinishedAt = &now
			return e.store.SaveProgress(ctx, task, time.Second)
		}
		err = connection.Submit(ctx, task)
		if err != nil {
			var submission *SubmissionError
			if errors.As(err, &submission) && !submission.Uncertain {
				task.Status = Failed
				task.BillingStatus = BillingPending
				task.ErrorCode = "upstream_rejected"
				task.ErrorMessage = "The upstream rejected this video request."
				var policy PolicyFailure
				if errors.As(err, &policy) {
					task.ErrorCode = "request_processing_failed"
					task.ErrorMessage = "Request-processing policy could not be applied. The reservation will be released."
					if policy.TaskPolicyRejected() {
						task.ErrorCode = "request_processing_rejected"
						task.ErrorMessage = "Request rejected by request-processing policy. The reservation will be released."
					}
				}
				now := e.now().UTC()
				task.FinishedAt = &now
				if task.PreparationStatus != "" {
					task.PreparationStatus = "cleanup"
				}
			} else {
				task.Status = Uncertain
				task.BillingStatus = BillingReview
				task.ErrorCode = "submission_uncertain"
				task.ErrorMessage = "The upstream submission requires reconciliation; it will not be submitted twice."
			}
		}
	} else {
		err = connection.Poll(ctx, task)
		if err != nil {
			task.ErrorCode = "poll_retrying"
			task.ErrorMessage = "The upstream status is temporarily unavailable."
		} else if task.ErrorCode == "poll_retrying" {
			task.ErrorCode = ""
			task.ErrorMessage = ""
		}
	}
	// Persist terminal facts before financial effects. A crash resumes settlement.
	if saveErr := e.store.SaveProgress(ctx, task, 10*time.Second); saveErr != nil {
		return saveErr
	}
	return err
}
