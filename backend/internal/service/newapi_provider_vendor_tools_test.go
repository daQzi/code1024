package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"
)

func TestNewAPIProviderVendorToolsSurvivePresetAndComposedConversion(t *testing.T) {
	for _, index := range []int{0, 1, 3} { // Responses, Anthropic and Chat upstreams.
		for _, plugin := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				tc := providerPresetCases()[index]
				t.Run(fmt.Sprintf("%s/plugin=%t/stream=%t", tc.converter, plugin, stream), func(t *testing.T) {
					field := "usage.server_side_tool_usage_details"
					counts := `{"web_search_calls":2,"x_posts_fetched":3,"x_users_fetched":1}`
					prices := `{"prices":{"web_search":10,"x_search_posts":2,"x_search_profiles":4}}`
					if index == 1 {
						field = "usage.server_tool_use"
						counts = `{"web_search_requests":2,"web_fetch_requests":1,"code_execution_requests":3,"tool_search_requests":4}`
						prices = `{"prices":{"web_search":10,"web_fetch":4,"code_execution":1,"tool_search":0.75}}`
					}
					var err error
					tc.json, err = sjson.SetRaw(tc.json, field, counts)
					require.NoError(t, err)
					tc.events = mapPresetToolEvents(t, tc.events, func(raw string) string {
						target := field
						if strings.Contains(raw, `"type":"response.completed"`) {
							target = "response." + field
						}
						if strings.Contains(raw, `"usage"`) {
							if index == 1 && !strings.Contains(raw, `"type":"message_delta"`) {
								return raw
							}
							raw, err = sjson.SetRaw(raw, target, counts)
							require.NoError(t, err)
						}
						return raw
					})
					s, account, upstream := providerFixture(t, providerPresetSource(tc, plugin))
					s.marketSettings = &providerToolPriceRepo{raw: &prices}
					body := tc.input
					upstream.response, upstream.contentType = tc.json, "application/json"
					if plugin {
						upstream.response = `{"result":` + tc.json + `}`
					}
					if stream {
						body = strings.TrimSuffix(body, "}") + `,"stream":true}`
						upstream.response, upstream.contentType = tc.events, "text/event-stream"
						if plugin {
							upstream.response = wrapPresetEvents(t, tc.events)
						}
					}
					c, recorder := providerContext(tc.path, body)
					result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), tc.path, "")
					require.NoError(t, err, recorder.Body.String())
					require.NotNil(t, result)
					require.NotNil(t, result.providerToolCharges)
					require.Equal(t, 2, result.SearchCount)
					require.InDelta(t, 0.03, result.providerToolCharges.TotalUSD, 1e-12)
					assertNewAPIProviderToolsBilling(t, result, account, 0.060088)
					require.Contains(t, recorder.Body.String(), "hello")
					require.NotContains(t, recorder.Body.String(), "upstream-fixture-key")
					require.Len(t, upstream.requests, 1)
				})
			}
		}
	}
}

func mapPresetToolEvents(t *testing.T, events string, change func(string) string) string {
	t.Helper()
	var out strings.Builder
	for line := range strings.SplitSeq(events, "\n") {
		if raw, ok := strings.CutPrefix(line, "data: "); ok && json.Valid([]byte(raw)) {
			line = "data: " + change(raw)
		}
		out.WriteString(line + "\n")
	}
	return out.String()
}

func TestNewAPIProviderVendorToolSnapshotCannotOverrideExplicitHook(t *testing.T) {
	tc := providerPresetCases()[1]
	var err error
	tc.json, err = sjson.SetRaw(tc.json, "usage.server_tool_use", `{"web_search_requests":99}`)
	require.NoError(t, err)
	source := strings.Replace(providerPresetSource(tc, true), `return {body:JSON.parse(resp.body).result};`, `return {body:JSON.parse(resp.body).result,tools:{web_search:0}};`, 1)
	s, account, upstream := providerFixture(t, source)
	upstream.response = `{"result":` + tc.json + `}`
	c, _ := providerContext(tc.path, tc.input)
	result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(tc.input), tc.path, "")
	require.NoError(t, err)
	require.Zero(t, result.SearchCount)
	require.NotNil(t, result.providerToolCharges)
	require.Zero(t, result.providerToolCharges.TotalUSD, "an explicit zero also suppresses unpriced vendor inference")
}

func TestNewAPIProviderVendorToolsDirectResponses(t *testing.T) {
	for _, mode := range []string{"json", "sse", "unpriced", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			s, account, upstream := providerFixture(t, providerFixtureSource)
			prices := `{"prices":{"web_search":10,"x_search_posts":2,"x_search_profiles":4}}`
			if mode == "unpriced" {
				prices = `{"prices":{}}`
			}
			s.marketSettings = &providerToolPriceRepo{raw: &prices}
			response := `{"id":"vendor-tools","model":"vendor-text","object":"response","status":"completed","output":[],"usage":{"input_tokens":12,"output_tokens":5,"server_side_tool_usage_details":{"web_search_calls":2,"x_posts_fetched":3,"x_users_fetched":1}}}`
			if mode == "invalid" {
				response = strings.Replace(response, `"x_posts_fetched":3`, `"x_posts_fetched":1.5`, 1)
			}
			stream := mode == "sse"
			upstream.response = response
			if stream {
				upstream.contentType = "text/event-stream"
				upstream.response = "data: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
			}
			body := fmt.Sprintf(`{"model":"public-text","input":"hello","stream":%t}`, stream)
			c, recorder := providerContext(newAPIProviderResponses, body)
			result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderResponses, "")
			if mode == "invalid" || mode == "unpriced" {
				require.Error(t, err)
				require.Equal(t, http.StatusBadGateway, recorder.Code)
				return
			}
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			require.NotNil(t, result.providerToolCharges)
			require.Equal(t, 2, result.SearchCount)
			require.InDelta(t, 0.03, result.providerToolCharges.TotalUSD, 1e-12)
			assertNewAPIProviderToolsBilling(t, result, account, 0.060088)
		})
	}
}

func TestNewAPIProviderGeminiGroundingPreservedAndPricedOnce(t *testing.T) {
	for _, index := range []int{2, 4} {
		for _, plugin := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				for _, perPrompt := range []bool{false, true} {
					tc := providerPresetCases()[index]
					t.Run(fmt.Sprintf("%s/plugin=%t/stream=%t/prompt=%t", tc.converter, plugin, stream, perPrompt), func(t *testing.T) {
						var err error
						metadata := `{"webSearchQueries":["private query one"," private query two ","private query one"," "],"imageSearchQueries":["private query two","private query three"]}`
						tc.json, err = sjson.SetRaw(tc.json, "candidates.0.groundingMetadata", metadata)
						require.NoError(t, err)
						tc.events = "data: " + tc.json + "\n\ndata: " + tc.json + "\n\n"
						s, account, upstream := providerFixture(t, providerPresetSource(tc, plugin))
						prices := `{"prices":{"google_search":10,"google_search_grounded_prompt":0}}`
						if perPrompt {
							prices = `{"prices":{"google_search":0,"google_search_grounded_prompt":30}}`
						}
						s.marketSettings = &providerToolPriceRepo{raw: &prices}
						body := tc.input
						upstream.response, upstream.contentType = tc.json, "application/json"
						if plugin {
							upstream.response = `{"result":` + tc.json + `}`
						}
						if stream {
							body = strings.TrimSuffix(body, "}") + `,"stream":true}`
							upstream.response, upstream.contentType = tc.events, "text/event-stream"
							if plugin {
								upstream.response = wrapPresetEvents(t, tc.events)
							}
						}
						c, recorder := providerContext(tc.path, body)
						result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), tc.path, "")
						require.NoError(t, err, recorder.Body.String())
						require.NotNil(t, result)
						require.NotNil(t, result.providerToolCharges)
						require.Len(t, result.providerToolCharges.Items, 2)
						require.Equal(t, 3, result.providerToolCharges.Items[0].Count)
						require.Equal(t, 1, result.providerToolCharges.Items[1].Count)
						require.InDelta(t, 0.03, result.providerToolCharges.TotalUSD, 1e-12)
						assertNewAPIProviderToolsBilling(t, result, account, 0.060088)
						trace, err := json.Marshal(result.providerToolCharges)
						require.NoError(t, err)
						require.NotContains(t, string(trace), "private query")
						require.Len(t, upstream.requests, 1)
					})
				}
			}
		}
	}
}

func TestNewAPIProviderAliAzureReportsSurvivePresetsAndUseOneLedger(t *testing.T) {
	for _, vendor := range []struct{ name, path string }{
		{"search_strategy_agent", "usage.x_tools.web_search.count"},
		{"bing_web_search", "tool_usage.web_search.num_requests"},
	} {
		for _, plugin := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/plugin=%t/stream=%t", vendor.name, plugin, stream), func(t *testing.T) {
					tc := providerPresetCases()[0]
					var err error
					tc.json, err = sjson.Set(tc.json, vendor.path, 2)
					require.NoError(t, err)
					tc.json, err = sjson.SetRaw(tc.json, "output.1", `{"id":"projected-search","type":"web_search_call","status":"completed"}`)
					require.NoError(t, err)
					tc.events = "data: {\"type\":\"response.completed\",\"response\":" + tc.json + "}\n\n"
					s, account, upstream := providerFixture(t, providerPresetSource(tc, plugin))
					prices := fmt.Sprintf(`{"prices":{%q:10}}`, vendor.name)
					s.marketSettings = &providerToolPriceRepo{raw: &prices}
					body := tc.input
					upstream.response, upstream.contentType = tc.json, "application/json"
					if plugin {
						upstream.response = `{"result":` + tc.json + `}`
					}
					if stream {
						body = strings.TrimSuffix(body, "}") + `,"stream":true}`
						upstream.response, upstream.contentType = tc.events, "text/event-stream"
						if plugin {
							upstream.response = wrapPresetEvents(t, tc.events)
						}
					}
					c, recorder := providerContext(tc.path, body)
					result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), tc.path, "")
					require.NoError(t, err, recorder.Body.String())
					require.NotNil(t, result)
					require.NotNil(t, result.providerToolCharges)
					require.InDelta(t, 0.02, result.providerToolCharges.TotalUSD, 1e-12)
					assertNewAPIProviderToolsBilling(t, result, account, 0.040088)
					require.Zero(t, result.SearchCount, "legacy OpenAI search projection cannot bill another vendor's metric twice")
					require.Len(t, upstream.requests, 1)
				})
			}
		}
	}
}
