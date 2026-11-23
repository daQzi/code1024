package provider

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestVendorToolsAnthropicCumulativeCountsAndExplicitPrecedence(t *testing.T) {
	p := NewProtocolTools(nil, nil)
	for _, event := range []string{
		`{"type":"message_start","message":{"usage":{"server_tool_use":{"web_search_requests":1,"web_fetch_requests":2}}}}`,
		`{"type":"message_delta","usage":{"server_tool_use":{"web_search_requests":3,"code_execution_requests":4,"tool_search_requests":5}}}`,
		`{"type":"message_delta","usage":{"server_tool_use":{"web_search_requests":3}}}`,
	} {
		require.NoError(t, p.ObserveVendor(VendorAnthropic, []byte(event), true))
	}
	combined, err := p.Combined(nil)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"web_search": 3, "web_fetch": 2, "code_execution": 4, "tool_search": 5}, combined.Counts())
	var explicit ToolUsage
	require.NoError(t, explicit.Add(map[string]int{"web_search": 0}))
	combined, err = p.Combined(&explicit)
	require.NoError(t, err)
	require.Equal(t, 0, combined.Counts()["web_search"])
	require.Equal(t, 2, combined.Counts()["web_fetch"])
	// A new cumulative zero is an actual report, not a missing field.
	require.NoError(t, p.ObserveVendor(VendorAnthropic, []byte(`{"type":"message_delta","usage":{"server_tool_use":{"web_fetch_requests":0}}}`), true))
	combined, err = p.Combined(nil)
	require.NoError(t, err)
	require.Equal(t, 0, combined.Counts()["web_fetch"])
	other := NewProtocolTools(nil, nil)
	combined, err = other.Combined(nil)
	require.NoError(t, err)
	require.Empty(t, combined.Counts(), "vendor counters belong to one logical request")
}

func TestVendorToolsXAIReportsReplacePublicItemsAndDoNotAddSnapshots(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			p := NewProtocolTools(nil, nil)
			require.NoError(t, p.Observe("/v1/responses", []byte(`{"type":"response.output_item.done","output_index":0,"item":{"type":"web_search_call","id":"s1"}}`), true))
			raw := `{"status":"completed","output":[{"type":"web_search_call","id":"s1"}],"usage":{"num_sources_used":99,"server_side_tool_usage_details":{"web_search_calls":2,"x_posts_fetched":5,"x_users_fetched":3}}}`
			if stream {
				raw = `{"type":"response.completed","response":` + raw + `}`
			}
			require.NoError(t, p.Observe("/v1/responses", []byte(raw), stream))
			// The preset tap and public tap may see the same response. Both are
			// snapshots; neither replay nor the final output bills it twice.
			require.NoError(t, p.ObserveVendor(VendorOpenAI, []byte(raw), stream))
			combined, err := p.Combined(nil)
			require.NoError(t, err)
			require.Equal(t, map[string]int{"web_search_preview": 0, "web_search": 2, "x_search_posts": 5, "x_search_profiles": 3}, combined.Counts())
		})
	}
	// Chat-compatible upstreams use the same usage extension on each chunk.
	p := NewProtocolTools(nil, nil)
	for _, count := range []int{2, 3, 3, 0} {
		raw := fmt.Sprintf(`{"choices":[],"usage":{"server_side_tool_usage_details":{"web_search_calls":%d}}}`, count)
		require.NoError(t, p.Observe("/v1/chat/completions", []byte(raw), true))
	}
	combined, err := p.Combined(nil)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"web_search_preview": 0, "web_search": 0}, combined.Counts())
}

func TestVendorToolsRejectInvalidReportsAtomically(t *testing.T) {
	for _, invalid := range []string{`-1`, `1.5`, `10001`, `1e309`, `"2"`, `null`, `true`, `{}`, `[]`} {
		t.Run(invalid, func(t *testing.T) {
			p := NewProtocolTools(nil, nil)
			require.NoError(t, p.ObserveVendor(VendorAnthropic, []byte(`{"type":"message","usage":{"server_tool_use":{"web_search_requests":2}}}`), false))
			raw := `{"type":"message","usage":{"server_tool_use":{"web_search_requests":4,"web_fetch_requests":` + invalid + `}}}`
			require.Error(t, p.ObserveVendor(VendorAnthropic, []byte(raw), false))
			combined, err := p.Combined(nil)
			require.NoError(t, err)
			require.Equal(t, map[string]int{"web_search": 2}, combined.Counts())
		})
	}
	for _, raw := range []string{
		`{"type":"message","usage":{"server_tool_use":{"web_search_requests":1,"web_search_requests":2}}}`,
		`{"type":"message","usage":{"server_tool_use":{},"server_tool_use":{"web_search_requests":2}}}`,
		`{"type":"message","usage":{},"usage":{"server_tool_use":{"web_search_requests":2}}}`,
		`{"type":"message","usage":{"server_tool_use":[]}}`,
		`{"type":"message","usage":{"server_tool_use":2}}`,
	} {
		p := NewProtocolTools(nil, nil)
		require.Error(t, p.ObserveVendor(VendorAnthropic, []byte(raw), false), raw)
		require.Empty(t, p.vendor.Counts())
	}
}

func TestVendorToolsIgnoreErrorsDeclarationsMissingAndUnselectedProtocols(t *testing.T) {
	for _, tc := range []struct {
		protocol VendorProtocol
		raw      string
		stream   bool
	}{
		{VendorAnthropic, `{"type":"error","usage":{"server_tool_use":{"web_search_requests":99}}}`, true},
		{VendorAnthropic, `{"type":"message_delta","error":{"message":"denied"},"usage":{"server_tool_use":{"web_search_requests":99}}}`, true},
		{VendorAnthropic, `{"type":"content_block_start","content_block":{"type":"server_tool_use","name":"web_search"}}`, true},
		{VendorAnthropic, `{"type":"message","usage":{"server_tool_use":null}}`, false},
		{VendorAnthropic, `{"type":"message","usage":{}}`, false},
		{VendorOpenAI, `{"usage":{"num_sources_used":99}}`, false},
		{VendorOpenAI, `{"type":"response.failed","response":{"usage":{"server_side_tool_usage_details":{"web_search_calls":99}}}}`, true},
		{VendorOpenAI, `{"type":"response.completed","response":{"status":"failed","usage":{"server_side_tool_usage_details":{"web_search_calls":99}}}}`, true},
		{VendorOpenAI, `{"type":"response.output_item.done","usage":{"server_side_tool_usage_details":{"web_search_calls":99}}}`, true},
		{VendorOpenAI, `{"type":"message","usage":{"server_tool_use":{"web_search_requests":99}}}`, false},
		{VendorProtocol("unknown"), `{"usage":{"server_side_tool_usage_details":{"web_search_calls":99}}}`, false},
	} {
		p := NewProtocolTools(nil, nil)
		require.NoError(t, p.ObserveVendor(tc.protocol, []byte(tc.raw), tc.stream), tc.raw)
		require.Empty(t, p.vendor.Counts(), tc.raw)
	}
}

func TestVendorToolsAliAndAzureUseOwnKeysAndReplaceBothPublicAliases(t *testing.T) {
	for _, tc := range []struct{ name, field string }{
		{"search_strategy_agent", `"usage":{"x_tools":{"web_search":{"count":2}}}`},
		{"bing_web_search", `"tool_usage":{"web_search":{"num_requests":2}}`},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				p := NewProtocolTools(nil, nil)
				item := `{"type":"web_search_call","id":"s1"}`
				require.NoError(t, p.Observe("/v1/responses", []byte(`{"type":"response.output_item.done","output_index":0,"item":`+item+`}`), true))
				raw := `{"status":"completed","output":[` + item + `],` + tc.field + `}`
				if stream {
					raw = `{"type":"response.completed","response":` + raw + `}`
				}
				for range 2 {
					require.NoError(t, p.ObserveVendor(VendorOpenAI, []byte(raw), stream))
				}
				require.NoError(t, p.Observe("/v1/responses", []byte(raw), stream))
				usage, err := p.Combined(nil)
				require.NoError(t, err)
				require.Equal(t, map[string]int{tc.name: 2, "web_search": 0, "web_search_preview": 0}, usage.Counts())
				charges, err := usage.Charges(func(name string) (float64, bool) { return 10, name == tc.name })
				require.NoError(t, err, "silenced public aliases do not need invented prices")
				require.InDelta(t, 0.02, charges.TotalUSD, 1e-12)
				var explicit ToolUsage
				require.NoError(t, explicit.Add(map[string]int{tc.name: 0}))
				usage, err = p.Combined(&explicit)
				require.NoError(t, err)
				require.Zero(t, usage.Counts()[tc.name])
			})
		}
	}
}

func TestVendorToolsAliAzureRejectConflictsAndBadCountersWithoutPartialCommit(t *testing.T) {
	for _, raw := range []string{
		`{"usage":{"x_tools":{"web_search":{"count":2}}},"tool_usage":{"web_search":{"num_requests":2}}}`,
		`{"usage":{"server_side_tool_usage_details":{"web_search_calls":2},"x_tools":{"web_search":{"count":2}}}}`,
		`{"usage":{"x_tools":{"web_search":{"count":1.5}}}}`,
		`{"usage":{"x_tools":{"web_search":{"count":2,"count":3}}}}`,
		`{"usage":{"x_tools":{"web_search":{"count":null}}}}`,
		`{"usage":{"x_tools":[]}}`,
		`{"tool_usage":{"web_search":{"num_requests":"2"}}}`,
		`{"tool_usage":{"web_search":{"num_requests":-1}}}`,
		`{"tool_usage":{"web_search":{"num_requests":10001}}}`,
		`{"tool_usage":{"web_search":{"num_requests":1e309}}}`,
	} {
		p := NewProtocolTools(nil, nil)
		require.Error(t, p.ObserveVendor(VendorOpenAI, []byte(raw), false), raw)
		require.Empty(t, p.vendor.Counts(), "invalid report cannot apply only its other fields")
	}
	for _, field := range []string{`"usage":{"x_tools":{"web_search":{"count":0}}}`, `"tool_usage":{"web_search":{"num_requests":0}}`} {
		p := NewProtocolTools(nil, nil)
		require.NoError(t, p.ObserveVendor(VendorOpenAI, []byte(`{`+field+`}`), false))
		require.Len(t, p.vendor.Counts(), 3, "reported zero retains its own key and both aliases")
	}
	p := NewProtocolTools(nil, nil)
	require.NoError(t, p.ObserveVendor(VendorOpenAI, []byte(`{"usage":{"x_tools":{"web_search":{"count":2}}}}`), false))
	require.Error(t, p.ObserveVendor(VendorOpenAI, []byte(`{"tool_usage":{"web_search":{"num_requests":2}}}`), false))
	require.Equal(t, map[string]int{"search_strategy_agent": 2, "web_search": 0, "web_search_preview": 0}, p.vendor.Counts(), "changing a vendor metric mid-request cannot charge both search families")
}

func TestVendorToolsGLMAndDashScopeRequestAndResponseAccounting(t *testing.T) {
	// GLM: default search_std, explicit search_pro, forced search_intent:false, non-stream web_search array dedup.
	glmForced := []byte(`{"model":"glm-4","tools":[{"type":"web_search","web_search":{"enable":true,"search_engine":"search_pro","search_intent":false}}]}`)
	p := NewProtocolTools(glmForced, nil)
	usage, err := p.Combined(nil)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"search_pro": 1}, usage.Counts(), "forced GLM stream without array still bills 1")

	require.NoError(t, p.Observe("/v1/chat/completions", []byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"web_search":[{"title":"r1"},{"title":"r2"}]}`), false))
	usage, err = p.Combined(nil)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"search_pro": 1, "web_search": 0, "web_search_preview": 0}, usage.Counts(), "non-stream web_search array confirms the same single call without double billing")

	// GLM disabled does not bill or set glmSearchKey.
	glmDisabled := []byte(`{"model":"glm-4","tools":[{"type":"web_search","web_search":{"enable":false,"search_engine":"search_pro"}}]}`)
	pDisabled := NewProtocolTools(glmDisabled, nil)
	require.NoError(t, pDisabled.Observe("/v1/chat/completions", []byte(`{"choices":[{"message":{"content":"ok"}}],"web_search":[{"title":"r1"}]}`), false))
	usage, err = pDisabled.Combined(nil)
	require.NoError(t, err)
	require.Empty(t, usage.Counts())

	// GLM parser validation rejects unknown search_engine or non-bool enable/search_intent.
	for _, raw := range []string{
		`{"type":"web_search","web_search":"bad"}`,
		`{"type":"web_search","web_search":{"enable":"yes"}}`,
		`{"type":"web_search","web_search":{"enable":true,"search_engine":"unknown_engine"}}`,
		`{"type":"web_search","web_search":{"enable":true,"search_intent":"no"}}`,
	} {
		_, _, _, ok := ParseGLMSearchEngine(gjson.Parse(raw))
		require.False(t, ok, raw)
	}

	// DashScope: forced_search bills configured strategy when no x_tools count is returned; explicit x_tools count supersedes it.
	dashForced := []byte(`{"model":"qwen-plus","enable_search":true,"search_options":{"search_strategy":"max","forced_search":true}}`)
	pDash := NewProtocolTools(dashForced, nil)
	usage, err = pDash.Combined(nil)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"search_strategy_max": 1}, usage.Counts())
	require.NoError(t, pDash.Observe("/v1/chat/completions", []byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"x_tools":{"web_search":{"count":2}}}}`), false))
	usage, err = pDash.Combined(nil)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"search_strategy_agent": 2, "web_search": 0, "web_search_preview": 0}, usage.Counts())

	// DashScope parser validation rejects unknown search_strategy or bad field types.
	for _, raw := range []string{
		`{"enable_search":"yes"}`,
		`{"enable_search":true,"search_options":"bad"}`,
		`{"enable_search":true,"search_options":{"search_strategy":"custom"}}`,
		`{"enable_search":true,"search_options":{"forced_search":"true"}}`,
	} {
		_, _, _, ok := ParseDashScopeSearchStrategy([]byte(raw))
		require.False(t, ok, raw)
	}
}

func TestProtocolToolsAnthropicMessagesAndUpstreamResponsesHostedTools(t *testing.T) {
	book, err := NewPriceBook(map[string]float64{"web_search": 10, "web_fetch": 2, "file_search": 2.5, "custom_fn": 3})
	require.NoError(t, err)
	lookup := func(name string) (float64, bool) { return book.Lookup(name, "claude-sonnet-4-5") }

	// Direct /v1/messages JSON: server_tool_use + priced tool_use block.
	p := NewProtocolTools([]byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"}]}`), lookup)
	require.NoError(t, p.Observe("/v1/messages", []byte(`{"type":"message","content":[{"type":"tool_use","id":"tu_1","name":"custom_fn"},{"type":"tool_use","id":"tu_2","name":"web_search"}],"usage":{"server_tool_use":{"web_search_requests":2,"web_fetch_requests":1}}}`), false))
	usage, err := p.Combined(nil)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"web_search": 2, "web_fetch": 1, "custom_fn": 1}, usage.Counts())

	// Direct /v1/messages SSE: message_start + content_block_start + message_delta + message_stop.
	pStream := NewProtocolTools([]byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"}]}`), lookup)
	for _, chunk := range []string{
		`{"type":"message_start","message":{"usage":{"server_tool_use":{"web_search_requests":1}}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_1","name":"custom_fn"}}`,
		`{"type":"message_delta","usage":{"server_tool_use":{"web_search_requests":2,"web_fetch_requests":1}}}`,
		`{"type":"message_stop"}`,
	} {
		require.NoError(t, pStream.Observe("/v1/messages", []byte(chunk), true))
	}
	usage, err = pStream.Combined(nil)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"web_search": 2, "web_fetch": 1, "custom_fn": 1}, usage.Counts())

	// Upstream Responses hosted tools observed before conversion to Chat/Claude.
	pUp := NewProtocolTools([]byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"}]}`), lookup)
	require.NoError(t, pUp.ObserveUpstreamResponsesHostedTools([]byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"ws_1","type":"web_search_call"}}`), true))
	require.NoError(t, pUp.ObserveUpstreamResponsesHostedTools([]byte(`{"type":"response.completed","response":{"status":"completed","output":[{"id":"ws_1","type":"web_search_call"},{"id":"fs_1","type":"file_search_call"}]}}`), true))
	usage, err = pUp.Combined(nil)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"web_search": 1, "file_search": 1}, usage.Counts())
}
