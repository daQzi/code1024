package generation_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// This store models claim fencing and failure points for independent engine
// tests. The host's PostgreSQL suite separately verifies real atomic funds,
// locks, idempotency, quota delivery and crash recovery.
type recoveryStore struct {
	generation.Store
	task        *generation.Task
	saveErr     error
	settleErr   error
	settlements int
	amount      decimal.Decimal
	delay       time.Duration
}

func cloneTask(task *generation.Task) *generation.Task {
	// Task deliberately hides private snapshots in public JSON. Copy those
	// bytes explicitly instead of serializing away the execution state.
	copy := *task
	copy.Request = append(json.RawMessage(nil), task.Request...)
	copy.State = append(json.RawMessage(nil), task.State...)
	copy.Data = append(json.RawMessage(nil), task.Data...)
	copy.Usage = append(json.RawMessage(nil), task.Usage...)
	return &copy
}

func (s *recoveryStore) Claim(_ context.Context, token string, ttl time.Duration) (*generation.Task, error) {
	if token == "" || ttl != generation.ClaimTTL {
		return nil, errors.New("invalid lease")
	}
	if s.task.Status == generation.Uncertain || s.task.BillingStatus == generation.BillingReview || s.task.BillingStatus == generation.BillingSettled {
		return nil, generation.ErrTaskNotFound
	}
	switch s.task.Status {
	case generation.Queued:
		s.task.Status = generation.Submitting
	case generation.Submitting:
		if s.task.PreparationStatus != "pending" {
			s.task.Status = generation.Uncertain
		}
	}
	s.task.LeaseToken = token
	return cloneTask(s.task), nil
}

func (s *recoveryStore) SaveProgress(_ context.Context, task *generation.Task, delay time.Duration) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	if task.LeaseToken != s.task.LeaseToken {
		return generation.ErrLeaseLost
	}
	s.task, s.delay = cloneTask(task), delay
	return nil
}

func (s *recoveryStore) Settle(_ context.Context, task *generation.Task, amount decimal.Decimal, _ *generation.Cost) error {
	if s.settleErr != nil {
		return s.settleErr
	}
	if task.LeaseToken != s.task.LeaseToken {
		return generation.ErrLeaseLost
	}
	if s.task.BillingStatus != generation.BillingSettled {
		s.settlements++
		s.amount = amount
	}
	task.BillingStatus = generation.BillingSettled
	s.task = cloneTask(task)
	return nil
}

type fixtureConnection struct {
	available, acquired                bool
	releases, submits, polls, prepares int
	submitErr, prepareErr, pollErr     error
}

func (c *fixtureConnection) Schedulable() bool { return c.available }
func (c *fixtureConnection) Acquire(context.Context) (func(), bool, error) {
	return func() { c.releases++ }, c.acquired, nil
}
func (c *fixtureConnection) Prepare(context.Context, *generation.Task) error {
	c.prepares++
	return c.prepareErr
}
func (c *fixtureConnection) Submit(_ context.Context, task *generation.Task) error {
	c.submits++
	if c.submitErr == nil {
		task.Status, task.UpstreamID = generation.Running, "private-upstream-id"
	}
	return c.submitErr
}
func (c *fixtureConnection) Poll(_ context.Context, task *generation.Task) error {
	c.polls++
	if c.pollErr == nil {
		task.Status, task.DeliveryStatus = generation.Succeeded, generation.DeliveryReady
		task.BillingStatus = generation.BillingPending
		task.Usage = json.RawMessage(`{"seconds":8,"_host_final_usage":{"seconds":6}}`)
		now := time.Now().UTC()
		task.FinishedAt = &now
	}
	return c.pollErr
}

func queuedTask(t *testing.T) *generation.Task {
	t.Helper()
	unit := decimal.RequireFromString("0.02")
	price, err := generation.NewPriceSnapshot(generation.PriceRule{Mode: "fixed", Unit: "second", UnitPrice: &unit}, decimal.NewFromInt(8), nil, decimal.NewFromInt(1))
	require.NoError(t, err)
	id, err := generation.NewTaskID()
	require.NoError(t, err)
	return &generation.Task{ID: id, Status: generation.Queued, BillingStatus: generation.BillingHeld, CreatedAt: time.Now().UTC(), Price: price, Usage: json.RawMessage(`{"seconds":8}`), Connection: generation.Connection{BaseURL: "https://original.example", Kind: "vendor"}, PluginVersionID: 7}
}

func executor(store *recoveryStore, connection *fixtureConnection, after generation.AfterSettlement) *generation.Executor {
	return generation.NewExecutor(generation.ExecutorOptions{Store: store, Resolve: func(context.Context, *generation.Task) (generation.ConnectionRuntime, error) { return connection, nil }, AfterSettlement: after})
}

func TestExecutorRecoversFinalFactsBeforeAtomicSettlement(t *testing.T) {
	store := &recoveryStore{task: queuedTask(t)}
	connection := &fixtureConnection{available: true, acquired: true}
	reports := 0
	after := func(context.Context, *generation.Task, decimal.Decimal) { reports++ }
	ctx := context.Background()
	require.NoError(t, executor(store, connection, after).RunOnce(ctx))
	require.Equal(t, generation.Running, store.task.Status)
	require.NoError(t, executor(store, connection, after).RunOnce(ctx))
	require.Equal(t, generation.Succeeded, store.task.Status)
	require.Zero(t, store.settlements, "terminal facts must be durable before touching funds")
	store.settleErr = errors.New("transaction rolled back")
	require.Error(t, executor(store, connection, after).RunOnce(ctx))
	require.Zero(t, reports)
	store.settleErr = nil
	require.NoError(t, executor(store, connection, after).RunOnce(ctx))
	require.Equal(t, "0.12", store.amount.String(), "settle measured 6 seconds, not the 8-second hold")
	require.Equal(t, 1, store.settlements)
	require.Equal(t, 1, reports)
	require.Equal(t, 1, connection.submits)
	require.Equal(t, 1, connection.polls)
	require.Equal(t, int64(7), store.task.PluginVersionID)
	require.Equal(t, "https://original.example", store.task.Connection.BaseURL)
	require.ErrorIs(t, executor(store, connection, after).RunOnce(ctx), generation.ErrTaskNotFound)
}

func TestExecutorLostSubmitPersistenceNeverRepeatsPaidRequest(t *testing.T) {
	store := &recoveryStore{task: queuedTask(t), saveErr: generation.ErrLeaseLost}
	connection := &fixtureConnection{available: true, acquired: true}
	require.ErrorIs(t, executor(store, connection, nil).RunOnce(context.Background()), generation.ErrLeaseLost)
	require.Equal(t, generation.Submitting, store.task.Status)
	store.saveErr = nil // Simulate the old submitting lease expiring after restart.
	require.NoError(t, executor(store, connection, nil).RunOnce(context.Background()))
	require.Equal(t, generation.Uncertain, store.task.Status)
	require.ErrorIs(t, executor(store, connection, nil).RunOnce(context.Background()), generation.ErrTaskNotFound)
	require.Equal(t, 1, connection.submits)
	require.Zero(t, connection.polls)
	require.Zero(t, store.settlements)
}

func TestExecutorSubmissionClassificationAndPrivacy(t *testing.T) {
	for _, uncertain := range []bool{true, false} {
		t.Run(map[bool]string{true: "unknown acceptance", false: "confirmed rejection"}[uncertain], func(t *testing.T) {
			store := &recoveryStore{task: queuedTask(t)}
			private := errors.New("private token=upstream-secret")
			var failure error = private
			if !uncertain {
				failure = &generation.SubmissionError{Cause: private}
			}
			connection := &fixtureConnection{available: true, acquired: true, submitErr: failure}
			require.ErrorIs(t, executor(store, connection, nil).RunOnce(context.Background()), private)
			require.NotContains(t, store.task.ErrorMessage, "upstream-secret")
			require.Equal(t, 1, connection.releases)
			if uncertain {
				require.Equal(t, generation.Uncertain, store.task.Status)
				require.Equal(t, generation.BillingReview, store.task.BillingStatus)
			} else {
				require.Equal(t, generation.Failed, store.task.Status)
				require.NoError(t, executor(store, connection, nil).RunOnce(context.Background()))
				require.True(t, store.amount.IsZero())
				require.Equal(t, 1, store.settlements)
			}
			require.Equal(t, 1, connection.submits)
		})
	}
}

func TestExecutorPreparationAndAdmissionNeverCrossPaidBoundary(t *testing.T) {
	for _, name := range []string{"waiting", "permanent", "expired", "disabled", "busy", "ready"} {
		t.Run(name, func(t *testing.T) {
			store := &recoveryStore{task: queuedTask(t)}
			store.task.PreparationStatus = "pending"
			connection := &fixtureConnection{available: true, acquired: true}
			switch name {
			case "waiting":
				connection.prepareErr = &generation.PreparationError{RetryAfter: 2 * time.Hour}
			case "permanent":
				connection.prepareErr = &generation.PreparationError{Permanent: true}
			case "expired":
				store.task.CreatedAt = time.Now().Add(-16 * time.Minute)
				connection.prepareErr = errors.New("temporary upload error")
			case "disabled":
				connection.available = false
			case "busy":
				connection.acquired = false
			}
			require.NoError(t, executor(store, connection, nil).RunOnce(context.Background()))
			require.Zero(t, connection.submits)
			switch name {
			case "permanent", "expired":
				require.Equal(t, generation.Failed, store.task.Status)
				require.Equal(t, "cleanup", store.task.PreparationStatus)
			case "ready":
				require.Equal(t, "ready", store.task.PreparationStatus)
				require.NoError(t, executor(store, connection, nil).RunOnce(context.Background()))
				require.Equal(t, 1, connection.submits)
			default:
				require.Equal(t, generation.Queued, store.task.Status)
				require.LessOrEqual(t, store.delay, time.Hour)
			}
		})
	}
}

func TestExecutorReviewMissingOrExcessFinalUsage(t *testing.T) {
	for _, usage := range []string{`{"seconds":8}`, `{"_host_final_usage":{"seconds":10}}`} {
		store := &recoveryStore{task: queuedTask(t)}
		store.task.Status, store.task.Usage = generation.Succeeded, json.RawMessage(usage)
		require.NoError(t, executor(store, nil, nil).RunOnce(context.Background()))
		require.Equal(t, generation.BillingReview, store.task.BillingStatus)
		require.Zero(t, store.settlements)
	}
}

type blockingStore struct {
	generation.Store
	entered chan struct{}
	once    sync.Once
}

func (s *blockingStore) Claim(ctx context.Context, _ string, _ time.Duration) (*generation.Task, error) {
	s.once.Do(func() { close(s.entered) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestWorkerStopCancelsAndJoinsAllClaims(t *testing.T) {
	store := &blockingStore{entered: make(chan struct{})}
	workers := generation.NewExecutor(generation.ExecutorOptions{Store: store}).Start(context.Background(), 2)
	defer workers.Stop()
	select {
	case <-store.entered:
	case <-time.After(4 * time.Second):
		t.Fatal("worker never claimed a task")
	}
	var stopped sync.WaitGroup
	for range 4 {
		stopped.Add(1)
		go func() { defer stopped.Done(); workers.Stop() }()
	}
	done := make(chan struct{})
	go func() { stopped.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown left a claim running")
	}
}

func TestTaskJSONDoesNotExposeExecutionSnapshots(t *testing.T) {
	task := queuedTask(t)
	task.ConfigEncrypted, task.RequestPluginsEncrypted = "private-config", "private-policy"
	task.PreparationEncrypted, task.UpstreamID = "private-upload", "private-upstream"
	task.CredentialBinding = generation.CredentialBinding{Mode: "login", Identity: strings.Repeat("a", 64), Connection: json.RawMessage(`{"private":"credential"}`)}
	raw, err := json.Marshal(task)
	require.NoError(t, err)
	for _, secret := range []string{"private-", "credential", "original.example", "unit_price", "upstream_id", "lease_token"} {
		require.NotContains(t, string(raw), secret)
	}
}

func TestBillingPendingWindowDeterministicSettleVsAdminReview(t *testing.T) {
	baseTime := time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)
	finished := baseTime.Add(-15 * time.Minute)

	// 1) Fixed per-request task settles deterministically after billingPendingWindow.
	unit := decimal.RequireFromString("0.5")
	fixedPrice, err := generation.NewPriceSnapshot(generation.PriceRule{Mode: "fixed", Unit: "request", UnitPrice: &unit}, decimal.NewFromInt(1), nil, decimal.NewFromInt(1))
	require.NoError(t, err)
	fixedTask := &generation.Task{
		ID: "video_det", Status: generation.Succeeded, DeliveryStatus: generation.DeliveryReady, BillingStatus: generation.BillingPending,
		Price: fixedPrice, Usage: json.RawMessage(`{"_host_final_usage":{"billing_pending":true}}`),
		CreatedAt: baseTime.Add(-20 * time.Minute), FinishedAt: &finished, LeaseToken: "lease-1",
	}
	store := &recoveryStore{task: cloneTask(fixedTask)}
	exec := generation.NewExecutor(generation.ExecutorOptions{Store: store, BillingPendingWindow: 10 * time.Minute, Now: func() time.Time { return baseTime }})
	require.NoError(t, exec.Process(context.Background(), fixedTask))
	require.Equal(t, generation.BillingSettled, store.task.BillingStatus)
	require.Equal(t, "0.5", store.amount.String())

	// 2) Cost-plus final-bill task must move to BillingReview after billingPendingWindow.
	exRate := decimal.NewFromInt(1)
	markup := decimal.RequireFromString("1.2")
	cap := decimal.NewFromInt(5)
	costPrice, err := generation.NewPriceSnapshot(generation.PriceRule{Mode: "cost_plus", SourceCurrency: "USD", SourceUnit: "upstream_cost", ExchangeRate: &exRate, Markup: &markup, Maximum: &cap}, decimal.NewFromInt(1), nil, decimal.NewFromInt(1))
	require.NoError(t, err)
	costTask := &generation.Task{
		ID: "video_rev", Status: generation.Succeeded, DeliveryStatus: generation.DeliveryReady, BillingStatus: generation.BillingPending,
		Price: costPrice, Usage: json.RawMessage(`{"_host_final_usage":{"billing_pending":true}}`),
		CreatedAt: baseTime.Add(-20 * time.Minute), FinishedAt: &finished, LeaseToken: "lease-2",
	}
	store2 := &recoveryStore{task: cloneTask(costTask)}
	exec2 := generation.NewExecutor(generation.ExecutorOptions{Store: store2, BillingPendingWindow: 10 * time.Minute, Now: func() time.Time { return baseTime }})
	require.NoError(t, exec2.Process(context.Background(), costTask))
	require.Equal(t, generation.BillingReview, store2.task.BillingStatus)
	require.Equal(t, "upstream_billing_timeout", store2.task.ErrorCode)
}

func TestExecutorSettlesCostPlusNonFinalUnitFromObservedCost(t *testing.T) {
	now := time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)
	exRate := decimal.RequireFromString("0.14")
	markup := decimal.RequireFromString("1.5")
	quote := &generation.Cost{Amount: decimal.NewFromInt(10), Currency: "CNY", Unit: "currency", Source: "quote_hook", ObservedAt: now}
	price, err := generation.NewPriceSnapshotAt(generation.PriceRule{Mode: "cost_plus", SourceCurrency: "CNY", SourceUnit: "currency", ExchangeRate: &exRate, Markup: &markup}, decimal.NewFromInt(1), quote, decimal.NewFromInt(1), now)
	require.NoError(t, err)
	task := &generation.Task{
		ID: "video_obs", Status: generation.Succeeded, DeliveryStatus: generation.DeliveryReady, BillingStatus: generation.BillingPending,
		Price: price, Usage: json.RawMessage(`{"_host_final_usage":{"upstream_cost":{"amount":"8","currency":"CNY","unit":"currency","final":true,"source":"invoice"}}}`),
		CreatedAt: now, LeaseToken: "lease-3",
	}
	store := &recoveryStore{task: cloneTask(task)}
	exec := generation.NewExecutor(generation.ExecutorOptions{Store: store, Now: func() time.Time { return now }})
	require.NoError(t, exec.Settle(context.Background(), task))
	require.Equal(t, generation.BillingSettled, store.task.BillingStatus)
	require.Equal(t, "1.68", store.amount.String()) // 8 * 0.14 * 1.5
}
