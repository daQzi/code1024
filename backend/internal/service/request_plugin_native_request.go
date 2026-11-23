package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type nativeRequestPluginKey struct{}
type nativeRequestPluginAppliedKey struct{}

// Handler-owned, synchronous state. The plan pins source/config, and each
// native retry starts from the existing gateway's body, never a previous
// plugin output. No Gin context, writer or account credentials are retained.
type nativeRequestPluginAttempt struct {
	ctx             context.Context
	accountID       int64
	attempt         int
	requiresAttempt bool
	finalBody       []byte
	format          string
	model           string
	wireFormat      string
	wireReasoning   string
}

func nativeRequestStagePath(format, model string) string {
	if format == "gemini" {
		return "/v1beta/models/" + model + ":generateContent"
	}
	return "/v1/messages"
}

func guardUnsupportedNativeProtocolRequest(ctx context.Context, c *gin.Context, account *Account) error {
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	if pinned.plan == nil || account == nil || !slices.ContainsFunc(pinned.plan.attemptItems(account.ID, requestPluginChannelType(account, false)), pinned.plan.hasAttemptStage) {
		return nil
	}
	if pinned.plan.service == nil {
		return writeProviderRequestPluginFailure(c, &RequestPluginFailure{Class: "unavailable"})
	}
	return writeProviderRequestPluginFailure(c, unsupportedNativeRequestPluginAttempt(ctx, account, &nativeRequestPluginAttempt{attempt: max(0, pinned.plan.nextAttempt-1)}))
}

// The native gateway still owns mapping, conversion and credentials. This
// synchronous scope runs request once, and its builders run the late stages
// on a fresh wire body for every real HTTP attempt.
func beginNativeProtocolRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, model, format string) (context.Context, []byte, func(*error), error) {
	noop := func(*error) {}
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	if account == nil || pinned.plan == nil || !slices.ContainsFunc(pinned.plan.attemptItems(account.ID, requestPluginChannelType(account, false)), pinned.plan.hasAttemptStage) {
		return ctx, body, noop, nil
	}
	if pinned.plan.service == nil {
		return ctx, nil, noop, writeRequestPluginFailure(c, &RequestPluginFailure{Class: "unavailable"}, format)
	}
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	ctx = context.WithValue(ctx, requestPluginProviderBodyKey{}, body)
	attempt := &nativeRequestPluginAttempt{ctx: ctx, accountID: account.ID, attempt: max(0, pinned.plan.nextAttempt-1), format: format, model: model}
	for _, item := range pinned.plan.attemptItems(account.ID, requestPluginChannelType(account, false)) {
		attempt.requiresAttempt = attempt.requiresAttempt || slices.ContainsFunc(item.Stages, func(stage string) bool {
			return stage == "outbound" || stage == "egress" || stage == "ingress" && pinned.plan.lateIngressItem(item)
		})
	}
	ctx = context.WithValue(ctx, nativeRequestPluginKey{}, attempt)
	deferReasoning := format == "claude" && (account.Platform == PlatformGemini || account.Platform == PlatformAntigravity && strings.HasPrefix(model, "gemini-"))
	out, err := pinned.plan.service.relayRequestPluginStages(ctx, account, nativeRequestStagePath(format, model), model, body, contract.AdvancedCustomRoute{}, requestPluginStageRun{attempt: attempt.attempt, stages: []string{"request"}, format: format, protected: nativeProtocolRequestProtectedHeader, deferReasoning: deferReasoning})
	if err != nil {
		release()
		return ctx, nil, noop, writeRequestPluginFailure(c, err, format)
	}
	if deferReasoning || format == "gemini" {
		attempt.wireReasoning = out.ReasoningEffort
	}
	originalRequest := c.Request
	c.Request = c.Request.WithContext(ctx)
	finish := func(retErr *error) {
		defer release()
		defer func() { c.Request = originalRequest }()
		var failure *RequestPluginFailure
		if errors.As(*retErr, &failure) && !c.Writer.Written() {
			*retErr = writeRequestPluginFailure(c, failure, format)
		}
	}
	return ctx, out.Body, finish, nil
}

func nativeProtocolRequestOutbound(ctx context.Context, account *Account, body []byte, passthrough bool) (RequestPluginOutcome, error) {
	return nativeProtocolRequestOutboundAs(ctx, account, body, "claude", "", passthrough)
}

func nativeProtocolRequestOutboundAs(ctx context.Context, account *Account, body []byte, format, upstreamModel string, passthrough bool) (RequestPluginOutcome, error) {
	out := RequestPluginOutcome{Body: body}
	attempt, _ := ctx.Value(nativeRequestPluginKey{}).(*nativeRequestPluginAttempt)
	if attempt == nil || attempt.format == "" || !attempt.requiresAttempt {
		return out, nil
	}
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	if account.ID != attempt.accountID || pinned.plan == nil || pinned.plan.service == nil {
		return out, &RequestPluginFailure{Class: "identity"}
	}
	model := attempt.model
	if upstreamModel != "" {
		model = upstreamModel
	}
	if actual := strings.TrimSpace(gjson.GetBytes(body, "model").String()); format == "claude" && actual != "" {
		model = actual
	}
	setNativeReplyUpstreamModel(ctx, model)
	guard := nativeProtocolRequestBodyGuard
	if format == "gemini" {
		guard = nativeGeminiRequestBodyGuard
	}
	out, err := pinned.plan.service.relayRequestPluginStages(attempt.ctx, account, nativeRequestStagePath(format, model), model, body, contract.AdvancedCustomRoute{}, requestPluginStageRun{
		attempt: attempt.attempt, stages: []string{"ingress", "outbound", "egress"}, format: format, protected: nativeProtocolRequestProtectedHeader, passthrough: passthrough,
		bodyGuard: guard,
	})
	if err == nil {
		attempt.finalBody, attempt.wireFormat = out.Body, format
	}
	return out, err
}

// Run on the native builder's final request, without exposing its credentials
// to JS. Code Assist envelopes remain host-owned; only their inner Gemini
// request is inspected. Each retry still builds from the gateway's own body.
func processNativeGeminiHTTPRequest(ctx context.Context, account *Account, req *http.Request, model string) error {
	attempt, _ := ctx.Value(nativeRequestPluginKey{}).(*nativeRequestPluginAttempt)
	if attempt == nil || attempt.format == "" || !attempt.requiresAttempt && attempt.wireReasoning == "" {
		return nil
	}
	if req.Body == nil || req.ContentLength > MaxRequestPluginBody {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return &RequestPluginFailure{Class: "unsupported_body"}
	}
	temp, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := pkghttputil.ReserveRequestBodyMemory(temp, 3*MaxRequestPluginBody); err != nil {
		_ = req.Body.Close()
		return &RequestPluginFailure{Class: "capacity"}
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, MaxRequestPluginBody+1))
	_ = req.Body.Close()
	if err != nil || len(raw) > MaxRequestPluginBody || !gjson.ValidBytes(raw) || !newAPIProviderUnambiguousJSON(gjson.ParseBytes(raw), 0) {
		return &RequestPluginFailure{Class: "unsupported_body"}
	}
	body := raw
	wrapped := strings.Contains(req.URL.Path, "/v1internal:")
	if wrapped {
		inner := gjson.GetBytes(raw, "request")
		if !inner.IsObject() || gjson.GetBytes(raw, "model").String() != model {
			return &RequestPluginFailure{Class: "identity"}
		}
		body = []byte(inner.Raw)
	}
	if attempt.wireReasoning != "" {
		body, err = requestpatch.SetGeminiReasoning(body, model, attempt.wireReasoning)
		if err != nil {
			return &RequestPluginFailure{Class: "invalid_effect"}
		}
	}
	out, err := nativeProtocolRequestOutboundAs(ctx, account, body, "gemini", model, false)
	if err != nil {
		return err
	}
	attempt.finalBody, attempt.wireFormat = out.Body, "gemini"
	final := out.Body
	if wrapped {
		final, err = sjson.SetRawBytes(raw, "request", final)
		if err != nil || len(final) > MaxRequestPluginBody {
			return &RequestPluginFailure{Class: "unsupported_body"}
		}
	}
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, int64(len(final))); err != nil {
		return &RequestPluginFailure{Class: "capacity"}
	}
	req.Body = io.NopCloser(bytes.NewReader(final))
	req.ContentLength = int64(len(final))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(final)), nil }
	applyNativeProtocolRequestHeaders(req, out.Headers)
	setNativeReplyUpstreamModel(ctx, model)
	return nil
}

func recordNativeRequestReasoning(ctx context.Context, result *ForwardResult, body []byte, format string) {
	attempt, _ := ctx.Value(nativeRequestPluginKey{}).(*nativeRequestPluginAttempt)
	if result == nil || attempt == nil || attempt.format == "" {
		return
	}
	if attempt.finalBody != nil {
		body, format = attempt.finalBody, attempt.wireFormat
	}
	var effort string
	var explicit bool
	if format == "gemini" {
		// Gemini billing tiers use explicit levels, not inferred budget buckets.
		// Clear any pre-plugin level when the actual wire no longer contains it.
		result.ReasoningEffort = extractGeminiReasoningEffortFromBody(body)
		return
	} else {
		effort, explicit = requestpatch.ClaudeReasoningEffort(body)
	}
	if explicit && effort != "" {
		result.ReasoningEffort = &effort
	}
}

func nativeGeminiRequestBodyGuard(before, after []byte) bool {
	if !requestpatch.UnambiguousGeminiReasoning(after) || !nativeProtocolRequestBodyGuard(before, after) {
		return false
	}
	// Image permissions and prices have already been selected. Preserve every
	// generation option except reasoning and ordinary sampling controls, also
	// protecting future vendor pricing fields and alternate JSON spellings.
	controls := func(body []byte) map[string]any {
		out := map[string]any{}
		for _, path := range []string{"generationConfig", "generation_config"} {
			gjson.GetBytes(body, path).ForEach(func(key, value gjson.Result) bool {
				if !slices.Contains([]string{"thinkingConfig", "thinking_config", "temperature", "topP", "top_p", "topK", "top_k", "stopSequences", "stop_sequences"}, key.Str) {
					out[path+"."+key.Str] = value.Value()
				}
				return true
			})
		}
		for _, path := range []string{"cachedContent", "cached_content"} {
			if value := gjson.GetBytes(body, path); value.Exists() {
				out[path] = value.Value()
			}
		}
		return out
	}
	return reflect.DeepEqual(controls(before), controls(after))
}

func applyNativeProtocolRequestHeaders(req *http.Request, headers map[string]string) {
	if attempt, _ := req.Context().Value(nativeRequestPluginKey{}).(*nativeRequestPluginAttempt); attempt != nil && attempt.format != "" {
		applyNativeRequestPluginHeaders(req, headers)
	}
}

func nativeProtocolRequestBodyGuard(before, after []byte) bool {
	if !requestpatch.UnambiguousClaudeReasoning(after) {
		return false
	}
	// These native fields select billing, credentials, response mode or host
	// protocol envelopes. They cannot change after routing and admission.
	fields := []string{"speed", "anthropic_version", "anthropic_beta", "project", "project_id", "request", "metadata.user_id"}
	for _, path := range fields {
		a, b := gjson.GetBytes(before, path), gjson.GetBytes(after, path)
		if a.Exists() != b.Exists() || !reflect.DeepEqual(a.Value(), b.Value()) {
			return false
		}
	}
	spellings := func(body []byte) map[string]any {
		out := map[string]any{}
		gjson.ParseBytes(body).ForEach(func(key, value gjson.Result) bool {
			for _, field := range fields {
				root, _, _ := strings.Cut(field, ".")
				if strings.EqualFold(key.Str, root) && key.Str != root {
					out[key.Str] = value.Value()
				}
			}
			return true
		})
		gjson.GetBytes(body, "metadata").ForEach(func(key, value gjson.Result) bool {
			if strings.EqualFold(key.Str, "user_id") && key.Str != "user_id" {
				out["metadata."+key.Str] = value.Value()
			}
			return true
		})
		return out
	}
	return reflect.DeepEqual(spellings(before), spellings(after))
}

func (s *OpenAIGatewayService) forwardNativeWithRequestPlugins(ctx context.Context, c *gin.Context, account *Account, body []byte, path, defaultMappedModel string, forward func(context.Context, []byte) (*OpenAIForwardResult, error)) (result *OpenAIForwardResult, retErr error) {
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	items := pinned.plan.attemptItems(account.ID, requestPluginChannelType(account, false))
	if !slices.ContainsFunc(items, pinned.plan.hasAttemptStage) {
		return forward(ctx, body)
	}
	if s.newAPIPlugins == nil {
		return nil, writeProviderRequestPluginFailure(c, &RequestPluginFailure{Class: "unavailable"})
	}
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	clientBody := body
	mpForm := requestPluginMultipartFromContext(ctx)
	if mpForm != nil {
		clientBody = mpForm.View
	}
	ctx = context.WithValue(ctx, requestPluginProviderBodyKey{}, clientBody)
	attempt := &nativeRequestPluginAttempt{ctx: ctx, accountID: account.ID, attempt: max(0, pinned.plan.nextAttempt-1)}
	for _, item := range items {
		attempt.requiresAttempt = attempt.requiresAttempt || slices.ContainsFunc(item.Stages, func(stage string) bool {
			return stage == "outbound" || stage == "egress" || stage == "ingress" && pinned.plan.lateIngressItem(item)
		})
	}
	ctx = context.WithValue(ctx, nativeRequestPluginKey{}, attempt)
	model := nativeRequestPluginUpstreamModel(account, clientBody, path, defaultMappedModel)
	out, err := s.newAPIPlugins.relayRequestPluginStages(ctx, account, path, model, clientBody, contract.AdvancedCustomRoute{}, requestPluginStageRun{attempt: attempt.attempt, stages: []string{"request"}, protected: nativeRequestPluginProtectedHeader, multipart: mpForm})
	if err != nil {
		return nil, writeProviderRequestPluginFailure(c, err)
	}
	originalRequest := c.Request
	c.Request = c.Request.WithContext(ctx)
	defer func() {
		c.Request = originalRequest
		if result != nil && !result.ImageEndpoint && attempt.finalBody != nil {
			// Outbound can change reasoning after the native converter measured
			// it. Billing must observe what was actually sent, as for Provider.
			effort := extractOpenAIReasoningEffortFromBody(attempt.finalBody, result.UpstreamModel, result.BillingModel, result.Model)
			result.ReasoningEffort = ApplyThinkingEnabledFallback(effort, attempt.finalBody, result.UpstreamModel)
		}
		var failure *RequestPluginFailure
		if errors.As(retErr, &failure) && !c.Writer.Written() {
			retErr = writeProviderRequestPluginFailure(c, failure)
		}
	}()
	if mpForm != nil {
		return forward(ctx, body)
	}
	return forward(ctx, out.Body)
}

func nativeRequestPluginProtectedHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(name, "x-codex-") || strings.HasPrefix(name, "chatgpt-") || slices.Contains([]string{
		"x-api-key", "x-goog-api-key", "openai-organization", "openai-project", "session_id", "conversation_id", "originator", "version",
	}, name)
}

func nativeProtocolRequestProtectedHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return nativeRequestPluginProtectedHeader(name) || strings.HasPrefix(name, "anthropic-") || strings.HasPrefix(name, "x-amz") || strings.HasPrefix(name, "x-goog-") || strings.HasPrefix(name, "x-stainless-") || slices.Contains([]string{"x-claude-code-session-id", "x-app", "x-client-request-id", "user-agent"}, name)
}

// Called on the native gateway's final protocol body, before HTTP creation.
// Existing converters and account routing remain the sole protocol owners.
func (s *OpenAIGatewayService) nativeRequestPluginOutbound(ctx context.Context, account *Account, path string, body []byte, passthrough ...bool) (RequestPluginOutcome, error) {
	out := RequestPluginOutcome{Body: body}
	attempt, _ := ctx.Value(nativeRequestPluginKey{}).(*nativeRequestPluginAttempt)
	if attempt == nil || !attempt.requiresAttempt || attempt.accountID != account.ID {
		return out, nil
	}
	if isOpenAIImagesSelfBuiltRequest(ctx) {
		// The Images adapter has already run on the final native/Responses
		// body before token resolution. The shared Responses builder must not
		// run those policies a second time or apply text-only host defaults.
		if attempt.finalBody == nil || !bytes.Equal(body, attempt.finalBody) {
			return out, &RequestPluginFailure{Class: "identity"}
		}
		return out, nil
	}
	model := gjson.GetBytes(body, "model").String()
	out, err := s.newAPIPlugins.relayRequestPluginStages(attempt.ctx, account, path, model, body, contract.AdvancedCustomRoute{}, requestPluginStageRun{
		attempt: attempt.attempt, stages: []string{"ingress", "outbound", "egress"}, protected: nativeRequestPluginProtectedHeader, passthrough: len(passthrough) > 0 && passthrough[0],
		bodyGuard: nativeRequestPluginBodyGuard(path),
	})
	if err == nil {
		attempt.finalBody = out.Body
	}
	return out, err
}

func applyNativeRequestPluginHeaders(req *http.Request, headers map[string]string) {
	attempt, _ := req.Context().Value(nativeRequestPluginKey{}).(*nativeRequestPluginAttempt)
	if attempt == nil || !attempt.requiresAttempt {
		return
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	*req = *req.WithContext(context.WithValue(req.Context(), nativeRequestPluginAppliedKey{}, attempt))
}

func guardNativeRequestPluginAttempt(req *http.Request, account *Account) error {
	attempt, _ := req.Context().Value(nativeRequestPluginKey{}).(*nativeRequestPluginAttempt)
	if attempt != nil && attempt.requiresAttempt && (attempt.accountID != account.ID || req.Context().Value(nativeRequestPluginAppliedKey{}) != attempt) {
		return unsupportedNativeRequestPluginAttempt(req.Context(), account, attempt)
	}
	return nil
}

func unsupportedNativeRequestPluginAttempt(ctx context.Context, account *Account, attempt *nativeRequestPluginAttempt) error {
	// An unintegrated native protocol must fail before sending, rather than
	// silently bypass an administrator's required attempt stages.
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	out := RequestPluginOutcome{}
	for _, item := range pinned.plan.attemptItems(account.ID, requestPluginChannelType(account, false)) {
		for _, stage := range []string{"request", "ingress", "outbound", "egress"} {
			if slices.Contains(item.Stages, stage) && (stage != "ingress" || pinned.plan.lateIngressItem(item)) {
				out.Records = append(out.Records, RequestPluginRecord{InstanceID: item.ID, VersionID: item.PluginVersionID, Stage: stage, Action: "error", ErrorClass: "unsupported_protocol", AccountID: account.ID, Attempt: attempt.attempt})
				break
			}
		}
	}
	pinned.plan.appendReportRecords(out.Records)
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := pinned.plan.service.RecordRequestPluginRun(auditCtx, pinned.plan.call, out, false); err != nil {
		return &RequestPluginFailure{Class: "audit"}
	}
	return &RequestPluginFailure{Class: "unsupported_protocol"}
}
