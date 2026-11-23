package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestGenerationResultDeletionLocalSoftDeletesPreservingLedgerAndAudit(t *testing.T) {
	f, parent, cmd := generationOriginSQLFixture(t)
	ctx := context.Background()

	// Active or unsettled tasks cannot be deleted (409).
	require.ErrorIs(t, f.repo.DeleteGenerationResult(ctx, parent.ID, f.user, f.key, f.group, true), service.ErrGenerationTaskResultNotDeletable)

	// Settle parent task cleanly through real ledger settlement (actual = $0.60, hold was $1.00).
	// parent is already status='succeeded', billing_status='pending' from generationOriginSQLFixture.
	_, err := f.db.Exec(`UPDATE sub2api_generation_tasks SET next_run_at=NOW(),lease_token='',lease_until=NULL WHERE id=$1`, parent.ID)
	require.NoError(t, err)
	claimed, err := f.repo.Claim(ctx, "delete-settle", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.repo.Settle(ctx, claimed, decimal.RequireFromString("0.6"), &service.GenerationCost{Amount: decimal.RequireFromString("0.6"), Currency: "USD", Unit: "request", Final: true, Source: "fixture"}))

	// Foreign user, key, or group must see 404.
	for _, owner := range [][3]int64{{f.user + 1, f.key, f.group}, {f.user, f.key + 1, f.group}, {f.user, f.key, f.group + 1}} {
		require.ErrorIs(t, f.repo.DeleteGenerationResult(ctx, parent.ID, owner[0], owner[1], owner[2], true), service.ErrGenerationTaskNotFound)
	}

	// Non-video task must be excluded by videoOnly=true, while deletable with videoOnly=false.
	sunoTask, sunoCmd := f.task(t, &service.GenerationFile{})
	sunoTask.Files = nil
	sunoTask.Protocol = service.GenerationProtocolSunoTask
	_, err = f.repo.CreateHeld(ctx, sunoTask, sunoCmd)
	require.NoError(t, err)
	sunoClaimed, err := f.repo.Claim(ctx, "delete-suno-submit", time.Minute)
	require.NoError(t, err)
	sunoClaimed.Status = service.GenerationSucceeded
	sunoClaimed.UpstreamID = "private-suno-upstream"
	require.NoError(t, f.repo.SaveProgress(ctx, sunoClaimed, time.Second))
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET next_run_at=NOW() WHERE id=$1`, sunoTask.ID)
	require.NoError(t, err)
	sunoClaimed, err = f.repo.Claim(ctx, "delete-suno-settle", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.repo.Settle(ctx, sunoClaimed, decimal.RequireFromString("0.4"), &service.GenerationCost{Amount: decimal.RequireFromString("0.4"), Currency: "USD", Unit: "request", Final: true, Source: "fixture"}))
	require.ErrorIs(t, f.repo.DeleteGenerationResult(ctx, sunoTask.ID, f.user, f.key, f.group, true), service.ErrGenerationTaskNotFound)
	require.NoError(t, f.repo.DeleteGenerationResult(ctx, sunoTask.ID, f.user, f.key, f.group, false))

	// Soft-delete the settled parent video result twice -> idempotent, single audit event.
	require.NoError(t, f.repo.DeleteGenerationResult(ctx, parent.ID, f.user, f.key, f.group, true))
	require.NoError(t, f.repo.DeleteGenerationResult(ctx, parent.ID, f.user, f.key, f.group, true))

	var deleteEvents int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM sub2api_generation_events WHERE task_id=$1 AND event_type='result_deleted'`, parent.ID).Scan(&deleteEvents))
	require.Equal(t, 1, deleteEvents)

	// After deletion, public reads/listing/origin lookup/cancellation all hide the task.
	restarted := &generationTaskRepository{db: f.db}
	_, err = restarted.GetOwned(ctx, parent.ID, f.user, f.key)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)

	listed, err := restarted.ListOwned(ctx, f.user, f.key, "", 20, "desc")
	require.NoError(t, err)
	require.Empty(t, listed)

	_, err = restarted.GetVideoOrigin(ctx, parent.ID, f.user, f.key, f.group)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)

	_, err = restarted.CancelQueuedTask(ctx, parent.ID, f.user, f.key, f.group)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)

	_, err = restarted.ConfirmUpstreamCancelTask(ctx, parent.ID, f.user, f.key, f.group)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)

	// Deriving a child task from the deleted parent must fail closed and leave balance untouched.
	child := generationOriginSQLChild(t, parent)
	_, err = restarted.CreateHeld(ctx, child, cmd)
	require.ErrorIs(t, err, service.ErrGenerationOrigin)

	// Reusing the deleted task's Idempotency-Key must conflict (never silently create a second charge or resurrect the deleted result).
	_, err = restarted.FindIdempotent(ctx, f.user, f.key, parent.IdempotencyKey)
	require.ErrorIs(t, err, service.ErrGenerationIdempotencyConflict)
	reused := *parent
	reused.ID, err = service.NewGenerationTaskID()
	require.NoError(t, err)
	_, err = restarted.CreateHeld(ctx, &reused, cmd)
	require.ErrorIs(t, err, service.ErrGenerationIdempotencyConflict)

	// Billing ledger, dedup record, and audit task logs remain completely intact.
	var balance, frozen float64
	require.NoError(t, f.db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, f.user).Scan(&balance, &frozen))
	require.InDelta(t, 99.0, balance, 1e-6, "parent ($0.60) + suno ($0.40) settled = $1.00 spent from $100")
	require.Zero(t, frozen)

	var usageLogs, settledEvents int
	var usageActualCost float64
	require.NoError(t, f.db.QueryRow(`SELECT count(*), COALESCE(SUM(actual_cost),0) FROM usage_logs WHERE request_id=$1`, parent.ID).Scan(&usageLogs, &usageActualCost))
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM sub2api_generation_events WHERE task_id=$1 AND event_type='funds_settled'`, parent.ID).Scan(&settledEvents))
	require.Equal(t, 1, usageLogs)
	require.InDelta(t, 0.6, usageActualCost, 1e-6)
	require.Equal(t, 1, settledEvents)

	// Task logs (user + admin audit) retain full history for the deleted task.
	taskLogs := service.TaskLogRepository(restarted)
	detail, err := taskLogs.GetTaskLog(ctx, parent.ID, f.user)
	require.NoError(t, err)
	require.Equal(t, parent.ID, detail.ID)
	require.Equal(t, service.GenerationBillingSettled, detail.BillingStatus)

	items, total, err := taskLogs.ListTaskLogs(ctx, service.TaskLogFilter{UserID: f.user, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, items, 2)
}
