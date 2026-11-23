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

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func nativeImagesOAuthPolicyFixture(t *testing.T, path, source string, stages []string, direct bool) (*NewAPIPluginService, *Account, *generationFixtureHTTP, *RequestPluginPlan) {
	t.Helper()
	s, account, upstream, plan := nativeImagesPolicyFixture(t, path, source, stages)
	account.Type = AccountTypeOAuth
	account.Credentials = map[string]any{"access_token": "oauth-fixture-token", "chatgpt_account_id": "oauth-fixture-account"}
	if !direct {
		account.Credentials["model_mapping"] = map[string]any{"gpt-image-2": "gpt-image-1"}
	}
	return s, account, upstream, plan
}

func nativeImagesOAuthPolicyResponse(upstream *generationFixtureHTTP, direct, stream bool) {
	upstream.response = `{"created":1710000007,"data":[{"b64_json":"ZmluYWw="}],"usage":{"input_tokens":10,"output_tokens":18,"output_tokens_details":{"image_tokens":8}}}`
	if !direct {
		upstream.response = `{"id":"resp-image-policy","status":"completed","created_at":1710000007,"output":[{"type":"image_generation_call","id":"image-policy","status":"completed","result":"ZmluYWw="}],"usage":{"input_tokens":10,"output_tokens":18,"output_tokens_details":{"image_tokens":8}}}`
	}
	if !direct || stream {
		upstream.contentType = "text/event-stream"
		if direct {
			upstream.response = "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"ZmluYWw=\",\"usage\":{\"input_tokens\":10,\"output_tokens\":18,\"output_tokens_details\":{\"image_tokens\":8}}}\n\ndata: [DONE]\n\n"
		} else {
			upstream.response = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + upstream.response + "}\n\n"
		}
	}
}

func TestNativeImagesOAuthRequestStagesWireAndConfirmedBilling(t *testing.T) {
	for _, direct := range []bool{true, false} {
		for _, path := range []string{openAIImagesGenerationsEndpoint, openAIImagesEditsEndpoint} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("direct=%t/%s/stream=%t", direct, path, stream), func(t *testing.T) {
					source := globalHTTPReportSource + `
export function request(ctx,req){
 if(ctx.format!=="openai" || ctx.model!=="gpt-image-2" || ctx.channel.type!==57 || req.body.prompt!=="draw")throw Error("wrong image request");
 return {action:"continue"};
}
export function ingress(ctx,req){
 const bridge=ctx.format==="openai_responses";
 const prompt=bridge?req.body.input[0].content[0].text:req.body.prompt;
 if(prompt!=="draw" || ctx.upstreamModel!==(bridge?"gpt-image-1":"gpt-image-2") || req.headers?.Authorization)throw Error("wrong image wire");
 if(bridge && (req.body.tools[0].model!=="gpt-image-1" || req.body.model==="gpt-image-1"))throw Error("wrong bridge identity");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:bridge?"/input/0/content/0/text":"/prompt",value:"sanitized"}]}]};
}
export function outbound(ctx,req){
 const bridge=ctx.format==="openai_responses";
 if((bridge?req.body.input[0].content[0].text:req.body.prompt)!=="sanitized" || req.body.user)throw Error("wrong image stage order");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/user",value:"policy"}]},{type:"header.patch",set:{"X-Image-Policy":"checked"}}]};
}
export function egress(ctx,req){
 if(req.body.user!=="policy" || req.headers?.["X-Image-Policy"]!=="checked" || req.body.stream_options)throw Error("wrong image egress");
 return {action:"continue"};
}
export function reply(){throw Error("images must skip text reply");}`
					s, account, upstream, plan := nativeImagesOAuthPolicyFixture(t, path, source, []string{"request", "ingress", "outbound", "egress", "reply", "report"}, direct)
					body := []byte(fmt.Sprintf(`{"model":"gpt-image-2","prompt":"draw","n":1,"size":"1024x1024","stream":%t,"response_format":"b64_json"}`, stream))
					if path == openAIImagesEditsEndpoint {
						body = []byte(strings.TrimSuffix(string(body), "}") + `,"images":[{"image_url":"data:image/png;base64,aW5wdXQ="}]}`)
					}
					nativeImagesOAuthPolicyResponse(upstream, direct, stream)
					reports := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 4)}
					s.gateway.httpUpstream = &relayReportHTTP{upstream: s.gateway.httpUpstream, reports: reports}
					s.taskReports = newTaskReportExecutor(s)
					t.Cleanup(s.taskReports.close)
					c, response := providerContext(path, string(body))
					c.Request.Header.Set("Content-Type", "application/json")
					parsed, err := s.gateway.ParseOpenAIImagesRequest(c, body)
					require.NoError(t, err)
					original := c.Request
					ctx := WithRequestPluginPlan(c.Request.Context(), plan)
					report := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
					require.NotNil(t, report)
					c.Writer = report
					result, err := s.gateway.ForwardImages(ctx, c, account, body, parsed, "")
					require.NoError(t, err, "runs: %+v", s.repo.(*requestPluginFixtureRepo).runs)
					require.Equal(t, http.StatusOK, response.Code)
					require.Same(t, original, c.Request)
					require.Len(t, upstream.requests, 1, "each image attempt sends once")
					req := upstream.requests[0]
					sent, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.Equal(t, "Bearer oauth-fixture-token", req.Header.Get("Authorization"))
					require.Equal(t, "oauth-fixture-account", req.Header.Get("Chatgpt-Account-Id"))
					require.Equal(t, "chatgpt.com", req.Host)
					require.NotEmpty(t, req.Header.Get("Originator"))
					require.Equal(t, "checked", req.Header.Get("X-Image-Policy"))
					require.Equal(t, "policy", gjson.GetBytes(sent, "user").Str)
					require.False(t, gjson.GetBytes(sent, "stream_options").Exists())
					require.Nil(t, result.ReasoningEffort, "bridge text reasoning is not an image pricing tier")
					if direct {
						require.Equal(t, "/backend-api/codex"+strings.TrimPrefix(path, "/v1"), req.URL.Path)
						require.Equal(t, "sanitized", gjson.GetBytes(sent, "prompt").Str)
						require.Equal(t, "gpt-image-2", gjson.GetBytes(sent, "model").Str)
						require.Equal(t, "gpt-image-2", result.UpstreamModel)
						require.Empty(t, req.Header.Get("OpenAI-Beta"))
					} else {
						require.Equal(t, "/backend-api/codex/responses", req.URL.Path)
						require.Equal(t, "sanitized", gjson.GetBytes(sent, "input.0.content.0.text").Str)
						require.Equal(t, "gpt-image-1", gjson.GetBytes(sent, "tools.0.model").Str)
						require.Equal(t, "gpt-image-1", result.UpstreamModel)
						require.Equal(t, "responses=experimental", req.Header.Get("OpenAI-Beta"))
					}
					require.Contains(t, response.Body.String(), "ZmluYWw=")
					require.Equal(t, 1, result.ImageCount)
					report.Finish(body)
					require.Empty(t, reports.requests, "reports await the committed image charge")
					usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
					billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
					billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
					price, groupID := 0.2, int64(7)
					result.RequestID = "oauth-image-policy-billing"
					err = billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: &APIKey{ID: 5, GroupID: &groupID, Group: &Group{ID: groupID, RateMultiplier: 2, ImagePrice1K: &price}}, User: &User{ID: 4}, Account: account})
					require.NoError(t, err)
					require.Equal(t, 1, billingRepo.calls)
					require.InDelta(t, 0.4, billingRepo.lastCmd.BalanceCost, 1e-10)
					payload := awaitTaskReport(t, reports)
					require.Equal(t, path, gjson.GetBytes(payload["ctx"], "path").Str)
					require.Equal(t, "0.4", gjson.GetBytes(payload["record"], "cost.usd").String())
					counts := map[string]int{}
					for _, item := range gjson.GetBytes(payload["record"], "requestPlugins").Array() {
						counts[item.Get("stage").Str]++
					}
					require.Equal(t, map[string]int{"request": 1, "ingress": 1, "outbound": 1, "egress": 1}, counts)
					require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 1 }, 5*time.Second, 5*time.Millisecond)
					require.Zero(t, s.TaskReportStats().Failed)
				})
			}
		}
	}
}

func TestNativeImagesOAuthGuardsAndOptionalAtomicity(t *testing.T) {
	for _, direct := range []bool{true, false} {
		for _, tc := range []struct{ name, stage, hook string }{
			{"reasoning effect", "request", `return {action:"continue",effects:[{type:"reasoning.set",effort:"high"}]}`},
			{"price", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:ctx.format==="openai"?"/n":"/tools/0/n",value:8}]}]}`},
			{"image model", "ingress", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:ctx.format==="openai"?"/model":"/tools/0/model",value:"gpt-image-2.6-flare"}]}]}`},
			{"media", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:ctx.format==="openai"?"/images":"/input/0/content/-",value:{type:"input_image",image_url:"https://private.invalid/input"}}]}]}`},
			{"hidden instructions", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/instructions",value:"other"}]}]}`},
			{"stream", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/stream",value:false}]}]}`},
			{"empty prompt", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:ctx.format==="openai"?"/prompt":"/input/0/content/0/text",value:" "}]}]}`},
			{"auth", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{Authorization:"private-token"}}]}`},
			{"OAuth account", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{"Chatgpt-Account-Id":"other"}}]}`},
			{"protocol", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{"OpenAI-Beta":"other"}}]}`},
			{"accept", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{Accept:"other"}}]}`},
			{"fingerprint", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{"User-Agent":"other"}}]}`},
			{"read-only egress", "egress", `return {action:"continue",effects:[{type:"header.patch",set:{"X-Late":"other"}}]}`},
			{"reject", "egress", `return {action:"reject",code:"blocked",message:"private policy reason"}`},
		} {
			t.Run(fmt.Sprintf("direct=%t/%s", direct, tc.name), func(t *testing.T) {
				source := providerPolicyMeta + `export function ` + tc.stage + `(ctx){` + tc.hook + `}`
				s, account, upstream, plan := nativeImagesOAuthPolicyFixture(t, openAIImagesGenerationsEndpoint, source, []string{tc.stage}, direct)
				body := []byte(`{"model":"gpt-image-2","prompt":"draw","n":1,"size":"1024x1024","stream":true}`)
				c, response := providerContext(openAIImagesGenerationsEndpoint, string(body))
				c.Request.Header.Set("Content-Type", "application/json")
				parsed, err := s.gateway.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)
				_, err = s.gateway.ForwardImages(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, parsed, "")
				var failure *RequestPluginFailure
				require.ErrorAs(t, err, &failure)
				require.Equal(t, tc.name == "reject", failure.Rejected)
				require.Empty(t, upstream.requests)
				require.GreaterOrEqual(t, response.Code, 400)
				require.NotContains(t, response.Body.String(), "private")
			})
		}
		t.Run(fmt.Sprintf("direct=%t/optional atomic rollback", direct), func(t *testing.T) {
			source := strings.Replace(providerPolicyMeta, "apiVersion:1,", "apiVersion:1,optional:true,", 1) + `export function outbound(ctx){return {action:"continue",effects:[{type:"header.patch",set:{"X-Atomic":"bad"}},{type:"json.patch",ops:[{op:"add",path:ctx.format==="openai"?"/n":"/tools/0/n",value:99}]}]}}`
			s, account, upstream, plan := nativeImagesOAuthPolicyFixture(t, openAIImagesGenerationsEndpoint, source, []string{"outbound"}, direct)
			body := []byte(`{"model":"gpt-image-2","prompt":"draw"}`)
			c, _ := providerContext(openAIImagesGenerationsEndpoint, string(body))
			c.Request.Header.Set("Content-Type", "application/json")
			parsed, err := s.gateway.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			nativeImagesOAuthPolicyResponse(upstream, direct, false)
			result, err := s.gateway.ForwardImages(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, parsed, "")
			require.NoError(t, err)
			require.Equal(t, 1, result.ImageCount)
			require.Len(t, upstream.requests, 1)
			require.Empty(t, upstream.requests[0].Header.Get("X-Atomic"))
			require.Equal(t, "skipped", s.repo.(*requestPluginFixtureRepo).runs[0].Records[0].Action)
		})
	}
}

func TestNativeImagesOAuthAdmissionBeforeCredentialsAndSending(t *testing.T) {
	for _, direct := range []bool{true, false} {
		for _, reason := range []string{"audit", "capacity", "canceled", "rejected before token lookup"} {
			t.Run(fmt.Sprintf("direct=%t/%s", direct, reason), func(t *testing.T) {
				source := providerPolicyMeta + `export function outbound(){return {action:"reject",code:"blocked"}}`
				s, account, upstream, plan := nativeImagesOAuthPolicyFixture(t, openAIImagesGenerationsEndpoint, source, []string{"outbound"}, direct)
				delete(account.Credentials, "access_token")
				ctx := context.Background()
				if reason == "audit" {
					s.repo.(*requestPluginFixtureRepo).runErr = errors.New("private DB error")
				} else if reason == "capacity" {
					var release func()
					ctx, release = pkghttputil.WithRequestBodyMemoryBudget(ctx, pkghttputil.NewBodyMemoryBudget(1))
					defer release()
				} else if reason == "canceled" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				body := []byte(`{"model":"gpt-image-2","prompt":"draw"}`)
				c, response := providerContext(openAIImagesGenerationsEndpoint, string(body))
				c.Request.Header.Set("Content-Type", "application/json")
				parsed, err := s.gateway.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)
				_, err = s.gateway.ForwardImages(WithRequestPluginPlan(ctx, plan), c, account, body, parsed, "")
				var failure *RequestPluginFailure
				require.ErrorAs(t, err, &failure, "policy admission must precede token lookup")
				require.Empty(t, upstream.requests)
				require.NotContains(t, response.Body.String(), "private")
			})
		}
	}
}

func TestNativeImagesOAuthScopesAndSetupToken(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		for _, direct := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/direct=%t", accountType, direct), func(t *testing.T) {
				source := strings.Replace(providerPolicyMeta, "apiVersion:1,", "apiVersion:1,scope:{channelTypes:[57]},", 1) + `
export function request(ctx,req){
 if(req.body.prompt!=="draw" || ctx.channel.type!==57)throw Error("wrong client request");
 return {action:"continue"};
}
function patch(ctx,req,marker){
 const bridge=ctx.format==="openai_responses";
 const text=bridge?req.body.input[0].content[0].text:req.body.prompt;
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:bridge?"/input/0/content/0/text":"/prompt",value:text+marker+ctx.instance.id}]}]};
}
export function ingress(ctx,req){return patch(ctx,req,"i");}
export function outbound(ctx,req){return patch(ctx,req,"o");}
export function egress(ctx,req){
 const text=ctx.format==="openai_responses"?req.body.input[0].content[0].text:req.body.prompt;
 if(text!=="drawi1i2i4o1o2o4")throw Error("wrong stage/scope order");
 return {action:"continue"};
}`
				s, account, upstream, plan := nativeImagesOAuthPolicyFixture(t, openAIImagesGenerationsEndpoint, source, []string{"request", "ingress", "outbound", "egress"}, direct)
				account.Type = accountType
				item := plan.items[0]
				global, group, other := item, item, item
				global.ID, global.Scope, global.AccountIDs = 1, "global", nil
				group.ID, group.Scope, group.GroupIDs, group.AccountIDs = 2, "group", []int64{7}, nil
				other.ID, other.AccountIDs = 9, []int64{999}
				plan.items = []RequestPluginInstance{global, group, item, other}
				body := []byte(`{"model":"gpt-image-2","prompt":"draw"}`)
				early, err := plan.Ingress(context.Background(), body)
				require.NoError(t, err)
				require.Empty(t, early.Records, "channel-scoped ingress waits for the selected OAuth account")
				c, _ := providerContext(openAIImagesGenerationsEndpoint, string(body))
				c.Request.Header.Set("Content-Type", "application/json")
				parsed, err := s.gateway.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)
				nativeImagesOAuthPolicyResponse(upstream, direct, false)
				result, err := s.gateway.ForwardImages(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, parsed, "")
				require.NoError(t, err, "runs: %+v", s.repo.(*requestPluginFixtureRepo).runs)
				require.Equal(t, 1, result.ImageCount)
				require.Len(t, upstream.requests, 1)
				sent, err := io.ReadAll(upstream.requests[0].Body)
				require.NoError(t, err)
				promptPath := "prompt"
				if !direct {
					promptPath = "input.0.content.0.text"
				}
				require.Equal(t, "drawi1i2i4o1o2o4", gjson.GetBytes(sent, promptPath).Str)
				require.Equal(t, "draw", parsed.Prompt, "policies do not mutate the reusable admission snapshot")
				counts := map[string][]int64{}
				for _, run := range s.repo.(*requestPluginFixtureRepo).runs {
					for _, record := range run.Records {
						counts[record.Stage] = append(counts[record.Stage], record.InstanceID)
					}
				}
				require.Equal(t, map[string][]int64{"request": {1, 2, 4}, "ingress": {1, 2, 4}, "outbound": {1, 2, 4}, "egress": {1, 2, 4}}, counts)
			})
		}
	}
}

func TestNativeImagesOAuthFallbackRechecksOriginalBodyAndStopsOnPolicyFailure(t *testing.T) {
	for _, rejectFallback := range []bool{false, true} {
		t.Run(fmt.Sprintf("reject-fallback=%t", rejectFallback), func(t *testing.T) {
			source := providerPolicyMeta + `
export function outbound(ctx,req){
 const bridge=ctx.format==="openai_responses";
 const text=bridge?req.body.input[0].content[0].text:req.body.prompt;
 if(text!=="draw" || req.body.user)throw Error("a previous attempt leaked into the retry");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:bridge?"/input/0/content/0/text":"/prompt",value:"sanitized"},{op:"add",path:"/user",value:"once"}]},{type:"header.patch",set:{"X-Image-Wire":ctx.format}}]};
}
export function egress(ctx){
 if(ctx.format==="openai_responses" && ` + fmt.Sprint(rejectFallback) + `)return {action:"reject",code:"blocked",message:"private fallback policy"};
 return {action:"continue"};
}`
			s, account, _, plan := nativeImagesOAuthPolicyFixture(t, openAIImagesGenerationsEndpoint, source, []string{"outbound", "egress"}, true)
			calls := 0
			s.gateway.httpUpstream = &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				calls++
				body, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Equal(t, "once", gjson.GetBytes(body, "user").Str)
				if calls == 1 {
					require.Equal(t, "/backend-api/codex/images/generations", req.URL.Path)
					require.Equal(t, "openai", req.Header.Get("X-Image-Wire"))
					require.Equal(t, "sanitized", gjson.GetBytes(body, "prompt").Str)
					return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"endpoint unavailable"}}`))}, nil
				}
				require.Equal(t, 2, calls)
				require.Equal(t, "/backend-api/codex/responses", req.URL.Path)
				require.Equal(t, "openai_responses", req.Header.Get("X-Image-Wire"))
				require.Equal(t, "sanitized", gjson.GetBytes(body, "input.0.content.0.text").Str)
				require.False(t, gjson.GetBytes(body, "stream_options").Exists())
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"image_generation_call\",\"result\":\"ZmluYWw=\"}],\"usage\":{\"input_tokens\":10,\"output_tokens\":18}}}\n\n"))}, nil
			}}
			body := []byte(`{"model":"gpt-image-2","prompt":"draw"}`)
			c, response := providerContext(openAIImagesGenerationsEndpoint, string(body))
			c.Request.Header.Set("Content-Type", "application/json")
			parsed, err := s.gateway.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			result, err := s.gateway.ForwardImages(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, parsed, "")
			if rejectFallback {
				var failure *RequestPluginFailure
				require.ErrorAs(t, err, &failure)
				require.True(t, failure.Rejected)
				require.Equal(t, 1, calls, "the rejected fallback never sends a second request")
				require.Equal(t, http.StatusForbidden, response.Code)
				require.NotContains(t, response.Body.String(), "private")
			} else {
				require.NoError(t, err)
				require.Equal(t, 2, calls)
				require.Equal(t, 1, result.ImageCount)
				require.Nil(t, result.ReasoningEffort)
			}
			require.Equal(t, "draw", parsed.Prompt)
		})
	}
}
