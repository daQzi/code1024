package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

const (
	newAPIProviderTokenLimit        = 10_000
	newAPIProviderExchangeLimit     = 128
	newAPIProviderCredentialTimeout = 15 * time.Second
	newAPIProviderTokenMargin       = 30 * time.Second
	newAPIProviderSetupTimeout      = newAPIProviderCredentialTimeout + 10*time.Second
)

// Like New API's static-key cache, tokens belong to an immutable connection
// identity. A late rejection must never evict a newer token for that identity.
// Neither keys nor tokens are persisted or included in cache keys verbatim.
type newAPIProviderTokenCache struct {
	mu      sync.Mutex
	tokens  map[string]jsplugin.ExchangedToken
	flights map[string]*newAPIProviderTokenFlight
}

type newAPIProviderTokenFlight struct {
	done  chan struct{}
	token json.RawMessage
	err   error
}

type newAPIProviderCredentialError struct{ status int }

func (*newAPIProviderCredentialError) Error() string { return "Provider key exchange failed" }

func newAPIProviderTokenKey(version *NewAPIPluginVersion, account *Account, base string, config json.RawMessage) string {
	source := sha256.Sum256([]byte(version.Source))
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	identity, _ := json.Marshal([]any{account.ID, version.ID, hex.EncodeToString(source[:]), base, config, account.GetCredential("api_key"), account.ProxyID, proxy})
	sum := sha256.Sum256(identity)
	return hex.EncodeToString(sum[:])
}

func (c *newAPIProviderTokenCache) get(ctx context.Context, key string, exchange func(context.Context) (jsplugin.ExchangedToken, error)) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if cached, ok := c.tokens[key]; ok && (cached.ExpiresAt == 0 || time.Unix(cached.ExpiresAt, 0).After(time.Now().Add(newAPIProviderTokenMargin))) {
		c.mu.Unlock()
		return bytes.Clone(cached.Token), nil
	}
	delete(c.tokens, key)
	flight := c.flights[key]
	if flight == nil {
		if len(c.flights) >= newAPIProviderExchangeLimit {
			c.mu.Unlock()
			return nil, errors.New("provider credential exchange is busy")
		}
		if c.flights == nil {
			c.flights = make(map[string]*newAPIProviderTokenFlight)
		}
		flight = &newAPIProviderTokenFlight{done: make(chan struct{})}
		c.flights[key] = flight
		go func() {
			// Shared work survives one caller disconnecting, but has its own
			// deadline and uses a bounded worker/fetch budget.
			hookCtx, cancel := context.WithTimeout(context.Background(), newAPIProviderCredentialTimeout)
			defer cancel()
			result, err := exchange(hookCtx)
			if err == nil && (len(result.Token) == 0 || len(result.Token) > jsplugin.MaxCredentialBytes || !json.Valid(result.Token) || result.ExpiresAt != 0 && result.ExpiresAt <= time.Now().Unix()) {
				err = &newAPIProviderCredentialError{}
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if err == nil {
				flight.token = bytes.Clone(result.Token)
				if result.ExpiresAt == 0 || time.Unix(result.ExpiresAt, 0).After(time.Now().Add(newAPIProviderTokenMargin)) {
					if c.tokens == nil || len(c.tokens) >= newAPIProviderTokenLimit {
						c.tokens = make(map[string]jsplugin.ExchangedToken)
					}
					result.Token = bytes.Clone(result.Token)
					c.tokens[key] = result
				}
			}
			flight.err = err
			delete(c.flights, key)
			close(flight.done)
		}()
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-flight.done:
		return bytes.Clone(flight.token), flight.err
	}
}

func (c *newAPIProviderTokenCache) invalidate(key string, used json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.tokens[key]; ok && bytes.Equal(cached.Token, used) {
		delete(c.tokens, key)
	}
}

// Reuse the existing bounded plugin HTTP host, including account proxy,
// allowed hosts, public DNS validation and disabled redirects. Only credential
// exchange/check hooks gain fetch; relay and signing hooks remain offline.
func (s *NewAPIPluginService) providerCredentialSession(client *runner.Client, version *NewAPIPluginVersion, account *Account, base string, config json.RawMessage) *newAPITaskSession {
	return &newAPITaskSession{service: s, client: client, version: version, account: account, base: base, config: config,
		hosts: append(slices.Clone(version.Meta.AllowedHosts), client.Meta.ChannelConfig.URLHosts(config)...)}
}

func (s *NewAPIPluginService) exchangeProviderKey(ctx context.Context, version *NewAPIPluginVersion, account *Account, base string, config json.RawMessage) (jsplugin.ExchangedToken, error) {
	var result jsplugin.ExchangedToken
	err := s.pool.With(ctx, version.Source, func(client *runner.Client) error {
		session := s.providerCredentialSession(client, version, account, base, config)
		values, err := client.Typed(ctx, session.fetch, "CallExchangeKey", session.credential())
		if err != nil || len(values) != 2 {
			// Vendor/plugin messages may echo keys or tokens. Do not forward
			// them to logs, account errors or the downstream API.
			return &newAPIProviderCredentialError{}
		}
		if string(values[1]) != "null" {
			var upstream jsplugin.UpstreamError
			if json.Unmarshal(values[1], &upstream) != nil || upstream.Status < 400 || upstream.Status > 599 {
				return &newAPIProviderCredentialError{}
			}
			return &newAPIProviderCredentialError{status: upstream.Status}
		}
		if json.Unmarshal(values[0], &result) != nil {
			return &newAPIProviderCredentialError{}
		}
		return nil
	})
	if err != nil {
		var credentialErr *newAPIProviderCredentialError
		if errors.As(err, &credentialErr) {
			return jsplugin.ExchangedToken{}, credentialErr
		}
		return jsplugin.ExchangedToken{}, &newAPIProviderCredentialError{}
	}
	return result, nil
}

func (s *NewAPIPluginService) providerCredentialAccount(ctx context.Context, account *Account) (*Account, error) {
	copy := *account
	if account.ProxyID != nil && (account.Proxy == nil || account.Proxy.ID != *account.ProxyID) {
		if s.proxyRepo == nil {
			return nil, errors.New("provider account proxy is unavailable")
		}
		proxy, err := s.proxyRepo.GetByID(ctx, *account.ProxyID)
		if err != nil || proxy == nil {
			return nil, errors.New("provider account proxy is unavailable")
		}
		copy.Proxy = proxy
	}
	return &copy, nil
}

func (s *NewAPIPluginService) sameProviderCredential(account *Account, base string, config json.RawMessage, previous []*Account) bool {
	if len(previous) == 0 || previous[0] == nil {
		return false
	}
	old := previous[0]
	if old.Type != account.Type || old.Platform != account.Platform || old.NewAPIPluginVersionID() != account.NewAPIPluginVersionID() || old.GetCredential("api_key") != account.GetCredential("api_key") || old.GetCredential("base_url") != base || !reflect.DeepEqual(old.ProxyID, account.ProxyID) {
		return false
	}
	plain := "{}"
	if sealed := old.GetCredential(newAPIPluginConfigEncryptedKey); sealed != "" {
		var err error
		plain, err = s.encryptor.Decrypt(sealed)
		if err != nil {
			return false
		}
	}
	var before, after any
	return json.Unmarshal([]byte(plain), &before) == nil && json.Unmarshal(config, &after) == nil && reflect.DeepEqual(before, after)
}

func (s *NewAPIPluginService) checkProviderKey(ctx context.Context, client *runner.Client, version *NewAPIPluginVersion, account *Account, base string, config json.RawMessage) error {
	if !slices.Contains(client.Descriptor.Hooks, "checkKey") {
		return nil
	}
	if s.gateway == nil || s.gateway.httpUpstream == nil {
		return errors.New("插件凭据校验服务不可用")
	}
	if _, err := s.gateway.validateUpstreamBaseURL(base); err != nil {
		return errors.New("插件凭据校验的上游地址无效")
	}
	var err error
	account, err = s.providerCredentialAccount(ctx, account)
	if err != nil {
		return errors.New("插件凭据校验的账号代理不可用")
	}
	session := s.providerCredentialSession(client, version, account, base, config)
	values, err := client.Typed(ctx, session.fetch, "CallCheckKey", base, config, account.GetCredential("api_key"))
	if err != nil || len(values) != 1 {
		return errors.New("插件 Key 校验失败，请检查上游地址、凭据与插件配置")
	}
	var message jsplugin.LocalizedText
	// LocalizedText marshals a nil success value as {}, including over the
	// typed worker boundary. Both null and an empty object mean success.
	if json.Unmarshal(values[0], &message) != nil || len(message) > 0 {
		return errors.New("插件拒绝该 Key，请检查上游地址、凭据与插件配置")
	}
	return nil
}

type newAPIProviderReadCloser struct {
	io.Reader
	io.Closer
}

func (s *NewAPIPluginService) providerCredentialWasRejected(ctx context.Context, exchange *newAPIProviderExchange, resp *http.Response) bool {
	if resp.StatusCode == http.StatusUnauthorized {
		return true
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 || !exchange.credentialRejected {
		return false
	}
	prefix, err := io.ReadAll(io.LimitReader(resp.Body, jsplugin.MaxRejectedBodyBytes))
	resp.Body = newAPIProviderReadCloser{Reader: io.MultiReader(bytes.NewReader(prefix), resp.Body), Closer: resp.Body}
	if err != nil {
		return false
	}
	hookCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var rejected bool
	err = s.pool.With(hookCtx, exchange.source, func(client *runner.Client) error {
		values, err := client.Typed(hookCtx, nil, "CallCredentialRejected", exchange.hookCtx, resp.StatusCode, pluginResponseHeaders(resp.Header), string(prefix))
		if err != nil || len(values) != 1 || json.Unmarshal(values[0], &rejected) != nil {
			return errors.New("provider rejection check failed")
		}
		return nil
	})
	return err == nil && rejected
}
