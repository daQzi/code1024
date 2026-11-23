package repository

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestNativeVideoPollLocalPostgresLeasesAndHistory(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var user, key, group, account int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES('native-poll@example.invalid','fixture',100) RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('native-poll','grok') RETURNING id`).Scan(&group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'native-poll-key','fixture',$2) RETURNING id`, user, group).Scan(&key))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('native-poll','grok','apikey','{}',1,1) RETURNING id`).Scan(&account))
	repo := NewGenerationTaskRepository(db)
	history, polls, logs := repo.(service.NativeVideoTaskRepository), repo.(service.NativeVideoPollRepository), repo.(service.TaskLogRepository)
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	o := service.NativeVideoObservation{UserID: user, APIKeyID: key, GroupID: group, AccountID: account, UpstreamID: "native-task", Model: "public-model", Platform: service.PlatformGrok, Protocol: service.GenerationProtocolXAI, Action: "text_to_video", Status: service.GenerationQueued, DeliveryStatus: service.GenerationDeliveryPending, CreatedAt: now, ObservedAt: now, Create: true, PollConnectionHash: strings.Repeat("a", 64)}
	require.NoError(t, history.ObserveNativeVideo(ctx, o))
	id := service.NativeVideoTaskID(o)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sub2api_native_video_polls`).Scan(&count))
	require.Equal(t, 1, count)
	// An observer retry cannot replace the pinned connection or spawn a job.
	repeated := o
	repeated.PollConnectionHash = strings.Repeat("b", 64)
	require.NoError(t, history.ObserveNativeVideo(ctx, repeated))
	var hash string
	require.NoError(t, db.QueryRow(`SELECT connection_hash FROM sub2api_native_video_polls WHERE task_id=$1`, id).Scan(&hash))
	require.Equal(t, o.PollConnectionHash, hash)

	// Eight processes racing for one due job get exactly one lease.
	var wg sync.WaitGroup
	claimed := make(chan *service.NativeVideoPollTask, 8)
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, err := polls.ClaimNativeVideoPoll(ctx, strings.Repeat(string(rune('a'+i)), 16), time.Minute)
			if err == nil {
				claimed <- task
			} else {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(claimed)
	close(errs)
	require.Len(t, claimed, 1)
	for err := range errs {
		require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	}
	task := <-claimed
	require.Equal(t, o.AccountID, task.Observation.AccountID)
	require.Equal(t, o.PollConnectionHash, task.ConnectionHash)
	// A crashed process's lease expires and a fresh repository recovers it.
	_, err := db.Exec(`UPDATE sub2api_native_video_polls SET lease_until=NOW()-INTERVAL '1 second' WHERE task_id=$1`, id)
	require.NoError(t, err)
	restarted := NewGenerationTaskRepository(db).(service.NativeVideoPollRepository)
	recovered, err := restarted.ClaimNativeVideoPoll(ctx, "restarted", time.Minute)
	require.NoError(t, err)
	update := service.NativeVideoPollUpdate{ObservedAt: now.Add(10 * time.Second), State: &service.NativeVideoState{Status: service.GenerationRunning, DeliveryStatus: service.GenerationDeliveryPending}, MonitorStatus: service.NativeVideoMonitorActive, Delay: time.Second}
	require.ErrorIs(t, polls.SaveNativeVideoPoll(ctx, task, update), service.ErrGenerationLeaseLost)
	detail, err := logs.GetTaskLog(ctx, id, user)
	require.NoError(t, err)
	require.Equal(t, service.GenerationQueued, detail.Status, "losing lease cannot update lifecycle separately")
	require.Equal(t, service.NativeVideoMonitorActive, detail.MonitorStatus)
	// The foreground path locks history first. While a worker waits for it,
	// the monitor must remain unlocked so the foreground can finish safely.
	foreground, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = foreground.Rollback() })
	var lockedID string
	require.NoError(t, foreground.QueryRow(`SELECT id FROM sub2api_native_video_tasks WHERE id=$1 FOR UPDATE`, id).Scan(&lockedID))
	saveCtx, cancelSave := context.WithTimeout(ctx, 5*time.Second)
	defer cancelSave()
	saved := make(chan error, 1)
	go func() { saved <- restarted.SaveNativeVideoPoll(saveCtx, recovered, update) }()
	require.Eventually(t, func() bool {
		var waiting int
		err := db.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT id FROM sub2api_native_video_tasks%'`).Scan(&waiting)
		return err == nil && waiting == 1
	}, 2*time.Second, 10*time.Millisecond)
	require.NoError(t, foreground.QueryRow(`SELECT task_id FROM sub2api_native_video_polls WHERE task_id=$1 FOR UPDATE NOWAIT`, id).Scan(&lockedID))
	require.NoError(t, foreground.Commit())
	require.NoError(t, <-saved)
	detail, err = logs.GetTaskLog(ctx, id, user)
	require.NoError(t, err)
	require.Equal(t, service.GenerationRunning, detail.Status)
	require.Len(t, detail.Events, 2)

	claimNext := func() *service.NativeVideoPollTask {
		t.Helper()
		_, err := db.Exec(`UPDATE sub2api_native_video_polls SET next_run_at=NOW()-INTERVAL '1 second' WHERE task_id=$1`, id)
		require.NoError(t, err)
		task, err := polls.ClaimNativeVideoPoll(ctx, "next", time.Minute)
		require.NoError(t, err)
		return task
	}
	// Unknown responses and busy accounts cannot mark a generation failed.
	task = claimNext()
	update.State, update.ErrorCode, update.ObservedAt = nil, "native_monitor_upstream_error", now.Add(20*time.Second)
	require.NoError(t, polls.SaveNativeVideoPoll(ctx, task, update))
	task = claimNext()
	require.Equal(t, 1, task.Failures)
	update.ErrorCode = "native_monitor_capacity"
	require.NoError(t, polls.SaveNativeVideoPoll(ctx, task, update))
	task = claimNext()
	require.Equal(t, 1, task.Failures, "capacity waits must not exhaust upstream error retries")
	update.MonitorStatus, update.ErrorCode = service.NativeVideoMonitorReview, "native_monitor_connection_changed"
	require.NoError(t, polls.SaveNativeVideoPoll(ctx, task, update))
	detail, err = logs.GetTaskLog(ctx, id, user)
	require.NoError(t, err)
	require.Equal(t, service.GenerationRunning, detail.Status)
	require.Equal(t, service.NativeVideoMonitorReview, detail.MonitorStatus)
	require.Equal(t, "native_monitor_connection_changed", detail.MonitorErrorCode)
	require.Equal(t, "native_monitor_review", detail.Events[len(detail.Events)-1].Type)
	_, total, err := logs.ListTaskLogs(ctx, service.TaskLogFilter{UserID: user, ReviewOnly: true, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	_, err = polls.ClaimNativeVideoPoll(ctx, "must-not-retry-review", time.Minute)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)

	// An explicit authenticated native poll may still finish a paused monitor.
	completed := o
	completed.Create, completed.PendingBilling, completed.PollConnectionHash = false, nil, ""
	completed.Status, completed.DeliveryStatus, completed.ObservedAt = service.GenerationSucceeded, service.GenerationDeliveryReady, now.Add(30*time.Second)
	require.NoError(t, history.ObserveNativeVideo(ctx, completed))
	detail, err = logs.GetTaskLog(ctx, id, user)
	require.NoError(t, err)
	require.Equal(t, service.GenerationSucceeded, detail.Status)
	require.Equal(t, service.NativeVideoMonitorComplete, detail.MonitorStatus)
	require.Empty(t, detail.MonitorErrorCode)
	require.Equal(t, "unconfirmed", detail.BillingStatus, "background completion is not evidence of settlement")
	require.Nil(t, detail.ActualAmount)
	// No reservation, usage, quota or financial dedup row is written by polling.
	var balance, frozen, quota float64
	require.NoError(t, db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, user).Scan(&balance, &frozen))
	require.Equal(t, 100.0, balance)
	require.Zero(t, frozen)
	require.NoError(t, db.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, key).Scan(&quota))
	require.Zero(t, quota)
	for _, table := range []string{"usage_logs", "usage_billing_dedup", "sub2api_generation_tasks"} {
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&count))
		require.Zero(t, count)
	}
	// Only newly recorded creations with an explicit fingerprint are enrolled.
	old := o
	old.UpstreamID, old.PollConnectionHash = "old-task", ""
	require.NoError(t, history.ObserveNativeVideo(ctx, old))
	old.PollConnectionHash = strings.Repeat("c", 64)
	require.NoError(t, history.ObserveNativeVideo(ctx, old))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sub2api_native_video_polls`).Scan(&count))
	require.Equal(t, 1, count)
	_, err = db.Exec(`DELETE FROM schema_migrations WHERE filename='261_native_video_polling.sql'`)
	require.NoError(t, err)
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sub2api_native_video_polls`).Scan(&count))
	require.Equal(t, 1, count)
	// Real foreground/background races keep terminal history and monitoring
	// consistent regardless of which transaction wins first.
	for i := range 4 {
		race := o
		race.UpstreamID = "racing-" + string(rune('a'+i))
		require.NoError(t, history.ObserveNativeVideo(ctx, race))
		leased, err := polls.ClaimNativeVideoPoll(ctx, "racing", time.Minute)
		require.NoError(t, err)
		race.Create, race.PollConnectionHash = false, ""
		race.Status, race.DeliveryStatus, race.ObservedAt = service.GenerationSucceeded, service.GenerationDeliveryReady, now.Add(60*time.Second)
		results := make(chan error, 2)
		start := make(chan struct{})
		raceCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		go func() { <-start; results <- history.ObserveNativeVideo(raceCtx, race) }()
		go func() {
			<-start
			results <- polls.SaveNativeVideoPoll(raceCtx, leased, service.NativeVideoPollUpdate{ObservedAt: now.Add(50 * time.Second), State: &service.NativeVideoState{Status: service.GenerationRunning, DeliveryStatus: service.GenerationDeliveryPending}, MonitorStatus: service.NativeVideoMonitorActive, Delay: time.Second})
		}()
		close(start)
		for range 2 {
			err := <-results
			require.True(t, err == nil || errors.Is(err, service.ErrGenerationLeaseLost), "unexpected race error: %v", err)
		}
		cancel()
		detail, err := logs.GetTaskLog(ctx, service.NativeVideoTaskID(race), user)
		require.NoError(t, err)
		require.Equal(t, service.GenerationSucceeded, detail.Status)
		require.Equal(t, service.GenerationDeliveryReady, detail.DeliveryStatus)
		require.Equal(t, service.NativeVideoMonitorComplete, detail.MonitorStatus)
	}
}

func TestNativeVideoPollLocalPostgresAtomicEnrolment(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var user, key, group, account int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash) VALUES('poll-atomic@example.invalid','fixture') RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('poll-atomic','grok') RETURNING id`).Scan(&group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'poll-atomic','fixture',$2) RETURNING id`, user, group).Scan(&key))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('poll-atomic','grok','apikey','{}',1,1) RETURNING id`).Scan(&account))
	_, err := db.Exec(`CREATE FUNCTION reject_native_poll() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture enqueue failed'; END; $$;
 CREATE TRIGGER reject_native_poll BEFORE INSERT ON sub2api_native_video_polls FOR EACH ROW EXECUTE FUNCTION reject_native_poll()`)
	require.NoError(t, err)
	now := time.Now().UTC()
	o := service.NativeVideoObservation{UserID: user, APIKeyID: key, GroupID: group, AccountID: account, UpstreamID: "atomic-task", Model: "public", Platform: service.PlatformGrok, Protocol: service.GenerationProtocolXAI, Action: "text_to_video", Status: service.GenerationQueued, DeliveryStatus: service.GenerationDeliveryPending, CreatedAt: now, ObservedAt: now, Create: true, PollConnectionHash: strings.Repeat("a", 64)}
	history := NewGenerationTaskRepository(db).(service.NativeVideoTaskRepository)
	require.Error(t, history.ObserveNativeVideo(ctx, o))
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sub2api_native_video_tasks`).Scan(&count))
	require.Zero(t, count, "history and monitor must commit together")
}

type nativeVideoPollAccounts struct {
	service.AccountRepository
	account *service.Account
}

func (r *nativeVideoPollAccounts) GetByID(context.Context, int64) (*service.Account, error) {
	return r.account, nil
}

type nativeVideoPollHTTP struct {
	service.HTTPUpstream
	client *http.Client
}

func (u *nativeVideoPollHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(req)
}

type nativeVideoPollSlots struct {
	service.ConcurrencyCache
	acquired, released atomic.Int32
}

func (s *nativeVideoPollSlots) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	s.acquired.Add(1)
	return true, nil
}

func (s *nativeVideoPollSlots) ReleaseAccountSlot(context.Context, int64, string) error {
	s.released.Add(1)
	return nil
}

func TestNativeVideoPollLocalPostgresWorkerWithoutClient(t *testing.T) {
	for _, protocol := range []string{service.GenerationProtocolXAI, service.GenerationProtocolArk} {
		t.Run(protocol, func(t *testing.T) {
			db := generationLocalDB(t)
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.Method != http.MethodGet || req.Header.Get("Authorization") != "Bearer fixture-key" {
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if protocol == service.GenerationProtocolArk && req.URL.Path == "/api/v3/contents/generations/tasks/worker-task" {
					_, _ = w.Write([]byte(`{"id":"worker-task","status":"succeeded","content":{"video_url":"https://media.example/private?signature=secret"}}`))
				} else if protocol == service.GenerationProtocolXAI && req.URL.Path == "/v1/videos/worker-task" {
					_, _ = w.Write([]byte(`{"request_id":"worker-task","status":"done","video":{"url":"https://media.example/private?signature=secret"}}`))
				} else {
					http.Error(w, "unexpected path", http.StatusBadRequest)
				}
			}))
			defer upstream.Close()
			var user, key, group, accountID int64
			require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES('poll-worker@example.invalid','fixture',100) RETURNING id`).Scan(&user))
			require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('poll-worker','grok') RETURNING id`).Scan(&group))
			require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'poll-worker','fixture',$2) RETURNING id`, user, group).Scan(&key))
			require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('poll-worker','grok','apikey','{}',1,1) RETURNING id`).Scan(&accountID))
			account := &service.Account{ID: accountID, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Concurrency: 2, Credentials: map[string]any{"base_url": upstream.URL, "api_key": "fixture-key"}}
			upstreamID := "worker-task"
			if protocol == service.GenerationProtocolArk {
				account.Platform = service.PlatformOpenAI
				account.Credentials["base_url"] = upstream.URL + "/api/v3"
				account.Credentials["openai_capabilities"] = []string{"seedance"}
				upstreamID = "seedance:" + upstreamID
			}
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			slots := &nativeVideoPollSlots{}
			accounts := &nativeVideoPollAccounts{account: account}
			gateway := service.NewOpenAIGatewayService(accounts, nil, nil, nil, nil, nil, nil, cfg, nil, service.NewConcurrencyService(slots), nil, nil, nil, &nativeVideoPollHTTP{client: upstream.Client()}, nil, nil, nil, nil, nil, nil, nil, nil)
			hash, err := gateway.NativeVideoPollConnectionHash(account, protocol, upstreamID)
			require.NoError(t, err)
			repo := NewGenerationTaskRepository(db)
			now := time.Now().UTC().Add(-time.Minute)
			o := service.NativeVideoObservation{UserID: user, APIKeyID: key, GroupID: group, AccountID: accountID, UpstreamID: upstreamID, Model: "public-model", Platform: account.Platform, Protocol: protocol, Action: "text_to_video", Status: service.GenerationQueued, DeliveryStatus: service.GenerationDeliveryPending, CreatedAt: now, ObservedAt: now, Create: true, PollConnectionHash: hash}
			require.NoError(t, repo.(service.NativeVideoTaskRepository).ObserveNativeVideo(context.Background(), o))
			worker := service.NewGenerationService(repo, nil, accounts, gateway, nil, nil)
			t.Cleanup(worker.Stop)
			id := service.NativeVideoTaskID(o)
			require.Eventually(t, func() bool {
				var status, monitor string
				err := db.QueryRow(`SELECT t.status,p.monitor_status FROM sub2api_native_video_tasks t JOIN sub2api_native_video_polls p ON p.task_id=t.id WHERE t.id=$1`, id).Scan(&status, &monitor)
				return err == nil && status == service.GenerationSucceeded && monitor == service.NativeVideoMonitorComplete
			}, 5*time.Second, 20*time.Millisecond, "no client query is required to persist completion")
			worker.Stop()
			require.EqualValues(t, 1, calls.Load(), "the monitor must only query status once, without generating or downloading")
			require.Equal(t, slots.acquired.Load(), slots.released.Load())
			detail, err := repo.(service.TaskLogRepository).GetTaskLog(context.Background(), id, user)
			require.NoError(t, err)
			require.Equal(t, service.GenerationDeliveryReady, detail.DeliveryStatus)
			require.Equal(t, "unconfirmed", detail.BillingStatus)
			require.Nil(t, detail.ActualAmount)
			var balance float64
			require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, user).Scan(&balance))
			require.Equal(t, 100.0, balance)
			var usages int
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM usage_logs`).Scan(&usages))
			require.Zero(t, usages)
			var leaked sql.NullString
			require.NoError(t, db.QueryRow(`SELECT public_fields::text FROM sub2api_native_video_tasks WHERE id=$1`, id).Scan(&leaked))
			require.NotContains(t, leaked.String, "signature")
		})
	}
}
