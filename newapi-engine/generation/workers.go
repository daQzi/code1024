package generation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const ClaimTTL = 2 * time.Minute
const ExecutionTimeout = 100 * time.Second

// RunOnce claims exactly one task. The database transaction ends before any
// upstream I/O starts. No retry here may resubmit a lost submitting lease.
func (e *Executor) RunOnce(ctx context.Context) error {
	if e.store == nil {
		return errors.New("generation task store unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx, ExecutionTimeout)
	defer cancel()
	task, err := e.store.Claim(callCtx, uuid.NewString(), ClaimTTL)
	if err != nil {
		return err
	}
	return e.Process(callCtx, task)
}

type Workers struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

// Start returns a bounded, independently stoppable pool. Hosts start their old
// native video/cleanup workers separately, with different claim repositories.
func (e *Executor) Start(ctx context.Context, concurrency int) *Workers {
	ctx, cancel := context.WithCancel(ctx)
	w := &Workers{cancel: cancel}
	for range min(max(concurrency, 1), 32) {
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				if err := e.RunOnce(ctx); err != nil && !errors.Is(err, ErrTaskNotFound) && !errors.Is(err, context.Canceled) {
					// Error strings can contain secrets echoed by an upstream or
					// script. The task persists only host-owned public messages.
					slog.Warn("generation_worker", "error_type", fmt.Sprintf("%T", err))
				}
			}
		}()
	}
	return w
}

func (w *Workers) Stop() {
	if w != nil {
		w.once.Do(func() { w.cancel(); w.wg.Wait() })
	}
}
