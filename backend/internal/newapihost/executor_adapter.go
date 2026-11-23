package newapihost

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/newapi/officialupload"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
)

// NewTaskExecutorPorts composes the standalone generation.Executor ports in the
// single-point host assembly package. It binds Sub2API's existing task store,
// account lookup, concurrency slot limiter, upload preparation error taxonomy,
// JS plugin driver, and post-settlement notification hook without creating a
// second wallet or touching Sub2API's native .s2plugin PluginManager.
func NewTaskExecutorPorts(
	tasks service.GenerationTaskRepository,
	host *service.GenerationService,
	accounts service.AccountRepository,
	plugins *service.NewAPIPluginService,
) generation.ExecutorOptions {
	return generation.ExecutorOptions{
		Store: tasks,
		Resolve: func(ctx context.Context, task *service.GenerationTask) (generation.ConnectionRuntime, error) {
			if accounts == nil || plugins == nil || host == nil {
				return nil, errors.New("generation account runtime unavailable")
			}
			account, err := accounts.GetByID(ctx, task.AccountID)
			if err != nil || account == nil {
				return nil, err
			}
			return &hostConnectionRuntime{
				host:    host,
				plugins: plugins,
				account: account,
			}, nil
		},
		AfterSettlement: host.AfterTaskSettlement,
	}
}

type hostConnectionRuntime struct {
	host    *service.GenerationService
	plugins *service.NewAPIPluginService
	account *service.Account
}

func (c *hostConnectionRuntime) Schedulable() bool {
	return c.account != nil && c.account.IsSchedulable()
}

func (c *hostConnectionRuntime) Acquire(ctx context.Context) (func(), bool, error) {
	return c.host.AcquireTaskAccountSlot(ctx, c.account)
}

func (c *hostConnectionRuntime) Prepare(ctx context.Context, task *service.GenerationTask) error {
	err := c.host.PrepareAccountTaskFiles(ctx, c.account, task)
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

func (c *hostConnectionRuntime) Submit(ctx context.Context, task *service.GenerationTask) error {
	return c.plugins.SubmitTask(ctx, c.account, task)
}

func (c *hostConnectionRuntime) Poll(ctx context.Context, task *service.GenerationTask) error {
	return c.plugins.PollTask(ctx, c.account, task)
}
