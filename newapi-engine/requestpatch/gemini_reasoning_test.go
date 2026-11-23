package requestpatch

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiReasoningPreservesBusinessJSONAndNativeSpellings(t *testing.T) {
	for _, tc := range []struct{ model, body, path, field string }{
		{"gemini-3.1-pro", `{"generationConfig":{"maxOutputTokens":1024,"thinkingConfig":{"thinkingBudget":512,"includeThoughts":true,"vendor":7},"responseJsonSchema":{"z":1,"a":2}},"vendor":12345678901234567890}`, "generationConfig.thinkingConfig", "thinkingLevel"},
		{"gemini-2.5-flash", `{"generation_config":{"max_output_tokens":1024,"thinking_config":{"thinking_level":"low","include_thoughts":true,"vendor":7},"responseJsonSchema":{"z":1,"a":2}},"vendor":12345678901234567890}`, "generation_config.thinking_config", "thinking_budget"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			body := []byte(tc.body)
			out, err := SetGeminiReasoning(body, tc.model, "high")
			require.NoError(t, err)
			require.True(t, gjson.GetBytes(out, tc.path+"."+tc.field).Exists())
			require.Equal(t, "12345678901234567890", gjson.GetBytes(out, "vendor").Raw)
			require.EqualValues(t, 7, gjson.GetBytes(out, tc.path+".vendor").Int())
			require.Contains(t, string(out), `"responseJsonSchema":{"z":1,"a":2}`)
			require.Contains(t, string(out), `:true`)
			effort, explicit := GeminiReasoningEffort(out)
			require.True(t, explicit)
			require.Equal(t, "high", effort)
			require.Equal(t, tc.body, string(body))
		})
	}
}

func TestGeminiReasoningInvalidAliasesAndControls(t *testing.T) {
	for _, body := range []string{
		`[]`, `{"GenerationConfig":{}}`, `{"generation_config":{},"generationConfig":{}}`,
		`{"generationConfig":{"thinking_config":{},"thinkingConfig":{}}}`,
		`{"generationConfig":{"ThinkingConfig":{}}}`,
		`{"generationConfig":{"thinkingConfig":{"ThinkingBudget":123}}}`,
		`{"generationConfig":{"thinkingConfig":{"thinkingBudget":12,"thinking_budget":12}}}`,
		`{"generationConfig":{"maxOutputTokens":12,"max_output_tokens":12}}`,
		`{"generationConfig":false}`, `{"generationConfig":{"thinkingConfig":"invalid"}}`,
		`{"generationConfig":{"maxOutputTokens":-1}}`, `{"generationConfig":{"maxOutputTokens":2.5}}`,
		`{"generationConfig":{"thinkingConfig":{"includeThoughts":"yes"}}}`,
	} {
		_, err := SetGeminiReasoning([]byte(body), "gemini-2.5-flash", "high")
		require.Error(t, err, body)
	}
	for _, tc := range []struct {
		body, effort string
		explicit     bool
	}{
		{`{}`, "", false}, {`{"generationConfig":{"thinkingConfig":{"thinkingBudget":0}}}`, "none", true},
		{`{"generationConfig":{"thinkingConfig":{"thinkingBudget":-1}}}`, "high", true},
		{`{"generationConfig":{"thinkingConfig":{"thinkingBudget":"invalid"}}}`, "", true},
		{`{"generation_config":{"thinking_config":{"thinking_level":"low"}}}`, "low", true},
	} {
		effort, explicit := GeminiReasoningEffort([]byte(tc.body))
		require.Equal(t, tc.effort, effort)
		require.Equal(t, tc.explicit, explicit)
	}
}

func TestGeminiReasoningUsesFixedRendererCapabilities(t *testing.T) {
	out, err := SetGeminiReasoning([]byte(`{}`), "gemini-2.5-pro", "none")
	require.NoError(t, err)
	require.EqualValues(t, 128, gjson.GetBytes(out, "generationConfig.thinkingConfig.thinkingBudget").Int())
	body := []byte(`{"generationConfig":{"thinkingConfig":{"thinkingBudget":123,"includeThoughts":true}}}`)
	out, err = SetGeminiReasoning(body, "unknown-vendor-model", "high")
	require.NoError(t, err)
	require.Equal(t, string(body), string(out), "unknown models retain native controls, as required by the pinned renderer")
}
