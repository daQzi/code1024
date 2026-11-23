package service

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/newapi/officialupload"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/shopspring/decimal"
)

// The service facade supplies only the host capabilities required by durable
// execution. The engine does not import accounts, gateways or upload vendors.
func (s *GenerationService) taskExecutor() *generation.Executor {
	if s.engine != nil {
		return s.engine
	}
	return generation.NewExecutor(s.TaskExecutorPorts())
}

// TaskExecutorPorts exposes the host capabilities used by the independent
// engine. It does not expose native plugin management or a second wallet.
func (s *GenerationService) TaskExecutorPorts() generation.ExecutorOptions {
	return generation.ExecutorOptions{
		Store: s.repo, Resolve: s.resolveTaskConnection, AfterSettlement: s.afterTaskSettlement,
	}
}

func (s *GenerationService) resolveTaskConnection(ctx context.Context, task *GenerationTask) (generation.ConnectionRuntime, error) {
	if s.accounts == nil || s.plugins == nil {
		return nil, errors.New("generation account runtime unavailable")
	}
	account, err := s.accounts.GetByID(ctx, task.AccountID)
	if err != nil || account == nil {
		return nil, err
	}
	return &generationHostConnection{service: s, account: account}, nil
}

type generationHostConnection struct {
	service *GenerationService
	account *Account
}

func (c *generationHostConnection) Schedulable() bool { return c.account.IsSchedulable() }

func (c *generationHostConnection) Acquire(ctx context.Context) (func(), bool, error) {
	if c.service.gateway == nil || c.service.gateway.concurrencyService == nil {
		return nil, false, generation.ErrConcurrencyUnavailable
	}
	slot, err := c.service.gateway.concurrencyService.AcquireAccountSlot(ctx, c.account.ID, c.account.Concurrency)
	if err != nil {
		return nil, false, err
	}
	return slot.ReleaseFunc, slot.Acquired, nil
}

func (c *generationHostConnection) Prepare(ctx context.Context, task *GenerationTask) error {
	err := c.service.plugins.prepareTaskFiles(ctx, c.account, task)
	if err == nil {
		return nil
	}
	result := &generation.PreparationError{Cause: err}
	var permanent *officialupload.Permanent
	result.Permanent = errors.As(err, &permanent)
	var waiting *officialupload.Waiting
	if errors.As(err, &waiting) {
		result.RetryAfter = waiting.Delay
	}
	return result
}

func (c *generationHostConnection) Submit(ctx context.Context, task *GenerationTask) error {
	return c.service.plugins.SubmitTask(ctx, c.account, task)
}

func (c *generationHostConnection) Poll(ctx context.Context, task *GenerationTask) error {
	return c.service.plugins.PollTask(ctx, c.account, task)
}

// AcquireTaskAccountSlot exposes bounded account concurrency acquisition to the
// single-point newapihost composition layer without exposing internal gateway fields.
func (s *GenerationService) AcquireTaskAccountSlot(ctx context.Context, account *Account) (func(), bool, error) {
	if s == nil || account == nil || s.gateway == nil || s.gateway.concurrencyService == nil {
		return nil, false, generation.ErrConcurrencyUnavailable
	}
	slot, err := s.gateway.concurrencyService.AcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil {
		return nil, false, err
	}
	return slot.ReleaseFunc, slot.Acquired, nil
}

// PrepareAccountTaskFiles runs optional pre-submission upload preparation for a
// bound task account, returning raw host errors for newapihost translation.
func (s *GenerationService) PrepareAccountTaskFiles(ctx context.Context, account *Account, task *GenerationTask) error {
	if s == nil || s.plugins == nil {
		return errors.New("generation plugin service unavailable")
	}
	return s.plugins.prepareTaskFiles(ctx, account, task)
}

// AfterTaskSettlement dispatches post-settlement cache invalidation and optional
// async task completion reports for the newapihost executor adapter.
func (s *GenerationService) AfterTaskSettlement(ctx context.Context, task *GenerationTask, actual decimal.Decimal) {
	if s == nil {
		return
	}
	s.afterTaskSettlement(ctx, task, actual)
}
