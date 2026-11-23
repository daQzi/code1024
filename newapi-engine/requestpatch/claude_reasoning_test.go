package requestpatch

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeReasoningPreservesBusinessJSON(t *testing.T) {
	body := []byte(`{"model":"public","max_tokens":4096,"thinking":{"type":"enabled","budget_tokens":1024,"display":"omitted"},"output_config":{"format":{"type":"json_schema","schema":{"z":1,"a":2}}},"vendor":12345678901234567890,"temperature":0.4}`)
	out, err := SetClaudeReasoning(body, "claude-sonnet-4-6", "high")
	require.NoError(t, err)
	require.Equal(t, "adaptive", gjson.GetBytes(out, "thinking.type").String())
	require.False(t, gjson.GetBytes(out, "thinking.budget_tokens").Exists())
	require.Equal(t, "omitted", gjson.GetBytes(out, "thinking.display").String())
	require.Equal(t, "high", gjson.GetBytes(out, "output_config.effort").String())
	require.Equal(t, `{"type":"json_schema","schema":{"z":1,"a":2}}`, gjson.GetBytes(out, "output_config.format").Raw)
	require.Equal(t, "12345678901234567890", gjson.GetBytes(out, "vendor").Raw)
	require.Equal(t, "public", gjson.GetBytes(out, "model").String())
	require.Contains(t, string(body), `"budget_tokens":1024`)
	effort, explicit := ClaudeReasoningEffort(out)
	require.True(t, explicit)
	require.Equal(t, "high", effort)
}

func TestClaudeReasoningManualBudgetAndSampling(t *testing.T) {
	out, err := SetClaudeReasoning([]byte(`{"max_tokens":16,"temperature":0.4,"top_p":0.9,"top_k":3}`), "claude-sonnet-4", "high")
	require.NoError(t, err)
	require.Equal(t, "enabled", gjson.GetBytes(out, "thinking.type").String())
	require.GreaterOrEqual(t, gjson.GetBytes(out, "thinking.budget_tokens").Int(), int64(1024))
	require.Greater(t, gjson.GetBytes(out, "max_tokens").Int(), gjson.GetBytes(out, "thinking.budget_tokens").Int())
	require.EqualValues(t, 1, gjson.GetBytes(out, "temperature").Float())
	require.False(t, gjson.GetBytes(out, "top_p").Exists())
	require.False(t, gjson.GetBytes(out, "top_k").Exists())
	for _, tc := range []struct {
		body, effort string
		explicit     bool
	}{
		{`{}`, "", false}, {`{"thinking":{"type":"disabled"}}`, "none", true},
		{`{"thinking":{"type":"adaptive"}}`, "high", true}, {`{"thinking":{"budget_tokens":-1}}`, "high", true},
		{`{"thinking":{"budget_tokens":"invalid"}}`, "", true}, {`{"output_config":{"effort":null}}`, "", true},
	} {
		effort, explicit := ClaudeReasoningEffort([]byte(tc.body))
		require.Equal(t, tc.effort, effort, tc.body)
		require.Equal(t, tc.explicit, explicit, tc.body)
	}
	for _, body := range []string{`[]`, `{"thinking":"invalid"}`, `{"output_config":false}`, `{"max_tokens":-1}`, `{"max_tokens":2.5}`, `{"Thinking":{"type":"disabled"}}`, `{"thinking":{"Budget_Tokens":1024}}`, `{"output_config":{"Effort":"low"}}`} {
		_, err := SetClaudeReasoning([]byte(body), "claude-sonnet-4", "high")
		require.Error(t, err, body)
	}
}
