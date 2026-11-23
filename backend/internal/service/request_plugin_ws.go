// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// Per-turn Responses WebSocket request/reply/report semantics follow
// QuantumNous/new-api relay/requestplugin/socket.go at
// 48f9df96ea7c68eae04a0f3687869f232f48929f.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/replyfilter"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type wsPluginHeadersKey struct{}

func withWSPluginExtraHeaders(ctx context.Context, headers map[string]string) context.Context {
	if len(headers) == 0 {
		return ctx
	}
	return context.WithValue(ctx, wsPluginHeadersKey{}, headers)
}

func applyWSPluginExtraHeaders(ctx context.Context, req *http.Request) {
	if req == nil || ctx == nil {
		return
	}
	headers, _ := ctx.Value(wsPluginHeadersKey{}).(map[string]string)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
}

// RequestPluginWSTurnReject represents a per-turn policy or modifier rejection
// on a Responses WebSocket. The session emits EventJSON() to the client and
// keeps the WebSocket open for subsequent turns.
type RequestPluginWSTurnReject struct {
	StatusCode int
	ErrorType  string
	Code       string
	Message    string
	ResponseID string
}

func (r *RequestPluginWSTurnReject) Error() string {
	if r == nil {
		return ""
	}
	return r.Message
}

// EventJSON encodes a Responses WebSocket error event for this turn rejection.
func (r *RequestPluginWSTurnReject) EventJSON() []byte {
	if r == nil {
		return nil
	}
	return BuildResponsesWSErrorEvent(r.StatusCode, r.ErrorType, r.Code, r.Message, r.ResponseID)
}

// BuildResponsesWSErrorEvent constructs a standard Responses WebSocket error
// frame without exposing private plugin diagnostics.
func BuildResponsesWSErrorEvent(status int, errType, code, message, responseID string) []byte {
	if status <= 0 {
		status = http.StatusBadRequest
	}
	if errType == "" {
		errType = "request_processing_error"
	}
	if code == "" {
		code = "request_processing_rejected"
	}
	payload := map[string]any{
		"type":   "error",
		"status": status,
		"error": map[string]any{
			"type":    errType,
			"code":    code,
			"message": message,
		},
	}
	if strings.TrimSpace(responseID) != "" {
		payload["response_id"] = strings.TrimSpace(responseID)
	}
	encoded, _ := json.Marshal(payload)
	return encoded
}

func wsTurnRejectFromFailure(err error) *RequestPluginWSTurnReject {
	var failure *RequestPluginFailure
	if errors.As(err, &failure) && failure != nil && failure.Rejected {
		return &RequestPluginWSTurnReject{
			StatusCode: http.StatusForbidden,
			ErrorType:  "request_processing_error",
			Code:       "request_processing_rejected",
			Message:    "Request rejected by request-processing policy",
		}
	}
	return &RequestPluginWSTurnReject{
		StatusCode: http.StatusServiceUnavailable,
		ErrorType:  "request_processing_error",
		Code:       "request_processing_failed",
		Message:    "Request-processing policy could not be applied",
	}
}

func wsTurnRejectFromReplyFailure(fail *replyfilter.Failure, responseID string) *RequestPluginWSTurnReject {
	if fail == nil {
		return nil
	}
	code := "response_processing_rejected"
	status := http.StatusForbidden
	if fail.Failed {
		code = "response_processing_failed"
		status = http.StatusServiceUnavailable
	}
	return &RequestPluginWSTurnReject{
		StatusCode: status,
		ErrorType:  "request_processing_error",
		Code:       code,
		Message:    fail.Error(),
		ResponseID: responseID,
	}
}

type wsReplySession struct {
	service       *NewAPIPluginService
	plan          *RequestPluginPlan
	items         []RequestPluginInstance
	accountID     int64
	attempt       int
	upstreamModel string
	filter        *replyfilter.SocketFilter
	releaseMem    func()
	finished      bool
	writeErrMu    sync.Mutex
	writeErr      error
}

// RequestPluginWSTurn owns the pinned per-turn Request Processing plan, reply
// filter, confirmed-ledger report capture, and @ model modifier state for one
// Responses WebSocket turn.
type RequestPluginWSTurn struct {
	service             *NewAPIPluginService
	plan                *RequestPluginPlan
	report              *RequestPluginHTTPReport
	clientBody          []byte
	clientModel         string
	modifier            modelmodifier.Spec
	modifierApplied     bool
	modifierAppliedBody []byte
	finalBody           []byte
	reply               *wsReplySession
	reportBound         bool
	reportFinished      bool
}

// NewAPIPlugins returns the attached New API plugin service, if configured.
func (s *OpenAIGatewayService) NewAPIPlugins() *NewAPIPluginService {
	if s == nil {
		return nil
	}
	return s.newAPIPlugins
}

// shouldUseManagedWSForPlugins checks whether a passthrough WS connection
// should execute via the managed WS v2 turn loop so per-turn Request Plugins
// or @ model modifiers run on every response.create turn.
func (s *OpenAIGatewayService) shouldUseManagedWSForPlugins(ctx context.Context, c *gin.Context, hooks *OpenAIWSIngressHooks, firstMessage []byte) bool {
	if hooks != nil && hooks.InitialPluginTurn != nil {
		return true
	}
	if s != nil && s.newAPIPlugins != nil {
		apiKey := getAPIKeyFromContext(c)
		if apiKey == nil && hooks != nil {
			apiKey = hooks.APIKey
		}
		if apiKey != nil && apiKey.GroupID != nil {
			if applicable, err := s.newAPIPlugins.RequestPluginsMayApply(ctx, *apiKey.GroupID); err != nil || applicable {
				return true
			}
		}
	}
	if model := strings.TrimSpace(gjson.GetBytes(firstMessage, "model").String()); model != "" {
		if spec, err := modelmodifier.Resolve(model, model); err != nil || spec.HasControls() || spec.Base != model {
			return true
		}
	}
	return false
}

// PrepareResponsesWSTurnIngress validates per-turn @ model modifiers, pins
// matching Request Processing instances for the turn, initializes per-turn
// report capture, and executes global/group early ingress hooks. A rejection
// returns *RequestPluginWSTurnReject without closing the WebSocket.
func (s *OpenAIGatewayService) PrepareResponsesWSTurnIngress(
	ctx context.Context,
	c *gin.Context,
	apiKey *APIKey,
	rawBody []byte,
	clientModel string,
) ([]byte, *RequestPluginWSTurn, *RequestPluginWSTurnReject) {
	clientModel = strings.TrimSpace(clientModel)
	if clientModel != "" {
		if _, err := modelmodifier.Resolve(clientModel, clientModel); err != nil {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalModelConfiguration)
			return nil, nil, &RequestPluginWSTurnReject{
				StatusCode: http.StatusBadRequest,
				ErrorType:  "invalid_request_error",
				Code:       "invalid_model_modifier",
				Message:    modelmodifier.ErrInvalid.Error(),
			}
		}
	}
	if s == nil || s.newAPIPlugins == nil || apiKey == nil || apiKey.GroupID == nil {
		return rawBody, nil, nil
	}
	applicable, err := s.newAPIPlugins.RequestPluginsMayApply(ctx, *apiKey.GroupID)
	if err != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		return nil, nil, &RequestPluginWSTurnReject{
			StatusCode: http.StatusServiceUnavailable,
			ErrorType:  "request_processing_error",
			Code:       "request_processing_failed",
			Message:    "Request-processing policy could not be applied",
		}
	}
	if !applicable {
		return rawBody, nil, nil
	}
	path := openAIResponsesEndpoint
	if c != nil && c.Request != nil && strings.TrimSpace(c.Request.URL.Path) != "" {
		path = c.Request.URL.Path
	}
	candidates := make([]string, 0, 4)
	addCandidate := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" && !slices.Contains(candidates, v) {
			candidates = append(candidates, v)
		}
	}
	addCandidate(clientModel)
	if clientModel != "" {
		addCandidate(modelmodifier.Base(clientModel))
		for _, b := range modelmodifier.BillingNames(clientModel) {
			addCandidate(b)
		}
	}
	call := RequestPluginCall{
		RequestID:       uuid.NewString(),
		UserID:          apiKey.UserID,
		TokenID:         apiKey.ID,
		TokenName:       apiKey.Name,
		GroupID:         *apiKey.GroupID,
		Group:           strconv.FormatInt(*apiKey.GroupID, 10),
		Model:           clientModel,
		ModelCandidates: candidates,
		Format:          "openai_responses",
		Path:            path,
	}
	plan, err := s.newAPIPlugins.NewRequestPluginPlan(ctx, call)
	if err != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		return nil, nil, &RequestPluginWSTurnReject{
			StatusCode: http.StatusServiceUnavailable,
			ErrorType:  "request_processing_error",
			Code:       "request_processing_failed",
			Message:    "Request-processing policy could not be applied",
		}
	}
	username := ""
	if apiKey.User != nil {
		username = apiKey.User.Username
	}
	report := s.newAPIPlugins.BeginRequestPluginWSReport(WithRequestPluginPlan(ctx, plan), username)
	turn := &RequestPluginWSTurn{
		service:     s.newAPIPlugins,
		plan:        plan,
		report:      report,
		clientBody:  bytes.Clone(rawBody),
		clientModel: clientModel,
	}
	if !plan.HasEarlyIngress() {
		return rawBody, turn, nil
	}
	if len(rawBody) > MaxRequestPluginBody {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		rej := &RequestPluginWSTurnReject{
			StatusCode: http.StatusRequestEntityTooLarge,
			ErrorType:  "request_processing_error",
			Code:       "request_processing_body_too_large",
			Message:    "Request-processing body exceeds 1 MiB",
		}
		turn.RecordEvent(rej.EventJSON())
		turn.FinishReport(rej.StatusCode)
		return nil, nil, rej
	}
	if !gjson.ValidBytes(rawBody) || !gjson.ParseBytes(rawBody).IsObject() {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		rej := &RequestPluginWSTurnReject{
			StatusCode: http.StatusBadRequest,
			ErrorType:  "invalid_request_error",
			Code:       "invalid_json",
			Message:    "Invalid JSON request body",
		}
		turn.RecordEvent(rej.EventJSON())
		turn.FinishReport(rej.StatusCode)
		return nil, nil, rej
	}
	memCtx, releaseMem := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer releaseMem()
	if err := pkghttputil.ReserveRequestBodyMemory(memCtx, int64(len(rawBody))*6+(8<<20)); err != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		rej := &RequestPluginWSTurnReject{
			StatusCode: http.StatusServiceUnavailable,
			ErrorType:  "request_processing_error",
			Code:       "request_processing_failed",
			Message:    "Request-processing policy could not be applied",
		}
		turn.RecordEvent(rej.EventJSON())
		turn.FinishReport(rej.StatusCode)
		return nil, nil, rej
	}
	out, ingressErr := plan.Ingress(memCtx, rawBody)
	auditCtx, cancelAudit := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	auditErr := s.newAPIPlugins.RecordRequestPluginRun(auditCtx, call, out, false)
	cancelAudit()
	if auditErr != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		rej := &RequestPluginWSTurnReject{
			StatusCode: http.StatusServiceUnavailable,
			ErrorType:  "request_processing_error",
			Code:       "request_processing_failed",
			Message:    "Request-processing policy could not be applied",
		}
		turn.RecordEvent(rej.EventJSON())
		turn.FinishReport(rej.StatusCode)
		return nil, nil, rej
	}
	if ingressErr != nil {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		rej := wsTurnRejectFromFailure(ingressErr)
		turn.RecordEvent(rej.EventJSON())
		turn.FinishReport(rej.StatusCode)
		return nil, nil, rej
	}
	turn.clientBody = bytes.Clone(out.Body)
	return out.Body, turn, nil
}

// ResetAttempt clears transient per-attempt reply state before trying another
// account on Turn 1 failover, while retaining early ingress patches and records.
func (t *RequestPluginWSTurn) ResetAttempt() {
	if t == nil {
		return
	}
	if t.reply != nil && t.reply.releaseMem != nil {
		t.reply.releaseMem()
	}
	t.reply = nil
	t.finalBody = nil
}

// RunAttemptStages executes request, late ingress, outbound, and egress stages
// for the selected account on this turn's actual upstream payload.
func (t *RequestPluginWSTurn) RunAttemptStages(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	upstreamModel string,
	payload []byte,
	modifierSpec modelmodifier.Spec,
	modifierApplied bool,
	modifierAppliedBody []byte,
) ([]byte, map[string]string, *RequestPluginWSTurnReject) {
	if t == nil {
		return payload, nil, nil
	}
	t.modifier = modifierSpec
	t.modifierApplied = modifierApplied
	t.modifierAppliedBody = modifierAppliedBody
	if t.plan == nil || t.service == nil || account == nil {
		return payload, nil, nil
	}
	planCtx := WithRequestPluginPlan(ctx, t.plan)
	planCtx = context.WithValue(planCtx, requestPluginProviderBodyKey{}, t.clientBody)
	if modifierApplied {
		planCtx = modifierSpec.Context(planCtx)
	}
	beginRelayReportAttempt(planCtx, account, upstreamModel, false)

	channelType := requestPluginChannelType(account, false)
	attemptItems := t.plan.attemptItems(account.ID, channelType)
	if !slices.ContainsFunc(attemptItems, t.plan.hasAttemptStage) {
		return payload, nil, nil
	}
	memCtx, releaseMem := pkghttputil.WithRequestBodyMemoryScope(planCtx)
	defer releaseMem()

	attempt := max(0, t.plan.nextAttempt-1)
	path := t.plan.call.Path
	if path == "" {
		path = openAIResponsesEndpoint
	}
	expectedModel := gjson.GetBytes(payload, "model").Str
	expectedType := gjson.GetBytes(payload, "type").Str
	bodyGuard := func(before, after []byte) bool {
		if expectedModel != "" && gjson.GetBytes(after, "model").Str != expectedModel {
			return false
		}
		if expectedType != "" && gjson.GetBytes(after, "type").Str != expectedType {
			return false
		}
		return true
	}
	current := payload
	var mergedHeaders map[string]string

	hasStage := func(stage string) bool {
		for _, item := range attemptItems {
			if slices.Contains(item.Stages, stage) && (stage != "ingress" || t.plan.lateIngressItem(item)) {
				return true
			}
		}
		return false
	}
	if hasStage("request") {
		out, err := t.service.relayRequestPluginStages(memCtx, account, path, upstreamModel, current, contract.AdvancedCustomRoute{}, requestPluginStageRun{
			attempt:   attempt,
			stages:    []string{"request"},
			format:    "openai_responses",
			bodyGuard: bodyGuard,
			protected: nativeRequestPluginProtectedHeader,
		})
		if err != nil {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			rej := wsTurnRejectFromFailure(err)
			t.RecordEvent(rej.EventJSON())
			t.FinishReport(rej.StatusCode)
			return nil, nil, rej
		}
		current = out.Body
		mergedHeaders = out.Headers
	}
	if hasStage("ingress") || hasStage("outbound") || hasStage("egress") {
		out, err := t.service.relayRequestPluginStages(memCtx, account, path, upstreamModel, current, contract.AdvancedCustomRoute{}, requestPluginStageRun{
			attempt:   attempt,
			stages:    []string{"ingress", "outbound", "egress"},
			format:    "openai_responses",
			bodyGuard: bodyGuard,
			protected: nativeRequestPluginProtectedHeader,
		})
		if err != nil {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			rej := wsTurnRejectFromFailure(err)
			t.RecordEvent(rej.EventJSON())
			t.FinishReport(rej.StatusCode)
			return nil, nil, rej
		}
		current = out.Body
		if len(out.Headers) > 0 {
			if mergedHeaders == nil {
				mergedHeaders = map[string]string{}
			}
			for k, v := range out.Headers {
				mergedHeaders[k] = v
			}
		}
	}
	t.finalBody = current
	return current, mergedHeaders, nil
}

// BeginReply admits and initializes the per-turn WebSocket reply filter before
// the turn is written upstream.
func (t *RequestPluginWSTurn) BeginReply(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	upstreamModel string,
	writeClient func([]byte) error,
	onStop func(),
) (context.Context, *RequestPluginWSTurnReject) {
	if t == nil || t.plan == nil || t.service == nil || account == nil {
		return ctx, nil
	}
	channelType := requestPluginChannelType(account, false)
	items := t.plan.replyItems(account.ID, channelType)
	if len(items) == 0 {
		return ctx, nil
	}
	attempt := max(0, t.plan.nextAttempt-1)
	session := &wsReplySession{
		service:       t.service,
		plan:          t.plan,
		items:         items,
		accountID:     account.ID,
		attempt:       attempt,
		upstreamModel: upstreamModel,
	}
	memCtx, releaseMem := pkghttputil.WithRequestBodyMemoryScope(ctx)
	class := ""
	if len(items) > replyfilter.MaxInstances {
		class = "limit"
	} else if err := pkghttputil.ReserveRequestBodyMemory(memCtx, providerReplyMemory); err != nil {
		class = "capacity"
	}
	if class != "" {
		releaseMem()
		out := RequestPluginOutcome{Records: session.records("error", class)}
		auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_ = t.service.RecordRequestPluginRun(auditCtx, t.plan.call, out, false)
		cancel()
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		rej := wsTurnRejectFromFailure(&RequestPluginFailure{Class: class})
		t.RecordEvent(rej.EventJSON())
		t.FinishReport(rej.StatusCode)
		return ctx, rej
	}
	send := func(msg []byte) {
		if writeClient != nil {
			if err := writeClient(msg); err != nil {
				session.writeErrMu.Lock()
				if session.writeErr == nil {
					session.writeErr = err
				}
				session.writeErrMu.Unlock()
				return
			}
		}
		t.RecordEvent(msg)
	}
	filter, childCtx, err := replyfilter.NewSocket(
		ctx,
		"openai_responses",
		t.service.replyInstancesForChannel(t.plan, items, account.ID, channelType, func() string { return session.upstreamModel }, func() int { return session.attempt }),
		send,
		func() {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			if onStop != nil {
				onStop()
			}
		},
	)
	if err != nil {
		releaseMem()
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		rej := wsTurnRejectFromFailure(&RequestPluginFailure{Class: "reply_setup"})
		t.RecordEvent(rej.EventJSON())
		t.FinishReport(rej.StatusCode)
		return ctx, rej
	}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	auditErr := t.service.RecordRequestPluginRun(auditCtx, t.plan.call, RequestPluginOutcome{Records: session.records("started", "")}, false)
	cancel()
	if auditErr != nil {
		filter.Finish()
		releaseMem()
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		rej := wsTurnRejectFromFailure(&RequestPluginFailure{Class: "audit"})
		t.RecordEvent(rej.EventJSON())
		t.FinishReport(rej.StatusCode)
		return ctx, rej
	}
	session.filter = filter
	session.releaseMem = releaseMem
	t.reply = session
	return childCtx, nil
}

func (s *wsReplySession) records(action, class string) []RequestPluginRecord {
	records := make([]RequestPluginRecord, 0, len(s.items))
	for _, item := range s.items {
		records = append(records, RequestPluginRecord{
			InstanceID: item.ID,
			VersionID:  item.PluginVersionID,
			Stage:      "reply",
			Action:     action,
			ErrorClass: class,
			AccountID:  s.accountID,
			Attempt:    s.attempt,
		})
	}
	return records
}

func (s *wsReplySession) popWriteErr() error {
	if s == nil {
		return nil
	}
	s.writeErrMu.Lock()
	err := s.writeErr
	s.writeErr = nil
	s.writeErrMu.Unlock()
	return err
}

// EmitEvent delivers one non-terminal upstream JSON event through the reply
// filter (if active) and records emitted events for the turn report.
func (t *RequestPluginWSTurn) EmitEvent(msg []byte, writeClient func([]byte) error) error {
	if t != nil && t.reply != nil && t.reply.filter != nil {
		t.reply.filter.Event(msg)
		return t.reply.popWriteErr()
	}
	if writeClient != nil {
		if err := writeClient(msg); err != nil {
			return err
		}
	}
	t.RecordEvent(msg)
	return nil
}

// EmitTerminal delivers the terminal upstream JSON event through the reply
// filter. When stopped, returns the reply failure without writing the terminal
// event to the client.
func (t *RequestPluginWSTurn) EmitTerminal(msg []byte, writeClient func([]byte) error) (*replyfilter.Failure, error) {
	if t != nil && t.reply != nil && t.reply.filter != nil {
		finalMsg := t.reply.filter.Terminal(msg)
		if stopFail := t.reply.filter.Stopped(); stopFail != nil {
			return stopFail, t.reply.popWriteErr()
		}
		if err := t.reply.popWriteErr(); err != nil {
			return nil, err
		}
		if finalMsg != nil {
			if writeClient != nil {
				if err := writeClient(finalMsg); err != nil {
					return nil, err
				}
			}
			t.RecordEvent(finalMsg)
		}
		return nil, nil
	}
	if writeClient != nil {
		if err := writeClient(msg); err != nil {
			return nil, err
		}
	}
	t.RecordEvent(msg)
	return nil, nil
}

// ReplyStopped returns the reply failure if a reply hook stopped this turn.
func (t *RequestPluginWSTurn) ReplyStopped() *replyfilter.Failure {
	if t == nil || t.reply == nil || t.reply.filter == nil {
		return nil
	}
	return t.reply.filter.Stopped()
}

// FinishReply closes the per-turn reply filter, appends aggregate reply records
// to the turn report, and persists the terminal reply audit record.
func (t *RequestPluginWSTurn) FinishReply(ctx context.Context, c *gin.Context) {
	if t == nil || t.reply == nil || t.reply.finished {
		return
	}
	session := t.reply
	session.finished = true
	defer func() {
		if session.releaseMem != nil {
			session.releaseMem()
			session.releaseMem = nil
		}
	}()
	records, stopped := session.filter.Finish()
	out := RequestPluginOutcome{Records: session.records("unchecked", "")}
	for i, item := range session.items {
		for _, rec := range records {
			if rec.ID != strconv.FormatInt(item.ID, 10) {
				continue
			}
			out.Records[i].Action, out.Records[i].ErrorClass = rec.Action, rec.ErrorClass
			out.Records[i].Effects, out.Records[i].Findings, out.Records[i].DurationMS = rec.Masked, rec.Findings, rec.DurationMS
		}
	}
	t.plan.appendReportRecords(out.Records)
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	err := session.service.RecordRequestPluginRun(auditCtx, session.plan.call, out, false)
	cancel()
	if err != nil {
		count := session.service.replyAuditFailures.Add(1)
		if count == 1 || count%64 == 0 {
			slog.Warn("request_plugin_ws_reply_terminal_audit_failed", "failures", count)
		}
	}
	if stopped {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	}
}

// RecordEvent appends an emitted WS event to the turn's report buffer.
func (t *RequestPluginWSTurn) RecordEvent(event []byte) {
	if t == nil || t.report == nil || len(event) == 0 {
		return
	}
	t.report.RecordWSEvent(event)
}

// BindResult attaches the turn's confirmed-ledger billing rendezvous to result.
func (t *RequestPluginWSTurn) BindResult(account *Account, result *OpenAIForwardResult) {
	if t == nil || t.report == nil || result == nil || t.reportBound {
		return
	}
	if account != nil && t.report.accountID == 0 {
		beginRelayReportAttempt(WithRequestPluginPlan(context.Background(), t.plan), account, result.UpstreamModel, false)
	}
	t.report.result(result)
	t.reportBound = true
}

// FinishReport dispatches the turn's report job (idempotent).
func (t *RequestPluginWSTurn) FinishReport(status int) {
	if t == nil || t.report == nil || t.reportFinished {
		return
	}
	t.reportFinished = true
	t.report.SetWSStatus(status)
	t.report.Finish(t.clientBody)
}

// ReconcileResult aligns result model/billing/reasoning metadata with @ model
// modifiers and any Request Processing body modifications made on this turn.
func (t *RequestPluginWSTurn) ReconcileResult(result *OpenAIForwardResult, spec modelmodifier.Spec, modifierApplied bool, originalModel string, appliedBody []byte) {
	if result == nil {
		return
	}
	finalBody := appliedBody
	if t != nil && len(t.finalBody) > 0 {
		finalBody = t.finalBody
	}
	if modifierApplied {
		reconciled := spec
		if len(finalBody) > 0 && len(appliedBody) > 0 {
			if updated, err := spec.ReconcileForFormat(appliedBody, finalBody, "openai_responses", false); err == nil {
				reconciled = updated
			}
		}
		if strings.TrimSpace(originalModel) != "" {
			result.Model = originalModel
		}
		if result.UpstreamModel == "" || result.UpstreamModel == originalModel {
			result.UpstreamModel = spec.Base
		}
		if reconciled.Thinking {
			if names := reconciled.BillingNames(); len(names) > 0 && result.BillingModel == "" {
				result.BillingModel = names[0]
			}
			if value := reconciled.Effort(); value != "" {
				effort := value
				result.ReasoningEffort = &effort
			}
		} else if result.BillingModel == "" || result.BillingModel == originalModel {
			result.BillingModel = spec.Base
		}
	} else if t != nil && len(t.finalBody) > 0 {
		effort := extractOpenAIReasoningEffortFromBody(t.finalBody, result.UpstreamModel, result.BillingModel, result.Model)
		result.ReasoningEffort = ApplyThinkingEnabledFallback(effort, t.finalBody, result.UpstreamModel)
	}
}

// applyWSModelModifier resolves and applies @ model modifiers on one WS turn
// payload without leaking raw @ modifiers into the upstream frame.
func applyWSModelModifier(body []byte, originalModel, mappedModel string) ([]byte, modelmodifier.Spec, bool, *RequestPluginWSTurnReject) {
	spec, err := modelmodifier.Resolve(originalModel, mappedModel)
	if err != nil {
		return nil, modelmodifier.Spec{}, false, &RequestPluginWSTurnReject{
			StatusCode: http.StatusBadRequest,
			ErrorType:  "invalid_request_error",
			Code:       "invalid_model_modifier",
			Message:    modelmodifier.ErrInvalid.Error(),
		}
	}
	if !spec.HasControls() && spec.Base == originalModel && spec.Base == mappedModel {
		return body, spec, false, nil
	}
	next := body
	if spec.HasControls() {
		next, err = spec.ApplyForFormat(next, "openai_responses")
		if err != nil {
			return nil, modelmodifier.Spec{}, false, &RequestPluginWSTurnReject{
				StatusCode: http.StatusBadRequest,
				ErrorType:  "invalid_request_error",
				Code:       "invalid_model_modifier",
				Message:    modelmodifier.ErrInvalid.Error(),
			}
		}
	}
	if len(next) > 0 {
		next, err = sjson.SetBytes(next, "model", spec.Base)
		if err != nil {
			return nil, modelmodifier.Spec{}, false, &RequestPluginWSTurnReject{
				StatusCode: http.StatusBadRequest,
				ErrorType:  "invalid_request_error",
				Code:       "invalid_model_modifier",
				Message:    modelmodifier.ErrInvalid.Error(),
			}
		}
	}
	return next, spec, true, nil
}
