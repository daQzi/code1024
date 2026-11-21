package service

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func generationDecimal(value string) *decimal.Decimal {
	d := decimal.RequireFromString(value)
	return &d
}

func TestGenerationPricingExplicitZeroAndCostPending(t *testing.T) {
	rule := GenerationPriceRule{Mode: "fixed", Unit: "second"}
	_, err := NewGenerationPriceSnapshot(rule, decimal.NewFromInt(30), nil, decimal.NewFromInt(1))
	require.Error(t, err)
	rule.UnitPrice = generationDecimal("0")
	snapshot, err := NewGenerationPriceSnapshot(rule, decimal.NewFromInt(30), nil, decimal.NewFromInt(1))
	require.NoError(t, err)
	require.True(t, snapshot.Hold.IsZero())
	rule = GenerationPriceRule{Mode: "cost_plus", SourceCurrency: "CNY", SourceUnit: "currency", ExchangeRate: generationDecimal("0.14"), Markup: generationDecimal("1.3")}
	_, err = NewGenerationPriceSnapshot(rule, decimal.Zero, nil, decimal.NewFromInt(1))
	require.ErrorIs(t, err, ErrGenerationPriceUnavailable)
	quote := &GenerationCost{Amount: decimal.NewFromInt(2), Currency: "CNY", Unit: "currency", Source: "upstream_quote", ObservedAt: time.Now()}
	snapshot, err = NewGenerationPriceSnapshot(rule, decimal.Zero, quote, decimal.NewFromInt(1))
	require.NoError(t, err)
	require.Equal(t, "0.364", snapshot.Hold.String())
	_, err = snapshot.Settle(decimal.Zero, quote)
	require.ErrorIs(t, err, ErrGenerationCostPending)
	final := *quote
	final.Final = true
	final.Amount = decimal.RequireFromString("1.5")
	actual, err := snapshot.Settle(decimal.Zero, &final)
	require.NoError(t, err)
	require.Equal(t, "0.273", actual.String())
	final.Amount = decimal.NewFromInt(3)
	_, err = snapshot.Settle(decimal.Zero, &final)
	require.ErrorIs(t, err, ErrGenerationPriceExceedsHold)
	final.Amount = decimal.Zero
	final.Currency = "USD"
	_, err = snapshot.Settle(decimal.Zero, &final)
	require.Error(t, err)
}

func TestGenerationPriceBoundsAndLongVideo(t *testing.T) {
	rule := GenerationPriceRule{Mode: "fixed", Unit: "second", UnitPrice: generationDecimal("0.0125"), ServiceFee: decimal.RequireFromString("0.1"), Minimum: decimal.RequireFromString("0.2")}
	snapshot, err := NewGenerationPriceSnapshot(rule, decimal.NewFromInt(30), nil, decimal.RequireFromString("1.2"))
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
