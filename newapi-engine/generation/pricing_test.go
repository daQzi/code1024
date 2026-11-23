package generation

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestFinalBillObservationsNeverDefaultMissingAmountToZero(t *testing.T) {
	for _, amount := range []string{"", `"amount":null,`, `"amount":"1e1000000",`, `"amount":"-1",`} {
		raw := json.RawMessage(`{"upstream_cost":{` + amount + `"source":"invoice","currency":"USD","unit":"upstream_cost","final":true}}`)
		require.Nil(t, ObservedCost(raw))
	}
	cost := ObservedCost(json.RawMessage(`{"upstream_cost":{"amount":0,"source":"invoice","currency":"USD","unit":"upstream_cost","final":true}}`))
	require.NotNil(t, cost)
	require.True(t, cost.Amount.IsZero())
}

func generationDecimal(value string) *decimal.Decimal {
	d := decimal.RequireFromString(value)
	return &d
}

func TestGenerationPricingExplicitZeroAndCostPending(t *testing.T) {
	rule := PriceRule{Mode: "fixed", Unit: "second"}
	_, err := NewPriceSnapshot(rule, decimal.NewFromInt(30), nil, decimal.NewFromInt(1))
	require.Error(t, err)
	rule.UnitPrice = generationDecimal("0")
	snapshot, err := NewPriceSnapshot(rule, decimal.NewFromInt(30), nil, decimal.NewFromInt(1))
	require.NoError(t, err)
	require.True(t, snapshot.Hold.IsZero())
	rule = PriceRule{Mode: "cost_plus", SourceCurrency: "CNY", SourceUnit: "currency", ExchangeRate: generationDecimal("0.14"), Markup: generationDecimal("1.3")}
	_, err = NewPriceSnapshot(rule, decimal.Zero, nil, decimal.NewFromInt(1))
	require.ErrorIs(t, err, ErrPriceUnavailable)
	quote := &Cost{Amount: decimal.NewFromInt(2), Currency: "CNY", Unit: "currency", Source: "upstream_quote", ObservedAt: time.Now()}
	snapshot, err = NewPriceSnapshot(rule, decimal.Zero, quote, decimal.NewFromInt(1))
	require.NoError(t, err)
	require.Equal(t, "0.364", snapshot.Hold.String())
	_, err = snapshot.Settle(decimal.Zero, quote)
	require.ErrorIs(t, err, ErrCostPending)
	final := *quote
	final.Final = true
	final.Amount = decimal.RequireFromString("1.5")
	actual, err := snapshot.Settle(decimal.Zero, &final)
	require.NoError(t, err)
	require.Equal(t, "0.273", actual.String())
	final.Amount = decimal.NewFromInt(3)
	_, err = snapshot.Settle(decimal.Zero, &final)
	require.ErrorIs(t, err, ErrPriceExceedsHold)
	final.Amount = decimal.Zero
	final.Currency = "USD"
	_, err = snapshot.Settle(decimal.Zero, &final)
	require.Error(t, err)
}

func TestGenerationPriceBoundsAndLongVideo(t *testing.T) {
	rule := PriceRule{Mode: "fixed", Unit: "second", UnitPrice: generationDecimal("0.0125"), ServiceFee: decimal.RequireFromString("0.1"), Minimum: decimal.RequireFromString("0.2")}
	snapshot, err := NewPriceSnapshot(rule, decimal.NewFromInt(30), nil, decimal.RequireFromString("1.2"))
	require.NoError(t, err)
	require.Equal(t, "0.57", snapshot.Hold.String()) // 30 seconds is never clamped to Grok's old 15s limit.
	actual, err := snapshot.Settle(decimal.NewFromInt(24), nil)
	require.NoError(t, err)
	require.Equal(t, "0.48", actual.String())
	rule.UnitPrice = generationDecimal("100")
	actual, err = snapshot.Settle(decimal.NewFromInt(24), nil)
	require.NoError(t, err)
	require.Equal(t, "0.48", actual.String())
}

func TestCostPlusQuoteValidationExpiryAndComparison(t *testing.T) {
	now := time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)
	future := now.Add(5 * time.Minute)
	past := now.Add(-1 * time.Minute)
	rule := PriceRule{
		Mode:           "cost_plus",
		SourceCurrency: "USD",
		SourceUnit:     "upstream_cost",
		ExchangeRate:   generationDecimal("1"),
		Markup:         generationDecimal("1.2"),
		Maximum:        generationDecimal("10"),
	}
	expired := &Cost{Amount: decimal.RequireFromString("2"), Currency: "USD", Unit: "upstream_cost", Source: "quote_api", ObservedAt: now.Add(-10 * time.Minute), ExpiresAt: &past}
	_, err := NewPriceSnapshotAt(rule, decimal.NewFromInt(1), expired, decimal.NewFromInt(1), now)
	require.ErrorIs(t, err, ErrQuoteExpired)

	overCap := &Cost{Amount: decimal.RequireFromString("9"), Currency: "USD", Unit: "upstream_cost", Source: "quote_api", ObservedAt: now, ExpiresAt: &future}
	_, err = NewPriceSnapshotAt(rule, decimal.NewFromInt(1), overCap, decimal.NewFromInt(1), now)
	require.ErrorIs(t, err, ErrPriceExceedsHold)

	validQuote := &Cost{Amount: decimal.RequireFromString("2"), Currency: "USD", Unit: "upstream_cost", Final: true, Source: "quote_api", ObservedAt: now, ExpiresAt: &future}
	snapshot, err := NewPriceSnapshotAt(rule, decimal.NewFromInt(1), validQuote, decimal.RequireFromString("1.5"), now)
	require.NoError(t, err)
	require.Equal(t, "15", snapshot.Hold.String()) // Maximum * GroupMultiplier
	require.NotNil(t, snapshot.Quote)
	require.False(t, snapshot.Quote.Final, "frozen pre-submit quote must never be marked final")

	finalCost := &Cost{Amount: decimal.RequireFromString("2.5"), Currency: "USD", Unit: "upstream_cost", Final: true, Source: "invoice", ObservedAt: now.Add(time.Minute)}
	actual, err := snapshot.Settle(decimal.NewFromInt(1), finalCost)
	require.NoError(t, err)
	require.Equal(t, "4.5", actual.String()) // 2.5 * 1.2 * 1.5

	cmp := snapshot.CompareQuote(finalCost, &actual)
	require.NotNil(t, cmp)
	require.Equal(t, "2", cmp.QuotedCost.String())
	require.Equal(t, "3.6", cmp.QuotedSaleAmount.String()) // 2 * 1.2 * 1.5
	require.Equal(t, "2.5", cmp.FinalCost.String())
	require.Equal(t, "0.5", cmp.CostDelta.String())
	require.Equal(t, "0.9", cmp.SaleDelta.String())
}
