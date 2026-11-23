package generation

import (
	"errors"
	"time"
)

var ErrConcurrencyUnavailable = errors.New("generation concurrency service unavailable")

// An unclassified submit error is always uncertain. Only a driver that can
// prove the upstream did not accept a paid request may set Uncertain=false.
type SubmissionError struct {
	Uncertain bool
	Cause     error
}

func (e *SubmissionError) Error() string {
	if e.Cause == nil {
		return "generation submission failed"
	}
	return e.Cause.Error()
}
func (e *SubmissionError) Unwrap() error { return e.Cause }

// PreparationError describes resumable work before a paid submission. Hosts
// translate vendor-specific upload errors here instead of importing a vendor
// implementation into the executor.
type PreparationError struct {
	Permanent  bool
	RetryAfter time.Duration
	Cause      error
}

func (e *PreparationError) Error() string { return "generation preparation failed" }
func (e *PreparationError) Unwrap() error { return e.Cause }

// PolicyFailure exposes only the decision used by the state machine. The
// original failure may carry private request data and is never persisted.
type PolicyFailure interface {
	error
	TaskPolicyRejected() bool
}
