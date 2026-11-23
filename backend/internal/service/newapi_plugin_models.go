// SPDX-License-Identifier: AGPL-3.0-or-later
// The model-list management contract follows QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f, providerplugin/management.go.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/tidwall/gjson"
)

type NewAPIPluginModels struct {
	AccountID       int64     `json:"account_id"`
	PluginVersionID int64     `json:"plugin_version_id"`
	Models          []string  `json:"models"`
	FetchedAt       time.Time `json:"fetched_at"`
}

// QueryNewAPIPluginModels invokes only an explicitly declared /v1/models
// management route. Discovery cannot mutate mapping, capability or price.
func (s *NewAPIPluginService) QueryNewAPIPluginModels(ctx context.Context, id int64) (*NewAPIPluginModels, error) {
	if id <= 0 {
		return nil, pluginInputError("账号 ID 无效")
	}
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, newAPIProviderMemory); err != nil {
		return nil, err
	}
	if s == nil || s.gateway == nil || s.gateway.accountRepo == nil || s.gateway.httpUpstream == nil || s.gateway.concurrencyService == nil {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "PLUGIN_MODELS_UNAVAILABLE", "插件模型发现服务暂不可用")
	}
	ctx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(ctx)), 45*time.Second)
	defer cancel()
	account, version, base, err := s.savedPluginManagementAccount(ctx, id, true)
	if err != nil {
		return nil, err
	}
	rawRoute, exists := version.Meta.ModelListRoute()
	if !exists || rawRoute.PassThroughBodyEnabled || !rawRoute.IsNative() && strings.TrimSpace(rawRoute.Converter) != jsplugin.ProviderConverter {
		return nil, pluginInputError("固定插件版本没有已支持的 /v1/models 模型发现路由")
	}
	slot, err := s.gateway.concurrencyService.AcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil || slot == nil || !slot.Acquired {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "PLUGIN_MODELS_BUSY", "账号正在使用中，请稍后重试")
	}
	defer slot.ReleaseFunc()
	models, err := s.queryNewAPIPluginModels(ctx, account, version, base, !rawRoute.IsNative())
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return nil, infraerrors.New(http.StatusGatewayTimeout, "PLUGIN_MODELS_TIMEOUT", "上游模型列表查询超时")
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, pkghttputil.ErrRequestBodyMemoryBudget) {
			return nil, err
		}
		return nil, infraerrors.New(http.StatusBadGateway, "PLUGIN_MODELS_QUERY_FAILED", "上游模型列表查询失败，请检查插件、连接和凭据")
	}
	return &NewAPIPluginModels{AccountID: account.ID, PluginVersionID: version.ID, Models: models, FetchedAt: time.Now().UTC()}, nil
}

func (s *NewAPIPluginService) queryNewAPIPluginModels(ctx context.Context, account *Account, version *NewAPIPluginVersion, base string, converted bool) ([]string, error) {
	account, err := s.loadProviderLoginCredential(ctx, account, version, base, "")
	if err != nil {
		return nil, err
	}
	config := json.RawMessage(`{}`)
	if sealed := account.GetCredential(newAPIPluginConfigEncryptedKey); sealed != "" {
		plain, err := s.encryptor.Decrypt(sealed)
		if err != nil {
			return nil, errors.New("cannot decrypt management config")
		}
		config = json.RawMessage(plain)
	}
	hookCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = s.pool.With(hookCtx, version.Source, func(client *runner.Client) error {
		var err error
		config, err = client.ValidateConfig(hookCtx, config, false)
		if err == nil && converted && !slices.Contains(client.Descriptor.Hooks, "convertResponse") {
			return errors.New("model response converter unavailable")
		}
		return err
	})
	cancel()
	if err != nil {
		return nil, err
	}
	routes := version.Meta.ChannelRelayConfig(config)
	route, exists := routes.ModelListRoute()
	if !exists {
		return nil, errors.New("model list route unavailable")
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
		return nil, err
	}
	step, renewed := jsplugin.ConvertedRequest{Path: route.UpstreamPath}, false
	var state json.RawMessage
	for followups := 0; ; {
		if step.Path == "" && step.URL == "" {
			step.Path = route.UpstreamPath
		}
		req, err := s.buildNewAPIPluginManagementRequest(ctx, version, credential, step, &route, account)
		if err != nil {
			return nil, err
		}
		proxy := ""
		if account.ProxyID != nil && account.Proxy != nil {
			proxy = account.Proxy.URL()
		}
		resp, err := s.gateway.doOpenAIUpstream(req, proxy, account)
		if err != nil {
			return nil, err
		}
		raw, err := readNewAPIPluginBalanceBody(ctx, resp)
		if err != nil {
			return nil, err
		}
		canRenew := !renewed && providerCredentialCanRenew(version, account)
		var result jsplugin.ConvertedResponse
		var upstreamError *jsplugin.UpstreamError
		if resp.StatusCode != http.StatusUnauthorized && converted {
			hookCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = s.pool.With(hookCtx, version.Source, func(client *runner.Client) error {
				values, callErr := client.Typed(hookCtx, nil, "CallConvertResponse", jsplugin.HookContext{BaseURL: base, Path: contract.AdvancedCustomModelListPath, Config: config, Token: credential.Token, State: state}, resp.StatusCode, pluginResponseHeaders(resp.Header), raw)
				if callErr != nil || len(values) != 2 {
					return errors.New("model list conversion failed")
				}
				if json.Unmarshal(values[0], &result) != nil || json.Unmarshal(values[1], &upstreamError) != nil {
					return errors.New("invalid model conversion")
				}
				return nil
			})
			cancel()
			if err != nil {
				return nil, err
			}
		}
		if resp.StatusCode == http.StatusUnauthorized || upstreamError != nil && upstreamError.Status == http.StatusUnauthorized {
			if !canRenew {
				return nil, errors.New("model discovery credential rejected")
			}
			renewed = true
			if account.newAPIProviderCredential != "" {
				account, err = s.loadProviderLoginCredential(ctx, account, version, base, account.newAPIProviderCredential)
				if err != nil {
					return nil, err
				}
				credential = providerCredentialContext(account, base, config, nil)
			}
			s.providerTokens.invalidate(tokenKey, credential.Token)
			if err := loadToken(); err != nil {
				return nil, err
			}
			continue
		}
		if upstreamError != nil {
			return nil, errors.New("model upstream error")
		}
		if result.Next != nil {
			if followups >= newAPIProviderMaxFollowups || len(result.State) > MaxRequestPluginBody || len(result.Tools) > 0 {
				return nil, errors.New("model discovery chain exceeds limits")
			}
			if err := waitProviderFollowup(ctx, result.Next.Delay); err != nil {
				return nil, err
			}
			step, state = *result.Next, result.State
			followups++
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, errors.New("model upstream status failed")
		}
		if converted {
			if len(result.Tools) > 0 || !newAPIProviderJSONContentType(result.ContentType) {
				return nil, errors.New("unsupported model conversion result")
			}
			raw = result.Body
		}
		return pluginManagementModelIDs(raw)
	}
}

func pluginManagementModelIDs(raw []byte) ([]string, error) {
	if len(raw) > MaxRequestPluginBody || !gjson.ValidBytes(raw) || !newAPIProviderUnambiguousJSON(gjson.ParseBytes(raw), 0) {
		return nil, errors.New("model catalog exceeds limit or has ambiguous JSON")
	}
	_, entries, err := modelCatalogEntries(raw, "data")
	if err != nil || len(entries) > 4096 {
		return nil, errors.New("model catalog must contain at most 4096 models")
	}
	models := make([]string, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		var model struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(entry, &model) != nil || model.ID == "" || len(model.ID) > 256 || strings.TrimSpace(model.ID) != model.ID || !utf8.ValidString(model.ID) || strings.ContainsAny(model.ID, "*\u0000") || strings.ContainsFunc(model.ID, unicode.IsControl) {
			return nil, errors.New("invalid model catalog entry")
		}
		if !seen[model.ID] {
			models = append(models, model.ID)
			seen[model.ID] = true
		}
	}
	return models, nil
}
