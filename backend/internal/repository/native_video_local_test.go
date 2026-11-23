package repository

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestNativeVideoLocalPostgresHistoryAndLedgerProjection(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var user, key, group, account, otherAccount int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES('native-video@example.invalid','fixture',100) RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('native-video','grok') RETURNING id`).Scan(&group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'native-key','fixture',$2) RETURNING id`, user, group).Scan(&key))
	for _, dest := range []*int64{&account, &otherAccount} {
		require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('native','grok','apikey','{}',2,1) RETURNING id`).Scan(dest))
	}
	repo := NewGenerationTaskRepository(db)
	history := repo.(service.NativeVideoTaskRepository)
	logs := repo.(service.TaskLogRepository)
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	o := service.NativeVideoObservation{UserID: user, APIKeyID: key, GroupID: group, AccountID: account, UpstreamID: "shared-upstream-id", Model: "public-alias", Platform: service.PlatformGrok, Protocol: "xai_video", Action: "text_to_video", Status: service.GenerationQueued, DeliveryStatus: service.GenerationDeliveryPending, Fields: service.GenerationVideoFields{Prompt: "Ocean", Seconds: "8"}, CreatedAt: now, ObservedAt: now, Create: true}
	// Repeated observations are idempotent, including simultaneous creates.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- history.ObserveNativeVideo(ctx, o) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	id := service.NativeVideoTaskID(o)
	items, total, err := logs.ListTaskLogs(ctx, service.TaskLogFilter{UserID: user, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	require.Equal(t, "native", items[0].Source)
	require.Equal(t, "unconfirmed", items[0].BillingStatus)
	require.Nil(t, items[0].ActualAmount, "no ledger entry must not be presented as free")
	detail, err := logs.GetTaskLog(ctx, id, user)
	require.NoError(t, err)
	require.Equal(t, o.Fields, detail.Fields)
	require.Equal(t, "native_submitted", detail.Events[0].Type)
	_, err = logs.GetTaskLog(ctx, id, user+1)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)

	poll := o
	poll.Create, poll.Model, poll.Fields = false, "ignored-model", service.GenerationVideoFields{Prompt: "ignored"}
	poll.Status, poll.ObservedAt = service.GenerationRunning, now.Add(10*time.Second)
	require.NoError(t, history.ObserveNativeVideo(ctx, poll))
	// A stale queued response arriving later cannot reverse a running task.
	poll.Status, poll.ObservedAt = service.GenerationQueued, now.Add(20*time.Second)
	require.NoError(t, history.ObserveNativeVideo(ctx, poll))
	detail, err = logs.GetTaskLog(ctx, id, user)
	require.NoError(t, err)
	require.Equal(t, service.GenerationRunning, detail.Status)
	require.Equal(t, o.Model, detail.Model)
	require.Equal(t, o.Fields, detail.Fields)
	require.Equal(t, "native_running", detail.Events[1].Type)
	// An older observation timestamp cannot replace newer progress.
	poll.Status, poll.ObservedAt = service.GenerationFailed, now.Add(5*time.Second)
	require.NoError(t, history.ObserveNativeVideo(ctx, poll))
	poll.Status, poll.DeliveryStatus, poll.ObservedAt = service.GenerationSucceeded, service.GenerationDeliveryReady, now.Add(30*time.Second)
	require.NoError(t, history.ObserveNativeVideo(ctx, poll))
	finished := poll.ObservedAt
	terminalErrors := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); terminalErrors <- history.ObserveNativeVideo(ctx, poll) }()
	}
	wg.Wait()
	close(terminalErrors)
	for err := range terminalErrors {
		require.NoError(t, err)
	}
	poll.Status, poll.DeliveryStatus, poll.ObservedAt = service.GenerationFailed, "failed", now.Add(40*time.Second)
	require.NoError(t, history.ObserveNativeVideo(ctx, poll))
	detail, err = logs.GetTaskLog(ctx, id, user)
	require.NoError(t, err)
	require.Equal(t, service.GenerationSucceeded, detail.Status)
	require.Equal(t, service.GenerationDeliveryReady, detail.DeliveryStatus)
	require.True(t, finished.Equal(*detail.FinishedAt))
	require.Len(t, detail.Events, 3, "poll retries must not duplicate timeline events")
	// No guessed history for a pre-upgrade task or a different owner identity.
	poll.UpstreamID = "old-unobserved-task"
	require.NoError(t, history.ObserveNativeVideo(ctx, poll))
	_, err = logs.GetTaskLog(ctx, service.NativeVideoTaskID(poll), user)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)

	// A confirmed zero-cost ledger entry is settled. The observer itself must
	// never write a usage row, reserve funds or consume key quota.
	var entries int
	var balance, frozen, used float64
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM usage_logs`).Scan(&entries))
	require.Zero(t, entries)
	require.NoError(t, db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, user).Scan(&balance, &frozen))
	require.Equal(t, 100.0, balance)
	require.Zero(t, frozen)
	require.NoError(t, db.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, key).Scan(&used))
	require.Zero(t, used)
	_, err = db.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,group_id,request_id,model,total_cost,actual_cost) VALUES($1,$2,$3,$4,$5,'public-alias',0,0)`, user, key, account, group, service.StableGrokVideoBillingRequestID(o.UpstreamID))
	require.NoError(t, err)
	// Billing failures also write zero-cost usage rows. Only a committed dedup
	// entry (including its archive) proves that financial effects succeeded.
	detail, err = logs.GetTaskLog(ctx, id, user)
	require.NoError(t, err)
	require.Equal(t, "unconfirmed", detail.BillingStatus)
	require.Nil(t, detail.ActualAmount)
	_, err = db.Exec(`INSERT INTO usage_billing_dedup(request_id,api_key_id,request_fingerprint) VALUES($1,$2,$3)`, service.StableGrokVideoBillingRequestID(o.UpstreamID), key, strings.Repeat("c", 64))
	require.NoError(t, err)
	detail, err = logs.GetTaskLog(ctx, id, user)
	require.NoError(t, err)
	require.Equal(t, "settled", detail.BillingStatus)
	require.NotNil(t, detail.ActualAmount)
	require.True(t, detail.ActualAmount.IsZero())
	_, err = db.Exec(`UPDATE usage_logs SET actual_cost=0.75 WHERE api_key_id=$1`, key)
	require.NoError(t, err)
	detail, err = logs.GetTaskLog(ctx, id, user)
	require.NoError(t, err)
	require.Equal(t, "0.75", detail.ActualAmount.String())
	_, err = db.Exec(`INSERT INTO usage_billing_dedup_archive(request_id,api_key_id,request_fingerprint,created_at) SELECT request_id,api_key_id,request_fingerprint,created_at FROM usage_billing_dedup`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM usage_billing_dedup`)
	require.NoError(t, err)
	detail, err = logs.GetTaskLog(ctx, id, user)
	require.NoError(t, err)
	require.Equal(t, "settled", detail.BillingStatus)
	require.Equal(t, "0.75", detail.ActualAmount.String())

	// An upstream ID reused on another account must not share its history or
	// inherit another account's ledger, even with the same API key.
	other := o
	other.AccountID = otherAccount
	other.CreatedAt, other.ObservedAt = now.Add(time.Second), now.Add(time.Second)
	require.NoError(t, history.ObserveNativeVideo(ctx, other))
	otherDetail, err := logs.GetTaskLog(ctx, service.NativeVideoTaskID(other), user)
	require.NoError(t, err)
	require.Equal(t, "unconfirmed", otherDetail.BillingStatus)
	require.Nil(t, otherDetail.ActualAmount)
	// Delivery may become available after completion; the first terminal time
	// is preserved. Success and delivery are independent facts.
	other.Create, other.Status, other.DeliveryStatus, other.ObservedAt = false, service.GenerationSucceeded, "failed", now.Add(10*time.Second)
	require.NoError(t, history.ObserveNativeVideo(ctx, other))
	other.DeliveryStatus, other.ObservedAt = service.GenerationDeliveryReady, now.Add(20*time.Second)
	require.NoError(t, history.ObserveNativeVideo(ctx, other))
	otherDetail, err = logs.GetTaskLog(ctx, service.NativeVideoTaskID(other), user)
	require.NoError(t, err)
	require.Equal(t, service.GenerationDeliveryReady, otherDetail.DeliveryStatus)
	require.True(t, now.Add(10*time.Second).Equal(*otherDetail.FinishedAt))

	// The same console page must merge plugin and native tasks before filtering
	// and pagination; page boundaries cannot omit or duplicate either source.
	var version int64
	require.NoError(t, db.QueryRow(`INSERT INTO sub2api_js_plugin_versions(plugin_key,version,source,sha256,meta,capabilities) VALUES('fixture','1','fixture',$1,'{}','{}') RETURNING id`, strings.Repeat("a", 64)).Scan(&version))
	pluginID, err := service.NewGenerationTaskID()
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO sub2api_generation_tasks(id,user_id,api_key_id,group_id,account_id,plugin_version_id,idempotency_key,fingerprint,model,upstream_model,platform,protocol,action,request,connection,price,funding,hold_amount,created_at) VALUES($1,$2,$3,$4,$5,$6,'fixture',$7,'plugin-model','vendor-model','grok','openai_video','text_to_video','{}','{}','{}','{}',0,$8)`, pluginID, user, key, group, account, version, strings.Repeat("b", 64), now.Add(2*time.Second))
	require.NoError(t, err)
	for page, expected := range []string{pluginID, service.NativeVideoTaskID(other), id} {
		items, total, err = logs.ListTaskLogs(ctx, service.TaskLogFilter{UserID: user, Page: page + 1, PageSize: 1})
		require.NoError(t, err)
		require.EqualValues(t, 3, total)
		require.Len(t, items, 1)
		require.Equal(t, expected, items[0].ID)
	}
	for _, filter := range []service.TaskLogFilter{
		{UserID: user + 1}, {UserID: user, GroupID: group + 1}, {UserID: user, Model: "unknown"}, {UserID: user, ReviewOnly: true},
	} {
		filter.Page, filter.PageSize = 1, 20
		items, total, err = logs.ListTaskLogs(ctx, filter)
		require.NoError(t, err)
		require.Zero(t, total)
		require.Empty(t, items)
	}
	items, total, err = logs.ListTaskLogs(ctx, service.TaskLogFilter{UserID: user, BillingStatus: "unconfirmed", PluginKey: "native-xai_video", Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, otherDetail.ID, items[0].ID)
	items, total, err = logs.ListTaskLogs(ctx, service.TaskLogFilter{UserID: user, TaskID: o.UpstreamID, AccountID: account, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, id, items[0].ID, "native callers can search their original returned task ID")
	_, total, err = logs.ListTaskLogs(ctx, service.TaskLogFilter{UserID: user + 1, TaskID: o.UpstreamID, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Zero(t, total, "knowing an upstream ID must not cross the owner boundary")
	_, err = repo.Claim(ctx, "fixture", time.Minute)
	require.NoError(t, err, "only the plugin fixture should be claimed")
	_, err = repo.Claim(ctx, "second", time.Minute)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound, "native tasks must never be submitted or settled by the plugin worker")
	var arkAccount int64
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('ark','openai','apikey','{}',2,1) RETURNING id`).Scan(&arkAccount))
	ark := o
	ark.AccountID, ark.Platform, ark.Protocol, ark.UpstreamID = arkAccount, service.PlatformOpenAI, "ark_video", service.SeedanceTaskKey("ark-task")
	require.NoError(t, history.ObserveNativeVideo(ctx, ark))
	for _, lookup := range []string{"ark-task", "seedance:ark-task", service.NativeVideoTaskID(ark)} {
		items, total, err = logs.ListTaskLogs(ctx, service.TaskLogFilter{UserID: user, TaskID: lookup, Page: 1, PageSize: 20})
		require.NoError(t, err)
		require.EqualValues(t, 1, total)
		require.Equal(t, service.NativeVideoTaskID(ark), items[0].ID)
	}
	require.NoError(t, ApplyMigrations(ctx, db))
}
