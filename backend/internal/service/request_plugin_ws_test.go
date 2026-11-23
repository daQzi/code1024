//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const wsMultiStagePluginSource = `export const meta = {
  apiVersion: 1,
  key: "rp-ws-e2e",
  name: "WS E2E",
  version: "1.0.0",
  author: {name: "Fixture"},
  allowedHosts: ["reports.example.com"],
};
export function ingress(ctx, req) {
  const input = String(req.body.input || "");
  if (input.includes("forbidden")) {
    return {action: "reject", code: "blocked.ws", message: "Blocked"};
  }
  if (!input.includes("secret")) {
    return {action: "continue"};
  }
  return {
    action: "continue",
    effects: [{type: "json.patch", ops: [{op: "replace", path: "/input", value: input.replaceAll("secret", "[redacted]")}]}],
  };
}
export function request(ctx, req) {
  if (String(req.body.input || "").includes("plan")) {
    return {
      action: "continue",
      effects: [{type: "reasoning.set", effort: "medium"}],
    };
  }
  return {action: "continue"};
}
export function outbound(ctx, req) {
  return {
    action: "continue",
    effects: [
      {type: "json.patch", ops: [{op: "add", path: "/metadata", value: {out_stage: "ok"}}]},
      {type: "header.patch", set: {"X-Plugin-WS-Bridge": "applied"}},
    ],
  };
}
export function egress(ctx, req) {
  if (String(req.body.input || "").includes("egress-block")) {
    return {action: "reject", code: "blocked.egress", message: "Blocked at egress"};
  }
  return {action: "continue"};
}
export function reply(ctx, res) {
  const mask = [];
  for (const output of res.outputs) {
    if (output.text.includes("stopword")) {
      return {action: "stop", code: "reply.blocked"};
    }
    for (let i = output.text.indexOf("bad"); i >= 0; i = output.text.indexOf("bad", i + 1)) {
      mask.push({index: output.index, start: i, end: i + 3, category: "word"});
    }
  }
  return {action: "continue", mask};
}
export function report(ctx, record) {
  utils.fetch({
    url: "https://reports.example.com/ws-callback",
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({ctx, record}),
  });
}`

// wsInteractiveUpstreamConn implements openAIWSClientConn backed by channels so
// tests can inspect every client frame (including response.cancel) and control
// streamed upstream events deterministically.
type wsInteractiveUpstreamConn struct {
	in      chan []byte
	out     chan []byte
	closed  chan struct{}
	once    sync.Once
	handler func(conn *wsInteractiveUpstreamConn)
}

func newWSInteractiveUpstreamConn(handler func(conn *wsInteractiveUpstreamConn)) *wsInteractiveUpstreamConn {
	c := &wsInteractiveUpstreamConn{
		in:      make(chan []byte, 32),
		out:     make(chan []byte, 64),
		closed:  make(chan struct{}),
		handler: handler,
	}
	go c.handler(c)
	return c
}

func (c *wsInteractiveUpstreamConn) WriteJSON(ctx context.Context, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	select {
	case <-c.closed:
		return errors.New("upstream ws closed")
	case <-ctx.Done():
		return ctx.Err()
	case c.in <- raw:
		return nil
	}
}

func (c *wsInteractiveUpstreamConn) ReadMessage(ctx context.Context) ([]byte, error) {
	select {
	case <-c.closed:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-c.out:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	}
}

func (c *wsInteractiveUpstreamConn) Ping(_ context.Context) error { return nil }

func (c *wsInteractiveUpstreamConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *wsInteractiveUpstreamConn) send(event string) bool {
	select {
	case <-c.closed:
		return false
	case c.out <- []byte(event):
		return true
	}
}

func (c *wsInteractiveUpstreamConn) recv() ([]byte, bool) {
	select {
	case <-c.closed:
		return nil, false
	case msg := <-c.in:
		return msg, true
	}
}

func readWSClientJSON(t *testing.T, conn *coderws.Conn) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	require.NoError(t, err)
	return raw
}

func writeWSClientJSON(t *testing.T, conn *coderws.Conn, payload string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(payload)))
}

func newWSPluginGatewayConfig(httpBridge bool) *config.Config {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.MaxLineSize = defaultMaxLineSize
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 2
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 5
	if httpBridge {
		cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
		cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
	}
	return cfg
}

func TestResponsesWebSocketRequestPluginsMultiTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)

	pluginSvc, repo := requestPluginFixture(t, wsMultiStagePluginSource)
	v := repo.versions[1]
	item := requestFixtureItem(1, "global", `{}`, v)
	item.Stages = []string{"ingress", "request", "outbound", "egress", "reply", "report"}
	item.Formats = []string{"openai_responses"}
	repo.items = []RequestPluginInstance{item}

	reports := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 16)}
	pluginSvc.gateway.httpUpstream = reports
	pluginSvc.taskReports = newTaskReportExecutor(pluginSvc)
	t.Cleanup(pluginSvc.taskReports.close)

	received := make(chan string, 16)
	const usageJSON = `"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}`
	upstreamConn := newWSInteractiveUpstreamConn(func(conn *wsInteractiveUpstreamConn) {
		for idx := 1; ; idx++ {
			create, ok := conn.recv()
			if !ok {
				return
			}
			received <- string(create)
			respID := fmt.Sprintf("resp_%d", idx)
			if !conn.send(`{"type":"response.created","response":{"id":"` + respID + `","status":"in_progress"}}`) {
				return
			}
			text := gjson.GetBytes(create, "input").Str
			switch {
			case strings.Contains(text, "[redacted]"):
				for _, delta := range []string{"a bad ", "word"} {
					if !conn.send(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"` + delta + `"}`) {
						return
					}
				}
				conn.send(`{"type":"response.completed","response":{"id":"` + respID + `","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"a bad word"}]}],` + usageJSON + `}}`)
			case strings.Contains(text, "stop"):
				if !conn.send(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"say stopword now"}`) {
					return
				}
				cancelFrame, ok := conn.recv()
				if !ok {
					return
				}
				received <- string(cancelFrame)
				conn.send(`{"type":"response.cancelled","response":{"id":"` + respID + `","status":"cancelled",` + usageJSON + `}}`)
			default:
				conn.send(`{"type":"response.completed","response":{"id":"` + respID + `","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],` + usageJSON + `}}`)
			}
		}
	})
	t.Cleanup(func() { _ = upstreamConn.Close() })

	cfg := newWSPluginGatewayConfig(false)
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSSingleConnDialer{conn: upstreamConn})

	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     reports,
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
		newAPIPlugins:    pluginSvc,
	}
	account := &Account{
		ID:          211,
		Name:        "openai-ws-plugins",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 2,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true},
	}
	groupID := int64(7)
	apiKey := &APIKey{ID: 41, UserID: 9, Name: "ws-key", GroupID: &groupID, User: &User{ID: 9, Username: "ws-user"}}

	type turnResult struct {
		turn   int
		result *OpenAIForwardResult
		err    error
	}
	turnResults := make(chan turnResult, 8)
	serverErrCh := make(chan error, 1)

	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			serverErrCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		req := r.Clone(r.Context())
		req.URL.Path = "/v1/responses"
		ginCtx.Request = req
		ginCtx.Set("api_key", apiKey)

		var firstMessage []byte
		var initialTurn *RequestPluginWSTurn
		for {
			readCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			_, raw, readErr := conn.Read(readCtx)
			cancel()
			if readErr != nil {
				serverErrCh <- readErr
				return
			}
			reqModel := strings.TrimSpace(gjson.GetBytes(raw, "model").String())
			patched, pluginTurn, rej := svc.PrepareResponsesWSTurnIngress(r.Context(), ginCtx, apiKey, raw, reqModel)
			if rej != nil {
				writeCtx, wCancel := context.WithTimeout(r.Context(), 5*time.Second)
				wErr := conn.Write(writeCtx, coderws.MessageText, rej.EventJSON())
				wCancel()
				if wErr != nil {
					serverErrCh <- wErr
					return
				}
				continue
			}
			firstMessage = patched
			initialTurn = pluginTurn
			break
		}

		hooks := &OpenAIWSIngressHooks{
			APIKey:            apiKey,
			InitialPluginTurn: initialTurn,
			AfterTurn: func(turn int, result *OpenAIForwardResult, turnErr error) {
				if result != nil && result.pluginReportBilling != nil {
					result.pluginReportBilling.complete(decimal.RequireFromString("0.0025"), true)
				}
				turnResults <- turnResult{turn: turn, result: result, err: turnErr}
			},
		}
		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, hooks)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 5*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	// Turn 1: rejected by early ingress -> 0 upstream frames, WS error event, connection stays open.
	writeWSClientJSON(t, clientConn, `{"type":"response.create","model":"gpt-5.4","input":"a forbidden question"}`)
	rejEvent := readWSClientJSON(t, clientConn)
	require.Equal(t, "error", gjson.GetBytes(rejEvent, "type").String())
	require.Equal(t, int64(http.StatusForbidden), gjson.GetBytes(rejEvent, "status").Int())
	require.Equal(t, "request_processing_rejected", gjson.GetBytes(rejEvent, "error.code").String())
	require.Empty(t, received, "rejected turn must never reach upstream")

	rejReport := awaitTaskReport(t, reports)
	require.Equal(t, int64(http.StatusForbidden), gjson.GetBytes(rejReport["record"], "response.status").Int())

	// Turn 2: ingress + request + outbound + egress patch the payload; reply masks deltas & completed; report gets confirmed cost & masked events.
	writeWSClientJSON(t, clientConn, `{"type":"response.create","model":"gpt-5.4","input":"my secret plan"}`)
	upstreamFrame1 := <-received
	require.Equal(t, "my [redacted] plan", gjson.Get(upstreamFrame1, "input").String())
	require.Equal(t, "medium", gjson.Get(upstreamFrame1, "reasoning.effort").String())
	require.Equal(t, "ok", gjson.Get(upstreamFrame1, "metadata.out_stage").String())

	require.Equal(t, "response.created", gjson.GetBytes(readWSClientJSON(t, clientConn), "type").String())
	delta1 := readWSClientJSON(t, clientConn)
	delta2 := readWSClientJSON(t, clientConn)
	require.Equal(t, "response.output_text.delta", gjson.GetBytes(delta1, "type").String())
	require.Equal(t, "a *** ", gjson.GetBytes(delta1, "delta").String())
	require.Equal(t, "word", gjson.GetBytes(delta2, "delta").String())
	completed := readWSClientJSON(t, clientConn)
	require.Equal(t, "response.completed", gjson.GetBytes(completed, "type").String())
	require.Equal(t, "a *** word", gjson.GetBytes(completed, "response.output.0.content.0.text").String())

	tr1 := <-turnResults
	require.NoError(t, tr1.err)
	require.NotNil(t, tr1.result)
	require.Equal(t, 10, tr1.result.Usage.InputTokens)
	require.Equal(t, 5, tr1.result.Usage.OutputTokens)

	rep1 := awaitTaskReport(t, reports)
	require.Equal(t, "0.0025", gjson.GetBytes(rep1["record"], "cost.usd").String())
	require.Equal(t, int64(10), gjson.GetBytes(rep1["record"], "usage.promptTokens").Int())
	require.Equal(t, int64(5), gjson.GetBytes(rep1["record"], "usage.completionTokens").Int())
	require.NotContains(t, string(rep1["record"]), "a bad", "report must only contain masked output")
	require.Contains(t, string(rep1["record"]), "a *** word")

	// Turn 3: reply returns action="stop" -> gateway sends response.cancel with response_id, sends WS error event with response_id, settles cancelled usage, and keeps connection open.
	writeWSClientJSON(t, clientConn, `{"type":"response.create","model":"gpt-5.4","input":"please stop"}`)
	_ = <-received // response.create
	require.Equal(t, "response.created", gjson.GetBytes(readWSClientJSON(t, clientConn), "type").String())
	cancelMsg := <-received
	require.Equal(t, "response.cancel", gjson.Get(cancelMsg, "type").String())
	require.Equal(t, "resp_2", gjson.Get(cancelMsg, "response_id").String())

	stopErrEvent := readWSClientJSON(t, clientConn)
	require.Equal(t, "error", gjson.GetBytes(stopErrEvent, "type").String())
	require.Equal(t, int64(http.StatusForbidden), gjson.GetBytes(stopErrEvent, "status").Int())
	require.Equal(t, "resp_2", gjson.GetBytes(stopErrEvent, "response_id").String())
	require.Equal(t, "response_processing_rejected", gjson.GetBytes(stopErrEvent, "error.code").String())

	tr2 := <-turnResults
	require.NoError(t, tr2.err)
	require.NotNil(t, tr2.result)
	require.Equal(t, "response.cancelled", tr2.result.UpstreamTerminalEvent)
	require.Equal(t, 10, tr2.result.Usage.InputTokens)
	require.Equal(t, 5, tr2.result.Usage.OutputTokens)

	rep2 := awaitTaskReport(t, reports)
	require.Equal(t, "0.0025", gjson.GetBytes(rep2["record"], "cost.usd").String())
	require.NotContains(t, string(rep2["record"]), "stopword")

	// Turn 4: same WebSocket continues cleanly after both ingress reject and reply stop.
	writeWSClientJSON(t, clientConn, `{"type":"response.create","model":"gpt-5.4","input":"hello"}`)
	_ = <-received
	require.Equal(t, "response.created", gjson.GetBytes(readWSClientJSON(t, clientConn), "type").String())
	done4 := readWSClientJSON(t, clientConn)
	require.Equal(t, "response.completed", gjson.GetBytes(done4, "type").String())
	require.Equal(t, "hello", gjson.GetBytes(done4, "response.output.0.content.0.text").String())

	tr3 := <-turnResults
	require.NoError(t, tr3.err)
	_ = awaitTaskReport(t, reports)

	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))
	require.NoError(t, <-serverErrCh)
}

func TestResponsesWebSocketModelModifiersPerTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)

	received := make(chan string, 8)
	upstreamConn := newWSInteractiveUpstreamConn(func(conn *wsInteractiveUpstreamConn) {
		for idx := 1; ; idx++ {
			create, ok := conn.recv()
			if !ok {
				return
			}
			received <- string(create)
			respID := fmt.Sprintf("resp_mod_%d", idx)
			model := gjson.GetBytes(create, "model").String()
			conn.send(`{"type":"response.created","response":{"id":"` + respID + `","model":"` + model + `","status":"in_progress"}}`)
			conn.send(`{"type":"response.completed","response":{"id":"` + respID + `","model":"` + model + `","status":"completed","usage":{"input_tokens":4,"output_tokens":2}}}`)
		}
	})
	t.Cleanup(func() { _ = upstreamConn.Close() })

	cfg := newWSPluginGatewayConfig(false)
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSSingleConnDialer{conn: upstreamConn})

	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
	}
	account := &Account{
		ID:          212,
		Name:        "openai-ws-modifiers",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 2,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true},
	}

	results := make(chan *OpenAIForwardResult, 4)
	serverErrCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			serverErrCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		ginCtx.Request = r.Clone(r.Context())

		readCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		_, firstMessage, readErr := conn.Read(readCtx)
		cancel()
		if readErr != nil {
			serverErrCh <- readErr
			return
		}
		hooks := &OpenAIWSIngressHooks{
			AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) {
				if result != nil {
					results <- result
				}
			},
		}
		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, hooks)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 5*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	// Turn 1: valid @effort:high strips modifier before upstream and sets reasoning.effort=high.
	writeWSClientJSON(t, clientConn, `{"type":"response.create","model":"gpt-5.4@effort:high","input":"hi"}`)
	frame1 := <-received
	require.Equal(t, "gpt-5.4", gjson.Get(frame1, "model").String())
	require.Equal(t, "high", gjson.Get(frame1, "reasoning.effort").String())
	require.Equal(t, "response.created", gjson.GetBytes(readWSClientJSON(t, clientConn), "type").String())
	require.Equal(t, "response.completed", gjson.GetBytes(readWSClientJSON(t, clientConn), "type").String())
	res1 := <-results
	require.Equal(t, "gpt-5.4@effort:high", res1.Model)
	require.Equal(t, "gpt-5.4", res1.UpstreamModel)
	require.NotNil(t, res1.ReasoningEffort)
	require.Equal(t, "high", *res1.ReasoningEffort)

	// Turn 2: invalid @bogus:1 emits WS 400 error without touching upstream or closing the socket.
	writeWSClientJSON(t, clientConn, `{"type":"response.create","model":"gpt-5.4@bogus:1","input":"hi"}`)
	errEvent := readWSClientJSON(t, clientConn)
	require.Equal(t, "error", gjson.GetBytes(errEvent, "type").String())
	require.Equal(t, int64(http.StatusBadRequest), gjson.GetBytes(errEvent, "status").Int())
	require.Equal(t, "invalid_model_modifier", gjson.GetBytes(errEvent, "error.code").String())
	require.Empty(t, received)

	// Turn 3: valid @effort:low succeeds on the same connection.
	writeWSClientJSON(t, clientConn, `{"type":"response.create","model":"gpt-5.4@effort:low","input":"next"}`)
	frame3 := <-received
	require.Equal(t, "gpt-5.4", gjson.Get(frame3, "model").String())
	require.Equal(t, "low", gjson.Get(frame3, "reasoning.effort").String())
	require.Equal(t, "response.created", gjson.GetBytes(readWSClientJSON(t, clientConn), "type").String())
	require.Equal(t, "response.completed", gjson.GetBytes(readWSClientJSON(t, clientConn), "type").String())
	res3 := <-results
	require.Equal(t, "gpt-5.4@effort:low", res3.Model)
	require.Equal(t, "gpt-5.4", res3.UpstreamModel)
	require.NotNil(t, res3.ReasoningEffort)
	require.Equal(t, "low", *res3.ReasoningEffort)

	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))
	require.NoError(t, <-serverErrCh)
}

type wsBridgePluginUpstream struct {
	reports   *taskReportHTTP
	turnCalls atomic.Int32
	abortedCh chan struct{}
	headersCh chan http.Header
	bodiesCh  chan string
}

func (u *wsBridgePluginUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

func (u *wsBridgePluginUpstream) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	if req.URL.Hostname() == "reports.example.com" {
		return u.reports.Do(req, proxy, accountID, concurrency)
	}
	bodyBytes, _ := io.ReadAll(req.Body)
	u.headersCh <- req.Header.Clone()
	u.bodiesCh <- string(bodyBytes)
	call := u.turnCalls.Add(1)
	switch call {
	case 1:
		sse := strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_bridge_1","status":"in_progress"}}`,
			``,
			`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"a bad "}`,
			``,
			`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"word"}`,
			``,
			`data: {"type":"response.completed","response":{"id":"resp_bridge_1","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"a bad word"}]}],"usage":{"input_tokens":8,"output_tokens":4}}}`,
			``,
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(bytes.NewBufferString(sse)),
		}, nil
	case 2:
		pr, pw := io.Pipe()
		go func() {
			defer func() { _ = pw.Close() }()
			_, _ = io.WriteString(pw, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_bridge_2\",\"status\":\"in_progress\"}}\n\n")
			_, _ = io.WriteString(pw, "data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"say stopword now\"}\n\n")
			<-req.Context().Done()
			select {
			case u.abortedCh <- struct{}{}:
			default:
			}
		}()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       pr,
		}, nil
	default:
		sse := strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_bridge_3","status":"in_progress"}}`,
			``,
			`data: {"type":"response.completed","response":{"id":"resp_bridge_3","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"after stop"}]}],"usage":{"input_tokens":3,"output_tokens":2}}}`,
			``,
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(bytes.NewBufferString(sse)),
		}, nil
	}
}

func TestResponsesWebSocketHTTPBridgeRequestPlugins(t *testing.T) {
	gin.SetMode(gin.TestMode)

	pluginSvc, repo := requestPluginFixture(t, wsMultiStagePluginSource)
	v := repo.versions[1]
	item := requestFixtureItem(1, "global", `{}`, v)
	item.Stages = []string{"ingress", "request", "outbound", "egress", "reply", "report"}
	item.Formats = []string{"openai_responses"}
	repo.items = []RequestPluginInstance{item}

	reports := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 16)}
	upstream := &wsBridgePluginUpstream{
		reports:   reports,
		abortedCh: make(chan struct{}, 1),
		headersCh: make(chan http.Header, 8),
		bodiesCh:  make(chan string, 8),
	}
	pluginSvc.gateway.httpUpstream = upstream
	pluginSvc.taskReports = newTaskReportExecutor(pluginSvc)
	t.Cleanup(pluginSvc.taskReports.close)

	cfg := newWSPluginGatewayConfig(true)
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     upstream,
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		newAPIPlugins:    pluginSvc,
	}
	account := &Account{
		ID:          213,
		Name:        "openai-ws-bridge-plugins",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 2,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true},
	}
	groupID := int64(7)
	apiKey := &APIKey{ID: 42, UserID: 10, Name: "bridge-key", GroupID: &groupID, User: &User{ID: 10, Username: "bridge-user"}}

	serverErrCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			serverErrCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		req := r.Clone(r.Context())
		req.URL.Path = "/v1/responses"
		ginCtx.Request = req
		ginCtx.Set("api_key", apiKey)

		readCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		_, firstMessage, readErr := conn.Read(readCtx)
		cancel()
		if readErr != nil {
			serverErrCh <- readErr
			return
		}
		hooks := &OpenAIWSIngressHooks{
			APIKey: apiKey,
			AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) {
				if result != nil && result.pluginReportBilling != nil {
					result.pluginReportBilling.complete(decimal.RequireFromString("0.0015"), true)
				}
			},
		}
		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, hooks)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 5*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	// Turn 1: HTTP bridge receives outbound header.patch and patched body; reply masks output; report receives confirmed cost & masked events.
	writeWSClientJSON(t, clientConn, `{"type":"response.create","model":"gpt-5.4","input":"my secret bridge"}`)
	hdr1 := <-upstream.headersCh
	body1 := <-upstream.bodiesCh
	require.Equal(t, "applied", hdr1.Get("X-Plugin-WS-Bridge"))
	require.Equal(t, "my [redacted] bridge", gjson.Get(body1, "input").String())
	require.Equal(t, "ok", gjson.Get(body1, "metadata.out_stage").String())

	require.Equal(t, "response.created", gjson.GetBytes(readWSClientJSON(t, clientConn), "type").String())
	require.Equal(t, "a *** ", gjson.GetBytes(readWSClientJSON(t, clientConn), "delta").String())
	require.Equal(t, "word", gjson.GetBytes(readWSClientJSON(t, clientConn), "delta").String())
	comp1 := readWSClientJSON(t, clientConn)
	require.Equal(t, "response.completed", gjson.GetBytes(comp1, "type").String())
	require.Equal(t, "a *** word", gjson.GetBytes(comp1, "response.output.0.content.0.text").String())

	rep1 := awaitTaskReport(t, reports)
	require.Equal(t, "0.0015", gjson.GetBytes(rep1["record"], "cost.usd").String())
	require.NotContains(t, string(rep1["record"]), "a bad")

	// Turn 2: reply stop immediately cancels upstream HTTP stream and emits WS error without closing the socket.
	writeWSClientJSON(t, clientConn, `{"type":"response.create","model":"gpt-5.4","input":"stop bridge"}`)
	_ = <-upstream.headersCh
	_ = <-upstream.bodiesCh
	require.Equal(t, "response.created", gjson.GetBytes(readWSClientJSON(t, clientConn), "type").String())
	stopEv := readWSClientJSON(t, clientConn)
	require.Equal(t, "error", gjson.GetBytes(stopEv, "type").String())
	require.Equal(t, "resp_bridge_2", gjson.GetBytes(stopEv, "response_id").String())
	require.Equal(t, "response_processing_rejected", gjson.GetBytes(stopEv, "error.code").String())
	select {
	case <-upstream.abortedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("expected HTTP bridge upstream stream to be cancelled on reply stop")
	}
	_ = awaitTaskReport(t, reports)

	// Turn 3: connection remains alive after HTTP bridge reply stop.
	writeWSClientJSON(t, clientConn, `{"type":"response.create","model":"gpt-5.4","input":"still alive"}`)
	_ = <-upstream.headersCh
	_ = <-upstream.bodiesCh
	require.Equal(t, "response.created", gjson.GetBytes(readWSClientJSON(t, clientConn), "type").String())
	comp3 := readWSClientJSON(t, clientConn)
	require.Equal(t, "response.completed", gjson.GetBytes(comp3, "type").String())
	require.Equal(t, "after stop", gjson.GetBytes(comp3, "response.output.0.content.0.text").String())
	_ = awaitTaskReport(t, reports)

	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))
	require.NoError(t, <-serverErrCh)
}
