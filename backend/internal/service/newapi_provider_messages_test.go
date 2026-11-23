//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNewAPIProviderMessagesDirectJSONStreamErrorsAndModifier(t *testing.T) {
	source := `export const meta={apiVersion:1,key:"msg-direct",name:"Messages direct",version:"1.0.0",author:{name:"Fixture"},models:["vendor-text","claude-sonnet-4-5"],
relay:[{incoming_path:"/v1/messages",upstream_path:"/v1/messages",converter:"none",models:["vendor-text","claude-sonnet-4-5"],auth:{type:"header",name:"x-api-key",value:"{api_key}"}}]};`

	upstreamJSON := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":14,"output_tokens":6}}`
	upstreamSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"usage\":{\"input_tokens\":14,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":6}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			s, account, upstream := providerFixture(t, source)
			account.Credentials["model_mapping"] = map[string]any{"public-claude": "claude-sonnet-4-5"}
			require.NoError(t, s.NormalizeAccountBinding(context.Background(), account, nil))
			require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMessages))
			require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))

			body := `{"model":"public-claude@thinking:2048","max_tokens":1024,"messages":[{"role":"user","content":"hi"}]}`
			upstream.response, upstream.contentType = upstreamJSON, "application/json"
			if stream {
				body = strings.TrimSuffix(body, "}") + `,"stream":true}`
				upstream.response, upstream.contentType = upstreamSSE, "text/event-stream"
			}
			c, recorder := providerContext(newAPIProviderMessages, body)
			result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderMessages, "")
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			require.Equal(t, stream, result.Stream)
			require.Equal(t, 14, result.Usage.InputTokens)
			require.Equal(t, 6, result.Usage.OutputTokens)
			require.Equal(t, "claude-sonnet-4-5@thinking:on", result.BillingModel)
			require.Contains(t, recorder.Body.String(), "hello")
			require.Len(t, upstream.requests, 1)
			req := upstream.requests[0]
			require.Equal(t, "2023-06-01", req.Header.Get("Anthropic-Version"))
			sent, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, "claude-sonnet-4-5", gjson.GetBytes(sent, "model").String())
			require.Equal(t, "enabled", gjson.GetBytes(sent, "thinking.type").String())
			require.EqualValues(t, 2048, gjson.GetBytes(sent, "thinking.budget_tokens").Int())
			require.Greater(t, gjson.GetBytes(sent, "max_tokens").Int(), int64(2048))
		})
	}

	// Local rejection on /v1/messages emits Anthropic error shape.
	s, account, upstream := providerFixture(t, source)
	account.Credentials["model_mapping"] = map[string]any{"public-claude": "claude-sonnet-4-5"}
	require.NoError(t, s.NormalizeAccountBinding(context.Background(), account, nil))
	c, recorder := providerContext(newAPIProviderMessages, `{"model":"public-claude@effort:bogus","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(`{"model":"public-claude@effort:bogus","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`), newAPIProviderMessages, "")
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "error", gjson.GetBytes(recorder.Body.Bytes(), "type").String())
	require.Equal(t, "invalid_request_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
	require.Empty(t, upstream.requests)
}

func TestNewAPIProviderMessagesPresetProtocols(t *testing.T) {
	claudeInput := `{"model":"public-text","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`
	upstreamChat := `{"id":"chat-upstream","object":"chat.completion","model":"vendor-text","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`
	upstreamResponses := `{"id":"resp-upstream","object":"response","model":"vendor-text","status":"completed","output":[{"id":"msg-1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":12,"output_tokens":5,"total_tokens":17}}`
	chatEvents := "data: {\"id\":\"chat-upstream\",\"model\":\"vendor-text\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\ndata: {\"id\":\"chat-upstream\",\"model\":\"vendor-text\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5,\"total_tokens\":17}}\n\ndata: [DONE]\n\n"
	responsesEvents := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + upstreamResponses + "}\n\n"

	cases := []providerPresetCase{
		{relayconvert.ConverterClaudeMessagesToOpenAIChat, newAPIProviderMessages, "/chat", claudeInput, upstreamChat, chatEvents, "messages"},
		{relayconvert.ConverterClaudeMessagesToOpenAIResponses, newAPIProviderMessages, "/responses", claudeInput, upstreamResponses, responsesEvents, "input"},
	}
	for _, tc := range cases {
		for _, plugin := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/plugin=%t/stream=%t", tc.converter, plugin, stream), func(t *testing.T) {
					s, account, upstream := providerFixture(t, providerPresetSource(tc, plugin))
					body := tc.input
					upstream.response, upstream.contentType = tc.json, "application/json"
					if plugin {
						upstream.response = `{"result":` + tc.json + `}`
					}
					if stream {
						body = strings.TrimSuffix(body, "}") + `,"stream":true}`
						upstream.response, upstream.contentType = tc.events, "text/event-stream"
						if plugin {
							upstream.response = wrapPresetEvents(t, tc.events)
						}
					}
					c, recorder := providerContext(tc.path, body)
					result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), tc.path, "")
					require.NoError(t, err, recorder.Body.String())
					require.NotNil(t, result)
					require.Equal(t, stream, result.Stream)
					require.Equal(t, 12, result.Usage.InputTokens)
					require.Equal(t, 5, result.Usage.OutputTokens)
					require.Contains(t, recorder.Body.String(), "hello")
					require.Len(t, upstream.requests, 1)
				})
			}
		}
	}
}

func TestNewAPIProviderGLMDashScopeAndAnthropicHostedTools(t *testing.T) {
	savePrices := func(t *testing.T, s *NewAPIPluginService, prices map[string]float64) {
		t.Helper()
		repo := &providerToolPriceRepo{}
		s.marketSettings = repo
		cur, err := s.ProviderToolPrices(context.Background())
		require.NoError(t, err)
		_, err = s.SaveProviderToolPrices(context.Background(), ProviderToolPriceSettings{Prices: prices, Revision: cur.Revision})
		require.NoError(t, err)
	}

	t.Run("glm search admission and billing", func(t *testing.T) {
		source := `export const meta={apiVersion:1,key:"glm-tools",name:"GLM tools",version:"1.0.0",author:{name:"Fixture"},models:["vendor-text"],
relay:[{incoming_path:"/v1/chat/completions",upstream_path:"/chat",converter:"none",models:["vendor-text"]}]};`
		s, account, upstream := providerFixture(t, source)
		body := `{"model":"public-text","messages":[{"role":"user","content":"news"}],"tools":[{"type":"web_search","web_search":{"enable":true,"search_engine":"search_pro","search_intent":false}}]}`

		// Without explicit search_pro price -> local 400 before upstream.
		c, rec := providerContext(newAPIProviderChat, body)
		_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Empty(t, upstream.requests)

		// With explicit search_pro price ($5 / 1k) -> bills 1 call (deduped with non-stream web_search array).
		savePrices(t, s, map[string]float64{"search_pro": 5})
		upstream.response = `{"id":"c1","object":"chat.completion","model":"vendor-text","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5},"web_search":[{"title":"hit1"}]}`
		upstream.contentType = "application/json"
		c, rec = providerContext(newAPIProviderChat, body)
		res, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
		require.NoError(t, err, rec.Body.String())
		require.NotNil(t, res.providerToolCharges)
		require.InDelta(t, 0.005, res.providerToolCharges.TotalUSD, 1e-12)
	})

	t.Run("dashscope search admission and billing", func(t *testing.T) {
		source := `export const meta={apiVersion:1,key:"ali-tools",name:"Ali tools",version:"1.0.0",author:{name:"Fixture"},models:["vendor-text"],
relay:[{incoming_path:"/v1/chat/completions",upstream_path:"/chat",converter:"none",models:["vendor-text"]}]};`
		s, account, upstream := providerFixture(t, source)
		body := `{"model":"public-text","messages":[{"role":"user","content":"news"}],"enable_search":true,"search_options":{"search_strategy":"max","forced_search":true}}`

		// Unpriced -> local 400.
		c, rec := providerContext(newAPIProviderChat, body)
		_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Empty(t, upstream.requests)

		// Priced ($8 / 1k) -> bills 1 forced search_strategy_max call.
		savePrices(t, s, map[string]float64{"search_strategy_max": 8})
		upstream.response = `{"id":"c2","object":"chat.completion","model":"vendor-text","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`
		upstream.contentType = "application/json"
		c, rec = providerContext(newAPIProviderChat, body)
		res, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
		require.NoError(t, err, rec.Body.String())
		require.NotNil(t, res.providerToolCharges)
		require.InDelta(t, 0.008, res.providerToolCharges.TotalUSD, 1e-12)
	})

	t.Run("anthropic messages preset to responses hosted web_search_call billing", func(t *testing.T) {
		source := `export const meta={apiVersion:1,key:"msg-resp-tools",name:"Msg Resp tools",version:"1.0.0",author:{name:"Fixture"},models:["vendor-text"],
relay:[{incoming_path:"/v1/messages",upstream_path:"/responses",converter:"claude_messages_to_openai_responses",models:["vendor-text"]}]};`
		s, account, upstream := providerFixture(t, source)
		savePrices(t, s, map[string]float64{"web_search": 10})
		body := `{"model":"public-text","max_tokens":100,"messages":[{"role":"user","content":"news"}],"tools":[{"type":"web_search_20250305","name":"web_search"}]}`
		upstream.response = `{"id":"r1","object":"response","model":"vendor-text","status":"completed","output":[{"id":"ws1","type":"web_search_call","status":"completed"},{"id":"m1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`
		upstream.contentType = "application/json"
		c, rec := providerContext(newAPIProviderMessages, body)
		res, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderMessages, "")
		require.NoError(t, err, rec.Body.String())
		require.Equal(t, 1, res.SearchCount)
		require.NotNil(t, res.providerToolCharges)
		require.InDelta(t, 0.01, res.providerToolCharges.TotalUSD, 1e-12)
	})
}
