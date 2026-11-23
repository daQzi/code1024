package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type captureGenerationPricingRepo struct {
	generationReplayFixture
	command UsageBillingCommand
}

func (r *captureGenerationPricingRepo) CreateHeld(ctx context.Context, task *GenerationTask, cmd *UsageBillingCommand) (*GenerationTask, error) {
	r.command = *cmd
	return r.generationReplayFixture.CreateHeld(ctx, task, cmd)
}

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

func TestGenerationCatalogShowsExpressionPricingAsDynamic(t *testing.T) {
	price, ok := generationDisplayPrice(GenerationPriceRule{
		Mode: "expression", Expression: `tier("standard", u("seconds") * 0.02)`, Maximum: generationDecimal("1"),
	})
	require.True(t, ok)
	require.True(t, price.Dynamic)
	require.Equal(t, "expression", price.Unit)
}

func TestGenerationFinalBillReservesBothCeilingsWithoutQuote(t *testing.T) {
	plugins, account, upstream := generationPluginFixture(t, "sora")
	account.RateMultiplier = new(float64)
	*account.RateMultiplier = 2
	account.Credentials[NewAPIVideoPricingKey] = map[string]any{"sora-2": map[string]any{
		"mode": "cost_plus", "source_unit": "upstream_cost", "source_currency": "CNY", "exchange_rate": "0.14", "markup": "1.2", "maximum": "1.5",
	}}
	repo := &captureGenerationPricingRepo{}
	svc := &GenerationService{repo: repo, plugins: plugins, gateway: plugins.gateway}
	key := &APIKey{ID: 5, UserID: 4, User: &User{ID: 4}, Group: &Group{ID: 7, RateMultiplier: 1.5}}
	task, err := svc.Create(context.Background(), key, nil, account, "sora-2", json.RawMessage(`{"model":"sora-2","seconds":"4","prompt":"Draw"}`), "final-bill")
	require.NoError(t, err)
	require.Equal(t, "2.25", task.Price.Hold.String())
	require.Equal(t, 3.0, repo.command.AccountQuotaCost)
	require.Nil(t, task.Price.Quote, "the reservation ceiling is not an upstream quote")
	require.Empty(t, upstream.requests)
}
