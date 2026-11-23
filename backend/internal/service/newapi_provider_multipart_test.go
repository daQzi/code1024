package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/stretchr/testify/require"
)

const providerAudioFixtureSource = `export const meta={apiVersion:1,key:"audio-fixture",name:"Audio provider",version:"1.0.0",author:{name:"Fixture"},models:["vendor-text"],
relay:[{incoming_path:"/v1/audio/speech",upstream_path:"/vendor/speech",models:["vendor-text"]},
{incoming_path:"/v1/audio/transcriptions",upstream_path:"/vendor/transcribe",models:["vendor-text"],converter:"plugin"}]};
export function convertRequest(ctx,req){
 if(req.form.fields.model!=="vendor-text" || req.form.files.length!==1)throw Error("wrong request form");
 return {file:"file",nativeResponse:true};
}
export function convertResponse(ctx,res){return {body:res.body};}
export function convertStreamEvent(){return {};}
export function signRequest(ctx,req){return {headers:{"X-Body-Digest":req.bodySha256}};}`

func TestNewAPIProviderAudioForwardsAndPinsPrice(t *testing.T) {
	for _, path := range []string{newAPIProviderAudioSpeech, newAPIProviderAudioTranscriptions} {
		t.Run(path, func(t *testing.T) {
			s, account, upstream := providerFixture(t, providerAudioFixtureSource)
			s.gateway.resolver = NewModelPricingResolver(nil, &BillingService{})
			unitPrice := 0.06
			group := &Group{ID: 7}
			key := &APIKey{GroupID: &group.ID, Group: group}
			body := []byte(`{"model":"public-text","input":"hello"}`)
			wantWire := []byte(`{"model":"vendor-text","input":"hello"}`)
			contentType := "application/json"
			upstream.contentType, upstream.response = "audio/mpeg", "fixture-audio"
			if path == newAPIProviderAudioTranscriptions {
				var buffer bytes.Buffer
				form := multipart.NewWriter(&buffer)
				require.NoError(t, form.WriteField("model", "public-text"))
				file, err := form.CreateFormFile("file", "speech.mp3")
				require.NoError(t, err)
				_, err = file.Write([]byte("fixture-upload"))
				require.NoError(t, err)
				require.NoError(t, form.Close())
				body, contentType = buffer.Bytes(), form.FormDataContentType()
				wantWire = []byte("fixture-upload")
				upstream.contentType, upstream.response = "application/json", `{"text":"transcript"}`
			}
			call := func() (*OpenAIForwardResult, int, error) {
				c, recorder := providerContext(path, string(body))
				c.Set("api_key", key)
				result, err := s.gateway.ForwardNewAPIProviderMultipart(c.Request.Context(), c, account, body, contentType, path, "public-text", "")
				return result, recorder.Code, err
			}
			require.Equal(t, "public-text", ExtractNewAPIProviderMultipartModel(contentType, body))
			require.True(t, account.SupportsOpenAIEndpointCapability(NewAPIProviderAudioCapability(path)))
			_, status, err := call()
			require.ErrorIs(t, err, ErrModelPricingUnavailable)
			require.Equal(t, http.StatusBadRequest, status)
			require.Empty(t, upstream.requests)
			group.ModelPricing = []ChannelModelPricing{{Models: []string{"public-text"}, BillingMode: BillingModePerRequest, PerRequestPrice: &unitPrice}}
			result, status, err := call()
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status)
			require.Len(t, upstream.requests, 1)
			req := upstream.requests[0]
			out, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.Equal(t, wantWire, out)
			digest := sha256.Sum256(out)
			require.Equal(t, hex.EncodeToString(digest[:]), req.Header.Get("X-Body-Digest"))
			unitPrice = 10 // A later edit cannot change the admitted request's price.
			cost, err := s.gateway.calculateOpenAIRecordUsageCost(context.Background(), result, key, nil, 4, 4, 4, 2, UsageTokens{}, "", nil, false, time.Time{})
			require.NoError(t, err)
			require.InDelta(t, 0.12, cost.ActualCost, 1e-8)
			require.Equal(t, string(BillingModePerRequest), cost.BillingMode)
			upstream.contentType, upstream.response = "text/html", "<html>failed</html>"
			result, status, err = call()
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, http.StatusBadGateway, status)
		})
	}
}

func buildAudioMultipartBody(t *testing.T, model string) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	form := multipart.NewWriter(&buffer)
	require.NoError(t, form.WriteField("model", model))
	file, err := form.CreateFormFile("file", "speech.mp3")
	require.NoError(t, err)
	_, err = file.Write([]byte("fixture-upload"))
	require.NoError(t, err)
	require.NoError(t, form.Close())
	return buffer.Bytes(), form.FormDataContentType()
}

func TestNewAPIProviderAudioUsageMeteringAndFailClosed(t *testing.T) {
	t.Run("tts per million characters metering and fail-closed on missing input", func(t *testing.T) {
		s, account, upstream := providerFixture(t, providerAudioFixtureSource)
		s.gateway.resolver = NewModelPricingResolver(nil, &BillingService{})
		ttsRate := 15.0 // $15 per 1M characters
		group := &Group{ID: 8, AudioTTSPricePerMillionChars: &ttsRate}
		key := &APIKey{GroupID: &group.ID, Group: group}
		upstream.contentType, upstream.response = "audio/mpeg", "fixture-audio"

		// Missing, empty, or non-string input fails closed with zero upstream calls.
		for _, badBody := range []string{
			`{"model":"public-text"}`,
			`{"model":"public-text","input":""}`,
			`{"model":"public-text","input":123}`,
		} {
			c, recorder := providerContext(newAPIProviderAudioSpeech, badBody)
			c.Set("api_key", key)
			result, err := s.gateway.ForwardNewAPIProviderMultipart(c.Request.Context(), c, account, []byte(badBody), "application/json", newAPIProviderAudioSpeech, "public-text", "")
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		}
		require.Empty(t, upstream.requests)

		// UTF-8 rune counting: "你好hello" = 7 runes.
		body := `{"model":"public-text","input":"你好hello"}`
		c, recorder := providerContext(newAPIProviderAudioSpeech, body)
		c.Set("api_key", key)
		result, err := s.gateway.ForwardNewAPIProviderMultipart(c.Request.Context(), c, account, []byte(body), "application/json", newAPIProviderAudioSpeech, "public-text", "")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.NotNil(t, result.AudioUsage)
		require.Equal(t, "tts", result.AudioUsage.Mode)
		require.InDelta(t, 7.0/1_000_000.0, result.AudioUsage.DurationOrUnits, 1e-12)
		cost, err := s.gateway.calculateOpenAIRecordUsageCost(context.Background(), result, key, nil, 1, 1, 1, 2, UsageTokens{}, "", nil, false, time.Time{})
		require.NoError(t, err)
		// Base = 15 * 7 / 1e6 = 0.000105; with multiplier 2 => 0.00021
		require.InDelta(t, 0.000105, cost.TotalCost, 1e-10)
		require.InDelta(t, 0.00021, cost.ActualCost, 1e-10)

		// Explicit zero TTS rate is permitted.
		zero := 0.0
		group.AudioTTSPricePerMillionChars = &zero
		c, recorder = providerContext(newAPIProviderAudioSpeech, body)
		c.Set("api_key", key)
		result, err = s.gateway.ForwardNewAPIProviderMultipart(c.Request.Context(), c, account, []byte(body), "application/json", newAPIProviderAudioSpeech, "public-text", "")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, recorder.Code)
		cost, err = s.gateway.calculateOpenAIRecordUsageCost(context.Background(), result, key, nil, 1, 1, 1, 2, UsageTokens{}, "", nil, false, time.Time{})
		require.NoError(t, err)
		require.Zero(t, cost.ActualCost)

		// Negative rate fails closed before upstream.
		neg := -1.0
		group.AudioTTSPricePerMillionChars = &neg
		beforeCount := len(upstream.requests)
		c, recorder = providerContext(newAPIProviderAudioSpeech, body)
		c.Set("api_key", key)
		result, err = s.gateway.ForwardNewAPIProviderMultipart(c.Request.Context(), c, account, []byte(body), "application/json", newAPIProviderAudioSpeech, "public-text", "")
		require.ErrorIs(t, err, ErrModelPricingUnavailable)
		require.Nil(t, result)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Len(t, upstream.requests, beforeCount)
	})

	t.Run("stt per hour duration metering and fail-closed on missing or invalid duration", func(t *testing.T) {
		s, account, upstream := providerFixture(t, providerAudioFixtureSource)
		s.gateway.resolver = NewModelPricingResolver(nil, &BillingService{})
		sttRate := 0.36 // $0.36 per hour ($0.0001/sec)
		group := &Group{ID: 9, AudioSTTPricePerHour: &sttRate}
		key := &APIKey{GroupID: &group.ID, Group: group}
		body, ct := buildAudioMultipartBody(t, "public-text")

		// Case 1: usage.seconds
		upstream.contentType, upstream.response = "application/json; charset=utf-8", `{"text":"hello","usage":{"type":"duration","seconds":30}}`
		c, recorder := providerContext(newAPIProviderAudioTranscriptions, string(body))
		c.Set("api_key", key)
		result, err := s.gateway.ForwardNewAPIProviderMultipart(c.Request.Context(), c, account, body, ct, newAPIProviderAudioTranscriptions, "public-text", "")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.NotNil(t, result.AudioUsage)
		require.Equal(t, "stt", result.AudioUsage.Mode)
		require.InDelta(t, 30.0/3600.0, result.AudioUsage.DurationOrUnits, 1e-12)
		cost, err := s.gateway.calculateOpenAIRecordUsageCost(context.Background(), result, key, nil, 1, 1, 1, 2, UsageTokens{}, "", nil, false, time.Time{})
		require.NoError(t, err)
		require.InDelta(t, 0.003, cost.TotalCost, 1e-9)
		require.InDelta(t, 0.006, cost.ActualCost, 1e-9)

		// Case 2: top-level verbose_json duration
		upstream.contentType, upstream.response = "application/json", `{"text":"hello","duration":120}`
		c, recorder = providerContext(newAPIProviderAudioTranscriptions, string(body))
		c.Set("api_key", key)
		result, err = s.gateway.ForwardNewAPIProviderMultipart(c.Request.Context(), c, account, body, ct, newAPIProviderAudioTranscriptions, "public-text", "")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, recorder.Code)
		cost, err = s.gateway.calculateOpenAIRecordUsageCost(context.Background(), result, key, nil, 1, 1, 1, 1, UsageTokens{}, "", nil, false, time.Time{})
		require.NoError(t, err)
		require.InDelta(t, 0.012, cost.ActualCost, 1e-9)

		// Fail-closed cases: non-JSON, missing duration, zero/negative, non-number, excessive, invalid explicit usage.seconds.
		for _, tc := range []struct {
			name, respCT, respBody string
		}{
			{"plain text", "text/plain", "just text"},
			{"missing duration", "application/json", `{"text":"no duration"}`},
			{"zero seconds", "application/json", `{"text":"zero","usage":{"seconds":0}}`},
			{"negative duration", "application/json", `{"text":"neg","duration":-2}`},
			{"string duration", "application/json", `{"text":"str","duration":"10"}`},
			{"excessive duration", "application/json", `{"text":"huge","duration":90000}`},
			{"null usage.seconds does not fallback to duration", "application/json", `{"text":"bad","usage":{"seconds":null},"duration":10}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				upstream.contentType, upstream.response = tc.respCT, tc.respBody
				c, recorder := providerContext(newAPIProviderAudioTranscriptions, string(body))
				c.Set("api_key", key)
				result, err := s.gateway.ForwardNewAPIProviderMultipart(c.Request.Context(), c, account, body, ct, newAPIProviderAudioTranscriptions, "public-text", "")
				require.Error(t, err)
				require.Nil(t, result)
				require.Equal(t, http.StatusBadGateway, recorder.Code)
			})
		}

		// Flat per-request channel price takes precedence over group STT duration pricing (allows plain text).
		flat := 0.05
		group.ModelPricing = []ChannelModelPricing{{Models: []string{"public-text"}, BillingMode: BillingModePerRequest, PerRequestPrice: &flat}}
		upstream.contentType, upstream.response = "text/plain", "plain transcript"
		c, recorder = providerContext(newAPIProviderAudioTranscriptions, string(body))
		c.Set("api_key", key)
		result, err = s.gateway.ForwardNewAPIProviderMultipart(c.Request.Context(), c, account, body, ct, newAPIProviderAudioTranscriptions, "public-text", "")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, recorder.Code)
		cost, err = s.gateway.calculateOpenAIRecordUsageCost(context.Background(), result, key, nil, 1, 1, 1, 1, UsageTokens{}, "", nil, false, time.Time{})
		require.NoError(t, err)
		require.InDelta(t, 0.05, cost.ActualCost, 1e-9)
	})
}

func TestNewAPIProviderMultipartHardeningR4(t *testing.T) {
	t.Run("part count limit enforced fail-closed", func(t *testing.T) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for i := 0; i <= newAPIProviderMultipartMaxParts; i++ {
			require.NoError(t, w.WriteField("field", "v"))
		}
		require.NoError(t, w.Close())
		_, err := parseNewAPIProviderMultipart(w.FormDataContentType(), buf.Bytes())
		require.Error(t, err)
		require.Contains(t, err.Error(), "exceeds part limit")
	})

	t.Run("shared memory budget rejects oversized multipart copy with 503", func(t *testing.T) {
		s, account, upstream := providerFixture(t, providerAudioFixtureSource)
		s.gateway.resolver = NewModelPricingResolver(nil, &BillingService{})
		flat := 0.01
		group := &Group{ID: 71, ModelPricing: []ChannelModelPricing{{Models: []string{"public-text"}, BillingMode: BillingModePerRequest, PerRequestPrice: &flat}}}
		key := &APIKey{ID: 7, UserID: 7, GroupID: &group.ID, Group: group}
		upstream.contentType, upstream.response = "application/json", `{"text":"ok","duration":1}`

		var buf bytes.Buffer
		form := multipart.NewWriter(&buf)
		require.NoError(t, form.WriteField("model", "public-text"))
		part, err := form.CreateFormFile("file", "clip.mp3")
		require.NoError(t, err)
		_, err = part.Write(bytes.Repeat([]byte("a"), 2048))
		require.NoError(t, err)
		require.NoError(t, form.Close())
		body, ct := buf.Bytes(), form.FormDataContentType()

		c, recorder := providerContext(newAPIProviderAudioTranscriptions, string(body))
		c.Set("api_key", key)
		budgetCtx, release := pkghttputil.WithRequestBodyMemoryBudget(c.Request.Context(), pkghttputil.NewBodyMemoryBudget(512))
		defer release()
		c.Request = c.Request.WithContext(budgetCtx)

		result, err := s.gateway.ForwardNewAPIProviderMultipart(budgetCtx, c, account, body, ct, newAPIProviderAudioTranscriptions, "public-text", "")
		require.Error(t, err)
		require.Nil(t, result)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Equal(t, "1", recorder.Header().Get("Retry-After"))
		require.Empty(t, upstream.requests)
	})

	t.Run("binary audio responses carry sandbox CSP and nosniff headers", func(t *testing.T) {
		s, account, upstream := providerFixture(t, providerAudioFixtureSource)
		s.gateway.resolver = NewModelPricingResolver(nil, &BillingService{})
		flat := 0.01
		group := &Group{ID: 72, ModelPricing: []ChannelModelPricing{{Models: []string{"public-text"}, BillingMode: BillingModePerRequest, PerRequestPrice: &flat}}}
		key := &APIKey{ID: 8, UserID: 8, GroupID: &group.ID, Group: group}

		upstream.contentType, upstream.response = "audio/mpeg; charset=binary", "ID3binaryaudio"
		ttsBody := []byte(`{"model":"public-text","input":"hi"}`)
		c, recorder := providerContext(newAPIProviderAudioSpeech, string(ttsBody))
		c.Set("api_key", key)
		res, err := s.gateway.ForwardNewAPIProviderMultipart(c.Request.Context(), c, account, ttsBody, "application/json", newAPIProviderAudioSpeech, "public-text", "")
		require.NoError(t, err)
		require.NotNil(t, res)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
		require.Equal(t, "no-referrer", recorder.Header().Get("Referrer-Policy"))
		require.Equal(t, "sandbox", recorder.Header().Get("Content-Security-Policy"))

		upstream.contentType, upstream.response = "application/json", `{"text":"hello","duration":2}`
		sttBody, sttCT := buildAudioMultipartBody(t, "public-text")
		c2, recorder2 := providerContext(newAPIProviderAudioTranscriptions, string(sttBody))
		c2.Set("api_key", key)
		res2, err := s.gateway.ForwardNewAPIProviderMultipart(c2.Request.Context(), c2, account, sttBody, sttCT, newAPIProviderAudioTranscriptions, "public-text", "")
		require.NoError(t, err)
		require.NotNil(t, res2)
		require.Equal(t, http.StatusOK, recorder2.Code)
		require.Equal(t, "nosniff", recorder2.Header().Get("X-Content-Type-Options"))
		require.Equal(t, "no-referrer", recorder2.Header().Get("Referrer-Policy"))
		require.Empty(t, recorder2.Header().Get("Content-Security-Policy"))
	})
}
