package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestGenerationFinalBillLocalSettlementUsesPersistedCostAndSeparateAccountRate(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	d := decimal.RequireFromString
	exchange, markup, ceiling := d("0.14"), d("1.2"), d("2")
	rule := service.GenerationPriceRule{Mode: "cost_plus", SourceCurrency: "CNY", SourceUnit: "upstream_cost", ExchangeRate: &exchange, Markup: &markup, Maximum: &ceiling}
	_, err := f.db.Exec(`UPDATE accounts SET extra='{"quota_limit":100}' WHERE id=$1`, f.account)
	require.NoError(t, err)
	for _, scenario := range []struct {
		name, rate, cost, wantUser, wantAccount string
	}{
		{"normal", "1.5", `{"amount":"5","currency":"CNY","unit":"upstream_cost","final":true,"source":"invoice"}`, "1.26", "1.68"},
		{"free user", "0", `{"amount":"5","currency":"CNY","unit":"upstream_cost","final":true,"source":"invoice"}`, "0", "1.68"},
		{"explicit zero", "1", `{"amount":"0","currency":"CNY","unit":"upstream_cost","final":true,"source":"invoice"}`, "0", "0"},
		{"missing final invoice", "1", `null`, "", ""},
		{"missing amount is not zero", "1", `{"currency":"CNY","unit":"upstream_cost","final":true,"source":"invoice"}`, "", ""},
		{"null amount is not zero", "1", `{"amount":null,"currency":"CNY","unit":"upstream_cost","final":true,"source":"invoice"}`, "", ""},
		{"overlarge exponent", "1", `{"amount":"1e1000000","currency":"CNY","unit":"upstream_cost","final":true,"source":"invoice"}`, "", ""},
		{"wrong currency", "1", `{"amount":"5","currency":"USD","unit":"upstream_cost","final":true,"source":"invoice"}`, "", ""},
		{"over ceiling with zero user rate", "0", `{"amount":"15","currency":"CNY","unit":"upstream_cost","final":true,"source":"invoice"}`, "", ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var accountBefore float64
			require.NoError(t, f.db.QueryRow(`SELECT COALESCE((extra->>'quota_used')::numeric,0) FROM accounts WHERE id=$1`, f.account).Scan(&accountBefore))
			task, cmd := f.task(t, &service.GenerationFile{})
			task.Files = nil
			var err error
			task.Price, err = service.NewGenerationPriceSnapshot(rule, d("1"), nil, d(scenario.rate))
			require.NoError(t, err)
			hold := task.Price.Hold.InexactFloat64()
			cmd.BalanceCost, cmd.APIKeyQuotaCost, cmd.APIKeyRateLimitCost, cmd.AccountQuotaCost = hold, hold, hold, 4
			_, err = f.repo.CreateHeld(ctx, task, cmd)
			require.NoError(t, err)
			_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET status='succeeded',billing_status='pending',finished_at=NOW(),usage=$2::jsonb WHERE id=$1`, task.ID, `{"_host_final_usage":{"upstream_cost":`+scenario.cost+`}}`)
			require.NoError(t, err)
			claimed, err := f.repo.Claim(ctx, "final-bill-worker", time.Minute)
			require.NoError(t, err)
			require.Equal(t, task.ID, claimed.ID)
			if scenario.wantUser != "" && scenario.wantUser != "0" {
				require.ErrorIs(t, f.repo.Settle(ctx, claimed, decimal.Zero, nil), service.ErrUsageBillingRequestConflict)
			}
			executor := generation.NewExecutor(generation.ExecutorOptions{Store: f.repo})
			require.NoError(t, executor.Process(ctx, claimed))
			var accountAfter float64
			require.NoError(t, f.db.QueryRow(`SELECT (extra->>'quota_used')::numeric FROM accounts WHERE id=$1`, f.account).Scan(&accountAfter))
			if scenario.wantUser == "" {
				require.Equal(t, service.GenerationBillingReview, claimed.BillingStatus)
				require.InDelta(t, 4, accountAfter-accountBefore, 1e-8)
				return
			}
			require.Equal(t, service.GenerationBillingSettled, claimed.BillingStatus)
			var actual, accountAmount float64
			require.NoError(t, f.db.QueryRow(`SELECT actual_cost,account_stats_cost FROM usage_logs WHERE request_id=$1`, task.ID).Scan(&actual, &accountAmount))
			require.InDelta(t, d(scenario.wantUser).InexactFloat64(), actual, 1e-8)
			require.InDelta(t, d(scenario.wantAccount).InexactFloat64(), accountAmount, 1e-8)
			require.InDelta(t, accountAmount, accountAfter-accountBefore, 1e-8)
		})
	}
}
