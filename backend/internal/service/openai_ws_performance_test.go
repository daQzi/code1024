package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/relaymetrics"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSPerformanceDistinguishesUpstreamAndClientClose(t *testing.T) {
	closed := coderws.CloseError{Code: coderws.StatusNormalClosure}
	require.True(t, OpenAIWSUpstreamTurnFailure(wrapOpenAIWSIngressTurnError("read_upstream", closed, true)))
	require.False(t, OpenAIWSUpstreamTurnFailure(wrapOpenAIWSIngressTurnError("write_client", closed, true)))
	require.False(t, OpenAIWSUpstreamTurnFailure(closed))
}

func TestForwardOpenAIWSV2CarriesTerminalPerformance(t *testing.T) {
	for _, tc := range []struct {
		name, event string
		want        relaymetrics.Outcome
	}{
		{"complete", `{"type":"response.completed","response":{"id":"resp_metric","status":"completed","output":[]}}`, relaymetrics.Success},
		{"upstream failure", `{"type":"response.failed","response":{"id":"resp_metric","status":"failed","error":{"code":"server_error"}}}`, relaymetrics.Failure},
		{"token limit", `{"type":"response.incomplete","response":{"id":"resp_metric","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`, relaymetrics.Success},
		{"safety", `{"type":"response.incomplete","response":{"id":"resp_metric","status":"incomplete","incomplete_details":{"reason":"safety"}}}`, relaymetrics.Ignored},
		{"cancelled", `{"type":"response.cancelled","response":{"id":"resp_metric","status":"cancelled"}}`, relaymetrics.Ignored},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newOpenAIWSV2TestConfig()
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			pool := newOpenAIWSConnPool(cfg)
			t.Cleanup(pool.Close)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: &openAIWSCaptureConn{events: [][]byte{[]byte(tc.event)}}})
			s := &OpenAIGatewayService{cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{}, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
			account := &Account{ID: 9987, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "fixture-key"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			result, err := s.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.5","stream":false,"input":"hi"}`))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.OpenAIWSMode)
			require.Equal(t, tc.want, result.PerformanceOutcome)
		})
	}
	// Classification adds no new success fallback for transport errors.
	require.False(t, OpenAIWSUpstreamTurnFailure(errors.New("no stage evidence")))
}
