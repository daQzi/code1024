//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Preserve the normal converters and add vendor-reported facts at each hook.
func providerToolsSource(source string) string {
	source = strings.Replace(source, "export function convertResponse(", "function toolBaseResponse(", 1)
	source = strings.Replace(source, "export function convertStreamEvent(", "function toolBaseStream(", 1)
	return source + `
export function convertResponse(ctx,resp){const r=toolBaseResponse(ctx,resp);r.tools=JSON.parse(resp.body).tools;return r;}
export function convertStreamEvent(ctx,e){const r=toolBaseStream(ctx,e);r.tools=e.done?{web_search:1}:JSON.parse(e.data).tools;return r;}`
}

func providerSearchKey(price *float64) *APIKey {
	return &APIKey{Group: &Group{SearchPricePer1k: price}}
}

func TestNewAPIProviderToolsJSONAndSingleLedger(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		for _, price := range []float64{10, 0} {
			t.Run(fmt.Sprintf("%s/price=%g", path, price), func(t *testing.T) {
				s, account, _ := providerFixture(t, providerToolsSource(providerConverterSource()))
				configuredPrice := price
				key := providerSearchKey(&configuredPrice)
				s.gateway.httpUpstream = &providerChainHTTP{send: func(*http.Request) (*http.Response, error) {
					configuredPrice = 999 // A response cannot switch an admitted request's price.
					return chainResponse(200, `{"reply":"hello","input":12,"output":5,"tools":{"web_search":2,"web_search_preview":1}}`), nil
				}}
				body := `{"model":"public-text","messages":[],"input":"hello"}`
				c, recorder := providerContext(path, body)
				c.Set("api_key", key)
				result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), path, "")
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Equal(t, 3, result.SearchCount)
				require.NotNil(t, result.providerToolCharges)
				require.Equal(t, price, *result.providerToolCharges.Items[0].PricePer1K)
				assertNewAPIProviderToolsBilling(t, result, account, 0.000088+3*price/1000*2)
			})
		}
	}
}

func assertNewAPIProviderToolsBilling(t *testing.T, result *OpenAIForwardResult, account *Account, expected float64) {
	t.Helper()
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	billing.resolver = NewModelPricingResolver(nil, billing.billingService)
	groupID, inputPrice, outputPrice, changedSearchPrice := int64(7), 2e-6, 4e-6, 999.0
	group := &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 2, SearchPricePer1k: &changedSearchPrice,
		ModelPricing: []ChannelModelPricing{{Models: []string{"vendor-text"}, BillingMode: BillingModeToken, InputPrice: &inputPrice, OutputPrice: &outputPrice}}}
	result.RequestID = "provider-tools-billing-fixture"
	require.NoError(t, billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: &APIKey{ID: 8, GroupID: &groupID, Group: group}, User: &User{ID: 9}, Account: account, InboundEndpoint: newAPIProviderChat, UpstreamEndpoint: result.UpstreamEndpoint}))
	require.Equal(t, 1, billingRepo.calls, "token and search costs enter the existing ledger once")
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, expected, billingRepo.lastCmd.BalanceCost, 1e-10)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "public-text", usageRepo.lastLog.Model)
	require.Equal(t, 12, usageRepo.lastLog.InputTokens)
	require.Equal(t, 5, usageRepo.lastLog.OutputTokens)
	snapshot, err := result.providerToolCharges.Snapshot(2)
	require.NoError(t, err)
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.JSONEq(t, string(encoded), string(usageRepo.lastLog.ProviderToolBilling), "the same captured tool prices enter the existing usage row")
	require.InDelta(t, expected-0.000088, snapshot.ActualUSD, 1e-10)
}

func TestNewAPIProviderToolsStreamAndCollectedEOFDeltas(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		for _, mode := range []string{"sse", "ndjson", "collect-body", "collect-chunks"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				collect := strings.HasPrefix(mode, "collect-")
				source := providerConverterSource()
				payload := "{\"seq\":0,\"text\":\"hello\",\"tools\":{\"web_search\":2}}\n{\"seq\":1,\"finished\":true,\"input\":12,\"output\":5,\"tools\":{\"web_search_preview\":1}}\n"
				if collect {
					source = providerCollectSource(mode == "collect-chunks")
					payload = "{\"text\":\"hello\"}\n{\"finish\":true,\"tools\":{\"web_search\":2}}\n"
				}
				s, account, upstream := providerFixture(t, providerToolsSource(source))
				upstream.contentType, upstream.response = "application/x-ndjson", payload
				if mode == "sse" {
					upstream.contentType = "text/event-stream"
					upstream.response = "data: " + strings.ReplaceAll(strings.TrimSpace(payload), "\n", "\n\ndata: ") + "\n\n"
				}
				price := 10.0
				body := fmt.Sprintf(`{"model":"public-text","messages":[],"input":"hello","stream":%t}`, !collect)
				c, recorder := providerContext(path, body)
				c.Set("api_key", providerSearchKey(&price))
				result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), path, "")
				require.NoError(t, err)
				require.NotNil(t, result)
				require.GreaterOrEqual(t, result.SearchCount, 3)
				require.LessOrEqual(t, result.SearchCount, 4, "the clean EOF hook may add one call if the protocol reader reaches it")
				if collect {
					require.Equal(t, 3, result.SearchCount, "collector must account for the final EOF hook once")
				}
				require.Contains(t, recorder.Body.String(), "public-text")
				require.Len(t, upstream.requests, 1)
				assertNewAPIProviderToolsBilling(t, result, account, 0.000088+float64(result.SearchCount)*price/1000*2)
			})
		}
	}
}

func TestNewAPIProviderToolsChainRenewalDoesNotCountRejectedStep(t *testing.T) {
	s, account, _ := providerFixture(t, providerExchangeSource(providerToolsSource(providerChainSource())))
	var creates, polls, exchanges int
	s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/prefix/token":
			exchanges++
			return chainResponse(200, fmt.Sprintf(`{"token":{"value":"token-%d"},"expiresAt":0}`, exchanges)), nil
		case "/prefix/chain/create":
			creates++
			return chainResponse(202, `{"next":"poll","tools":{"web_search":2}}`), nil
		case "/prefix/chain/poll":
			polls++
			if polls%2 == 1 {
				return chainResponse(401, `{"reject":true,"tools":{"web_search":99}}`), nil
			}
			return chainResponse(200, `{"reply":"hello","expected":1,"tools":{"web_search_preview":1}}`), nil
		}
		t.Fatalf("unexpected path %s", req.URL.Path)
		return nil, nil
	}}
	price := 10.0
	for range 2 {
		body := `{"model":"public-text","messages":[]}`
		c, _ := providerContext(newAPIProviderChat, body)
		c.Set("api_key", providerSearchKey(&price))
		result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
		require.NoError(t, err)
		require.Equal(t, 3, result.SearchCount, "one counter per logical request, not per step or worker")
		assertNewAPIProviderToolsBilling(t, result, account, 0.060088)
	}
	require.Equal(t, 2, creates, "accepted generation is never resubmitted during renewal")
	require.Equal(t, 4, polls)
	require.Equal(t, 3, exchanges, "second request reuses its cached token before the rejection")
}

func TestNewAPIProviderToolsAdmissionAndValidation(t *testing.T) {
	for _, suffix := range []string{`,"tools":[{"type":"web_search"}]`, `,"tools":[{"type":"web_search_preview_2025_03_11"}]`, `,"tool_choice":{"type":"web_search"}`, `,"web_search_options":{}`} {
		s, account, upstream := providerFixture(t, providerToolsSource(providerConverterSource()))
		body := `{"model":"public-text","messages":[]` + suffix + `}`
		c, recorder := providerContext(newAPIProviderChat, body)
		_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Contains(t, recorder.Body.String(), "provider_tool_price_required")
		require.Empty(t, upstream.requests)
	}
	for _, counts := range []string{`{"search":1}`, `{"web_search":-1}`, `{"web_search":10001}`, `{"web_search":1.5}`, `{"web_search":"1"}`} {
		s, account, upstream := providerFixture(t, providerToolsSource(providerConverterSource()))
		upstream.response = `{"reply":"hello","input":12,"output":5,"tools":` + counts + `}`
		body := `{"model":"public-text","messages":[]}`
		c, recorder := providerContext(newAPIProviderChat, body)
		price := 10.0
		c.Set("api_key", providerSearchKey(&price))
		_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
		require.Error(t, err)
		require.NotContains(t, recorder.Body.String(), "upstream-fixture-key")
	}
	for _, price := range []*float64{nil, new(-1.0), new(math.Inf(1)), new(math.NaN())} {
		usage := newProviderToolUsage(providerSearchKey(price))
		require.Error(t, usage.add(map[string]int{"web_search": 1}))
		require.Empty(t, usage.usage.Counts())
	}
	usage := newProviderToolUsage(nil)
	require.NoError(t, usage.admitRequest([]byte(`{"tools":[{"type":"function","function":{"name":"web_search"}}]}`)))
	require.NoError(t, usage.add(map[string]int{"web_search": 0}))
	result := &OpenAIForwardResult{SearchCount: 9}
	usage.apply(result)
	require.Zero(t, result.SearchCount, "explicit zero replaces protocol-inferred search calls")
	require.Nil(t, result.providerToolCharges.Items[0].PricePer1K, "zero without a price must not import the native vendor default")
}

func TestNewAPIProviderToolsPartialFailureRetainsOnlyAcceptedFacts(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		s, account, upstream := providerFixture(t, providerToolsSource(providerConverterSource()))
		upstream.contentType = "application/x-ndjson"
		upstream.response = "{\"seq\":0,\"usage\":true,\"input\":12,\"output\":5,\"tools\":{\"web_search\":2}}\n{\"seq\":1,\"fail\":true,\"tools\":{\"web_search\":99}}\n"
		price := 10.0
		body := `{"model":"public-text","messages":[],"stream":true}`
		c, recorder := providerContext(path, body)
		c.Set("api_key", providerSearchKey(&price))
		result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), path, "")
		require.Error(t, err)
		require.NotNil(t, result)
		require.Equal(t, 2, result.SearchCount, "failure and uncalled EOF hooks add no facts")
		require.NotContains(t, recorder.Body.String(), "[DONE]")
		assertNewAPIProviderToolsBilling(t, result, account, 0.040088)
	}
}

func TestNewAPIProviderRejectedHandshakeDoesNotCommitToolsNextOrState(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses, newAPIProviderEmbeddings} {
		for _, status := range []int{http.StatusUnauthorized, 419} {
			for _, renewal := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/status=%d/renewal=%t", path, status, renewal), func(t *testing.T) {
					source := providerToolsSource(providerChainSource())
					if path == newAPIProviderEmbeddings {
						source = strings.Replace(source, `relay:[`, `relay:[{incoming_path:"/v1/embeddings",upstream_path:"/vector",converter:"plugin",models:["vendor-text"]},`, 1)
						source = strings.Replace(source, `if(s.path==="/v1/responses")`, `if(s.path==="/v1/embeddings")return {body:{model:s.model,data:[{embedding:[0.1]}],usage:{prompt_tokens:12}}};if(s.path==="/v1/responses")`, 1)
					}
					if renewal {
						source = providerExchangeSource(source)
					} else {
						source += `export function credentialRejected(ctx,resp){return resp.status===419;}`
					}
					// The rejected hook returns an otherwise valid next/state and
					// unknown positive tool facts. None may reach the accepted chain.
					s, account, _ := providerFixture(t, source)
					creates, polls, tokens := 0, 0, 0
					s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
						switch req.URL.Path {
						case "/prefix/token":
							tokens++
							return chainResponse(200, fmt.Sprintf(`{"token":{"value":"token-%d"},"expiresAt":0}`, tokens)), nil
						case "/prefix/chain/create":
							creates++
							if creates == 1 {
								return chainResponse(status, `{"next":"forbidden","error":{"code":"token_expired"},"tools":{"unpriced_rejected_tool":999}}`), nil
							}
							return chainResponse(200, `{"reply":"hello","expected":0}`), nil
						default:
							polls++
							return chainResponse(200, `{"reply":"unexpected","expected":1}`), nil
						}
					}}
					body := `{"model":"public-text","messages":[],"input":"hello"}`
					c, recorder := providerContext(path, body)
					result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), path, "")
					require.Zero(t, polls, "a rejected credential never authorizes a next request")
					if renewal {
						require.NoError(t, err, recorder.Body.String())
						require.Equal(t, 2, creates)
						require.Equal(t, 2, tokens)
						require.NotNil(t, result)
						require.Nil(t, result.providerToolCharges, "unknown rejected costs must not contaminate a valid renewal")
					} else {
						require.Error(t, err)
						require.Equal(t, 1, creates)
						require.Nil(t, result)
					}
				})
			}
		}
	}
}
