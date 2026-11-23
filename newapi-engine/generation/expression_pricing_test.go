package generation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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

func TestGenerationExpressionChecksAllSchemaBranchesBeforeNewHold(t *testing.T) {
	rule := PriceRule{Mode: "expression", Expression: `u("seconds") == 3600 ? -1 : 0.25`, Maximum: generationDecimal("10")}
	_, err := NewExpressionSnapshot(rule, generationExpressionSchema(), json.RawMessage(`{"seconds":1}`), decimal.NewFromInt(1))
	require.ErrorContains(t, err, "schema boundary")
	// Previously accepted in-flight tasks retain their immutable contract;
	// editing the admission checks must not strand a valid final settlement.
	snapshot := PriceSnapshot{Rule: rule, Hold: decimal.NewFromInt(10), GroupMultiplier: decimal.NewFromInt(1), Expression: &ExpressionSnapshot{
		SHA256: HashPayload([]byte(rule.Expression)), Schema: map[string]ExpressionField{"seconds": {Type: "number"}}, Facts: map[string]any{"seconds": float64(1)},
	}}
	charge, err := snapshot.SettleExpression(json.RawMessage(`{"seconds":1}`))
	require.NoError(t, err)
	require.Equal(t, "0.25", charge.Amount.String())
}

func TestGenerationExpressionTimePricingSurvivesRestart(t *testing.T) {
	at := time.Date(2026, time.November, 21, 23, 59, 0, 0, time.UTC)
	rule := PriceRule{Mode: "expression", Expression: `weekday("Asia/Shanghai") == 0 && hour("Asia/Shanghai") < 12 ? tier("weekend-morning", u("seconds") * 0.1) : tier("standard", u("seconds") * 0.2)`, Maximum: generationDecimal("10")}
	snapshot, err := newExpressionSnapshotAt(rule, generationExpressionSchema(), json.RawMessage(`{"seconds":8}`), decimal.NewFromInt(2), at)
	require.NoError(t, err)
	require.NotNil(t, snapshot.Expression.Clock)
	require.Equal(t, "1.6", snapshot.Expression.EstimatedAmount.String())
	require.Equal(t, "weekend-morning", snapshot.Expression.EstimatedTier)
	require.Equal(t, 8*3600, snapshot.Expression.Clock.Offsets["Asia/Shanghai"])
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var restarted PriceSnapshot
	require.NoError(t, json.Unmarshal(encoded, &restarted))
	charge, err := restarted.SettleExpression(json.RawMessage(`{"seconds":4}`))
	require.NoError(t, err)
	require.Equal(t, "0.8", charge.Amount.String())
	require.Equal(t, "weekend-morning", charge.Tier)
	// Calendar state is mandatory for a time-aware rule; a missing or corrupt
	// snapshot cannot silently use the later worker's time or reinterpret UTC.
	delete(restarted.Expression.Clock.Offsets, "Asia/Shanghai")
	_, err = restarted.SettleExpression(json.RawMessage(`{"seconds":4}`))
	require.Error(t, err)
	restarted.Expression.Clock = nil
	_, err = restarted.SettleExpression(json.RawMessage(`{"seconds":4}`))
	require.Error(t, err)
	require.NoError(t, json.Unmarshal(encoded, &restarted))
	restarted.CreatedAt = at.Add(time.Hour)
	_, err = restarted.SettleExpression(json.RawMessage(`{"seconds":4}`))
	require.ErrorContains(t, err, "admission instant")
}

func TestGenerationExpressionSnapshotFreezesMultidimensionalUSD(t *testing.T) {
	rule := PriceRule{Mode: "expression", Expression: `tier("video", u("seconds") * (u("resolution") == "1080p" ? 0.4 : 0.2) * (u("audio") ? 2 : 1))`, ServiceFee: decimal.RequireFromString("0.1"), Maximum: generationDecimal("10")}
	schema := generationExpressionSchema()
	snapshot, err := NewExpressionSnapshot(rule, schema, json.RawMessage(`{"seconds":10,"resolution":"1080p","audio":true,"private":"secret"}`), decimal.RequireFromString("1.5"))
	require.NoError(t, err)
	require.Equal(t, "15", snapshot.Hold.String(), "reserve the explicit ceiling, not the estimate")
	require.Equal(t, "12.15", snapshot.Expression.EstimatedAmount.String(), "Task expressions return USD without a million-token conversion")
	require.Equal(t, "video", snapshot.Expression.EstimatedTier)
	require.NotContains(t, snapshot.Expression.Facts, "private")
	*rule.Maximum = decimal.NewFromInt(100)
	schema["resolution"].Enum[0] = "changed"
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var restarted PriceSnapshot
	require.NoError(t, json.Unmarshal(encoded, &restarted))
	charge, err := restarted.SettleExpression(json.RawMessage(`{"seconds":4}`))
	require.NoError(t, err)
	require.Equal(t, "4.95", charge.Amount.String(), "immutable enum and boolean selectors may be retained")
	require.Equal(t, "3.3", charge.BaseAmount.String())
	charge, err = restarted.SettleExpression(json.RawMessage(`{"seconds":4,"resolution":"720p","audio":false}`))
	require.NoError(t, err)
	require.Equal(t, "1.35", charge.Amount.String(), "explicit completion selectors override admission")
	_, err = restarted.SettleExpression(json.RawMessage(`{"resolution":"720p"}`))
	require.ErrorIs(t, err, ErrCostPending, "numeric estimates cannot become final usage")
	charge, err = restarted.SettleExpression(json.RawMessage(`{"seconds":0}`))
	require.NoError(t, err)
	require.Equal(t, "0.15", charge.Amount.String(), "explicit measured zero is valid")
	restarted.Expression.SHA256 = strings.Repeat("0", 64)
	_, err = restarted.SettleExpression(json.RawMessage(`{"seconds":4}`))
	require.ErrorIs(t, err, ErrPriceUnavailable)
}

func TestGenerationExpressionValidationAndUnroundedCeiling(t *testing.T) {
	schema := generationExpressionSchema()
	base := PriceRule{Mode: "expression", Expression: `u("seconds")`, Maximum: generationDecimal("1")}
	for _, tc := range []struct {
		name  string
		rule  PriceRule
		usage string
	}{
		{"no maximum", PriceRule{Mode: "expression", Expression: "1"}, `{}`},
		{"zero maximum", PriceRule{Mode: "expression", Expression: "0", Maximum: generationDecimal("0")}, `{}`},
		{"unknown field", PriceRule{Mode: "expression", Expression: `u("secret")`, Maximum: generationDecimal("1")}, `{"secret":1}`},
		{"missing quantity", base, `{}`},
		{"quoted quantity", base, `{"seconds":"1"}`},
		{"negative quantity", base, `{"seconds":-1}`},
		{"null quantity", base, `{"seconds":null}`},
		{"subprecision excess", base, `{"seconds":1.000000001}`},
		{"unsupported relay expression", PriceRule{Mode: "expression", Expression: "p * 2", Maximum: generationDecimal("1")}, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewExpressionSnapshot(tc.rule, schema, json.RawMessage(tc.usage), decimal.NewFromInt(1))
			require.Error(t, err)
		})
	}
	snapshot, err := NewExpressionSnapshot(base, schema, json.RawMessage(`{"seconds":0.5}`), decimal.Zero)
	require.NoError(t, err)
	require.True(t, snapshot.Hold.IsZero())
	_, err = snapshot.SettleExpression(json.RawMessage(`{"seconds":1.000000001}`))
	require.ErrorIs(t, err, ErrPriceExceedsHold, "zero group multiplier does not waive the account ceiling")
	charge, err := snapshot.SettleExpression(json.RawMessage(`{"seconds":0.25}`))
	require.NoError(t, err)
	require.True(t, charge.Amount.IsZero())
	require.Equal(t, "0.25", charge.BaseAmount.String())
	_, err = base.Price(decimal.NewFromInt(1), nil, decimal.NewFromInt(1))
	require.Error(t, err, "the scalar price path must not bypass schema freezing")
	_, err = NewExpressionSnapshot(base, schema, json.RawMessage(`{"seconds":0.5}`), decimal.NewFromInt(-1))
	require.Error(t, err)
}

func TestGenerationExpressionFinalSchemaAndSelectors(t *testing.T) {
	rule := PriceRule{Mode: "expression", Expression: `u("seconds") * (u("audio") ? 2 : 1) * (u("resolution") == "1080p" ? 2 : 1)`, Maximum: generationDecimal("10")}
	snapshot, err := NewExpressionSnapshot(rule, generationExpressionSchema(), json.RawMessage(`{"seconds":1,"audio":false,"resolution":"720p"}`), decimal.NewFromInt(1))
	require.NoError(t, err)
	for _, final := range []string{`{}`, `[]`, `null`, `{"seconds":null}`, `{"seconds":1,"resolution":"4k"}`, `{"seconds":1,"audio":null}`, `{"seconds":1,"audio":0}`, `{"seconds":1e13}`} {
		t.Run(final, func(t *testing.T) {
			_, err := snapshot.SettleExpression(json.RawMessage(final))
			require.ErrorIs(t, err, ErrCostPending)
		})
	}
	delete(snapshot.Expression.Schema, "audio")
	_, err = snapshot.SettleExpression(json.RawMessage(`{"seconds":1}`))
	require.Error(t, err)
	rule.Expression = `tier("request", 0.25)`
	snapshot, err = NewExpressionSnapshot(rule, nil, json.RawMessage(`{}`), decimal.NewFromInt(2))
	require.NoError(t, err)
	charge, err := snapshot.SettleExpression(nil)
	require.NoError(t, err)
	require.Equal(t, "0.5", charge.Amount.String())
}

func TestGenerationExpressionRequestProbesSurviveRestartAndTraceRules(t *testing.T) {
	rule := PriceRule{
		Mode:       "expression",
		Expression: `tier("hd", u("seconds") * (param("quality") == "pro" ? 1.5 : 1) * (header("X-Tier") == "vip" ? 0.5 : 1))`,
		Maximum:    generationDecimal("50"),
	}
	body := []byte(`{"quality":"pro","secret_ignored":"never-stored"}`)
	headers := map[string]string{"X-Tier": "vip", "Authorization": "Bearer secret"}
	snapshot, err := NewExpressionSnapshotWithRequest(rule, generationExpressionSchema(), json.RawMessage(`{"seconds":8}`), body, headers, decimal.NewFromInt(2))
	require.NoError(t, err)
	require.NotNil(t, snapshot.Expression.Probes)
	require.Equal(t, "pro", snapshot.Expression.Probes.Params["quality"])
	require.Equal(t, "vip", snapshot.Expression.Probes.Headers["x-tier"])
	require.NotContains(t, snapshot.Expression.Probes.Headers, "authorization")
	require.NotContains(t, snapshot.Expression.Probes.Params, "secret_ignored")
	// 8 * 1.5 * 0.5 = 6 USD base; * 2 group multiplier = 12 USD
	require.Equal(t, "12", snapshot.Expression.EstimatedAmount.String())
	require.Len(t, snapshot.Expression.EstimatedRequestRules, 2)

	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret")
	var restarted PriceSnapshot
	require.NoError(t, json.Unmarshal(raw, &restarted))
	charge, err := restarted.SettleExpression(json.RawMessage(`{"seconds":4}`))
	require.NoError(t, err)
	require.Equal(t, "6", charge.Amount.String())
	require.Equal(t, "hd", charge.Tier)
	require.Len(t, charge.RequestRules, 2)

	// Missing probe snapshot must fail closed.
	restarted.Expression.Probes = nil
	_, err = restarted.SettleExpression(json.RawMessage(`{"seconds":4}`))
	require.Error(t, err)
}

func TestGenerationExpressionDeterministicWithoutFinalResponse(t *testing.T) {
	selectorRule := PriceRule{
		Mode:       "expression",
		Expression: `u("resolution") == "1080p" ? tier("hd", 0.4) : tier("sd", 0.2)`,
		Maximum:    generationDecimal("5"),
	}
	selectorSnap, err := NewExpressionSnapshot(selectorRule, generationExpressionSchema(), json.RawMessage(`{"resolution":"1080p"}`), decimal.NewFromInt(2))
	require.NoError(t, err)
	require.True(t, selectorSnap.DeterministicWithoutFinalResponse())
	charge, err := selectorSnap.SettleExpression(nil)
	require.NoError(t, err)
	require.Equal(t, "0.8", charge.Amount.String())
	require.Equal(t, "hd", charge.Tier)

	numericRule := PriceRule{
		Mode:       "expression",
		Expression: `u("seconds") * 0.2`,
		Maximum:    generationDecimal("5"),
	}
	numericSnap, err := NewExpressionSnapshot(numericRule, generationExpressionSchema(), json.RawMessage(`{"seconds":5}`), decimal.NewFromInt(1))
	require.NoError(t, err)
	require.False(t, numericSnap.DeterministicWithoutFinalResponse())
	_, err = numericSnap.SettleExpression(nil)
	require.ErrorIs(t, err, ErrCostPending)
}
