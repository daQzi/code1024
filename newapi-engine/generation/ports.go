package generation

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// Store owns durable claims and short, atomic financial transactions. Claim
// must fence an expired submitting lease as Uncertain, unless the task was
// still preparing files and had not crossed the paid submission boundary.
// SaveProgress and Settle must reject a stale/expired lease with ErrLeaseLost.
type Store interface {
	Claim(context.Context, string, time.Duration) (*Task, error)
	SaveProgress(context.Context, *Task, time.Duration) error
	// Settle atomically commits the terminal facts, reservation release,
	// final charge/refund, ledger and durable quota outbox. It is idempotent
	// per task and updates BillingStatus to BillingSettled after commit.
	// It must revalidate persisted facts; callers cannot authorize a new bill.
	Settle(context.Context, *Task, decimal.Decimal, *Cost) error
}

// Connection is a host-authorized projection of the original task connection.
// It never reroutes a task to another account or a newer plugin revision.
// Credential refresh is allowed only for the same bound upstream identity.
type ConnectionRuntime interface {
	Schedulable() bool
	Acquire(context.Context) (release func(), acquired bool, err error)
	Prepare(context.Context, *Task) error
	Submit(context.Context, *Task) error
	Poll(context.Context, *Task) error
}

type ResolveConnection func(context.Context, *Task) (ConnectionRuntime, error)

// AfterSettlement is best-effort host notification after the atomic commit.
// Durable financial delivery belongs in Store.Settle's outbox, not here.
type AfterSettlement func(context.Context, *Task, decimal.Decimal)

const DefaultBillingPendingWindow = 2 * time.Hour

type ExecutorOptions struct {
	Store           Store
	Resolve         ResolveConnection
	AfterSettlement AfterSettlement
	// BillingPendingWindow limits how long a finished task may wait for an
	// upstream final bill before deterministic recovery or admin review.
	BillingPendingWindow time.Duration
	// Now permits deterministic host-independent failure/recovery tests.
	Now func() time.Time
}
