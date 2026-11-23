//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const scopedNativeRequestHooks = `
export function request(ctx,req){
 if(req.body.model!=="public-text" || ctx.model!=="public-text" || ctx.upstreamModel!=="vendor-text" || ![1,43,57,61].includes(ctx.channel.type) || req.summary.totalTextLength!==5)throw Error("wrong request identity");
 return {action:"continue",effects:[{type:"reasoning.set",effort:{"1":"low","2":"medium","4":"high"}[ctx.instance.id],override:true}]};
}
export function ingress(ctx,req){
 if(!ctx.channel)return {action:"continue"};
 if(ctx.instance.id!=="4" || req.body.model!=="vendor-text" || (req.body.reasoning_effort || req.body.reasoning?.effort)!=="high" || req.body.metadata?.policy)throw Error("wrong late ingress");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/late_marker",value:ctx.instance.id}]}]};
}
export function outbound(ctx,req){
 if(req.body.model!=="vendor-text" || req.body.late_marker!=="4" || req.headers?.Authorization || req.headers?.["X-Api-Key"])throw Error("wrong outbound placement");
 if(ctx.format==="openai" && !req.body.messages || ctx.format==="openai_responses" && !req.body.input)throw Error("wrong final protocol");
 let policy=(req.body.metadata?.policy || "")+ctx.instance.id;
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/metadata",value:{...req.body.metadata,policy}}]},{type:"header.patch",set:{"X-Policy":policy}}]};
}
export function egress(ctx,req){
 if(req.body.metadata?.policy!=="124" || req.headers?.["X-Policy"]!=="124" || req.headers?.Authorization)throw Error("wrong egress order");
 if(ctx.format==="openai" && req.body.stream && !req.body.stream_options?.include_usage)throw Error("missing host usage setting");
 return {action:"continue"};
}
export function reply(){return {action:"continue"};}
`

func nativeRequestAccount(account *Account, responses bool) {
	account.Credentials = map[string]any{"base_url": "https://api.example.com", "api_key": "upstream-fixture-key", "model_mapping": map[string]any{"public-text": "vendor-text"}}
	account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: responses}
	if responses {
		account.Credentials["api_protocol"] = APIProtocolResponses
	} else {
		account.Credentials["api_protocol"] = APIProtocolChatCompletions
	}
}

func nativeRequestResponse(upstream *generationFixtureHTTP, responses, stream bool) {
	if responses {
		upstream.response = `{"id":"resp-native","object":"response","model":"vendor-text","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":12,"output_tokens":5,"total_tokens":17}}`
		if stream {
			upstream.response = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"hello\"}\n\n" + "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + upstream.response + "}\n\n"
		}
	} else {
		upstream.response = `{"id":"chat-native","object":"chat.completion","model":"vendor-text","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`
		if stream {
			upstream.response = "data: {\"id\":\"chat-native\",\"object\":\"chat.completion.chunk\",\"model\":\"vendor-text\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5,\"total_tokens\":17}}\n\ndata: [DONE]\n\n"
		}
	}
	if stream {
		upstream.contentType = "text/event-stream"
	}
}

func TestScopedHTTPRequestStagesNativeAndProviderActualProtocol(t *testing.T) {
	for _, scenario := range []struct {
		name, path          string
		responses, provider bool
	}{
		{"native-chat", newAPIProviderChat, false, false}, {"native-responses", newAPIProviderResponses, true, false},
		{"native-chat-via-responses", newAPIProviderChat, true, false}, {"native-responses-via-chat", newAPIProviderResponses, false, false},
		{"provider-chat", newAPIProviderChat, false, true}, {"provider-responses", newAPIProviderResponses, true, true},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(scenario.name+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
				s, account, upstream, plan := scopedReplyFixture(t, scenario.path, globalHTTPReportSource+scopedNativeRequestHooks, []string{"ingress", "request", "outbound", "egress", "reply", "report"})
				if !scenario.provider {
					nativeRequestAccount(account, scenario.responses)
				}
				nativeRequestResponse(upstream, scenario.responses, stream || !scenario.provider && scenario.responses && scenario.path == newAPIProviderChat)
				h := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 8)}
				s.gateway.httpUpstream = &relayReportHTTP{upstream: s.gateway.httpUpstream, reports: h}
				s.taskReports = newTaskReportExecutor(s)
				t.Cleanup(s.taskReports.close)
				body := []byte(`{"model":"public-text","messages":[{"role":"user","content":"hello"}],"input":"hello","stream":` + strconv.FormatBool(stream) + `}`)
				early, err := plan.Ingress(context.Background(), body)
				require.NoError(t, err)
				require.Len(t, early.Records, 2, "only global/group ingress runs at the client boundary")
				c, recorder := providerContext(scenario.path, string(early.Body))
				original := c.Request
				ctx := WithRequestPluginPlan(c.Request.Context(), plan)
				r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
				c.Writer = r
				var result *OpenAIForwardResult
				if scenario.path == newAPIProviderChat {
					result, err = s.gateway.ForwardAsChatCompletions(ctx, c, account, early.Body, "", "")
				} else {
					result, err = s.gateway.Forward(ctx, c, account, early.Body)
				}
				require.NoError(t, err, "runs: %+v", s.repo.(*requestPluginFixtureRepo).runs)
				require.NotNil(t, result)
				require.Equal(t, 5, result.Usage.OutputTokens)
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, "high", *result.ReasoningEffort)
				require.Contains(t, recorder.Body.String(), "hello")
				require.Same(t, original, c.Request)
				require.Same(t, r, c.Writer)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, "124", upstream.requests[0].Header.Get("X-Policy"))
				wire, err := io.ReadAll(upstream.requests[0].Body)
				require.NoError(t, err)
				require.Equal(t, "124", gjson.GetBytes(wire, "metadata.policy").String())
				r.Finish(early.Body)
				result.pluginReportBilling.complete(decimal.RequireFromString("0.12"), true)
				for _, id := range []int64{1, 2, 4} {
					payload := awaitTaskReport(t, h)
					require.Equal(t, strconv.FormatInt(id, 10), gjson.GetBytes(payload["ctx"], "instance.id").String())
					counts := map[string]int{}
					for _, record := range gjson.GetBytes(payload["record"], "requestPlugins").Array() {
						counts[record.Get("stage").String()]++
						require.NotEqualValues(t, 9, record.Get("instance_id").Int())
					}
					require.Equal(t, map[string]int{"ingress": 3, "request": 3, "outbound": 3, "egress": 3, "reply": 3}, counts)
					require.Equal(t, "0.12", gjson.GetBytes(payload["record"], "cost.usd").String())
				}
			})
		}
	}
}

func TestNativeRequestPolicyGuardsBeforeNetworkAndOptionalAtomicity(t *testing.T) {
	for _, scenario := range []struct{ name, stage, hook string }{
		{"reject", "egress", `return {action:"reject",message:"PRIVATE"}`},
		{"routing", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/model",value:"other"}]}]}`},
		{"billing", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/seconds",value:999}]}]}`},
		{"authorization", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{Authorization:"private-key"}}]}`},
		{"native-key", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{"x-api-key":"private-key"}}]}`},
		{"oauth-identity", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{"Chatgpt-Account-Id":"other"}}]}`},
		{"read-only", "egress", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/temperature",value:0.5}]}]}`},
		{"network", "outbound", `utils.fetch({url:"https://api.example.com"});return {action:"continue"}`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+`export function `+scenario.stage+`(){`+scenario.hook+`}`, []string{scenario.stage}, newAPIProviderChat)
			nativeRequestAccount(account, false)
			body := []byte(`{"model":"public-text","messages":[]}`)
			c, w := providerContext(newAPIProviderChat, string(body))
			_, err := s.gateway.ForwardAsChatCompletions(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, "", "")
			var failure *RequestPluginFailure
			require.ErrorAs(t, err, &failure)
			require.Empty(t, upstream.requests)
			if failure.Rejected {
				require.Equal(t, http.StatusForbidden, w.Code)
			} else {
				require.Equal(t, http.StatusServiceUnavailable, w.Code)
			}
			require.NotContains(t, w.Body.String(), "PRIVATE")
			require.NotContains(t, w.Body.String(), "private-key")
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover), "local policy must not trigger account failover")
			require.Len(t, s.repo.(*requestPluginFixtureRepo).runs, 1)
		})
	}
	policy := strings.Replace(providerPolicyMeta, "apiVersion:1,", "apiVersion:1,optional:true,", 1) + `export function outbound(){return {action:"continue",effects:[{type:"header.patch",set:{"X-Atomic":"must-not-leak"}},{type:"json.patch",ops:[{op:"replace",path:"/model",value:"other"}]}]}}`
	s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, policy, []string{"outbound"}, newAPIProviderChat)
	nativeRequestAccount(account, false)
	c, _ := providerContext(newAPIProviderChat, `{}`)
	_, err := s.gateway.ForwardAsChatCompletions(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(`{"model":"public-text","messages":[]}`), "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Empty(t, upstream.requests[0].Header.Get("X-Atomic"))
	require.Equal(t, "skipped", s.repo.(*requestPluginFixtureRepo).runs[0].Records[0].Action)
}

func TestNativeRequestPolicyAdmissionAndPinnedScope(t *testing.T) {
	for _, scenario := range []string{"memory", "audit", "cancel", "other-account", "success"} {
		t.Run(scenario, func(t *testing.T) {
			s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+`export function outbound(){return {action:"continue"}}`, []string{"outbound"}, newAPIProviderChat)
			nativeRequestAccount(account, false)
			capacity := int64(16 << 20)
			if scenario == "memory" {
				capacity = 1
			}
			ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), pkghttputil.NewBodyMemoryBudget(capacity))
			defer release()
			switch scenario {
			case "audit":
				s.repo.(*requestPluginFixtureRepo).runErr = errors.New("private DB error")
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "other-account":
				account.ID++
				s.repo.(*requestPluginFixtureRepo).runErr = errors.New("must not run")
			}
			c, w := providerContext(newAPIProviderChat, `{}`)
			_, err := s.gateway.ForwardAsChatCompletions(WithRequestPluginPlan(ctx, plan), c, account, []byte(`{"model":"public-text","messages":[]}`), "", "")
			if scenario == "success" || scenario == "other-account" {
				require.NoError(t, err)
				require.Len(t, upstream.requests, 1)
				if scenario == "success" {
					require.NoError(t, pkghttputil.ReserveRequestBodyMemory(ctx, 16<<20), "native policy scope releases all reservations")
				}
			} else {
				require.Error(t, err)
				require.Empty(t, upstream.requests)
			}
			require.NotContains(t, w.Body.String(), "private DB error")
		})
	}
}

func TestNativeRequestOAuthAndSetupTokenKeepCredentialOwnership(t *testing.T) {
	source := providerPolicyMeta + `export function request(ctx,req){if(ctx.channel.type!==57 || req.body.model!=="public-text")throw Error("wrong requester");return {action:"continue",effects:[{type:"reasoning.set",effort:"high"}]};}
export function outbound(ctx,req){if(ctx.channel.type!==57 || ctx.upstreamModel!=="vendor-text" || req.body.model!=="vendor-text" || req.headers?.Authorization || req.headers?.["Chatgpt-Account-Id"])throw Error("wrong native identity");return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/metadata",value:{proof:"native-oauth"}}]}]};}
export function egress(ctx,req){if(req.body.metadata?.proof!=="native-oauth")throw Error("wrong egress");return {action:"continue"};}
export function reply(){return {action:"continue"};}`
	for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(kind, func(t *testing.T) {
			s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, source, []string{"request", "outbound", "egress", "reply"}, newAPIProviderResponses)
			nativeRequestAccount(account, true)
			account.Type = kind
			account.Credentials["access_token"] = "saved-native-token"
			account.Credentials["chatgpt_account_id"] = "fixture-tenant"
			s.gateway.accountRepo = &stubOpenAIAccountRepo{accounts: []Account{*account}}
			_, err := s.SaveRequestPluginInstance(context.Background(), RequestPluginInstance{Name: "Native identity", PluginVersionID: 2, Scope: "account", AccountIDs: []int64{account.ID}, Stages: []string{"request", "outbound", "egress", "reply"}, Formats: []string{"openai_responses"}, Config: json.RawMessage(`{}`)}, 4)
			require.NoError(t, err)
			nativeRequestResponse(upstream, true, true)
			body := []byte(`{"model":"public-text","input":"hello","stream":true}`)
			c, w := providerContext(newAPIProviderResponses, string(body))
			result, err := s.gateway.Forward(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body)
			require.NoError(t, err, "runs: %+v", s.repo.(*requestPluginFixtureRepo).runs)
			require.NotNil(t, result)
			require.Equal(t, 5, result.Usage.OutputTokens)
			require.Contains(t, w.Body.String(), "hello")
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "Bearer saved-native-token", upstream.requests[0].Header.Get("Authorization"))
			require.Equal(t, "fixture-tenant", upstream.requests[0].Header.Get("Chatgpt-Account-Id"))
			require.NotContains(t, w.Body.String(), "saved-native-token")
		})
	}
}

func TestNativeRequestPassthroughChecksFinalBodyWithoutMutatingIt(t *testing.T) {
	source := providerPolicyMeta + `export function ingress(ctx){if(ctx.channel)throw Error("late ingress must stay unchecked");return {action:"continue"};}
export function outbound(){throw Error("passthrough outbound must stay unchecked");}
export function egress(ctx,req){if(ctx.format!=="openai_responses" || req.body.input!=="hello")throw Error("wrong final body");return {action:"continue"};}`
	s, account, upstream, plan := scopedReplyFixture(t, newAPIProviderResponses, source, []string{"ingress", "outbound", "egress"})
	nativeRequestAccount(account, true)
	account.Extra["openai_passthrough"] = true
	// Use the gateway's public setting, rather than manufacturing a raw HTTP
	// request that would miss real passthrough routing and transport guards.
	nativeRequestResponse(upstream, true, false)
	c, _ := providerContext(newAPIProviderResponses, `{}`)
	_, err := s.gateway.Forward(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(`{"model":"public-text","input":"hello"}`))
	require.NoError(t, err, "runs: %+v", s.repo.(*requestPluginFixtureRepo).runs)
	require.Len(t, upstream.requests, 1)
	for _, run := range s.repo.(*requestPluginFixtureRepo).runs {
		for _, record := range run.Records {
			if record.Stage == "outbound" || record.Stage == "ingress" {
				require.Equal(t, "unchecked", record.Action)
			} else {
				require.Equal(t, "continue", record.Action)
			}
		}
	}
}

func TestNativeRequestUnsupportedProtocolIsAuditedBeforeSending(t *testing.T) {
	s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+`export function outbound(){return {action:"continue"}}`, []string{"outbound"}, newAPIProviderChat)
	nativeRequestAccount(account, false)
	account.Platform = PlatformKimi
	account.Credentials["api_protocol"] = APIProtocolAnthropic
	require.Equal(t, APIProtocolAnthropic, s.gateway.resolveUpstreamProtocolFor(context.Background(), account, APIProtocolChatCompletions, "vendor-text"))
	body := []byte(`{"model":"public-text","messages":[{"role":"user","content":"hello"}]}`)
	c, w := providerContext(newAPIProviderChat, string(body))
	_, err := s.gateway.ForwardAsChatCompletions(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, "", "")
	var failure *RequestPluginFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "unsupported_protocol", failure.Class)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Empty(t, upstream.requests)
	runs := s.repo.(*requestPluginFixtureRepo).runs
	require.Len(t, runs, 1)
	require.Equal(t, "outbound", runs[0].Records[0].Stage)
	require.Equal(t, "unsupported_protocol", runs[0].Records[0].ErrorClass)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
}

func TestNativeRequestOutboundReasoningMatchesActualWireUsageMetadata(t *testing.T) {
	s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+`export function outbound(){return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/reasoning_effort",value:"low"}]}]}}`, []string{"outbound"}, newAPIProviderChat)
	nativeRequestAccount(account, false)
	body := []byte(`{"model":"public-text","messages":[],"reasoning_effort":"high"}`)
	c, _ := providerContext(newAPIProviderChat, string(body))
	result, err := s.gateway.ForwardAsChatCompletions(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "low", *result.ReasoningEffort, "billing/log facts cannot retain the pre-plugin effort")
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Len(t, upstream.requests, 1)
	wire, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "low", gjson.GetBytes(wire, "reasoning_effort").String())
	require.Equal(t, "high", gjson.GetBytes(body, "reasoning_effort").String(), "retry input stays immutable")
}
