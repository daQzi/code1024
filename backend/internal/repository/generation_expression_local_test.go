package repository

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestGenerationExpressionLocalPostgresSettlement(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	source, err := builtin.Source("xai")
	require.NoError(t, err)
	loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	plugins := NewNewAPIPluginRepository(db)
	v, err := plugins.Install(ctx, &service.NewAPIPluginVersion{Key: loaded.Meta.Key, Version: loaded.Meta.Version, SHA256: strings.Repeat("a", 64), Source: source, Meta: loaded.Meta, Capabilities: runner.Descriptor{Task: true}}, nil)
	require.NoError(t, err)
	require.NoError(t, plugins.Activate(ctx, v.ID, true))
	sequence := 0
	create := func(t *testing.T, multiplier string) *service.GenerationTask {
		t.Helper()
		sequence++
		name := "expression-" + strconv.Itoa(sequence)
		var user, group, key, account int64
		require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES($1,'fixture',100) RETURNING id`, name+"@example.invalid").Scan(&user))
		require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES($1,'grok') RETURNING id`, name).Scan(&group))
		require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id,quota,rate_limit_5h) VALUES($1,$2,$2,$3,50,50) RETURNING id`, user, name, group).Scan(&key))
		require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,extra) VALUES($1,'grok','apikey','{}','{"quota_limit":100,"quota_daily_limit":100,"quota_weekly_limit":100}') RETURNING id`, name).Scan(&account))
		cap := decimal.NewFromInt(10)
		price, err := service.NewGenerationExpressionSnapshot(service.GenerationPriceRule{Mode: "expression", Expression: `tier("video", u("seconds") * (u("resolution") == "1080p" ? 0.4 : 0.2))`, Maximum: &cap}, map[string]jsplugin.UsageFieldSchema{"seconds": {Type: "number", Unit: "second"}, "resolution": {Enum: []string{"720p", "1080p"}}}, json.RawMessage(`{"seconds":8,"resolution":"720p"}`), decimal.RequireFromString(multiplier))
		require.NoError(t, err)
		id, err := service.NewGenerationTaskID()
		require.NoError(t, err)
		task := &service.GenerationTask{ID: id, UserID: user, APIKeyID: key, GroupID: group, AccountID: account, PluginVersionID: v.ID, IdempotencyKey: name, Fingerprint: strings.Repeat("b", 64), Model: "grok-imagine-video", UpstreamModel: "grok-imagine-video", Platform: "grok", Protocol: "openai_video", Action: "text_to_video", Request: json.RawMessage(`{"prompt":"fixture"}`), Price: price, Connection: service.GenerationConnection{BaseURL: "https://fixture.example.invalid", Kind: "vendor"}}
		amount := price.Hold.InexactFloat64()
		held, err := NewGenerationTaskRepository(db).CreateHeld(ctx, task, &service.UsageBillingCommand{UserID: user, APIKeyID: key, AccountID: account, AccountType: service.AccountTypeAPIKey, BalanceCost: amount, APIKeyQuotaCost: amount, APIKeyRateLimitCost: amount, AccountQuotaCost: 20, AccountQuotaHasDailyLimit: true, AccountQuotaHasWeeklyLimit: true})
		require.NoError(t, err)
		return held
	}
	complete := func(t *testing.T, task *service.GenerationTask, status, usage string) *service.GenerationTask {
		t.Helper()
		_, err := db.Exec(`UPDATE sub2api_generation_tasks SET status=$2,billing_status='pending',usage=$3::jsonb,finished_at=NOW() WHERE id=$1`, task.ID, status, usage)
		require.NoError(t, err)
		task, err = NewGenerationTaskRepository(db).Claim(ctx, "expression-lease", time.Minute)
		require.NoError(t, err)
		return task
	}
	check := func(t *testing.T, task *service.GenerationTask, balance, keyUsage, accountUsage string) {
		t.Helper()
		var actualBalance, frozen, actualKey, actualAccount decimal.Decimal
		require.NoError(t, db.QueryRow(`SELECT u.balance,u.frozen_balance,k.quota_used,(a.extra->>'quota_used')::numeric FROM users u,api_keys k,accounts a WHERE u.id=$1 AND k.id=$2 AND a.id=$3`, task.UserID, task.APIKeyID, task.AccountID).Scan(&actualBalance, &frozen, &actualKey, &actualAccount))
		require.Equal(t, balance, actualBalance.String())
		require.True(t, frozen.IsZero())
		require.Equal(t, keyUsage, actualKey.String())
		require.Equal(t, accountUsage, actualAccount.String())
		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE request_id=$1`, task.ID).Scan(&count))
		require.Equal(t, 1, count)
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sub2api_generation_events WHERE task_id=$1 AND event_key='settled'`, task.ID).Scan(&count))
		require.Equal(t, 1, count)
	}
	t.Run("persistent facts win and duplicate settlement is harmless", func(t *testing.T) {
		task := complete(t, create(t, "1.5"), service.GenerationSucceeded, `{"seconds":8,"_host_final_usage":{"seconds":4}}`)
		// Neither a mutable in-memory price nor usage can authorize a different
		// debit from the one recomputed under the database row lock.
		task.Price.Rule.Expression = "0"
		task.Usage = json.RawMessage(`{"_host_final_usage":{"seconds":0}}`)
		repo := NewGenerationTaskRepository(db)
		require.ErrorIs(t, repo.Settle(ctx, task, decimal.Zero, nil), service.ErrUsageBillingRequestConflict)
		actual := decimal.RequireFromString("1.2")
		require.NoError(t, repo.Settle(ctx, task, actual, nil))
		require.NoError(t, repo.Settle(ctx, task, actual, nil))
		require.ErrorIs(t, repo.Settle(ctx, task, decimal.NewFromInt(1), nil), service.ErrUsageBillingRequestConflict)
		check(t, task, "98.8", "1.2", "1.6")
		var event string
		require.NoError(t, db.QueryRow(`SELECT payload::text FROM sub2api_generation_events WHERE task_id=$1 AND event_key='settled'`, task.ID).Scan(&event))
		require.Contains(t, event, `"tier": "video"`)
		require.NotContains(t, event, `"facts"`)
	})
	t.Run("zero group multiplier still releases account ceiling", func(t *testing.T) {
		task := complete(t, create(t, "0"), service.GenerationSucceeded, `{"_host_final_usage":{"seconds":4}}`)
		require.NoError(t, NewGenerationTaskRepository(db).Settle(ctx, task, decimal.Zero, nil))
		check(t, task, "100", "0", "1.6")
	})
	t.Run("failure returns both reservations without final usage", func(t *testing.T) {
		task := complete(t, create(t, "1"), service.GenerationFailed, `{}`)
		repo := NewGenerationTaskRepository(db)
		require.ErrorIs(t, repo.Settle(ctx, task, decimal.NewFromInt(1), nil), service.ErrUsageBillingRequestConflict)
		require.NoError(t, repo.Settle(ctx, task, decimal.Zero, nil))
		check(t, task, "100", "0", "0")
	})
	t.Run("explicit final zero is billable evidence", func(t *testing.T) {
		task := complete(t, create(t, "1"), service.GenerationSucceeded, `{"seconds":8,"_host_final_usage":{"seconds":0}}`)
		require.NoError(t, NewGenerationTaskRepository(db).Settle(ctx, task, decimal.Zero, nil))
		check(t, task, "100", "0", "0")
	})
	t.Run("missing numeric usage needs reconciliation", func(t *testing.T) {
		task := complete(t, create(t, "1"), service.GenerationSucceeded, `{"seconds":8,"_host_final_usage":{}}`)
		repo := NewGenerationTaskRepository(db)
		require.ErrorIs(t, repo.Settle(ctx, task, decimal.NewFromInt(1), nil), service.ErrGenerationCostPending)
		_, err := db.Exec(`UPDATE sub2api_generation_tasks SET billing_status='review',lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, task.ID)
		require.NoError(t, err)
		confirmed := decimal.RequireFromString("0.5")
		require.NoError(t, repo.Reconcile(ctx, task.ID, task.UserID, "settle", "", "confirmed upstream bill", &confirmed))
		task, err = repo.Claim(ctx, "reconciled-expression-lease", time.Minute)
		require.NoError(t, err)
		require.ErrorIs(t, repo.Settle(ctx, task, decimal.NewFromInt(1), nil), service.ErrUsageBillingRequestConflict)
		require.NoError(t, repo.Settle(ctx, task, confirmed, nil))
		check(t, task, "99.5", "0.5", "1")
	})
	t.Run("beyond ceiling rolls back every financial effect", func(t *testing.T) {
		task := complete(t, create(t, "1"), service.GenerationSucceeded, `{"_host_final_usage":{"seconds":60}}`)
		repo := NewGenerationTaskRepository(db)
		require.ErrorIs(t, repo.Settle(ctx, task, decimal.NewFromInt(10), nil), service.ErrGenerationPriceExceedsHold)
		var balance, frozen, keyUsed, accountUsed decimal.Decimal
		require.NoError(t, db.QueryRow(`SELECT u.balance,u.frozen_balance,k.quota_used,(a.extra->>'quota_used')::numeric FROM users u,api_keys k,accounts a WHERE u.id=$1 AND k.id=$2 AND a.id=$3`, task.UserID, task.APIKeyID, task.AccountID).Scan(&balance, &frozen, &keyUsed, &accountUsed))
		require.Equal(t, "90", balance.String())
		require.Equal(t, "10", frozen.String())
		require.Equal(t, "10", keyUsed.String())
		require.Equal(t, "20", accountUsed.String())
	})
}

func TestGenerationQuoteComparisonAndAdminBillingLocalPostgres(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	source, err := builtin.Source("xai")
	require.NoError(t, err)
	loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	plugins := NewNewAPIPluginRepository(db)
	v, err := plugins.Install(ctx, &service.NewAPIPluginVersion{Key: loaded.Meta.Key, Version: loaded.Meta.Version, SHA256: strings.Repeat("c", 64), Source: source, Meta: loaded.Meta, Capabilities: runner.Descriptor{Task: true}}, nil)
	require.NoError(t, err)
	require.NoError(t, plugins.Activate(ctx, v.ID, true))

	var user, group, key, account int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES('quote-admin@example.invalid','fixture',100) RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('quote-admin-group','grok') RETURNING id`).Scan(&group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id,quota,rate_limit_5h) VALUES($1,'quote-admin-key','quote-admin-key',$2,50,50) RETURNING id`, user, group).Scan(&key))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,extra) VALUES('quote-admin-acc','grok','apikey','{}','{"quota_limit":100}') RETURNING id`).Scan(&account))

	repo := NewGenerationTaskRepository(db)
	taskLogs := repo.(service.TaskLogRepository)

	// 1) Expression with request probes persists request_rules and surfaces them only to admin.
	cap := decimal.NewFromInt(20)
	exprRule := service.GenerationPriceRule{
		Mode:       "expression",
		Expression: `tier("pro-tier", u("seconds") * (param("quality") == "pro" ? 1.5 : 1) * (header("X-Tier") == "vip" ? 0.5 : 1))`,
		Maximum:    &cap,
	}
	exprPrice, err := service.NewGenerationExpressionSnapshotWithRequest(exprRule, map[string]jsplugin.UsageFieldSchema{"seconds": {Type: "number", Unit: "second"}}, json.RawMessage(`{"seconds":8}`), []byte(`{"quality":"pro"}`), map[string]string{"x-tier": "vip"}, decimal.RequireFromString("1.5"))
	require.NoError(t, err)
	exprID, err := service.NewGenerationTaskID()
	require.NoError(t, err)
	exprTask := &service.GenerationTask{ID: exprID, UserID: user, APIKeyID: key, GroupID: group, AccountID: account, PluginVersionID: v.ID, IdempotencyKey: "probe-expr-1", Fingerprint: strings.Repeat("d", 64), Model: "grok-imagine-video", UpstreamModel: "grok-imagine-video", Platform: "grok", Protocol: "openai_video", Action: "text_to_video", Request: json.RawMessage(`{"prompt":"fixture"}`), Price: exprPrice, Connection: service.GenerationConnection{BaseURL: "https://fixture.example.invalid", Kind: "vendor"}}
	_, err = repo.CreateHeld(ctx, exprTask, &service.UsageBillingCommand{UserID: user, APIKeyID: key, AccountID: account, AccountType: service.AccountTypeAPIKey, BalanceCost: exprPrice.Hold.InexactFloat64(), APIKeyQuotaCost: exprPrice.Hold.InexactFloat64(), APIKeyRateLimitCost: exprPrice.Hold.InexactFloat64(), AccountQuotaCost: 20})
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE sub2api_generation_tasks SET status='succeeded',billing_status='pending',usage='{"seconds":8,"_host_final_usage":{"seconds":4}}'::jsonb,finished_at=NOW() WHERE id=$1`, exprID)
	require.NoError(t, err)
	claimed, err := repo.Claim(ctx, "probe-lease", time.Minute)
	require.NoError(t, err)
	// 4 * 1.5 * 0.5 = 3 USD base; * 1.5 group = 4.5 USD
	require.NoError(t, repo.Settle(ctx, claimed, decimal.RequireFromString("4.5"), nil))

	userView, err := taskLogs.GetTaskLog(ctx, exprID, user)
	require.NoError(t, err)
	require.Nil(t, userView.Billing, "ordinary user task log must not include admin billing details")
	adminView, err := taskLogs.GetTaskLog(ctx, exprID, 0)
	require.NoError(t, err)
	require.NotNil(t, adminView.Billing)
	require.Equal(t, "expression", adminView.Billing.Mode)
	require.Equal(t, "pro-tier", adminView.Billing.MatchedTier)
	require.Len(t, adminView.Billing.RequestRules, 2)

	// 2) cost_plus with frozen pre-submit quote persists quote_comparison for admin only.
	exRate := decimal.NewFromInt(1)
	markup := decimal.RequireFromString("1.2")
	costCap := decimal.NewFromInt(10)
	costRule := service.GenerationPriceRule{Mode: "cost_plus", SourceCurrency: "USD", SourceUnit: "upstream_cost", ExchangeRate: &exRate, Markup: &markup, Maximum: &costCap}
	now := time.Now().UTC()
	future := now.Add(10 * time.Minute)
	quote := &service.GenerationCost{Amount: decimal.NewFromInt(2), Currency: "USD", Unit: "upstream_cost", Source: "quote_api", ObservedAt: now, ExpiresAt: &future}
	costPrice, err := service.NewGenerationPriceSnapshot(costRule, decimal.NewFromInt(1), quote, decimal.NewFromInt(1))
	require.NoError(t, err)
	costID, err := service.NewGenerationTaskID()
	require.NoError(t, err)
	costTask := &service.GenerationTask{ID: costID, UserID: user, APIKeyID: key, GroupID: group, AccountID: account, PluginVersionID: v.ID, IdempotencyKey: "quote-cost-1", Fingerprint: strings.Repeat("e", 64), Model: "grok-imagine-video", UpstreamModel: "grok-imagine-video", Platform: "grok", Protocol: "openai_video", Action: "text_to_video", Request: json.RawMessage(`{"prompt":"fixture"}`), Price: costPrice, Connection: service.GenerationConnection{BaseURL: "https://fixture.example.invalid", Kind: "vendor"}}
	_, err = repo.CreateHeld(ctx, costTask, &service.UsageBillingCommand{UserID: user, APIKeyID: key, AccountID: account, AccountType: service.AccountTypeAPIKey, BalanceCost: costPrice.Hold.InexactFloat64(), APIKeyQuotaCost: costPrice.Hold.InexactFloat64(), APIKeyRateLimitCost: costPrice.Hold.InexactFloat64(), AccountQuotaCost: 10})
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE sub2api_generation_tasks SET status='succeeded',billing_status='pending',usage='{"_host_final_usage":{"upstream_cost":{"amount":"2.5","currency":"USD","unit":"upstream_cost","final":true,"source":"invoice"}}}'::jsonb,finished_at=NOW() WHERE id=$1`, costID)
	require.NoError(t, err)
	claimedCost, err := repo.Claim(ctx, "quote-lease", time.Minute)
	require.NoError(t, err)
	// 2.5 * 1.2 = 3.0 USD
	require.NoError(t, repo.Settle(ctx, claimedCost, decimal.NewFromInt(3), nil))

	userCostView, err := taskLogs.GetTaskLog(ctx, costID, user)
	require.NoError(t, err)
	require.Nil(t, userCostView.Billing)
	adminCostView, err := taskLogs.GetTaskLog(ctx, costID, 0)
	require.NoError(t, err)
	require.NotNil(t, adminCostView.Billing)
	require.Equal(t, "cost_plus", adminCostView.Billing.Mode)
	require.NotNil(t, adminCostView.Billing.QuoteComparison)
	require.Equal(t, "2", adminCostView.Billing.QuoteComparison.QuotedCost.String())
	require.Equal(t, "2.5", adminCostView.Billing.QuoteComparison.FinalCost.String())
	require.Equal(t, "0.5", adminCostView.Billing.QuoteComparison.CostDelta.String())
	require.Equal(t, "0.6", adminCostView.Billing.QuoteComparison.SaleDelta.String())
}
