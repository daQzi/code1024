package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"github.com/Wei-Shaw/sub2api/newapi-engine/protocolconv"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

// Close the actual transport body on cancellation, including while reading
// a credential-rejection prefix or a JSON handshake. Converted streams own
// their own cancellation hook after this function returns.
func (s *NewAPIPluginService) readProviderStep(ctx context.Context, exchange *newAPIProviderExchange, resp *http.Response) (bool, error) {
	body := resp.Body
	stop := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stop()
	rejected := s.providerCredentialWasRejected(ctx, exchange, resp)
	if exchange.convert {
		if err := s.convertProviderResponse(ctx, exchange, resp, rejected); err != nil {
			return rejected, err
		}
	}
	if exchange.protocol != nil && exchange.followup == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := s.convertProviderPresetResponse(ctx, exchange, resp); err != nil {
			return rejected, err
		}
	}
	return rejected || resp.StatusCode == http.StatusUnauthorized, ctx.Err()
}

const (
	// Match the fixed New API baseline: 32 follow-ups after the first request.
	newAPIProviderMaxFollowups = 32
	// This bounds request handshakes/polling only. Once the final stream is
	// selected its existing idle/read/write limits own its lifetime.
	newAPIProviderChainTimeout = 2 * time.Minute
	newAPIProviderMemory       = 16 << 20
)

// Immutable request-time facts shared by the chain. It never owns a Gin
// context, writer, mutable plugin registry entry or a previous HTTP response.
type newAPIProviderChainSpec struct {
	account     *Account
	version     *NewAPIPluginVersion
	base        string
	config      json.RawMessage
	path, model string
	stream      bool
	hostBody    []byte
	protocol    *protocolconv.Bridge
	modifier    *modelmodifier.Spec
	multipart   *newAPIProviderMultipartInput
}

func (s *NewAPIPluginService) buildProviderFollowup(ctx context.Context, spec *newAPIProviderChainSpec, step jsplugin.ConvertedRequest, state json.RawMessage) (*newAPIProviderExchange, error) {
	if spec == nil || step.WebSocket != nil || step.NativeResponse && step.Stream != nil && *step.Stream && !spec.stream {
		return nil, errors.New("provider chained transport is unsupported")
	}
	if len(state) > MaxRequestPluginBody {
		return nil, errors.New("provider chained body or state exceeds JSON limits")
	}
	wireBody, wireContentType, formOrFile, err := materializeProviderStepBody(step, spec.multipart)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	routes := spec.version.Meta.ChannelRelayConfig(spec.config)
	route, ok := routes.MatchPathForModel(spec.path, spec.model)
	if !ok {
		return nil, errors.New("provider chained route is unavailable")
	}
	exchange := &newAPIProviderExchange{
		source: spec.version.Source, convert: true, stream: spec.stream,
		nativeResponse: step.NativeResponse, hostBody: spec.hostBody, chain: spec,
		protocol:           spec.protocol,
		modifier:           spec.modifier,
		tokenKey:           newAPIProviderTokenKey(spec.version, spec.account, spec.base, spec.config),
		credentialRejected: slices.Contains(spec.version.Capabilities.Hooks, "credentialRejected"),
		hookCtx:            jsplugin.HookContext{BaseURL: spec.base, Path: spec.path, Config: spec.config, State: state},
	}
	if step.Stream != nil {
		exchange.readStream = *step.Stream
	}
	if spec.account.ProxyID != nil && spec.account.Proxy != nil {
		exchange.proxy = spec.account.Proxy.URL()
	}
	if spec.account.newAPIProviderCredential == "" && slices.Contains(spec.version.Capabilities.Hooks, "exchangeKey") {
		token, err := s.providerTokens.get(ctx, exchange.tokenKey, func(hookCtx context.Context) (jsplugin.ExchangedToken, error) {
			return s.exchangeProviderKey(hookCtx, spec.version, spec.account, spec.base, spec.config)
		})
		if err != nil {
			return nil, err
		}
		exchange.hookCtx.Token = token
		// Renewal retries precisely this step, with the same plugin state and
		// body. It must never replay the first (possibly paid) generation.
		exchange.renew = func(hookCtx context.Context) (*newAPIProviderExchange, error) {
			return s.buildProviderFollowup(hookCtx, spec, step, state)
		}
	}
	if spec.account.newAPIProviderCredential != "" && slices.Contains(spec.version.Capabilities.Hooks, "refresh") {
		exchange.renew = func(hookCtx context.Context) (*newAPIProviderExchange, error) {
			account, err := s.loadProviderLoginCredential(hookCtx, spec.account, spec.version, spec.base, spec.account.newAPIProviderCredential)
			if err != nil {
				return nil, err
			}
			copy := *spec
			copy.account = account
			return s.buildProviderFollowup(hookCtx, &copy, step, state)
		}
	}
	hookCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = s.pool.With(hookCtx, exchange.source, func(client *runner.Client) error {
		return s.buildProviderStepWithContentType(hookCtx, client, exchange, spec.account, spec.version, route, spec.model, wireBody, step, nil, wireContentType, formOrFile, false)
	})
	return exchange, err
}

func waitProviderFollowup(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	// The typed decoder already validates this; preserve the bound for host
	// callers too, so direct calls cannot accidentally create a long wait.
	if delay > 10*time.Second {
		return errors.New("provider chained delay exceeds limit")
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
