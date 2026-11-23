//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNativeAccountHTTPReportSaveAndFinalBilling(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformDeepseek} {
		t.Run(platform, func(t *testing.T) {
			source := globalHTTPReportSource + `export function outbound(){return {action:"continue"}}`
			s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, source, []string{"report"}, newAPIProviderChat)
			account.Platform = platform
			account.Credentials = map[string]any{"base_url": "https://api.example.com", "api_key": "upstream-fixture-key", "api_protocol": APIProtocolChatCompletions, "model_mapping": map[string]any{"public-text": "vendor-text"}}
			account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
			s.gateway.accountRepo = &stubOpenAIAccountRepo{accounts: []Account{*account}}
			input := RequestPluginInstance{Name: "Native report", PluginVersionID: 2, Scope: "account", AccountIDs: []int64{account.ID}, Formats: []string{"openai"}, Stages: []string{"report"}, Config: json.RawMessage(`{}`)}
			_, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
			require.NoError(t, err)
			input.Stages = []string{"outbound", "report"}
			_, err = s.SaveRequestPluginInstance(context.Background(), input, 4)
			require.NoError(t, err, "native OpenAI gateway accounts now support explicitly selected request stages")
			h := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 4)}
			s.gateway.httpUpstream = &relayReportHTTP{upstream: s.gateway.httpUpstream, reports: h}
			s.taskReports = newTaskReportExecutor(s)
			t.Cleanup(s.taskReports.close)
			upstream.response = `{"id":"native-report","object":"chat.completion","model":"vendor-text","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`
			body := []byte(`{"model":"public-text","messages":[{"role":"user","content":"hello"}]}`)
			c, recorder := providerContext(newAPIProviderChat, string(body))
			ctx := WithRequestPluginPlan(c.Request.Context(), plan)
			r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
			c.Writer = r
			result, err := s.gateway.ForwardAsChatCompletions(ctx, c, account, body, "", "")
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.NotNil(t, result.pluginReportBilling)
			r.Finish(body)
			require.Empty(t, h.requests)
			result.pluginReportBilling.complete(decimal.RequireFromString("0.013"), true)
			payload := awaitTaskReport(t, h)
			require.EqualValues(t, account.ID, gjson.GetBytes(payload["ctx"], "channel.id").Int())
			require.EqualValues(t, requestPluginChannelType(account, false), gjson.GetBytes(payload["ctx"], "channel.type").Int())
			require.Equal(t, "0.013", gjson.GetBytes(payload["record"], "cost.usd").String())
			require.JSONEq(t, recorder.Body.String(), gjson.GetBytes(payload["record"], "response.body").Raw)
		})
	}
}

func TestNativeAccountHTTPReportScopeCannotDisableBoundProviderPolicies(t *testing.T) {
	source := globalHTTPReportSource + `export function outbound(){return {action:"reject",code:"blocked",message:"PRIVATE"}}`
	s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, source, []string{"report"}, newAPIProviderChat)
	account.Credentials = map[string]any{"api_key": "upstream-fixture-key", "base_url": "https://api.example.com", "api_protocol": APIProtocolChatCompletions}
	account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
	policy := plan.items[0]
	policy.ID, policy.Stages = 5, []string{"outbound"}
	plan.items = append(plan.items, policy)
	ctx := WithRequestPluginPlan(context.Background(), plan)
	c, recorder := providerContext(newAPIProviderChat, `{}`)
	_, err := s.gateway.ForwardAsChatCompletions(ctx, c, account, []byte(`{"model":"public-text","messages":[]}`), "", "")
	var failure *RequestPluginFailure
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.Rejected)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "PRIVATE")
	require.Empty(t, upstream.requests, "losing the Provider binding must never bypass a mutation policy")
	runs := s.repo.(*requestPluginFixtureRepo).runs
	require.Len(t, runs, 1)
	require.EqualValues(t, 5, runs[0].Records[0].InstanceID)
}

func TestNativeAccountHTTPReportPlatformAdmission(t *testing.T) {
	s, _, _, _ := providerPolicyFixture(t, providerFixtureSource, globalHTTPReportSource, []string{"report"}, newAPIProviderChat)
	for _, candidate := range []struct {
		platform, kind string
		allowed        bool
	}{{PlatformOpenAI, AccountTypeOAuth, true}, {PlatformOpenAI, AccountTypeSetupToken, true}, {PlatformGrok, AccountTypeAPIKey, true}, {PlatformKimi, AccountTypeAPIKey, true}, {PlatformGemini, AccountTypeAPIKey, false}, {PlatformAnthropic, AccountTypeAPIKey, false}, {PlatformTypeSafe, AccountTypeAPIKey, false}, {"unknown", AccountTypeAPIKey, false}} {
		s.gateway.accountRepo = &stubOpenAIAccountRepo{accounts: []Account{{ID: 19, Platform: candidate.platform, Type: candidate.kind}}}
		_, err := s.SaveRequestPluginInstance(context.Background(), RequestPluginInstance{Name: "Native report", PluginVersionID: 2, Scope: "account", AccountIDs: []int64{19}, Formats: []string{"openai_responses"}, Stages: []string{"report"}, Config: json.RawMessage(`{}`)}, 4)
		if candidate.allowed {
			require.NoError(t, err, "%s/%s", candidate.platform, candidate.kind)
		} else {
			require.Error(t, err, "%s/%s", candidate.platform, candidate.kind)
		}
	}
}
