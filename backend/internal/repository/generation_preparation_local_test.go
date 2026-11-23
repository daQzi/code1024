package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestGenerationPreparationLocalSafeRecoveryAndPaidBoundary(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	task, command := f.task(t, &service.GenerationFile{})
	task.Files, task.PreparationStatus = nil, "pending"
	_, err := f.repo.CreateHeld(ctx, task, command)
	require.NoError(t, err)
	claimed, err := f.repo.Claim(ctx, "upload-worker", time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.GenerationSubmitting, claimed.Status)
	claimed.PreparationEncrypted = "encrypted-private-upload-state"
	require.NoError(t, f.repo.CheckpointPreparation(ctx, claimed))
	// A crash during upload must resume preparation, never claim that a paid
	// generation might have been sent. The ciphertext survives repository reload.
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	restarted := &generationTaskRepository{db: f.db}
	resumed, err := restarted.Claim(ctx, "upload-restart", time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.GenerationSubmitting, resumed.Status)
	require.Equal(t, "pending", resumed.PreparationStatus)
	require.Equal(t, claimed.PreparationEncrypted, resumed.PreparationEncrypted)
	require.ErrorIs(t, f.repo.CheckpointPreparation(ctx, claimed), service.ErrGenerationLeaseLost)
	resumed.Status, resumed.PreparationStatus = service.GenerationQueued, "ready"
	require.NoError(t, restarted.SaveProgress(ctx, resumed, time.Second))
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET next_run_at=NOW()-INTERVAL '1 second' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	paid, err := restarted.Claim(ctx, "paid-worker", time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.GenerationSubmitting, paid.Status)
	require.Equal(t, "ready", paid.PreparationStatus)
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	lost, err := restarted.Claim(ctx, "paid-lost", time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.GenerationUncertain, lost.Status, "past the paid boundary, never submit automatically again")
	_, err = restarted.Claim(ctx, "must-not-repeat", time.Minute)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	var balance, frozen float64
	require.NoError(t, f.db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, f.user).Scan(&balance, &frozen))
	require.Equal(t, 99.0, balance)
	require.Equal(t, 1.0, frozen)
}

func TestGenerationPreparationLocalCleanupIndependentOfRefund(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	task, command := f.task(t, &service.GenerationFile{})
	task.Files, task.PreparationStatus = nil, "pending"
	_, err := f.repo.CreateHeld(ctx, task, command)
	require.NoError(t, err)
	claimed, err := f.repo.Claim(ctx, "failed-upload", time.Minute)
	require.NoError(t, err)
	claimed.PreparationEncrypted = "encrypted-session-to-abort"
	require.NoError(t, f.repo.CheckpointPreparation(ctx, claimed))
	claimed.Status, claimed.BillingStatus, claimed.PreparationStatus = service.GenerationFailed, service.GenerationBillingPending, "cleanup"
	require.NoError(t, f.repo.SaveProgress(ctx, claimed, time.Second))
	_, err = f.repo.ClaimPreparationCleanup(ctx, "premature-cleanup", time.Minute)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET next_run_at=NOW()-INTERVAL '1 second' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	settlement, err := f.repo.Claim(ctx, "refund", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.repo.Settle(ctx, settlement, decimal.Zero, nil))
	var balance, frozen float64
	require.NoError(t, f.db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, f.user).Scan(&balance, &frozen))
	require.Equal(t, 100.0, balance, "refund must finish even while upstream storage is offline")
	require.Zero(t, frozen)
	cleanup, err := f.repo.ClaimPreparationCleanup(ctx, "cleanup-worker", time.Minute)
	require.NoError(t, err)
	_, err = f.repo.ClaimPreparationCleanup(ctx, "cleanup-conflict", time.Minute)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	cleanup.PreparationEncrypted = "encrypted-one-part-aborted"
	require.NoError(t, f.repo.CheckpointPreparation(ctx, cleanup))
	require.NoError(t, f.repo.FinishPreparationCleanup(ctx, cleanup, false, time.Second))
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET next_run_at=NOW()-INTERVAL '1 second' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	restarted := &generationTaskRepository{db: f.db}
	cleanup, err = restarted.ClaimPreparationCleanup(ctx, "cleanup-restarted", time.Minute)
	require.NoError(t, err)
	require.Equal(t, "encrypted-one-part-aborted", cleanup.PreparationEncrypted)
	require.NoError(t, restarted.FinishPreparationCleanup(ctx, cleanup, true, time.Second))
	require.ErrorIs(t, restarted.FinishPreparationCleanup(ctx, cleanup, true, time.Second), service.ErrGenerationLeaseLost)
	loaded, err := restarted.GetOwned(ctx, task.ID, f.user, f.key)
	require.NoError(t, err)
	require.Equal(t, "cleaned", loaded.PreparationStatus)
	require.Empty(t, loaded.PreparationEncrypted)
	require.Equal(t, service.GenerationBillingSettled, loaded.BillingStatus)
	require.NoError(t, ApplyMigrations(ctx, f.db))
}

func TestGenerationPreparationLocalRejectsOversizedAndForeignCheckpoints(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	task, command := f.task(t, &service.GenerationFile{})
	task.Files, task.PreparationStatus = nil, "pending"
	_, err := f.repo.CreateHeld(ctx, task, command)
	require.NoError(t, err)
	claimed, err := f.repo.Claim(ctx, "upload-bounds", time.Minute)
	require.NoError(t, err)
	claimed.PreparationEncrypted = strings.Repeat("x", 98305)
	require.Error(t, f.repo.CheckpointPreparation(ctx, claimed))
	claimed.PreparationEncrypted, claimed.LeaseToken = "sealed", "foreign"
	require.ErrorIs(t, f.repo.CheckpointPreparation(ctx, claimed), service.ErrGenerationLeaseLost)
	var stored string
	require.NoError(t, f.db.QueryRow(`SELECT preparation_encrypted FROM sub2api_generation_tasks WHERE id=$1`, task.ID).Scan(&stored))
	require.Empty(t, stored)
	view, err := json.Marshal(claimed)
	require.NoError(t, err)
	require.NotContains(t, string(view), "sealed")
}

func TestGenerationPreparationLocalSettlementClearsCompletedUploadSecrets(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	task, command := f.task(t, &service.GenerationFile{})
	task.Files = nil
	_, err := f.repo.CreateHeld(ctx, task, command)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET status='failed',billing_status='pending',preparation_status='ready',preparation_encrypted='encrypted-original-credential' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	claimed, err := f.repo.Claim(ctx, "clear-upload-secrets", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.repo.Settle(ctx, claimed, decimal.Zero, nil))
	require.Equal(t, "cleaned", claimed.PreparationStatus)
	require.Empty(t, claimed.PreparationEncrypted)
	loaded, err := f.repo.GetOwned(ctx, task.ID, f.user, f.key)
	require.NoError(t, err)
	require.Equal(t, "cleaned", loaded.PreparationStatus)
	require.Empty(t, loaded.PreparationEncrypted)
}
