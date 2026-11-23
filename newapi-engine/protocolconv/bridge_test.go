package protocolconv

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const chatRequest = `{"model":"vendor-text","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`
const responsesRequest = `{"model":"vendor-text","input":"hello","max_output_tokens":100}`
const claudeRequest = `{"model":"vendor-text","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`
const geminiRequest = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":100}}`

func TestBridgeModelModifierRetainsExactThinkingBudget(t *testing.T) {
	parsed, err := modelmodifier.Resolve("public@thinking:2048", "claude-sonnet-4-5")
	require.NoError(t, err)
	ctx := parsed.Context(context.Background())
	_, raw, err := New(ctx, relayconvert.ConverterOpenAIChatToClaudeMessages, parsed.Base, []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}],"max_tokens":8192}`), false)
	require.NoError(t, err)
	require.Equal(t, "enabled", gjson.GetBytes(raw, "thinking.type").String())
	require.EqualValues(t, 2048, gjson.GetBytes(raw, "thinking.budget_tokens").Int())
	require.False(t, gjson.GetBytes(raw, "ReasoningConversion").Exists())
}

func TestBridgeRequestProtocols(t *testing.T) {
	for _, tc := range []struct{ converter, request, field string }{
		{relayconvert.ConverterOpenAIChatToOpenAIResponses, chatRequest, "input.0.content"},
		{relayconvert.ConverterOpenAIChatToClaudeMessages, chatRequest, "messages.0.content"},
		{relayconvert.ConverterOpenAIChatToGeminiContent, chatRequest, "contents.0.parts.0.text"},
		{relayconvert.ConverterOpenAIResponsesToOpenAIChat, responsesRequest, "messages.0.content"},
		{relayconvert.ConverterOpenAIResponsesToClaudeMessages, responsesRequest, "messages.0.content"},
		{relayconvert.ConverterOpenAIResponsesToGemini, responsesRequest, "contents.0.parts.0.text"},
		{relayconvert.ConverterClaudeMessagesToOpenAIChat, claudeRequest, "messages.0.content"},
		{relayconvert.ConverterClaudeMessagesToOpenAIResponses, claudeRequest, "input.0.content"},
		{ConverterClaudeMessagesToGeminiContent, claudeRequest, "contents.0.parts.0.text"},
		{relayconvert.ConverterGeminiContentToOpenAIChat, geminiRequest, "messages.0.content"},
		{relayconvert.ConverterGeminiContentToOpenAIResponses, geminiRequest, "input.0.content"},
		{ConverterGeminiContentToClaudeMessages, geminiRequest, "messages.0.content"},
	} {
		t.Run(tc.converter, func(t *testing.T) {
			require.True(t, Supported(tc.converter))
			_, raw, err := New(context.Background(), tc.converter, "vendor-text", []byte(tc.request), false)
			require.NoError(t, err)
			require.Contains(t, gjson.GetBytes(raw, tc.field).String(), "hello")
		})
	}
}

func TestBridgeAllTwelveConvertersJSONAndStream(t *testing.T) {
	upstreamChat := `{"id":"chat-1","object":"chat.completion","model":"vendor-text","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`
	upstreamResponses := `{"id":"resp-1","object":"response","model":"vendor-text","status":"completed","output":[{"id":"msg-1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":12,"output_tokens":5,"total_tokens":17}}`
	upstreamClaude := `{"id":"msg-1","type":"message","role":"assistant","model":"vendor-text","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":5}}`
	upstreamGemini := `{"modelVersion":"vendor-text","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":5,"totalTokenCount":17}}`

	chatStream := []string{
		`{"id":"chat-1","model":"vendor-text","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"}}]}`,
		`{"id":"chat-1","model":"vendor-text","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`,
	}
	responsesStream := []string{
		`{"type":"response.output_text.delta","delta":"hello"}`,
		`{"type":"response.completed","response":` + upstreamResponses + `}`,
	}
	claudeStream := []string{
		`{"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","model":"vendor-text","content":[],"usage":{"input_tokens":12,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	}
	geminiStream := []string{upstreamGemini}

	cases := []struct {
		converter  string
		req        string
		respJSON   string
		respStream []string
		wantDone   bool
		wantNamed  bool
	}{
		{relayconvert.ConverterOpenAIChatToOpenAIResponses, chatRequest, upstreamResponses, responsesStream, true, false},
		{relayconvert.ConverterOpenAIChatToClaudeMessages, chatRequest, upstreamClaude, claudeStream, true, false},
		{relayconvert.ConverterOpenAIChatToGeminiContent, chatRequest, upstreamGemini, geminiStream, true, false},
		{relayconvert.ConverterOpenAIResponsesToOpenAIChat, responsesRequest, upstreamChat, chatStream, false, true},
		{relayconvert.ConverterOpenAIResponsesToClaudeMessages, responsesRequest, upstreamClaude, claudeStream, false, true},
		{relayconvert.ConverterOpenAIResponsesToGemini, responsesRequest, upstreamGemini, geminiStream, false, true},
		{relayconvert.ConverterClaudeMessagesToOpenAIChat, claudeRequest, upstreamChat, chatStream, false, true},
		{relayconvert.ConverterClaudeMessagesToOpenAIResponses, claudeRequest, upstreamResponses, responsesStream, false, true},
		{ConverterClaudeMessagesToGeminiContent, claudeRequest, upstreamGemini, geminiStream, false, true},
		{relayconvert.ConverterGeminiContentToOpenAIChat, geminiRequest, upstreamChat, chatStream, false, false},
		{relayconvert.ConverterGeminiContentToOpenAIResponses, geminiRequest, upstreamResponses, responsesStream, false, false},
		{ConverterGeminiContentToClaudeMessages, geminiRequest, upstreamClaude, claudeStream, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.converter, func(t *testing.T) {
			bridge, _, err := New(context.Background(), tc.converter, "vendor-text", []byte(tc.req), false)
			require.NoError(t, err)
			out, err := bridge.JSON(context.Background(), []byte(tc.respJSON))
			require.NoError(t, err)
			require.Contains(t, string(out), "hello")

			sBridge, reqRaw, err := New(context.Background(), tc.converter, "vendor-text", []byte(tc.req), true)
			require.NoError(t, err)
			if sBridge.To() == "openai" {
				require.True(t, gjson.GetBytes(reqRaw, "stream").Bool())
				require.True(t, gjson.GetBytes(reqRaw, "stream_options.include_usage").Bool())
			}
			var all []Event
			for _, chunk := range tc.respStream {
				ev, err := sBridge.Stream(context.Background(), chunk, false)
				require.NoError(t, err)
				all = append(all, ev...)
			}
			doneEv, err := sBridge.Stream(context.Background(), "", true)
			require.NoError(t, err)
			all = append(all, doneEv...)
			require.NotEmpty(t, all)
			var joined strings.Builder
			sawDone := false
			for _, e := range all {
				if e.Data == "[DONE]" {
					sawDone = true
					continue
				}
				joined.WriteString(e.Data)
				if tc.wantNamed {
					require.NotEmpty(t, e.Name)
				}
			}
			require.Equal(t, tc.wantDone, sawDone)
			require.Contains(t, joined.String(), "hello")
		})
	}
}

func TestBridgeRejectsUnsafeOrIncompleteInputs(t *testing.T) {
	for _, raw := range []string{
		`{"model":"vendor-text","model":"other"}`,
		`{"model":"vendor-text","messages":[],"padding":"` + strings.Repeat("x", MaxBody) + `"}`,
		`{"model":"vendor-text","messages":[],"items":[` + strings.Repeat("0,", MaxNodes) + `0]}`,
		`{"model":"vendor-text","messages":[],"items":` + strings.Repeat("[", MaxDepth+1) + `0` + strings.Repeat("]", MaxDepth+1) + `}`,
		`{"model":"vendor-text","messages":[{"role":"user","content":"` + string([]byte{0xff}) + `"}]}`,
	} {
		_, _, err := New(context.Background(), relayconvert.ConverterOpenAIChatToOpenAIResponses, "vendor-text", []byte(raw), false)
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := New(ctx, relayconvert.ConverterOpenAIChatToOpenAIResponses, "vendor-text", []byte(chatRequest), false)
	require.ErrorIs(t, err, context.Canceled)
	// Never guess a token limit for a Claude target. Match the shared codec's
	// explicit requirement when no host default has been configured.
	_, _, err = New(context.Background(), relayconvert.ConverterOpenAIChatToClaudeMessages, "vendor-text", []byte(`{"model":"vendor-text","messages":[]}`), false)
	require.Error(t, err)
	for _, raw := range []string{`{}`, `{"status":"in_progress","output":[]}`, `{"status":"completed","output":[],"error":{"message":"failure"}}`} {
		bridge, _, err := New(context.Background(), relayconvert.ConverterOpenAIChatToOpenAIResponses, "vendor-text", []byte(chatRequest), false)
		require.NoError(t, err)
		_, err = bridge.JSON(context.Background(), []byte(raw))
		require.Error(t, err)
	}
}

func TestBridgeStreamRequiresRealTerminal(t *testing.T) {
	for _, converter := range []string{relayconvert.ConverterOpenAIResponsesToOpenAIChat, relayconvert.ConverterOpenAIChatToOpenAIResponses, relayconvert.ConverterOpenAIChatToClaudeMessages, relayconvert.ConverterOpenAIChatToGeminiContent} {
		t.Run(converter, func(t *testing.T) {
			request := chatRequest
			if converter == relayconvert.ConverterOpenAIResponsesToOpenAIChat {
				request = responsesRequest
			}
			bridge, _, err := New(context.Background(), converter, "vendor-text", []byte(request), true)
			require.NoError(t, err)
			_, err = bridge.Stream(context.Background(), "", true)
			require.Error(t, err)
		})
	}
	bridge, _, err := New(context.Background(), relayconvert.ConverterOpenAIResponsesToOpenAIChat, "vendor-text", []byte(responsesRequest), true)
	require.NoError(t, err)
	_, err = bridge.Stream(context.Background(), "[DONE]", false)
	require.Error(t, err)
}

func TestBridgeResponsesCustomToolRoundTrip(t *testing.T) {
	request := `{"model":"vendor-text","input":"hello","tools":[{"type":"custom","name":"code","description":"Run code"}]}`
	bridge, raw, err := New(context.Background(), relayconvert.ConverterOpenAIResponsesToOpenAIChat, "vendor-text", []byte(request), false)
	require.NoError(t, err)
	require.Equal(t, "function", gjson.GetBytes(raw, "tools.0.type").String())
	response := `{"id":"chat-tool","model":"vendor-text","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"code","arguments":"{\"input\":\"print(1)\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`
	raw, err = bridge.JSON(context.Background(), []byte(response))
	require.NoError(t, err)
	require.Equal(t, "custom_tool_call", gjson.GetBytes(raw, "output.0.type").String())
	require.Equal(t, "print(1)", gjson.GetBytes(raw, "output.0.input").String())
}

func TestBridgeStreamStateBoundAndCancel(t *testing.T) {
	bridge, _, err := New(context.Background(), relayconvert.ConverterOpenAIResponsesToOpenAIChat, "vendor-text", []byte(responsesRequest), true)
	require.NoError(t, err)
	chunk := `{"choices":[{"index":0,"delta":{"content":"` + strings.Repeat("x", 8000) + `"}}]}`
	for i := 0; i < MaxBody/len(chunk); i++ {
		_, err := bridge.Stream(context.Background(), chunk, false)
		require.NoError(t, err)
	}
	_, err = bridge.Stream(context.Background(), chunk, false)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = bridge.Stream(ctx, "", true)
	require.ErrorIs(t, err, context.Canceled)
}

func BenchmarkBridgeRequest(b *testing.B) {
	for _, size := range []int{64 << 10, 512 << 10} {
		body := []byte(`{"model":"vendor-text","messages":[{"role":"user","content":"` + strings.Repeat("x", size) + `"}]}`)
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, _, err := New(context.Background(), relayconvert.ConverterOpenAIChatToOpenAIResponses, "vendor-text", body, false); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
