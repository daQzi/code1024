package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/relaymetrics"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEmbeddingsPerformanceRequiresOutput(t *testing.T) {
	for _, body := range []string{
		`{"data":[{"index":0,"embedding":[0.1,0.2]}]}`,
		`{"data":[{"index":0,"embedding":"zcxMPs3MzD4="}]}`,
	} {
		require.Equal(t, relaymetrics.Success, embeddingsPerformance([]byte(body)))
	}
	for _, body := range []string{`{}`, `{"usage":{"prompt_tokens":12}}`, `{"data":[]}`, `{"data":[{"embedding":[]}]}`, `{"data":[{"embedding":[ ]}]}`, `{"data":[{"embedding":[1]},{"embedding":""}]}`, `{"error":{"message":"failed"},"data":[{"embedding":[1]}]}`, `<html>maintenance</html>`} {
		require.Equal(t, relaymetrics.Failure, embeddingsPerformance([]byte(body)))
	}
}

func TestImageHTTPPerformanceRequiresDeliveredTerminalOutput(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		count      int
		want       relaymetrics.Outcome
	}{
		{"completed", "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"aW1hZ2U=\"}\n\n", 1, relaymetrics.Success},
		{"canonical done", "data: {\"data\":[{\"url\":\"https://media.invalid/image.png\"}]}\n\ndata: [DONE]\n\n", 1, relaymetrics.Success},
		{"missing done", "data: {\"data\":[{\"url\":\"https://media.invalid/image.png\"}]}\n\n", 1, relaymetrics.Failure},
		{"usage only", "data: {\"usage\":{\"images\":2}}\n\ndata: [DONE]\n\n", 0, relaymetrics.Failure},
		{"no result", "data: {\"type\":\"image_generation.completed\"}\n\n", 0, relaymetrics.Failure},
		{"error after output", "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"aW1hZ2U=\"}\n\ndata: {\"error\":{\"type\":\"server_error\"}}\n\ndata: [DONE]\n\n", 1, relaymetrics.Failure},
		{"business refusal", "data: {\"error\":{\"code\":\"content_policy_violation\"}}\n\ndata: [DONE]\n\n", 0, relaymetrics.Ignored},
		{"invalid JSON", "data: invalid\n\ndata: [DONE]\n\n", 0, relaymetrics.Failure},
		{"complete JSON fallback", `{"data":[{"b64_json":"aW1hZ2U="}]}`, 1, relaymetrics.Success},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			_, count, _, _, err := svc.handleOpenAIImagesStreamingResponse(resp, c, time.Now(), nil)
			require.NoError(t, err, "telemetry must preserve legacy forwarding/billing behavior")
			require.Equal(t, tc.count, count)
			require.Equal(t, tc.want, imageHTTPPerformance(c, count))
		})
	}
}

func TestVoiceHTTPPerformanceRequiresOutput(t *testing.T) {
	for _, tc := range []struct {
		mode, contentType, body string
		want                    relaymetrics.Outcome
	}{
		{"tts", "audio/mpeg", "ID3-audio", relaymetrics.Success},
		{"tts", "audio/wav; rate=24000", "RIFF-audio", relaymetrics.Success},
		{"tts", "application/octet-stream", "audio", relaymetrics.Success},
		{"stt", "application/json", `{"text":"hello"}`, relaymetrics.Success},
		{"stt", "application/json", `{"text":""}`, relaymetrics.Success},
		{"stt", "text/plain; charset=utf-8", "hello", relaymetrics.Success},
		{"tts", "audio/mpeg", "", relaymetrics.Failure},
		{"tts", "application/json", `{"error":{"message":"failed"}}`, relaymetrics.Failure},
		{"stt", "application/json", `{"duration":120}`, relaymetrics.Failure},
		{"stt", "application/json", `{"text":"partial","error":{"message":"failed"}}`, relaymetrics.Failure},
		{"stt", "text/html", "maintenance", relaymetrics.Failure},
	} {
		t.Run(tc.mode+"/"+tc.contentType+"/"+tc.body, func(t *testing.T) {
			require.Equal(t, tc.want, voiceHTTPPerformance(tc.mode, tc.contentType, []byte(tc.body)))
		})
	}
	// A fallback estimate for money must never create healthy image telemetry.
	require.Equal(t, relaymetrics.Failure, mediaCountPerformance(0))
	require.Equal(t, relaymetrics.Success, mediaCountPerformance(1))
}
