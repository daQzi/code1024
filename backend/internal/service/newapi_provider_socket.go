package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	coderws "github.com/coder/websocket"
)

func providerWebSocketHTTPURL(raw string) string {
	if suffix, ok := strings.CutPrefix(raw, "wss://"); ok {
		return "https://" + suffix
	}
	if suffix, ok := strings.CutPrefix(raw, "ws://"); ok {
		return "http://" + suffix
	}
	return raw
}

type providerSocketTransport func(*http.Request) (*http.Response, error)

func (f providerSocketTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func (s *OpenAIGatewayService) doNewAPIProviderTransport(ctx context.Context, req *http.Request, exchange *newAPIProviderExchange, account *Account) (*http.Response, error) {
	if exchange.socket == nil {
		return s.doOpenAIUpstream(req, exchange.proxy, account)
	}
	conn, resp, err := s.dialNewAPIProviderSocket(ctx, req, account, exchange.proxy, exchange.socket.Protocols)
	if err != nil {
		if resp != nil && resp.StatusCode >= 300 && resp.StatusCode <= 599 && resp.Body != nil {
			return resp, nil // HTTP credential rejection may renew once before any frames.
		}
		return nil, errors.New("provider WebSocket handshake failed")
	}
	conn.SetReadLimit(MaxRequestPluginBody)
	stream := &providerSocketBody{conn: conn}
	// The upgrade (even a GET) and its initial frames can start paid work.
	exchange.credentialReplayUnsafe = true
	writeCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	err = stream.send(writeCtx, exchange.socket.Messages)
	stop()
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: stream, Header: resp.Header.Clone(), ContentLength: -1}, nil
}

func (s *OpenAIGatewayService) dialNewAPIProviderSocket(ctx context.Context, req *http.Request, account *Account, proxy string, protocols []string) (*coderws.Conn, *http.Response, error) {
	// Use the same validated egress and account proxy as Provider HTTP. The
	// websocket dialer owns handshake headers, not the uploaded plugin source.
	// coder/websocket clears resp.Body on non-101 replies, so capture a bounded
	// copy here to preserve HTTP 401 and plugin credentialRejected inspection.
	var capturedResp *http.Response
	client := &http.Client{Transport: providerSocketTransport(func(request *http.Request) (*http.Response, error) {
		hop := WithHTTPUpstreamProfile(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(request.Context())), HTTPUpstreamProfileProviderSocket)
		resp, err := s.doOpenAIUpstream(request.WithContext(hop), proxy, account)
		if err == nil && resp != nil && resp.StatusCode != http.StatusSwitchingProtocols && resp.Body != nil {
			bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, MaxRequestPluginBody))
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			cloned := *resp
			cloned.Header = resp.Header.Clone()
			cloned.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			capturedResp = &cloned
		}
		return resp, err
	}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	headers := req.Header.Clone()
	for name := range headers {
		if key := strings.ToLower(name); key == "connection" || key == "upgrade" || strings.HasPrefix(key, "sec-websocket-") {
			headers.Del(name)
		}
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, resp, err := coderws.Dial(dialCtx, req.URL.String(), &coderws.DialOptions{
		HTTPClient: client, HTTPHeader: headers, Subprotocols: protocols,
	})
	if err != nil && capturedResp != nil {
		return nil, capturedResp, err
	}
	return conn, resp, err
}

type newAPIResponsesSocketRoutingKey struct{}
type newAPIResponsesHandshakeKey struct{}

func WithNewAPIResponsesWebSocketRouting(ctx context.Context) context.Context {
	return context.WithValue(WithNewAPIProviderRouting(ctx, newAPIProviderResponses), newAPIResponsesSocketRoutingKey{}, true)
}

func (a *Account) supportsNewAPIResponsesWebSocket(model string) bool {
	if !a.SupportsNewAPIProviderModel(newAPIProviderResponses, model) {
		return false
	}
	return (jsplugin.Meta{Relay: a.newAPIProviderBinding().Routes}).ServesResponsesWebSocket(modelmodifier.Base(a.mappedNewAPIProviderModel(model)))
}

type providerResponsesSocketDialer struct {
	service *OpenAIGatewayService
	account *Account
	model   string
}

var providerSocketPoolSequence atomic.Uint64

func (d providerResponsesSocketDialer) Dial(ctx context.Context, _ string, headers http.Header, _ string) (openAIWSClientConn, int, http.Header, error) {
	handshakeHeaders := make(map[string]string, len(headers))
	for name, values := range headers {
		if len(values) == 0 {
			continue
		}
		if v := strings.TrimSpace(values[0]); v != "" {
			handshakeHeaders[name] = v
		}
	}
	ctx = context.WithValue(ctx, newAPIResponsesHandshakeKey{}, handshakeHeaders)
	plugins := d.service.newAPIPlugins
	step, err := plugins.buildProviderRequest(ctx, d.account, newAPIProviderResponses, d.model, nil)
	if err != nil {
		return nil, 0, nil, err
	}
	for attempt := 0; ; attempt++ {
		conn, resp, err := d.service.dialNewAPIProviderSocket(ctx, step.request, d.account, step.proxy, nil)
		if err == nil {
			conn.SetReadLimit(openAIWSMessageReadLimitBytes)
			return &coderOpenAIWSClientConn{conn: conn}, 0, resp.Header.Clone(), nil
		}
		if resp == nil {
			return nil, 0, nil, errors.New("provider Responses socket handshake failed")
		}
		rejected := resp.Body != nil && plugins.providerCredentialWasRejected(ctx, step, resp)
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		if rejected {
			plugins.providerTokens.invalidate(step.tokenKey, step.hookCtx.Token)
			if attempt == 0 && step.renew != nil {
				if fresh, renewErr := step.renew(ctx); renewErr == nil {
					step = fresh
					continue
				}
			}
		}
		return nil, resp.StatusCode, resp.Header.Clone(), errors.New("provider Responses socket handshake rejected")
	}
}

type providerSocketBody struct {
	conn      *coderws.Conn
	closeOnce sync.Once
	bytesSent int
	frames    int
}

func (*providerSocketBody) Read([]byte) (int, error) {
	return 0, errors.New("provider socket must be decoded as frames")
}

func (b *providerSocketBody) Close() error {
	b.closeOnce.Do(func() { _ = b.conn.CloseNow() })
	return nil
}

func (b *providerSocketBody) next(ctx context.Context, idle time.Duration) (*jsplugin.StreamEvent, error) {
	ctx, cancel := context.WithTimeout(ctx, idle)
	defer cancel()
	kind, data, err := b.conn.Read(ctx)
	if err != nil {
		if coderws.CloseStatus(err) == coderws.StatusNormalClosure {
			return nil, io.EOF
		}
		return nil, errors.New("provider WebSocket frame read failed")
	}
	if kind == coderws.MessageBinary {
		if data == nil {
			data = []byte{}
		}
		return &jsplugin.StreamEvent{Binary: data}, nil
	}
	return &jsplugin.StreamEvent{Data: string(data)}, nil
}

func (b *providerSocketBody) send(ctx context.Context, messages []jsplugin.WebSocketMessage) error {
	for _, message := range messages {
		kind, data := coderws.MessageText, []byte(message.Text)
		if message.IsBinary {
			kind, data = coderws.MessageBinary, message.Binary
		}
		if len(data) > MaxRequestPluginBody || b.frames >= 4096 || b.bytesSent+len(data) > 16<<20 {
			return errors.New("provider WebSocket exceeded its outgoing frame budget")
		}
		b.frames++
		b.bytesSent += len(data)
		if err := b.conn.Write(ctx, kind, data); err != nil {
			return errors.New("provider WebSocket frame write failed")
		}
	}
	return nil
}
