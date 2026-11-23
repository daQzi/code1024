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
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const geminiRequestHooks = `
export function request(ctx,req){
 if(!["claude","gemini"].includes(ctx.format)||ctx.model!=="public-text"||ctx.upstreamModel!=="gemini-3.1-pro"||req.summary.totalTextLength!==5)throw Error("request identity");
 return {action:"continue",effects:[{type:"reasoning.set",effort:"high",override:true}]};
}
export function ingress(ctx,req){
 if(!ctx.channel)return {action:"continue"};
 if(req.body.generationConfig?.topK!==undefined)throw Error("dirty retry");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/generationConfig/topK",value:4}]}]};
}
export function outbound(ctx,req){
 if(ctx.format!=="gemini"||ctx.model!=="public-text"||ctx.upstreamModel!=="gemini-3.1-pro"||req.body.generationConfig?.topK!==undefined&&req.body.generationConfig.topK!==4||req.body.generationConfig?.thinkingConfig?.thinkingLevel!=="high"||req.body.project||req.body.request||req.headers?.Authorization||req.headers?.["X-Goog-Api-Key"])throw Error("outbound identity");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/contents/0/parts/0/text",value:"hello policy"},{op:"replace",path:"/generationConfig/thinkingConfig/thinkingLevel",value:"medium"}]},{type:"header.patch",set:{"X-Policy":"checked"}}]};
}
export function egress(ctx,req){
 if(req.body.contents[0].parts[0].text!=="hello policy"||req.headers["X-Policy"]!=="checked"||req.body.generationConfig.thinkingConfig.thinkingLevel!=="medium")throw Error("egress identity");
 return {action:"continue"};
}`

type geminiPolicyHTTP struct {
	requests []*http.Request
	bodies   [][]byte
	stream   bool
	wrapped  bool
	status   []int
}

func (h *geminiPolicyHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	h.requests, h.bodies = append(h.requests, req), append(h.bodies, body)
	status := http.StatusOK
	if len(h.status) >= len(h.requests) {
		status = h.status[len(h.requests)-1]
	}
	contentType, response := "application/json", `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello secret"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":13,"candidatesTokenCount":4,"totalTokenCount":17}}`
	if status >= 400 {
		response = `{"error":{"code":400,"message":"Corrupted thought signature.","status":"INVALID_ARGUMENT"}}`
		if status == 404 {
			response = `{"error":{"code":404,"message":"model not found"}}`
		}
	} else {
		if h.wrapped {
			response = `{"response":` + response + `}`
		}
		if h.stream {
			contentType, response = "text/event-stream", "data: "+response+"\n\n"
		}
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}, "X-Request-Id": {fmt.Sprintf("gemini-policy-%d", len(h.requests))}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

func (h *geminiPolicyHTTP) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return h.Do(req, proxy, id, concurrency)
}

func geminiRequestFixture(t *testing.T, source string, stages []string, messages, antigravity bool) (*NewAPIPluginService, *Account, *RequestPluginPlan) {
	t.Helper()
	s, account, _, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+source, stages, newAPIProviderChat)
	account.Platform, account.Type = PlatformGemini, AccountTypeAPIKey
	account.Credentials = map[string]any{"api_key": "fixture-private-key", "base_url": "https://api.example.com", "model_mapping": map[string]any{"public-text": "gemini-3.1-pro"}}
	if antigravity {
		account.Platform, account.Type = PlatformAntigravity, AccountTypeOAuth
		account.Credentials["access_token"], account.Credentials["project_id"] = "fixture-private-token", "fixture-project"
	}
	plan.call.Format, plan.call.Path = "gemini", "/v1beta/models/public-text:generateContent"
	if messages {
		plan.call.Format, plan.call.Path = "claude", "/v1/messages"
	}
	plan.items[0].Formats = []string{plan.call.Format}
	s.gateway.accountRepo = &stubOpenAIAccountRepo{accounts: []Account{*account}}
	return s, account, plan
}

func TestGeminiNativeRequestStagesActualWireAndOriginalUsage(t *testing.T) {
	for _, ag := range []bool{false, true} {
		for _, messages := range []bool{false, true} {
			for _, scope := range []string{"global", "group", "account"} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("antigravity=%t/messages=%t/%s/stream=%t", ag, messages, scope, stream), func(t *testing.T) {
						s, account, plan := geminiRequestFixture(t, geminiRequestHooks, []string{"ingress", "request", "outbound", "egress"}, messages, ag)
						plan.items[0].Scope = scope
						if scope == "group" {
							plan.items[0].GroupIDs = []int64{7}
						}
						body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":4096}}`)
						if messages {
							body = []byte(fmt.Sprintf(`{"model":"public-text","max_tokens":4096,"stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream))
						}
						if scope != "account" {
							early, err := plan.Ingress(context.Background(), body)
							require.NoError(t, err)
							body = early.Body
						}
						c, recorder := providerContext(plan.call.Path, string(body))
						original := c.Request
						upstream := &geminiPolicyHTTP{stream: stream || ag, wrapped: ag}
						ctx := WithRequestPluginPlan(c.Request.Context(), plan)
						var result *ForwardResult
						var err error
						if ag {
							gateway := newAntigravityCompatService(config.GatewayConfig{MaxLineSize: defaultMaxLineSize}, upstream)
							if messages {
								result, err = gateway.Forward(ctx, c, account, body, false)
							} else {
								result, err = gateway.ForwardGemini(ctx, c, account, "public-text", "generateContent", stream, body, false)
							}
						} else {
							gateway := newGeminiSignalService("", "")
							gateway.httpUpstream = upstream
							if messages {
								result, err = gateway.Forward(ctx, c, account, body)
							} else {
								result, err = gateway.ForwardNative(ctx, c, account, "public-text", "generateContent", stream, body)
							}
						}
						require.NoError(t, err, "response: %s; audit: %+v", recorder.Body, s.repo.(*requestPluginFixtureRepo).runs)
						require.NotNil(t, result)
						require.Equal(t, 13, result.Usage.InputTokens)
						require.Equal(t, 4, result.Usage.OutputTokens)
						require.Equal(t, "medium", *result.ReasoningEffort, "billing uses the final native level")
						require.Same(t, original, c.Request)
						require.Contains(t, recorder.Body.String(), "hello secret")
						require.Len(t, upstream.bodies, 1)
						require.Equal(t, "checked", upstream.requests[0].Header.Get("X-Policy"))
						wire := upstream.bodies[0]
						if ag {
							require.Equal(t, "fixture-project", gjson.GetBytes(wire, "project").String())
							require.Equal(t, "gemini-3.1-pro", gjson.GetBytes(wire, "model").String())
							wire = []byte(gjson.GetBytes(wire, "request").Raw)
							require.Contains(t, upstream.requests[0].Header.Get("Authorization"), "fresh-oauth-token")
						} else {
							require.Equal(t, "fixture-private-key", upstream.requests[0].Header.Get("X-Goog-Api-Key"))
						}
						require.Equal(t, "hello policy", gjson.GetBytes(wire, "contents.0.parts.0.text").String())
						require.Equal(t, "medium", gjson.GetBytes(wire, "generationConfig.thinkingConfig.thinkingLevel").String())
						if scope == "account" {
							require.EqualValues(t, 4, gjson.GetBytes(wire, "generationConfig.topK").Int())
						}
						counts := map[string]int{}
						for _, record := range plan.records {
							counts[record.Stage]++
						}
						for _, run := range s.repo.(*requestPluginFixtureRepo).runs {
							for _, record := range run.Records {
								counts[record.Stage]++
							}
						}
						require.Equal(t, map[string]int{"ingress": 1, "request": 1, "outbound": 1, "egress": 1}, counts, "client ingress never repeats at the account boundary")
					})
				}
			}
		}
	}
}

func TestGeminiNativeRequestGuardsAndOptionalAtomicity(t *testing.T) {
	for _, tc := range []struct{ name, stage, hook string }{
		{"image-price", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/generationConfig/imageConfig",value:{imageSize:"4K"}}]}]}`},
		{"modality", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/generationConfig/responseModalities",value:["IMAGE"]}]}]}`},
		{"candidates", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/generationConfig/candidate_count",value:9}]}]}`},
		{"future-price", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/generationConfig/futurePaidOption",value:true}]}]}`},
		{"output-limit", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/generationConfig/maxOutputTokens",value:9999}]}]}`},
		{"cached-content", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/cachedContent",value:"private-cache"}]}]}`},
		{"alias", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/GenerationConfig",value:{candidateCount:9}}]}]}`},
		{"key", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{"X-Goog-Api-Key":"PRIVATE"}}]}`},
		{"project-header", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{"X-Goog-User-Project":"other"}}]}`},
		{"egress-readonly", "egress", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/temperature",value:0.1}]}]}`},
		{"deny", "egress", `return {action:"reject",code:"blocked",message:"PRIVATE"}`},
	} {
		for _, optional := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/optional=%t", tc.name, optional), func(t *testing.T) {
				s, account, plan := geminiRequestFixture(t, "export function "+tc.stage+"(){"+tc.hook+"}", []string{tc.stage}, false, false)
				plan.items[0].Version.Meta.Optional = optional
				body := []byte(`{"contents":[],"generationConfig":{"maxOutputTokens":16}}`)
				c, recorder := providerContext(plan.call.Path, string(body))
				h := &geminiPolicyHTTP{}
				gateway := newGeminiSignalService("", "")
				gateway.httpUpstream = h
				_, err := gateway.ForwardNative(WithRequestPluginPlan(c.Request.Context(), plan), c, account, "public-text", "generateContent", false, body)
				if optional && tc.name != "deny" {
					require.NoError(t, err, recorder.Body.String())
					require.Len(t, h.bodies, 1)
					require.JSONEq(t, string(body), string(h.bodies[0]), "all invalid effects roll back atomically")
				} else {
					var failure *RequestPluginFailure
					require.ErrorAs(t, err, &failure)
					require.Empty(t, h.bodies)
					require.True(t, HasOpsClientBusinessLimited(c))
					require.EqualValues(t, recorder.Code, gjson.Get(recorder.Body.String(), "error.code").Int())
					require.Contains(t, []string{"UNAVAILABLE", "PERMISSION_DENIED"}, gjson.Get(recorder.Body.String(), "error.status").String())
				}
				require.NotContains(t, recorder.Body.String(), "PRIVATE")
				require.NotEmpty(t, s.repo.(*requestPluginFixtureRepo).runs)
			})
		}
	}
}

func TestGeminiNativeRequestCredentialsAndBudgetBillingSemantics(t *testing.T) {
	for _, kind := range []string{"oauth-direct", "oauth-project", "vertex-api-key", "api-key-budget"} {
		t.Run(kind, func(t *testing.T) {
			_, account, plan := geminiRequestFixture(t, `export function request(){return {action:"continue",effects:[{type:"reasoning.set",effort:"high",override:true}]};} export function outbound(){return {action:"continue",effects:[{type:"header.patch",set:{"X-Policy":"checked"}}]};}`, []string{"request", "outbound"}, false, false)
			model := "gemini-3.1-pro"
			gateway := newGeminiSignalService("", "")
			gateway.tokenProvider = &GeminiTokenProvider{}
			if strings.HasPrefix(kind, "oauth") {
				account.Type = AccountTypeOAuth
				account.Credentials["access_token"] = "fixture-private-token"
				account.Credentials["expires_at"] = time.Now().Add(time.Hour).Format(time.RFC3339)
				if kind == "oauth-project" {
					account.Credentials["project_id"] = "fixture-project"
				}
			} else if kind == "vertex-api-key" {
				account.Type, account.Credentials = AccountTypeServiceAccount, geminiVertexAPIKeyTestAccount("").Credentials
				account.Credentials["model_mapping"] = map[string]any{"public-text": model}
			} else {
				model = "gemini-2.5-flash"
				account.Credentials["model_mapping"] = map[string]any{"public-text": model}
			}
			h := &geminiPolicyHTTP{wrapped: kind == "oauth-project"}
			gateway.httpUpstream = h
			body := []byte(`{"contents":[],"generationConfig":{"maxOutputTokens":4096}}`)
			c, recorder := providerContext(plan.call.Path, string(body))
			publicModel := "public-text"
			if strings.HasPrefix(kind, "oauth") {
				publicModel, plan.call.Model = model, model // Native OAuth intentionally does not use API-key model mapping.
			}
			result, err := gateway.ForwardNative(WithRequestPluginPlan(c.Request.Context(), plan), c, account, publicModel, "generateContent", false, body)
			require.NoError(t, err, recorder.Body.String())
			require.Len(t, h.bodies, 1)
			require.Equal(t, "checked", h.requests[0].Header.Get("X-Policy"))
			if kind == "api-key-budget" {
				require.Nil(t, result.ReasoningEffort, "token budgets never synthesize a billable Gemini level")
				require.Positive(t, gjson.GetBytes(h.bodies[0], "generationConfig.thinkingConfig.thinkingBudget").Int())
			} else {
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, "high", *result.ReasoningEffort)
			}
			if strings.HasPrefix(kind, "oauth") {
				require.Equal(t, "Bearer fixture-private-token", h.requests[0].Header.Get("Authorization"))
				if kind == "oauth-project" {
					require.Equal(t, "fixture-project", gjson.GetBytes(h.bodies[0], "project").String())
					require.Equal(t, model, gjson.GetBytes(h.bodies[0], "model").String())
				}
			} else if kind == "vertex-api-key" {
				requireVertexAPIKeyAuth(t, h.requests[0])
			}
		})
	}
}

func TestGeminiNativeRequestAdmissionAndOfflineStages(t *testing.T) {
	s, account, plan := geminiRequestFixture(t, `export function request(){return {action:"continue",effects:[{type:"reasoning.set",effort:"high"}]};} export function ingress(){return {action:"continue"};} export function outbound(){return {action:"continue"};} export function egress(){return {action:"continue"};}`, []string{"ingress", "request", "outbound", "egress"}, false, false)
	input := RequestPluginInstance{Name: "Gemini native", PluginVersionID: 2, Scope: "account", AccountIDs: []int64{account.ID}, Formats: []string{"gemini"}, Stages: []string{"ingress", "request", "outbound", "egress"}, Config: json.RawMessage(`{}`)}
	_, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.NoError(t, err)
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
		copy := *account
		copy.Platform = platform
		s.gateway.accountRepo = &stubOpenAIAccountRepo{accounts: []Account{copy}}
		_, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
		require.Error(t, err, "a Gemini rule cannot be bound to an account with an unimplemented Gemini ingress")
	}
	s.gateway.accountRepo = &stubOpenAIAccountRepo{accounts: []Account{*account}}
	plan.call.Model = "gemini-3.1-pro"
	for _, stage := range []string{"request", "late_ingress", "outbound", "egress"} {
		out, err := s.DryRunRequestPluginStage(context.Background(), 2, input.Config, json.RawMessage(`{"contents":[],"generationConfig":{"maxOutputTokens":4096}}`), plan.call, stage)
		require.NoError(t, err)
		require.Empty(t, out.ErrorClass, stage)
		if stage == "request" {
			require.Equal(t, "high", gjson.GetBytes(out.Body, "generationConfig.thinkingConfig.thinkingLevel").String())
		}
	}
	for _, scenario := range []string{"memory", "audit", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			s, account, plan := geminiRequestFixture(t, `export function outbound(){return {action:"continue"};}`, []string{"outbound"}, false, false)
			ctx := context.Background()
			if scenario == "memory" {
				var release func()
				ctx, release = pkghttputil.WithRequestBodyMemoryBudget(ctx, pkghttputil.NewBodyMemoryBudget(1))
				defer release()
			} else if scenario == "audit" {
				s.repo.(*requestPluginFixtureRepo).runErr = errors.New("PRIVATE")
			} else {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			h := &geminiPolicyHTTP{}
			gateway := newGeminiSignalService("", "")
			gateway.httpUpstream = h
			c, recorder := providerContext(plan.call.Path, `{}`)
			_, err := gateway.ForwardNative(WithRequestPluginPlan(ctx, plan), c, account, "public-text", "generateContent", false, []byte(`{"contents":[]}`))
			require.Error(t, err)
			require.Empty(t, h.requests)
			require.NotContains(t, recorder.Body.String(), "PRIVATE")
		})
	}
}

func TestGeminiNativeRequestRetriesUseFreshBodiesAndStopOnPolicyFailure(t *testing.T) {
	for _, ag := range []bool{false, true} {
		for _, rejectRetry := range []bool{false, true} {
			t.Run(fmt.Sprintf("antigravity=%t/reject-retry=%t", ag, rejectRetry), func(t *testing.T) {
				source := `
export function request(){return {action:"continue",effects:[{type:"reasoning.set",effort:"high",override:true}]};}
export function ingress(ctx,req){
 if(req.body.generationConfig.topK!==undefined)throw Error("dirty retry");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/generationConfig/topK",value:4}]}]};
}
export function outbound(ctx,req){
 if(req.body.generationConfig.topK!==4)throw Error("missing ingress");
 return {action:"continue",effects:[{type:"header.patch",set:{"X-Policy":"checked"}}]};
}`
				s, account, plan := geminiRequestFixture(t, source, []string{"ingress", "request", "outbound"}, false, ag)
				s.repo.(*requestPluginFixtureRepo).items[0].Enabled = false
				h := &geminiRetryPolicyHTTP{geminiPolicyHTTP: geminiPolicyHTTP{stream: ag, wrapped: ag, status: []int{500, 200}}, repo: s.repo.(*requestPluginFixtureRepo), rejectRetry: rejectRetry}
				body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":4096}}`)
				if ag {
					// A signature repair is an existing retry path with a fresh envelope.
					h.status[0] = 400
					body = []byte(`{"contents":[{"role":"model","parts":[{"text":"hello","thoughtSignature":"old-signature","thought":true}]}],"generationConfig":{"maxOutputTokens":4096}}`)
				}
				c, recorder := providerContext(plan.call.Path, string(body))
				ctx := WithRequestPluginPlan(c.Request.Context(), plan)
				var err error
				if ag {
					gateway := newAntigravityCompatService(config.GatewayConfig{MaxLineSize: defaultMaxLineSize}, h)
					_, err = gateway.ForwardGemini(ctx, c, account, "public-text", "generateContent", false, body, false)
				} else {
					gateway := newGeminiSignalService("", "")
					gateway.cfg.Gateway.MaxSameAccountRetriesGemini = 1
					gateway.httpUpstream = h
					_, err = gateway.ForwardNative(ctx, c, account, "public-text", "generateContent", false, body)
				}
				if rejectRetry {
					var failure *RequestPluginFailure
					require.ErrorAs(t, err, &failure)
					require.Equal(t, "audit", failure.Class)
					require.Len(t, h.requests, 1, "a failed required policy never sends the second attempt")
				} else {
					require.NoError(t, err, "response: %s; audit: %+v", recorder.Body, s.repo.(*requestPluginFixtureRepo).runs)
					require.Len(t, h.bodies, 2)
					counts := map[string]int{}
					for _, run := range s.repo.(*requestPluginFixtureRepo).runs {
						for _, record := range run.Records {
							counts[record.Stage]++
						}
					}
					require.Equal(t, map[string]int{"request": 1, "ingress": 2, "outbound": 2}, counts)
					for i, wire := range h.bodies {
						if ag {
							wire = []byte(gjson.GetBytes(wire, "request").Raw)
						}
						require.EqualValues(t, 4, gjson.GetBytes(wire, "generationConfig.topK").Int())
						require.Equal(t, "checked", h.requests[i].Header.Get("X-Policy"))
					}
				}
			})
		}
	}
}

type geminiRetryPolicyHTTP struct {
	geminiPolicyHTTP
	repo        *requestPluginFixtureRepo
	rejectRetry bool
}

func (h *geminiRetryPolicyHTTP) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	response, err := h.geminiPolicyHTTP.Do(req, proxy, accountID, concurrency)
	if h.rejectRetry {
		h.repo.runErr = errors.New("PRIVATE audit failure")
	}
	return response, err
}

func (h *geminiRetryPolicyHTTP) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return h.Do(req, proxy, id, concurrency)
}

func TestGeminiNativeRequestFallbackRendersActualModelAndMayReject(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprintf("reject-fallback=%t", reject), func(t *testing.T) {
			source := `
export function request(){return {action:"continue",effects:[{type:"reasoning.set",effort:"high",override:true}]};}
export function outbound(ctx,req){
 if(ctx.upstreamModel==="gemini-2.5-flash"){
  if(req.body.generationConfig.thinkingConfig.thinkingLevel||!req.body.generationConfig.thinkingConfig.thinkingBudget)throw Error("fallback renderer");
  REJECT
 }
 return {action:"continue",effects:[{type:"header.patch",set:{"X-Actual-Model":ctx.upstreamModel}}]};
}`
			decision := ""
			if reject {
				decision = `return {action:"reject",code:"blocked",message:"PRIVATE"};`
			}
			_, account, plan := geminiRequestFixture(t, strings.Replace(source, "REJECT", decision, 1), []string{"request", "outbound"}, false, true)
			h := &geminiPolicyHTTP{stream: true, wrapped: true, status: []int{404, 200}}
			gateway := newAntigravityCompatService(config.GatewayConfig{MaxLineSize: defaultMaxLineSize}, h)
			gateway.settingService = NewSettingService(&antigravitySettingRepoStub{values: map[string]string{SettingKeyEnableModelFallback: "true", SettingKeyFallbackModelAntigravity: "gemini-2.5-flash"}}, &config.Config{})
			body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":4096}}`)
			c, recorder := providerContext(plan.call.Path, string(body))
			result, err := gateway.ForwardGemini(WithRequestPluginPlan(c.Request.Context(), plan), c, account, "public-text", "generateContent", false, body, false)
			if reject {
				var failure *RequestPluginFailure
				require.ErrorAs(t, err, &failure)
				require.True(t, failure.Rejected)
				require.Len(t, h.requests, 1)
				require.NotContains(t, recorder.Body.String(), "PRIVATE")
			} else {
				require.NoError(t, err, recorder.Body.String())
				require.Len(t, h.requests, 2)
				require.Equal(t, "gemini-2.5-flash", result.UpstreamModel)
				require.Equal(t, "gemini-2.5-flash", h.requests[1].Header.Get("X-Actual-Model"))
				require.Nil(t, result.ReasoningEffort, "fallback to budget-based Gemini cannot keep the old level for billing")
			}
		})
	}
}

func TestGeminiNativeRequestAntigravityUpstreamPassthrough(t *testing.T) {
	s, account, plan := geminiRequestFixture(t, `export function request(){return {action:"continue",effects:[{type:"reasoning.set",effort:"high"}]};} export function ingress(){throw Error("passthrough must skip late ingress");} export function outbound(){throw Error("passthrough must skip outbound");} export function egress(ctx,req){if(req.body.output_config.effort!=="high"||ctx.format!=="claude")throw Error("wrong final body");return {action:"continue"};}`, []string{"ingress", "request", "outbound", "egress"}, true, true)
	account.Type = AccountTypeUpstream
	account.Credentials["base_url"], account.Credentials["api_key"] = "https://api.example.com", "fixture-passthrough-key"
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}],"max_tokens":4096}`)
	plan.call.Model = "claude-sonnet-4-6"
	h := &anthropicHTTPUpstreamRecorder{resp: claudeRequestResponse(false)}
	gateway := newAntigravityCompatService(config.GatewayConfig{}, h)
	c, recorder := providerContext(plan.call.Path, string(body))
	result, err := gateway.Forward(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, false)
	require.NoError(t, err, recorder.Body.String())
	require.Equal(t, "Bearer fixture-passthrough-key", h.lastReq.Header.Get("Authorization"))
	require.Equal(t, "fixture-passthrough-key", h.lastReq.Header.Get("X-Api-Key"))
	require.Equal(t, "high", *result.ReasoningEffort)
	counts := map[string]string{}
	for _, run := range s.repo.(*requestPluginFixtureRepo).runs {
		for _, record := range run.Records {
			counts[record.Stage] = record.Action
		}
	}
	require.Equal(t, map[string]string{"request": "continue", "ingress": "unchecked", "outbound": "unchecked", "egress": "continue"}, counts)
}
