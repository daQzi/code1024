package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Force synchronous ordinary log writes in this disposable DB fixture.
type nativePriceSQL struct{ *sql.DB }

func TestNativeVideoBillingLocalPostgresFrozenFundingAndReplay(t *testing.T) {
	for _, protocol := range []string{service.GenerationProtocolXAI, service.GenerationProtocolArk} {
		for _, subscription := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/balance", true: "/subscription"}[subscription], func(t *testing.T) {
				db := generationLocalDB(t)
				ctx := context.Background()
				var user, keyID, groupID, accountID int64
				require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES('native-price@example.invalid','fixture',100) RETURNING id`).Scan(&user))
				require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('native-price','grok') RETURNING id`).Scan(&groupID))
				require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id,quota,rate_limit_5h) VALUES($1,'native-price-key','fixture',$2,100,100) RETURNING id`, user, groupID).Scan(&keyID))
				require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,extra,concurrency,priority) VALUES('native-price','grok','apikey','{}','{"quota_limit":100}',1,1) RETURNING id`).Scan(&accountID))
				price, rate := 0.037, 0.5
				key := &service.APIKey{ID: keyID, UserID: user, User: &service.User{ID: user}, GroupID: &groupID, Quota: 100, RateLimit5h: 100,
					Group: &service.Group{ID: groupID, Platform: service.PlatformGrok, RateMultiplier: 2, ImageRateMultiplier: 1, VideoRateMultiplier: 1, VideoPrice720P: &price}}
				account := &service.Account{ID: accountID, Type: service.AccountTypeAPIKey, Platform: service.PlatformGrok, RateMultiplier: &rate, Extra: map[string]any{"quota_limit": float64(100)}}
				cfg := &config.Config{}
				billingService := service.NewBillingService(cfg, nil)
				gateway := service.NewOpenAIGatewayService(nil, newUsageLogRepositoryWithSQL(nil, nativePriceSQL{db}), NewUsageBillingRepository(nil, db), nil, nil, nil, nil, cfg, nil, nil, billingService, nil, &service.BillingCacheService{}, nil, &service.DeferredService{}, nil, nil, service.NewModelPricingResolver(nil, billingService), nil, nil, nil, nil)
				var sub *service.UserSubscription
				if subscription {
					key.Group.SubscriptionType = service.SubscriptionTypeSubscription
					var id int64
					require.NoError(t, db.QueryRow(`INSERT INTO user_subscriptions(user_id,group_id,starts_at,expires_at) VALUES($1,$2,NOW(),NOW()+INTERVAL '1 day') RETURNING id`, user, groupID).Scan(&id))
					sub = &service.UserSubscription{ID: id, UserID: user, GroupID: groupID}
				}
				if protocol == service.GenerationProtocolArk {
					key.Group.Platform, account.Platform = service.PlatformOpenAI, service.PlatformOpenAI
					output := 0.00001
					key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"video-model"}, BillingMode: service.BillingModeToken, OutputPrice: &output}}
				}
				now := time.Now().UTC()
				p, err := gateway.CaptureNativeVideoBilling(ctx, key, account, sub, protocol, "video-model", "public-model", "720p", account.Platform, now)
				require.NoError(t, err)
				p.TaskID = "fixture-task"
				if protocol == service.GenerationProtocolArk {
					p.TaskID = "seedance:" + p.TaskID
				}
				pending := &service.GrokVideoPendingBilling{Model: "video-model", BillingModel: "video-model", VideoResolution: "720p", VideoDurationSeconds: 8, CreatedAt: now.Format(time.RFC3339Nano), Financial: p}
				o := service.NativeVideoObservation{UserID: user, APIKeyID: keyID, GroupID: groupID, AccountID: accountID, UpstreamID: p.TaskID, Model: "public-model", Platform: account.Platform, Protocol: protocol, Action: "create", Status: service.GenerationQueued, DeliveryStatus: service.GenerationDeliveryPending, Create: true, CreatedAt: now, ObservedAt: now, PendingBilling: pending}
				repo := NewGenerationTaskRepository(db)
				history := repo.(service.NativeVideoTaskRepository)
				require.NoError(t, history.ObserveNativeVideo(ctx, o))
				// A re-observed create cannot replace the immutable financial input.
				changed := *p
				changed.UnitActual = changed.UnitActual.Mul(changed.UnitActual.Add(changed.UnitActual))
				changedPending := *pending
				changedPending.Financial = &changed
				o.PendingBilling = &changedPending
				require.NoError(t, history.ObserveNativeVideo(ctx, o))
				loaded, err := NewGenerationTaskRepository(db).(service.NativeVideoRecoveryRepository).GetNativeVideoRecovery(ctx, service.NativeVideoLookup{UserID: user, APIKeyID: keyID, GroupID: groupID, Protocol: protocol, UpstreamID: p.TaskID})
				require.NoError(t, err)
				require.True(t, p.UnitActual.Equal(loaded.PendingBilling.Financial.UnitActual))
				p = loaded.PendingBilling.Financial // simulate all Redis pending data being lost
				price, rate, key.Group.RateMultiplier = 9, 20, 90
				key.Group.ModelPricing = nil
				key.Group.SubscriptionType = "standard"
				key.Quota, key.RateLimit5h, account.Extra = 0, 0, nil
				// Every completion caller uses its own DTO and the same frozen
				// task key; financial effects remain one transaction in the old ledger.
				bill := func() error {
					r := &service.OpenAIForwardResult{ResponseID: p.TaskID, NativeVideoBilling: p, Duration: time.Minute}
					if protocol == service.GenerationProtocolArk {
						r.Usage.OutputTokens = 100_000
					} else {
						r.VideoCount, r.VideoDurationSeconds, r.VideoResolution = 1, 8, "720p"
					}
					return gateway.RecordUsage(ctx, &service.OpenAIRecordUsageInput{Result: r, APIKey: key, User: key.User, Account: account, Subscription: nil})
				}
				var wg sync.WaitGroup
				errs := make(chan error, 8)
				for range 8 {
					wg.Add(1)
					go func() { defer wg.Done(); errs <- bill() }()
				}
				wg.Wait()
				close(errs)
				for err := range errs {
					require.NoError(t, err)
				}
				want := 0.592
				if protocol == service.GenerationProtocolArk {
					want = 2
				}
				var balance, quota, window float64
				require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, user).Scan(&balance))
				if subscription {
					require.Equal(t, 100.0, balance)
					var usage float64
					require.NoError(t, db.QueryRow(`SELECT daily_usage_usd FROM user_subscriptions WHERE id=$1`, sub.ID).Scan(&usage))
					require.InDelta(t, want, usage, 1e-12)
				} else {
					require.InDelta(t, 100-want, balance, 1e-12)
				}
				require.NoError(t, db.QueryRow(`SELECT quota_used,usage_5h FROM api_keys WHERE id=$1`, keyID).Scan(&quota, &window))
				require.InDelta(t, want, quota, 1e-12)
				require.InDelta(t, want, window, 1e-12)
				var accountQuota float64
				require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_used')::numeric FROM accounts WHERE id=$1`, accountID).Scan(&accountQuota))
				require.InDelta(t, want/4, accountQuota, 1e-12)
				var count int
				require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM usage_logs`).Scan(&count))
				require.Equal(t, 1, count)
				var amount float64
				require.NoError(t, db.QueryRow(`SELECT actual_cost FROM usage_logs`).Scan(&amount))
				require.InDelta(t, want, amount, 1e-12)
				_, err = db.Exec(`INSERT INTO usage_billing_dedup_archive(request_id,api_key_id,request_fingerprint,created_at) SELECT request_id,api_key_id,request_fingerprint,created_at FROM usage_billing_dedup`)
				require.NoError(t, err)
				_, err = db.Exec(`DELETE FROM usage_billing_dedup`)
				require.NoError(t, err)
				require.NoError(t, bill())
				var after float64
				require.NoError(t, db.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, keyID).Scan(&after))
				require.Equal(t, quota, after, "archived claims still prevent repeated payment")
				detail, err := repo.(service.TaskLogRepository).GetTaskLog(ctx, service.NativeVideoTaskID(o), user)
				require.NoError(t, err)
				require.Equal(t, "settled", detail.BillingStatus)
				public, err := json.Marshal(detail)
				require.NoError(t, err)
				require.NotContains(t, string(public), "unit_actual")
				require.NotContains(t, string(public), "financial")
			})
		}
	}
}
