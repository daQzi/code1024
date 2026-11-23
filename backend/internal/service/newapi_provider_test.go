package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const providerFixtureSource = `export const meta = {apiVersion:1,key:"native-provider-fixture",name:"Native provider",version:"1.0.0",author:{name:"Fixture"},models:["vendor-text"],
channelConfig:{type:"object",properties:{label:{type:"string",title:"Label",description:"Signing label",default:"signed"}}},
relay:[{incoming_path:"/v1/chat/completions",upstream_path:"/vendor/chat?fixed=yes",models:["vendor-text"],auth:{type:"header",name:"X-Vendor-Key",value:"{api_key}"}},
{incoming_path:"/v1/responses",upstream_path:"/vendor/responses/{model}",models:["vendor-text"],auth:{type:"query",name:"key",value:"{api_key}"}}]};
export function signRequest(ctx,req){return {headers:{"X-Body-Digest":req.bodySha256,"X-Config":ctx.config.label},query:{signed:"yes"}};}`

type providerCheckedUpstream struct {
	HTTPUpstream
	inner *generationFixtureHTTP
}

func (h *providerCheckedUpstream) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return h.inner.Do(req, proxy, accountID, concurrency)
}

func providerFixture(t *testing.T, source string) (*NewAPIPluginService, *Account, *generationFixtureHTTP) {
	t.Helper()
	s, repo := requestPluginFixture(t, source)
	repo.versions[1].Enabled = true
	account := &Account{ID: 17, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 2,
		Credentials: map[string]any{NewAPIPluginRevisionKey: 1, "base_url": "https://api.example.com/prefix", "api_key": "upstream-fixture-key", "model_mapping": map[string]any{"public-text": "vendor-text"}}}
	require.NoError(t, s.NormalizeAccountBinding(context.Background(), account, map[string]any{NewAPIPluginConfigKey: map[string]any{"label": "saved"}}))
	s.gateway.newAPIPlugins = s
	s.gateway.cfg = &config.Config{}
	upstream := s.gateway.httpUpstream.(*generationFixtureHTTP)
	s.gateway.httpUpstream = &providerCheckedUpstream{inner: upstream}
	return s, account, upstream
}

func providerContext(path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path+"?client_secret=not-forwarded", strings.NewReader(body))
	c.Request.Header.Set("Authorization", "Bearer downstream-secret")
	c.Request.Header.Set("X-Extra-Secret", "downstream-only")
	return c, recorder
}

func TestNewAPIProviderNativeRelay(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		for _, stream := range []bool{false, true} {
			t.Run(path+map[bool]string{false: "/json", true: "/sse"}[stream], func(t *testing.T) {
				s, account, upstream := providerFixture(t, providerFixtureSource)
				body := `{"model":"public-text","messages":[{"role":"user","content":"hello"}],"input":"hello","stream":` + map[bool]string{false: "false", true: "true"}[stream] + `}`
				if path == newAPIProviderChat {
					upstream.response = `{"id":"chat-fixture","object":"chat.completion","model":"vendor-text","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`
					if stream {
						upstream.response = "data: {\"model\":\"vendor-text\",\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\ndata: {\"model\":\"vendor-text\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n"
					}
				} else {
					upstream.response = `{"id":"resp-fixture","object":"response","status":"completed","error":null,"model":"vendor-text","output":[],"usage":{"input_tokens":12,"output_tokens":5,"total_tokens":17}}`
					if stream {
						upstream.response = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + upstream.response + "}\n\n"
					}
				}
				if stream {
					upstream.contentType = "text/event-stream"
				}
				c, recorder := providerContext(path, body)
				var result *OpenAIForwardResult
				var err error
				if path == newAPIProviderChat {
					result, err = s.gateway.ForwardAsChatCompletions(c.Request.Context(), c, account, []byte(body), "", "")
				} else {
					result, err = s.gateway.Forward(c.Request.Context(), c, account, []byte(body))
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, 12, result.Usage.InputTokens)
				assert.Equal(t, 5, result.Usage.OutputTokens)
				assert.Equal(t, "public-text", result.Model)
				assert.Equal(t, "vendor-text", result.BillingModel)
				assert.Equal(t, "vendor-text", result.UpstreamModel)
				assert.Equal(t, stream, result.Stream)
				assert.Equal(t, http.StatusOK, recorder.Code)
				assert.Contains(t, recorder.Body.String(), "public-text")
				require.Len(t, upstream.requests, 1)
				req := upstream.requests[0]
				assert.Equal(t, result.UpstreamEndpoint, req.URL.Path)
				assert.Empty(t, req.Header.Get("Authorization"))
				assert.Empty(t, req.Header.Get("X-Extra-Secret"))
				assert.Empty(t, req.URL.Query().Get("client_secret"))
				assert.Equal(t, "yes", req.URL.Query().Get("signed"))
				assert.Equal(t, "saved", req.Header.Get("X-Config"))
				if path == newAPIProviderChat {
					assert.Equal(t, "/prefix/vendor/chat", req.URL.Path)
					assert.Equal(t, "upstream-fixture-key", req.Header.Get("X-Vendor-Key"))
				} else {
					assert.Equal(t, "/prefix/vendor/responses/vendor-text", req.URL.Path)
					assert.Equal(t, "upstream-fixture-key", req.URL.Query().Get("key"))
				}
				sent, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				digest := sha256.Sum256(sent)
				assert.Equal(t, hex.EncodeToString(digest[:]), req.Header.Get("X-Body-Digest"), "sign exact mapped and usage-normalized bytes")
				assert.Contains(t, string(sent), `"model":"vendor-text"`)
			})
		}
	}
}

func TestNewAPIProviderAdmissionAndFailures(t *testing.T) {
	s, account, upstream := providerFixture(t, providerFixtureSource)
	ctx := context.Background()
	assert.False(t, account.IsSchedulableForModel("public-text"))
	assert.True(t, account.IsSchedulableForModelWithContext(WithNewAPIProviderRouting(ctx, newAPIProviderChat), "public-text"))
	assert.False(t, account.IsSchedulableForModelWithContext(WithNewAPIProviderRouting(ctx, "/v1/messages"), "public-text"))
	assert.False(t, account.IsSchedulableForModelWithContext(WithNewAPIPluginRouting(ctx), "public-text"))
	assert.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
	assert.False(t, account.SupportsOpenAIEndpointCapability(""))
	assert.False(t, account.SupportsOpenAIEndpointCapability("plugin_task"))
	assert.Equal(t, []string{"public-text"}, account.NewAPIProviderModels())
	account.Extra = map[string]any{"openai_passthrough": true}
	assert.False(t, account.SupportsNewAPIProviderModel(newAPIProviderChat, "unconfigured-text"))
	scheduler := &defaultOpenAIAccountScheduler{}
	ok, reason := scheduler.isAccountRequestCompatibleReason(ctx, account, OpenAIAccountScheduleRequest{RequestedModel: "public-text", RequiredCapability: OpenAIEndpointCapabilityChatCompletions})
	assert.False(t, ok)
	assert.Equal(t, "plugin_protocol_mismatch", reason)

	for _, change := range []string{"disabled", "wrong-model", "compact", "cancelled", "duplicate-model", "case-model", "image-tool"} {
		t.Run(change, func(t *testing.T) {
			s.repo.(*requestPluginFixtureRepo).versions[1].Enabled = change != "disabled"
			body, path := `{"model":"public-text","input":"hello"}`, newAPIProviderResponses
			if change == "wrong-model" {
				body = `{"model":"other","input":"hello"}`
			}
			if change == "compact" {
				path += "/compact"
			}
			if change == "duplicate-model" {
				body = `{"model":"public-text","model":"other","input":"hello"}`
			}
			if change == "case-model" {
				body = `{"model":"public-text","Model":"other","input":"hello"}`
			}
			if change == "image-tool" {
				body = `{"model":"public-text","input":"draw","tools":[{"type":"image_generation"}]}`
			}
			c, _ := providerContext(path, body)
			if change == "cancelled" {
				requestCtx, cancel := context.WithCancel(c.Request.Context())
				cancel()
				c.Request = c.Request.WithContext(requestCtx)
			}
			_, err := s.gateway.Forward(c.Request.Context(), c, account, []byte(body))
			require.Error(t, err)
			assert.Empty(t, upstream.requests, "a rejected Provider must not use the native transport")
		})
	}
}

func TestNewAPIProviderUnsupportedRoutesAndSigning(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"login", providerFixtureSource + `export function login(){return {}}`},
		{"refresh", providerFixtureSource + `export function refresh(){return {}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo := requestPluginFixture(t, tc.source)
			repo.versions[1].Enabled = true
			binding := pluginProviderBinding(repo.versions[1].Meta, repo.versions[1].Capabilities)
			assert.True(t, binding.supports(newAPIProviderChat, "vendor-text"))
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{NewAPIPluginRevisionKey: 1, "base_url": "https://api.example.com"}}
			require.NoError(t, s.NormalizeAccountBinding(context.Background(), account, map[string]any{}))
		})
	}
	for _, source := range []string{
		strings.Replace(providerFixtureSource, `return {headers:{"X-Body-Digest":req.bodySha256,"X-Config":ctx.config.label},query:{signed:"yes"}}`, `return {headers:{"Host":"evil.example"}}`, 1),
		strings.Replace(providerFixtureSource, `return {headers:{"X-Body-Digest":req.bodySha256,"X-Config":ctx.config.label},query:{signed:"yes"}}`, `utils.fetch("https://api.example.com"); return {}`, 1),
		strings.ReplaceAll(providerFixtureSource, `/vendor/chat?fixed=yes`, `https://unapproved.example/chat`),
	} {
		s, account, upstream := providerFixture(t, source)
		c, recorder := providerContext(newAPIProviderChat, `{"model":"public-text","messages":[]}`)
		_, err := s.gateway.ForwardAsChatCompletions(c.Request.Context(), c, account, []byte(`{"model":"public-text","messages":[]}`), "", "")
		require.Error(t, err)
		assert.Empty(t, upstream.requests)
		assert.NotContains(t, recorder.Body.String(), "upstream-fixture-key")
	}
	// A route with unsupported pass-through mode must not fall through to a
	// later native catch-all, even though its converter itself is supported.
	binding := newAPIProviderBinding{Routes: []contract.AdvancedCustomRoute{
		{IncomingPath: newAPIProviderChat, Converter: "openai_chat_completions_to_openai_responses+plugin", PassThroughBodyEnabled: true, Models: []string{"special"}},
		{IncomingPath: newAPIProviderChat, Converter: "none"},
	}}
	assert.False(t, binding.supports(newAPIProviderChat, "special"))
	assert.True(t, binding.supports(newAPIProviderChat, "ordinary"))
}

func TestNewAPIProviderStreamTruncationAndErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		name, path, contentType, response string
		status                            int
	}{
		{"chat-truncated", newAPIProviderChat, "text/event-stream", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", 200},
		{"responses-truncated", newAPIProviderResponses, "text/event-stream", "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", 200},
		{"chat-error-with-usage", newAPIProviderChat, "text/event-stream", "data: {\"error\":{\"message\":\"failed\"},\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n", 200},
		{"redirect", newAPIProviderChat, "application/json", `{}`, 302},
		{"bad-request", newAPIProviderChat, "application/json", `{"error":{"message":"invalid vendor input","type":"invalid_request_error"}}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, account, upstream := providerFixture(t, providerFixtureSource)
			upstream.response, upstream.contentType, upstream.status = tc.response, tc.contentType, tc.status
			body := []byte(`{"model":"public-text","messages":[],"input":"hello","stream":true}`)
			c, _ := providerContext(tc.path, string(body))
			_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, body, tc.path, "")
			require.Error(t, err)
			require.Len(t, upstream.requests, 1, "neither redirect nor stream truncation may trigger an implicit native replay")
			assert.True(t, HTTPUpstreamRedirectsDisabled(upstream.requests[0].Context()))
			assert.True(t, HTTPUpstreamPublicHostsOnly(upstream.requests[0].Context()))
		})
	}
}

func TestNewAPIProviderRejectsFailedJSON(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		for _, payload := range []string{`{"error":{"message":"failed"},"usage":{"input_tokens":12}}`, `<html>maintenance</html>`, `{}`} {
			s, account, upstream := providerFixture(t, providerFixtureSource)
			upstream.response = payload
			body := []byte(`{"model":"public-text","messages":[],"input":"hello"}`)
			c, recorder := providerContext(path, string(body))
			_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, body, path, "")
			require.Error(t, err)
			assert.Equal(t, http.StatusBadGateway, recorder.Code)
			assert.NotContains(t, recorder.Body.String(), "maintenance")
		}
	}
}

func TestNewAPIProviderBuiltinAndExistingBilling(t *testing.T) {
	source, err := builtin.Source("sub2api-openai-relay")
	require.NoError(t, err)
	s, repo := requestPluginFixture(t, source)
	repo.versions[1].Enabled = true
	account := &Account{ID: 17, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{NewAPIPluginRevisionKey: 1, "base_url": "https://api.example.com", "api_key": "fixture-key"}}
	require.Error(t, s.NormalizeAccountBinding(context.Background(), account, map[string]any{}), "generic relay requires configured concrete models")
	account.Credentials["model_mapping"] = map[string]any{"public-text": "vendor-text"}
	account.Credentials["newapi_upstream_kind"] = "new_api"
	require.NoError(t, s.NormalizeAccountBinding(context.Background(), account, map[string]any{}))
	s.gateway.newAPIPlugins = s
	s.gateway.cfg = &config.Config{}
	upstream := s.gateway.httpUpstream.(*generationFixtureHTTP)
	upstream.response = `{"id":"chat-fixture","object":"chat.completion","model":"vendor-text","choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5}}`
	body := []byte(`{"model":"public-text","messages":[{"role":"user","content":"hello"}]}`)
	c, recorder := providerContext(newAPIProviderChat, string(body))
	result, err := s.gateway.ForwardAsChatCompletions(c.Request.Context(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, http.StatusOK, recorder.Code)
	require.Len(t, upstream.requests, 1)
	assert.Equal(t, "Bearer fixture-key", upstream.requests[0].Header.Get("Authorization"))
	assert.Equal(t, "/v1/chat/completions", upstream.requests[0].URL.Path)
	assertNewAPIProviderExistingBilling(t, result, account)
}

func assertNewAPIProviderExistingBilling(t *testing.T, result *OpenAIForwardResult, account *Account) {
	t.Helper()
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	billing.resolver = NewModelPricingResolver(nil, billing.billingService)
	groupID, inputPrice, outputPrice := int64(7), 2e-6, 4e-6
	group := &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 2, ModelPricing: []ChannelModelPricing{{Models: []string{"vendor-text"}, BillingMode: BillingModeToken, InputPrice: &inputPrice, OutputPrice: &outputPrice}}}
	result.RequestID = "provider-billing-fixture"
	require.NoError(t, billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: &APIKey{ID: 8, GroupID: &groupID, Group: group}, User: &User{ID: 9}, Account: account, InboundEndpoint: newAPIProviderChat, UpstreamEndpoint: result.UpstreamEndpoint}))
	require.NotNil(t, billingRepo.lastCmd)
	assert.InDelta(t, 0.000088, billingRepo.lastCmd.BalanceCost, 1e-10, "real 12/5 usage must use the existing group prices and 2x multiplier")
	assert.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	assert.Equal(t, "public-text", usageRepo.lastLog.Model)
	assert.Equal(t, 12, usageRepo.lastLog.InputTokens)
	assert.Equal(t, 5, usageRepo.lastLog.OutputTokens)
}

func TestNewAPIProviderSnapshotOverwrite(t *testing.T) {
	s, account, _ := providerFixture(t, providerFixtureSource)
	account.Credentials[newAPIPluginProviderKey] = map[string]any{"routes": []any{map[string]any{"incoming_path": "/v1/messages"}}}
	require.NoError(t, s.NormalizeAccountBinding(context.Background(), account, map[string]any{newAPIPluginProviderKey: account.Credentials[newAPIPluginProviderKey]}))
	assert.True(t, account.SupportsNewAPIProviderModel(newAPIProviderChat, "public-text"))
	assert.False(t, account.SupportsNewAPIProviderModel("/v1/messages", "public-text"))
	account.Credentials[NewAPIPluginRevisionKey] = nil
	require.NoError(t, s.NormalizeAccountBinding(context.Background(), account, map[string]any{}))
	assert.NotContains(t, account.Credentials, newAPIPluginProviderKey)
}
