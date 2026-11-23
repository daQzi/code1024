//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNativeProtocolReportActualOutputAndCommittedBilling(t *testing.T) {
	for _, protocol := range []string{"claude", "gemini", "gemini-messages", "antigravity-claude", "antigravity-gemini"} {
		for _, scope := range []string{"global", "group", "account"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", protocol, scope, stream), func(t *testing.T) {
					source := strings.Replace(globalHTTPReportSource, `["openai","openai_responses"]`, `["openai","openai_responses","claude","gemini"]`, 1)
					source += `
export function reply(ctx,res){
 if(!["claude","gemini"].includes(ctx.format)||ctx.stage!=="reply"||!ctx.channel)throw Error("reply identity");
 const mask=[];
 for(const output of res.outputs){const start=output.text.indexOf("secret");if(start>=0)mask.push({index:output.index,start,end:start+6});}
 return {action:"continue",mask,hold:res.stream?8:0};
}`
					stages := []string{"reply", "report"}
					if protocol == "claude" {
						stages = []string{"request", "outbound", "egress", "reply", "report"}
						source += `
export function request(){return {action:"continue",effects:[{type:"reasoning.set",effort:"high",override:true}]};}
export function outbound(ctx,req){if(ctx.format!=="claude")throw Error("wrong format");return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/policy_checked",value:true}]}]};}
export function egress(ctx,req){if(!req.body.policy_checked)throw Error("missing policy");return {action:"continue"};}`
					} else {
						stages = []string{"request", "outbound", "egress", "reply", "report"}
						source += `
export function request(){return {action:"continue"};}
export function outbound(ctx,req){if(ctx.format!=="gemini"||!req.body.contents||req.headers?.Authorization)throw Error("wrong native wire");return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/policy_checked",value:true}]}]};}
export function egress(ctx,req){if(!req.body.policy_checked)throw Error("missing Gemini policy");return {action:"continue"};}`
					}
					s, _, _, plan := providerPolicyFixture(t, providerFixtureSource, source, stages, newAPIProviderChat)
					h := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 4)}
					s.gateway.httpUpstream = &relayReportHTTP{reports: h}
					s.taskReports = newTaskReportExecutor(s)
					t.Cleanup(s.taskReports.close)
					plan.call.Format, plan.call.Path, plan.call.Model = "claude", "/v1/messages", "claude-sonnet-4"
					account := newAnthropicOAuthAccountForPartialUsageTest()
					body := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4","max_tokens":16,"stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream))
					if protocol == "gemini" || protocol == "antigravity-gemini" {
						plan.call.Format, plan.call.Path, plan.call.Model = "gemini", "/v1beta/models/gemini-2.5-flash:generateContent", "gemini-2.5-flash"
						body = geminiSignalTestRequest()
					}
					if protocol != "claude" {
						account = geminiSignalTestAccount()
					}
					if strings.HasPrefix(protocol, "antigravity-") {
						account = newAntigravityCompatAccount(AccountTypeOAuth)
						account.Credentials["model_mapping"] = map[string]any{plan.call.Model: plan.call.Model}
					}
					plan.items[0].Scope = scope
					if scope == "account" {
						plan.items[0].AccountIDs = []int64{account.ID}
					} else {
						plan.items[0].AccountIDs = nil
					}
					if scope == "group" {
						plan.items[0].GroupIDs = []int64{7}
					}
					c, recorder := providerContext(plan.call.Path, string(body))
					ctx := WithRequestPluginPlan(c.Request.Context(), plan)
					c.Request = c.Request.WithContext(ctx)
					r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "native-user")
					require.NotNil(t, r)
					c.Writer = r
					originalRequest := c.Request
					var result *ForwardResult
					var err error
					contentType := "application/json"
					if stream {
						contentType = "text/event-stream"
					}
					if protocol == "claude" {
						response := `{"id":"msg-report","type":"message","role":"assistant","model":"claude-sonnet-4","content":[{"type":"text","text":"hello secret"}],"stop_reason":"end_turn","usage":{"input_tokens":13,"output_tokens":4,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}}`
						if stream {
							response = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-report\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":13,\"cache_read_input_tokens\":3,\"cache_creation_input_tokens\":2}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello secret\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":4}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
						}
						upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}, "X-Request-Id": []string{"native-report"}}, Body: io.NopCloser(strings.NewReader(response))}}
						gateway := newForwardPartialUsageServiceForTest(upstream)
						parsed, parseErr := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
						require.NoError(t, parseErr)
						result, err = gateway.Forward(ctx, c, account, parsed)
					} else if strings.HasPrefix(protocol, "antigravity-") {
						response := "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello secret\"}],\"role\":\"model\"},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":13,\"candidatesTokenCount\":4,\"totalTokenCount\":17}}}\n\n"
						upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"ag-report"}}, Body: io.NopCloser(strings.NewReader(response))}}}
						gateway := newAntigravityCompatService(config.GatewayConfig{MaxLineSize: defaultMaxLineSize}, upstream)
						if protocol == "antigravity-gemini" {
							result, err = gateway.ForwardGemini(ctx, c, account, plan.call.Model, "generateContent", stream, body, false)
						} else {
							result, err = gateway.Forward(ctx, c, account, body, false)
						}
					} else {
						response := `{"candidates":[{"content":{"parts":[{"text":"hello secret"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":13,"candidatesTokenCount":4,"totalTokenCount":17}}`
						if stream {
							response = "data: " + response + "\n\n"
						}
						gateway := newGeminiSignalService(contentType, response)
						if protocol == "gemini" {
							action := "generateContent"
							if stream {
								action = "streamGenerateContent"
							}
							result, err = gateway.ForwardNative(ctx, c, account, plan.call.Model, action, stream, body)
						} else {
							result, err = gateway.Forward(ctx, c, account, body)
						}
					}
					require.NoError(t, err, recorder.Body.String())
					require.NotNil(t, result)
					require.NotNil(t, result.pluginReportBilling)
					require.Equal(t, 4, result.Usage.OutputTokens, "reply redaction must not change upstream usage")
					require.NotContains(t, recorder.Body.String(), "secret")
					require.Contains(t, recorder.Body.String(), "***")
					require.Same(t, r, c.Writer)
					require.Same(t, originalRequest, c.Request)
					require.NoError(t, c.Request.Context().Err())
					require.Equal(t, http.StatusOK, recorder.Code)
					r.Finish(body)
					require.Empty(t, h.requests, "report must await the original ledger")
					usage := &openAIRecordUsageLogRepoStub{}
					ledger := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
					billing := newGatewayRecordUsageServiceWithBillingRepoForTest(usage, ledger, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
					fixturePrice := &LiteLLMModelPricing{InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6, CacheCreationInputTokenCost: 1e-6, CacheReadInputTokenCost: .2e-6}
					billing.billingService = NewBillingService(billing.cfg, &PricingService{pricingData: map[string]*LiteLLMModelPricing{result.Model: fixturePrice}})
					billing.billingService.pricingService.pricingData[result.UpstreamModel] = fixturePrice
					err = billing.RecordUsage(context.Background(), &RecordUsageInput{Result: result, APIKey: &APIKey{ID: 5}, User: &User{ID: 4}, Account: account})
					require.NoError(t, err)
					require.Equal(t, 1, ledger.calls)
					require.Positive(t, usage.lastLog.ActualCost)
					payload := awaitTaskReport(t, h)
					require.Equal(t, plan.call.Format, gjson.GetBytes(payload["ctx"], "format").String())
					require.EqualValues(t, account.ID, gjson.GetBytes(payload["ctx"], "channel.id").Int())
					require.Equal(t, decimal.NewFromFloat(usage.lastLog.ActualCost).Round(8).String(), gjson.GetBytes(payload["record"], "cost.usd").String())
					require.EqualValues(t, result.Usage.InputTokens, gjson.GetBytes(payload["record"], "usage.promptTokens").Int())
					require.EqualValues(t, result.Usage.OutputTokens, gjson.GetBytes(payload["record"], "usage.completionTokens").Int())
					if stream {
						require.True(t, gjson.GetBytes(payload["record"], "response.stream").Bool())
						require.NotEmpty(t, gjson.GetBytes(payload["record"], "response.events").Array())
					} else {
						require.JSONEq(t, recorder.Body.String(), gjson.GetBytes(payload["record"], "response.body").Raw)
					}
					require.NotContains(t, string(payload["record"]), "secret")
					require.NotContains(t, string(payload["record"]), "oauth-token")
					require.NotContains(t, string(payload["record"]), "test-key")
				})
			}
		}
	}
}

func TestNativeProtocolReportBillingFailureAndDedup(t *testing.T) {
	for _, scenario := range []string{"committed", "duplicate", "failed", "simple", "legacy"} {
		t.Run(scenario, func(t *testing.T) {
			usage := &openAIRecordUsageLogRepoStub{}
			ledger := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: scenario != "duplicate"}}
			gateway := newGatewayRecordUsageServiceWithBillingRepoForTest(usage, ledger, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
			if scenario == "failed" {
				ledger.err = errors.New("ledger unavailable")
			}
			if scenario == "simple" {
				gateway.cfg.RunMode = config.RunModeSimple
			}
			if scenario == "legacy" {
				gateway.usageBillingRepo = nil
			}
			b := &relayReportBilling{done: make(chan struct{})}
			result := &ForwardResult{pluginReportBilling: b, RequestID: "native-report-ledger", Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 100, OutputTokens: 20}}
			input := &RecordUsageInput{Result: result, APIKey: &APIKey{ID: 5}, User: &User{ID: 4}, Account: &Account{ID: 6}}
			err := gateway.RecordUsage(context.Background(), input)
			if scenario == "failed" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			select {
			case <-b.done:
			default:
				t.Fatal("billing completion not signalled")
			}
			require.Equal(t, scenario == "committed" || scenario == "simple", b.confirmed)
			if scenario == "simple" {
				require.True(t, b.amount.IsZero())
			}
		})
	}
}

func TestNativeProtocolReportInstanceAdmission(t *testing.T) {
	s, _, _, _ := providerPolicyFixture(t, providerFixtureSource, globalHTTPReportSource+`export function reply(){return {action:"pass"}}; export function ingress(){return {action:"continue"}}; export function request(){return {action:"continue"}}; export function outbound(){return {action:"continue"}}; export function egress(){return {action:"continue"}}`, []string{"reply", "report"}, newAPIProviderChat)
	for _, platform := range []string{PlatformAnthropic, PlatformGemini, PlatformAntigravity} {
		s.gateway.accountRepo = &stubOpenAIAccountRepo{accounts: []Account{{ID: 19, Platform: platform, Type: AccountTypeAPIKey}}}
		input := RequestPluginInstance{Name: "Native report", PluginVersionID: 2, Scope: "account", AccountIDs: []int64{19}, Formats: []string{"claude", "gemini"}, Stages: []string{"report"}, Config: json.RawMessage(`{}`)}
		_, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
		require.NoError(t, err, platform)
		for _, stages := range [][]string{{"reply"}, {"reply", "report"}} {
			input.Stages = stages
			_, err = s.SaveRequestPluginInstance(context.Background(), input, 4)
			require.NoError(t, err)
		}
		for _, stages := range [][]string{{"ingress", "report"}, {"outbound", "report"}, {"request", "reply"}, {"egress", "reply"}} {
			input.Stages = stages
			_, err = s.SaveRequestPluginInstance(context.Background(), input, 4)
			if platform == PlatformAnthropic {
				require.Error(t, err, "Anthropic request stages require the claude format")
			} else {
				require.NoError(t, err, "Gemini and Antigravity now support native HTTP request stages")
			}
		}
	}
	for _, scope := range []string{"global", "group"} {
		require.NoError(t, validateRequestPluginStages([]string{"report"}, []string{"claude", "gemini"}, scope))
		require.NoError(t, validateRequestPluginStages([]string{"reply", "report"}, []string{"claude", "gemini"}, scope))
		require.NoError(t, validateRequestPluginStages([]string{"outbound", "report"}, []string{"claude"}, scope))
		require.NoError(t, validateRequestPluginStages([]string{"outbound", "report"}, []string{"gemini"}, scope))
		require.Error(t, validateRequestPluginStages([]string{"report"}, []string{"task", "gemini"}, scope))
	}
}

func (h *replyCancellationHTTP) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return h.Do(req, proxy, accountID, concurrency)
}

func TestNativeProtocolReplyStopCancelsHTTPAndPreservesUsage(t *testing.T) {
	for _, protocol := range []string{"claude", "gemini", "gemini-messages", "antigravity-claude", "antigravity-gemini"} {
		t.Run(protocol, func(t *testing.T) {
			_, _, _, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+`export function reply(){return {action:"stop",code:"blocked",message:"PRIVATE"}}`, []string{"reply"}, newAPIProviderChat)
			plan.call.Format, plan.call.Path, plan.call.Model = "claude", "/v1/messages", "claude-sonnet-4"
			account := newAnthropicOAuthAccountForPartialUsageTest()
			body := []byte(`{"model":"claude-sonnet-4","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
			h := &replyCancellationHTTP{timeout: 5 * time.Second}
			h.chunks = []string{
				"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":13}}}\n\n",
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"secret\"}}\n\n",
			}
			if protocol != "claude" {
				account = geminiSignalTestAccount()
				usage := `{"candidates":[],"usageMetadata":{"promptTokenCount":13,"candidatesTokenCount":4,"totalTokenCount":17}}`
				text := `{"candidates":[{"content":{"role":"model","parts":[{"text":"secret"}]}}]}`
				if strings.HasPrefix(protocol, "antigravity-") {
					account = newAntigravityCompatAccount(AccountTypeOAuth)
					usage, text = `{"response":`+usage+`}`, `{"response":`+text+`}`
				}
				h.chunks = []string{"data: " + usage + "\n\n", "data: " + text + "\n\n"}
			}
			if protocol == "gemini" || protocol == "antigravity-gemini" {
				plan.call.Format, plan.call.Path, plan.call.Model = "gemini", "/v1beta/models/gemini-2.5-flash:streamGenerateContent", "gemini-2.5-flash"
				body = geminiSignalTestRequest()
			}
			if strings.HasPrefix(protocol, "antigravity-") {
				account.Credentials["model_mapping"] = map[string]any{plan.call.Model: plan.call.Model}
			}
			plan.items[0].AccountIDs = []int64{account.ID}
			c, recorder := providerContext(plan.call.Path, string(body))
			originalRequest, originalWriter := c.Request, c.Writer
			parent, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
			defer cancel()
			ctx := WithRequestPluginPlan(parent, plan)
			var result *ForwardResult
			var err error
			switch protocol {
			case "claude":
				gateway := newForwardPartialUsageServiceForTest(&anthropicHTTPUpstreamRecorder{})
				gateway.httpUpstream = h
				parsed, parseErr := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, parseErr)
				result, err = gateway.Forward(ctx, c, account, parsed)
			case "gemini", "gemini-messages":
				gateway := newGeminiSignalService("", "")
				gateway.httpUpstream = h
				if protocol == "gemini" {
					result, err = gateway.ForwardNative(ctx, c, account, plan.call.Model, "streamGenerateContent", true, body)
				} else {
					result, err = gateway.Forward(ctx, c, account, body)
				}
			default:
				gateway := newAntigravityCompatService(config.GatewayConfig{MaxLineSize: defaultMaxLineSize}, h)
				if protocol == "antigravity-gemini" {
					result, err = gateway.ForwardGemini(ctx, c, account, plan.call.Model, "streamGenerateContent", true, body, false)
				} else {
					result, err = gateway.Forward(ctx, c, account, body, false)
				}
			}
			var failure *RequestPluginFailure
			require.ErrorAs(t, err, &failure)
			require.True(t, failure.Rejected, "class=%s response=%s", failure.Class, recorder.Body.String())
			require.NotNil(t, result)
			require.Equal(t, 13, result.Usage.InputTokens, "keep actual usage observed before the stop")
			if protocol == "claude" {
				require.Zero(t, result.Usage.OutputTokens, "a stop before message_delta must not invent final output tokens")
			} else {
				require.Equal(t, 4, result.Usage.OutputTokens)
			}
			require.Equal(t, 1, h.requests, "policy stops must not retry a paid generation")
			require.ErrorIs(t, h.requestCtx.Err(), context.Canceled)
			require.True(t, h.body.closed)
			require.NoError(t, parent.Err(), "policy cancellation must not cancel the billing parent")
			require.Same(t, originalWriter, c.Writer)
			require.Same(t, originalRequest, c.Request)
			require.NotContains(t, recorder.Body.String(), "secret")
			require.NotContains(t, recorder.Body.String(), "PRIVATE")
		})
	}
}
