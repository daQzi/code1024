//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type providerPresetCase struct {
	converter, path, target, input, json, events, requestField string
}

func providerPresetCases() []providerPresetCase {
	chat := `{"model":"public-text","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`
	responses := `{"model":"public-text","input":"hello","max_output_tokens":100}`
	upstreamChat := `{"id":"chat-upstream","object":"chat.completion","model":"vendor-text","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`
	upstreamResponses := `{"id":"resp-upstream","object":"response","model":"vendor-text","status":"completed","output":[{"id":"msg-1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":12,"output_tokens":5,"total_tokens":17}}`
	upstreamClaude := `{"id":"msg-upstream","type":"message","role":"assistant","model":"vendor-text","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":5}}`
	upstreamGemini := `{"modelVersion":"vendor-text","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":5,"totalTokenCount":17}}`
	chatEvents := "data: {\"id\":\"chat-upstream\",\"model\":\"vendor-text\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\ndata: {\"id\":\"chat-upstream\",\"model\":\"vendor-text\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5,\"total_tokens\":17}}\n\ndata: [DONE]\n\n"
	responsesEvents := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + upstreamResponses + "}\n\n"
	claudeEvents := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-upstream\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"vendor-text\",\"content\":[],\"usage\":{\"input_tokens\":12,\"output_tokens\":0}}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
	return []providerPresetCase{
		{relayconvert.ConverterOpenAIChatToOpenAIResponses, newAPIProviderChat, "/responses", chat, upstreamResponses, responsesEvents, "input"},
		{relayconvert.ConverterOpenAIChatToClaudeMessages, newAPIProviderChat, "/messages", chat, upstreamClaude, claudeEvents, "messages"},
		{relayconvert.ConverterOpenAIChatToGeminiContent, newAPIProviderChat, "/models/{model}:generateContent", chat, upstreamGemini, "data: " + upstreamGemini + "\n\n", "contents"},
		{relayconvert.ConverterOpenAIResponsesToOpenAIChat, newAPIProviderResponses, "/chat", responses, upstreamChat, chatEvents, "messages"},
		{relayconvert.ConverterOpenAIResponsesToGemini, newAPIProviderResponses, "/models/{model}:generateContent", responses, upstreamGemini, "data: " + upstreamGemini + "\n\n", "contents"},
	}
}

func providerPresetSource(tc providerPresetCase, plugin bool) string {
	converter := tc.converter
	if plugin {
		converter += "+plugin"
	}
	source := fmt.Sprintf(`export const meta={apiVersion:1,key:"preset-fixture",name:"Preset fixture",version:"1.0.0",author:{name:"Fixture"},models:["vendor-text"],
channelConfig:{type:"object",properties:{label:{type:"string",title:"Label",default:"saved"}}},
relay:[{incoming_path:%q,upstream_path:%q,converter:%q,models:["vendor-text"],auth:{type:"header",name:"X-Vendor-Key",value:"{api_key}"}}]};
export function signRequest(ctx,req){return {headers:{"X-Body-Digest":req.bodySha256,"X-Config":ctx.config.label}};}`, tc.path, tc.target, converter)
	if plugin {
		source += fmt.Sprintf(`
export function convertRequest(ctx,req){if(!req.body.%s || req.model!=="vendor-text")throw Error("preset order");return {body:{vendor:req.body},state:{converted:true}};}
export function convertResponse(ctx,resp){if(!ctx.state.converted)throw Error("missing state");return {body:JSON.parse(resp.body).result};}
export function convertStreamEvent(ctx,e){if(!ctx.state.converted)throw Error("missing state");if(e.done)return {};return {events:[{event:e.event,data:JSON.parse(e.data).result}]};}`, tc.requestField)
	}
	return source
}

func wrapPresetEvents(t *testing.T, events string) string {
	t.Helper()
	var body strings.Builder
	for line := range strings.SplitSeq(events, "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			var value any
			if data == "[DONE]" {
				value = data
			} else {
				require.NoError(t, json.Unmarshal([]byte(data), &value))
			}
			raw, err := json.Marshal(map[string]any{"result": value})
			require.NoError(t, err)
			line = "data: " + string(raw)
		}
		body.WriteString(line + "\n")
	}
	return body.String()
}

func TestNewAPIProviderPresetProtocols(t *testing.T) {
	for _, tc := range providerPresetCases() {
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
					require.Equal(t, "public-text", result.Model)
					require.Equal(t, "vendor-text", result.BillingModel)
					require.Equal(t, 12, result.Usage.InputTokens)
					require.Equal(t, 5, result.Usage.OutputTokens)
					assertNewAPIProviderExistingBilling(t, result, account)
					require.Contains(t, recorder.Body.String(), "hello")
					require.Contains(t, recorder.Body.String(), "public-text")
					require.NotContains(t, recorder.Body.String(), "upstream-fixture-key")
					require.Len(t, upstream.requests, 1)
					request := upstream.requests[0]
					sent, err := io.ReadAll(request.Body)
					require.NoError(t, err)
					field := tc.requestField
					if plugin {
						field = "vendor." + field
					}
					require.True(t, gjson.GetBytes(sent, field).Exists(), string(sent))
					digest := sha256.Sum256(sent)
					require.Equal(t, hex.EncodeToString(digest[:]), request.Header.Get("X-Body-Digest"))
					require.Equal(t, "upstream-fixture-key", request.Header.Get("X-Vendor-Key"))
					if strings.Contains(tc.converter, "to_anthropic") {
						require.Equal(t, "2023-06-01", request.Header.Get("Anthropic-Version"))
					}
					if strings.Contains(tc.converter, "to_gemini") && stream {
						require.Contains(t, request.URL.Path, ":streamGenerateContent")
						require.Equal(t, "sse", request.URL.Query().Get("alt"))
					}
				})
			}
		}
	}
}

func TestNewAPIProviderPresetRejectsTruncatedAndOversize(t *testing.T) {
	for _, tc := range providerPresetCases() {
		for _, mode := range []string{"empty", "partial", "read-error", "too-large", "error-json"} {
			t.Run(tc.converter+"/"+mode, func(t *testing.T) {
				s, account, upstream := providerFixture(t, providerPresetSource(tc, false))
				body := strings.TrimSuffix(tc.input, "}") + `,"stream":true}`
				upstream.contentType = "text/event-stream"
				switch mode {
				case "partial":
					upstream.response = "data: {}\n\n"
				case "read-error":
					s.gateway.httpUpstream = &providerChainHTTP{send: func(*http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF))}, nil
					}}
				case "too-large":
					upstream.response = "data: {\"padding\":\"" + strings.Repeat("x", MaxRequestPluginBody) + "\"}\n\n"
				case "error-json":
					body, upstream.response, upstream.contentType = tc.input, `{"error":{"message":"denied"}}`, "application/json"
				}
				c, _ := providerContext(tc.path, body)
				_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), tc.path, "")
				require.Error(t, err)
			})
		}
	}
}

func TestNewAPIProviderPresetFollowupKeepsProtocol(t *testing.T) {
	tc := providerPresetCases()[0]
	source := providerPresetSource(tc, true)
	source = strings.Replace(source, `return {body:JSON.parse(resp.body).result};`, `if(!ctx.state.polled)return {next:{path:"/poll"},state:{converted:true,polled:true}};return {body:JSON.parse(resp.body).result};`, 1)
	s, account, _ := providerFixture(t, source)
	var paths []string
	s.gateway.httpUpstream = &providerChainHTTP{send: func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		body := `{"pending":true}`
		if request.URL.Path == "/prefix/poll" {
			body = `{"result":` + tc.json + `}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}}
	c, recorder := providerContext(tc.path, tc.input)
	result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(tc.input), tc.path, "")
	require.NoError(t, err)
	require.Equal(t, []string{"/prefix/responses", "/prefix/poll"}, paths)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Contains(t, recorder.Body.String(), "hello")
}

func TestNewAPIProviderPresetCancellationAndAdmission(t *testing.T) {
	for _, tc := range providerPresetCases() {
		for _, overload := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/overload=%t", tc.converter, overload), func(t *testing.T) {
				s, account, _ := providerFixture(t, providerPresetSource(tc, false))
				capacity := int64(newAPIProviderMemory)
				if overload {
					capacity--
				}
				budget := pkghttputil.NewBodyMemoryBudget(capacity)
				ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
				defer release()
				ctx, cancel := context.WithCancel(ctx)
				defer cancel()
				entered := make(chan struct{})
				reader, writer := io.Pipe()
				defer reader.Close()
				defer writer.Close()
				s.gateway.httpUpstream = &providerChainHTTP{send: func(*http.Request) (*http.Response, error) {
					close(entered)
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
				}}
				body := strings.TrimSuffix(tc.input, "}") + `,"stream":true}`
				c, recorder := providerContext(tc.path, body)
				c.Request = c.Request.WithContext(ctx)
				type outcome struct {
					result *OpenAIForwardResult
					err    error
				}
				finished := make(chan outcome, 1)
				go func() {
					result, err := s.gateway.forwardNewAPIProvider(ctx, c, account, []byte(body), tc.path, "")
					finished <- outcome{result, err}
				}()
				if !overload {
					select {
					case <-entered:
						cancel()
					case <-time.After(5 * time.Second):
						t.Fatal("preset never reached upstream")
					}
				}
				select {
				case final := <-finished:
					if overload {
						require.ErrorIs(t, final.err, pkghttputil.ErrRequestBodyMemoryBudget)
						require.Equal(t, 503, recorder.Code)
						require.Equal(t, "1", recorder.Header().Get("Retry-After"))
						select {
						case <-entered:
							t.Fatal("capacity rejection reached the upstream")
						default:
						}
					} else if final.err == nil {
						require.NotNil(t, final.result)
						require.True(t, final.result.ClientDisconnect, "partial native usage is not successful delivery")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("preset did not close its actual upstream read after cancellation")
				}
				release()
				other, done := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
				defer done()
				require.NoError(t, pkghttputil.ReserveRequestBodyMemory(other, capacity))
			})
		}
	}
}
