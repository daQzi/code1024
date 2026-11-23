package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const providerLateIngress = `
export function ingress(ctx, req) {
 if (ctx.stage !== "ingress" || ctx.channel.id !== 17 || ctx.channel.type !== 61 || ctx.model !== "public-text" || ctx.upstreamModel !== "vendor-text" || req.body.model !== "vendor-text" || req.headers?.Authorization) throw Error("wrong late placement");
 if ((req.body.reasoning_effort || req.body.reasoning?.effort) !== "high" || req.body.policy_marker !== undefined || req.body.late_marker !== undefined) throw Error("wrong late order or retry base");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/late_marker",value:ctx.attempt}]}]};
}`

func TestProviderLateIngressDefersUntilMappedAttempt(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		t.Run(path, func(t *testing.T) {
			policy := providerPolicyMeta + providerPolicyHooks + providerLateIngress
			s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, policy, []string{"ingress", "request", "outbound", "egress"}, path)
			require.True(t, plan.HasStage("ingress"))
			require.False(t, plan.HasEarlyIngress())
			// Even an unreadable attachment must not execute an account rule or
			// acquire its worker at the global/client boundary.
			early, err := plan.Ingress(context.Background(), []byte("not JSON"))
			require.NoError(t, err)
			require.Equal(t, "not JSON", string(early.Body))
			require.Empty(t, early.Records)
			body := `{"model":"public-text","messages":[{"role":"user","content":"hello"}],"input":"hello"}`
			if path == newAPIProviderResponses {
				upstream.response = `{"id":"resp-test","object":"response","status":"completed","model":"vendor-text","output":[],"usage":{"input_tokens":12,"output_tokens":5}}`
			}
			c, recorder := providerContext(path, body)
			result, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), path, "")
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, 5, result.Usage.OutputTokens)
			require.Len(t, upstream.requests, 1)
			wire, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.EqualValues(t, 0, gjson.GetBytes(wire, "late_marker").Int())
			require.True(t, gjson.GetBytes(wire, "late_marker").Exists())
			runs := s.repo.(*requestPluginFixtureRepo).runs
			require.Len(t, runs, 1)
			var stages []string
			for _, record := range runs[0].Records {
				stages = append(stages, record.Stage)
				require.EqualValues(t, 17, record.AccountID)
				require.Equal(t, "continue", record.Action)
			}
			require.Equal(t, []string{"request", "ingress", "outbound", "egress"}, stages)
			require.NotContains(t, body, "late_marker")
		})
	}
}

func TestProviderLateIngressRetryPinnedRulesAndConversion(t *testing.T) {
	policy := providerPolicyMeta + providerPolicyHooks + providerLateIngress
	s, account, _, plan := providerPolicyFixture(t, providerExchangeSource(providerFixtureSource), policy, []string{"ingress", "request", "outbound", "egress"}, newAPIProviderChat)
	h := &providerCredentialHTTP{rejectStatus: 419, rejections: 1}
	s.gateway.httpUpstream = h
	repo := s.repo.(*requestPluginFixtureRepo)
	repo.items[0].Enabled = false
	repo.items[0].ConfigEncrypted = `ENC:{"effort":"low"}`
	body := `{"model":"public-text","messages":[{"role":"user","content":"hello"}]}`
	c, _ := providerContext(newAPIProviderChat, body)
	_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
	require.NoError(t, err)
	require.Equal(t, 2, h.chats)
	require.Len(t, repo.runs, 2)
	for attempt, run := range repo.runs {
		require.Len(t, run.Records, 4)
		require.Equal(t, "ingress", run.Records[1].Stage)
		require.Equal(t, attempt, run.Records[1].Attempt)
		require.EqualValues(t, attempt, gjson.Get(h.bodies[attempt*2+1], "late_marker").Int())
	}

	provider := strings.Replace(providerConverterSource(), `body:{deployment:req.model,`, `body:{proof:req.body.late_marker,deployment:req.model,`, 1)
	s, account, upstream, plan := providerPolicyFixture(t, provider, policy, []string{"ingress", "request", "outbound", "egress"}, newAPIProviderChat)
	upstream.response = `{"reply":"hello","input":12,"output":5}`
	c, _ = providerContext(newAPIProviderChat, body)
	_, err = s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
	require.NoError(t, err)
	wire, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(wire, "proof").Exists(), "late ingress precedes Provider conversion")
}

func TestProviderLateIngressGuardsAuditAndOptionalAtomicity(t *testing.T) {
	for _, tc := range []struct{ name, hook string }{
		{"model", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/model",value:"other"}]}]}`},
		{"cost", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/n",value:5}]}]}`},
		{"stream", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/stream",value:true}]}]}`},
		{"header", `return {action:"continue",effects:[{type:"header.patch",set:{"X-Forbidden":"yes"}}]}`},
		{"media", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/message",value:"[media:0]"}]}]}`},
		{"network", `utils.fetch("https://example.test"); return {action:"continue"}`},
		{"reject", `return {action:"reject",code:"blocked",message:"PRIVATE"}`},
		{"audit", `return {action:"continue"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := providerPolicyMeta + `export function ingress(){` + tc.hook + `}`
			s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, source, []string{"ingress"}, newAPIProviderChat)
			if tc.name == "audit" {
				s.repo.(*requestPluginFixtureRepo).runErr = io.ErrClosedPipe
			}
			body := `{"model":"public-text","messages":[]}`
			c, recorder := providerContext(newAPIProviderChat, body)
			_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
			var failure *RequestPluginFailure
			require.ErrorAs(t, err, &failure)
			require.Empty(t, upstream.requests)
			require.NotContains(t, recorder.Body.String(), "PRIVATE")
			require.True(t, HasOpsClientBusinessLimited(c))
		})
	}
	optional := strings.Replace(providerPolicyMeta, "apiVersion:1,", "apiVersion:1,optional:true,", 1) + `export function ingress(){return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/partial",value:true},{op:"replace",path:"/model",value:"other"}]}]}}`
	s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, optional, []string{"ingress"}, newAPIProviderChat)
	body := `{"model":"public-text","messages":[]}`
	c, _ := providerContext(newAPIProviderChat, body)
	_, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), newAPIProviderChat, "")
	require.NoError(t, err)
	wire, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(wire, "partial").Exists())
	require.Equal(t, "skipped", s.repo.(*requestPluginFixtureRepo).runs[0].Records[0].Action)
}

func TestProviderLateIngressDryRunAndSave(t *testing.T) {
	source := providerPolicyMeta + `export function ingress(){return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/n",value:5}]}]}}`
	s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, source, []string{"ingress"}, newAPIProviderChat)
	input := RequestPluginInstance{Name: "Late ingress", PluginVersionID: 2, Scope: "account", AccountIDs: []int64{account.ID}, Formats: []string{"openai"}, Stages: []string{"ingress"}, Config: json.RawMessage(`{}`)}
	saved, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.NoError(t, err)
	require.Equal(t, input.Stages, saved.Stages)
	body := json.RawMessage(`{"model":"public-text","messages":[]}`)
	early, err := s.DryRunRequestPluginStage(context.Background(), 2, input.Config, body, plan.call, "ingress")
	require.NoError(t, err)
	require.Empty(t, early.ErrorClass, "early ingress runs before estimation")
	late, err := s.DryRunRequestPluginStage(context.Background(), 2, input.Config, body, plan.call, "late_ingress")
	require.NoError(t, err)
	require.Equal(t, "guard", late.ErrorClass, "late ingress must preserve already priced members")
	require.Equal(t, "ingress", late.Records[0].Stage)
	require.JSONEq(t, string(body), string(late.Body))
	require.Empty(t, upstream.requests)
	call := plan.call
	call.Format = "task"
	_, err = s.DryRunRequestPluginStage(context.Background(), 2, input.Config, body, call, "late_ingress")
	require.Error(t, err)
	require.NoError(t, validateRequestPluginStages([]string{"ingress", "request", "outbound", "egress", "reply", "report"}, []string{"openai"}, "account"))
}
