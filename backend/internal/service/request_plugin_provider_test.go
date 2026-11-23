//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const providerPolicyMeta = `export const meta={apiVersion:1,key:"provider-policy",name:"Policy",version:"1.0.0",author:{name:"Fixture"},configSchema:{type:"object",properties:{effort:{type:"string",default:"high",title:"Effort"}}}};`
const providerPolicyHooks = `
export function request(ctx,req){
 if(ctx.model!=="public-text" || ctx.upstreamModel!=="vendor-text" || ctx.channel.id!==17 || ctx.channel.type!==61 || req.body.model!=="public-text" || req.summary.messageCount!==1 || req.summary.totalTextLength!==5) throw Error("wrong request context");
 return {action:"continue",effects:[{type:"reasoning.set",effort:ctx.config.effort}]};
}
export function outbound(ctx,req){
 if(req.body.model!=="vendor-text" || req.body.deployment || req.headers?.Authorization || req.headers?.["X-Vendor-Key"]) throw Error("wrong outbound context");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/policy_marker",value:ctx.attempt}]},{type:"header.patch",set:{"X-Policy-Attempt":String(ctx.attempt),"X-Prepared":"policy-value"}}]};
}
export function egress(ctx,req){
 if(req.body.deployment || req.body.policy_marker!==ctx.attempt || req.headers["X-Policy-Attempt"]!==String(ctx.attempt) || req.headers?.Authorization || req.headers?.["X-Vendor-Key"]) throw Error("wrong egress context");
 if(req.body.stream && ctx.format==="openai" && !req.body.stream_options.include_usage) throw Error("missing host usage override");
 return {action:"continue"};
}`

func providerPolicyFixture(t *testing.T, provider, policy string, stages []string, path string) (*NewAPIPluginService, *Account, *generationFixtureHTTP, *RequestPluginPlan) {
	t.Helper()
	s, account, upstream := providerFixture(t, provider)
	repo := s.repo.(*requestPluginFixtureRepo)
	p, err := jsplugin.CompilePlugin(policy, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	v := &NewAPIPluginVersion{ID: 2, Source: policy, Meta: p.Meta, Capabilities: runner.Describe(p)}
	repo.versions[2] = v
	initialConfig := json.RawMessage(`{}`)
	if p.Meta.Key == "request-redact" && !slices.Contains(stages, "egress") {
		initialConfig = json.RawMessage(`{"finalCheck":"off"}`)
	}
	config, err := s.validateRequestPluginStageConfig(context.Background(), v, initialConfig, slices.Contains(stages, "egress"), slices.Contains(stages, "reply"))
	require.NoError(t, err)
	item := requestFixtureItem(4, "account", string(config), v)
	item.GroupIDs, item.AccountIDs = []int64{}, []int64{account.ID}
	item.Formats, item.Stages = []string{"openai", "openai_responses"}, stages
	repo.items = []RequestPluginInstance{item}
	format := "openai"
	if path == newAPIProviderResponses {
		format = "openai_responses"
	}
	plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{RequestID: "provider-request", UserID: 4, TokenID: 5, GroupID: 7, Group: "7", Model: "public-text", Format: format, Path: path})
	require.NoError(t, err)
	require.NotNil(t, plan)
	s.gateway.accountRepo = &stubOpenAIAccountRepo{accounts: []Account{*account}}
	upstream.response = `{"id":"chat-fixture","object":"chat.completion","model":"vendor-text","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":5}}`
	return s, account, upstream, plan
}

func TestProviderRequestStagesRejectNonJSONObjectSnapshots(t *testing.T) {
	for _, snapshot := range []string{"client", "wire"} {
		for _, invalid := range []string{"null", "[]", `{"model":"text"} trailing`, `{"model":"text"`} {
			t.Run(snapshot+"/"+invalid, func(t *testing.T) {
				source := providerPolicyMeta + `export function outbound(){throw Error("invalid input reached JS");}`
				s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, source, []string{"outbound"}, newAPIProviderChat)
				client, wire := []byte(`{"model":"public-text","messages":[]}`), []byte(`{"model":"vendor-text","messages":[]}`)
				if snapshot == "client" {
					client = []byte(invalid)
				} else {
					wire = []byte(invalid)
				}
				ctx := context.WithValue(WithRequestPluginPlan(context.Background(), plan), requestPluginProviderBodyKey{}, client)
				_, err := s.providerRequestPluginStages(ctx, account, newAPIProviderChat, "vendor-text", wire, contract.AdvancedCustomRoute{})
				var failure *RequestPluginFailure
				require.ErrorAs(t, err, &failure)
				require.Equal(t, "unsupported_body", failure.Class)
				require.Empty(t, upstream.requests)
				runs := s.repo.(*requestPluginFixtureRepo).runs
				require.Len(t, runs, 1)
				require.Equal(t, "unsupported_body", runs[0].Records[0].ErrorClass)
			})
		}
	}
}

func TestProviderRequestPluginOrderConversionAndExactSigning(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		t.Run(path, func(t *testing.T) {
			provider := strings.Replace(providerConverterSource(), `body:{deployment:req.model,`, `body:{proof:req.body.policy_marker,effort:req.body.reasoning_effort || req.body.reasoning?.effort,deployment:req.model,`, 1)
			s, account, upstream, plan := providerPolicyFixture(t, provider, providerPolicyMeta+providerPolicyHooks, []string{"request", "outbound", "egress"}, path)
			upstream.response = `{"reply":"hello","input":12,"output":5}`
			body := `{"model":"public-text","messages":[{"role":"user","content":"hello"}],"input":"hello"}`
			c, recorder := providerContext(path, body)
			ctx := WithRequestPluginPlan(c.Request.Context(), plan)
			result, err := s.gateway.forwardNewAPIProvider(ctx, c, account, []byte(body), path, "")
			require.NoError(t, err, "audit: %+v", s.repo.(*requestPluginFixtureRepo).runs)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, 5, result.Usage.OutputTokens)
			require.Len(t, upstream.requests, 1)
			req := upstream.requests[0]
			bytes, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, "high", gjson.GetBytes(bytes, "effort").String())
			require.NotNil(t, result.ReasoningEffort)
			require.Equal(t, "high", *result.ReasoningEffort)
			require.True(t, gjson.GetBytes(bytes, "proof").Exists(), "outbound ran before Provider conversion")
			require.Equal(t, "0", req.Header.Get("X-Policy-Attempt"))
			require.Equal(t, "saved", req.Header.Get("X-Prepared"), "Provider preparation takes precedence over the queued business header")
			digest := sha256.Sum256(bytes)
			require.Equal(t, hex.EncodeToString(digest[:]), req.Header.Get("X-Body-Digest"))
			records := s.repo.(*requestPluginFixtureRepo).runs[0].Records
			require.Len(t, records, 3)
			for i, stage := range []string{"request", "outbound", "egress"} {
				require.Equal(t, stage, records[i].Stage)
				require.EqualValues(t, account.ID, records[i].AccountID)
			}
		})
	}
}

func TestProviderRequestPluginRetryPinnedRulesAndOriginalBody(t *testing.T) {
	s, account, _, plan := providerPolicyFixture(t, providerExchangeSource(providerFixtureSource), providerPolicyMeta+providerPolicyHooks, []string{"request", "outbound", "egress"}, newAPIProviderChat)
	h := &providerCredentialHTTP{rejectStatus: 419, rejections: 1}
	s.gateway.httpUpstream = h
	repo := s.repo.(*requestPluginFixtureRepo)
	repo.items[0].ConfigEncrypted = `ENC:{"effort":"low"}`
	repo.items[0].Enabled = false
	body := `{"model":"public-text","messages":[{"role":"user","content":"hello"}]}`
	c, recorder := providerContext(newAPIProviderChat, body)
	result, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
	require.NoError(t, err, "audit: %+v", s.repo.(*requestPluginFixtureRepo).runs)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "high", *result.ReasoningEffort)
	require.Equal(t, 2, h.chats)
	require.Equal(t, 2, h.exchanges)
	require.Len(t, repo.runs, 2)
	for attempt, run := range repo.runs {
		require.Len(t, run.Records, 3)
		for _, record := range run.Records {
			require.Equal(t, attempt, record.Attempt)
		}
		require.Equal(t, strconv.Itoa(attempt), h.headers[attempt*2+1].Get("X-Policy-Attempt"))
		require.Equal(t, "high", gjson.Get(h.bodies[attempt*2+1], "reasoning_effort").String())
	}
}

func TestProviderRequestPluginGuardsFailClosedAndOptionalAtomicity(t *testing.T) {
	for _, tc := range []struct{ name, stage, hook string }{
		{"model", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/model",value:"expensive"}]}]}`},
		{"billing", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/N",value:8}]}]}`},
		{"auth", "outbound", `return {action:"continue",effects:[{type:"header.patch",set:{"X-Vendor-Key":"stolen"}}]}`},
		{"media-placeholder", "outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/metadata",value:{ref:"\u005bmedia:0]"}}]}]}`},
		{"read-only", "egress", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/temperature",value:1}]}]}`},
		{"wrong-request-effect", "request", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/temperature",value:1}]}]}`},
		{"invalid-effort", "request", `return {action:"continue",effects:[{type:"reasoning.set",effort:"automatic"}]}`},
		{"network", "outbound", `utils.fetch("https://api.example.com"); return {action:"continue"}`},
		{"reject", "egress", `return {action:"reject",code:"sensitive",message:"PRIVATE"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+`export function `+tc.stage+`(){`+tc.hook+`}`, []string{tc.stage}, newAPIProviderChat)
			body := `{"model":"public-text","messages":[]}`
			c, recorder := providerContext(newAPIProviderChat, body)
			_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
			var failure *RequestPluginFailure
			require.ErrorAs(t, err, &failure)
			require.Empty(t, upstream.requests)
			require.NotContains(t, recorder.Body.String(), "PRIVATE")
			require.NotContains(t, recorder.Body.String(), "stolen")
			require.Len(t, s.repo.(*requestPluginFixtureRepo).runs, 1)
		})
	}
	for _, reject := range []bool{false, true} {
		policy := strings.Replace(providerPolicyMeta, "apiVersion:1,", "apiVersion:1,optional:true,", 1) + `export function outbound(){return {action:"continue",effects:[{type:"header.patch",set:{"X-Atomic":"changed"}},{type:"json.patch",ops:[{op:"replace",path:"/model",value:"expensive"}]}]}}`
		if reject {
			// A valid explicit rejection cannot be made optional.
			policy = strings.Replace(providerPolicyMeta, "apiVersion:1,", "apiVersion:1,optional:true,", 1) + `export function outbound(){return {action:"reject",code:"blocked"}}`
		}
		s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, policy, []string{"outbound"}, newAPIProviderChat)
		body := `{"model":"public-text","messages":[]}`
		c, _ := providerContext(newAPIProviderChat, body)
		_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
		if reject {
			require.Error(t, err)
			require.Empty(t, upstream.requests)
		} else {
			require.NoError(t, err)
			require.Empty(t, upstream.requests[0].Header.Get("X-Atomic"))
			require.Equal(t, "skipped", s.repo.(*requestPluginFixtureRepo).runs[0].Records[0].Action)
		}
	}
}

func TestProviderRequestPluginAuditAccountAndAdmissionBoundaries(t *testing.T) {
	for _, boundary := range []string{"audit", "changed-binding", "other-account", "memory", "cancel"} {
		t.Run(boundary, func(t *testing.T) {
			s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+`export function egress(){return {action:"continue"}}`, []string{"egress"}, newAPIProviderChat)
			body := `{"model":"public-text","messages":[]}`
			c, recorder := providerContext(newAPIProviderChat, body)
			ctx := WithRequestPluginPlan(c.Request.Context(), plan)
			switch boundary {
			case "audit":
				s.repo.(*requestPluginFixtureRepo).runErr = io.ErrClosedPipe
			case "changed-binding":
				nativeRequestAccount(account, false)
			case "other-account":
				account.ID++
				s.repo.(*requestPluginFixtureRepo).runErr = io.ErrClosedPipe // other account must not run this policy
			case "memory":
				var release func()
				ctx, release = pkghttputil.WithRequestBodyMemoryBudget(ctx, pkghttputil.NewBodyMemoryBudget(1))
				defer release()
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := s.gateway.ForwardAsChatCompletions(ctx, c, account, []byte(body), "", "")
			if boundary == "other-account" || boundary == "changed-binding" {
				require.NoError(t, err)
				require.Len(t, upstream.requests, 1)
				if boundary == "changed-binding" {
					require.Len(t, s.repo.(*requestPluginFixtureRepo).runs, 1)
					require.Equal(t, "egress", s.repo.(*requestPluginFixtureRepo).runs[0].Records[0].Stage)
				}
			} else {
				require.Error(t, err)
				require.Empty(t, upstream.requests)
				if boundary != "cancel" {
					require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
				}
			}
		})
	}
}

func TestProviderRequestPluginOriginalAutoReasoningAndDryRun(t *testing.T) {
	source, err := builtin.Source("auto-reasoning")
	require.NoError(t, err)
	s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, source, []string{"request"}, newAPIProviderChat)
	for _, clientEffort := range []string{"", "low"} {
		body := `{"model":"public-text","messages":[],"tools":[{"type":"function","function":{"name":"test"}}]`
		if clientEffort != "" {
			body += `,"reasoning_effort":"` + clientEffort + `"`
		}
		body += `}`
		c, _ := providerContext(newAPIProviderChat, body)
		_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
		require.NoError(t, err)
		data, err := io.ReadAll(upstream.requests[len(upstream.requests)-1].Body)
		require.NoError(t, err)
		want := "high"
		if clientEffort != "" {
			want = clientEffort
		}
		require.Equal(t, want, gjson.GetBytes(data, "reasoning_effort").String())
	}
	out, err := s.DryRunRequestPluginStage(context.Background(), 2, json.RawMessage(`{}`), json.RawMessage(`{"model":"public-text","input":"hello","tools":[{}]}`), RequestPluginCall{Model: "public-text", Format: "openai_responses", Path: "/dry-run"}, "request")
	require.NoError(t, err)
	require.Empty(t, out.ErrorClass)
	require.Equal(t, "high", gjson.GetBytes(out.Body, "reasoning.effort").String())
	// Hooks blocked on pool admission or an infinite loop honor cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.DryRunRequestPluginStage(ctx, 2, json.RawMessage(`{}`), json.RawMessage(`{}`), RequestPluginCall{Format: "openai"}, "request")
	require.Error(t, err)
}

func TestProviderRequestPluginMediaProjectionPreservesWireBody(t *testing.T) {
	policy := providerPolicyMeta + `export function outbound(ctx,req){
 if(req.body.messages[0].content[0].image_url.url!=="[media:0]" || req.media.length!==1 || req.media[0].mimeType!=="image/png") throw Error("media was exposed");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/temperature",value:0.2}]}]};}`
	s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, policy, []string{"outbound"}, newAPIProviderChat)
	body := `{"model":"public-text","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,YWJj"}}]}]}`
	c, _ := providerContext(newAPIProviderChat, body)
	_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
	require.NoError(t, err)
	data, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "data:image/png;base64,YWJj", gjson.GetBytes(data, "messages.0.content.0.image_url.url").String())
	require.Equal(t, 0.2, gjson.GetBytes(data, "temperature").Float())
}

func TestProviderRequestPluginReasoningAndHeaderBounds(t *testing.T) {
	for _, body := range []string{`{"Reasoning_Effort":"low"}`, `{"Reasoning":{"effort":"low"}}`, `{"reasoning":{"Effort":"low"}}`} {
		require.False(t, requestPluginUnambiguousReasoning([]byte(body)))
	}
	require.True(t, requestPluginUnambiguousReasoning([]byte(`{"reasoning":{"effort":"high"}}`)))
	headers := http.Header{}
	for i := range 64 {
		headers.Set("X-Policy-"+strconv.Itoa(i), "value")
	}
	require.True(t, boundedRequestPluginHeaders(headers))
	headers.Set("X-Policy-Extra", "value")
	require.False(t, boundedRequestPluginHeaders(headers))
	require.False(t, boundedRequestPluginHeaders(http.Header{"X-Policy": {strings.Repeat("x", 64<<10)}}))
}

func TestProviderRequestPluginOriginalParameterOverride(t *testing.T) {
	source, err := builtin.Source("param-override")
	require.NoError(t, err)
	for _, tc := range []struct {
		name, config string
		rejected     bool
	}{
		{"conditions-and-header", `{"override":{"operations":[{"mode":"set","path":"temperature","value":0.2,"conditions":[{"path":"original_model","mode":"full","value":"public-text"},{"path":"channel_id","mode":"full","value":17}],"logic":"AND"},{"mode":"set_header","path":"X-Parameter-Policy","value":"applied"}]}}`, false},
		{"explicit-reject", `{"override":{"operations":[{"mode":"return_error","value":{"message":"PRIVATE denial","code":"Blocked"}}]}}`, true},
		{"protected-model", `{"override":{"model":"other-model"}}`, true},
		{"protected-auth", `{"override":{"operations":[{"mode":"set_header","path":"X-Vendor-Key","value":"PRIVATE credential"}]}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, source, []string{"outbound"}, newAPIProviderChat)
			plan.items[0].Config, err = s.validateRequestPluginStageConfig(context.Background(), plan.items[0].Version, json.RawMessage(tc.config), false, false)
			require.NoError(t, err)
			body := `{"model":"public-text","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,YWJj"}}]}]}`
			c, recorder := providerContext(newAPIProviderChat, body)
			_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
			if tc.rejected {
				require.Error(t, err)
				require.Empty(t, upstream.requests)
				require.NotContains(t, recorder.Body.String(), "PRIVATE")
				return
			}
			require.NoError(t, err)
			require.Len(t, upstream.requests, 1)
			data, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.Equal(t, 0.2, gjson.GetBytes(data, "temperature").Float())
			require.Equal(t, "data:image/png;base64,YWJj", gjson.GetBytes(data, "messages.0.content.0.image_url.url").String())
			require.Equal(t, "applied", upstream.requests[0].Header.Get("X-Parameter-Policy"))
		})
	}
}

func TestProviderRequestPluginSaveRequiresSupportedAccountAndFormats(t *testing.T) {
	s, account, _, _ := providerPolicyFixture(t, providerFixtureSource, providerPolicyMeta+providerPolicyHooks, []string{"request", "outbound", "egress"}, newAPIProviderChat)
	input := RequestPluginInstance{Name: "Provider policy", PluginVersionID: 2, Scope: "account", AccountIDs: []int64{account.ID}, Formats: []string{"openai", "openai_responses"}, Stages: []string{"request", "outbound", "egress"}, Config: json.RawMessage(`{}`)}
	saved, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.NoError(t, err)
	require.Equal(t, input.AccountIDs, saved.AccountIDs)
	for _, change := range []func(*RequestPluginInstance){
		func(i *RequestPluginInstance) { i.AccountIDs = nil },
		func(i *RequestPluginInstance) { i.AccountIDs = []int64{account.ID, account.ID} },
		func(i *RequestPluginInstance) { i.AccountIDs = []int64{99999} },
		func(i *RequestPluginInstance) { i.GroupIDs = []int64{7} },
		func(i *RequestPluginInstance) { i.Formats = nil },
		func(i *RequestPluginInstance) { i.Formats = []string{"task"} },
		func(i *RequestPluginInstance) { i.Stages = []string{"ingress"} },
	} {
		invalid := input
		change(&invalid)
		_, err := s.SaveRequestPluginInstance(context.Background(), invalid, 4)
		require.Error(t, err)
	}
	global := input
	global.Scope, global.AccountIDs = "global", nil
	_, err = s.SaveRequestPluginInstance(context.Background(), global, 4)
	require.NoError(t, err, "global HTTP request stages are explicitly supported")
	compatibility := AssessPluginHost(s.repo.(*requestPluginFixtureRepo).versions[2].Meta, s.repo.(*requestPluginFixtureRepo).versions[2].Capabilities)
	require.True(t, compatibility.Runnable)
	require.Contains(t, compatibility.Kinds, "transform")
}

func BenchmarkRequestPluginMediaProjection(b *testing.B) {
	for _, size := range []int{64 << 10, 1<<20 - 128} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			body := []byte(`{"model":"text","messages":[{"content":"` + strings.Repeat("x", size) + `"}]}`)
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for b.Loop() {
				view, _ := projectRequestPluginMedia(body, MaxRequestPluginBody)
				if view == nil {
					b.Fatal("projection failed")
				}
			}
		})
	}
}
