package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const providerSocketFixtureSource = `export const meta={apiVersion:1,key:"socket-fixture",name:"Socket fixture",version:"1.0.0",author:{name:"Fixture"},models:["vendor-text"],relay:[{incoming_path:"/v1/chat/completions",upstream_path:"/unused",converter:"plugin"}]};
export function convertRequest(ctx, req){return {websocket:{path:"/socket",protocols:["fixture"],messages:["start"]},state:{stream:req.stream}};}
export function convertResponse(ctx, res){return {body:res.body};}
export function signRequest(ctx, req){return {headers:{"X-Handshake-Method":req.method}};}
export function convertStreamEvent(ctx, ev){
 if(ev.done)return {};
 if(ev.dataBase64==="AQID")return {send:[{bodyBase64:"BAU="}]};
 const result=JSON.parse(ev.data);
 if(!ctx.state.stream)return {body:result};
 return {events:[{data:result},{data:"[DONE]"}],close:true};
}`

const providerResponsesWSFixtureSource = `export const meta={apiVersion:1,key:"responses-ws-fixture",name:"Responses WS fixture",version:"1.0.0",author:{name:"Fixture"},models:["vendor-text"],
relay:[{incoming_path:"/v1/responses",upstream_path:"/ws/responses/{model}",models:["vendor-text"],auth:{type:"header",name:"X-Vendor-Auth",value:"{api_key}"}}]};
export function exchangeKey(ctx){
 const res=utils.fetch({url:ctx.baseUrl+"/token",method:"POST",headers:{"Authorization":"Bearer "+ctx.apiKey}});
 return res.body;
}
export function credentialRejected(ctx, res){
 return res.status===419 && JSON.parse(res.body).error.code==="token_expired";
}
export function signRequest(ctx, req){
 return {headers:{"X-Signed-Method":req.method,"X-Signed-Token":String(ctx.token&&ctx.token.value||"")},query:{signed:"1"}};
}`

// pipeListener lets tests run real HTTP/1.1 + coder/websocket handshakes
// completely in-memory via net.Pipe() without binding TCP loopback ports.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn, 16), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80} }

func (l *pipeListener) DialContext(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()
		return nil, ctx.Err()
	}
}

func startPipeHTTPServer(t *testing.T, handler http.Handler) *http.Client {
	t.Helper()
	ln := newPipeListener()
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = ln.Close()
	})
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return ln.DialContext(ctx)
			},
		},
	}
}

func newPipeClientWSPair(t *testing.T) (clientConn *coderws.Conn, serverConn *coderws.Conn) {
	t.Helper()
	accepted := make(chan *coderws.Conn, 1)
	httpClient := startPipeHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		accepted <- c
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := coderws.Dial(ctx, "ws://pipe.local/v1/responses", &coderws.DialOptions{HTTPClient: httpClient})
	require.NoError(t, err)
	s := <-accepted
	t.Cleanup(func() {
		_ = c.CloseNow()
		_ = s.CloseNow()
	})
	return c, s
}

type providerSocketFixtureTransport struct {
	HTTPUpstream
	target *url.URL
	client *http.Client
}

func (f providerSocketFixtureTransport) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	if req.URL.Host != "api.example.com" || !HTTPUpstreamPublicHostsOnly(req.Context()) || !HTTPUpstreamRedirectsDisabled(req.Context()) || HTTPUpstreamProfileFromContext(req.Context()) != HTTPUpstreamProfileProviderSocket {
		return nil, errors.New("unexpected or unguarded socket target")
	}
	local := req.Clone(req.Context())
	local.URL.Scheme, local.URL.Host = f.target.Scheme, f.target.Host
	return f.client.Do(local)
}

type providerPipeSocketTransport struct {
	HTTPUpstream
	wsClient *http.Client
	onToken  func(req *http.Request) (*http.Response, error)
}

func (f providerPipeSocketTransport) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	if req.URL.Host != "api.example.com" || !HTTPUpstreamPublicHostsOnly(req.Context()) || !HTTPUpstreamRedirectsDisabled(req.Context()) {
		return nil, errors.New("unexpected or unguarded provider target")
	}
	if req.URL.Path == "/prefix/token" && f.onToken != nil {
		return f.onToken(req)
	}
	if HTTPUpstreamProfileFromContext(req.Context()) != HTTPUpstreamProfileProviderSocket {
		return nil, errors.New("missing provider socket profile")
	}
	local := req.Clone(req.Context())
	local.URL.Scheme, local.URL.Host = "http", "pipe.local"
	return f.wsClient.Do(local)
}

func TestNewAPIProviderUpstreamSocket(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			s, account, _ := providerFixture(t, providerSocketFixtureSource)
			resultJSON := `{"id":"chat-1","object":"chat.completion","model":"vendor-text","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4}}`
			if stream {
				resultJSON = `{"id":"chat-1","object":"chat.completion.chunk","model":"vendor-text","choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4}}`
			}
			upstreamCheck := make(chan error, 1)
			pipeClient := startPipeHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/prefix/socket" || r.Header.Get("Authorization") != "Bearer upstream-fixture-key" || r.Header.Get("X-Handshake-Method") != "GET" {
					upstreamCheck <- errors.New("wrong signed socket handshake")
					http.Error(w, "bad fixture handshake", 403)
					return
				}
				conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{Subprotocols: []string{"fixture"}})
				if err != nil {
					upstreamCheck <- err
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				kind, data, err := conn.Read(ctx)
				if err != nil || kind != coderws.MessageText || string(data) != "start" {
					upstreamCheck <- errors.New("missing initial text frame")
					return
				}
				if err = conn.Write(ctx, coderws.MessageBinary, []byte{1, 2, 3}); err != nil {
					upstreamCheck <- err
					return
				}
				kind, data, err = conn.Read(ctx)
				if err != nil || kind != coderws.MessageBinary || string(data) != "\x04\x05" {
					upstreamCheck <- errors.New("missing converted binary reply")
					return
				}
				upstreamCheck <- conn.Write(ctx, coderws.MessageText, []byte(resultJSON))
				_ = conn.Close(coderws.StatusNormalClosure, "")
			}))
			s.gateway.httpUpstream = providerPipeSocketTransport{wsClient: pipeClient}
			request := `{"model":"public-text","messages":[{"role":"user","content":"hi"}],"stream":` + map[bool]string{true: "true", false: "false"}[stream] + `}`
			c, recorder := providerContext(newAPIProviderChat, request)
			result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(request), newAPIProviderChat, "")
			require.NoError(t, err, recorder.Body.String())
			require.NoError(t, <-upstreamCheck)
			require.Equal(t, 10, result.Usage.InputTokens)
			require.Equal(t, 4, result.Usage.OutputTokens)
			require.Contains(t, recorder.Body.String(), "ok")
		})
	}
}

func TestNewAPIProviderResponsesWebSocketEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, account, _ := providerFixture(t, providerResponsesWSFixtureSource)
	wsCfg := &config.Config{}
	wsCfg.Gateway.OpenAIWS.Enabled = true
	wsCfg.Gateway.OpenAIWS.APIKeyEnabled = true
	wsCfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	wsCfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
	wsCfg.Gateway.OpenAIWS.MaxIdlePerAccount = 2
	wsCfg.Gateway.OpenAIWS.QueueLimitPerConn = 4
	wsCfg.Gateway.OpenAIWS.DialTimeoutSeconds = 5
	wsCfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
	wsCfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 5
	s.gateway.cfg = wsCfg

	var tokenCalls atomic.Int32
	var handshakeCalls atomic.Int32
	var upstreamAcceptedConns atomic.Int32
	var upstreamClosedConns atomic.Int32
	var receivedModelsMu sync.Mutex
	var receivedModels []string
	var handshakeHeadersMu sync.Mutex
	var handshakeHeaders []http.Header
	var handshakePaths []string

	pipeClient := startPipeHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := handshakeCalls.Add(1)
		handshakeHeadersMu.Lock()
		handshakeHeaders = append(handshakeHeaders, r.Header.Clone())
		handshakePaths = append(handshakePaths, r.URL.RequestURI())
		handshakeHeadersMu.Unlock()

		// First handshake rejects with HTTP 419 + JSON body to verify captured non-101 body triggers credentialRejected + single renewal.
		if n == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(419)
			_, _ = w.Write([]byte(`{"error":{"code":"token_expired","message":"expired token"}}`))
			return
		}
		if r.Header.Get("X-Signed-Token") != "tok-2" || r.Header.Get("X-Vendor-Auth") != "upstream-fixture-key" || r.Header.Get("X-Signed-Method") != "GET" {
			http.Error(w, "invalid signature or auth", http.StatusForbidden)
			return
		}
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		upstreamAcceptedConns.Add(1)
		defer func() {
			_ = conn.CloseNow()
			upstreamClosedConns.Add(1)
		}()

		for turn := 1; ; turn++ {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, raw, readErr := conn.Read(ctx)
			cancel()
			if readErr != nil {
				return
			}
			model := gjson.GetBytes(raw, "model").String()
			receivedModelsMu.Lock()
			receivedModels = append(receivedModels, model)
			receivedModelsMu.Unlock()

			// Ensure no Codex internal metadata leaked into Provider WS payloads.
			if gjson.GetBytes(raw, "client_metadata.x-codex-turn-metadata").Exists() {
				_ = conn.Close(coderws.StatusPolicyViolation, "leaked codex metadata")
				return
			}
			inTok := 10 * turn
			outTok := 4 * turn
			respID := "resp_turn_" + string(rune('0'+turn))
			events := []string{
				`{"type":"response.created","response":{"id":"` + respID + `","model":"` + model + `","status":"in_progress"}}`,
				`{"type":"response.output_text.delta","response_id":"` + respID + `","delta":"turn-ok"}`,
				`{"type":"response.completed","response":{"id":"` + respID + `","model":"` + model + `","status":"completed","usage":{"input_tokens":` + jsonInt(inTok) + `,"output_tokens":` + jsonInt(outTok) + `,"total_tokens":` + jsonInt(inTok+outTok) + `}}}`,
			}
			for _, ev := range events {
				wCtx, wCancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := conn.Write(wCtx, coderws.MessageText, []byte(ev))
				wCancel()
				if err != nil {
					return
				}
			}
		}
	}))

	s.gateway.httpUpstream = providerPipeSocketTransport{
		wsClient: pipeClient,
		onToken: func(req *http.Request) (*http.Response, error) {
			n := tokenCalls.Add(1)
			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Type", "application/json")
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.WriteString(`{"token":{"value":"tok-` + jsonInt(int(n)) + `"},"expiresAt":` + jsonInt(int(time.Now().Add(time.Hour).Unix())) + `}`)
			return rec.Result(), nil
		},
	}

	// Verify routing eligibility & scheduler compatibility.
	require.True(t, account.supportsNewAPIResponsesWebSocket("public-text"))
	require.True(t, s.gateway.isOpenAIAccountTransportCompatible(account, OpenAIUpstreamTransportResponsesWebsocketV2Ingress))
	wsCtx := WithNewAPIResponsesWebSocketRouting(context.Background())
	require.True(t, account.supportsNewAPIPluginScope(wsCtx, "public-text"))
	require.False(t, account.supportsNewAPIPluginScope(wsCtx, "unmapped-model"))

	// Session 1: channel-mapped model -> two turns reusing the same upstream socket -> model switch rejected!
	clientConn1, serverConn1 := newPipeClientWSPair(t)
	c1, _ := providerContext(newAPIProviderResponses, "")
	c1.Request.Header.Set("Authorization", "Bearer downstream-secret-must-not-leak")
	c1.Request.Header.Set("X-Codex-Window-Id", "codex-must-not-leak")
	c1.Request.Header.Set(openAIWSTurnMetadataHeader, "codex-turn-must-not-leak")

	var billedTurnsMu sync.Mutex
	var billedTurns []*OpenAIForwardResult
	hooks1 := &OpenAIWSIngressHooks{
		MapRequestModel: func(turn int, originalModel string) (string, error) {
			if originalModel == "channel-alias" {
				return "public-text", nil
			}
			return originalModel, nil
		},
		AfterTurn: func(turn int, result *OpenAIForwardResult, turnErr error) {
			if result != nil && turnErr == nil {
				billedTurnsMu.Lock()
				billedTurns = append(billedTurns, result)
				billedTurnsMu.Unlock()
			}
		},
	}
	firstMsg1 := []byte(`{"type":"response.create","model":"channel-alias","input":"hello-1"}`)
	proxyErrCh1 := make(chan error, 1)
	go func() {
		proxyErrCh1 <- s.gateway.ProxyResponsesWebSocketFromClient(context.Background(), c1, serverConn1, account, "", firstMsg1, hooks1)
	}()

	// Turn 1 events: model must be restored to client's "channel-alias".
	readUntilCompleted := func(conn *coderws.Conn) []byte {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, raw, err := conn.Read(ctx)
			cancel()
			require.NoError(t, err)
			if gjson.GetBytes(raw, "type").String() == "response.completed" {
				return raw
			}
		}
	}
	completed1 := readUntilCompleted(clientConn1)
	require.Equal(t, "channel-alias", gjson.GetBytes(completed1, "response.model").String())

	// Turn 2 with same model reuses the same session-private upstream connection.
	wCtx, wCancel := context.WithTimeout(context.Background(), 5*time.Second)
	require.NoError(t, clientConn1.Write(wCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"channel-alias","input":"hello-2"}`)))
	wCancel()
	completed2 := readUntilCompleted(clientConn1)
	require.Equal(t, "channel-alias", gjson.GetBytes(completed2, "response.model").String())

	// Turn 3 switches to a different model -> must be rejected with StatusPolicyViolation and NOT forwarded upstream!
	wCtx, wCancel = context.WithTimeout(context.Background(), 5*time.Second)
	require.NoError(t, clientConn1.Write(wCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"other-model","input":"switch"}`)))
	wCancel()

	proxyErr1 := <-proxyErrCh1
	var closeErr *OpenAIWSClientCloseError
	require.ErrorAs(t, proxyErr1, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
	require.Contains(t, closeErr.Error(), "pinned to its first model")

	// Wait briefly for upstream connection close cleanup triggered by defer pool.Close().
	require.Eventually(t, func() bool { return upstreamClosedConns.Load() == 1 }, 3*time.Second, 20*time.Millisecond)
	require.Equal(t, int32(1), upstreamAcceptedConns.Load(), "both turns in session 1 must reuse one socket")
	require.Equal(t, int32(2), tokenCalls.Load(), "401 rejection must trigger exactly one exchangeKey renewal")
	require.Equal(t, int32(2), handshakeCalls.Load(), "initial 401 + renewed handshake")

	// Verify header isolation on handshake.
	handshakeHeadersMu.Lock()
	require.Len(t, handshakeHeaders, 2)
	require.Equal(t, "/prefix/ws/responses/vendor-text?signed=1", handshakePaths[1])
	require.Empty(t, handshakeHeaders[1].Get("Authorization"), "downstream Authorization must never leak")
	require.Empty(t, handshakeHeaders[1].Get("X-Codex-Window-Id"), "Codex headers must never leak")
	require.Empty(t, handshakeHeaders[1].Get("X-Extra-Secret"), "downstream custom headers must never leak")
	require.Equal(t, openAIWSBetaV2Value, handshakeHeaders[1].Get("OpenAI-Beta"))
	handshakeHeadersMu.Unlock()

	// Verify upstream received mapped model "vendor-text" for both turns, and never "other-model".
	receivedModelsMu.Lock()
	require.Equal(t, []string{"vendor-text", "vendor-text"}, receivedModels)
	receivedModelsMu.Unlock()

	// Verify per-turn usage billing callback captured both turns accurately.
	billedTurnsMu.Lock()
	require.Len(t, billedTurns, 2)
	require.Equal(t, "channel-alias", billedTurns[0].Model)
	require.Equal(t, "vendor-text", billedTurns[0].UpstreamModel)
	require.Equal(t, 10, billedTurns[0].Usage.InputTokens)
	require.Equal(t, 4, billedTurns[0].Usage.OutputTokens)
	require.Equal(t, 20, billedTurns[1].Usage.InputTokens)
	require.Equal(t, 8, billedTurns[1].Usage.OutputTokens)
	billedTurnsMu.Unlock()

	// Session 2: must dial a fresh isolated connection (never sharing Session 1's pool) and clean up on client disconnect.
	clientConn2, serverConn2 := newPipeClientWSPair(t)
	c2, _ := providerContext(newAPIProviderResponses, "")
	proxyErrCh2 := make(chan error, 1)
	go func() {
		proxyErrCh2 <- s.gateway.ProxyResponsesWebSocketFromClient(context.Background(), c2, serverConn2, account, "", []byte(`{"type":"response.create","model":"public-text","input":"session-2"}`), nil)
	}()
	completed3 := readUntilCompleted(clientConn2)
	require.Equal(t, "public-text", gjson.GetBytes(completed3, "response.model").String())
	require.Equal(t, int32(2), upstreamAcceptedConns.Load(), "session 2 must have its own isolated connection pool")
	_ = clientConn2.Close(coderws.StatusNormalClosure, "bye")
	require.NoError(t, <-proxyErrCh2)
	require.Eventually(t, func() bool { return upstreamClosedConns.Load() == 2 }, 3*time.Second, 20*time.Millisecond)
}

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
