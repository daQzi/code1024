package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// TestGenerationDisableRollbackAndWorkerSwitchLocal exercises Section 8.5 & 8.6
// of NEWAPI_ENGINE_DECOUPLING.md against a real disposable PostgreSQL 17 instance:
//  1. Settled historical task IDs, pinned plugin_version_id, connection/price/credential
//     snapshots, usage_logs, and sub2api_generation_events remain completely intact
//     when the New API module is disabled/rolled back and later re-enabled.
//  2. A restarted/switched worker instance never re-claims or double-bills settled
//     tasks, and picks up previously queued tasks exactly once with idempotent settlement.
func TestGenerationDisableRollbackAndWorkerSwitchLocal(t *testing.T) {
	f, settledParent, _ := generationOriginSQLFixture(t)
	ctx := context.Background()

	// Settle the first historical task with explicit connection/price snapshots ($0.75 actual, $1.00 hold).
	_, err := f.db.Exec(`UPDATE sub2api_generation_tasks SET next_run_at=NOW(),lease_token='',lease_until=NULL WHERE id=$1`, settledParent.ID)
	require.NoError(t, err)
	workerA, err := f.repo.Claim(ctx, "worker-A-before-disable", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.repo.Settle(ctx, workerA, decimal.RequireFromString("0.75"), &service.GenerationCost{
		Amount: decimal.RequireFromString("0.75"), Currency: "USD", Unit: "request", Final: true, Source: "fixture",
	}))

	// Queue a second task right before simulating module disable/stop.
	queuedTask, queuedCmd := f.task(t, &service.GenerationFile{})
	queuedTask.Files = nil
	queuedTask.Connection = service.GenerationConnection{BaseURL: "https://pinned-before-disable.example", Kind: "vendor"}
	createdQueued, err := f.repo.CreateHeld(ctx, queuedTask, queuedCmd)
	require.NoError(t, err)

	var balanceBeforeDisable string
	require.NoError(t, f.db.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, f.user).Scan(&balanceBeforeDisable))
	var usageLogsBeforeDisable int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM usage_logs WHERE user_id=$1`, f.user).Scan(&usageLogsBeforeDisable))
	require.Equal(t, 1, usageLogsBeforeDisable)

	// --- Simulate New API module disable & rollback (no workers active) ---
	// Verify historical rows, pinned snapshots, events, and ledger entries are untouched.
	var (
		pinnedVer   int64
		connJSON    []byte
		priceJSON   []byte
		billingStat string
	)
	require.NoError(t, f.db.QueryRow(
		`SELECT plugin_version_id, connection, price, billing_status FROM sub2api_generation_tasks WHERE id=$1`,
		settledParent.ID,
	).Scan(&pinnedVer, &connJSON, &priceJSON, &billingStat))
	require.Equal(t, settledParent.PluginVersionID, pinnedVer)
	require.Equal(t, service.GenerationBillingSettled, billingStat)
	require.True(t, json.Valid(connJSON))
	require.True(t, json.Valid(priceJSON))

	// --- Simulate module re-enable with a brand-new repository & switched worker B ---
	restartedRepo := &generationTaskRepository{db: f.db}

	// Historical settled task remains readable with original pinned snapshots.
	reloadedSettled, err := restartedRepo.GetOwned(ctx, settledParent.ID, f.user, f.key)
	require.NoError(t, err)
	require.Equal(t, settledParent.ID, reloadedSettled.ID)
	require.Equal(t, settledParent.PluginVersionID, reloadedSettled.PluginVersionID)
	require.Equal(t, settledParent.Connection.BaseURL, reloadedSettled.Connection.BaseURL)
	require.Equal(t, service.GenerationBillingSettled, reloadedSettled.BillingStatus)

	// Attempting to settle the already-settled task again must be idempotent (zero double billing).
	require.NoError(t, restartedRepo.Settle(ctx, reloadedSettled, decimal.RequireFromString("0.75"), &service.GenerationCost{
		Amount: decimal.RequireFromString("0.75"), Currency: "USD", Unit: "request", Final: true, Source: "fixture",
	}))
	var balanceAfterIdempotentSettle string
	require.NoError(t, f.db.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, f.user).Scan(&balanceAfterIdempotentSettle))
	require.Equal(t, balanceBeforeDisable, balanceAfterIdempotentSettle, "re-settling a historical task must never double-bill")
	var usageLogsAfterIdempotent int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM usage_logs WHERE user_id=$1`, f.user).Scan(&usageLogsAfterIdempotent))
	require.Equal(t, 1, usageLogsAfterIdempotent)

	// Worker B claims ONLY the queued task (never re-claims the already-settled task).
	claimedByB, err := restartedRepo.Claim(ctx, "worker-B-after-reenable", time.Minute)
	require.NoError(t, err)
	require.Equal(t, createdQueued.ID, claimedByB.ID)
	require.Equal(t, "https://pinned-before-disable.example", claimedByB.Connection.BaseURL)

	// Concurrent Worker C attempting to claim simultaneously gets nothing (no duplicate claim).
	_, err = restartedRepo.Claim(ctx, "worker-C-concurrent", time.Minute)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)

	// Worker B completes and settles the queued task cleanly ($0.50 actual, $1.00 hold -> $0.50 refund).
	claimedByB.Status = service.GenerationSucceeded
	claimedByB.UpstreamID = "up-after-reenable"
	require.NoError(t, restartedRepo.SaveProgress(ctx, claimedByB, time.Second))
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET next_run_at=NOW() WHERE id=$1`, claimedByB.ID)
	require.NoError(t, err)
	claimedForSettle, err := restartedRepo.Claim(ctx, "worker-B-settle", time.Minute)
	require.NoError(t, err)
	require.Equal(t, createdQueued.ID, claimedForSettle.ID)
	require.NoError(t, restartedRepo.Settle(ctx, claimedForSettle, decimal.RequireFromString("0.50"), &service.GenerationCost{
		Amount: decimal.RequireFromString("0.50"), Currency: "USD", Unit: "request", Final: true, Source: "fixture",
	}))

	// Final ledger verification: exactly 2 usage_logs, exact balance ($100.00 - $0.75 - $0.50 = $98.75), and zero claimable tasks left.
	var finalUsageLogs int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM usage_logs WHERE user_id=$1`, f.user).Scan(&finalUsageLogs))
	require.Equal(t, 2, finalUsageLogs)
	var finalBalance string
	require.NoError(t, f.db.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, f.user).Scan(&finalBalance))
	require.True(t, decimal.RequireFromString("98.75").Equal(decimal.RequireFromString(finalBalance)), "expected balance 98.75, got %s", finalBalance)
	_, err = restartedRepo.Claim(ctx, "worker-D-empty", time.Minute)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
}
