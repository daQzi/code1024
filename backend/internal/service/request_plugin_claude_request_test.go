//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const claudeRequestHooks = `
export function request(ctx,req){
 if(ctx.format!=="claude"||ctx.model!=="public-text"||ctx.upstreamModel!=="claude-sonnet-4-6"||req.summary.totalTextLength!==5||req.body.model!=="public-text")throw Error("request identity");
 return {action:"continue",effects:[{type:"reasoning.set",effort:"high",override:true}]};
}
export function ingress(ctx,req){
 if(!ctx.channel)return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/late_marker",value:true}]}]};
 if(req.body.metadata?.policy||req.body.late_marker)throw Error("dirty retry");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/late_marker",value:true}]}]};
}
export function outbound(ctx,req){
 if(ctx.format!=="claude"||ctx.upstreamModel!=="claude-sonnet-4-6"||!req.body.late_marker||req.body.output_config.effort!=="high"||req.headers?.Authorization||req.headers?.["X-Api-Key"])throw Error("outbound identity");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/metadata/policy",value:"checked"}]},{type:"header.patch",set:{"X-Policy":"checked"}}]};
}
export function egress(ctx,req){
 if(req.body.metadata.policy!=="checked"||req.headers["X-Policy"]!=="checked"||req.headers?.Authorization)throw Error("egress identity");
 return {action:"continue"};
}`

func claudeRequestFixture(t *testing.T, source string, stages []string) (*NewAPIPluginService, *Account, *RequestPluginPlan) {
	t.Helper()
	s, account, _, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+source, stages, newAPIProviderChat)
	account.Platform, account.Type = PlatformAnthropic, AccountTypeAPIKey
	account.Credentials = map[string]any{"api_key": "fixture-private-key", "base_url": "https://api.example.com", "model_mapping": map[string]any{"public-text": "claude-sonnet-4-6"}}
	plan.call.Format, plan.call.Path = "claude", "/v1/messages"
	plan.items[0].Formats = []string{"claude"}
	s.gateway.accountRepo = &stubOpenAIAccountRepo{accounts: []Account{*account}}
	return s, account, plan
}

func claudeRequestResponse(stream bool) *http.Response {
	body := `{"id":"msg-policy","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":13,"output_tokens":4}}`
	contentType := "application/json"
	if stream {
		contentType = "text/event-stream"
		body = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-policy\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":13}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":4}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestClaudeNativeRequestStagesActualWireAndOriginalUsage(t *testing.T) {
	for _, scope := range []string{"global", "group", "account"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", scope, stream), func(t *testing.T) {
				s, account, plan := claudeRequestFixture(t, claudeRequestHooks, []string{"ingress", "request", "outbound", "egress"})
				plan.items[0].Scope = scope
				if scope == "group" {
					plan.items[0].GroupIDs = []int64{7}
				}
				body := []byte(fmt.Sprintf(`{"model":"public-text","max_tokens":4096,"stream":%t,"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"client-owner"}}`, stream))
				if scope != "account" {
					early, err := plan.Ingress(context.Background(), body)
					require.NoError(t, err)
					body = early.Body
				}
				c, recorder := providerContext("/v1/messages", string(body))
				originalRequest := c.Request
				upstream := &anthropicHTTPUpstreamRecorder{resp: claudeRequestResponse(stream)}
				gateway := newForwardPartialUsageServiceForTest(upstream)
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, err)
				result, err := gateway.Forward(WithRequestPluginPlan(c.Request.Context(), plan), c, account, parsed)
				require.NoError(t, err, "audit: %+v", s.repo.(*requestPluginFixtureRepo).runs)
				require.NotNil(t, result)
				require.Equal(t, 13, result.Usage.InputTokens)
				require.Equal(t, 4, result.Usage.OutputTokens)
				require.Equal(t, "high", *result.ReasoningEffort)
				require.Same(t, originalRequest, c.Request)
				require.Contains(t, recorder.Body.String(), "hello")
				require.Equal(t, "fixture-private-key", getHeaderRaw(upstream.lastReq.Header, "x-api-key"))
				require.Equal(t, "checked", upstream.lastReq.Header.Get("X-Policy"))
				require.Equal(t, "checked", gjson.GetBytes(upstream.lastBody, "metadata.policy").String())
				require.Equal(t, "client-owner", gjson.GetBytes(upstream.lastBody, "metadata.user_id").String())
				require.Equal(t, "claude-sonnet-4-6", gjson.GetBytes(upstream.lastBody, "model").String())
			})
		}
	}
}

func TestClaudeNativeRequestGuards(t *testing.T) {
	for _, tc := range []struct{ name, stage, hook string }{
		{"route", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/model",value:"other"}]}]}`},
		{"speed", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/speed",value:"fast"}]}]}`},
		{"case-speed", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/Speed",value:"fast"}]}]}`},
		{"owner", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/metadata/user_id",value:"other"}]}]}`},
		{"auth", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{"X-Api-Key":"other"}}]}`},
		{"beta", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{"Anthropic-Beta":"other"}}]}`},
		{"egress-readonly", "egress", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/temperature",value:0.5}]}]}`},
		{"deny", "egress", `return {action:"reject",code:"blocked",message:"PRIVATE"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, account, plan := claudeRequestFixture(t, "export function "+tc.stage+"(){"+tc.hook+"}", []string{tc.stage})
			body := []byte(`{"model":"public-text","messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"owner"}}`)
			c, recorder := providerContext("/v1/messages", string(body))
			upstream := &anthropicHTTPUpstreamRecorder{resp: claudeRequestResponse(false)}
			gateway := newForwardPartialUsageServiceForTest(upstream)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			_, err = gateway.Forward(WithRequestPluginPlan(c.Request.Context(), plan), c, account, parsed)
			var failure *RequestPluginFailure
			require.ErrorAs(t, err, &failure)
			require.Nil(t, upstream.lastReq)
			require.True(t, HasOpsClientBusinessLimited(c))
			require.NotContains(t, recorder.Body.String(), "PRIVATE")
			require.Equal(t, "error", gjson.Get(recorder.Body.String(), "type").String())
			require.NotEmpty(t, s.repo.(*requestPluginFixtureRepo).runs)
		})
	}
}

func TestClaudeNativeRequestOptionalFailureIsAtomicAndAuditFailureStops(t *testing.T) {
	for _, auditFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("audit=%t", auditFails), func(t *testing.T) {
			s, account, plan := claudeRequestFixture(t, `export function outbound(){return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/partial",value:true},{op:"add",path:"/speed",value:"fast"}]}]};}`, []string{"outbound"})
			plan.items[0].Version.Meta.Optional = true
			if auditFails {
				s.repo.(*requestPluginFixtureRepo).runErr = io.ErrClosedPipe
			}
			body := []byte(`{"model":"public-text","messages":[{"role":"user","content":"hello"}]}`)
			c, recorder := providerContext("/v1/messages", string(body))
			upstream := &anthropicHTTPUpstreamRecorder{resp: claudeRequestResponse(false)}
			gateway := newForwardPartialUsageServiceForTest(upstream)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			_, err = gateway.Forward(WithRequestPluginPlan(c.Request.Context(), plan), c, account, parsed)
			if auditFails {
				var failure *RequestPluginFailure
				require.ErrorAs(t, err, &failure)
				require.Equal(t, "audit", failure.Class)
				require.Nil(t, upstream.lastReq)
			} else {
				require.NoError(t, err, recorder.Body.String())
				require.False(t, gjson.GetBytes(upstream.lastBody, "partial").Exists())
				require.False(t, gjson.GetBytes(upstream.lastBody, "speed").Exists())
				require.Equal(t, "skipped", s.repo.(*requestPluginFixtureRepo).runs[0].Records[0].Action)
			}
		})
	}
}

func TestClaudeNativeRequestSaveAndOfflineStages(t *testing.T) {
	s, account, plan := claudeRequestFixture(t, claudeRequestHooks, []string{"ingress", "request", "outbound", "egress"})
	input := RequestPluginInstance{Name: "Claude native", PluginVersionID: 2, Scope: "account", AccountIDs: []int64{account.ID}, Formats: []string{"claude"}, Stages: []string{"ingress", "request", "outbound", "egress"}, Config: json.RawMessage(`{}`)}
	_, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.NoError(t, err)
	body := json.RawMessage(`{"model":"public-text","max_tokens":4096,"messages":[{"role":"user","content":"hello"}]}`)
	out, err := s.DryRunRequestPluginStage(context.Background(), 2, input.Config, body, plan.call, "request")
	// Offline trials use the supplied public name as upstream identity.
	require.NoError(t, err)
	require.Equal(t, "execution", out.ErrorClass)
	for _, scope := range []string{"global", "group", "account"} {
		require.NoError(t, validateRequestPluginStages(input.Stages, []string{"claude"}, scope))
		require.NoError(t, validateRequestPluginStages(input.Stages, []string{"gemini"}, scope))
	}
	s, _, plan = claudeRequestFixture(t, `
export function request(){return {action:"continue",effects:[{type:"reasoning.set",effort:"low"}]};}
export function ingress(){return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/speed",value:"fast"}]}]};}
export function outbound(){return {action:"continue",effects:[{type:"header.patch",set:{"X-Policy":"checked"}}]};}
export function egress(){return {action:"continue"};}`, []string{"request", "outbound", "egress"})
	plan.call.Model = "claude-sonnet-4-6"
	for _, stage := range []string{"request", "outbound", "egress", "late_ingress"} {
		out, err := s.DryRunRequestPluginStage(context.Background(), 2, json.RawMessage(`{}`), body, plan.call, stage)
		require.NoError(t, err)
		if stage == "late_ingress" {
			require.Equal(t, "guard", out.ErrorClass)
		} else {
			require.Empty(t, out.ErrorClass, stage)
			if stage == "outbound" {
				require.Equal(t, "checked", out.Headers["X-Policy"])
			}
		}
	}
}

type claudePolicyRetryHTTP struct {
	requests []*http.Request
	bodies   [][]byte
	reject   bool
}

func (h *claudePolicyRetryHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	h.requests, h.bodies = append(h.requests, req), append(h.bodies, body)
	if h.reject && len(h.bodies) == 1 {
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"overloaded_error","message":"retry"}}`))}, nil
	}
	return claudeRequestResponse(false), nil
}

func (h *claudePolicyRetryHTTP) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return h.Do(req, proxy, id, concurrency)
}

func TestClaudeNativeRequestRetryUsesFreshWireAndPinnedRules(t *testing.T) {
	s, account, plan := claudeRequestFixture(t, claudeRequestHooks, []string{"ingress", "request", "outbound", "egress"})
	account.Credentials["custom_error_codes_enabled"] = true
	account.Credentials["custom_error_codes"] = []any{float64(400)}
	// Disabling the live instance does not mutate this request's pinned plan.
	s.repo.(*requestPluginFixtureRepo).items[0].Enabled = false
	h := &claudePolicyRetryHTTP{reject: true}
	gateway := newForwardPartialUsageServiceForTest(nil)
	gateway.httpUpstream = h
	body := []byte(`{"model":"public-text","max_tokens":4096,"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"owner"}}`)
	c, recorder := providerContext("/v1/messages", string(body))
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	_, err = gateway.Forward(WithRequestPluginPlan(c.Request.Context(), plan), c, account, parsed)
	require.NoError(t, err, recorder.Body.String())
	require.Len(t, h.bodies, 2)
	require.JSONEq(t, string(h.bodies[0]), string(h.bodies[1]))
	counts := map[string]int{}
	for _, run := range s.repo.(*requestPluginFixtureRepo).runs {
		for _, record := range run.Records {
			counts[record.Stage]++
		}
	}
	require.Equal(t, map[string]int{"request": 1, "ingress": 2, "outbound": 2, "egress": 2}, counts)
}

func TestClaudeNativeRequestOAuthPassthroughVertexAndBedrock(t *testing.T) {
	for _, mode := range []string{"oauth", "setup-token", "passthrough", "vertex", "bedrock-key", "bedrock-sigv4"} {
		t.Run(mode, func(t *testing.T) {
			source := `
export function ingress(ctx,req){if(req.body.policy_marker)throw Error("dirty body");return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/policy_marker",value:true}]}]};}
export function outbound(ctx,req){if(!req.body.policy_marker||ctx.format!=="claude"||req.headers?.Authorization)throw Error("wrong wire");return {action:"continue",effects:[{type:"header.patch",set:{"X-Policy":"checked"}}]};}
export function egress(ctx,req){return {action:"continue"};}`
			if mode == "passthrough" {
				source = `export function ingress(){throw Error("must skip")};export function outbound(){throw Error("must skip")};export function egress(ctx,req){if(req.body.policy_marker)throw Error("mutated passthrough");return {action:"continue"};}`
			}
			s, account, plan := claudeRequestFixture(t, source, []string{"ingress", "outbound", "egress"})
			if mode == "oauth" || mode == "setup-token" {
				account.Type = AccountTypeOAuth
				if mode == "setup-token" {
					account.Type = AccountTypeSetupToken
				}
				account.Credentials["access_token"] = "fixture-oauth-token"
			}
			if mode == "passthrough" {
				account.Extra = map[string]any{"anthropic_passthrough": true}
			}
			if strings.HasPrefix(mode, "bedrock-") {
				account.Type = AccountTypeBedrock
				account.Credentials["aws_access_key_id"], account.Credentials["aws_secret_access_key"] = "fixture-ak", "fixture-sk"
				account.Credentials["aws_region"] = "us-east-1"
				if mode == "bedrock-key" {
					account.Credentials["auth_mode"] = "apikey"
				}
			}
			body := []byte(`{"model":"public-text","max_tokens":4096,"messages":[{"role":"user","content":"hello"}]}`)
			c, recorder := providerContext("/v1/messages", string(body))
			originalRequest := c.Request
			h := &claudePolicyRetryHTTP{}
			gateway := newForwardPartialUsageServiceForTest(nil)
			gateway.httpUpstream = h
			ctx := WithRequestPluginPlan(c.Request.Context(), plan)
			if mode == "vertex" {
				account.Type = AccountTypeServiceAccount
				account.Credentials["project_id"] = "fixture-project"
				child, requestBody, finish, err := beginNativeProtocolRequest(ctx, c, account, body, "claude-sonnet-4-6", "claude")
				require.NoError(t, err)
				req, err := gateway.buildUpstreamRequestAnthropicVertex(child, c, account, requestBody, "fixture-vertex-token", "claude-sonnet-4-6", false)
				finish(&err)
				require.NoError(t, err)
				require.Same(t, originalRequest, c.Request)
				wire, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.True(t, gjson.GetBytes(wire, "policy_marker").Bool())
				require.False(t, gjson.GetBytes(wire, "model").Exists())
				require.Equal(t, "checked", req.Header.Get("X-Policy"))
				require.Equal(t, "Bearer fixture-vertex-token", getHeaderRaw(req.Header, "Authorization"))
				return
			}
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			result, err := gateway.Forward(ctx, c, account, parsed)
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			require.Equal(t, 4, result.Usage.OutputTokens)
			require.Len(t, h.bodies, 1)
			require.Same(t, originalRequest, c.Request)
			if mode == "passthrough" {
				require.False(t, gjson.GetBytes(h.bodies[0], "policy_marker").Exists())
				records := s.repo.(*requestPluginFixtureRepo).runs[0].Records
				require.Equal(t, "unchecked", records[0].Action)
				require.Equal(t, "unchecked", records[1].Action)
				return
			}
			require.True(t, gjson.GetBytes(h.bodies[0], "policy_marker").Bool())
			require.Equal(t, "checked", h.requests[0].Header.Get("X-Policy"))
			if mode == "oauth" || mode == "setup-token" {
				require.Equal(t, "Bearer fixture-oauth-token", getHeaderRaw(h.requests[0].Header, "Authorization"))
			} else if mode == "bedrock-key" {
				require.Equal(t, "Bearer fixture-private-key", h.requests[0].Header.Get("Authorization"))
			} else {
				req := h.requests[0]
				require.Contains(t, req.Header.Get("Authorization"), "x-policy")
				signedAt, err := time.Parse("20060102T150405Z", req.Header.Get("X-Amz-Date"))
				require.NoError(t, err)
				signer, err := NewBedrockSignerFromAccount(account)
				require.NoError(t, err)
				copy := req.Clone(context.Background())
				copy.Header.Del("Authorization")
				err = signer.signer.SignHTTP(context.Background(), signer.credentials, copy, sha256Hash(h.bodies[0]), "bedrock", signer.region, signedAt)
				require.NoError(t, err)
				require.Equal(t, req.Header.Get("Authorization"), copy.Header.Get("Authorization"), "signature must cover processed body and business headers")
			}
		})
	}
}
