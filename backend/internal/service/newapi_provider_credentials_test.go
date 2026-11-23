//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAPIProviderTokenCache(t *testing.T) {
	ctx := context.Background()
	var cache newAPIProviderTokenCache
	var calls atomic.Int32
	exchange := func(context.Context) (jsplugin.ExchangedToken, error) {
		return jsplugin.ExchangedToken{Token: json.RawMessage(fmt.Sprintf(`"token-%d"`, calls.Add(1)))}, nil
	}
	first, err := cache.get(ctx, "connection", exchange)
	require.NoError(t, err)
	first[1] = 'X' // A caller cannot mutate the cached token.
	second, err := cache.get(ctx, "connection", exchange)
	require.NoError(t, err)
	require.JSONEq(t, `"token-1"`, string(second))
	require.EqualValues(t, 1, calls.Load())
	cache.invalidate("connection", json.RawMessage(`"old-token"`))
	_, err = cache.get(ctx, "connection", exchange)
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())
	cache.invalidate("connection", second)
	third, err := cache.get(ctx, "connection", exchange)
	require.NoError(t, err)
	require.JSONEq(t, `"token-2"`, string(third))
	cache.invalidate("connection", second)
	_, err = cache.get(ctx, "connection", exchange)
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load(), "a late rejection cannot evict the replacement")

	for _, expires := range []int64{time.Now().Add(-time.Minute).Unix(), time.Now().Add(10 * time.Second).Unix(), time.Now().Add(time.Hour).Unix()} {
		t.Run(fmt.Sprint(expires), func(t *testing.T) {
			var cache newAPIProviderTokenCache
			var calls int
			for range 2 {
				_, err := cache.get(ctx, "expiry", func(context.Context) (jsplugin.ExchangedToken, error) {
					calls++
					return jsplugin.ExchangedToken{Token: json.RawMessage(`{"value":"token"}`), ExpiresAt: expires}, nil
				})
				if expires <= time.Now().Unix() {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			}
			if time.Unix(expires, 0).After(time.Now().Add(newAPIProviderTokenMargin)) {
				require.Equal(t, 1, calls)
			} else {
				require.Equal(t, 2, calls)
			}
		})
	}

	for _, token := range []json.RawMessage{nil, json.RawMessage(`invalid`), json.RawMessage(`"` + strings.Repeat("x", jsplugin.MaxCredentialBytes) + `"`)} {
		var cache newAPIProviderTokenCache
		_, err := cache.get(ctx, "invalid", func(context.Context) (jsplugin.ExchangedToken, error) {
			return jsplugin.ExchangedToken{Token: token}, nil
		})
		require.Error(t, err)
		require.Empty(t, cache.tokens)
	}
	for range 2 {
		_, err := cache.get(ctx, "failed", func(context.Context) (jsplugin.ExchangedToken, error) {
			return jsplugin.ExchangedToken{}, errors.New("exchange failed")
		})
		require.Error(t, err)
	}
	require.NotContains(t, cache.tokens, "failed")
}

func TestNewAPIProviderTokenConcurrentCancellationAndBounds(t *testing.T) {
	var cache newAPIProviderTokenCache
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	exchange := func(ctx context.Context) (jsplugin.ExchangedToken, error) {
		calls.Add(1)
		close(started)
		select {
		case <-ctx.Done():
			return jsplugin.ExchangedToken{}, ctx.Err()
		case <-release:
			return jsplugin.ExchangedToken{Token: json.RawMessage(`"shared"`)}, nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := cache.get(ctx, "same", exchange); first <- err }()
	<-started
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			token, err := cache.get(context.Background(), "same", exchange)
			assert.NoError(t, err)
			assert.JSONEq(t, `"shared"`, string(token))
		})
	}
	cancel()
	require.ErrorIs(t, <-first, context.Canceled)
	close(release)
	wg.Wait()
	require.EqualValues(t, 1, calls.Load())

	cache.flights = make(map[string]*newAPIProviderTokenFlight)
	for i := range newAPIProviderExchangeLimit {
		cache.flights[fmt.Sprint(i)] = &newAPIProviderTokenFlight{}
	}
	_, err := cache.get(context.Background(), "overflow", exchange)
	require.ErrorContains(t, err, "busy")
	cache.flights = nil
	cache.tokens = make(map[string]jsplugin.ExchangedToken)
	for i := range newAPIProviderTokenLimit {
		cache.tokens[fmt.Sprint(i)] = jsplugin.ExchangedToken{Token: json.RawMessage(`"cached"`)}
	}
	_, err = cache.get(context.Background(), "new", func(context.Context) (jsplugin.ExchangedToken, error) {
		return jsplugin.ExchangedToken{Token: json.RawMessage(`"new"`)}, nil
	})
	require.NoError(t, err)
	require.Len(t, cache.tokens, 1)
}

func TestNewAPIProviderTokenIdentity(t *testing.T) {
	version := &NewAPIPluginVersion{ID: 1, Source: "source"}
	account := &Account{ID: 7, Credentials: map[string]any{"api_key": "secret"}}
	key := newAPIProviderTokenKey(version, account, "https://api.example.com", json.RawMessage(`{"x":1}`))
	require.Len(t, key, 64)
	for _, change := range []func(*NewAPIPluginVersion, *Account){
		func(v *NewAPIPluginVersion, a *Account) { v.ID++ },
		func(v *NewAPIPluginVersion, a *Account) { v.Source = "edited" },
		func(v *NewAPIPluginVersion, a *Account) { a.ID++ },
		func(v *NewAPIPluginVersion, a *Account) { a.Credentials["api_key"] = "rotated" },
		func(v *NewAPIPluginVersion, a *Account) { id := int64(2); a.ProxyID = &id },
	} {
		v, a := *version, *account
		a.Credentials = maps.Clone(account.Credentials)
		change(&v, &a)
		require.NotEqual(t, key, newAPIProviderTokenKey(&v, &a, "https://api.example.com", json.RawMessage(`{"x":1}`)))
	}
	require.NotEqual(t, key, newAPIProviderTokenKey(version, account, "https://other.example.com", json.RawMessage(`{"x":1}`)))
	require.NotEqual(t, key, newAPIProviderTokenKey(version, account, "https://api.example.com", json.RawMessage(`{"x":2}`)))
}

type providerCredentialHTTP struct {
	HTTPUpstream
	mu               sync.Mutex
	paths            []string
	headers          []http.Header
	bodies           []string
	proxies          []string
	exchanges        int
	chats            int
	rejections       int
	rejectStatus     int
	response         string
	exchangeResponse string
	contentType      string
	beforeRenew      func()
}

func (h *providerCredentialHTTP) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if !HTTPUpstreamPublicHostsOnly(req.Context()) || !HTTPUpstreamRedirectsDisabled(req.Context()) {
		return nil, errors.New("missing plugin network policy")
	}
	body, _ := io.ReadAll(req.Body)
	h.paths = append(h.paths, req.URL.Path)
	h.headers = append(h.headers, req.Header.Clone())
	h.bodies = append(h.bodies, string(body))
	h.proxies = append(h.proxies, proxy)
	status, response := 200, h.response
	switch req.URL.Path {
	case "/prefix/token":
		h.exchanges++
		response = fmt.Sprintf(`{"token":{"value":"token-%d"},"expiresAt":0}`, h.exchanges)
		if h.exchangeResponse != "" {
			response = h.exchangeResponse
		}
	case "/prefix/check":
		response = `{}`
	default:
		h.chats++
		if h.chats <= h.rejections {
			status, response = h.rejectStatus, `{"error":{"message":"credential expired","code":"token_expired"}}`
			if h.beforeRenew != nil {
				h.beforeRenew()
			}
		}
		if response == "" {
			response = `{"id":"chat-creds","object":"chat.completion","model":"vendor-text","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`
		}
	}
	kind := h.contentType
	if kind == "" || req.URL.Path == "/prefix/token" || req.URL.Path == "/prefix/check" {
		kind = "application/json"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

func providerExchangeSource(source string) string {
	source = strings.Replace(source, `"X-Config":ctx.config.label`, `"X-Config":ctx.config.label,"X-Token":ctx.token.value`, 1)
	return source + `
export function exchangeKey(ctx){
  const resp=utils.fetch({url:ctx.baseUrl+"/token",method:"POST",headers:{"X-Exchange-Key":ctx.apiKey},body:JSON.stringify({label:ctx.config.label})});
  return resp.body;
}
export function credentialRejected(ctx,resp){return resp.status===419 && JSON.parse(resp.body).error.code==="token_expired";}`
}

func TestNewAPIProviderExchangeRelayAndRenewal(t *testing.T) {
	for _, rejection := range []int{0, 401, 419, 500} {
		t.Run(fmt.Sprint(rejection), func(t *testing.T) {
			s, account, _ := providerFixture(t, providerExchangeSource(providerFixtureSource))
			h := &providerCredentialHTTP{rejectStatus: rejection}
			if rejection != 0 {
				h.rejections = 1
			}
			s.gateway.httpUpstream = h
			accountState := &compatStreamSideEffectsRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}}}
			s.gateway.rateLimitService = &RateLimitService{accountRepo: accountState}
			body := `{"model":"public-text","messages":[]}`
			c, recorder := providerContext(newAPIProviderChat, body)
			result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
			if rejection == 500 {
				require.Error(t, err)
				require.Equal(t, 1, h.exchanges)
				require.Equal(t, 1, h.chats)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 200, recorder.Code)
			require.Equal(t, 12, result.Usage.InputTokens)
			require.Equal(t, 5, result.Usage.OutputTokens)
			require.Empty(t, accountState.errorCalls, "a recovered token must not disable the account")
			if rejection == 0 {
				require.Equal(t, []string{"/prefix/token", "/prefix/vendor/chat"}, h.paths)
			} else {
				require.Equal(t, []string{"/prefix/token", "/prefix/vendor/chat", "/prefix/token", "/prefix/vendor/chat"}, h.paths)
				require.Equal(t, "token-2", h.headers[3].Get("X-Token"))
			}
			require.Equal(t, "upstream-fixture-key", h.headers[0].Get("X-Exchange-Key"))
			require.Equal(t, "token-1", h.headers[1].Get("X-Token"))
			require.Empty(t, h.headers[1].Get("X-Exchange-Key"))
			c, _ = providerContext(newAPIProviderChat, body)
			_, err = s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
			require.NoError(t, err)
			require.Equal(t, 1+map[bool]int{true: 1}[rejection != 0], h.exchanges, "subsequent requests reuse the token")
			assertNewAPIProviderExistingBilling(t, result, account)
		})
	}
}

func TestNewAPIProviderRenewalIsBoundedAndPinned(t *testing.T) {
	s, account, _ := providerFixture(t, providerExchangeSource(providerFixtureSource))
	h := &providerCredentialHTTP{rejectStatus: 419, rejections: 3}
	s.gateway.httpUpstream = h
	body := `{"model":"public-text","messages":[]}`
	c, _ := providerContext(newAPIProviderChat, body)
	_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
	require.Error(t, err)
	require.Equal(t, 2, h.chats)
	require.Equal(t, 2, h.exchanges)
	require.Empty(t, s.providerTokens.tokens, "the final rejected token must be evicted too")

	s, account, _ = providerFixture(t, providerExchangeSource(providerFixtureSource))
	h = &providerCredentialHTTP{rejectStatus: 401, rejections: 1}
	s.gateway.httpUpstream = h
	h.beforeRenew = func() {
		account.Credentials["api_key"] = "rotated-key"
		account.Credentials[newAPIPluginConfigEncryptedKey] = `ENC:{"label":"edited"}`
		account.Credentials["base_url"] = "https://other.example.com"
	}
	c, _ = providerContext(newAPIProviderChat, body)
	_, err = s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
	require.NoError(t, err)
	require.Equal(t, "upstream-fixture-key", h.headers[2].Get("X-Exchange-Key"))
	require.JSONEq(t, `{"label":"saved"}`, h.bodies[2])
	require.Equal(t, "saved", h.headers[3].Get("X-Config"))
}

func TestNewAPIProviderCheckKeyOnChangedConnection(t *testing.T) {
	source := providerFixtureSource + `
export function checkKey(ctx,key){
  if(key!=="upstream-fixture-key") return {error:{en:"secret: "+key}};
  const resp=utils.fetch({url:ctx.baseUrl+"/check",headers:{"X-Check-Key":key}});
  if(resp.status!==200) throw Error(key);
  return null;
}`
	s, repo := requestPluginFixture(t, source)
	repo.versions[1].Enabled = true
	h := &providerCredentialHTTP{}
	s.gateway.httpUpstream = h
	account := &Account{ID: 17, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{NewAPIPluginRevisionKey: 1, "base_url": "https://api.example.com/prefix", "api_key": "upstream-fixture-key", "model_mapping": map[string]any{"public-text": "vendor-text"}}}
	require.NoError(t, s.NormalizeAccountBinding(context.Background(), account, map[string]any{}))
	require.Equal(t, []string{"/prefix/check"}, h.paths)
	previous := *account
	previous.Credentials = maps.Clone(account.Credentials)
	require.NoError(t, s.NormalizeAccountBinding(context.Background(), account, map[string]any{}, &previous))
	require.Len(t, h.paths, 1, "unchanged key/config/base/version/proxy should not require upstream availability")
	account.Credentials["api_key"] = "private-key-to-redact"
	err := s.NormalizeAccountBinding(context.Background(), account, map[string]any{}, &previous)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-key-to-redact")
	account.Credentials["api_key"] = "upstream-fixture-key"
	account.Credentials["base_url"] = "https://api.example.com/changed"
	require.NoError(t, s.NormalizeAccountBinding(context.Background(), account, map[string]any{}, &previous))
	require.Equal(t, "/changed/check", h.paths[1])
	previous = *account
	previous.Credentials = maps.Clone(account.Credentials)
	proxyID := int64(11)
	account.ProxyID = &proxyID
	s.proxyRepo = &providerCredentialProxyRepo{proxy: &Proxy{ID: proxyID, Protocol: "http", Host: "proxy.example.com", Port: 3128}}
	require.NoError(t, normalizeNewAPIPluginAccount(context.Background(), s, account, nil, &previous))
	require.Equal(t, "http://proxy.example.com:3128", h.proxies[2], "proxy-only edits must resolve and check through the new proxy")
	previous = *account
	previous.Credentials = maps.Clone(account.Credentials)
	account.Type = AccountTypeOAuth
	require.Error(t, normalizeNewAPIPluginAccount(context.Background(), s, account, nil, &previous))
}

type providerCredentialProxyRepo struct {
	ProxyRepository
	proxy *Proxy
}

func (r *providerCredentialProxyRepo) GetByID(context.Context, int64) (*Proxy, error) {
	return r.proxy, nil
}

func TestNewAPIProviderCredentialNetworkIsolation(t *testing.T) {
	for _, target := range []string{"https://unapproved.example/token", "http://127.0.0.1/token"} {
		source := strings.Replace(providerExchangeSource(providerFixtureSource), `ctx.baseUrl+"/token"`, fmt.Sprintf("%q", target), 1)
		s, account, upstream := providerFixture(t, source)
		_, err := s.buildProviderRequest(context.Background(), account, newAPIProviderChat, "vendor-text", []byte(`{"model":"vendor-text","messages":[]}`))
		require.Error(t, err)
		require.Empty(t, upstream.requests)
		require.Empty(t, s.providerTokens.tokens)
	}
	s, account, _ := providerFixture(t, providerExchangeSource(providerFixtureSource))
	id := int64(11)
	account.ProxyID = &id
	_, err := s.buildProviderRequest(context.Background(), account, newAPIProviderChat, "vendor-text", []byte(`{"model":"vendor-text","messages":[]}`))
	require.ErrorContains(t, err, "proxy")
	account.Proxy = &Proxy{ID: id, Protocol: "http", Host: "proxy.example.com", Port: 3128}
	h := &providerCredentialHTTP{}
	s.gateway.httpUpstream = h
	exchange, err := s.buildProviderRequest(context.Background(), account, newAPIProviderChat, "vendor-text", []byte(`{"model":"vendor-text","messages":[]}`))
	require.NoError(t, err)
	require.Equal(t, account.Proxy.URL(), h.proxies[0])
	require.Equal(t, account.Proxy.URL(), exchange.proxy)
}

func TestNewAPIProviderTokenInAllConversionHooks(t *testing.T) {
	for _, path := range []string{newAPIProviderChat, newAPIProviderResponses} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprint(path, stream), func(t *testing.T) {
				source := providerExchangeSource(providerConverterSource())
				for _, hook := range []string{"convertRequest", "prepareRequest", "convertResponse", "convertStreamEvent"} {
					argument := map[string]string{"convertRequest": "req", "prepareRequest": "req", "convertResponse": "resp", "convertStreamEvent": "event"}[hook]
					declaration := "export function " + hook + "(ctx, " + argument + ") {"
					source = strings.Replace(source, declaration, declaration+`if(ctx.token.value!=="token-1") throw Error("missing token in `+hook+`");`, 1)
				}
				s, account, _ := providerFixture(t, source)
				h := &providerCredentialHTTP{response: `{"reply":"hello","input":12,"output":5}`}
				if stream {
					h.contentType = "text/event-stream"
					h.response = "data: {\"seq\":0,\"text\":\"hello\"}\n\ndata: {\"seq\":1,\"finished\":true,\"input\":12,\"output\":5}\n\n"
				}
				s.gateway.httpUpstream = h
				body := fmt.Sprintf(`{"model":"public-text","messages":[],"input":"hello","stream":%t}`, stream)
				c, _ := providerContext(path, body)
				result, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), path, "")
				require.NoError(t, err)
				require.Equal(t, 12, result.Usage.InputTokens)
				require.Equal(t, 5, result.Usage.OutputTokens)
				require.Equal(t, 1, h.exchanges)
				require.Equal(t, "token-1", h.headers[1].Get("X-Token"))
			})
		}
	}
}

func TestNewAPIProviderConcurrentBuildersShareExchange(t *testing.T) {
	s, account, _ := providerFixture(t, providerExchangeSource(providerFixtureSource))
	h := &providerCredentialHTTP{}
	s.gateway.httpUpstream = h
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			exchange, err := s.buildProviderRequest(ctx, account, newAPIProviderChat, "vendor-text", []byte(`{"model":"vendor-text","messages":[]}`))
			if assert.NoError(t, err) {
				assert.Equal(t, "token-1", exchange.request.Header.Get("X-Token"))
			}
		})
	}
	wg.Wait()
	require.Equal(t, 1, h.exchanges, "waiting for token exchange must not hold worker slots")
}

func TestNewAPIProviderCredentialFailureAndAccountPolicy(t *testing.T) {
	for _, exchangeResponse := range []string{
		`{"error":{"status":401,"message":"upstream-fixture-key","code":"bad_key"}}`,
		`{"token":null}`,
		`{"token":"upstream-fixture-key","expiresAt":1}`,
	} {
		s, account, _ := providerFixture(t, providerExchangeSource(providerFixtureSource))
		h := &providerCredentialHTTP{exchangeResponse: exchangeResponse}
		s.gateway.httpUpstream = h
		repo := &compatStreamSideEffectsRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}}}
		s.gateway.rateLimitService = &RateLimitService{accountRepo: repo}
		body := `{"model":"public-text","messages":[]}`
		for range 2 {
			c, recorder := providerContext(newAPIProviderChat, body)
			_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
			require.Error(t, err)
			require.Equal(t, 503, recorder.Code)
			require.NotContains(t, err.Error(), "upstream-fixture-key")
			require.NotContains(t, recorder.Body.String(), "upstream-fixture-key")
		}
		require.Equal(t, 2, h.exchanges, "failed exchanges must not be cached")
		require.Zero(t, h.chats)
		require.Empty(t, s.providerTokens.tokens)
		if strings.Contains(exchangeResponse, `"status":401`) {
			require.NotEmpty(t, repo.errorCalls, "exchange auth rejection must reach the existing account policy")
		}
		for _, message := range repo.errorCalls {
			require.NotContains(t, message, "upstream-fixture-key")
		}
	}
	s, account, _ := providerFixture(t, providerExchangeSource(providerFixtureSource))
	h := &providerCredentialHTTP{rejectStatus: 419, rejections: 3}
	s.gateway.httpUpstream = h
	repo := &compatStreamSideEffectsRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}}}
	s.gateway.rateLimitService = &RateLimitService{accountRepo: repo}
	body := `{"model":"public-text","messages":[]}`
	c, _ := providerContext(newAPIProviderChat, body)
	_, err := s.gateway.forwardNewAPIProvider(c.Request.Context(), c, account, []byte(body), newAPIProviderChat, "")
	require.Error(t, err)
	require.NotEmpty(t, repo.errorCalls, "custom credentialRejected must reach the existing auth error policy")
}

func TestNewAPIProviderRejectionHookFailurePreservesOriginalResponse(t *testing.T) {
	for _, hook := range []string{`throw Error("upstream-fixture-key")`, `return "invalid"`, `utils.fetch({url:"https://api.example.com"}); return true`} {
		source := strings.Replace(providerExchangeSource(providerFixtureSource), `return resp.status===419 && JSON.parse(resp.body).error.code==="token_expired";`, hook, 1)
		s, account, _ := providerFixture(t, source)
		h := &providerCredentialHTTP{rejectStatus: 419, rejections: 1}
		s.gateway.httpUpstream = h
		exchange, err := s.buildProviderRequest(context.Background(), account, newAPIProviderChat, "vendor-text", []byte(`{"model":"vendor-text","messages":[]}`))
		require.NoError(t, err)
		original := strings.Repeat("error-content", 10_000)
		resp := &http.Response{StatusCode: 419, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(original))}
		require.False(t, s.providerCredentialWasRejected(context.Background(), exchange, resp))
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, original, string(body))
		require.Len(t, h.paths, 1, "rejection hooks cannot fetch")
	}
}
