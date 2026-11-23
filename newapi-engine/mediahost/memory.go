package mediahost

import (
	"context"
	"errors"
	"sync"
)

var ErrMemoryCapacity = errors.New("media working memory capacity exhausted")

// MemoryAdmission reserves temporary working storage against a host's shared
// request budget before decoding an image or document. The returned context
// preserves the request's identity and cancellation, and release is owned by
// the synchronous operation. No request context is retained in a Source.
// Standalone callers may omit it; the engine's process-wide limits still apply.
type MemoryAdmission func(context.Context, int64) (context.Context, func(), error)

type Option func(*Source)

// WithMemoryAdmission is a trusted host option, never a plugin-controlled value.
// Options must be supplied at construction, before a Source is used.
func WithMemoryAdmission(admit MemoryAdmission) Option {
	return func(s *Source) { s.admitMemory = admit }
}

// Independent engine limits complement the host's request admission. Both
// pools fail immediately, without creating a waiter or unbounded goroutines.
type memoryBudget struct {
	mu          sync.Mutex
	limit, used int64
}

func (b *memoryBudget) reserve(n int64) (func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > b.limit-b.used {
		return nil, ErrMemoryCapacity
	}
	b.used += n
	return sync.OnceFunc(func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.used -= n
	}), nil
}

func reserveWorkingMemory(ctx context.Context, bytes int64, budget *memoryBudget, admit MemoryAdmission) (context.Context, func(), error) {
	releaseGlobal, err := budget.reserve(bytes)
	if err != nil {
		return ctx, func() {}, err
	}
	if admit == nil {
		return ctx, releaseGlobal, nil
	}
	child, releaseHost, err := admit(ctx, bytes)
	if releaseHost == nil {
		releaseHost = func() {}
	}
	if err != nil || child == nil {
		releaseHost()
		releaseGlobal()
		if err == nil {
			err = errors.New("media memory admission returned no context")
		}
		return ctx, func() {}, err
	}
	return child, sync.OnceFunc(func() { releaseHost(); releaseGlobal() }), nil
}
