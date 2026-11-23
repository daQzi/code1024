package modelmodifier

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestModifierPinnedSyntaxAndRightmostPrecedence(t *testing.T) {
	for _, raw := range []string{"model@region", "model@2026-11-22", "org/model@revision"} {
		parsed, err := Parse(raw)
		require.NoError(t, err)
		require.Equal(t, raw, parsed.Base)
		require.False(t, parsed.HasControls())
	}
	parsed, err := Parse("org/model@revision@effort:wrong@TEMPERATURE:0.1@effort:high@temperature:0.3@topp:0.8")
	require.NoError(t, err)
	require.Equal(t, "org/model@revision", parsed.Base)
	require.Equal(t, "high", parsed.Effort())
	require.Equal(t, 0.3, *parsed.Temperature)
	require.Equal(t, 0.8, *parsed.TopP)
	for _, raw := range []string{"m@effort:", "m@effort:random", "m@thinking:-2", "m@temperature:NaN", "m@temperature:+Inf", "m@topp:bad", "m@unknown:yes", "m" + strings.Repeat("@effort:high", 33)} {
		_, err := Parse(raw)
		require.ErrorIs(t, err, ErrInvalid, raw)
	}
}

func TestModifierThinkingOrderAndMappedOverlay(t *testing.T) {
	for _, tc := range []struct {
		name, effort string
		budget       int
		mode         reasoning.Mode
	}{
		{"m@thinking:2048@effort:high", "high", 2048, reasoning.ModeEnabled},
		{"m@effort:high@thinking:2048", "high", 2048, reasoning.ModeEnabled},
		{"m@thinking:2048@thinking:adaptive", "", 0, reasoning.ModeAdaptive},
		{"m@effort:high@thinking:off", "none", 0, reasoning.ModeDisabled},
		{"m@thinking:2048@effort:none", "none", 0, reasoning.ModeDisabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := Parse(tc.name)
			require.NoError(t, err)
			require.Equal(t, tc.mode, parsed.Intent.Mode)
			require.Equal(t, tc.effort, string(parsed.Intent.Effort))
			if tc.budget > 0 {
				require.Equal(t, tc.budget, *parsed.Intent.BudgetTokens)
			} else {
				require.Nil(t, parsed.Intent.BudgetTokens)
			}
		})
	}
	parsed, err := Resolve("public@thinking:4096@effort:high@temperature:0.3@topp:0.6", "vendor@effort:low@temperature:0.1")
	require.NoError(t, err)
	require.Equal(t, "vendor", parsed.Base)
	require.Equal(t, "low", parsed.Effort())
	require.Nil(t, parsed.Intent.BudgetTokens)
	require.Equal(t, 0.1, *parsed.Temperature)
	require.Equal(t, 0.6, *parsed.TopP)
	require.Equal(t, []string{"vendor@effort:low@thinking:on", "vendor@thinking:on", "vendor"}, parsed.BillingNames())
}

func TestModifierExemptionsPreserveCompleteLiteralNames(t *testing.T) {
	p, err := CompilePolicy([]string{"model@temperature:0.2", `re:^vendor/.+@sha256:`})
	require.NoError(t, err)
	for _, raw := range []string{"model@temperature:0.2", "vendor/model@sha256:abc"} {
		parsed, err := p.Parse(raw)
		require.NoError(t, err)
		require.Equal(t, raw, parsed.Base)
		require.False(t, parsed.HasControls())
	}
	_, err = p.Parse("other/model@sha256:abc")
	require.ErrorIs(t, err, ErrInvalid)
	parsed, err := p.Parse("model@temperature:0.3")
	require.NoError(t, err)
	require.Equal(t, "model", parsed.Base)
	for _, entries := range [][]string{{"re:["}, {"re:"}, {" "}, {strings.Repeat("x", 513)}, make([]string, 129)} {
		_, err = CompilePolicy(entries)
		require.Error(t, err)
	}
}

func TestModifierProtocolControlsPreserveOtherRequestFields(t *testing.T) {
	parsed, err := Resolve("m@effort:high@temperature:0.1@topp:0.8", "vendor")
	require.NoError(t, err)
	for _, responses := range []bool{false, true} {
		out, err := parsed.Apply([]byte(`{"model":"m","temperature":0.9,"reasoning_effort":"low","reasoning":{"summary":"auto","effort":"low","enabled":false,"max_tokens":100},"unknown":{"preserve":true}}`), responses)
		require.NoError(t, err)
		require.Equal(t, "m", gjson.GetBytes(out, "model").String())
		require.Equal(t, 0.1, gjson.GetBytes(out, "temperature").Float())
		require.Equal(t, 0.8, gjson.GetBytes(out, "top_p").Float())
		require.True(t, gjson.GetBytes(out, "unknown.preserve").Bool())
		if responses {
			require.Equal(t, "high", gjson.GetBytes(out, "reasoning.effort").String())
		} else {
			require.Equal(t, "high", gjson.GetBytes(out, "reasoning_effort").String())
			require.JSONEq(t, `{"summary":"auto"}`, gjson.GetBytes(out, "reasoning").Raw)
		}
	}
	parsed, err = Parse("m@thinking:2048")
	require.NoError(t, err)
	out, err := parsed.Apply([]byte(`{"model":"m","reasoning_effort":"low","reasoning":{"effort":"low"}}`), false)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(out, "reasoning_effort").Exists())
	require.True(t, gjson.GetBytes(out, "reasoning.enabled").Bool())
	require.EqualValues(t, 2048, gjson.GetBytes(out, "reasoning.max_tokens").Int())
	require.Equal(t, []string{"m@thinking:on", "m"}, parsed.BillingNames())
	require.Equal(t, []string{"m"}, BillingNames("m@temperature:0.2"))
}

func TestModifierRejectsAmbiguousAndUnboundedJSON(t *testing.T) {
	parsed, err := Parse("m@effort:high")
	require.NoError(t, err)
	for _, raw := range []string{`{"model":"m","reasoning":5}`, `{"model":"m","reasoning":{"effort":"low","effort":"high"}}`, `{"model":"m","Reasoning_Effort":"low"}`, `{"model":"m","Temperature":0.3}`, `{"model":"m","reasoning":{"Effort":"low"}}`, `{"model":"m","padding":"` + strings.Repeat("x", 1<<20) + `"}`, `{"model":"m","items":[` + strings.Repeat("0,", 8192) + `0]}`} {
		_, err := parsed.Apply([]byte(raw), false)
		require.ErrorIs(t, err, ErrInvalid)
	}
}

func TestModifierReconcilePreservesBudgetUntilExplicitOverride(t *testing.T) {
	for _, responses := range []bool{false, true} {
		spec, err := Parse("vendor@thinking:2048")
		require.NoError(t, err)
		before, err := spec.Apply([]byte(`{"model":"vendor","reasoning":{"effort":"medium"}}`), responses)
		require.NoError(t, err)
		unchanged, err := spec.Reconcile(before, before, responses, false)
		require.NoError(t, err)
		require.EqualValues(t, 2048, *unchanged.Intent.BudgetTokens)
		after := []byte(`{"model":"vendor","reasoning_effort":"low"}`)
		if responses {
			after = []byte(`{"model":"vendor","reasoning":{"effort":"low"}}`)
		}
		updated, err := spec.Reconcile(before, after, responses, true)
		require.NoError(t, err)
		require.Nil(t, updated.Intent.BudgetTokens)
		require.Equal(t, "low", updated.Effort())
		require.Equal(t, "vendor@effort:low@thinking:on", updated.BillingNames()[0])
	}
}

func TestModifierApplyAndReconcileForClaudeAndGemini(t *testing.T) {
	// Claude: thinking budget + effort + temperature/top_p + max_tokens floor.
	claudeSpec, err := Resolve("claude-sonnet-4-5@thinking:2048@temperature:0.2@topp:0.9", "claude-sonnet-4-5")
	require.NoError(t, err)
	out, err := claudeSpec.ApplyForFormat([]byte(`{"model":"claude-sonnet-4-5","max_tokens":1024,"messages":[{"role":"user","content":"hi"}]}`), "claude")
	require.NoError(t, err)
	require.Equal(t, "enabled", gjson.GetBytes(out, "thinking.type").String())
	require.EqualValues(t, 2048, gjson.GetBytes(out, "thinking.budget_tokens").Int())
	require.Greater(t, gjson.GetBytes(out, "max_tokens").Int(), int64(2048))
	require.Equal(t, 0.2, gjson.GetBytes(out, "temperature").Float())
	require.Equal(t, 0.9, gjson.GetBytes(out, "top_p").Float())

	// Reconcile Claude override preserves unchanged budget and adopts explicit effort change.
	unchanged, err := claudeSpec.ReconcileForFormat(out, out, "claude", false)
	require.NoError(t, err)
	require.EqualValues(t, 2048, *unchanged.Intent.BudgetTokens)

	overridden := []byte(`{"model":"claude-opus-4-6","max_tokens":8192,"thinking":{"type":"adaptive"},"output_config":{"effort":"low"}}`)
	claudeOpusSpec := Spec{Base: "claude-opus-4-6", Thinking: true, Intent: claudeSpec.Intent}
	updated, err := claudeOpusSpec.ReconcileForFormat(out, overridden, "claude", true)
	require.NoError(t, err)
	require.Equal(t, reasoning.ModeAdaptive, updated.Intent.Mode)
	require.Equal(t, "low", updated.Effort())

	// Claude rejects non-object thinking / output_config.
	_, err = claudeSpec.ApplyForFormat([]byte(`{"model":"claude-sonnet-4-5","thinking":true}`), "claude")
	require.ErrorIs(t, err, ErrInvalid)
	_, err = claudeSpec.ApplyForFormat([]byte(`{"model":"claude-sonnet-4-5","output_config":"high"}`), "claude")
	require.ErrorIs(t, err, ErrInvalid)

	// Gemini: camelCase generationConfig and snake_case generation_config.
	gemSpec, err := Resolve("gemini-2.5-pro@thinking:2048@temperature:0.3@topp:0.7", "gemini-2.5-pro")
	require.NoError(t, err)
	gemOut, err := gemSpec.ApplyForFormat([]byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`), "gemini")
	require.NoError(t, err)
	require.Equal(t, 0.3, gjson.GetBytes(gemOut, "generationConfig.temperature").Float())
	require.Equal(t, 0.7, gjson.GetBytes(gemOut, "generationConfig.topP").Float())
	require.EqualValues(t, 2048, gjson.GetBytes(gemOut, "generationConfig.thinkingConfig.thinkingBudget").Int())

	gemSnake, err := gemSpec.ApplyForFormat([]byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generation_config":{"max_output_tokens":4096}}`), "gemini")
	require.NoError(t, err)
	require.Equal(t, 0.3, gjson.GetBytes(gemSnake, "generation_config.temperature").Float())
	require.Equal(t, 0.7, gjson.GetBytes(gemSnake, "generation_config.top_p").Float())
	require.EqualValues(t, 2048, gjson.GetBytes(gemSnake, "generation_config.thinking_config.thinkingBudget").Int())

	// Gemini effort modifier on Gemini 3 renders thinkingLevel, and ReconcileForFormat reads it back.
	gem3Spec, err := Resolve("gemini-3-pro@effort:low", "gemini-3-pro")
	require.NoError(t, err)
	gem3Out, err := gem3Spec.ApplyForFormat([]byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`), "gemini")
	require.NoError(t, err)
	require.Equal(t, "low", strings.ToLower(gjson.GetBytes(gem3Out, "generationConfig.thinkingConfig.thinkingLevel").String()))
	gem3Override := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"HIGH"}}}`)
	gem3Updated, err := gem3Spec.ReconcileForFormat(gem3Out, gem3Override, "gemini", true)
	require.NoError(t, err)
	require.Equal(t, "high", gem3Updated.Effort())

	// Gemini rejects non-object generationConfig / thinkingConfig.
	_, err = gemSpec.ApplyForFormat([]byte(`{"generationConfig":42}`), "gemini")
	require.ErrorIs(t, err, ErrInvalid)
	_, err = gemSpec.ApplyForFormat([]byte(`{"generationConfig":{"thinkingConfig":"bad"}}`), "gemini")
	require.ErrorIs(t, err, ErrInvalid)
}
