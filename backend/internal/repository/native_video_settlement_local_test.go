package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type nativeSettlementSQLFixture struct {
	db      *sql.DB
	repo    service.GenerationTaskRepository
	gateway *service.OpenAIGatewayService
	key     *service.APIKey
	account *service.Account
	o       service.NativeVideoObservation
}

func newNativeSettlementSQLFixture(t *testing.T, protocol, mode string, subscription bool) *nativeSettlementSQLFixture {
	t.Helper()
	db := generationLocalDB(t)
	var user, keyID, groupID, accountID int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES('settlement@example.invalid','fixture',100) RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('settlement','grok') RETURNING id`).Scan(&groupID))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id,quota,rate_limit_5h) VALUES($1,'settlement','fixture',$2,100,100) RETURNING id`, user, groupID).Scan(&keyID))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,extra,concurrency,priority) VALUES('settlement','grok','apikey','{}','{"quota_limit":100}',1,1) RETURNING id`).Scan(&accountID))
	price, accountRate := .037, .5
	key := &service.APIKey{ID: keyID, UserID: user, User: &service.User{ID: user}, GroupID: &groupID, Quota: 100, RateLimit5h: 100,
		Group: &service.Group{ID: groupID, Platform: service.PlatformGrok, RateMultiplier: 2, ImageRateMultiplier: 1, VideoRateMultiplier: 1, VideoPrice720P: &price}}
	account := &service.Account{ID: accountID, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive,
		Concurrency: 2, RateMultiplier: &accountRate, Extra: map[string]any{"quota_limit": float64(100)}}
	cfg := &config.Config{}
	if mode == "log_only" || mode == "rate_only" {
		cfg.RunMode, cfg.SimpleModeKeyRateLimitEnabled = config.RunModeSimple, mode == "rate_only"
	}
	if mode == "free" {
		price = 0
	}
	if protocol == service.GenerationProtocolArk {
		output := .00001
		key.Group.Platform, account.Platform = service.PlatformOpenAI, service.PlatformOpenAI
		key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"video-model"}, BillingMode: service.BillingModeToken, OutputPrice: &output}}
	}
	bs := service.NewBillingService(cfg, nil)
	billing := service.NewUsageBillingLaneCombiner(NewUsageBillingRepository(nil, db))
	t.Cleanup(billing.Stop)
	gateway := service.NewOpenAIGatewayService(nil, newUsageLogRepositoryWithSQL(nil, nativePriceSQL{db}), billing, nil, nil, nil, nil, cfg, nil, nil, bs, nil, &service.BillingCacheService{}, nil, &service.DeferredService{}, nil, nil, service.NewModelPricingResolver(nil, bs), nil, nil, nil, nil)
	var sub *service.UserSubscription
	if subscription {
		key.Group.SubscriptionType = service.SubscriptionTypeSubscription
		var id int64
		require.NoError(t, db.QueryRow(`INSERT INTO user_subscriptions(user_id,group_id,starts_at,expires_at) VALUES($1,$2,NOW(),NOW()+INTERVAL '1 day') RETURNING id`, user, groupID).Scan(&id))
		sub = &service.UserSubscription{ID: id, UserID: user, GroupID: groupID}
	}
	now := time.Now().UTC().Add(-time.Minute)
	p, err := gateway.CaptureNativeVideoBilling(context.Background(), key, account, sub, protocol, "video-model", "public-model", "720p", account.Platform, now)
	require.NoError(t, err)
	p.TaskID = "settlement-task"
	if protocol == service.GenerationProtocolArk {
		p.TaskID = "seedance:" + p.TaskID
	}
	pending := &service.GrokVideoPendingBilling{Model: p.Model, VideoDurationSeconds: 8, VideoResolution: p.Resolution, Financial: p, CreatedAt: now.Format(time.RFC3339Nano)}
	o := service.NativeVideoObservation{UserID: user, APIKeyID: keyID, GroupID: groupID, AccountID: accountID,
		Model: "public-model", Platform: account.Platform, Protocol: protocol, UpstreamID: p.TaskID, Action: "create", Status: service.GenerationQueued,
		DeliveryStatus: service.GenerationDeliveryPending, Create: true, CreatedAt: now, ObservedAt: now, PendingBilling: pending, PollConnectionHash: strings.Repeat("a", 64)}
	return &nativeSettlementSQLFixture{db: db, repo: NewGenerationTaskRepository(db), gateway: gateway, key: key, account: account, o: o}
}

func (f *nativeSettlementSQLFixture) create(t *testing.T) {
	t.Helper()
	require.NoError(t, f.repo.(service.NativeVideoTaskRepository).ObserveNativeVideo(context.Background(), f.o))
}

func (f *nativeSettlementSQLFixture) final(units int) *service.NativeVideoFinalUsage {
	u := &service.NativeVideoFinalUsage{ObservedAt: time.Now().UTC()}
	if f.o.Protocol == service.GenerationProtocolArk {
		u.OutputTokens = units
	} else {
		u.VideoCount, u.DurationSeconds = 1, units
	}
	return u
}

func (f *nativeSettlementSQLFixture) bill(units int) error {
	p := f.o.PendingBilling.Financial
	return f.gateway.RecordUsage(context.Background(), &service.OpenAIRecordUsageInput{Result: f.final(units).Result(p, f.o.CreatedAt), User: f.key.User, APIKey: f.key, Account: f.account})
}

func (f *nativeSettlementSQLFixture) observe(t *testing.T, usage *service.NativeVideoFinalUsage, status string) {
	t.Helper()
	o := f.o
	o.Create, o.PendingBilling, o.PollConnectionHash = false, nil, ""
	o.Status, o.DeliveryStatus, o.ObservedAt, o.FinalUsage = status, service.GenerationDeliveryReady, time.Now().UTC(), usage
	if status == service.GenerationFailed {
		o.DeliveryStatus = "failed"
	}
	require.NoError(t, f.repo.(service.NativeVideoTaskRepository).ObserveNativeVideo(context.Background(), o))
}

func TestNativeVideoSettlementLocalPostgresAtomicEnrolment(t *testing.T) {
	f := newNativeSettlementSQLFixture(t, service.GenerationProtocolXAI, "standard", false)
	_, err := f.db.Exec(`CREATE FUNCTION reject_native_settlement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture enqueue failed'; END; $$;
 CREATE TRIGGER reject_native_settlement BEFORE INSERT ON sub2api_native_video_settlements FOR EACH ROW EXECUTE FUNCTION reject_native_settlement()`)
	require.NoError(t, err)
	require.Error(t, f.repo.(service.NativeVideoTaskRepository).ObserveNativeVideo(context.Background(), f.o))
	for _, table := range []string{"sub2api_native_video_tasks", "sub2api_native_video_polls", "sub2api_native_video_settlements"} {
		var count int
		require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&count))
		require.Zero(t, count, "history, polling and settlement commit together")
	}
	_, err = f.db.Exec(`DROP TRIGGER reject_native_settlement ON sub2api_native_video_settlements`)
	require.NoError(t, err)
	f.create(t)
	f.create(t)
	var count int
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM sub2api_native_video_settlements`).Scan(&count))
	require.Equal(t, 1, count)
	old := f.o
	old.UpstreamID, old.PendingBilling, old.PollConnectionHash = "old-unpriced-task", nil, ""
	require.NoError(t, f.repo.(service.NativeVideoTaskRepository).ObserveNativeVideo(context.Background(), old))
	_, err = f.db.Exec(`DELETE FROM schema_migrations WHERE filename='262_native_video_settlement.sql'`)
	require.NoError(t, err)
	require.NoError(t, ApplyMigrations(context.Background(), f.db))
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM sub2api_native_video_settlements`).Scan(&count))
	require.Equal(t, 1, count, "migration never enrolls older jobs")
	for _, failed := range []bool{false, true} {
		terminal := f.o
		terminal.UpstreamID = map[bool]string{false: "inline-success", true: "inline-failure"}[failed]
		pending, financial := *terminal.PendingBilling, *terminal.PendingBilling.Financial
		financial.TaskID = terminal.UpstreamID
		pending.Financial, terminal.PendingBilling = &financial, &pending
		terminal.Status, terminal.DeliveryStatus, terminal.FinalUsage = service.GenerationSucceeded, service.GenerationDeliveryReady, f.final(8)
		want := "ready"
		if failed {
			terminal.Status, terminal.DeliveryStatus, terminal.FinalUsage, want = service.GenerationFailed, "failed", nil, "ignored"
		}
		require.NoError(t, f.repo.(service.NativeVideoTaskRepository).ObserveNativeVideo(context.Background(), terminal))
		var status string
		require.NoError(t, f.db.QueryRow(`SELECT status FROM sub2api_native_video_settlements WHERE task_id=$1`, service.NativeVideoTaskID(terminal)).Scan(&status))
		require.Equal(t, want, status, "synchronous terminal submissions retain financial evidence")
	}
}

func TestNativeVideoSettlementLocalPostgresCanonicalUnitsAndLogRepair(t *testing.T) {
	for _, protocol := range []string{service.GenerationProtocolXAI, service.GenerationProtocolArk} {
		t.Run(protocol, func(t *testing.T) {
			f := newNativeSettlementSQLFixture(t, protocol, "standard", false)
			f.create(t)
			first, changed, want := 8, 12, .592
			if protocol == service.GenerationProtocolArk {
				first, changed, want = 100_000, 200_000, 2
			}
			f.observe(t, f.final(first), service.GenerationSucceeded)
			f.observe(t, f.final(changed), service.GenerationSucceeded)
			var wg sync.WaitGroup
			errs := make(chan error, 8)
			for range 8 {
				wg.Add(1)
				go func() { defer wg.Done(); errs <- f.bill(changed) }()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err, "foreground and background must use the first durable final units")
			}
			var balance, cost float64
			require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.o.UserID).Scan(&balance))
			require.InDelta(t, 100-want, balance, 1e-12)
			require.NoError(t, f.db.QueryRow(`SELECT actual_cost FROM usage_logs`).Scan(&cost))
			require.InDelta(t, want, cost, 1e-12)
			// Repair an old lost log and a zero-cost placeholder using the already
			// claimed (then archived) original ledger, without another deduction.
			_, err := f.db.Exec(`DELETE FROM usage_logs`)
			require.NoError(t, err)
			require.NoError(t, f.bill(first))
			_, err = f.db.Exec(`INSERT INTO usage_billing_dedup_archive(request_id,api_key_id,request_fingerprint,created_at)
 SELECT request_id,api_key_id,request_fingerprint,created_at FROM usage_billing_dedup;
 DELETE FROM usage_billing_dedup; UPDATE usage_logs SET actual_cost=0,output_cost=0`)
			require.NoError(t, err)
			require.NoError(t, f.bill(first))
			require.NoError(t, f.db.QueryRow(`SELECT actual_cost FROM usage_logs`).Scan(&cost))
			require.InDelta(t, want, cost, 1e-12)
			var after float64
			require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.o.UserID).Scan(&after))
			require.Equal(t, balance, after)
			changedPrice := *f.o.PendingBilling.Financial
			changedPrice.UnitActual = decimal.NewFromInt(2)
			_, err = NewUsageBillingRepository(nil, f.db).(service.NativeVideoFinalUsageRepository).ResolveNativeVideoFinalUsage(context.Background(), &changedPrice, f.final(first))
			require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict, "durable price cannot be replaced by mutable input")
		})
	}
}

func TestNativeVideoSettlementLocalPostgresLogFailureRollsBackMoney(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		t.Run(map[bool]string{false: "balance", true: "subscription"}[subscription], func(t *testing.T) {
			f := newNativeSettlementSQLFixture(t, service.GenerationProtocolXAI, "standard", subscription)
			f.create(t)
			_, err := f.db.Exec(`CREATE FUNCTION reject_native_usage() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture log failed'; END; $$;
 CREATE TRIGGER reject_native_usage BEFORE INSERT OR UPDATE ON usage_logs FOR EACH ROW EXECUTE FUNCTION reject_native_usage()`)
			require.NoError(t, err)
			require.Error(t, f.bill(8))
			var balance, quota, window, accountQuota float64
			require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.o.UserID).Scan(&balance))
			require.Equal(t, 100.0, balance)
			require.NoError(t, f.db.QueryRow(`SELECT quota_used,usage_5h FROM api_keys WHERE id=$1`, f.o.APIKeyID).Scan(&quota, &window))
			require.Zero(t, quota)
			require.Zero(t, window)
			require.NoError(t, f.db.QueryRow(`SELECT COALESCE((extra->>'quota_used')::numeric,0) FROM accounts WHERE id=$1`, f.o.AccountID).Scan(&accountQuota))
			require.Zero(t, accountQuota)
			if subscription {
				var used float64
				require.NoError(t, f.db.QueryRow(`SELECT daily_usage_usd FROM user_subscriptions`).Scan(&used))
				require.Zero(t, used)
			}
			for _, table := range []string{"usage_logs", "usage_billing_dedup"} {
				var count int
				require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&count))
				require.Zero(t, count)
			}
			_, err = f.db.Exec(`DROP TRIGGER reject_native_usage ON usage_logs`)
			require.NoError(t, err)
			require.NoError(t, f.bill(8))
		})
	}
}

func TestNativeVideoSettlementLocalPostgresLeaseRecoveryAndSimpleModes(t *testing.T) {
	for _, mode := range []string{"standard", "log_only", "rate_only", "free"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeSettlementSQLFixture(t, service.GenerationProtocolXAI, mode, false)
			f.create(t)
			f.observe(t, f.final(8), service.GenerationSucceeded)
			repo := f.repo.(service.NativeVideoSettlementRepository)
			leased, err := repo.ClaimNativeVideoSettlement(context.Background(), "crashed-process", time.Minute)
			require.NoError(t, err)
			_, err = repo.ClaimNativeVideoSettlement(context.Background(), "second", time.Minute)
			require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
			// Money/log commit succeeds but the worker crashes before marking
			// its queue item complete. A new process safely replays that item.
			require.NoError(t, f.bill(8))
			_, err = f.db.Exec(`UPDATE sub2api_native_video_settlements SET lease_until=NOW()-INTERVAL '1 second'`)
			require.NoError(t, err)
			require.ErrorIs(t, repo.SaveNativeVideoSettlement(context.Background(), leased, "settled", "", 0), service.ErrGenerationLeaseLost)
			worker := service.NewGenerationService(NewGenerationTaskRepository(f.db), nil, nil, f.gateway, nil, nil)
			t.Cleanup(worker.Stop)
			status := "settled"
			if mode == "log_only" || mode == "rate_only" {
				status = "logged"
			}
			require.Eventually(t, func() bool {
				var current string
				return f.db.QueryRow(`SELECT status FROM sub2api_native_video_settlements`).Scan(&current) == nil && current == status
			}, 5*time.Second, 20*time.Millisecond)
			worker.Stop()
			var balance, quota, window float64
			var usages, dedups int
			require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.o.UserID).Scan(&balance))
			require.NoError(t, f.db.QueryRow(`SELECT quota_used,usage_5h FROM api_keys WHERE id=$1`, f.o.APIKeyID).Scan(&quota, &window))
			require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM usage_logs`).Scan(&usages))
			require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM usage_billing_dedup`).Scan(&dedups))
			require.Equal(t, 1, usages)
			if mode == "standard" {
				require.InDelta(t, 99.408, balance, 1e-12)
				require.InDelta(t, .592, quota, 1e-12)
			} else {
				require.Equal(t, 100.0, balance)
				require.Zero(t, quota)
			}
			if mode == "rate_only" || mode == "standard" {
				require.InDelta(t, .592, window, 1e-12)
			} else {
				require.Zero(t, window)
			}
			if mode == "log_only" {
				require.Zero(t, dedups, "simple log-only mode must not claim a financial charge")
			} else {
				require.Equal(t, 1, dedups)
			}
			detail, err := f.repo.(service.TaskLogRepository).GetTaskLog(context.Background(), service.NativeVideoTaskID(f.o), f.o.UserID)
			require.NoError(t, err)
			if status == "logged" {
				require.Equal(t, "not_charged", detail.BillingStatus)
				require.Nil(t, detail.ActualAmount)
			} else {
				require.Equal(t, "settled", detail.BillingStatus)
			}
			public, err := json.Marshal(detail)
			require.NoError(t, err)
			for _, private := range []string{"financial", "unit_actual", "final_usage", "subscription_id", "connection_hash"} {
				require.NotContains(t, string(public), private)
			}
		})
	}
}

func TestNativeVideoSettlementLocalPostgresBackgroundActualUsageWithoutClient(t *testing.T) {
	for _, protocol := range []string{service.GenerationProtocolXAI, service.GenerationProtocolArk} {
		t.Run(protocol, func(t *testing.T) {
			f := newNativeSettlementSQLFixture(t, protocol, "standard", false)
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fixture-key" {
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				body := `{"request_id":"settlement-task","status":"done","video":{"duration":8,"url":"https://media.example/private?signature=secret"}}`
				if protocol == service.GenerationProtocolArk {
					body = `{"id":"settlement-task","status":"succeeded","content":{"video_url":"https://media.example/private?signature=secret"},"usage":{"completion_tokens":100000}}`
				}
				_, _ = w.Write([]byte(body))
			}))
			defer upstream.Close()
			f.account.Credentials = map[string]any{"api_key": "fixture-key", "base_url": upstream.URL}
			if protocol == service.GenerationProtocolArk {
				f.account.Credentials["base_url"], f.account.Credentials["openai_capabilities"] = upstream.URL+"/api/v3", []string{"seedance"}
			}
			accounts, slots := &nativeVideoPollAccounts{account: f.account}, &nativeVideoPollSlots{}
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			// Same ledger, with real bounded HTTP status queries for this fixture.
			bs := service.NewBillingService(cfg, nil)
			gateway := service.NewOpenAIGatewayService(accounts, newUsageLogRepositoryWithSQL(nil, nativePriceSQL{f.db}), NewUsageBillingRepository(nil, f.db), nil, nil, nil, nil, cfg, nil, service.NewConcurrencyService(slots), bs, nil, &service.BillingCacheService{}, &nativeVideoPollHTTP{client: upstream.Client()}, &service.DeferredService{}, nil, nil, service.NewModelPricingResolver(nil, bs), nil, nil, nil, nil)
			hash, err := gateway.NativeVideoPollConnectionHash(f.account, protocol, f.o.UpstreamID)
			require.NoError(t, err)
			f.o.PollConnectionHash = hash
			f.create(t)
			worker := service.NewGenerationService(f.repo, nil, accounts, gateway, nil, nil)
			t.Cleanup(worker.Stop)
			require.Eventually(t, func() bool {
				var status string
				return f.db.QueryRow(`SELECT status FROM sub2api_native_video_settlements`).Scan(&status) == nil && status == "settled"
			}, 6*time.Second, 20*time.Millisecond)
			worker.Stop()
			require.EqualValues(t, 1, calls.Load())
			require.Equal(t, slots.acquired.Load(), slots.released.Load())
			var balance, amount float64
			var logs int
			require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.o.UserID).Scan(&balance))
			want := .592
			if protocol == service.GenerationProtocolArk {
				want = 2
			}
			require.InDelta(t, 100-want, balance, 1e-12)
			require.NoError(t, f.db.QueryRow(`SELECT COUNT(*),MAX(actual_cost) FROM usage_logs`).Scan(&logs, &amount))
			require.Equal(t, 1, logs)
			require.InDelta(t, want, amount, 1e-12)
		})
	}
}

func TestNativeVideoSettlementLocalPostgresPendingUsageAndFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "delayed tokens", true: "failed generation"}[failed], func(t *testing.T) {
			f := newNativeSettlementSQLFixture(t, service.GenerationProtocolArk, "standard", false)
			f.create(t)
			polls := f.repo.(service.NativeVideoPollRepository)
			task, err := polls.ClaimNativeVideoPoll(context.Background(), "fixture", time.Minute)
			require.NoError(t, err)
			require.True(t, task.AwaitingUsage)
			status, delivery := service.GenerationSucceeded, service.GenerationDeliveryReady
			if failed {
				status, delivery = service.GenerationFailed, "failed"
			}
			require.NoError(t, polls.SaveNativeVideoPoll(context.Background(), task, service.NativeVideoPollUpdate{ObservedAt: time.Now().UTC(),
				State: &service.NativeVideoState{Status: status, DeliveryStatus: delivery}, MonitorStatus: service.NativeVideoMonitorComplete}))
			var settlement, monitor string
			require.NoError(t, f.db.QueryRow(`SELECT s.status,p.monitor_status FROM sub2api_native_video_settlements s
 JOIN sub2api_native_video_polls p ON p.task_id=s.task_id`).Scan(&settlement, &monitor))
			if failed {
				require.Equal(t, "ignored", settlement)
				require.Equal(t, service.NativeVideoMonitorComplete, monitor)
				detail, err := f.repo.(service.TaskLogRepository).GetTaskLog(context.Background(), service.NativeVideoTaskID(f.o), f.o.UserID)
				require.NoError(t, err)
				require.Equal(t, "not_charged", detail.BillingStatus)
				require.Nil(t, detail.ActualAmount)
				require.Error(t, f.bill(100_000), "late success must not invent a charge after a recorded failed generation")
				return
			}
			require.Equal(t, "awaiting_usage", settlement)
			require.Equal(t, service.NativeVideoMonitorActive, monitor)
			task, err = polls.ClaimNativeVideoPoll(context.Background(), "later-actual-usage", time.Minute)
			require.NoError(t, err)
			require.NoError(t, polls.SaveNativeVideoPoll(context.Background(), task, service.NativeVideoPollUpdate{ObservedAt: time.Now().UTC(),
				State: &service.NativeVideoState{Status: status, DeliveryStatus: delivery, FinalUsage: f.final(100_000)}, MonitorStatus: service.NativeVideoMonitorComplete}))
			detail, err := f.repo.(service.TaskLogRepository).GetTaskLog(context.Background(), service.NativeVideoTaskID(f.o), f.o.UserID)
			require.NoError(t, err)
			require.Equal(t, "pending", detail.BillingStatus)
			require.Nil(t, detail.ActualAmount)
			// Invalid financial ownership is parked visibly for review. It is
			// never used to construct a different payer in a restarted worker.
			_, err = f.db.Exec(`UPDATE sub2api_native_video_tasks SET pending_billing=jsonb_set(pending_billing,'{financial,user_id}','999999')`)
			require.NoError(t, err)
			worker := service.NewGenerationService(f.repo, nil, nil, f.gateway, nil, nil)
			t.Cleanup(worker.Stop)
			require.Eventually(t, func() bool {
				var current string
				return f.db.QueryRow(`SELECT status FROM sub2api_native_video_settlements`).Scan(&current) == nil && current == "review"
			}, 5*time.Second, 20*time.Millisecond)
			worker.Stop()
			detail, err = f.repo.(service.TaskLogRepository).GetTaskLog(context.Background(), service.NativeVideoTaskID(f.o), f.o.UserID)
			require.NoError(t, err)
			require.Equal(t, "review", detail.BillingStatus)
			require.Equal(t, service.GenerationSucceeded, detail.Status)
			require.Equal(t, "native_billing_review", detail.Events[len(detail.Events)-1].Type)
			_, total, err := f.repo.(service.TaskLogRepository).ListTaskLogs(context.Background(), service.TaskLogFilter{ReviewOnly: true, Page: 1, PageSize: 20})
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			var balance float64
			require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.o.UserID).Scan(&balance))
			require.Equal(t, 100.0, balance)
		})
	}
}

func TestNativeVideoMaintenanceLocalPostgresAuditLeasesAndResume(t *testing.T) {
	f := newNativeSettlementSQLFixture(t, service.GenerationProtocolXAI, "standard", false)
	f.account.Credentials = map[string]any{"api_key": "private-key", "base_url": "https://upstream.example/v1"}
	hash, err := f.gateway.NativeVideoPollConnectionHash(f.account, f.o.Protocol, f.o.UpstreamID)
	require.NoError(t, err)
	f.o.PollConnectionHash = hash
	f.create(t)
	svc := service.NewGenerationService(f.repo, nil, &nativeVideoPollAccounts{account: f.account}, f.gateway, nil, nil)
	svc.Stop() // exercise explicit review commands without an automatic claim
	id := service.NativeVideoTaskID(f.o)
	_, err = f.db.Exec(`UPDATE sub2api_native_video_polls SET monitor_status='review',last_error_code='native_monitor_upstream_error',failures=10`)
	require.NoError(t, err)
	require.Error(t, svc.MaintainNativeVideo(context.Background(), id, 999999, "resume_poll", "verified upstream"), "audit failure rolls back the resume")
	var monitor string
	require.NoError(t, f.db.QueryRow(`SELECT monitor_status FROM sub2api_native_video_polls`).Scan(&monitor))
	require.Equal(t, "review", monitor)
	_, err = f.db.Exec(`UPDATE sub2api_native_video_polls SET lease_token='still-active',lease_until=NOW()+INTERVAL '1 minute'`)
	require.NoError(t, err)
	require.ErrorIs(t, svc.MaintainNativeVideo(context.Background(), id, f.o.UserID, "resume_poll", "verified upstream"), service.ErrNativeVideoMaintenanceConflict)
	_, err = f.db.Exec(`UPDATE sub2api_native_video_polls SET lease_until=NOW()-INTERVAL '1 minute'`)
	require.NoError(t, err)
	// Independent concurrent operators cannot record two successful resumes.
	var wg sync.WaitGroup
	var resumed atomic.Int32
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := svc.MaintainNativeVideo(context.Background(), id, f.o.UserID, "resume_poll", "verified original upstream")
			if err == nil {
				resumed.Add(1)
			} else {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.ErrorIs(t, err, service.ErrNativeVideoMaintenanceConflict)
	}
	require.EqualValues(t, 1, resumed.Load())
	var actions int
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM sub2api_native_video_actions`).Scan(&actions))
	require.Equal(t, 1, actions)
	var amount float64
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.o.UserID).Scan(&amount))
	require.Equal(t, 100.0, amount, "resuming GETs does not create a generation or charge")
	f.observe(t, f.final(8), service.GenerationSucceeded)
	_, err = f.db.Exec(`UPDATE sub2api_native_video_settlements SET status='review',failures=10,last_error_code='native_billing_review'`)
	require.NoError(t, err)
	detail, err := svc.TaskLog(context.Background(), id, 0, true)
	require.NoError(t, err)
	require.Equal(t, []string{"retry_settlement"}, detail.NativeActions)
	public, err := svc.TaskLog(context.Background(), id, f.o.UserID, false)
	require.NoError(t, err)
	require.Empty(t, public.NativeActions)
	encoded, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "verified original upstream", "review reasons stay private")
	require.NoError(t, svc.MaintainNativeVideo(context.Background(), id, f.o.UserID, "retry_settlement", "database issue resolved"))
	worker := service.NewGenerationService(f.repo, nil, nil, f.gateway, nil, nil)
	t.Cleanup(worker.Stop)
	require.Eventually(t, func() bool {
		var status string
		return f.db.QueryRow(`SELECT status FROM sub2api_native_video_settlements`).Scan(&status) == nil && status == "settled"
	}, 5*time.Second, 20*time.Millisecond)
	worker.Stop()
	require.ErrorIs(t, svc.MaintainNativeVideo(context.Background(), id, f.o.UserID, "retry_settlement", "must not charge twice"), service.ErrNativeVideoMaintenanceConflict)
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.o.UserID).Scan(&amount))
	require.InDelta(t, 99.408, amount, 1e-12)
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM sub2api_native_video_actions`).Scan(&actions))
	require.Equal(t, 2, actions)
	detail, err = svc.TaskLog(context.Background(), id, 0, true)
	require.NoError(t, err)
	require.Empty(t, detail.NativeActions)
	var types []string
	for _, event := range detail.Events {
		types = append(types, event.Type)
	}
	require.Contains(t, types, "native_resume_poll")
	require.Contains(t, types, "native_retry_settlement")
}
