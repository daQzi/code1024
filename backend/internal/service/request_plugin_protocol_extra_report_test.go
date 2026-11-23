//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNativeProtocolExtraConfirmedReport(t *testing.T) {
	t.Run("grok-voice-tts", func(t *testing.T) {
		s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, globalHTTPReportSource, []string{"report"}, "/v1/tts")
		account.Platform = PlatformGrok
		account.Type = AccountTypeAPIKey
		account.Credentials = map[string]any{"api_key": "xai-fixture-key", "base_url": "https://api.x.ai/v1"}
		plan.call.Format, plan.call.Path, plan.call.Model = "openai", "/v1/tts", "tts"

		audioBytes := []byte("\xff\xfb\x90\x00AUDIO-BINARY-BYTES-TTS")
		upstream.contentType = "audio/mpeg"
		upstream.response = string(audioBytes)

		reports := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 4)}
		s.gateway.httpUpstream = &relayReportHTTP{upstream: s.gateway.httpUpstream, reports: reports}
		s.taskReports = newTaskReportExecutor(s)
		t.Cleanup(s.taskReports.close)

		body := []byte(`{"input":"hello voice","voice":"eve"}`)
		c, recorder := providerContext("/v1/tts", string(body))
		c.Request.Header.Set("Content-Type", "application/json")
		ctx := WithRequestPluginPlan(c.Request.Context(), plan)
		report := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "voice-user")
		require.NotNil(t, report)
		c.Writer = report

		result, err := s.gateway.ForwardGrokVoice(ctx, c, account, "tts", body, "application/json")
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.AudioUsage)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, audioBytes, recorder.Body.Bytes())

		report.Finish(body)
		require.Empty(t, reports.requests, "TTS report must wait for confirmed ledger commit")

		usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
		billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
		billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
		groupID := int64(7)
		ttsPrice := 100.0
		err = billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
			Result: result,
			APIKey: &APIKey{ID: 5, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformGrok, RateMultiplier: 1.5, AudioTTSPricePerMillionChars: &ttsPrice}},
			User:   &User{ID: 4},
			Account: account,
		})
		require.NoError(t, err)
		require.Equal(t, 1, billingRepo.calls)
		require.Positive(t, usageRepo.lastLog.ActualCost)

		payload := awaitTaskReport(t, reports)
		require.Equal(t, "/v1/tts", gjson.GetBytes(payload["ctx"], "path").Str)
		require.Equal(t, decimal.NewFromFloat(usageRepo.lastLog.ActualCost).Round(8).String(), gjson.GetBytes(payload["record"], "cost.usd").String())
		require.EqualValues(t, len("hello voice"), gjson.GetBytes(payload["record"], "usage.audioChars").Int())
		require.Equal(t, "audio/mpeg", gjson.GetBytes(payload["record"], "response.contentType").Str)
		require.EqualValues(t, len(audioBytes), gjson.GetBytes(payload["record"], "response.bytes").Int())
		require.False(t, gjson.GetBytes(payload["record"], "response.body").Exists(), "binary audio response must not be serialized as JSON body")
		require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 1 }, 5*time.Second, 5*time.Millisecond)
		require.Zero(t, s.TaskReportStats().Failed)
	})

	t.Run("grok-voice-stt-multipart", func(t *testing.T) {
		s, account, upstream, plan := providerPolicyFixture(t, providerFixtureSource, globalHTTPReportSource, []string{"report"}, "/v1/stt")
		account.Platform = PlatformGrok
		account.Type = AccountTypeAPIKey
		account.Credentials = map[string]any{"api_key": "xai-fixture-key", "base_url": "https://api.x.ai/v1"}
		plan.call.Format, plan.call.Path, plan.call.Model = "openai", "/v1/stt", "stt"

		upstream.contentType = "application/json"
		upstream.response = `{"text":"hello transcript","duration":3.5}`

		reports := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 4)}
		s.gateway.httpUpstream = &relayReportHTTP{upstream: s.gateway.httpUpstream, reports: reports}
		s.taskReports = newTaskReportExecutor(s)
		t.Cleanup(s.taskReports.close)

		var mpBuf bytes.Buffer
		writer := multipart.NewWriter(&mpBuf)
		require.NoError(t, writer.WriteField("language", "en"))
		hdr := make(textproto.MIMEHeader)
		hdr.Set("Content-Disposition", `form-data; name="file"; filename="clip.wav"`)
		hdr.Set("Content-Type", "audio/wav")
		part, err := writer.CreatePart(hdr)
		require.NoError(t, err)
		audioPayload := []byte("RIFF\x24\x00\x00\x00WAVEBINARYAUDIOSECRET")
		_, err = part.Write(audioPayload)
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		rawBody := mpBuf.Bytes()
		contentType := writer.FormDataContentType()

		c, recorder := providerContext("/v1/stt", "")
		c.Request = httptestNewMultipartRequest("/v1/stt", rawBody, contentType)
		ctx := WithRequestPluginPlan(c.Request.Context(), plan)
		report := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "voice-user")
		require.NotNil(t, report)
		mpForm, err := ParseRequestPluginMultipart(contentType, rawBody)
		require.NoError(t, err)
		report.SetMultipart(mpForm)
		c.Writer = report

		result, err := s.gateway.ForwardGrokVoice(ctx, c, account, "stt", rawBody, contentType)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.AudioUsage)
		require.Equal(t, http.StatusOK, recorder.Code)

		report.Finish(rawBody)
		require.Empty(t, reports.requests)

		usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
		billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
		billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
		groupID := int64(7)
		sttPrice := 3.6
		err = billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
			Result:  result,
			APIKey:  &APIKey{ID: 5, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformGrok, RateMultiplier: 1.0, AudioSTTPricePerHour: &sttPrice}},
			User:    &User{ID: 4},
			Account: account,
		})
		require.NoError(t, err)
		require.Equal(t, 1, billingRepo.calls)
		require.Positive(t, usageRepo.lastLog.ActualCost)

		payload := awaitTaskReport(t, reports)
		require.Equal(t, "/v1/stt", gjson.GetBytes(payload["ctx"], "path").Str)
		require.Equal(t, decimal.NewFromFloat(usageRepo.lastLog.ActualCost).Round(8).String(), gjson.GetBytes(payload["record"], "cost.usd").String())
		require.EqualValues(t, 3500, gjson.GetBytes(payload["record"], "usage.audioDurationMs").Int())
		require.Equal(t, "en", gjson.GetBytes(payload["record"], "request.body.language").Str)
		mediaItems := gjson.GetBytes(payload["record"], "request.media").Array()
		require.Len(t, mediaItems, 1)
		require.Equal(t, "audio/wav", mediaItems[0].Get("mimeType").Str)
		require.NotContains(t, string(payload["record"]), "WAVEBINARYAUDIOSECRET")
		require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 1 }, 5*time.Second, 5*time.Millisecond)
		require.Zero(t, s.TaskReportStats().Failed)
	})

	t.Run("alpha-search-ok-and-4xx-passthrough", func(t *testing.T) {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
			t.Run(http.StatusText(status), func(t *testing.T) {
				s, account, _, plan := providerPolicyFixture(t, providerFixtureSource, globalHTTPReportSource, []string{"report"}, "/v1/alpha/search")
				account.Platform = PlatformOpenAI
				account.Type = AccountTypeOAuth
				account.Credentials = map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-account"}
				plan.call.Format, plan.call.Path, plan.call.Model = "openai", "/v1/alpha/search", "gpt-5.6-sol"

				respBody := `{"output":"alpha result"}`
				if status >= 400 {
					respBody = `{"error":{"message":"invalid query"}}`
				}
				upstreamStub := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: status,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(respBody)),
				}}
				reports := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 4)}
				s.gateway.httpUpstream = &relayReportHTTP{upstream: upstreamStub, reports: reports}
				s.taskReports = newTaskReportExecutor(s)
				t.Cleanup(s.taskReports.close)

				body := []byte(`{"model":"gpt-5.6-sol","commands":{"search_query":[{"q":"sub2api"}]}}`)
				c, recorder := providerContext("/v1/alpha/search", string(body))
				c.Request.Header.Set("Content-Type", "application/json")
				ctx := WithRequestPluginPlan(c.Request.Context(), plan)
				report := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "search-user")
				require.NotNil(t, report)
				c.Writer = report

				result, err := s.gateway.ForwardAlphaSearch(ctx, c, account, body)
				require.NoError(t, err)
				require.Equal(t, status, recorder.Code)

				report.Finish(body)
				if status >= 400 {
					require.Nil(t, result)
					payload := awaitTaskReport(t, reports)
					require.Equal(t, "0", gjson.GetBytes(payload["record"], "cost.usd").String())
					require.EqualValues(t, http.StatusBadRequest, gjson.GetBytes(payload["record"], "response.status").Int())
					require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 1 }, 5*time.Second, 5*time.Millisecond)
					return
				}

				require.NotNil(t, result)
				require.Equal(t, 1, result.WebSearchCalls)
				require.Empty(t, reports.requests, "2xx alpha search must wait for confirmed ledger")

				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
				billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
				groupID := int64(7)
				perCall := 0.02
				result.RequestID = "alpha-search-billing-1"
				err = billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
					Result:  result,
					APIKey:  &APIKey{ID: 5, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 1.5, WebSearchPricePerCall: &perCall}},
					User:    &User{ID: 4},
					Account: account,
				})
				require.NoError(t, err)
				require.Equal(t, 1, billingRepo.calls)

				payload := awaitTaskReport(t, reports)
				require.Equal(t, decimal.NewFromFloat(usageRepo.lastLog.ActualCost).Round(8).String(), gjson.GetBytes(payload["record"], "cost.usd").String())
				require.EqualValues(t, 1, gjson.GetBytes(payload["record"], "usage.webSearchCalls").Int())
				require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 1 }, 5*time.Second, 5*time.Millisecond)
				require.Zero(t, s.TaskReportStats().Failed)
			})
		}
	})

	t.Run("web-search-and-x-search", func(t *testing.T) {
		for _, tc := range []struct {
			path  string
			model string
		}{
			{path: "/v1/web_search", model: "grok-web-search"},
			{path: "/v1/x_search", model: "grok-x-search"},
		} {
			t.Run(tc.model, func(t *testing.T) {
				s, account, _, plan := providerPolicyFixture(t, providerFixtureSource, globalHTTPReportSource, []string{"report"}, tc.path)
				account.Platform = PlatformGrok
				account.Type = AccountTypeAPIKey
				plan.call.Format, plan.call.Path, plan.call.Model = "openai", tc.path, tc.model

				reports := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 4)}
				s.gateway.httpUpstream = &relayReportHTTP{reports: reports}
				s.taskReports = newTaskReportExecutor(s)
				t.Cleanup(s.taskReports.close)

				body := []byte(`{"query":"latest release"}`)
				c, _ := providerContext(tc.path, string(body))
				ctx := WithRequestPluginPlan(c.Request.Context(), plan)
				report := s.BeginRequestPluginHTTPReport(ctx, c.Writer, "search-user")
				require.NotNil(t, report)
				c.Writer = report

				attemptReport := BeginNativeHTTPReportAttempt(ctx, account, "grok-3")
				require.Same(t, report, attemptReport)
				forwardResult := &ForwardResult{
					RequestID:     "search-req-" + tc.model,
					Model:         tc.model,
					UpstreamModel: "grok-3",
					SearchCount:   1,
				}
				attemptReport.NativeResult(forwardResult)
				c.Data(http.StatusOK, "application/json", []byte(`{"results":[{"title":"ok"}]}`))
				report.Finish(body)
				require.Empty(t, reports.requests)

				usage := &openAIRecordUsageLogRepoStub{}
				ledger := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
				billing := newGatewayRecordUsageServiceWithBillingRepoForTest(usage, ledger, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
				billing.billingService = NewBillingService(billing.cfg, &PricingService{pricingData: map[string]*LiteLLMModelPricing{}})
				groupID := int64(7)
				per1K := 5.0
				err := billing.RecordUsage(context.Background(), &RecordUsageInput{
					Result:  forwardResult,
					APIKey:  &APIKey{ID: 5, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformGrok, RateMultiplier: 2.0, SearchPricePer1k: &per1K}},
					User:    &User{ID: 4},
					Account: account,
				})
				require.NoError(t, err)
				require.Equal(t, 1, ledger.calls)
				require.InDelta(t, 0.01, usage.lastLog.ActualCost, 1e-10)

				payload := awaitTaskReport(t, reports)
				require.Equal(t, tc.path, gjson.GetBytes(payload["ctx"], "path").Str)
				require.Equal(t, "0.01", gjson.GetBytes(payload["record"], "cost.usd").String())
				require.EqualValues(t, 1, gjson.GetBytes(payload["record"], "usage.webSearchCalls").Int())
				require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 1 }, 5*time.Second, 5*time.Millisecond)
				require.Zero(t, s.TaskReportStats().Failed)
			})
		}
	})
}
