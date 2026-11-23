//go:build unit

package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRelayPerformanceActualGatewayRetriesProduceSingleOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, mode string
		success                bool
	}{
		{"responses", "/openai/v1/responses", `{"model":"grok","input":"hello","stream":false}`, "revoked", true},
		{"chat", "/openai/v1/chat/completions", `{"model":"grok","messages":[{"role":"user","content":"hello"}],"stream":false}`, "revoked", true},
		{"messages", "/openai/v1/messages", `{"model":"grok","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`, "revoked", true},
		{"capacity failure", "/openai/v1/responses", `{"model":"grok","input":"hello","stream":false}`, "all_429", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &relaySinkFixture{}
			_, _, _, router, cleanup := newGrokCredentialFailoverHandler(t, tc.mode, sink)
			defer cleanup()
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(tc.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Len(t, sink.samples, 1, recorder.Body.String())
			require.Equal(t, tc.success, sink.samples[0].Success)
			require.Equal(t, "grok", sink.samples[0].Platform)
			require.True(t, sink.samples[0].Valid())
		})
	}
}
