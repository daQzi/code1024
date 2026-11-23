package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNewAPIProviderModelModifierDispatchAndPricing(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		for _, mappedOverride := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/mapped=%t/stream=%t", path, mappedOverride, stream), func(t *testing.T) {
					s, account, upstream := providerFixture(t, providerFixtureSource)
					if mappedOverride {
						account.Credentials["model_mapping"] = map[string]any{"public-text": "vendor-text@effort:low@temperature:0.4"}
						require.NoError(t, s.NormalizeAccountBinding(context.Background(), account, nil))
					}
					model := "public-text@effort:high@temperature:0.2@topp:0.6"
					require.True(t, account.IsModelSupported(model))
					ctx := WithNewAPIProviderRouting(context.Background(), path)
					require.True(t, account.supportsNewAPIPluginScope(ctx, model))
					body := `{"model":"` + model + `","messages":[{"role":"user","content":"hello"}],"input":"hello","reasoning_effort":"medium","reasoning":{"effort":"medium"},"stream":` + fmt.Sprint(stream) + `}`
					upstream.response = `{"id":"chat-1","object":"chat.completion","model":"vendor-text","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`
					if stream {
						upstream.contentType = "text/event-stream"
						upstream.response = "data: {\"model\":\"vendor-text\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"model\":\"vendor-text\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n"
					}
					if path == newAPIProviderResponses {
						upstream.response = `{"id":"resp-1","object":"response","model":"vendor-text","status":"completed","output":[],"usage":{"input_tokens":12,"output_tokens":5}}`
						if stream {
							upstream.response = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + upstream.response + "}\n\n"
						}
					}
					c, recorder := providerContext(path, body)
					var result *OpenAIForwardResult
					var err error
					if path == newAPIProviderChat {
						result, err = s.gateway.ForwardAsChatCompletions(ctx, c, account, []byte(body), "", "")
					} else {
						result, err = s.gateway.Forward(ctx, c, account, []byte(body))
					}
					require.NoError(t, err, recorder.Body.String())
					require.Equal(t, http.StatusOK, recorder.Code)
					require.Len(t, upstream.requests, 1)
					wire, err := io.ReadAll(upstream.requests[0].Body)
					require.NoError(t, err)
					effort, temperature, field := "high", 0.2, "reasoning_effort"
					if mappedOverride {
						effort, temperature = "low", 0.4
					}
					if path == newAPIProviderResponses {
						field = "reasoning.effort"
					}
					require.Equal(t, "vendor-text", gjson.GetBytes(wire, "model").String())
					require.Equal(t, effort, gjson.GetBytes(wire, field).String())
					require.Equal(t, temperature, gjson.GetBytes(wire, "temperature").Float())
					require.Equal(t, 0.6, gjson.GetBytes(wire, "top_p").Float())
					require.Equal(t, model, result.Model)
					require.Equal(t, "vendor-text", result.UpstreamModel)
					require.Equal(t, "vendor-text@effort:"+effort+"@thinking:on", result.BillingModel)
					require.NotNil(t, result.ReasoningEffort)
					require.Equal(t, effort, *result.ReasoningEffort)
					require.Equal(t, []string{result.BillingModel, "vendor-text@thinking:on", "vendor-text"}, usageBillingModelCandidates(result.BillingModel))
					require.Equal(t, 12, result.Usage.InputTokens)
				})
			}
		}
	}
}

func TestNewAPIProviderInvalidModifiersNeverSendUpstream(t *testing.T) {
	s, account, upstream := providerFixture(t, providerFixtureSource)
	for _, suffix := range []string{"@effort:invalid", "@thinking:-2", "@temperature:NaN", "@secret:1"} {
		body := `{"model":"public-text` + suffix + `","messages":[]}`
		c, recorder := providerContext(newAPIProviderChat, body)
		_, err := s.gateway.ForwardAsChatCompletions(context.Background(), c, account, []byte(body), "", "")
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Contains(t, recorder.Body.String(), "invalid_model_modifier")
	}
	require.Empty(t, upstream.requests)
	require.False(t, account.SupportsNewAPIProviderModel(newAPIProviderChat, "unconfigured@effort:high"))
}

func TestRequestPluginModelModifierBaseScopeAndGroupAllowlist(t *testing.T) {
	source := strings.Replace(requestAppendSource, `key:"scope-fixture"`, `scope:{models:["public"]},key:"scope-fixture"`, 1)
	s, repo := requestPluginFixture(t, source)
	item := requestFixtureItem(1, "global", `{"suffix":"-matched"}`, repo.versions[1])
	item.Models = []string{"public"}
	repo.items = []RequestPluginInstance{item}
	model := "public@effort:high@temperature:0.2"
	plan, err := s.NewRequestPluginPlan(context.Background(), RequestPluginCall{GroupID: 7, Model: model, Format: "openai"})
	require.NoError(t, err)
	require.NotNil(t, plan)
	out, err := plan.Ingress(context.Background(), []byte(`{"model":"`+model+`","prompt":"hello"}`))
	require.NoError(t, err)
	require.Equal(t, "hello-matched", gjson.GetBytes(out.Body, "prompt").String())
	require.Equal(t, model, gjson.GetBytes(out.Body, "model").String())
	allowlist := GroupModelAllowlist{Enabled: true, Models: []string{"public"}}
	require.True(t, allowlist.Allows(model))
	require.False(t, allowlist.Allows("private@effort:high"))
}

func TestProviderModelModifierRequestPluginOverride(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		for _, override := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/override=%t", path, override), func(t *testing.T) {
				policy := providerPolicyMeta + `export function request(){return {action:"continue",effects:[{type:"reasoning.set",effort:"low",override:` + fmt.Sprint(override) + `}]}}`
				s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, policy, []string{"request"}, path)
				body := `{"model":"public-text@thinking:2048","messages":[{"role":"user","content":"hello"}],"input":"hello"}`
				if path == newAPIProviderResponses {
					upstream.response = `{"id":"resp-1","object":"response","status":"completed","output":[],"usage":{"input_tokens":12,"output_tokens":5}}`
				}
				c, recorder := providerContext(path, body)
				result, err := s.gateway.forwardNewAPIProvider(WithRequestPluginPlan(c.Request.Context(), plan), c, account, []byte(body), path, "")
				require.NoError(t, err, recorder.Body.String())
				require.Len(t, upstream.requests, 1)
				wire, err := io.ReadAll(upstream.requests[0].Body)
				require.NoError(t, err)
				require.NotNil(t, result.ReasoningEffort)
				if override {
					require.Equal(t, "low", *result.ReasoningEffort)
					require.Equal(t, "vendor-text@effort:low@thinking:on", result.BillingModel)
					require.False(t, gjson.GetBytes(wire, "reasoning.max_tokens").Exists())
				} else {
					require.Equal(t, "medium", *result.ReasoningEffort)
					require.Equal(t, "vendor-text@thinking:on", result.BillingModel)
					if path == newAPIProviderChat {
						require.EqualValues(t, 2048, gjson.GetBytes(wire, "reasoning.max_tokens").Int())
					}
				}
			})
		}
	}
}
