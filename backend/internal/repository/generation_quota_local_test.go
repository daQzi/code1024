//go:build unit

package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func generationQuotaTask(t *testing.T, f *generationFileSQLFixture, status string, subscription ...int64) *service.GenerationTask {
	t.Helper()
	task, command := f.task(t, &service.GenerationFile{})
	task.Files = nil
	if len(subscription) > 0 {
		command.SubscriptionID = &subscription[0]
		command.BalanceCost, command.SubscriptionCost = 0, 1
	}
	_, err := f.repo.CreateHeld(context.Background(), task, command)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET status=$2,billing_status='pending',lease_token='quota-fixture',lease_until=NOW()+INTERVAL '1 minute',finished_at=NOW() WHERE id=$1`, task.ID, status)
	require.NoError(t, err)
	task, err = f.repo.GetOwned(context.Background(), task.ID, f.user, f.key)
	require.NoError(t, err)
	return task
}

func generationQuotaSnapshot(entry *service.UserPlatformQuotaCacheEntry, user int64) UserPlatformQuotaSnapshot {
	return UserPlatformQuotaSnapshot{QuotaID: entry.QuotaID, GenerationCursor: entry.GenerationCursor, UserID: user, Platform: "openai", DailyUsageUSD: entry.DailyUsageUSD, WeeklyUsageUSD: entry.WeeklyUsageUSD, MonthlyUsageUSD: entry.MonthlyUsageUSD, DailyWindowStart: *entry.DailyWindowStart, WeeklyWindowStart: *entry.WeeklyWindowStart, MonthlyWindowStart: *entry.MonthlyWindowStart}
}

func TestGenerationQuotaLocalCrashReplaySnapshotsAndReset(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, f.db)))
	quotaRepo := &userPlatformQuotaRepository{client: client}
	now := time.Now().Truncate(time.Second)
	day, week, month := timezone.StartOfDay(now), timezone.StartOfWeek(now), now.Add(-24*time.Hour)
	var quotaID int64
	require.NoError(t, f.db.QueryRow(`INSERT INTO user_platform_quotas(user_id,platform,daily_limit_usd,weekly_limit_usd,monthly_limit_usd,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start,monthly_window_start)
 VALUES($1,'openai',100,100,100,2,2,2,$2,$3,$4) RETURNING id`, f.user, day, week, month).Scan(&quotaID))
	cache, _ := newMiniRedisCache(t)
	limit := float64(100)
	seed := &service.UserPlatformQuotaCacheEntry{QuotaID: quotaID, DailyUsageUSD: 2, WeeklyUsageUSD: 2, MonthlyUsageUSD: 2, SchemaVersion: service.UserPlatformQuotaCacheSchemaV1, DailyLimitUSD: &limit, WeeklyLimitUSD: &limit, MonthlyLimitUSD: &limit, DailyWindowStart: &day, WeeklyWindowStart: &week, MonthlyWindowStart: &month}
	require.NoError(t, cache.SetUserPlatformQuotaCache(ctx, f.user, "openai", seed, time.Hour))
	stale := generationQuotaSnapshot(seed, f.user)
	first := generationQuotaTask(t, f, service.GenerationSucceeded)
	require.NoError(t, f.repo.Settle(ctx, first, decimal.RequireFromString("0.4"), nil))
	require.NoError(t, f.repo.Settle(ctx, first, decimal.RequireFromString("0.4"), nil))
	read, err := quotaRepo.GetByUserPlatform(ctx, f.user, "openai")
	require.NoError(t, err)
	require.InDelta(t, 2.4, read.DailyUsageUSD, 1e-9, "quota must commit even when the process never reaches Redis")
	require.Positive(t, read.GenerationCursor)
	second := generationQuotaTask(t, f, service.GenerationSucceeded)
	require.NoError(t, f.repo.Settle(ctx, second, decimal.RequireFromString("0.3"), nil))
	var count int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM sub2api_generation_quota_outbox`).Scan(&count))
	require.Equal(t, 2, count, "duplicate settlement cannot append a second quota event")
	event1, err := f.repo.ClaimGenerationQuota(ctx, "worker-1", 10*time.Second)
	require.NoError(t, err)
	require.Equal(t, first.ID, event1.TaskID)
	_, err = f.repo.ClaimGenerationQuota(ctx, "worker-2", 10*time.Second)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound, "a later event for this quota cannot pass the leased head")
	for range 2 {
		require.NoError(t, cache.ApplyGenerationQuota(ctx, event1, time.Hour, true))
	}
	require.NoError(t, f.repo.CompleteGenerationQuota(ctx, event1, true))
	event2, err := f.repo.ClaimGenerationQuota(ctx, "worker-2", 10*time.Second)
	require.NoError(t, err)
	require.NoError(t, cache.ApplyGenerationQuota(ctx, event2, time.Hour, true))
	// Crash after Redis commit but before acknowledging SQL; a new repository
	// reclaims the same event and must not increment Redis a second time.
	_, err = f.db.Exec(`UPDATE sub2api_generation_quota_outbox SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, event2.ID)
	require.NoError(t, err)
	restarted := &generationTaskRepository{db: f.db}
	replayed, err := restarted.ClaimGenerationQuota(ctx, "restarted-worker", 10*time.Second)
	require.NoError(t, err)
	require.Equal(t, event2.ID, replayed.ID)
	require.NoError(t, cache.ApplyGenerationQuota(ctx, replayed, time.Hour, true))
	require.NoError(t, restarted.CompleteGenerationQuota(ctx, replayed, true))
	require.ErrorIs(t, f.repo.CompleteGenerationQuota(ctx, event2, true), service.ErrGenerationLeaseLost)
	entry, found, err := cache.GetUserPlatformQuotaCache(ctx, f.user, "openai")
	require.NoError(t, err)
	require.True(t, found)
	require.InDelta(t, 2.7, entry.DailyUsageUSD, 1e-9)
	require.Equal(t, event2.ID, entry.GenerationCursor)
	// Both a stale DB seed and a stale Redis snapshot must preserve Tasks.
	require.NoError(t, cache.SetUserPlatformQuotaCache(ctx, f.user, "openai", seed, time.Hour))
	require.NoError(t, quotaRepo.BatchSnapshotUsage(ctx, []UserPlatformQuotaSnapshot{stale}, time.Now()))
	read, err = quotaRepo.GetByUserPlatform(ctx, f.user, "openai")
	require.NoError(t, err)
	require.InDelta(t, 2.7, read.DailyUsageUSD, 1e-9)
	require.NoError(t, cache.IncrUserPlatformQuotaUsageCache(ctx, f.user, "openai", 0.5, time.Hour, true))
	entry, _, err = cache.GetUserPlatformQuotaCache(ctx, f.user, "openai")
	require.NoError(t, err)
	require.InDelta(t, 3.2, entry.DailyUsageUSD, 1e-9)
	current := generationQuotaSnapshot(entry, f.user)
	require.NoError(t, quotaRepo.BatchSnapshotUsage(ctx, []UserPlatformQuotaSnapshot{current}, time.Now()))
	read, err = quotaRepo.GetByUserPlatform(ctx, f.user, "openai")
	require.NoError(t, err)
	require.InDelta(t, 3.2, read.DailyUsageUSD, 1e-9, "ordinary increments and already included Task deltas must combine once")
	// Expiry/flush of Redis is recoverable from DB's atomic usage + cursor.
	require.NoError(t, cache.DeleteUserPlatformQuotaCache(ctx, f.user, "openai"))
	seed.QuotaID, seed.GenerationCursor = read.QuotaID, read.GenerationCursor
	seed.DailyUsageUSD, seed.WeeklyUsageUSD, seed.MonthlyUsageUSD = read.DailyUsageUSD, read.WeeklyUsageUSD, read.MonthlyUsageUSD
	require.NoError(t, cache.SetUserPlatformQuotaCache(ctx, f.user, "openai", seed, time.Hour))
	require.NoError(t, cache.ApplyGenerationQuota(ctx, replayed, time.Hour, true))
	entry, _, err = cache.GetUserPlatformQuotaCache(ctx, f.user, "openai")
	require.NoError(t, err)
	require.InDelta(t, 3.2, entry.DailyUsageUSD, 1e-9)
	// Manual reset protects its window from an already-read snapshot, without
	// erasing the other two windows or replaying delivered Task charges.
	require.NoError(t, quotaRepo.ResetExpiredWindow(ctx, f.user, "openai", "daily", time.Now()))
	require.NoError(t, quotaRepo.BatchSnapshotUsage(ctx, []UserPlatformQuotaSnapshot{current}, time.Now()))
	read, err = quotaRepo.GetByUserPlatform(ctx, f.user, "openai")
	require.NoError(t, err)
	require.Zero(t, read.DailyUsageUSD)
	require.InDelta(t, 3.2, read.WeeklyUsageUSD, 1e-9)
	// Removing/recreating a limit is a new identity. Old snapshots and events
	// cannot charge the new quota row even when user and platform are equal.
	require.NoError(t, quotaRepo.UpsertForUser(ctx, f.user, nil))
	require.NoError(t, quotaRepo.UpsertForUser(ctx, f.user, []UserPlatformQuotaRecord{{Platform: "openai", DailyLimitUSD: &limit}}))
	read, err = quotaRepo.GetByUserPlatform(ctx, f.user, "openai")
	require.NoError(t, err)
	require.Greater(t, read.QuotaID, quotaID)
	require.NoError(t, quotaRepo.BatchSnapshotUsage(ctx, []UserPlatformQuotaSnapshot{current}, time.Now()))
	require.NoError(t, cache.DeleteUserPlatformQuotaCache(ctx, f.user, "openai"))
	seed.QuotaID, seed.GenerationCursor = read.QuotaID, read.GenerationCursor
	seed.DailyUsageUSD = 0
	require.NoError(t, cache.SetUserPlatformQuotaCache(ctx, f.user, "openai", seed, time.Hour))
	require.NoError(t, cache.ApplyGenerationQuota(ctx, replayed, time.Hour, true))
	entry, _, err = cache.GetUserPlatformQuotaCache(ctx, f.user, "openai")
	require.NoError(t, err)
	require.Zero(t, entry.DailyUsageUSD)
}

func TestGenerationQuotaLocalAtomicRollbackAndNoQuotaCreation(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	_, err := f.db.Exec(`INSERT INTO user_platform_quotas(user_id,platform,daily_limit_usd) VALUES($1,'openai',100)`, f.user)
	require.NoError(t, err)
	task := generationQuotaTask(t, f, service.GenerationSucceeded)
	_, err = f.db.Exec(`CREATE FUNCTION reject_generation_quota_fixture() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'fixture quota failure'; END$$;
 CREATE TRIGGER reject_generation_quota_fixture BEFORE INSERT ON sub2api_generation_quota_outbox FOR EACH ROW EXECUTE FUNCTION reject_generation_quota_fixture()`)
	require.NoError(t, err)
	require.Error(t, f.repo.Settle(ctx, task, decimal.RequireFromString("0.5"), nil))
	var frozen, balance decimal.Decimal
	require.NoError(t, f.db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, f.user).Scan(&balance, &frozen))
	require.Equal(t, "99", balance.String())
	require.Equal(t, "1", frozen.String())
	var count int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM usage_logs WHERE request_id=$1`, task.ID).Scan(&count))
	require.Zero(t, count)
	_, err = f.db.Exec(`DROP TRIGGER reject_generation_quota_fixture ON sub2api_generation_quota_outbox; DROP FUNCTION reject_generation_quota_fixture()`)
	require.NoError(t, err)
	require.NoError(t, f.repo.Settle(ctx, task, decimal.RequireFromString("0.5"), nil))
	// A failed/free task emits no positive quota delta.
	failed := generationQuotaTask(t, f, service.GenerationFailed)
	require.NoError(t, f.repo.Settle(ctx, failed, decimal.Zero, nil))
	var subscription int64
	require.NoError(t, f.db.QueryRow(`INSERT INTO user_subscriptions(user_id,group_id,starts_at,expires_at) VALUES($1,$2,NOW(),NOW()+INTERVAL '1 day') RETURNING id`, f.user, f.group).Scan(&subscription))
	subscribed := generationQuotaTask(t, f, service.GenerationSucceeded, subscription)
	require.NoError(t, f.repo.Settle(ctx, subscribed, decimal.RequireFromString("0.5"), nil))
	_, err = f.db.Exec(`UPDATE user_platform_quotas SET deleted_at=NOW() WHERE user_id=$1`, f.user)
	require.NoError(t, err)
	unlimited := generationQuotaTask(t, f, service.GenerationSucceeded)
	require.NoError(t, f.repo.Settle(ctx, unlimited, decimal.RequireFromString("0.5"), nil))
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM sub2api_generation_quota_outbox`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM user_platform_quotas WHERE deleted_at IS NULL`).Scan(&count))
	require.Zero(t, count, "settlement must not recreate a removed quota")
	// Outbox payload contains no prompt, account credentials or request body.
	var columns string
	require.NoError(t, f.db.QueryRow(`SELECT string_agg(column_name,',') FROM information_schema.columns WHERE table_name='sub2api_generation_quota_outbox'`).Scan(&columns))
	for _, forbidden := range []string{"prompt", "credential", "request_body"} {
		require.NotContains(t, strings.ToLower(columns), forbidden)
	}
}

func TestGenerationQuotaLocalSnapshotWaitsForConcurrentProjection(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, f.db)))
	quotaRepo := &userPlatformQuotaRepository{client: client}
	now := time.Now().Truncate(time.Second)
	day, week := timezone.StartOfDay(now), timezone.StartOfWeek(now)
	var quotaID int64
	require.NoError(t, f.db.QueryRow(`INSERT INTO user_platform_quotas(user_id,platform,daily_limit_usd,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start,monthly_window_start)
 VALUES($1,'openai',100,2,2,2,$2,$3,$2) RETURNING id`, f.user, day, week).Scan(&quotaID))
	task := generationQuotaTask(t, f, service.GenerationSucceeded)
	tx, err := f.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	// Exercise the same projection helper under an uncommitted quota lock.
	require.NoError(t, applyGenerationQuotaTx(ctx, tx, task.ID, f.user, decimal.RequireFromString("0.4")))
	finished := make(chan error, 1)
	go func() {
		finished <- quotaRepo.BatchSnapshotUsage(ctx, []UserPlatformQuotaSnapshot{{QuotaID: quotaID, UserID: f.user, Platform: "openai", DailyUsageUSD: 2, WeeklyUsageUSD: 2, MonthlyUsageUSD: 2, DailyWindowStart: day, WeeklyWindowStart: week, MonthlyWindowStart: day}}, now)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		require.NoError(t, f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT q.id FROM user_platform_quotas q JOIN%')`).Scan(&waiting))
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("snapshot writer did not wait for the quota row lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(t, tx.Commit())
	require.NoError(t, <-finished)
	read, err := quotaRepo.GetByUserPlatform(ctx, f.user, "openai")
	require.NoError(t, err)
	require.InDelta(t, 2.4, read.DailyUsageUSD, 1e-9, "the post-lock statement must see the newly committed event")
}
