package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestGenerationCancelLocalRefundsOnceAndSerializesWithClaim(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	task, cmd := f.task(t, &service.GenerationFile{})
	task.Files = nil
	task.PreparationStatus = "pending"
	_, err := f.repo.CreateHeld(ctx, task, cmd)
	require.NoError(t, err)
	for _, owner := range [][3]int64{{f.user + 1, f.key, f.group}, {f.user, f.key + 1, f.group}, {f.user, f.key, f.group + 1}} {
		_, err = f.repo.CancelQueuedTask(ctx, task.ID, owner[0], owner[1], owner[2])
		require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	}
	// Concurrent requests create one terminal transition and one event.
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for range 6 {
		wg.Go(func() {
			_, err := f.repo.CancelQueuedTask(ctx, task.ID, f.user, f.key, f.group)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	var events int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM sub2api_generation_events WHERE task_id=$1 AND event_type='task_canceled'`, task.ID).Scan(&events))
	require.Equal(t, 1, events)
	claimed, err := f.repo.Claim(ctx, "cancel-refund", time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.GenerationFailed, claimed.Status, "workers may only settle the canceled task")
	require.Equal(t, "cleanup", claimed.PreparationStatus)
	require.Equal(t, service.GenerationCanceledBeforeSubmit, claimed.ErrorCode)
	require.Empty(t, claimed.UpstreamID)
	require.ErrorIs(t, f.repo.Settle(ctx, claimed, decimal.RequireFromString("0.1"), nil), service.ErrUsageBillingRequestConflict)
	require.ErrorIs(t, f.repo.Settle(ctx, claimed, decimal.Zero, &service.GenerationCost{}), service.ErrUsageBillingRequestConflict)
	executor := generation.NewExecutor(generation.ExecutorOptions{Store: f.repo})
	require.NoError(t, executor.Process(ctx, claimed)) // No connection resolver: no model I/O.
	var balance, frozen, quota float64
	require.NoError(t, f.db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, f.user).Scan(&balance, &frozen))
	require.NoError(t, f.db.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, f.key).Scan(&quota))
	require.Equal(t, 100.0, balance)
	require.Zero(t, frozen)
	require.Zero(t, quota)
	canceled, err := f.repo.CancelQueuedTask(ctx, task.ID, f.user, f.key, f.group)
	require.NoError(t, err)
	require.Equal(t, service.GenerationBillingSettled, canceled.BillingStatus)
	_, err = f.repo.Claim(ctx, "cannot-resubmit", time.Minute)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	claimed, err = f.repo.ClaimPreparationCleanup(ctx, "cancel-cleanup", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.repo.FinishPreparationCleanup(ctx, claimed, true, time.Second))
	metrics, err := NewModelPlazaMetricsRepository(f.db).ReadModelMetrics(ctx, task.Model, task.Platform, []int64{f.group}, time.Now().Add(-time.Hour), time.Now().Add(time.Minute))
	require.NoError(t, err)
	for _, point := range metrics {
		require.Zero(t, point.Requests, "a locally canceled job is not an upstream failure")
	}

	// If the worker wins, cancellation cannot relabel or refund possible
	// upstream work, including when a worker lease has expired.
	next, cmd := f.task(t, &service.GenerationFile{})
	next.Files = nil
	_, err = f.repo.CreateHeld(ctx, next, cmd)
	require.NoError(t, err)
	claimed, err = f.repo.Claim(ctx, "submit-first", time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.GenerationSubmitting, claimed.Status)
	_, err = f.repo.CancelQueuedTask(ctx, next.ID, f.user, f.key, f.group)
	require.ErrorIs(t, err, service.ErrGenerationTaskCannotCancel)
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, next.ID)
	require.NoError(t, err)
	_, err = f.repo.CancelQueuedTask(ctx, next.ID, f.user, f.key, f.group)
	require.ErrorIs(t, err, service.ErrGenerationTaskCannotCancel)
}

func TestGenerationConfirmedUpstreamCancelFencesPollAndRefundsOnce(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	task, cmd := f.task(t, &service.GenerationFile{})
	task.Files = nil
	_, err := f.repo.CreateHeld(ctx, task, cmd)
	require.NoError(t, err)
	_, err = f.repo.ConfirmUpstreamCancelTask(ctx, task.ID, f.user, f.key, f.group)
	require.ErrorIs(t, err, service.ErrGenerationTaskCannotCancel)
	worker, err := f.repo.Claim(ctx, "submit-cancel-fixture", time.Minute)
	require.NoError(t, err)
	worker.UpstreamID, worker.Status = "vendor-only-id", service.GenerationRunning
	require.NoError(t, f.repo.SaveProgress(ctx, worker, time.Second))
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET next_run_at=NOW() WHERE id=$1`, task.ID)
	require.NoError(t, err)
	worker, err = f.repo.Claim(ctx, "poll-before-cancel", time.Minute)
	require.NoError(t, err)
	_, err = f.repo.ConfirmUpstreamCancelTask(ctx, task.ID, f.user, f.key+1, f.group)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	canceled, err := f.repo.ConfirmUpstreamCancelTask(ctx, task.ID, f.user, f.key, f.group)
	require.NoError(t, err)
	require.Equal(t, service.GenerationCanceledUpstream, canceled.ErrorCode)
	require.Equal(t, service.GenerationBillingPending, canceled.BillingStatus)
	worker.Status = service.GenerationSucceeded
	require.ErrorIs(t, f.repo.SaveProgress(ctx, worker, time.Second), service.ErrGenerationLeaseLost)
	claimed, err := f.repo.Claim(ctx, "refund-confirmed-cancel", time.Minute)
	require.NoError(t, err)
	require.ErrorIs(t, f.repo.Settle(ctx, claimed, decimal.RequireFromString("0.1"), nil), service.ErrUsageBillingRequestConflict)
	require.NoError(t, generation.NewExecutor(generation.ExecutorOptions{Store: f.repo}).Process(ctx, claimed))
	canceled, err = f.repo.ConfirmUpstreamCancelTask(ctx, task.ID, f.user, f.key, f.group)
	require.NoError(t, err)
	require.Equal(t, service.GenerationBillingSettled, canceled.BillingStatus)
	var balance, frozen float64
	require.NoError(t, f.db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, f.user).Scan(&balance, &frozen))
	require.Equal(t, 100.0, balance)
	require.Zero(t, frozen)
	var events int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM sub2api_generation_events WHERE task_id=$1 AND event_type='task_canceled'`, task.ID).Scan(&events))
	require.Equal(t, 1, events)
}
