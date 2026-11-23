package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const globalHTTPReportSource = `export const meta={apiVersion:1,key:"global-report-fixture",name:"Report",version:"1.0.0",author:{name:"Fixture"},allowedHosts:["reports.example.com"]};
export function report(ctx,record){
 if(ctx.stage!=="report" || !["openai","openai_responses"].includes(ctx.format) || record.task!==undefined)throw Error("wrong report");
 utils.fetch({url:"https://reports.example.com/callback",method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({ctx,record})});
}`

func globalHTTPReportFixture(t *testing.T, path string) (*NewAPIPluginService, *Account, *generationFixtureHTTP, *RequestPluginPlan, *taskReportHTTP) {
	t.Helper()
	s, account, upstream, _ := providerPolicyFixture(t, providerConverterSource(), globalHTTPReportSource, []string{"report"}, path)
	repo := s.repo.(*requestPluginFixtureRepo)
	group := repo.items[0]
	group.ID, group.Scope, group.GroupIDs, group.AccountIDs = 2, "group", []int64{7}, nil
	global := group
	global.ID, global.Scope, global.GroupIDs = 1, "global", nil
	repo.items = []RequestPluginInstance{global, group, repo.items[0]}
	s.requestPlugins.expires = time.Time{}
	format := "openai"
	if path == newAPIProviderResponses {
		format = "openai_responses"
	}
	plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{RequestID: "global-report", UserID: 4, TokenID: 5, GroupID: 7, Group: "7", Model: "public-text", Format: format, Path: path})
	require.NoError(t, err)
	h := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 16)}
	s.gateway.httpUpstream = &relayReportHTTP{upstream: s.gateway.httpUpstream, reports: h}
	s.taskReports = newTaskReportExecutor(s)
	t.Cleanup(s.taskReports.close)
	return s, account, upstream, plan, h
}

func TestGlobalHTTPReportNativeRelayAndConfirmedSettlement(t *testing.T) {
	for _, scenario := range []struct {
		name, path   string
		useResponses bool
	}{{"chat", newAPIProviderChat, false}, {"responses", newAPIProviderResponses, true}, {"chat-via-responses", newAPIProviderChat, true}} {
		path := scenario.path
		for _, stream := range []bool{false, true} {
			t.Run(scenario.name+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
				s, account, upstream, plan, h := globalHTTPReportFixture(t, path)
				account.ID = 18 // The pinned Provider policy belongs to account 17.
				nativeReport := plan.items[2]
				nativeReport.ID, nativeReport.AccountIDs = 3, []int64{account.ID}
				plan.items = append(plan.items, nativeReport)
				account.Credentials = map[string]any{"base_url": "https://api.example.com", "api_key": "upstream-fixture-key", "model_mapping": map[string]any{"public-text": "vendor-text"}}
				account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: scenario.useResponses}
				s.gateway.cfg = &config.Config{}
				upstreamStream := stream || scenario.useResponses && path == newAPIProviderChat
				if scenario.useResponses {
					upstream.response = `{"id":"resp-native","object":"response","model":"vendor-text","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":12,"output_tokens":5,"total_tokens":17}}`
					if upstreamStream {
						upstream.response = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + upstream.response + "}\n\n"
					}
				} else {
					upstream.response = `{"id":"chat-native","object":"chat.completion","model":"vendor-text","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`
					if stream {
						upstream.response = "data: {\"id\":\"chat-native\",\"object\":\"chat.completion.chunk\",\"model\":\"vendor-text\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5,\"total_tokens\":17}}\n\ndata: [DONE]\n\n"
					}
				}
				if upstreamStream {
					upstream.contentType = "text/event-stream"
				}
				body := []byte(`{"model":"public-text","messages":[{"role":"user","content":"hello"}],"input":"hello","stream":` + strconv.FormatBool(stream) + `}`)
				c, recorder := providerContext(path, string(body))
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
				ctx := WithRequestPluginPlan(c.Request.Context(), plan)
				r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "native-user")
				require.NotNil(t, r)
				c.Writer = r
				var result *OpenAIForwardResult
				var err error
				if path == newAPIProviderChat {
					result, err = s.gateway.ForwardAsChatCompletions(ctx, c, account, body, "", "")
				} else {
					result, err = s.gateway.Forward(ctx, c, account, body)
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.NotNil(t, result.pluginReportBilling)
				r.Finish(body)
				require.Empty(t, h.requests, "native reports also wait for actual billing")
				result.pluginReportBilling.complete(decimal.RequireFromString("0.12000001"), true)
				for _, id := range []int64{1, 2, 3} {
					payload := awaitTaskReport(t, h)
					require.Equal(t, strconv.FormatInt(id, 10), gjson.GetBytes(payload["ctx"], "instance.id").String())
					require.EqualValues(t, account.ID, gjson.GetBytes(payload["ctx"], "channel.id").Int())
					require.EqualValues(t, 1, gjson.GetBytes(payload["ctx"], "channel.type").Int())
					require.Zero(t, gjson.GetBytes(payload["ctx"], "attempt").Int())
					require.Equal(t, "vendor-text", gjson.GetBytes(payload["ctx"], "upstreamModel").String())
					require.EqualValues(t, 12, gjson.GetBytes(payload["record"], "usage.promptTokens").Int())
					require.Equal(t, "0.12000001", gjson.GetBytes(payload["record"], "cost.usd").String())
					require.EqualValues(t, recorder.Body.Len(), gjson.GetBytes(payload["record"], "response.bytes").Int())
					if stream {
						require.NotEmpty(t, gjson.GetBytes(payload["record"], "response.events").Array())
					} else {
						require.JSONEq(t, recorder.Body.String(), gjson.GetBytes(payload["record"], "response.body").Raw)
					}
					raw, _ := json.Marshal(payload)
					require.NotContains(t, string(raw), "upstream-fixture-key")
					require.NotContains(t, string(raw), "downstream-secret")
				}
				require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 1 }, time.Second, time.Millisecond)
				require.Empty(t, h.requests, "Provider-bound report must not receive native results")
			})
		}
	}
}

func TestGlobalHTTPReportScopeFinalAttemptAndEarlyFailure(t *testing.T) {
	for _, final := range []string{"early", "provider", "native", "other-provider"} {
		t.Run(final, func(t *testing.T) {
			s, account, upstream, plan, h := globalHTTPReportFixture(t, newAPIProviderChat)
			ctx := WithRequestPluginPlan(context.Background(), plan)
			c, _ := providerContext(newAPIProviderChat, `{}`)
			r := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
			c.Writer = r
			want := []int64{1, 2}
			if final == "provider" {
				upstream.response = `{"reply":"hello","input":12,"output":5}`
				result, err := s.gateway.forwardNewAPIProvider(ctx, c, account, []byte(`{"model":"public-text","messages":[]}`), newAPIProviderChat, "")
				require.NoError(t, err)
				result.pluginReportBilling.complete(decimal.Zero, true)
				want = append(want, 4)
			} else {
				if final != "early" {
					beginRelayReportAttempt(ctx, account, "first-upstream", true)
					plan.nextAttempt++ // First Provider build, then a routing fallback.
					beginRelayReportAttempt(ctx, &Account{ID: 18, Platform: PlatformDeepseek}, "final-upstream", final == "other-provider")
				}
				c.JSON(http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"message": "No eligible account"}})
			}
			r.Finish([]byte(`{"prompt":"safe-client"}`))
			for _, id := range want {
				payload := awaitTaskReport(t, h)
				require.Equal(t, strconv.FormatInt(id, 10), gjson.GetBytes(payload["ctx"], "instance.id").String())
				if final == "early" {
					require.False(t, gjson.GetBytes(payload["ctx"], "channel").Exists())
					require.Equal(t, "No eligible account", gjson.GetBytes(payload["record"], "error.message").String())
				} else if final == "native" || final == "other-provider" {
					require.EqualValues(t, 18, gjson.GetBytes(payload["ctx"], "channel.id").Int())
					require.Equal(t, "final-upstream", gjson.GetBytes(payload["ctx"], "upstreamModel").String())
				}
			}
			require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 1 }, time.Second, time.Millisecond)
			require.Empty(t, h.requests)
		})
	}
}

func TestGlobalHTTPReportExplicitFormatsAndPlacement(t *testing.T) {
	for _, scope := range []string{"global", "group"} {
		for _, stages := range [][]string{{"report"}, {"ingress", "report"}} {
			for _, formats := range [][]string{{"task"}, {"openai"}, {"openai_responses"}, {"openai", "openai_responses"}, {"claude"}, {"gemini"}} {
				require.NoError(t, validateRequestPluginStages(stages, formats, scope))
			}
			for _, formats := range [][]string{nil, {"task", "openai"}, {"openai", "openai"}} {
				require.Error(t, validateRequestPluginStages(stages, formats, scope))
			}
		}
		require.NoError(t, validateRequestPluginStages([]string{"egress", "report"}, []string{"openai"}, scope))
	}
	for platform, want := range map[string]int{PlatformOpenAI: 1, PlatformAnthropic: 14, PlatformGemini: 24, PlatformKimi: 25, PlatformZhipu: 26, PlatformMiniMax: 35, PlatformDeepseek: 43, PlatformGrok: 48, PlatformOpenCodeGo: 0, "unknown": 0} {
		require.Equal(t, want, requestPluginChannelType(&Account{Platform: platform}, false))
		require.Equal(t, 61, requestPluginChannelType(&Account{Platform: platform}, true))
	}
	for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		require.Equal(t, 57, requestPluginChannelType(&Account{Platform: PlatformOpenAI, Type: kind}, false))
	}
	// Reuse the normal persistence service; report-only scopes never require a
	// Provider account or implicitly enable new native request mutations.
	s, repo := requestPluginFixture(t, globalHTTPReportSource)
	for _, scope := range []string{"global", "group"} {
		input := RequestPluginInstance{Name: "HTTP report", PluginVersionID: 1, Scope: scope, Stages: []string{"report"}, Formats: []string{"openai", "openai_responses"}, Config: json.RawMessage(`{}`)}
		if scope == "group" {
			input.GroupIDs = []int64{7}
		}
		_, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
		require.NoError(t, err)
	}
	call := RequestPluginCall{GroupID: 99, Model: "public-text", Format: "openai"}
	// Enable only the other-group instance, and verify it cannot apply here.
	for i := range repo.items {
		repo.items[i].Enabled = repo.items[i].Scope == "group"
	}
	s.requestPlugins.expires = time.Time{}
	plan, err := s.NewRequestPluginPlan(context.Background(), call)
	require.NoError(t, err)
	require.Nil(t, plan)
}
