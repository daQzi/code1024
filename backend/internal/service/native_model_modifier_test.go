package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type nativeModifierStubHTTP struct {
	send func(*http.Request) (*http.Response, error)
}

func (s *nativeModifierStubHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return s.send(req)
}

func (s *nativeModifierStubHTTP) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.send(req)
}

func TestNativeModelModifierAccountLookupMappingAndPricing(t *testing.T) {
	t.Cleanup(func() { require.NoError(t, modelmodifier.Configure(nil)) })

	account := &Account{
		ID:       1,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5":  "gpt-5-upstream",
				"public": "vendor@effort:low",
			},
		},
	}
	require.True(t, account.IsModelSupported("gpt-5@effort:high@temperature:0.2"))
	require.True(t, account.IsModelSupported("public@effort:high"))
	require.False(t, account.IsModelSupported("other@effort:high"))

	mapped, matched := account.ResolveMappedModel("gpt-5@effort:high")
	require.True(t, matched)
	require.Equal(t, "gpt-5-upstream", mapped)

	// Hot-editable exemption preserves exact literal name without stripping @...
	require.NoError(t, modelmodifier.Configure([]string{"real@effort:high"}))
	require.False(t, account.IsModelSupported("real@effort:high"))
	account.Credentials["model_mapping"] = map[string]any{"real@effort:high": "real@effort:high"}
	require.True(t, account.IsModelSupported("real@effort:high"))
	mapped, matched = account.ResolveMappedModel("real@effort:high")
	require.True(t, matched)
	require.Equal(t, "real@effort:high", mapped)
	require.NoError(t, modelmodifier.Configure(nil))

	// PricingService lookup candidates include modifier billing fallbacks.
	ps := &PricingService{}
	cands := ps.buildModelLookupCandidates("claude-sonnet-4-5@effort:high@thinking:on")
	require.Contains(t, cands, "claude-sonnet-4-5@effort:high@thinking:on")
	require.Contains(t, cands, "claude-sonnet-4-5@thinking:on")
	require.Contains(t, cands, "claude-sonnet-4-5")

	// Gemini client model validation admits modifiers on safe base models, rejects traversal.
	require.True(t, IsSafeGeminiClientModelName("gemini-2.5-pro"))
	require.True(t, IsSafeGeminiClientModelName("gemini-2.5-pro@thinking:2048@temperature:0.2"))
	require.True(t, IsSafeGeminiClientModelName("gemini-3-pro@effort:low"))
	require.False(t, IsSafeGeminiClientModelName("../evil@effort:high"))
	require.False(t, IsSafeGeminiClientModelName("gemini-2.5-pro@effort:high/../../x"))
	require.False(t, IsSafeGeminiClientModelName("gemini-2.5-pro@effort:high?key=1"))
}

func TestNativeModelModifierOpenAIForwardAndChatRejectsInvalidAndStripsUpstream(t *testing.T) {
	t.Cleanup(func() { require.NoError(t, modelmodifier.Configure(nil)) })
	gin.SetMode(gin.TestMode)

	// 1. OpenAI Responses Forward: valid modifier strips @ from upstream, sets reasoning.effort/temperature, reconciles billing.
	var captured []*http.Request
	var capturedBodies [][]byte
	upstream := &nativeModifierStubHTTP{send: func(req *http.Request) (*http.Response, error) {
		captured = append(captured, req)
		raw, _ := io.ReadAll(req.Body)
		capturedBodies = append(capturedBodies, raw)
		respBody := `{"id":"resp_1","object":"response","model":"gpt-5-upstream","status":"completed","output":[{"id":"m1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(respBody)),
		}, nil
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:       21,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Status:   StatusActive,
		Credentials: map[string]any{
			"api_key":       "sk-test",
			"base_url":      "https://api.openai.com/v1",
			"model_mapping": map[string]any{"public-gpt": "gpt-5-upstream@effort:low"},
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	reqBody := `{"model":"public-gpt@effort:high@temperature:0.25@topp:0.9","input":"hi"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(reqBody))

	res, err := svc.Forward(c.Request.Context(), c, account, []byte(reqBody))
	require.NoError(t, err, rec.Body.String())
	require.NotNil(t, res)
	require.Len(t, capturedBodies, 1)
	require.Equal(t, "gpt-5-upstream", gjson.GetBytes(capturedBodies[0], "model").String())
	require.NotContains(t, gjson.GetBytes(capturedBodies[0], "model").String(), "@")
	// Mapping target @effort:low overrides client @effort:high while keeping client temperature/top_p.
	require.Equal(t, "low", gjson.GetBytes(capturedBodies[0], "reasoning.effort").String())
	require.Equal(t, 0.25, gjson.GetBytes(capturedBodies[0], "temperature").Float())
	require.Equal(t, 0.9, gjson.GetBytes(capturedBodies[0], "top_p").Float())
	require.Equal(t, "public-gpt@effort:high@temperature:0.25@topp:0.9", res.Model)
	require.Equal(t, "gpt-5-upstream", res.UpstreamModel)
	require.Equal(t, "gpt-5-upstream@effort:low@thinking:on", res.BillingModel)
	require.NotNil(t, res.ReasoningEffort)
	require.Equal(t, "low", *res.ReasoningEffort)

	// 2. Invalid modifier fails closed with local 400 and ZERO upstream calls.
	captured = nil
	capturedBodies = nil
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	badBody := `{"model":"public-gpt@effort:invalid","input":"hi"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(badBody))
	_, err = svc.Forward(c.Request.Context(), c, account, []byte(badBody))
	require.ErrorIs(t, err, modelmodifier.ErrInvalid)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_model_modifier", gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
	require.Empty(t, captured)

	// 3. Invalid modifier on Chat Completions also fails closed with local 400 and zero upstream calls.
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	badChat := `{"model":"public-gpt@temperature:NaN","messages":[{"role":"user","content":"hi"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(badChat))
	_, err = svc.ForwardAsChatCompletions(c.Request.Context(), c, account, []byte(badChat), "", "")
	require.ErrorIs(t, err, modelmodifier.ErrInvalid)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, captured)
}

func TestNativeModelModifierAnthropicAndGeminiBillingDiscipline(t *testing.T) {
	t.Cleanup(func() { require.NoError(t, modelmodifier.Configure(nil)) })
	gin.SetMode(gin.TestMode)

	// Anthropic Forward: valid modifier strips @ from upstream, renders Claude thinking/max_tokens; invalid returns Anthropic 400 with zero upstream I/O.
	var anthBodies [][]byte
	anthUpstream := &nativeModifierStubHTTP{send: func(req *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(req.Body)
		anthBodies = append(anthBodies, raw)
		respBody := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(respBody)),
		}, nil
	}}
	gw := &GatewayService{cfg: &config.Config{}, httpUpstream: anthUpstream}
	anthAcct := &Account{ID: 31, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "sk-ant", "base_url": "https://api.anthropic.com"}}
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-sonnet-4-5@effort:bogus","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)), PlatformAnthropic)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	_, err = gw.Forward(c.Request.Context(), c, anthAcct, parsed)
	require.ErrorIs(t, err, modelmodifier.ErrInvalid)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "error", gjson.GetBytes(rec.Body.Bytes(), "type").String())
	require.Empty(t, anthBodies)

	validParsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-sonnet-4-5@thinking:2048@temperature:0.2","max_tokens":1024,"messages":[{"role":"user","content":"hi"}]}`)), PlatformAnthropic)
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	anthRes, err := gw.Forward(c.Request.Context(), c, anthAcct, validParsed)
	require.NoError(t, err, rec.Body.String())
	require.NotNil(t, anthRes)
	require.Len(t, anthBodies, 1)
	require.Equal(t, "claude-sonnet-4-5", gjson.GetBytes(anthBodies[0], "model").String())
	require.Equal(t, "enabled", gjson.GetBytes(anthBodies[0], "thinking.type").String())
	require.EqualValues(t, 2048, gjson.GetBytes(anthBodies[0], "thinking.budget_tokens").Int())
	require.Greater(t, gjson.GetBytes(anthBodies[0], "max_tokens").Int(), int64(2048))
	require.Equal(t, 0.2, gjson.GetBytes(anthBodies[0], "temperature").Float())
	require.Equal(t, "claude-sonnet-4-5@thinking:2048@temperature:0.2", anthRes.Model)
	require.Equal(t, "claude-sonnet-4-5", anthRes.UpstreamModel)

	// Gemini ForwardNative: thinkingBudget does NOT become billable ReasoningEffort; explicit thinkingLevel DOES; upstream URL never has @.
	var gemURLs []string
	var gemBodies [][]byte
	gemUpstream := &nativeModifierStubHTTP{send: func(req *http.Request) (*http.Response, error) {
		gemURLs = append(gemURLs, req.URL.Path)
		raw, _ := io.ReadAll(req.Body)
		gemBodies = append(gemBodies, raw)
		respBody := `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":4,"totalTokenCount":12}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(respBody)),
		}, nil
	}}
	gemSvc := &GeminiMessagesCompatService{
		cfg:          &config.Config{},
		httpUpstream: gemUpstream,
	}
	gemAcct := &Account{
		ID:       41,
		Platform: PlatformGemini,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "AIza-test",
			"base_url": "https://generativelanguage.googleapis.com",
		},
	}

	// Case A: Gemini 2.5 budget modifier -> sends thinkingBudget=2048, strips @ from URL, ReasoningEffort stays nil!
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	gemReq := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-pro@thinking:2048@temperature:0.2:generateContent", strings.NewReader(gemReq))
	res, err := gemSvc.ForwardNative(c.Request.Context(), c, gemAcct, "gemini-2.5-pro@thinking:2048@temperature:0.2", "generateContent", false, []byte(gemReq))
	require.NoError(t, err, rec.Body.String())
	require.NotNil(t, res)
	require.Len(t, gemURLs, 1)
	require.Equal(t, "/v1beta/models/gemini-2.5-pro:generateContent", gemURLs[0])
	require.NotContains(t, gemURLs[0], "@")
	require.EqualValues(t, 2048, gjson.GetBytes(gemBodies[0], "generationConfig.thinkingConfig.thinkingBudget").Int())
	require.Equal(t, 0.2, gjson.GetBytes(gemBodies[0], "generationConfig.temperature").Float())
	require.Nil(t, res.ReasoningEffort, "Gemini billing must never infer a reasoning level from thinkingBudget")
	require.Equal(t, "gemini-2.5-pro@thinking:2048@temperature:0.2", res.Model)
	require.Equal(t, "gemini-2.5-pro", res.UpstreamModel)

	// Case B: Gemini 3 effort modifier -> sends explicit thinkingLevel=low, strips @ from URL, ReasoningEffort is "low".
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3-pro@effort:low:generateContent", strings.NewReader(gemReq))
	res, err = gemSvc.ForwardNative(c.Request.Context(), c, gemAcct, "gemini-3-pro@effort:low", "generateContent", false, []byte(gemReq))
	require.NoError(t, err, rec.Body.String())
	require.NotNil(t, res)
	require.Len(t, gemURLs, 2)
	require.Equal(t, "/v1beta/models/gemini-3-pro:generateContent", gemURLs[1])
	require.Equal(t, "low", strings.ToLower(gjson.GetBytes(gemBodies[1], "generationConfig.thinkingConfig.thinkingLevel").String()))
	require.NotNil(t, res.ReasoningEffort)
	require.Equal(t, "low", *res.ReasoningEffort)

	// Case C: Invalid Gemini modifier returns Google 400 INVALID_ARGUMENT with zero upstream call.
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3-pro@effort:nope:generateContent", strings.NewReader(gemReq))
	_, err = gemSvc.ForwardNative(c.Request.Context(), c, gemAcct, "gemini-3-pro@effort:nope", "generateContent", false, []byte(gemReq))
	require.ErrorIs(t, err, modelmodifier.ErrInvalid)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "INVALID_ARGUMENT", gjson.GetBytes(rec.Body.Bytes(), "error.status").String())
	require.Len(t, gemURLs, 2)
}
