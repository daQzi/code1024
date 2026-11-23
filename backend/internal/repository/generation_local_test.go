package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// Opt-in local PostgreSQL coverage. The DSN must identify a disposable test
// database; each run creates and drops only its own separate test database.
func generationLocalDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("SUB2API_GENERATION_TEST_DSN")
	if dsn == "" {
		t.Skip("SUB2API_GENERATION_TEST_DSN is not configured")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	var dbName string
	require.NoError(t, admin.QueryRow(`SELECT current_database()`).Scan(&dbName))
	require.True(t, strings.HasSuffix(dbName, "_test"), "local generation tests require a database name ending in _test")
	// Historical migrations target public. Use a separate disposable database
	// so these tests exercise the production migration path unchanged.
	database := "generation_" + time.Now().Format("150405_000000000") + "_test"
	_, err = admin.Exec(`CREATE DATABASE ` + database)
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn+" dbname="+database)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close(); admin.Exec(`DROP DATABASE ` + database + ` WITH (FORCE)`); admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, ApplyMigrations(ctx, db))
	return db
}

func TestGenerationLocalPostgresAtomicFundingAndRecovery(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var user, key, group, account int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES('generation-fixture@example.invalid','fixture',100) RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('generation-fixture','grok') RETURNING id`).Scan(&group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id,quota,rate_limit_5h) VALUES($1,'fixture-key','fixture',$2,20,20) RETURNING id`, user, group).Scan(&key))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('fixture','grok','apikey','{}',1,1) RETURNING id`).Scan(&account))
	plugins := NewNewAPIPluginRepository(db)
	source, err := builtin.Source("xai")
	require.NoError(t, err)
	loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	v, err := plugins.Install(ctx, &service.NewAPIPluginVersion{Key: loaded.Meta.Key, Version: loaded.Meta.Version, SHA256: strings.Repeat("a", 64), Source: source, Meta: loaded.Meta, Capabilities: runner.Descriptor{Task: true}}, nil)
	require.NoError(t, err)
	require.NoError(t, plugins.Activate(ctx, v.ID, true))
	id, err := service.NewGenerationTaskID()
	require.NoError(t, err)
	unit := decimal.NewFromInt(1)
	price, err := service.NewGenerationPriceSnapshot(service.GenerationPriceRule{Mode: "fixed", Unit: "request", UnitPrice: &unit}, decimal.NewFromInt(1), nil, decimal.NewFromInt(1))
	require.NoError(t, err)
	task := &service.GenerationTask{ID: id, UserID: user, APIKeyID: key, GroupID: group, AccountID: account, PluginVersionID: v.ID, IdempotencyKey: "same", Fingerprint: strings.Repeat("b", 64), Model: "grok-imagine-video", UpstreamModel: "grok-imagine-video", Platform: "grok", Protocol: "openai_video", Action: "text_to_video", Request: json.RawMessage(`{"prompt":"fixture"}`), Price: price}
	task.Connection = service.GenerationConnection{BaseURL: "https://fixture.example.invalid", Kind: "vendor"}
	task.PublicFields = service.GenerationVideoFields{Seconds: "8", Size: "1280x720", Prompt: "fixture"}
	command := &service.UsageBillingCommand{UserID: user, APIKeyID: key, AccountID: account, AccountType: service.AccountTypeAPIKey, BalanceCost: 1, APIKeyQuotaCost: 1, APIKeyRateLimitCost: 1}
	repo := NewGenerationTaskRepository(db)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := repo.CreateHeld(ctx, task, command); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	replayed, err := repo.FindIdempotent(ctx, user, key, "same")
	require.NoError(t, err)
	require.Equal(t, id, replayed.ID)
	_, err = repo.FindIdempotent(ctx, user+1, key, "same")
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	_, err = repo.FindIdempotent(ctx, user, key+1, "same")
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	var balance, frozen, used float64
	require.NoError(t, db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, user).Scan(&balance, &frozen))
	require.Equal(t, 99.0, balance)
	require.Equal(t, 1.0, frozen)
	require.NoError(t, db.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, key).Scan(&used))
	require.Equal(t, 1.0, used)
	conflict := *task
	conflict.Fingerprint = strings.Repeat("c", 64)
	_, err = repo.CreateHeld(ctx, &conflict, command)
	require.ErrorIs(t, err, service.ErrGenerationIdempotencyConflict)
	claimed, err := repo.Claim(ctx, "fixture-lease", time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.GenerationSubmitting, claimed.Status)
	require.Equal(t, task.PublicFields, claimed.PublicFields)
	logs := repo.(service.TaskLogRepository)
	items, total, err := logs.ListTaskLogs(ctx, service.TaskLogFilter{UserID: user, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	require.Equal(t, id, items[0].ID)
	require.Equal(t, "generation-fixture", items[0].GroupName)
	detail, err := logs.GetTaskLog(ctx, id, user)
	require.NoError(t, err)
	require.Equal(t, task.PublicFields, detail.Fields)
	require.NotEmpty(t, detail.Events)
	_, err = logs.GetTaskLog(ctx, id, user+1)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	for _, filter := range []service.TaskLogFilter{
		{UserID: user + 1}, {UserID: user, Status: "succeeded"}, {UserID: user, Model: "other"}, {UserID: user, PluginKey: "other"}, {UserID: user, AccountID: account + 1}, {UserID: user, GroupID: group + 1},
	} {
		filter.Page, filter.PageSize = 1, 20
		items, total, err := logs.ListTaskLogs(ctx, filter)
		require.NoError(t, err)
		require.Zero(t, total)
		require.Empty(t, items)
	}
	// All migrations, including task log indexes, must remain idempotent.
	require.NoError(t, ApplyMigrations(ctx, db))
	// A crashed submit cannot be blindly repeated by another replica.
	_, err = db.Exec(`UPDATE sub2api_generation_tasks SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, id)
	require.NoError(t, err)
	claimed, err = repo.Claim(ctx, "recovery-lease", time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.GenerationUncertain, claimed.Status)
	_, err = repo.Claim(ctx, "third-lease", time.Minute)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	// Simulate an operator confirming the accepted task, then worker completion.
	_, err = db.Exec(`UPDATE sub2api_generation_tasks SET status='succeeded',upstream_id='fixture-upstream',delivery_status='ready',billing_status='pending' WHERE id=$1`, id)
	require.NoError(t, err)
	claimed, err = repo.GetOwned(ctx, id, user, key)
	require.NoError(t, err)
	require.NoError(t, repo.Settle(ctx, claimed, decimal.RequireFromString("0.4"), nil))
	require.NoError(t, repo.Settle(ctx, claimed, decimal.RequireFromString("0.4"), nil))
	err = repo.Settle(ctx, claimed, decimal.RequireFromString("0.5"), nil)
	require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
	require.NoError(t, db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, user).Scan(&balance, &frozen))
	require.Equal(t, 99.6, balance)
	require.Zero(t, frozen)
	require.NoError(t, db.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, key).Scan(&used))
	require.Equal(t, 0.4, used)
	_, err = repo.GetOwned(ctx, id, user+1, key)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	var entries int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*),SUM(actual_cost) FROM usage_logs WHERE request_id=$1`, id).Scan(&entries, &used))
	require.Equal(t, 1, entries)
	require.Equal(t, 0.4, used)

	// New reservations enforce durable active-task capacity, not just the
	// milliseconds spent sending an HTTP submission.
	newTask := *task
	newTask.ID, err = service.NewGenerationTaskID()
	require.NoError(t, err)
	newTask.IdempotencyKey = "review-task"
	command.AccountQuotaCost = 2
	command.AccountQuotaHasDailyLimit = true
	command.AccountQuotaHasWeeklyLimit = true
	_, err = db.Exec(`UPDATE accounts SET extra=jsonb_build_object('quota_limit',20,'quota_daily_limit',20,'quota_weekly_limit',20) WHERE id=$1`, account)
	require.NoError(t, err)
	held, err := repo.CreateHeld(ctx, &newTask, command)
	require.NoError(t, err)
	blocked := newTask
	blocked.ID, err = service.NewGenerationTaskID()
	require.NoError(t, err)
	blocked.IdempotencyKey = "over-capacity"
	_, err = repo.CreateHeld(ctx, &blocked, command)
	require.ErrorIs(t, err, service.ErrGenerationCapacity)
	var accountUsed, dailyUsed, weeklyUsed float64
	require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_used')::numeric,(extra->>'quota_daily_used')::numeric,(extra->>'quota_weekly_used')::numeric FROM accounts WHERE id=$1`, account).Scan(&accountUsed, &dailyUsed, &weeklyUsed))
	require.Equal(t, 2.0, accountUsed)
	require.Equal(t, 2.0, dailyUsed)
	require.Equal(t, 2.0, weeklyUsed)
	claimed, err = repo.Claim(ctx, "review-lease", time.Minute)
	require.NoError(t, err)
	require.Equal(t, held.ID, claimed.ID)
	_, err = db.Exec(`UPDATE sub2api_generation_tasks SET status='submission_uncertain',billing_status='review' WHERE id=$1`, held.ID)
	require.NoError(t, err)
	// An operator cannot race a worker that still owns an active lease.
	require.Error(t, repo.Reconcile(ctx, held.ID, user, "resume_poll", "confirmed-id", "upstream confirmed", nil))
	_, err = db.Exec(`UPDATE sub2api_generation_tasks SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, held.ID)
	require.NoError(t, err)
	reviews, err := repo.ListReview(ctx, 100)
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	require.NoError(t, repo.Reconcile(ctx, held.ID, user, "resume_poll", "confirmed-id", "upstream confirmed", nil))
	require.Error(t, repo.Reconcile(ctx, held.ID, user, "refund", "", "cannot decide twice", nil))
	claimed, err = repo.Claim(ctx, "confirmed-lease", time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.GenerationRunning, claimed.Status)
	require.Equal(t, "confirmed-id", claimed.UpstreamID)
	// A later billing window belongs to other requests; refund only the total
	// and original windows. Store string windows, never JSON-encoded []byte.
	_, err = db.Exec(`UPDATE accounts SET extra=extra||'{"quota_daily_start":"2099-01-01T00:00:00Z","quota_daily_used":7}'::jsonb WHERE id=$1`, account)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE api_keys SET window_5h_start=NOW()+INTERVAL '1 day',usage_5h=7 WHERE id=$1`, key)
	require.NoError(t, err)
	claimed.Status = service.GenerationSucceeded
	claimed.BillingStatus = service.GenerationBillingReview
	claimed.DeliveryStatus = service.GenerationDeliveryReady
	now := time.Now().UTC()
	claimed.FinishedAt = &now
	require.NoError(t, repo.SaveProgress(ctx, claimed, time.Second))
	tooHigh := decimal.NewFromInt(2)
	require.Error(t, repo.Reconcile(ctx, held.ID, user, "settle", "", "confirmed charge", &tooHigh))
	require.Error(t, repo.Reconcile(ctx, held.ID, user, "resume_poll", "another-id", "cannot reopen completion", nil))
	confirmed := decimal.RequireFromString("0.4")
	require.NoError(t, repo.Reconcile(ctx, held.ID, user, "settle", "", "confirmed charge", &confirmed))
	claimed, err = repo.Claim(ctx, "settle-lease", time.Minute)
	require.NoError(t, err)
	require.NotNil(t, claimed.ReconciledAmount)
	require.True(t, confirmed.Equal(*claimed.ReconciledAmount))
	require.NoError(t, repo.Settle(ctx, claimed, confirmed, nil))
	require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_used')::numeric,(extra->>'quota_daily_used')::numeric,(extra->>'quota_weekly_used')::numeric FROM accounts WHERE id=$1`, account).Scan(&accountUsed, &dailyUsed, &weeklyUsed))
	require.InDelta(t, 0.8, accountUsed, 1e-8)
	require.Equal(t, 7.0, dailyUsed)
	require.InDelta(t, 0.8, weeklyUsed, 1e-8)
	require.NoError(t, db.QueryRow(`SELECT usage_5h FROM api_keys WHERE id=$1`, key).Scan(&used))
	require.Equal(t, 7.0, used)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sub2api_generation_events WHERE task_id=$1 AND event_type='administrator_reconciliation'`, held.ID).Scan(&entries))
	require.Equal(t, 2, entries)
	for _, order := range []string{"asc", "desc"} {
		page, err := repo.ListOwned(ctx, user, key, "", 1, order)
		require.NoError(t, err)
		require.Len(t, page, 1)
		next, err := repo.ListOwned(ctx, user, key, page[0].ID, 1, order)
		require.NoError(t, err)
		require.Len(t, next, 1)
		require.NotEqual(t, page[0].ID, next[0].ID)
		foreign, err := repo.ListOwned(ctx, user+1, key, page[0].ID, 1, order)
		require.NoError(t, err)
		require.Empty(t, foreign)
	}
	points, err := NewModelPlazaMetricsRepository(db).ReadModelMetrics(ctx, task.Model, task.Platform, []int64{group}, time.Now().Add(-time.Hour), time.Now().Add(time.Minute))
	require.NoError(t, err)
	var summary *service.PlazaMetricsPoint
	for i := range points {
		if points[i].GroupID == 0 && points[i].Bucket == nil {
			summary = &points[i]
		}
	}
	require.NotNil(t, summary)
	require.EqualValues(t, 1, summary.Requests, "task settlement usage rows must not double-count the terminal task sample")
	require.EqualValues(t, 1, summary.Successes)
	require.NotNil(t, summary.AverageMs)
	batch, err := NewModelPlazaMetricsRepository(db).ReadModelSummaries(ctx, []service.PlazaMetricsScope{
		{ID: "visible", Model: task.Model, Platform: task.Platform, GroupIDs: []int64{group}},
		{ID: "foreign-group", Model: task.Model, Platform: task.Platform, GroupIDs: []int64{group + 1}},
		{ID: "foreign-model", Model: "other", Platform: task.Platform, GroupIDs: []int64{group}},
	}, time.Now().Add(-time.Hour), time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, batch, 1, "the batch must retain the exact model/platform/group permission tuples")
	require.EqualValues(t, 1, batch["visible"].Requests)
	require.EqualValues(t, 1, batch["visible"].Successes)
}
