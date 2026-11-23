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

func TestTaskEgressLocalPostgresSnapshotRefundAndMetrics(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	task, cmd := f.task(t, &service.GenerationFile{})
	task.Files = nil
	task.RequestPluginsEncrypted = "fixture-encrypted-policy-reference"
	task.RequestReportEncrypted = "fixture-encrypted-report-reference"
	_, err := f.repo.CreateHeld(ctx, task, cmd)
	require.NoError(t, err)
	// A new repository represents a restarted worker, not the admission object.
	restarted := &generationTaskRepository{db: f.db}
	claimed, err := restarted.Claim(ctx, "first-submit", time.Minute)
	require.NoError(t, err)
	require.Equal(t, task.RequestPluginsEncrypted, claimed.RequestPluginsEncrypted)
	require.Equal(t, task.RequestReportEncrypted, claimed.RequestReportEncrypted)
	require.Equal(t, service.GenerationSubmitting, claimed.Status)
	claimed.Status = service.GenerationFailed
	claimed.ErrorCode = "request_processing_rejected"
	claimed.ErrorMessage = "Request rejected by request-processing policy."
	claimed.BillingStatus = service.GenerationBillingPending
	claimed.RequestReportEncrypted = "fixture-report-with-egress-records"
	now := time.Now().UTC()
	claimed.FinishedAt = &now
	require.NoError(t, restarted.SaveProgress(ctx, claimed, time.Second))
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET next_run_at=NOW() WHERE id=$1`, task.ID)
	require.NoError(t, err)
	settling, err := restarted.Claim(ctx, "settlement", time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.GenerationFailed, settling.Status, "a rejected task is not resubmitted after a restart")
	require.Equal(t, claimed.RequestReportEncrypted, settling.RequestReportEncrypted)
	require.NoError(t, restarted.Settle(ctx, settling, decimal.Zero, nil))
	require.NoError(t, restarted.Settle(ctx, settling, decimal.Zero, nil))
	var balance, frozen, quota float64
	require.NoError(t, f.db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, f.user).Scan(&balance, &frozen))
	require.Equal(t, 100.0, balance)
	require.Zero(t, frozen)
	require.NoError(t, f.db.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, f.key).Scan(&quota))
	require.Zero(t, quota)
	var events, usage int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM sub2api_generation_events WHERE task_id=$1 AND event_type='funds_settled'`, task.ID).Scan(&events))
	require.Equal(t, 1, events)
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM usage_logs WHERE request_id=$1`, task.ID).Scan(&usage))
	require.Equal(t, 1, usage)
	public, err := json.Marshal(settling)
	require.NoError(t, err)
	require.NotContains(t, string(public), task.RequestPluginsEncrypted)
	require.NotContains(t, string(public), task.RequestReportEncrypted)
	metrics, err := NewModelPlazaMetricsRepository(f.db).ReadModelSummaries(ctx, []service.PlazaMetricsScope{{ID: "video", Model: task.Model, Platform: task.Platform, GroupIDs: []int64{f.group}}}, now.Add(-time.Hour), now.Add(time.Minute))
	require.NoError(t, err)
	require.Zero(t, metrics["video"].Requests, "local policy failures are not upstream outages")
}
