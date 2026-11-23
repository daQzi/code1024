//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/relaymetrics"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const providerEmbeddingResponse = `{"object":"list","model":"vendor-text","data":[{"object":"embedding","index":0,"embedding":[0.1,-0.2,0.3]}],"usage":{"prompt_tokens":12,"total_tokens":12}}`

func providerEmbeddingSource(converter string) string {
	return fmt.Sprintf(`export const meta={apiVersion:1,key:"embedding-fixture",name:"Embedding fixture",version:"1.0.0",author:{name:"Fixture"},models:["vendor-text"],relay:[{incoming_path:"/v1/embeddings",upstream_path:"/vector",converter:%q,models:["vendor-text"],auth:{type:"header",name:"X-Vendor-Key",value:"{api_key}"}}]};
export function signRequest(ctx,req){return {headers:{"X-Body-Digest":req.bodySha256}};}`, converter)
}

func TestNewAPIProviderEmbeddingsWireUsageBillingAndPlaza(t *testing.T) {
	for _, converter := range []string{"none", "plugin"} {
		t.Run(converter, func(t *testing.T) {
			source := providerEmbeddingSource(converter)
			if converter == "plugin" {
				source += `export function convertRequest(ctx,req){return {body:{deployment:req.model,values:req.body.input},state:{pinned:"yes"}};} export function convertResponse(ctx,resp){if(ctx.state.pinned!=="yes")throw Error("state lost");return {body:JSON.parse(resp.body).result};} export function convertStreamEvent(){throw Error("Embeddings must not stream");}`
			}
			s, account, upstream := providerFixture(t, source)
			require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityEmbeddings))
			require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
			ctx := WithNewAPIProviderRouting(context.Background(), newAPIProviderEmbeddings)
			require.True(t, account.IsSchedulableForModelWithContext(ctx, "public-text"))
			require.False(t, account.IsSchedulableForModelWithContext(WithNewAPIProviderRouting(ctx, newAPIProviderChat), "public-text"))
			require.Equal(t, []string{"public-text"}, account.NewAPIProviderModels())
			_, protocols := plazaAccountCapabilities("public-text", []*Account{account}, nil)
			require.Equal(t, []string{"openai_embeddings"}, protocols)
			upstream.response = providerEmbeddingResponse
			if converter == "plugin" {
				upstream.response = `{"result":` + providerEmbeddingResponse + `}`
			}
			body := `{"model":"public-text","input":["hello"],"encoding_format":"float"}`
			c, recorder := providerContext(newAPIProviderEmbeddings, body)
			result, err := s.gateway.ForwardEmbeddings(c.Request.Context(), c, account, []byte(body), "")
			require.NoError(t, err, recorder.Body.String())
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, "public-text", gjson.Get(recorder.Body.String(), "model").String())
			require.Equal(t, 12, result.Usage.InputTokens)
			require.Zero(t, result.Usage.OutputTokens)
			require.Equal(t, "vendor-text", result.BillingModel)
			require.Equal(t, "vendor-text", result.UpstreamResponseModel)
			require.Equal(t, relaymetrics.Success, result.PerformanceOutcome)
			require.Len(t, upstream.requests, 1)
			req := upstream.requests[0]
			raw, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, "/prefix/vector", req.URL.Path)
			require.Equal(t, "upstream-fixture-key", req.Header.Get("X-Vendor-Key"))
			require.Empty(t, req.Header.Get("Authorization"))
			require.Empty(t, req.Header.Get("X-Extra-Secret"))
			digest := sha256.Sum256(raw)
			require.Equal(t, hex.EncodeToString(digest[:]), req.Header.Get("X-Body-Digest"))
			if converter == "plugin" {
				require.JSONEq(t, `{"deployment":"vendor-text","values":["hello"]}`, string(raw))
			} else {
				require.Equal(t, "vendor-text", gjson.GetBytes(raw, "model").String())
			}
			assertNewAPIProviderEmbeddingBilling(t, result, account)
		})
	}
}

func assertNewAPIProviderEmbeddingBilling(t *testing.T, result *OpenAIForwardResult, account *Account) {
	t.Helper()
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	billing.resolver = NewModelPricingResolver(nil, billing.billingService)
	groupID, inputPrice, outputPrice := int64(7), 2e-6, 4e-6
	group := &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 2, ModelPricing: []ChannelModelPricing{{Models: []string{"vendor-text"}, BillingMode: BillingModeToken, InputPrice: &inputPrice, OutputPrice: &outputPrice}}}
	result.RequestID = "embedding-billing-fixture"
	require.NoError(t, billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: &APIKey{ID: 8, GroupID: &groupID, Group: group}, User: &User{ID: 9}, Account: account, InboundEndpoint: newAPIProviderEmbeddings, UpstreamEndpoint: result.UpstreamEndpoint}))
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, 0.000048, billingRepo.lastCmd.BalanceCost, 1e-10)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "public-text", usageRepo.lastLog.Model)
	require.Equal(t, 12, usageRepo.lastLog.InputTokens)
	require.Zero(t, usageRepo.lastLog.OutputTokens)
}

func TestNewAPIProviderEmbeddingsRejectsUnsupportedShapes(t *testing.T) {
	s, account, upstream := providerFixture(t, providerEmbeddingSource("none"))
	for _, body := range []string{`{"model":"public-text","input":"hello","stream":true}`, `{"model":"public-text@effort:high","input":"hello"}`} {
		c, recorder := providerContext(newAPIProviderEmbeddings, body)
		_, err := s.gateway.ForwardEmbeddings(c.Request.Context(), c, account, []byte(body), "")
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Empty(t, upstream.requests)
	}
	for _, response := range []string{`{}`, `{"data":[]}`, `{"data":[{"embedding":[]}]}`, `{"data":[{"embedding":[1]}],"error":{"message":"private failure"}}`, `{"data":[{"embedding":[1]}],"data":[]}`, `{"padding":"` + strings.Repeat("a", MaxRequestPluginBody) + `"}`} {
		upstream.response = response
		c, recorder := providerContext(newAPIProviderEmbeddings, `{"model":"public-text","input":"hello"}`)
		result, err := s.gateway.ForwardEmbeddings(c.Request.Context(), c, account, []byte(`{"model":"public-text","input":"hello"}`), "")
		require.Error(t, err)
		require.Nil(t, result)
		require.Equal(t, http.StatusBadGateway, recorder.Code)
		require.NotContains(t, recorder.Body.String(), "private failure")
	}
}

func TestNewAPIProviderConvertedRoutesParticipateInEndpointAdmission(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{}
	for _, tc := range providerPresetCases() {
		for _, composed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/plugin=%t", tc.converter, composed), func(t *testing.T) {
				_, account, _ := providerFixture(t, providerPresetSource(tc, composed))
				capability := OpenAIEndpointCapabilityChatCompletions
				if tc.path == newAPIProviderResponses {
					capability = OpenAIEndpointCapabilityResponses
				}
				require.True(t, account.SupportsOpenAIEndpointCapability(capability))
				ctx := WithNewAPIProviderRouting(context.Background(), tc.path)
				ok, reason := scheduler.isAccountRequestCompatibleReason(ctx, account, OpenAIAccountScheduleRequest{RequestedModel: "public-text", RequiredCapability: capability, RequiredTransport: OpenAIUpstreamTransportHTTPSSE})
				require.True(t, ok, reason)
			})
		}
	}
}

func TestNewAPIProviderEmbeddingsRequiresExplicitBoundedUsage(t *testing.T) {
	for _, test := range []struct {
		usage string
		valid bool
	}{
		{`{}`, false}, {`null`, false}, {`{"prompt_tokens":"12"}`, false},
		{`{"prompt_tokens":-1}`, false}, {`{"prompt_tokens":0.5}`, false},
		{`{"prompt_tokens":1e99}`, false}, {`{"prompt_tokens":12,"completion_tokens":false}`, false},
		{`{"prompt_tokens":0,"total_tokens":0}`, true}, {`{"input_tokens":12}`, true}, {`{"total_tokens":12}`, true},
	} {
		t.Run(test.usage, func(t *testing.T) {
			require.Equal(t, test.valid, newAPIProviderEmbeddingUsage(gjson.Parse(test.usage)))
		})
	}
	s, account, upstream := providerFixture(t, providerEmbeddingSource("none"))
	upstream.response = `{"model":"vendor-text","data":[{"embedding":[1]}]}`
	body := []byte(`{"model":"public-text","input":"hello"}`)
	c, recorder := providerContext(newAPIProviderEmbeddings, string(body))
	result, err := s.gateway.ForwardEmbeddings(c.Request.Context(), c, account, body, "")
	require.Error(t, err)
	require.Nil(t, result, "missing actual usage must not become a zero-cost successful result")
	require.Equal(t, http.StatusBadGateway, recorder.Code)
}

func TestEmbeddingRequestStagesAndConfirmedReport(t *testing.T) {
	for _, provider := range []bool{false, true} {
		t.Run(fmt.Sprintf("provider=%t", provider), func(t *testing.T) {
			source := globalHTTPReportSource + `
export function request(ctx,req){
 if(ctx.format!=="openai" || ctx.path!=="/v1/embeddings" || req.body.model!=="public-text" || req.summary.totalTextLength!==5 || ctx.upstreamModel!=="vendor-text")throw Error("wrong request identity");
 return {action:"continue"};
}
export function ingress(ctx,req){
 if(req.body.model!=="vendor-text" || req.body.input!=="hello" || !ctx.channel)throw Error("wrong late ingress");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/input",value:"sanitized"}]}]};
}
export function outbound(ctx,req){
 if(req.body.input!=="sanitized" || req.headers?.Authorization || req.headers?.["X-Vendor-Key"])throw Error("wrong outbound");
 return {action:"continue",effects:[{type:"json.patch",ops:[{op:"add",path:"/user",value:"policy"}]},{type:"header.patch",set:{"X-Policy":"embedding"}}]};
}
export function egress(ctx,req){
 if(req.body.user!=="policy" || req.headers?.["X-Policy"]!=="embedding" || req.body.reasoning_effort || req.body.stream_options)throw Error("wrong final embedding body");
 return {action:"continue"};
}
export function reply(){throw Error("vectors must never be treated as generated text");}
`
			s, account, upstream, plan := providerPolicyFixture(t, providerEmbeddingSource("none"), source, []string{"ingress", "request", "outbound", "egress", "reply", "report"}, newAPIProviderEmbeddings)
			if !provider {
				nativeRequestAccount(account, false)
			}
			upstream.response = providerEmbeddingResponse
			reports := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 4)}
			s.gateway.httpUpstream = &relayReportHTTP{upstream: s.gateway.httpUpstream, reports: reports}
			s.taskReports = newTaskReportExecutor(s)
			t.Cleanup(s.taskReports.close)
			body := []byte(`{"model":"public-text","input":"hello"}`)
			c, w := providerContext(newAPIProviderEmbeddings, string(body))
			original := c.Request
			ctx := WithRequestPluginPlan(c.Request.Context(), plan)
			report := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "")
			require.NotNil(t, report)
			c.Writer = report
			result, err := s.gateway.ForwardEmbeddings(ctx, c, account, body, "")
			require.NoError(t, err, "runs: %+v", s.repo.(*requestPluginFixtureRepo).runs)
			require.Same(t, original, c.Request)
			require.Same(t, report, c.Writer)
			require.Len(t, upstream.requests, 1)
			sent, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"model":"vendor-text","input":"sanitized","user":"policy"}`, string(sent))
			require.Equal(t, "embedding", upstream.requests[0].Header.Get("X-Policy"))
			require.Equal(t, 12, result.Usage.InputTokens)
			require.Zero(t, result.Usage.OutputTokens)
			require.JSONEq(t, `[0.1,-0.2,0.3]`, gjson.GetBytes(w.Body.Bytes(), "data.0.embedding").Raw)
			assertNewAPIProviderEmbeddingBilling(t, result, account)
			report.Finish(body)
			result.pluginReportBilling.complete(decimal.RequireFromString("0.000048"), true)
			payload := awaitTaskReport(t, reports)
			require.Equal(t, newAPIProviderEmbeddings, gjson.GetBytes(payload["ctx"], "path").String())
			require.Equal(t, "openai", gjson.GetBytes(payload["ctx"], "format").String())
			require.Equal(t, "0.000048", gjson.GetBytes(payload["record"], "cost.usd").String())
			counts := map[string]int{}
			for _, record := range gjson.GetBytes(payload["record"], "requestPlugins").Array() {
				counts[record.Get("stage").String()]++
			}
			require.Equal(t, map[string]int{"request": 1, "ingress": 1, "outbound": 1, "egress": 1}, counts, "reply is outside the pinned embedding contract")
			require.JSONEq(t, w.Body.String(), gjson.GetBytes(payload["record"], "response.body").Raw)
		})
	}
}

func TestEmbeddingPolicyFailureStopsBeforeUpstream(t *testing.T) {
	for _, provider := range []bool{false, true} {
		for _, tc := range []struct{ stage, hook string }{
			{"request", `return {action:"continue",effects:[{type:"reasoning.set",effort:"high"}]}`},
			{"outbound", `return {action:"continue",effects:[{type:"header.patch",set:{Authorization:"private"}}]}`},
			{"outbound", `return {action:"continue",effects:[{type:"json.patch",ops:[{op:"replace",path:"/model",value:"other"}]}]}`},
			{"egress", `return {action:"reject",message:"private upstream detail"}`},
		} {
			t.Run(fmt.Sprintf("provider=%t/%s/%s", provider, tc.stage, tc.hook), func(t *testing.T) {
				source := providerPolicyMeta + `export function ` + tc.stage + `(){` + tc.hook + `}`
				s, account, upstream, plan := providerPolicyFixture(t, providerEmbeddingSource("none"), source, []string{tc.stage}, newAPIProviderEmbeddings)
				if !provider {
					nativeRequestAccount(account, false)
				}
				body := []byte(`{"model":"public-text","input":"hello"}`)
				c, w := providerContext(newAPIProviderEmbeddings, string(body))
				result, err := s.gateway.ForwardEmbeddings(WithRequestPluginPlan(c.Request.Context(), plan), c, account, body, "")
				var failure *RequestPluginFailure
				require.True(t, errors.As(err, &failure))
				require.Nil(t, result)
				require.Empty(t, upstream.requests)
				require.NotContains(t, w.Body.String(), "private")
				require.GreaterOrEqual(t, w.Code, 400)
			})
		}
	}
}
