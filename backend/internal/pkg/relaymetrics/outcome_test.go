package relaymetrics

import (
	"fmt"
	"strings"
	"testing"
)

func TestResponsesTerminalOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       Outcome
	}{
		{"delta", `{"type":"response.output_text.delta","delta":"hello"}`, Unknown},
		{"invalid terminal JSON", `{"type":"response.completed","response":`, Unknown},
		{"completed", `{"type":"response.completed","response":{"status":"completed"}}`, Success},
		{"done", `{"type":"response.done","response":{}}`, Success},
		{"failed", `{"type":"response.failed","response":{"error":{"code":"server_error"}}}`, Failure},
		{"bare error", `{"type":"error","error":{"code":"rate_limit_exceeded"}}`, Failure},
		{"bad credentials override generic type", `{"type":"error","error":{"code":"invalid_api_key","type":"invalid_request_error"}}`, Failure},
		{"failed nested status", `{"type":"response.done","response":{"status":"failed","error":{"code":"server_error"}}}`, Failure},
		{"token limit", `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`, Success},
		{"legacy token limit", `{"type":"response.done","response":{"status":"incomplete","incomplete_details":{"reason":"max_tokens"}}}`, Success},
		{"unknown incomplete", `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"timeout"}}}`, Failure},
		{"content filter", `{"type":"response.incomplete","response":{"incomplete_details":{"reason":"content_filter"}}}`, Ignored},
		{"input error", `{"type":"response.failed","response":{"error":{"code":"context_length_exceeded"}}}`, Ignored},
		{"cancelled", `{"type":"response.cancelled"}`, Ignored},
		{"nested cancelled", `{"type":"response.done","response":{"status":"canceled"}}`, Ignored},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Terminal([]byte(tc.body)); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func BenchmarkResponsesTerminalOutcome(b *testing.B) {
	for _, size := range []int{64 << 10, 900 << 10} {
		body := []byte(`{"type":"response.completed","response":{"status":"completed","output":[{"text":"` + strings.Repeat("x", size) + `"}]}}`)
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				Terminal(body)
			}
		})
	}
}

func TestSpecificUpstreamFailurePrecedesGenericInputType(t *testing.T) {
	for _, code := range []string{"invalid_api_key", "api_key_service_blocked", "insufficient_user_quota", "pre_consume_token_quota_failed", "rate_limit_exceeded", "model_not_found"} {
		if IgnoreFailure(code, "invalid_request_error", 400) {
			t.Fatal("specific upstream failure ignored:", code)
		}
	}
}
