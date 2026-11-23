package service

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func generationExpressionSchema() map[string]jsplugin.UsageFieldSchema {
	return map[string]jsplugin.UsageFieldSchema{
		"seconds":    {Type: "number", Unit: "second"},
		"resolution": {Enum: []string{"720p", "1080p"}},
		"audio":      {Type: "boolean"},
	}
}

func TestGenerationExpressionBindingRejectsInvalidSchemaBeforeIO(t *testing.T) {
	plugins, account, upstream := generationPluginFixture(t, "sora")
	for _, expression := range []string{
		`false ? u("private") : 1`,
		`u("seconds") == 3600 ? -1 : 1`,
		`1 / u("seconds")`,
	} {
		t.Run(expression, func(t *testing.T) {
			account.Credentials[NewAPIVideoPricingKey] = map[string]GenerationPriceRule{
				"*": {Mode: "expression", Expression: expression, Maximum: generationDecimal("10")},
			}
			err := plugins.NormalizeAccountBinding(context.Background(), account, nil)
			require.ErrorContains(t, err, "sora-2")
		})
	}
	require.Empty(t, upstream.requests, "invalid prices must be rejected before key checks or upstream requests")
}

func TestGenerationExpressionBindingUsesResolvedModelProfiles(t *testing.T) {
	plugins, account, upstream := generationPluginFixture(t, "sora")
	version := plugins.repo.(*generationPluginFixtureRepo).version
	version.Meta.UsageProfiles = []jsplugin.UsageProfile{
		{Models: []string{"sora-2"}, Schema: map[string]jsplugin.UsageFieldSchema{"seconds": {Type: "number", Unit: "second"}}},
		{Models: []string{"sora-2-pro"}, Schema: map[string]jsplugin.UsageFieldSchema{"credits": {Type: "number", Unit: "credit"}}},
	}
	account.Credentials["model_mapping"] = map[string]any{"public-video": "sora-2", "sora-2-pro": "ep-vendor-model"}
	rules := map[string]GenerationPriceRule{
		"*":          {Mode: "expression", Expression: `u("seconds") * 0.1`, Maximum: generationDecimal("10")},
		"sora-2-pro": {Mode: "expression", Expression: `u("credits") * 0.01`, Maximum: generationDecimal("10")},
	}
	models := []string{"sora-2", "sora-2-pro"}
	require.NoError(t, validateAccountTaskExpressions(account, version, models, rules, nil), "public aliases use the upstream profile; endpoint IDs fall back to the declared model")
	delete(rules, "sora-2-pro")
	require.ErrorContains(t, validateAccountTaskExpressions(account, version, models, rules, nil), "sora-2-pro", "a fallback must match every model where it applies")
	rules["sora-2-pro"] = GenerationPriceRule{Mode: "fixed", Unit: "request", UnitPrice: generationDecimal("1")}
	require.NoError(t, validateAccountTaskExpressions(account, version, models, rules, nil))
	rules["public-video"] = rules["*"]
	rules["*"] = GenerationPriceRule{Mode: "expression", Expression: `u("not_active")`, Maximum: generationDecimal("10")}
	require.NoError(t, validateAccountTaskExpressions(account, version, models, rules, nil), "fully shadowed defaults do not affect the current account")
	rules["unknown-model"] = rules["*"]
	require.ErrorContains(t, validateAccountTaskExpressions(account, version, models, rules, nil), "具体视频模型")
	require.Empty(t, upstream.requests)
}

func TestGenerationExpressionBindingPreservesUnchangedLegacyRule(t *testing.T) {
	plugins, account, upstream := genericVideoFixture(t)
	rule := GenerationPriceRule{Mode: "expression", Expression: `u("removed") * 0.1`, Maximum: generationDecimal("10")}
	account.Credentials[NewAPIVideoPricingKey] = map[string]GenerationPriceRule{"*": rule}
	previous := *account
	previous.Credentials = maps.Clone(account.Credentials)
	require.NoError(t, plugins.NormalizeAccountBinding(context.Background(), account, nil, &previous), "an unrelated account edit must retain an unchanged rule")
	_, err := NewGenerationExpressionSnapshot(rule, generationExpressionSchema(), json.RawMessage(`{"removed":1}`), decimal.NewFromInt(1))
	require.ErrorContains(t, err, "not declared", "new reservations must still check the current model schema")
	rule.Expression += " + 0.1"
	account.Credentials[NewAPIVideoPricingKey] = map[string]GenerationPriceRule{"*": rule}
	err = plugins.NormalizeAccountBinding(context.Background(), account, nil, &previous)
	require.ErrorContains(t, err, "not declared", "modifying a stale rule must pass schema validation")
	require.Empty(t, upstream.requests)
}

func TestGenerationExpressionSnapshotFreezesMultidimensionalUSD(t *testing.T) {
	rule := GenerationPriceRule{Mode: "expression", Expression: `tier("video", u("seconds") * (u("resolution") == "1080p" ? 0.4 : 0.2) * (u("audio") ? 2 : 1))`, ServiceFee: decimal.RequireFromString("0.1"), Maximum: generationDecimal("10")}
	schema := generationExpressionSchema()
	snapshot, err := NewGenerationExpressionSnapshot(rule, schema, json.RawMessage(`{"seconds":10,"resolution":"1080p","audio":true,"private":"secret"}`), decimal.RequireFromString("1.5"))
	require.NoError(t, err)
	require.Equal(t, "15", snapshot.Hold.String(), "reserve the explicit ceiling, not the estimate")
	require.Equal(t, "12.15", snapshot.Expression.EstimatedAmount.String(), "Task expressions return USD without a million-token conversion")
	require.Equal(t, "video", snapshot.Expression.EstimatedTier)
	require.NotContains(t, snapshot.Expression.Facts, "private")
	*rule.Maximum = decimal.NewFromInt(100)
	schema["resolution"].Enum[0] = "changed"
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var restarted GenerationPriceSnapshot
	require.NoError(t, json.Unmarshal(encoded, &restarted))
	charge, err := restarted.SettleExpression(json.RawMessage(`{"seconds":4}`))
	require.NoError(t, err)
	require.Equal(t, "4.95", charge.Amount.String(), "immutable enum and boolean selectors may be retained")
	require.Equal(t, "3.3", charge.BaseAmount.String())
	charge, err = restarted.SettleExpression(json.RawMessage(`{"seconds":4,"resolution":"720p","audio":false}`))
	require.NoError(t, err)
	require.Equal(t, "1.35", charge.Amount.String(), "explicit completion selectors override admission")
	_, err = restarted.SettleExpression(json.RawMessage(`{"resolution":"720p"}`))
	require.ErrorIs(t, err, ErrGenerationCostPending, "numeric estimates cannot become final usage")
	charge, err = restarted.SettleExpression(json.RawMessage(`{"seconds":0}`))
	require.NoError(t, err)
	require.Equal(t, "0.15", charge.Amount.String(), "explicit measured zero is valid")
	restarted.Expression.SHA256 = strings.Repeat("0", 64)
	_, err = restarted.SettleExpression(json.RawMessage(`{"seconds":4}`))
	require.ErrorIs(t, err, ErrGenerationPriceUnavailable)
}

func TestGenerationExpressionValidationAndUnroundedCeiling(t *testing.T) {
	schema := generationExpressionSchema()
	base := GenerationPriceRule{Mode: "expression", Expression: `u("seconds")`, Maximum: generationDecimal("1")}
	for _, tc := range []struct {
		name  string
		rule  GenerationPriceRule
		usage string
	}{
		{"no maximum", GenerationPriceRule{Mode: "expression", Expression: "1"}, `{}`},
		{"zero maximum", GenerationPriceRule{Mode: "expression", Expression: "0", Maximum: generationDecimal("0")}, `{}`},
		{"unknown field", GenerationPriceRule{Mode: "expression", Expression: `u("secret")`, Maximum: generationDecimal("1")}, `{"secret":1}`},
		{"missing quantity", base, `{}`},
		{"quoted quantity", base, `{"seconds":"1"}`},
		{"negative quantity", base, `{"seconds":-1}`},
		{"null quantity", base, `{"seconds":null}`},
		{"subprecision excess", base, `{"seconds":1.000000001}`},
		{"unsupported relay expression", GenerationPriceRule{Mode: "expression", Expression: "p * 2", Maximum: generationDecimal("1")}, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewGenerationExpressionSnapshot(tc.rule, schema, json.RawMessage(tc.usage), decimal.NewFromInt(1))
			require.Error(t, err)
		})
	}
	snapshot, err := NewGenerationExpressionSnapshot(base, schema, json.RawMessage(`{"seconds":0.5}`), decimal.Zero)
	require.NoError(t, err)
	require.True(t, snapshot.Hold.IsZero())
	_, err = snapshot.SettleExpression(json.RawMessage(`{"seconds":1.000000001}`))
	require.ErrorIs(t, err, ErrGenerationPriceExceedsHold, "zero group multiplier does not waive the account ceiling")
	charge, err := snapshot.SettleExpression(json.RawMessage(`{"seconds":0.25}`))
	require.NoError(t, err)
	require.True(t, charge.Amount.IsZero())
	require.Equal(t, "0.25", charge.BaseAmount.String())
	_, err = base.Price(decimal.NewFromInt(1), nil, decimal.NewFromInt(1))
	require.Error(t, err, "the scalar price path must not bypass schema freezing")
	_, err = NewGenerationExpressionSnapshot(base, schema, json.RawMessage(`{"seconds":0.5}`), decimal.NewFromInt(-1))
	require.Error(t, err)
}

func TestGenerationExpressionFinalSchemaAndSelectors(t *testing.T) {
	rule := GenerationPriceRule{Mode: "expression", Expression: `u("seconds") * (u("audio") ? 2 : 1) * (u("resolution") == "1080p" ? 2 : 1)`, Maximum: generationDecimal("10")}
	snapshot, err := NewGenerationExpressionSnapshot(rule, generationExpressionSchema(), json.RawMessage(`{"seconds":1,"audio":false,"resolution":"720p"}`), decimal.NewFromInt(1))
	require.NoError(t, err)
	for _, final := range []string{`{}`, `[]`, `null`, `{"seconds":null}`, `{"seconds":1,"resolution":"4k"}`, `{"seconds":1,"audio":null}`, `{"seconds":1,"audio":0}`, `{"seconds":1e13}`} {
		t.Run(final, func(t *testing.T) {
			_, err := snapshot.SettleExpression(json.RawMessage(final))
			require.ErrorIs(t, err, ErrGenerationCostPending)
		})
	}
	delete(snapshot.Expression.Schema, "audio")
	_, err = snapshot.SettleExpression(json.RawMessage(`{"seconds":1}`))
	require.Error(t, err)
	rule.Expression = `tier("request", 0.25)`
	snapshot, err = NewGenerationExpressionSnapshot(rule, nil, json.RawMessage(`{}`), decimal.NewFromInt(2))
	require.NoError(t, err)
	charge, err := snapshot.SettleExpression(nil)
	require.NoError(t, err)
	require.Equal(t, "0.5", charge.Amount.String())
}

func TestGenerationExpressionCreateAndFinalSettlement(t *testing.T) {
	plugins, account, upstream := generationPluginFixture(t, "sora")
	account.Credentials[NewAPIVideoPricingKey] = map[string]any{"*": map[string]any{"mode": "expression", "expression": `tier("sora", u("seconds") * (u("size") == "1280x720" ? 0.3 : 0.2))`, "maximum": "10"}}
	repo := &generationReplayFixture{}
	s := &GenerationService{repo: repo, plugins: plugins}
	key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, Group: &Group{ID: 3, RateMultiplier: 2}}
	task, err := s.Create(context.Background(), key, nil, account, "sora-2", json.RawMessage(`{"model":"sora-2","prompt":"Ocean","seconds":"8","size":"1280x720"}`), "accepted")
	require.NoError(t, err)
	require.Equal(t, "20", task.Price.Hold.String())
	require.Equal(t, "4.8", task.Price.Expression.EstimatedAmount.String())
	require.Empty(t, upstream.requests, "pricing must not call the upstream")
	task.Status, task.BillingStatus = GenerationSucceeded, GenerationBillingPending
	settled := &generationSettlementFixture{}
	s = &GenerationService{repo: settled}
	require.NoError(t, s.settle(context.Background(), task))
	require.True(t, settled.saved)
	require.False(t, settled.settled)
	task.BillingStatus = GenerationBillingPending
	task.Usage = json.RawMessage(`{"seconds":8,"_host_final_usage":{"seconds":4}}`)
	require.NoError(t, s.settle(context.Background(), task))
	require.True(t, settled.settled)
	require.Equal(t, "2.4", settled.amount.String())
	task.Status, task.BillingStatus, task.Usage = GenerationFailed, GenerationBillingPending, nil
	settled.settled = false
	require.NoError(t, s.settle(context.Background(), task))
	require.True(t, settled.settled)
	require.True(t, settled.amount.IsZero())
}

func TestGenerationExpressionTimeCreateAndFinalSettlement(t *testing.T) {
	plugins, account, upstream := generationPluginFixture(t, "sora")
	account.Credentials[NewAPIVideoPricingKey] = map[string]any{"*": map[string]any{"mode": "expression", "expression": `hour("UTC") < 12 ? tier("morning", u("seconds") * 0.1) : tier("later", u("seconds") * 0.2)`, "maximum": "10"}}
	s := &GenerationService{repo: &generationReplayFixture{}, plugins: plugins}
	key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, Group: &Group{ID: 3, RateMultiplier: 2}}
	task, err := s.Create(context.Background(), key, nil, account, "sora-2", json.RawMessage(`{"model":"sora-2","prompt":"Ocean","seconds":"8","size":"1280x720"}`), "timed")
	require.NoError(t, err)
	require.Empty(t, upstream.requests)
	require.NotNil(t, task.Price.Expression.Clock)
	require.True(t, task.Price.CreatedAt.Equal(task.Price.Expression.Clock.At))
	price, err := json.Marshal(task.Price)
	require.NoError(t, err)
	var restored GenerationPriceSnapshot
	require.NoError(t, json.Unmarshal(price, &restored))
	task.Price = restored
	task.Status, task.BillingStatus = GenerationSucceeded, GenerationBillingPending
	task.Usage = json.RawMessage(`{"seconds":8,"_host_final_usage":{"seconds":4}}`)
	settled := &generationSettlementFixture{}
	s = &GenerationService{repo: settled}
	require.NoError(t, s.settle(context.Background(), task))
	require.True(t, settled.settled)
	want := "1.6"
	if task.Price.Expression.Clock.At.Hour() < 12 {
		want = "0.8"
	}
	require.Equal(t, want, settled.amount.String())
}
