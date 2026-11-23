package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

const newAPIPluginBalanceTimeout = 45 * time.Second

// A live upstream observation, not a local wallet entry. Querying it never
// updates account health, credit, billing, routing or the saved connection.
type NewAPIPluginBalance struct {
	AccountID       int64                  `json:"account_id"`
	PluginVersionID int64                  `json:"plugin_version_id"`
	PluginKey       string                 `json:"plugin_key"`
	PluginVersion   string                 `json:"plugin_version"`
	Balance         jsplugin.BalanceResult `json:"balance"`
	FetchedAt       time.Time              `json:"fetched_at"`
}

// QueryNewAPIPluginBalance uses the account's saved revision and credentials.
// It shares ingress memory, account concurrency, worker and token bounds with
// ordinary calls. No background work, new cache or polling loop is created.
func (s *NewAPIPluginService) QueryNewAPIPluginBalance(ctx context.Context, accountID int64) (*NewAPIPluginBalance, error) {
	if accountID <= 0 {
		return nil, pluginInputError("账号 ID 无效")
	}
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, newAPIProviderMemory); err != nil {
		return nil, err
	}
	if s == nil || s.gateway == nil || s.gateway.accountRepo == nil || s.gateway.httpUpstream == nil {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "PLUGIN_BALANCE_UNAVAILABLE", "插件余额服务暂不可用")
	}
	ctx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(ctx)), newAPIPluginBalanceTimeout)
	defer cancel()
	account, version, base, err := s.savedPluginManagementAccount(ctx, accountID, true)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(version.Capabilities.Hooks, "buildBalanceRequest") || !slices.Contains(version.Capabilities.Hooks, "parseBalance") {
		return nil, pluginInputError("该固定插件版本未启用，或未提供可用的余额查询钩子")
	}
	if s.gateway.concurrencyService == nil {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "PLUGIN_BALANCE_UNAVAILABLE", "账号并发服务暂不可用")
	}
	slot, err := s.gateway.concurrencyService.AcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil || slot == nil || !slot.Acquired {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "PLUGIN_BALANCE_BUSY", "账号正在使用中，请稍后重试")
	}
	defer slot.ReleaseFunc()
	result, err := s.queryNewAPIPluginBalance(ctx, account, version, base)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, infraerrors.New(http.StatusGatewayTimeout, "PLUGIN_BALANCE_TIMEOUT", "上游余额查询超时")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, pkghttputil.ErrRequestBodyMemoryBudget) {
			return nil, err
		}
		// Plugin/vendor errors may contain keys, tokens or private responses.
		return nil, infraerrors.New(http.StatusBadGateway, "PLUGIN_BALANCE_QUERY_FAILED", "上游余额查询失败，请检查插件、连接和凭据")
	}
	return &NewAPIPluginBalance{AccountID: account.ID, PluginVersionID: version.ID, PluginKey: version.Key, PluginVersion: version.Version, Balance: result, FetchedAt: time.Now().UTC()}, nil
}

func (s *NewAPIPluginService) queryNewAPIPluginBalance(ctx context.Context, account *Account, version *NewAPIPluginVersion, base string) (jsplugin.BalanceResult, error) {
	var zero jsplugin.BalanceResult
	account, err := s.loadProviderLoginCredential(ctx, account, version, base, "")
	if err != nil {
		return zero, err
	}
	config := json.RawMessage(`{}`)
	if sealed := account.GetCredential(newAPIPluginConfigEncryptedKey); sealed != "" {
		plain, err := s.encryptor.Decrypt(sealed)
		if err != nil {
			return zero, errors.New("cannot decrypt balance config")
		}
		config = json.RawMessage(plain)
	}
	var step jsplugin.ConvertedRequest
	hookCtx, hookCancel := context.WithTimeout(ctx, 5*time.Second)
	err = s.pool.With(hookCtx, version.Source, func(client *runner.Client) error {
		if !slices.Contains(client.Descriptor.Hooks, "buildBalanceRequest") || !slices.Contains(client.Descriptor.Hooks, "parseBalance") {
			return errors.New("balance hooks unavailable")
		}
		var err error
		config, err = client.ValidateConfig(hookCtx, config, false)
		return err
	})
	hookCancel()
	if err != nil {
		return zero, err
	}
	credential := providerCredentialContext(account, base, config, nil)
	tokenKey := newAPIProviderTokenKey(version, account, base, config)
	loadToken := func() error {
		if account.newAPIProviderCredential != "" || !slices.Contains(version.Capabilities.Hooks, "exchangeKey") {
			return nil
		}
		token, err := s.providerTokens.get(ctx, tokenKey, func(callCtx context.Context) (jsplugin.ExchangedToken, error) {
			return s.exchangeProviderKey(callCtx, version, account, base, config)
		})
		credential.Token = token
		return err
	}
	if err := loadToken(); err != nil {
		return zero, err
	}
	hookCtx, hookCancel = context.WithTimeout(ctx, 5*time.Second)
	err = s.pool.With(hookCtx, version.Source, func(client *runner.Client) error {
		values, err := client.Typed(hookCtx, nil, "CallBuildBalanceRequest", credential)
		if err != nil || len(values) != 1 {
			return errors.New("balance request hook failed")
		}
		return json.Unmarshal(values[0], &step)
	})
	hookCancel()
	if err != nil {
		return zero, err
	}
	state, renewed := step.State, false
	for followups := 0; ; {
		req, err := s.buildNewAPIPluginManagementRequest(ctx, version, credential, step, nil, account)
		if err != nil {
			return zero, err
		}
		proxy := ""
		if account.ProxyID != nil && account.Proxy != nil {
			proxy = account.Proxy.URL()
		}
		resp, err := s.gateway.doOpenAIUpstream(req, proxy, account)
		if err != nil {
			return zero, err
		}
		raw, readErr := readNewAPIPluginBalanceBody(ctx, resp)
		if readErr != nil {
			return zero, readErr
		}
		if resp.StatusCode == http.StatusUnauthorized && !renewed && providerCredentialCanRenew(version, account) {
			renewed = true
			if account.newAPIProviderCredential != "" {
				account, err = s.loadProviderLoginCredential(ctx, account, version, base, account.newAPIProviderCredential)
				if err != nil {
					return zero, err
				}
				credential = providerCredentialContext(account, base, config, nil)
			}
			s.providerTokens.invalidate(tokenKey, credential.Token)
			if err := loadToken(); err != nil {
				return zero, err
			}
			continue // Retry exactly this balance step, never the first one.
		}
		var parsed jsplugin.BalanceStep
		hookCtx, hookCancel = context.WithTimeout(ctx, 5*time.Second)
		err = s.pool.With(hookCtx, version.Source, func(client *runner.Client) error {
			values, err := client.Typed(hookCtx, nil, "CallParseBalance", credential, state, jsplugin.UpstreamResponse{Status: resp.StatusCode, Headers: pluginResponseHeaders(resp.Header), Body: raw})
			if err != nil || len(values) != 1 {
				return errors.New("balance response hook failed")
			}
			return json.Unmarshal(values[0], &parsed)
		})
		hookCancel()
		if err != nil {
			return zero, err
		}
		if parsed.Next == nil {
			if parsed.Balance == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return zero, errors.New("balance was not provided")
			}
			return *parsed.Balance, nil
		}
		if followups >= newAPIProviderMaxFollowups || len(parsed.State) > MaxRequestPluginBody {
			return zero, errors.New("balance chain exceeds limit")
		}
		if err := waitProviderFollowup(ctx, parsed.Next.Delay); err != nil {
			return zero, err
		}
		step, state = *parsed.Next, parsed.State
		followups++
	}
}

func (s *NewAPIPluginService) buildNewAPIPluginBalanceRequest(ctx context.Context, version *NewAPIPluginVersion, credential jsplugin.CredentialContext, step jsplugin.ConvertedRequest) (*http.Request, error) {
	return s.buildNewAPIPluginManagementRequest(ctx, version, credential, step, nil)
}

// A management request does not run relay preparation or request transforms.
// Model discovery opts into its explicit relay auth; balances supply their own.
func (s *NewAPIPluginService) buildNewAPIPluginManagementRequest(ctx context.Context, version *NewAPIPluginVersion, credential jsplugin.CredentialContext, step jsplugin.ConvertedRequest, route *contract.AdvancedCustomRoute, accounts ...*Account) (*http.Request, error) {
	if step.Form != nil || step.File != "" || step.WebSocket != nil || step.Stream != nil || step.NativeResponse || len(step.Body) > MaxRequestPluginBody || len(step.State) > MaxRequestPluginBody || step.Path == "" && step.URL == "" {
		return nil, errors.New("unsupported balance transport or size")
	}
	target := step.URL
	if target == "" {
		target = step.Path
	}
	target, err := newAPIProviderURL(credential.BaseURL, target, "")
	if err != nil {
		return nil, err
	}
	method := step.Method
	if method == "" {
		method = http.MethodGet
		if step.Body != nil {
			method = http.MethodPost
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(step.Body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if step.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if step.ContentType != "" {
		if _, _, err := mime.ParseMediaType(step.ContentType); err != nil {
			return nil, errors.New("invalid balance content type")
		}
		req.Header.Set("Content-Type", step.ContentType)
	}
	for name, value := range step.Headers {
		if strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
			return nil, errors.New("balance content type uses the contentType member")
		}
		if route != nil && newAPIProviderProtectedHeader(*route, name) {
			return nil, errors.New("management step changed a protected header")
		}
		req.Header.Set(name, value)
	}
	loginAuth := len(accounts) > 0 && applyProviderLoginAuth(req, accounts[0])
	if route != nil && !loginAuth {
		if route.Auth == nil {
			req.Header.Set("Authorization", "Bearer "+credential.APIKey)
		} else {
			value := strings.ReplaceAll(route.Auth.Value, "{api_key}", credential.APIKey)
			switch strings.TrimSpace(route.Auth.Type) {
			case contract.AdvancedCustomAuthTypeNone:
			case contract.AdvancedCustomAuthTypeHeader:
				req.Header.Set(route.Auth.Name, value)
			case contract.AdvancedCustomAuthTypeQuery:
				query := req.URL.Query()
				query.Set(route.Auth.Name, value)
				req.URL.RawQuery = query.Encode()
			default:
				return nil, errors.New("unsupported management authentication")
			}
		}
	}
	hookCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = s.pool.With(hookCtx, version.Source, func(client *runner.Client) error {
		if slices.Contains(client.Descriptor.Hooks, "signRequest") {
			digest := sha256.Sum256(step.Body)
			values, err := client.Typed(hookCtx, nil, "CallSignRequest", credential, req.Method, req.URL.String(), pluginResponseHeaders(req.Header), hex.EncodeToString(digest[:]))
			if err != nil || len(values) != 1 {
				return errors.New("balance signing failed")
			}
			var signed jsplugin.SignResult
			if json.Unmarshal(values[0], &signed) != nil {
				return errors.New("invalid balance signature")
			}
			for name, value := range signed.Headers {
				req.Header.Set(name, value)
			}
			query := req.URL.Query()
			for name, value := range signed.Query {
				query.Set(name, value)
			}
			req.URL.RawQuery = query.Encode()
		}
		hosts := append(slices.Clone(client.Meta.AllowedHosts), client.Meta.ChannelConfig.URLHosts(credential.Config)...)
		if err := jsplugin.ValidateRequestURL(req.URL.String(), credential.BaseURL, hosts); err != nil {
			return err
		}
		if _, err := s.gateway.validateOutboundURL(req.URL.String()); err != nil || req.URL.User != nil || req.URL.Fragment != "" || len(req.URL.String()) > 8192 {
			return errors.New("invalid balance target")
		}
		return jsplugin.ValidateRequestHeaders(pluginResponseHeaders(req.Header))
	})
	return req, err
}

func readNewAPIPluginBalanceBody(ctx context.Context, resp *http.Response) ([]byte, error) {
	body := resp.Body
	stop := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stop()
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, MaxRequestPluginBody+1))
	if err != nil || len(raw) > MaxRequestPluginBody {
		return nil, errors.New("balance response read failed or exceeds limit")
	}
	return raw, ctx.Err()
}
