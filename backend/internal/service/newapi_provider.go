package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"github.com/Wei-Shaw/sub2api/newapi-engine/protocolconv"
	"github.com/Wei-Shaw/sub2api/newapi-engine/provider"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const newAPIPluginProviderKey = "newapi_plugin_provider"

const (
	newAPIProviderChat                = "/v1/chat/completions"
	newAPIProviderResponses           = "/v1/responses"
	newAPIProviderMessages            = "/v1/messages"
	newAPIProviderEmbeddings          = "/v1/embeddings"
	newAPIProviderImagesGenerations   = "/v1/images/generations"
	newAPIProviderImagesEdits         = "/v1/images/edits"
	newAPIProviderAudioTranscriptions = "/v1/audio/transcriptions"
	newAPIProviderAudioTranslations   = "/v1/audio/translations"
	newAPIProviderAudioSpeech         = "/v1/audio/speech"
)

func newAPIProviderFormat(path string) string {
	if _, _, ok := providerGeminiPath(path); ok {
		return "gemini"
	}
	switch path {
	case newAPIProviderResponses:
		return "openai_responses"
	case newAPIProviderMessages:
		return "claude"
	default:
		return "openai"
	}
}

func writeNewAPIProviderError(c *gin.Context, path string, status int, errType, code, message string) {
	if _, _, ok := providerGeminiPath(path); ok {
		c.JSON(status, gin.H{"error": gin.H{"code": status, "status": providerGeminiErrorStatus(status), "message": message}})
		return
	}
	if path == newAPIProviderMessages {
		writeAnthropicError(c, status, errType, message)
		return
	}
	errObj := gin.H{"type": errType, "message": message}
	if code != "" {
		errObj["code"] = code
	}
	c.JSON(status, gin.H{"error": errObj})
}

type newAPIProviderScopeKey struct{}

// WithNewAPIProviderRouting admits providers only at implemented HTTP ingress
// paths. A shared Chat capability alone must not admit them to Messages or WS.
func WithNewAPIProviderRouting(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, newAPIProviderScopeKey{}, path)
}

type newAPIProviderBinding struct {
	Routes []contract.AdvancedCustomRoute `json:"routes"`
	Models []string                       `json:"models"`
}

// Never drop required conversion/auth hooks and silently use the native wire.
func pluginProviderBinding(meta jsplugin.Meta, descriptor runner.Descriptor) newAPIProviderBinding {
	binding := newAPIProviderBinding{Models: slices.Clone(meta.Models)}
	if !descriptor.Provider {
		return binding
	}
	for _, route := range meta.Relay {
		if newAPIProviderRouteSupported(route) {
			// Keep unsupported routes too, so an earlier model-specific route
			// cannot be bypassed through a later native catch-all.
			binding.Routes = slices.Clone(meta.Relay)
			break
		}
	}
	return binding
}

func (a *Account) newAPIProviderBinding() newAPIProviderBinding {
	var binding newAPIProviderBinding
	if a == nil || !a.HasNewAPIPlugin() || a.Type != AccountTypeAPIKey || a.Platform != PlatformOpenAI {
		return binding
	}
	raw, _ := json.Marshal(a.Credentials[newAPIPluginProviderKey])
	_ = json.Unmarshal(raw, &binding)
	return binding
}

func (b newAPIProviderBinding) supports(path, model string) bool {
	cfg := contract.AdvancedCustomConfig{Routes: b.Routes}
	route, ok := cfg.MatchPathForModel(path, model)
	return ok && newAPIProviderRouteSupported(route)
}

func newAPIProviderRouteSupported(route contract.AdvancedCustomRoute) bool {
	converter, _ := jsplugin.SplitProviderConverter(route.Converter)
	if route.PassThroughBodyEnabled {
		return false
	}
	if _, _, ok := providerGeminiPath(route.IncomingPath); ok {
		return converter == "" || converter == "none" || protocolconv.Supported(converter)
	}
	switch route.IncomingPath {
	case newAPIProviderChat, newAPIProviderResponses, newAPIProviderMessages:
		return converter == "" || converter == "none" || protocolconv.Supported(converter)
	case newAPIProviderEmbeddings,
		newAPIProviderImagesGenerations,
		newAPIProviderImagesEdits,
		newAPIProviderAudioTranscriptions,
		newAPIProviderAudioTranslations,
		newAPIProviderAudioSpeech:
		return converter == "" || converter == "none" // Optional plugin conversion is split above.
	}
	return false
}

func (a *Account) SupportsNewAPIProviderModel(path, model string) bool {
	if a == nil || !a.isModelMappingSupported(model) && !a.isModelMappingSupported(modelmodifier.Base(model)) {
		return false
	}
	binding := a.newAPIProviderBinding()
	mapped := modelmodifier.Base(a.mappedNewAPIProviderModel(model))
	if len(a.GetModelMapping()) == 0 && !slices.Contains(binding.Models, mapped) && !slices.ContainsFunc(binding.Routes, func(r contract.AdvancedCustomRoute) bool { return slices.Contains(r.Models, mapped) }) {
		return false // Regex/catch-all routes require explicit account models.
	}
	return binding.supports(path, mapped)
}

func (a *Account) mappedNewAPIProviderModel(model string) string {
	mapped, matched := a.ResolveMappedModel(model)
	if !matched && modelmodifier.Base(model) != model {
		return a.GetMappedModel(modelmodifier.Base(model))
	}
	return mapped
}

func (a *Account) supportsNewAPIPluginScope(ctx context.Context, model string) bool {
	if allowed, _ := ctx.Value(newAPIPluginRoutingKey{}).(bool); allowed {
		protocol, _ := ctx.Value(generationProtocolKey{}).(string)
		if strings.HasPrefix(protocol, GenerationProtocolNativePrefix) {
			if match, ok := generationRouteMatchFromContext(ctx); ok && match.VersionID != a.NewAPIPluginVersionID() {
				return false
			}
		}
		if protocol == GenerationProtocolOpenAIResponses {
			if mode := generationResponsesModeFromContext(ctx); mode != "" && !a.SupportsNewAPIResponsesTaskMode(mode) {
				return false
			}
		}
		return a.supportsPluginTaskModel(protocol, model)
	}
	path, _ := ctx.Value(newAPIProviderScopeKey{}).(string)
	if nativeWS, _ := ctx.Value(newAPIResponsesSocketRoutingKey{}).(bool); nativeWS {
		return a.supportsNewAPIResponsesWebSocket(model)
	}
	return path != "" && a.SupportsNewAPIProviderModel(path, model)
}

func (a *Account) NewAPIProviderModels() []string {
	binding := a.newAPIProviderBinding()
	candidates := slices.Clone(binding.Models)
	for _, route := range binding.Routes {
		for _, model := range route.Models {
			if len(model) < 3 || model[:3] != "re:" {
				candidates = append(candidates, model)
			}
		}
	}
	for alias := range a.GetModelMapping() {
		candidates = append(candidates, alias)
	}
	result := []string{}
	for _, model := range candidates {
		if model != "" && !strings.Contains(model, "*") && !strings.HasPrefix(model, "re:") && !slices.Contains(result, model) && a.SupportsAnyNewAPIProviderModel(model) {
			result = append(result, model)
		}
	}
	slices.Sort(result)
	return result
}

func (a *Account) SupportsAnyNewAPIProviderModel(model string) bool {
	return a.SupportsNewAPIProviderModel(newAPIProviderChat, model) ||
		a.SupportsNewAPIProviderModel(newAPIProviderResponses, model) ||
		a.SupportsNewAPIProviderModel(newAPIProviderMessages, model) ||
		a.SupportsNewAPIProviderModel(newAPIProviderEmbeddings, model) ||
		a.SupportsNewAPIProviderModel(newAPIProviderImagesGenerations, model) ||
		a.SupportsNewAPIProviderModel(newAPIProviderImagesEdits, model) ||
		a.SupportsNewAPIProviderModel(newAPIProviderAudioTranscriptions, model) ||
		a.SupportsNewAPIProviderModel(newAPIProviderAudioTranslations, model) ||
		a.SupportsNewAPIProviderModel(newAPIProviderAudioSpeech, model) ||
		a.SupportsNewAPIProviderGeminiModel(model)
}

// Build in the worker lane, release it before the potentially long response.
// Provider streams must not monopolize the four JavaScript worker processes.
func (s *NewAPIPluginService) buildProviderRequest(ctx context.Context, account *Account, path, model string, body []byte) (*newAPIProviderExchange, error) {
	return s.buildProviderRequestWithMultipart(ctx, account, path, model, body, nil)
}

func (s *NewAPIPluginService) buildProviderRequestWithMultipart(ctx context.Context, account *Account, path, model string, body []byte, mp *newAPIProviderMultipartInput) (*newAPIProviderExchange, error) {
	version, err := s.repo.Get(ctx, account.NewAPIPluginVersionID())
	if err != nil {
		return nil, errors.New("cannot load Provider version")
	}
	if !version.Enabled || !AssessPluginHost(version.Meta, version.Capabilities).Runnable || account.Type != AccountTypeAPIKey || account.Platform != PlatformOpenAI {
		return nil, errors.New("Provider version is disabled or account type is unsupported")
	}
	binding := pluginProviderBinding(version.Meta, version.Capabilities)
	if !binding.supports(path, model) {
		return nil, errors.New("Provider does not support this endpoint and model")
	}
	if s.gateway == nil || s.gateway.httpUpstream == nil {
		return nil, errors.New("Provider HTTP service is unavailable")
	}
	account, err = s.providerCredentialAccount(ctx, account)
	if err != nil {
		return nil, err
	}
	base, err := s.gateway.validateUpstreamBaseURL(account.GetCredential("base_url"))
	if err != nil {
		return nil, errors.New("Provider base URL is invalid")
	}
	key := account.GetCredential("api_key")
	if _, credential := jsplugin.ParseCredential(key); credential {
		return nil, errors.New("Provider login credentials are not yet supported")
	}
	config := json.RawMessage(`{}`)
	if sealed := account.GetCredential(newAPIPluginConfigEncryptedKey); sealed != "" {
		plain, err := s.encryptor.Decrypt(sealed)
		if err != nil {
			return nil, errors.New("cannot decrypt Provider configuration")
		}
		config = json.RawMessage(plain)
	}
	configCtx, configCancel := context.WithTimeout(ctx, 5*time.Second)
	err = s.pool.With(configCtx, version.Source, func(client *runner.Client) error {
		config, err = client.ValidateConfig(configCtx, config, false)
		return err
	})
	configCancel()
	if err != nil {
		return nil, errors.New("Provider configuration is invalid")
	}
	// Retries retain the original connection, source and validated config.
	// A concurrent account edit or plugin switch only affects new requests.
	pinnedAccount, pinnedVersion := *account, *version
	pinnedAccount.Credentials = maps.Clone(account.Credentials)
	if account.ProxyID != nil {
		id := *account.ProxyID
		pinnedAccount.ProxyID = &id
	}
	if account.Proxy != nil {
		proxy := *account.Proxy
		pinnedAccount.Proxy = &proxy
	}
	resolved, err := s.loadProviderLoginCredential(ctx, &pinnedAccount, &pinnedVersion, base, "")
	if err != nil {
		return nil, err
	}
	return s.buildPinnedProviderRequest(ctx, resolved, &pinnedVersion, base, config, path, model, body, mp)
}

func (s *NewAPIPluginService) buildPinnedProviderRequest(ctx context.Context, account *Account, version *NewAPIPluginVersion, base string, config json.RawMessage, path, model string, body []byte, mp *newAPIProviderMultipartInput) (*newAPIProviderExchange, error) {
	tokenKey := newAPIProviderTokenKey(version, account, base, config)
	var token json.RawMessage
	var err error
	if account.newAPIProviderCredential == "" && slices.Contains(version.Capabilities.Hooks, "exchangeKey") {
		token, err = s.providerTokens.get(ctx, tokenKey, func(hookCtx context.Context) (jsplugin.ExchangedToken, error) {
			return s.exchangeProviderKey(hookCtx, version, account, base, config)
		})
		if err != nil {
			return nil, err
		}
	}
	_, geminiStream, gemini := providerGeminiPath(path)
	stream := mp == nil && (gemini && geminiStream || !gemini && gjson.GetBytes(body, "stream").Bool())
	exchange := &newAPIProviderExchange{source: version.Source, stream: stream, readStream: stream}
	if account.ProxyID != nil && account.Proxy != nil {
		exchange.proxy = account.Proxy.URL()
	}
	exchange.tokenKey = tokenKey
	exchange.credentialRejected = slices.Contains(version.Capabilities.Hooks, "credentialRejected")
	if account.newAPIProviderCredential == "" && slices.Contains(version.Capabilities.Hooks, "exchangeKey") || account.newAPIProviderCredential != "" && slices.Contains(version.Capabilities.Hooks, "refresh") {
		originalBody := bytes.Clone(body)
		originalMP := mp.clone()
		exchange.renew = func(hookCtx context.Context) (*newAPIProviderExchange, error) {
			resolved, err := s.loadProviderLoginCredential(hookCtx, account, version, base, account.newAPIProviderCredential)
			if err != nil {
				return nil, err
			}
			return s.buildPinnedProviderRequest(hookCtx, resolved, version, base, config, path, model, originalBody, originalMP)
		}
	}
	routes := version.Meta.ChannelRelayConfig(config)
	route, ok := routes.MatchPathForModel(path, model)
	if !ok {
		return nil, errors.New("Provider route is unavailable")
	}
	if rawHeaders, socketHandshake := ctx.Value(newAPIResponsesHandshakeKey{}).(map[string]string); socketHandshake {
		if path != newAPIProviderResponses || !route.IsNative() || !newAPIProviderRouteSupported(route) {
			return nil, errors.New("Provider Responses WebSocket requires a native route")
		}
		exchange.hookCtx = jsplugin.HookContext{BaseURL: base, Path: path, Config: config, Token: token}
		var headers map[string]string
		for name, value := range rawHeaders {
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" || key == "authorization" || key == "host" || key == "connection" || key == "upgrade" || strings.HasPrefix(key, "sec-websocket-") || newAPIProviderProtectedHeader(route, name) {
				continue
			}
			if headers == nil {
				headers = make(map[string]string, len(rawHeaders))
			}
			headers[name] = value
		}
		step := jsplugin.ConvertedRequest{Method: http.MethodGet, Path: providerWebSocketHTTPURL(route.UpstreamPath)}
		stepCtx, stepCancel := context.WithTimeout(ctx, 5*time.Second)
		defer stepCancel()
		err := s.pool.With(stepCtx, version.Source, func(client *runner.Client) error {
			return s.buildProviderStep(stepCtx, client, exchange, account, version, route, model, nil, step, headers, false)
		})
		if err == nil && exchange.request != nil {
			exchange.request.Header.Del("Accept")
			exchange.request.Header.Del("Content-Type")
		}
		return exchange, err
	}
	processed, err := s.providerRequestPluginStages(ctx, account, path, model, body, route)
	if err != nil {
		return nil, err
	}
	if intent, ok := modelmodifier.Intent(ctx); ok {
		modifier := modelmodifier.Spec{Base: model, Thinking: true, Intent: intent}
		modifier, err = modifier.ReconcileForFormat(body, processed.Body, newAPIProviderFormat(path), processed.ReasoningSet)
		if err != nil {
			return nil, &RequestPluginFailure{Class: "invalid_effect"}
		}
		exchange.modifier = &modifier
		ctx = modifier.Context(ctx)
	}
	body = processed.Body
	exchange.hostBody = body
	if mp == nil {
		if converter := strings.TrimSpace(route.Converter); converter != "" && converter != "none" {
			exchange.protocol, body, err = protocolconv.New(ctx, converter, model, body, exchange.stream)
			if err != nil {
				return nil, err
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = s.pool.With(ctx, version.Source, func(client *runner.Client) error {
		// Resolve config URL placeholders with the pinned upstream contract.
		routes := version.Meta.ChannelRelayConfig(config)
		route, ok := routes.MatchPathForModel(path, model)
		original := contract.AdvancedCustomConfig{Routes: version.Meta.Relay}
		rawRoute, rawOK := original.MatchPathForModel(path, model)
		if !ok || !rawOK || !newAPIProviderRouteSupported(rawRoute) {
			return errors.New("Provider route requires an unsupported converter")
		}
		_, exchange.convert = jsplugin.SplitProviderConverter(rawRoute.Converter)
		exchange.hookCtx = jsplugin.HookContext{BaseURL: base, Path: path, Config: config, Token: token}
		// RawMessage.UnmarshalJSON reuses its buffer. Keep the host body out
		// of the conversion result so decoding cannot overwrite the pinned
		// request/effort facts (or the original body used by renewal).
		var step jsplugin.ConvertedRequest
		wireContentType := ""
		formOrFile := false
		if exchange.convert {
			convertInput := jsplugin.ConvertInput{Path: path, Body: body, Model: model, Stream: exchange.stream}
			if mp != nil {
				reqForm, _ := provider.BuildRequestForm(mp.fields, mp.files, model)
				convertInput = jsplugin.ConvertInput{Path: path, Form: reqForm, Model: model, Stream: false}
			}
			values, err := client.Typed(ctx, nil, "CallConvertRequest", exchange.hookCtx, convertInput)
			if err != nil {
				return errors.New("Provider request conversion failed")
			}
			if len(values) != 1 || json.Unmarshal(values[0], &step) != nil {
				return errors.New("invalid Provider converted request")
			}
			if step.NativeResponse && step.Stream != nil && *step.Stream && !exchange.stream {
				return errors.New("Provider conversion requires an unsupported transport or stream mode change")
			}
			if socket := step.WebSocket; socket != nil {
				exchange.socket = socket
				step.WebSocket = nil
				step.Method, step.Path, step.URL, step.Headers = http.MethodGet, socket.Path, providerWebSocketHTTPURL(socket.URL), socket.Headers
			}
			if len(step.State) > MaxRequestPluginBody {
				return errors.New("Provider conversion state exceeds limit")
			}
			var matErr error
			body, wireContentType, formOrFile, matErr = materializeProviderStepBody(step, mp)
			if matErr != nil {
				return matErr
			}
			exchange.hookCtx.State, exchange.nativeResponse = step.State, step.NativeResponse
			if step.Stream != nil {
				exchange.readStream = *step.Stream
			}
		} else if mp != nil {
			var passErr error
			body, wireContentType, passErr = mp.buildPassthrough(model)
			if passErr != nil {
				return passErr
			}
			formOrFile = true
		} else {
			step.Body = body
		}
		return s.buildProviderStepWithContentType(ctx, client, exchange, account, version, route, model, body, step, processed.Headers, wireContentType, formOrFile, true)
	})
	if err == nil && exchange.convert {
		exchange.chain = &newAPIProviderChainSpec{account: account, version: version, base: base, config: config, path: path, model: model, stream: exchange.stream, hostBody: exchange.hostBody, protocol: exchange.protocol, modifier: exchange.modifier, multipart: mp.clone()}
	}
	return exchange, err
}

// Each chained step builds and signs a fresh request. Only the first step
// receives transform and prepareRequest effects; later steps carry explicit
// plugin headers and state, never a previously signed request's headers.
func (s *NewAPIPluginService) buildProviderStep(ctx context.Context, client *runner.Client, exchange *newAPIProviderExchange, account *Account, version *NewAPIPluginVersion, route contract.AdvancedCustomRoute, model string, body []byte, step jsplugin.ConvertedRequest, headers map[string]string, first bool) error {
	return s.buildProviderStepWithContentType(ctx, client, exchange, account, version, route, model, body, step, headers, "", false, first)
}

func (s *NewAPIPluginService) buildProviderStepWithContentType(ctx context.Context, client *runner.Client, exchange *newAPIProviderExchange, account *Account, version *NewAPIPluginVersion, route contract.AdvancedCustomRoute, model string, body []byte, step jsplugin.ConvertedRequest, headers map[string]string, wireContentType string, formOrFile bool, first bool) error {
	base, config, token := exchange.hookCtx.BaseURL, exchange.hookCtx.Config, exchange.hookCtx.Token
	key := account.GetCredential("api_key")
	targetPath := route.UpstreamPath
	if step.URL != "" {
		targetPath = step.URL
	} else if step.Path != "" {
		targetPath = step.Path
	}
	target, err := newAPIProviderURL(base, targetPath, model)
	if err != nil {
		return err
	}
	method := step.Method
	if method == "" {
		method = http.MethodPost
		if !first && !step.HasBody() {
			method = http.MethodGet
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid Provider request")
	}
	if formOrFile && wireContentType != "" {
		req.Header.Set("Content-Type", wireContentType)
	} else if first || step.HasBody() || len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if exchange.readStream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if !formOrFile && step.ContentType != "" {
		if !newAPIProviderJSONContentType(step.ContentType) {
			return errors.New("Provider conversion requires JSON content type")
		}
		req.Header.Set("Content-Type", step.ContentType)
	} else if !formOrFile && wireContentType != "" {
		req.Header.Set("Content-Type", wireContentType)
	}
	if exchange.protocol.Claude() || (!exchange.convert && exchange.protocol == nil && exchange.hookCtx.Path == newAPIProviderMessages) {
		req.Header.Set("Anthropic-Version", "2023-06-01")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	for name, value := range step.Headers {
		if newAPIProviderProtectedHeader(route, name) {
			return errors.New("Provider conversion changed a protected header")
		}
		req.Header.Set(name, value)
	}
	if exchange.socket != nil {
		req.Header.Del("Content-Type")
		req.Header.Del("Accept")
	}
	if first && !formOrFile && slices.Contains(client.Descriptor.Hooks, "prepareRequest") && body != nil && gjson.ValidBytes(body) {
		body, err = prepareNewAPIProviderRequest(ctx, client, exchange.hookCtx, req, route, model, exchange.stream, body)
		if err != nil {
			return err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	if first && !exchange.convert && exchange.protocol == nil {
		if exchange.modifier != nil {
			modifier, err := exchange.modifier.ReconcileForFormat(exchange.hostBody, body, newAPIProviderFormat(exchange.hookCtx.Path), false)
			if err != nil {
				return errors.New("invalid Provider prepared reasoning controls")
			}
			exchange.modifier = &modifier
		}
		exchange.hostBody = body
	}
	// The preset selects Gemini's wire mode through its operation URL. Apply
	// this after prepareRequest's target override, before auth and signing.
	_, _, nativeGemini := providerGeminiPath(exchange.hookCtx.Path)
	if first && (exchange.protocol.Gemini() || nativeGemini && exchange.protocol == nil && !exchange.convert) && exchange.readStream {
		req.URL.Path = strings.Replace(req.URL.Path, ":generateContent", ":streamGenerateContent", 1)
		req.URL.RawPath = ""
		if strings.Contains(req.URL.Path, ":streamGenerateContent") {
			query := req.URL.Query()
			query.Set("alt", "sse")
			req.URL.RawQuery = query.Encode()
		}
	}
	// Apply auth after target overrides, so query credentials cannot be lost
	// when prepareRequest selects a different path. Hooks see business headers.
	if applyProviderLoginAuth(req, account) {
		// Persistent credential auth supersedes static relay auth.
	} else if route.Auth == nil {
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		value := strings.ReplaceAll(route.Auth.Value, "{api_key}", key)
		switch strings.TrimSpace(route.Auth.Type) {
		case contract.AdvancedCustomAuthTypeNone:
		case contract.AdvancedCustomAuthTypeHeader:
			req.Header.Set(route.Auth.Name, value)
		case contract.AdvancedCustomAuthTypeQuery:
			query := req.URL.Query()
			query.Set(route.Auth.Name, value)
			req.URL.RawQuery = query.Encode()
		default:
			return errors.New("unsupported Provider authentication")
		}
	}
	if slices.Contains(client.Descriptor.Hooks, "signRequest") {
		digest := sha256.Sum256(body)
		// signRequest cannot perform fetch, as required by the Provider API.
		values, err := client.Typed(ctx, nil, "CallSignRequest", providerCredentialContext(account, base, config, token), req.Method, req.URL.String(), pluginResponseHeaders(req.Header), hex.EncodeToString(digest[:]))
		if err != nil {
			return errors.New("Provider signing failed")
		}
		var signed jsplugin.SignResult
		if len(values) != 1 || json.Unmarshal(values[0], &signed) != nil {
			return errors.New("invalid Provider signature")
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
	hosts := append(slices.Clone(version.Meta.AllowedHosts), client.Meta.ChannelConfig.URLHosts(config)...)
	if err := jsplugin.ValidateRequestURL(req.URL.String(), base, hosts); err != nil {
		return errors.New("Provider target host is not allowed")
	}
	if _, err := s.gateway.validateOutboundURL(req.URL.String()); err != nil || req.URL.User != nil || req.URL.Fragment != "" || len(req.URL.String()) > 8192 {
		return errors.New("Provider target URL is invalid")
	}
	if err := jsplugin.ValidateRequestHeaders(pluginResponseHeaders(req.Header)); err != nil {
		return errors.New("invalid Provider request headers")
	}
	exchange.request = req
	return nil
}

// Same joining rule as New API advancedcustom: preserve the configured base
// path and replace its query with the route query. Never forward client query.
func newAPIProviderURL(base, target, model string) (string, error) {
	target = strings.ReplaceAll(target, "{model}", url.PathEscape(model))
	parsed, err := url.Parse(target)
	if err != nil || strings.HasPrefix(target, "//") {
		return "", errors.New("invalid Provider target")
	}
	if strings.HasPrefix(target, "/") {
		root, err := url.Parse(base)
		if err != nil {
			return "", errors.New("invalid Provider base URL")
		}
		root.Path = strings.TrimRight(root.Path, "/") + "/" + strings.TrimLeft(parsed.Path, "/")
		root.RawPath = ""
		root.RawQuery, root.Fragment = parsed.RawQuery, parsed.Fragment
		parsed = root
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("invalid Provider target URL")
	}
	return parsed.String(), nil
}

func validNewAPIProviderJSON(body []byte) bool {
	root := gjson.ParseBytes(body)
	if !gjson.ValidBytes(body) || !root.IsObject() || hasDuplicateJSONObjectKeys(root) {
		return false
	}
	valid := true
	root.ForEach(func(key, _ gjson.Result) bool {
		name := key.String()
		switch strings.ToLower(name) {
		case "model", "stream", "stream_options", "service_tier", "tools", "tool_choice", "modalities", "input", "messages", "thinking", "output_config", "max_tokens", "enable_search", "search_options":
			valid = name == strings.ToLower(name)
		}
		return valid
	})
	return valid
}

func (s *OpenAIGatewayService) forwardNewAPIProvider(ctx context.Context, c *gin.Context, account *Account, body []byte, path, defaultMappedModel string) (result *OpenAIForwardResult, retErr error) {
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	ctx = context.WithValue(ctx, requestPluginProviderBodyKey{}, body)
	start := time.Now()
	beginUpstreamResponseModelObservation(c)
	ClearActualOpenAIUpstreamEndpoint(c)
	geminiModel, geminiStream, gemini := providerGeminiPath(path)
	model := gjson.GetBytes(body, "model").String()
	if gemini {
		model = geminiModel
	}
	upstreamModel := resolveOpenAIForwardModel(account, model, defaultMappedModel)
	report := beginRelayReportAttempt(ctx, account, upstreamModel, true)
	defer func() { report.result(result) }()
	// These accounts cannot fall through to Codex/native protocol heuristics.
	if s.newAPIPlugins == nil || (!gemini && path != newAPIProviderChat && path != newAPIProviderResponses && path != newAPIProviderMessages && path != newAPIProviderEmbeddings) || isOpenAIResponsesCompactPath(c) {
		writeNewAPIProviderError(c, path, http.StatusServiceUnavailable, "plugin_unavailable", "", "Provider endpoint is not available")
		return nil, errors.New("Provider endpoint unavailable")
	}
	if len(body) > MaxRequestPluginBody {
		writeNewAPIProviderError(c, path, http.StatusRequestEntityTooLarge, "invalid_request_error", "", "Provider JSON request exceeds 1 MiB")
		return nil, errors.New("Provider request body exceeds limit")
	}
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, newAPIProviderMemory); err != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		c.Header("Retry-After", "1")
		writeNewAPIProviderError(c, path, http.StatusServiceUnavailable, "overloaded_error", "", "Provider conversion capacity is busy")
		return nil, err
	}
	if !validNewAPIProviderJSON(body) || model == "" || !account.SupportsNewAPIProviderModel(path, model) || !account.newAPIProviderBinding().supports(path, modelmodifier.Base(upstreamModel)) {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "", "Provider does not support this endpoint and model")
		return nil, errors.New("Provider model or endpoint mismatch")
	}
	modifiers, err := modelmodifier.Resolve(model, upstreamModel)
	if err != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalModelConfiguration)
		writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "invalid_model_modifier", modelmodifier.ErrInvalid.Error())
		return nil, err
	}
	if path == newAPIProviderEmbeddings && (modifiers.HasControls() || gjson.GetBytes(body, "stream").Bool()) {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "", "Embeddings do not accept generation modifiers or streaming")
		return nil, errors.New("Provider embeddings generation controls are unsupported")
	}
	body, err = modifiers.ApplyForFormat(body, newAPIProviderFormat(path))
	if err != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalModelConfiguration)
		writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "invalid_model_modifier", modelmodifier.ErrInvalid.Error())
		return nil, err
	}
	ctx = context.WithValue(modifiers.Context(ctx), requestPluginProviderBodyKey{}, body)
	upstreamModel = modifiers.Base
	stream := gemini && geminiStream || !gemini && gjson.GetBytes(body, "stream").Bool()
	if IsImageGenerationIntent(path, upstreamModel, body) || slices.ContainsFunc(gjson.GetBytes(body, "modalities").Array(), func(v gjson.Result) bool { return v.String() == "audio" }) {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "", "Provider relay currently supports text output only")
		return nil, errors.New("Provider generated media output is not yet supported")
	}
	if restriction := s.detectCodexClientRestriction(c, account, body); restriction.Enabled && !restriction.Matched {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		writeNewAPIProviderError(c, path, http.StatusForbidden, "permission_error", "", "This account only allows Codex official clients")
		return nil, errors.New("Provider account client restriction")
	}
	upstreamBody := body
	if !gemini {
		upstreamBody, err = sjson.SetBytes(body, "model", upstreamModel)
		if err != nil {
			return nil, err
		}
	}
	if path == newAPIProviderChat || path == newAPIProviderResponses {
		upstreamBody, err = s.applyOpenAIFastPolicyToBody(ctx, account, upstreamModel, upstreamBody)
		if err != nil {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			writeNewAPIProviderError(c, path, http.StatusForbidden, "permission_error", "", "Request is blocked by the service tier policy")
			return nil, err
		}
	}
	if stream && path == newAPIProviderChat {
		upstreamBody, err = ensureOpenAIChatStreamUsage(upstreamBody)
		if err != nil {
			return nil, err
		}
	}
	toolUsage, err := s.newAPIPlugins.providerToolUsage(ctx, getAPIKeyFromContext(c), modifiers.BillingNames()[0])
	if err != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalModelConfiguration)
		writeNewAPIProviderError(c, path, http.StatusServiceUnavailable, "server_error", "provider_tool_prices_unavailable", "Provider tool prices are unavailable")
		return nil, err
	}
	toolUsage.protocol = provider.NewProtocolTools(upstreamBody, toolUsage.lookup)
	if err := toolUsage.admitRequest(upstreamBody); err != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalModelConfiguration)
		writeNewAPIProviderError(c, path, http.StatusBadRequest, "invalid_request_error", "provider_tool_price_required", "Provider hosted tools require an explicit price")
		return nil, err
	}
	upstreamCtx, cancel := context.WithCancel(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(ctx)))
	defer cancel()
	// Bound JavaScript setup separately; the response keeps the client context.
	setupCtx, setupCancel := context.WithTimeout(upstreamCtx, newAPIProviderSetupTimeout)
	exchange, err := s.newAPIPlugins.buildProviderRequest(setupCtx, account, path, upstreamModel, upstreamBody)
	setupCancel()
	if err != nil {
		var processingErr *RequestPluginFailure
		if errors.As(err, &processingErr) {
			return nil, writeProviderRequestPluginFailure(c, processingErr)
		}
		var fastErr *OpenAIFastBlockedError
		if errors.As(err, &fastErr) {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			writeNewAPIProviderError(c, path, http.StatusForbidden, "permission_error", "", "Request is blocked by the service tier policy")
			return nil, fastErr
		}
		if errors.Is(err, pkghttputil.ErrRequestBodyMemoryBudget) {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			c.Header("Retry-After", "1")
			writeNewAPIProviderError(c, path, http.StatusServiceUnavailable, "overloaded_error", "", "Request-processing capacity is busy")
			return nil, err
		}
		var credentialErr *newAPIProviderCredentialError
		if errors.As(err, &credentialErr) {
			if credentialErr.status != 0 {
				s.handleOpenAIAccountUpstreamError(ctx, account, credentialErr.status, nil, []byte(`{"error":{"message":"Provider key exchange failed"}}`), upstreamModel)
			}
		} else {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		}
		writeNewAPIProviderError(c, path, http.StatusServiceUnavailable, "plugin_unavailable", "", "Provider request preparation is unavailable")
		return nil, fmt.Errorf("Provider setup: %w", err)
	}
	var reply *providerReplySession
	upstreamCtx, reply, err = s.newAPIPlugins.beginProviderReply(upstreamCtx, c, account, upstreamModel)
	if err != nil {
		return nil, writeProviderRequestPluginFailure(c, err)
	}
	if reply != nil {
		originalWriter, originalRequest := c.Writer, c.Request
		c.Writer, c.Request = reply.writer, c.Request.WithContext(upstreamCtx)
		defer func() {
			defer func() { c.Writer, c.Request = originalWriter, originalRequest }()
			if finishErr := reply.finish(ctx, c); finishErr != nil {
				// Keep any measured upstream usage even when a local policy
				// stopped the reply; the handler settles partial results.
				retErr = finishErr
			}
		}()
	}
	// The chain's two-minute budget includes handshakes, JSON reads, hooks,
	// renewal and delays. Stop the timer before delivering a final SSE stream;
	// its existing per-operation timeouts must not become a lifetime timeout.
	chainTimer := time.AfterFunc(newAPIProviderChainTimeout, cancel)
	defer chainTimer.Stop()
	var resp *http.Response
	var req *http.Request
	continuationStarted := false
	defer func() {
		if (continuationStarted || exchange.credentialReplayUnsafe) && retErr != nil {
			// A next step may follow a paid submit even before any client bytes
			// exist. Preserve account/error policy, but never let the handler
			// restart the entire chain on this or a different account.
			var retry *UpstreamFailoverError
			if errors.As(retErr, &retry) {
				stopped := *retry
				stopped.NextAccountAction = NextAccountStop
				stopped.RetryableOnSameAccount = false
				retErr = &stopped
			}
		}
	}()
	defer func() {
		if err := toolUsage.apply(result); err != nil {
			retErr = err
		}
	}()
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	credentialRejected, renewed := false, false
	for followups := 0; ; {
		// The entire chain and its single credential renewal share one count
		// projection. Counts never leak into another request or plugin state.
		exchange.tools = toolUsage
		req = exchange.request.WithContext(upstreamCtx)
		SetOpsUpstreamModel(c, upstreamModel)
		SetActualOpenAIUpstreamEndpoint(c, req.URL.Path)
		resp, err = s.doNewAPIProviderTransport(upstreamCtx, req, exchange, account)
		if err != nil {
			return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, path == newAPIProviderMessages)
		}
		credentialRejected, err = s.newAPIPlugins.readProviderStep(upstreamCtx, exchange, resp)
		if err != nil {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider response conversion failed")
			return nil, err
		}
		// A converted plugin error may reject credentials too. The one retry
		// belongs to the entire logical request, never to each poll in a chain.
		if credentialRejected {
			s.newAPIPlugins.providerTokens.invalidate(exchange.tokenKey, exchange.hookCtx.Token)
			if !renewed && exchange.renew != nil && !exchange.credentialReplayUnsafe {
				renewed = true
				s.readOpenAIUpstreamError(resp)
				renewCtx, renewCancel := context.WithTimeout(upstreamCtx, newAPIProviderSetupTimeout)
				replacement, renewErr := exchange.renew(renewCtx)
				renewCancel()
				if renewErr == nil {
					_ = resp.Body.Close()
					exchange = replacement
					if followups == 0 {
						if err := reply.admit(upstreamCtx); err != nil {
							return nil, writeProviderRequestPluginFailure(c, err)
						}
					}
					continue
				}
				var processingErr *RequestPluginFailure
				if errors.As(renewErr, &processingErr) {
					return nil, writeProviderRequestPluginFailure(c, processingErr)
				}
				// Preserve the original response if the single renewal failed.
			}
		}
		if exchange.followup == nil {
			break
		}
		continuationStarted = true
		if followups >= newAPIProviderMaxFollowups {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider exceeded its chained request limit")
			return nil, errors.New("Provider exceeded 32 follow-up requests")
		}
		_ = resp.Body.Close()
		if err := waitProviderFollowup(upstreamCtx, exchange.followup.Delay); err != nil {
			if ctx.Err() == nil {
				writeNewAPIProviderError(c, path, http.StatusGatewayTimeout, "upstream_error", "", "Provider chain time budget exceeded")
			}
			return nil, err
		}
		stepCtx, stepCancel := context.WithTimeout(upstreamCtx, newAPIProviderSetupTimeout)
		next, nextErr := s.newAPIPlugins.buildProviderFollowup(stepCtx, exchange.chain, *exchange.followup, exchange.followupState)
		stepCancel()
		if nextErr != nil {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider chained request preparation failed")
			return nil, nextErr
		}
		exchange = next
		followups++
	}
	if !chainTimer.Stop() {
		cancel()
		writeNewAPIProviderError(c, path, http.StatusGatewayTimeout, "upstream_error", "", "Provider chain time budget exceeded")
		return nil, context.DeadlineExceeded
	}
	if credentialRejected && resp.StatusCode != http.StatusUnauthorized {
		s.handleOpenAIAccountUpstreamError(ctx, account, http.StatusUnauthorized, nil, []byte(`{"error":{"message":"Provider credential was rejected"}}`), upstreamModel)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, message := s.readOpenAIUpstreamError(resp)
		if failover := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, responseBody, message, upstreamModel); failover != nil {
			return nil, failover
		}
		if resp.StatusCode < 400 {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider returned an unexpected redirect")
			return nil, errors.New("Provider redirect rejected")
		}
		if gemini {
			writeNewAPIProviderError(c, path, resp.StatusCode, "upstream_error", "", "Provider Gemini request failed")
			return nil, errors.New("Provider Gemini upstream rejected request")
		}
		if path == newAPIProviderMessages {
			return s.handleAnthropicErrorResponse(resp, c, account, upstreamModel)
		}
		return s.handleChatCompletionsErrorResponse(resp, c, account, upstreamModel)
	}
	if stream && !isEventStreamResponse(resp.Header) {
		writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider did not return an SSE stream")
		return nil, errors.New("Provider stream content type mismatch")
	}
	convertedStream, _ := resp.Body.(*newAPIProviderStream)
	var toolStream *newAPIProviderToolStream
	if stream && !gemini {
		toolStream = &newAPIProviderToolStream{ReadCloser: resp.Body, tools: toolUsage, path: path}
		resp.Body = toolStream
	}
	if !stream && !gemini {
		if path == newAPIProviderEmbeddings {
			return s.finishNewAPIProviderEmbeddings(ctx, c, account, resp, req, model, upstreamModel, start)
		}
		tooLarge := openAITooLargeError
		if path == newAPIProviderMessages {
			tooLarge = anthropicTooLargeError
		}
		responseBody, readErr := ReadUpstreamResponseBody(resp.Body, s.cfg, c, tooLarge)
		if readErr != nil {
			return nil, readErr
		}
		root := gjson.ParseBytes(responseBody)
		valid := gjson.ValidBytes(responseBody) && root.IsObject() && newAPIProviderUnambiguousJSON(root, 0) && root.Get("error").Type == gjson.Null
		switch path {
		case newAPIProviderChat:
			valid = valid && root.Get("choices").IsArray()
		case newAPIProviderMessages:
			valid = valid && root.Get("type").String() == "message" && root.Get("content").IsArray() && root.Get("stop_reason").String() != ""
		default:
			status := root.Get("status").String()
			valid = valid && root.Get("output").IsArray() && (status == "completed" || status == "incomplete")
		}
		if !valid {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider returned an invalid or failed response")
			return nil, errors.New("Provider response protocol mismatch")
		}
		if err := toolUsage.observe(path, responseBody, false); err != nil {
			writeNewAPIProviderError(c, path, http.StatusBadGateway, "upstream_error", "", "Provider tool usage could not be priced")
			return nil, err
		}
		resp.Body = io.NopCloser(bytes.NewReader(responseBody))
	}
	upstreamBody = exchange.hostBody
	tier := extractOpenAIServiceTierFromBody(upstreamBody)
	billingModel := upstreamModel
	if exchange.modifier != nil {
		billingModel = exchange.modifier.BillingNames()[0]
	}
	var effort *string
	if path == newAPIProviderMessages {
		effort = NormalizeClaudeOutputEffort(gjson.GetBytes(upstreamBody, "output_config.effort").String())
		effort = ApplyThinkingEnabledFallback(effort, upstreamBody, billingModel)
	} else {
		effort = extractOpenAIReasoningEffortFromBody(upstreamBody, upstreamModel, model)
	}
	if exchange.modifier != nil {
		if value := exchange.modifier.Effort(); value != "" {
			effort = &value
		}
	}
	switch {
	case gemini:
		result, err = s.finishNewAPIProviderGemini(upstreamCtx, c, resp, model, upstreamModel, billingModel, stream, start, toolUsage, path)
	case path == newAPIProviderChat:
		if stream {
			result, err = s.streamRawChatCompletions(c, resp, account, model, billingModel, upstreamModel, effort, tier, start, len(body))
		} else {
			result, err = s.bufferRawChatCompletions(c, resp, account, model, billingModel, upstreamModel, effort, tier, start)
		}
	case path == newAPIProviderMessages:
		if stream {
			result, err = s.handleNativeAnthropicStreamingResponse(upstreamCtx, resp, c, account, model, billingModel, upstreamModel, effort, start)
		} else {
			result, err = s.handleNativeAnthropicBufferedResponse(ctx, resp, c, account, model, billingModel, upstreamModel, effort, start)
		}
	default:
		result = &OpenAIForwardResult{RequestID: resp.Header.Get("x-request-id"), UpstreamHeaders: resp.Header, Model: model, BillingModel: billingModel, UpstreamModel: upstreamModel, Stream: stream, ServiceTier: tier, ReasoningEffort: effort}
		if stream {
			var output *openaiStreamingResult
			output, err = s.handleStreamingResponseWithReasoning(upstreamCtx, resp, c, account, start, model, upstreamModel, gjson.GetBytes(upstreamBody, "reasoning.effort").String())
			if output != nil {
				if output.usage != nil {
					result.Usage = *output.usage
				}
				result.ResponseID, result.FirstTokenMs = output.responseID, output.firstTokenMs
				result.ImageCount, result.ImageOutputSizes = output.imageCount, output.imageOutputSizes
			}
		} else {
			var output *openaiNonStreamingResult
			output, err = s.handleNonStreamingResponse(ctx, resp, c, account, model, upstreamModel)
			if output != nil {
				if output.usage != nil {
					result.Usage = *output.usage
				}
				result.ResponseID = output.responseID
				result.ImageCount, result.ImageOutputSizes = output.imageCount, output.imageOutputSizes
			}
		}
		result.Duration = time.Since(start)
		result.UpstreamResponseModel = observedUpstreamResponseModel(c)
		result.UpstreamResponseModelConflict = observedUpstreamResponseModelConflict(c)
		result.UpstreamResponseServiceTier = observedUpstreamResponseServiceTier(c)
	}
	if result != nil {
		result.UpstreamEndpoint = req.URL.Path
	}
	if converted := convertedStream; converted != nil && converted.failure != nil {
		if ctx.Err() != nil {
			// The native Chat parser can return partial billable usage without
			// an error after client cancellation. Preserve that billing contract
			// while preventing telemetry from calling the interrupted reply a
			// successfully delivered generation.
			if result != nil {
				result.ClientDisconnect = true
			}
		} else if err == nil {
			// A late hook failure must not be hidden by an earlier usage/terminal
			// event accepted by the shared OpenAI parser.
			err = newOpenAIUpstreamStreamReadError(converted.failure)
		}
	}
	if toolStream != nil && toolStream.failure != nil && err == nil {
		err = newOpenAIUpstreamStreamReadError(toolStream.failure)
	}
	return result, err
}
