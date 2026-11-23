package repository

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestNativeVideoRecoveryLocalPostgresSnapshotsAndLedger(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var user, key, group, account, otherAccount int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES('native-recovery@example.invalid','fixture',100) RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('recovery','grok') RETURNING id`).Scan(&group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'recovery-key','fixture',$2) RETURNING id`, user, group).Scan(&key))
	for _, id := range []*int64{&account, &otherAccount} {
		require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('recovery','grok','apikey','{}',1,1) RETURNING id`).Scan(id))
	}
	repo := NewGenerationTaskRepository(db)
	history := repo.(service.NativeVideoTaskRepository)
	recovery := repo.(service.NativeVideoRecoveryRepository)
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-48 * time.Hour)
	pending := service.GrokVideoPendingBilling{Model: "mapped-model", BillingModel: "billing-model", UpstreamModel: "vendor-model", OriginalModel: "public-alias", VideoResolution: "720p", VideoDurationSeconds: 6, CreatedAt: now.Format(time.RFC3339Nano)}
	o := service.NativeVideoObservation{UserID: user, APIKeyID: key, GroupID: group, AccountID: account, UpstreamID: "native-task", Model: "public-alias", Platform: service.PlatformGrok, Protocol: service.GenerationProtocolXAI, Action: "text_to_video", Status: service.GenerationQueued, DeliveryStatus: service.GenerationDeliveryPending, CreatedAt: now, ObservedAt: now, Create: true, PendingBilling: &pending}
	require.NoError(t, history.ObserveNativeVideo(ctx, o))
	q := service.NativeVideoLookup{UserID: user, APIKeyID: key, GroupID: group, Protocol: o.Protocol, UpstreamID: o.UpstreamID}
	loaded, err := recovery.GetNativeVideoRecovery(ctx, q)
	require.NoError(t, err)
	require.Equal(t, account, loaded.AccountID)
	require.Equal(t, &pending, loaded.PendingBilling, "snapshot outlives the 24h Redis TTL")

	// A repeated create must not overwrite the first billing model or clock.
	changed := pending
	changed.Model, changed.CreatedAt = "new-model", time.Now().UTC().Format(time.RFC3339Nano)
	repeated := o
	repeated.PendingBilling = &changed
	require.NoError(t, history.ObserveNativeVideo(ctx, repeated))
	poll := o
	poll.Create, poll.PendingBilling, poll.Status, poll.ObservedAt = false, nil, service.GenerationRunning, now.Add(time.Minute)
	require.NoError(t, history.ObserveNativeVideo(ctx, poll))
	loaded, err = recovery.GetNativeVideoRecovery(ctx, q)
	require.NoError(t, err)
	require.Equal(t, &pending, loaded.PendingBilling)
	// Public console projection never includes private billing inputs.
	detail, err := repo.(service.TaskLogRepository).GetTaskLog(ctx, service.NativeVideoTaskID(o), user)
	require.NoError(t, err)
	encoded, err := json.Marshal(detail)
	require.NoError(t, err)
	for _, private := range []string{"pending_billing", "billing-model", "vendor-model", "mapped-model"} {
		require.NotContains(t, string(encoded), private)
	}
	for name, mutate := range map[string]func(*service.NativeVideoLookup){
		"user":     func(q *service.NativeVideoLookup) { q.UserID++ },
		"key":      func(q *service.NativeVideoLookup) { q.APIKeyID++ },
		"group":    func(q *service.NativeVideoLookup) { q.GroupID++ },
		"protocol": func(q *service.NativeVideoLookup) { q.Protocol = service.GenerationProtocolArk },
		"task":     func(q *service.NativeVideoLookup) { q.UpstreamID = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			wrong := q
			mutate(&wrong)
			_, err := recovery.GetNativeVideoRecovery(ctx, wrong)
			require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
		})
	}
	// Incomplete pre-upgrade history proves ownership only, never a price or
	// model snapshot. Polls cannot fill missing fields after the fact.
	old := o
	old.UpstreamID, old.PendingBilling = "old-observed", nil
	require.NoError(t, history.ObserveNativeVideo(ctx, old))
	old.PendingBilling = &pending
	require.NoError(t, history.ObserveNativeVideo(ctx, old))
	oldQuery := q
	oldQuery.UpstreamID = old.UpstreamID
	loaded, err = recovery.GetNativeVideoRecovery(ctx, oldQuery)
	require.NoError(t, err)
	require.Equal(t, account, loaded.AccountID)
	require.Nil(t, loaded.PendingBilling)
	// Ark keys must retain their namespace in both ownership and the ledger.
	ark := o
	ark.Platform, ark.Protocol, ark.UpstreamID = service.PlatformOpenAI, service.GenerationProtocolArk, service.SeedanceTaskKey("native-task")
	require.NoError(t, history.ObserveNativeVideo(ctx, ark))
	arkQuery := q
	arkQuery.Protocol, arkQuery.UpstreamID = ark.Protocol, ark.UpstreamID
	_, err = recovery.GetNativeVideoRecovery(ctx, arkQuery)
	require.NoError(t, err)
	arkQuery.UpstreamID = "native-task"
	_, err = recovery.GetNativeVideoRecovery(ctx, arkQuery)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)

	// Resolving history performs no financial operations. Native tasks are
	// never claimed by the plugin executor or converted into frozen tasks.
	var balance, frozen, used float64
	var pluginTasks, ledgerEntries int
	require.NoError(t, db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, user).Scan(&balance, &frozen))
	require.Equal(t, 100.0, balance)
	require.Zero(t, frozen)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sub2api_generation_tasks`).Scan(&pluginTasks))
	require.Zero(t, pluginTasks)
	_, err = repo.Claim(ctx, "must-not-run-native", time.Minute)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	// Redis claim eviction permits another completion attempt; durable native
	// dedup, including its archive, must still charge exactly once.
	billing := NewUsageBillingRepository(nil, db)
	command := service.UsageBillingCommand{RequestID: service.StableGrokVideoBillingRequestID(o.UpstreamID), UserID: user, APIKeyID: key, AccountID: account, AccountType: service.AccountTypeAPIKey, Model: pending.BillingModel, BalanceCost: .75, APIKeyQuotaCost: .75}
	var applied atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := command
			result, err := billing.Apply(ctx, &copy)
			if err == nil && result.Applied {
				applied.Add(1)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, applied.Load())
	_, err = db.Exec(`INSERT INTO usage_billing_dedup_archive(request_id,api_key_id,request_fingerprint,created_at) SELECT request_id,api_key_id,request_fingerprint,created_at FROM usage_billing_dedup`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM usage_billing_dedup`)
	require.NoError(t, err)
	result, err := billing.Apply(ctx, &command)
	require.NoError(t, err)
	require.False(t, result.Applied)
	require.NoError(t, db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, user).Scan(&balance, &frozen))
	require.Equal(t, 99.25, balance)
	require.Zero(t, frozen)
	require.NoError(t, db.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, key).Scan(&used))
	require.Equal(t, .75, used)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM usage_billing_dedup_archive`).Scan(&ledgerEntries))
	require.Equal(t, 1, ledgerEntries)

	// A reused upstream ID is ambiguous even when one account lacks a snapshot.
	other := o
	other.AccountID, other.PendingBilling = otherAccount, nil
	require.NoError(t, history.ObserveNativeVideo(ctx, other))
	_, err = recovery.GetNativeVideoRecovery(ctx, q)
	require.ErrorIs(t, err, service.ErrNativeVideoRecoveryAmbiguous)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = recovery.GetNativeVideoRecovery(cancelled, q)
	require.ErrorIs(t, err, context.Canceled)
	// The migration may run repeatedly without modifying captured snapshots.
	_, err = db.Exec(`DELETE FROM schema_migrations WHERE filename='260_native_video_recovery.sql'`)
	require.NoError(t, err)
	require.NoError(t, ApplyMigrations(ctx, db))
	_, err = recovery.GetNativeVideoRecovery(ctx, oldQuery)
	require.NoError(t, err)
	// Reproduce the actual pre-260 schema with existing histories, then upgrade.
	// There is no guessed backfill, and existing ownership/ledger rows survive.
	_, err = db.Exec(`DROP VIEW sub2api_task_log_records`)
	require.NoError(t, err)
	_, err = db.Exec(`ALTER TABLE sub2api_native_video_tasks DROP COLUMN pending_billing`)
	require.NoError(t, err)
	_, err = db.Exec(`DROP INDEX sub2api_native_video_tasks_recovery`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM schema_migrations WHERE filename IN('260_native_video_recovery.sql','262_native_video_settlement.sql')`)
	require.NoError(t, err)
	require.NoError(t, ApplyMigrations(ctx, db))
	loaded, err = recovery.GetNativeVideoRecovery(ctx, oldQuery)
	require.NoError(t, err)
	require.Equal(t, account, loaded.AccountID)
	require.Nil(t, loaded.PendingBilling)
	for _, invalid := range []string{`null`, `[]`, `"secret"`} {
		_, err = db.Exec(`UPDATE sub2api_native_video_tasks SET pending_billing=$1::jsonb WHERE id=$2`, invalid, service.NativeVideoTaskID(old))
		require.Error(t, err, "SQL rejects non-object snapshots")
	}
	_, err = db.Exec(`UPDATE sub2api_native_video_tasks SET pending_billing='{}' WHERE id=$1`, service.NativeVideoTaskID(old))
	require.NoError(t, err)
	_, err = recovery.GetNativeVideoRecovery(ctx, oldQuery)
	require.Error(t, err, "corrupt private inputs must never fall back to public fields")
}
