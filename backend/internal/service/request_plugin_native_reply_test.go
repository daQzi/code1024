package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/replyfilter"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const nativeReplyMask = `
export function reply(ctx,res){
 if(ctx.stage!=="reply" || ![1,43,57,61].includes(ctx.channel.type) || ctx.model!=="public-text" || ctx.upstreamModel!=="vendor-text")throw Error("wrong reply identity");
 const mask=[];
 for(const o of res.outputs){let i=o.text.indexOf("secret");if(i>=0)mask.push({index:o.index,start:i,end:i+6});}
 return {action:"continue",mask,hold:res.stream?8:0};
}`

func scopedReplyFixture(t *testing.T, path, source string, stages []string) (*NewAPIPluginService, *Account, *generationFixtureHTTP, *RequestPluginPlan) {
	t.Helper()
	s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, source, stages, path)
	item := plan.items[0]
	global, group, other := item, item, item
	global.ID, global.Scope, global.AccountIDs = 1, "global", nil
	group.ID, group.Scope, group.GroupIDs, group.AccountIDs = 2, "group", []int64{7}, nil
	other.ID, other.AccountIDs = 9, []int64{999}
	plan.items = []RequestPluginInstance{global, group, item, other}
	return s, account, upstream, plan
}

func TestRelayReplyGlobalGroupNativeAndProviderActualResponse(t *testing.T) {
	for _, scenario := range []struct {
		name, path          string
		responses, provider bool
	}{
		{"native-chat", newAPIProviderChat, false, false}, {"native-responses", newAPIProviderResponses, true, false}, {"native-chat-via-responses", newAPIProviderChat, true, false},
		{"provider-chat", newAPIProviderChat, false, true}, {"provider-responses", newAPIProviderResponses, true, true},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(scenario.name+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
				s, account, upstream, plan := scopedReplyFixture(t, scenario.path, globalHTTPReportSource+nativeReplyMask, []string{"reply", "report"})
				if !scenario.provider {
					account.Credentials = map[string]any{"base_url": "https://api.example.com", "api_key": "upstream-fixture-key", "model_mapping": map[string]any{"public-text": "vendor-text"}}
					account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: scenario.responses}
				}
				upstreamStream := stream || !scenario.provider && scenario.responses && scenario.path == newAPIProviderChat
				if scenario.responses {
					upstream.response = `{"id":"resp-native","object":"response","model":"vendor-text","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello secret"}]}],"usage":{"input_tokens":12,"output_tokens":5,"total_tokens":17}}`
					if upstreamStream {
						upstream.response = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"hello secret\"}\n\n" +
							"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + upstream.response + "}\n\n"
					}
				} else {
					upstream.response = `{"id":"chat-native","object":"chat.completion","model":"vendor-text","choices":[{"index":0,"message":{"role":"assistant","content":"hello secret"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`
					if stream {
						upstream.response = "data: {\"id\":\"chat-native\",\"object\":\"chat.completion.chunk\",\"model\":\"vendor-text\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello secret\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5,\"total_tokens\":17}}\n\ndata: [DONE]\n\n"
					}
				}
				if upstreamStream {
					upstream.contentType = "text/event-stream"
				}
				h := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 8)}
				s.gateway.httpUpstream = &relayReportHTTP{upstream: s.gateway.httpUpstream, reports: h}
				s.taskReports = newTaskReportExecutor(s)
				t.Cleanup(s.taskReports.close)
				body := []byte(`{"model":"public-text","messages":[{"role":"user","content":"hello"}],"input":"hello","stream":` + strconv.FormatBool(stream) + `}`)
				c, recorder := providerContext(scenario.path, string(body))
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
				ctx := WithRequestPluginPlan(c.Request.Context(), plan)
				r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
				c.Writer = r
				originalRequest := c.Request
				var result *OpenAIForwardResult
				var err error
				if scenario.path == newAPIProviderChat {
					result, err = s.gateway.ForwardAsChatCompletions(ctx, c, account, body, "", "")
				} else {
					result, err = s.gateway.Forward(ctx, c, account, body)
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 5, result.Usage.OutputTokens, "masking never edits measured usage")
				require.NotContains(t, recorder.Body.String(), "secret")
				require.Contains(t, recorder.Body.String(), "***")
				require.Same(t, r, c.Writer)
				require.Same(t, originalRequest, c.Request)
				require.NoError(t, c.Request.Context().Err())
				require.Len(t, upstream.requests, 1)
				r.Finish(body)
				result.pluginReportBilling.complete(decimal.RequireFromString("0.12"), true)
				for _, id := range []int64{1, 2, 4} {
					payload := awaitTaskReport(t, h)
					require.Equal(t, strconv.FormatInt(id, 10), gjson.GetBytes(payload["ctx"], "instance.id").String())
					require.EqualValues(t, requestPluginChannelType(account, scenario.provider), gjson.GetBytes(payload["ctx"], "channel.type").Int())
					require.NotContains(t, string(payload["record"]), "secret")
					require.Equal(t, "0.12", gjson.GetBytes(payload["record"], "cost.usd").String())
					var ids []int64
					for _, record := range gjson.GetBytes(payload["record"], "requestPlugins").Array() {
						if record.Get("stage").String() == "reply" {
							ids = append(ids, record.Get("instance_id").Int())
						}
					}
					require.Equal(t, []int64{1, 2, 4}, ids)
				}
				require.Empty(t, h.requests)
			})
		}
	}
}

func TestNativeReplyStopKeepsUsageAndRestoresContext(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			s, account, upstream, plan := scopedReplyFixture(t, newAPIProviderChat, providerPolicyMeta+`export function reply(){return {action:"stop",code:"blocked",message:"PRIVATE"}}`, []string{"reply"})
			account.Credentials = map[string]any{"base_url": "https://api.example.com", "api_key": "upstream-fixture-key", "api_protocol": APIProtocolChatCompletions}
			account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
			upstream.response = `{"id":"c","model":"public-text","choices":[{"index":0,"message":{"role":"assistant","content":"secret"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`
			if stream {
				upstream.contentType = "text/event-stream"
				upstream.response = "data: {\"id\":\"c\",\"model\":\"public-text\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"secret\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n"
			}
			body := []byte(`{"model":"public-text","messages":[],"stream":` + strconv.FormatBool(stream) + `}`)
			c, w := providerContext(newAPIProviderChat, string(body))
			original := c.Request
			result, err := s.gateway.ForwardAsChatCompletions(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, "", "")
			var failure *RequestPluginFailure
			require.ErrorAs(t, err, &failure)
			require.True(t, failure.Rejected)
			require.NotNil(t, result)
			require.Equal(t, 5, result.Usage.OutputTokens)
			require.NotContains(t, w.Body.String(), "secret")
			require.NotContains(t, w.Body.String(), "PRIVATE")
			require.Same(t, original, c.Request)
			require.NoError(t, c.Request.Context().Err())
			require.Len(t, upstream.requests, 1)
		})
	}
}

func TestNativeReplyAdmissionAndMemoryRelease(t *testing.T) {
	for _, scenario := range []string{"audit", "memory", "instances", "success", "unrelated-account"} {
		t.Run(scenario, func(t *testing.T) {
			s, account, upstream, plan := scopedReplyFixture(t, newAPIProviderChat, providerPolicyMeta+`export function reply(){return {action:"pass"}}`, []string{"reply"})
			account.Credentials = map[string]any{"api_key": "upstream-fixture-key", "base_url": "https://api.example.com", "api_protocol": APIProtocolChatCompletions}
			account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
			capacity := int64(providerReplyMemory + 4<<20)
			if scenario == "memory" {
				capacity = 1
			}
			if scenario == "audit" {
				s.repo.(*requestPluginFixtureRepo).runErr = errors.New("private db failure")
			}
			if scenario == "instances" {
				for range replyfilter.MaxInstances {
					plan.items = append(plan.items, plan.items[0])
				}
			}
			if scenario == "unrelated-account" {
				plan.items = plan.items[3:]
			}
			budget := pkghttputil.NewBodyMemoryBudget(capacity)
			ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
			defer release()
			body := []byte(`{"model":"public-text","messages":[]}`)
			c, w := providerContext(newAPIProviderChat, string(body))
			_, err := s.gateway.ForwardAsChatCompletions(WithRequestPluginPlan(ctx, plan), c, account, body, "", "")
			if scenario == "success" || scenario == "unrelated-account" {
				require.NoError(t, err)
				require.Len(t, upstream.requests, 1)
			} else {
				require.Error(t, err)
				require.Empty(t, upstream.requests)
				require.Equal(t, http.StatusServiceUnavailable, w.Code)
			}
			require.NotContains(t, w.Body.String(), "private db failure")
			if scenario != "unrelated-account" {
				require.NoError(t, pkghttputil.ReserveRequestBodyMemory(ctx, capacity))
			}
		})
	}
}

func TestNativeReplyExcludedEndpointsKeepOriginalContext(t *testing.T) {
	for _, path := range []string{newAPIProviderEmbeddings, openAIImagesGenerationsEndpoint, openAIImagesEditsEndpoint} {
		t.Run(path, func(t *testing.T) {
			_, account, _, plan := scopedReplyFixture(t, path, providerPolicyMeta+`export function reply(){throw Error("not a text reply")}`, []string{"reply"})
			c, _ := providerContext(path, `{"model":"public-text"}`)
			parent := WithRequestPluginPlan(c.Request.Context(), plan)
			originalRequest, originalWriter := c.Request, c.Writer
			child, finish, err := beginNativeProtocolReply(parent, c, account, "vendor-text")
			require.NoError(t, err)
			require.Same(t, parent, child, "skipping reply must not leak a released memory scope or canceled abort")
			require.NoError(t, withNativeReplyUpstreamAbort(child).Err())
			finish(&err)
			require.NoError(t, err)
			require.Same(t, originalRequest, c.Request)
			require.Same(t, originalWriter, c.Writer)
		})
	}
}

func TestNativeReplyAndGlobalStageSaveValidation(t *testing.T) {
	s, account, _, _ := providerPolicyFixture(t, providerFixtureSource, globalHTTPReportSource+nativeReplyMask, []string{"reply", "report"}, newAPIProviderChat)
	account.Credentials = map[string]any{"api_key": "saved-key", "base_url": "https://api.example.com"}
	s.gateway.accountRepo = &stubOpenAIAccountRepo{accounts: []Account{*account}}
	for _, stages := range [][]string{{"reply"}, {"report"}, {"reply", "report"}} {
		_, err := s.SaveRequestPluginInstance(context.Background(), RequestPluginInstance{Name: "Native response", PluginVersionID: 2, Scope: "account", AccountIDs: []int64{account.ID}, Formats: []string{"openai"}, Stages: stages, Config: json.RawMessage(`{}`)}, 4)
		require.NoError(t, err)
	}
	for _, scope := range []string{"global", "group"} {
		for _, stages := range [][]string{{"reply"}, {"reply", "report"}, {"ingress", "reply", "report"}} {
			require.NoError(t, validateRequestPluginStages(stages, []string{"openai", "openai_responses"}, scope))
		}
		for _, formats := range [][]string{nil, {"task"}, {"openai", "task"}} {
			require.Error(t, validateRequestPluginStages([]string{"reply"}, formats, scope))
		}
		require.NoError(t, validateRequestPluginStages([]string{"egress", "reply"}, []string{"openai"}, scope))
		require.NoError(t, validateRequestPluginStages([]string{"outbound", "reply"}, []string{"openai"}, scope))
		require.Error(t, validateRequestPluginStages([]string{"egress", "reply"}, []string{"task"}, scope))
	}
}

func TestNativeReplyStopCancelsActualHTTPAndPreservesBillingContext(t *testing.T) {
	s, account, _, plan := scopedReplyFixture(t, newAPIProviderChat, providerPolicyMeta+`export function reply(){return {action:"stop",code:"blocked",message:"PRIVATE"}}`, []string{"reply"})
	account.Credentials = map[string]any{"base_url": "https://api.example.com", "api_key": "upstream-fixture-key", "api_protocol": APIProtocolChatCompletions}
	account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
	h := &replyCancellationHTTP{timeout: 5 * time.Second}
	s.gateway.httpUpstream = h
	body := []byte(`{"model":"public-text","messages":[],"stream":true}`)
	c, w := providerContext(newAPIProviderChat, string(body))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	result, err := s.gateway.ForwardAsChatCompletions(WithRequestPluginPlan(ctx, plan), c, account, body, "", "")
	var failure *RequestPluginFailure
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.Rejected)
	require.ErrorIs(t, h.requestCtx.Err(), context.Canceled)
	require.ErrorIs(t, h.body.ctx.Err(), context.Canceled)
	require.True(t, h.body.closed)
	require.NotNil(t, result)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.NotContains(t, w.Body.String(), "secret")
	require.NotContains(t, w.Body.String(), "[DONE]")
	require.NoError(t, ctx.Err())
}

func TestNativeReplyAbortPreservesLateContextAndClientDrain(t *testing.T) {
	type lateKey struct{}
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	abortCtx, abort := context.WithCancel(context.WithoutCancel(parent))
	defer abort()
	ctx := context.WithValue(parent, nativeReplyAbortKey{}, nativeReplyAbort{ctx: abortCtx, cancel: abort})
	ctx = context.WithValue(ctx, lateKey{}, "native-routing-identity")
	for _, stream := range []bool{false, true} {
		detached, release := detachUpstreamContext(ctx)
		if stream {
			detached, release = detachStreamUpstreamContext(ctx, true)
		}
		defer release()
		require.Equal(t, "native-routing-identity", detached.Value(lateKey{}))
		cancelParent()
		require.NoError(t, detached.Err(), "normal client cancellation retains the existing bounded usage drain")
		attempt, finish := pkghttputil.NewUpstreamAttemptContext(detached, time.Second)
		defer finish()
		if stream {
			abort()
			require.Eventually(t, func() bool { return attempt.Err() != nil }, time.Second, time.Millisecond)
			require.ErrorIs(t, attempt.Err(), context.Canceled)
		}
	}
}

func TestNativeReplyOptionalFailureAndTerminalAudit(t *testing.T) {
	for _, scenario := range []string{"required", "optional", "audit"} {
		t.Run(scenario, func(t *testing.T) {
			source := providerPolicyMeta + `export function reply(){throw Error("private-credential-error")}`
			if scenario == "optional" {
				source = strings.Replace(source, "apiVersion:1,", "apiVersion:1,optional:true,", 1)
			}
			if scenario == "audit" {
				source = providerPolicyMeta + `export function reply(){return {action:"pass"}}`
			}
			s, account, upstream, plan := scopedReplyFixture(t, newAPIProviderChat, source, []string{"reply"})
			account.Credentials = map[string]any{"base_url": "https://api.example.com", "api_key": "upstream-fixture-key", "api_protocol": APIProtocolChatCompletions}
			account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
			upstream.response = `{"id":"c","model":"public-text","choices":[{"index":0,"message":{"role":"assistant","content":"checked text"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`
			if scenario == "audit" {
				s.gateway.httpUpstream = &replyAuditFailureHTTP{inner: upstream, repo: s.repo.(*requestPluginFixtureRepo)}
			}
			body := []byte(`{"model":"public-text","messages":[]}`)
			c, w := providerContext(newAPIProviderChat, string(body))
			result, err := s.gateway.ForwardAsChatCompletions(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, "", "")
			require.NotNil(t, result)
			require.Equal(t, 5, result.Usage.OutputTokens)
			if scenario == "optional" {
				require.NoError(t, err)
				require.Contains(t, w.Body.String(), "checked text")
			} else {
				require.Error(t, err)
			}
			if scenario == "required" {
				require.NotContains(t, w.Body.String(), "checked text")
			}
			if scenario == "audit" {
				require.EqualValues(t, 1, s.ReplyAuditFailures())
				require.Len(t, s.repo.(*requestPluginFixtureRepo).runs, 1)
			}
			require.NotContains(t, w.Body.String(), "private-credential-error")
		})
	}
}
