package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/shopspring/decimal"
)

// ScriptHooks contains no HTTP, account, secret store or wallet. Production
// hosts supply a bounded isolated runner, including their authorized fetch.
type ScriptHooks struct {
	Call func(context.Context, string, ...any) (json.RawMessage, error)
	Has  func(string) bool
}

// ScriptDriver owns the Task hook/result lifecycle. Context must project only
// the original authorized task connection; the host validates, signs and sends
// descriptors after BuildRequest and classifies uncertain paid submissions.
type ScriptDriver struct {
	Hooks       ScriptHooks
	Context     func(*Task) map[string]any
	OnResultURL func(*Task, string)
	Now         func() time.Time
}

type ScriptResponse struct {
	Status  int
	Headers map[string]string
	Body    json.RawMessage
}

func (d ScriptDriver) available(task *Task) error {
	if task == nil || d.Hooks.Call == nil || d.Hooks.Has == nil || d.Context == nil {
		return errors.New("generation script driver is unavailable")
	}
	return nil
}

// HasQuoteHTTP reports whether the loaded plugin exports the optional
// buildQuoteRequest + parseQuoteResponse pre-submit quote pair.
func (d ScriptDriver) HasQuoteHTTP() bool {
	return d.Hooks.Has != nil && d.Hooks.Has("buildQuoteRequest") && d.Hooks.Has("parseQuoteResponse")
}

// HasQuotePure reports whether the loaded plugin exports the optional pure
// pre-submit quotePrice hook.
func (d ScriptDriver) HasQuotePure() bool {
	return d.Hooks.Has != nil && d.Hooks.Has("quotePrice")
}

func (d ScriptDriver) BuildQuoteRequest(ctx context.Context, task *Task) (pluginTask.Request, error) {
	if err := d.available(task); err != nil {
		return pluginTask.Request{}, err
	}
	if !d.HasQuoteHTTP() {
		return pluginTask.Request{}, errors.New("quote HTTP hooks are not exported")
	}
	raw, err := d.Hooks.Call(ctx, "buildQuoteRequest", d.Context(task))
	if err != nil {
		return pluginTask.Request{}, err
	}
	return pluginTask.Decode[pluginTask.Request](raw)
}

func (d ScriptDriver) ParseQuoteResponse(ctx context.Context, task *Task, response ScriptResponse) (*Cost, error) {
	if err := d.available(task); err != nil {
		return nil, err
	}
	if !d.HasQuoteHTTP() {
		return nil, errors.New("quote HTTP hooks are not exported")
	}
	if response.Status < 200 || response.Status >= 300 {
		return nil, errors.New("task quote returned non-success HTTP status")
	}
	headers := make(map[string][]string, len(response.Headers))
	for name, value := range response.Headers {
		headers[name] = []string{value}
	}
	raw, err := d.Hooks.Call(ctx, "parseQuoteResponse", d.Context(task), map[string]any{"statusCode": response.Status, "headers": headers, "body": response.Body})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if d.Now != nil {
		now = d.Now().UTC()
	}
	return ParseQuotePayload(raw, now)
}

func (d ScriptDriver) QuotePrice(ctx context.Context, task *Task) (*Cost, error) {
	if err := d.available(task); err != nil {
		return nil, err
	}
	if !d.HasQuotePure() {
		return nil, errors.New("quotePrice hook is not exported")
	}
	raw, err := d.Hooks.Call(ctx, "quotePrice", d.Context(task))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if d.Now != nil {
		now = d.Now().UTC()
	}
	return ParseQuotePayload(raw, now)
}

// CancelOutcome captures the parsed result of an optional upstream cancel call.
// Confirmed is true ONLY when parseCancelResponse explicitly confirms terminal
// non-billable cancellation; an HTTP 200 without confirmation never triggers a
// speculative refund.
type CancelOutcome struct {
	Confirmed bool            `json:"confirmed"`
	Status    string          `json:"status,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	State     json.RawMessage `json:"state,omitempty"`
}

// HasCancelHTTP reports whether the loaded plugin exports buildCancelRequest.
func (d ScriptDriver) HasCancelHTTP() bool {
	return d.Hooks.Has != nil && d.Hooks.Has("buildCancelRequest")
}

// HasCancelParser reports whether the loaded plugin exports parseCancelResponse.
func (d ScriptDriver) HasCancelParser() bool {
	return d.Hooks.Has != nil && d.Hooks.Has("parseCancelResponse")
}

func (d ScriptDriver) BuildCancelRequest(ctx context.Context, task *Task) (pluginTask.Request, error) {
	if err := d.available(task); err != nil {
		return pluginTask.Request{}, err
	}
	if !d.HasCancelHTTP() {
		return pluginTask.Request{}, errors.New("cancel hook is not exported")
	}
	raw, err := d.Hooks.Call(ctx, "buildCancelRequest", d.Context(task))
	if err != nil {
		return pluginTask.Request{}, err
	}
	return pluginTask.Decode[pluginTask.Request](raw)
}

func (d ScriptDriver) ParseCancelResponse(ctx context.Context, task *Task, response ScriptResponse) (CancelOutcome, error) {
	if err := d.available(task); err != nil {
		return CancelOutcome{}, err
	}
	if !d.HasCancelParser() {
		return CancelOutcome{}, nil
	}
	headers := make(map[string][]string, len(response.Headers))
	for name, value := range response.Headers {
		headers[name] = []string{value}
	}
	raw, err := d.Hooks.Call(ctx, "parseCancelResponse", d.Context(task), map[string]any{"statusCode": response.Status, "headers": headers, "body": response.Body})
	if err != nil {
		return CancelOutcome{}, err
	}
	return ParseCancelPayload(raw)
}

// ParseCancelPayload strictly decodes a plugin's parseCancelResponse return value.
// Only explicit terminal cancellation (canceled: true with non-contradicting
// status, or status CANCELED/CANCELLED/FAILURE with confirmed: true) sets
// Confirmed=true. Unknown fields fail closed.
func ParseCancelPayload(raw json.RawMessage) (CancelOutcome, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(trimmed) > 64<<10 || string(trimmed) == "null" {
		return CancelOutcome{}, errors.New("invalid cancel response payload")
	}
	var payload struct {
		Canceled  *bool           `json:"canceled"`
		Confirmed *bool           `json:"confirmed"`
		Status    string          `json:"status"`
		Reason    string          `json:"reason"`
		State     json.RawMessage `json:"state"`
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		return CancelOutcome{}, errors.New("cancel response payload contains invalid or unknown fields")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return CancelOutcome{}, errors.New("cancel response payload has trailing data")
	}
	status := strings.ToUpper(strings.TrimSpace(payload.Status))
	if len(payload.Reason) > 1024 {
		payload.Reason = payload.Reason[:1024]
	}
	confirmed := false
	switch status {
	case "CANCELED", "CANCELLED":
		confirmed = (payload.Canceled == nil || *payload.Canceled) && (payload.Confirmed == nil || *payload.Confirmed)
	case "FAILURE":
		confirmed = (payload.Canceled != nil && *payload.Canceled) || (payload.Confirmed != nil && *payload.Confirmed)
	case "":
		confirmed = (payload.Canceled != nil && *payload.Canceled) && (payload.Confirmed == nil || *payload.Confirmed)
	default:
		confirmed = false
	}
	return CancelOutcome{
		Confirmed: confirmed,
		Status:    status,
		Reason:    strings.TrimSpace(payload.Reason),
		State:     payload.State,
	}, nil
}

// ParseQuotePayload strictly decodes a plugin's pre-submit quote response.
// Missing/unbounded amounts, invalid currencies, unknown fields, or expired
// quotes fail closed, and Final is always forced to false.
func ParseQuotePayload(raw json.RawMessage, now time.Time) (*Cost, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(trimmed) > 64<<10 || string(trimmed) == "null" {
		return nil, errors.New("invalid upstream quote payload")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	var payload struct {
		Amount        json.RawMessage `json:"amount"`
		Currency      string          `json:"currency"`
		Unit          string          `json:"unit"`
		Source        string          `json:"source"`
		ObservedAt    string          `json:"observed_at"`
		ObservedAtAlt string          `json:"observedAt"`
		ExpiresAt     string          `json:"expires_at"`
		ExpiresAtAlt  string          `json:"expiresAt"`
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		return nil, errors.New("invalid upstream quote payload")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid upstream quote payload")
	}
	amountRaw := bytes.TrimSpace(payload.Amount)
	if len(amountRaw) == 0 || len(amountRaw) > 128 || string(amountRaw) == "null" {
		return nil, errors.New("upstream quote amount is required")
	}
	var amount decimal.Decimal
	if json.Unmarshal(amountRaw, &amount) != nil || !boundedFinalBillAmount(amount) {
		return nil, errors.New("upstream quote amount must be finite and non-negative")
	}
	if len(payload.Currency) != 3 {
		return nil, errors.New("upstream quote currency must be a 3-letter uppercase code")
	}
	for i := 0; i < 3; i++ {
		if payload.Currency[i] < 'A' || payload.Currency[i] > 'Z' {
			return nil, errors.New("upstream quote currency must be a 3-letter uppercase code")
		}
	}
	if payload.Unit == "" || len(payload.Unit) > 64 || strings.TrimSpace(payload.Unit) != payload.Unit {
		return nil, errors.New("upstream quote unit is required")
	}
	if payload.Source == "" || len(payload.Source) > 128 || strings.TrimSpace(payload.Source) != payload.Source {
		return nil, errors.New("upstream quote source is required")
	}
	if payload.ObservedAt != "" && payload.ObservedAtAlt != "" && payload.ObservedAt != payload.ObservedAtAlt {
		return nil, errors.New("conflicting upstream quote observed_at values")
	}
	observedText := payload.ObservedAt
	if observedText == "" {
		observedText = payload.ObservedAtAlt
	}
	observedAt := now
	if observedText != "" {
		parsed, err := time.Parse(time.RFC3339Nano, observedText)
		if err != nil {
			return nil, errors.New("invalid upstream quote observed_at timestamp")
		}
		observedAt = parsed.UTC()
	}
	if payload.ExpiresAt != "" && payload.ExpiresAtAlt != "" && payload.ExpiresAt != payload.ExpiresAtAlt {
		return nil, errors.New("conflicting upstream quote expires_at values")
	}
	expiresText := payload.ExpiresAt
	if expiresText == "" {
		expiresText = payload.ExpiresAtAlt
	}
	var expiresAt *time.Time
	if expiresText != "" {
		parsed, err := time.Parse(time.RFC3339Nano, expiresText)
		if err != nil {
			return nil, errors.New("invalid upstream quote expires_at timestamp")
		}
		exp := parsed.UTC()
		if !exp.After(observedAt) || !exp.After(now) {
			return nil, ErrQuoteExpired
		}
		expiresAt = &exp
	}
	return &Cost{
		Amount:     amount,
		Currency:   payload.Currency,
		Unit:       payload.Unit,
		Final:      false,
		Source:     payload.Source,
		ObservedAt: observedAt,
		ExpiresAt:  expiresAt,
	}, nil
}

// BuildRequest does not perform I/O or authorize a destination. The host must
// validate the returned descriptor against its URL/file/credential policy.
func (d ScriptDriver) BuildRequest(ctx context.Context, task *Task, submit bool) (pluginTask.Request, error) {
	if err := d.available(task); err != nil {
		return pluginTask.Request{}, err
	}
	hook := "buildQueryRequest"
	if submit {
		hook = "buildSubmitRequest"
	}
	raw, err := d.Hooks.Call(ctx, hook, d.Context(task))
	if err != nil {
		return pluginTask.Request{}, err
	}
	return pluginTask.Decode[pluginTask.Request](raw)
}

func (d ScriptDriver) AcceptSubmission(ctx context.Context, task *Task, response ScriptResponse) error {
	if err := d.available(task); err != nil {
		return err
	}
	if response.Status < 200 || response.Status >= 300 {
		return errors.New("task submission returned non-success HTTP status")
	}
	headers := make(map[string][]string, len(response.Headers))
	for name, value := range response.Headers {
		headers[name] = []string{value}
	}
	raw, err := d.Hooks.Call(ctx, "parseSubmitResponse", d.Context(task), map[string]any{"statusCode": response.Status, "headers": headers, "body": response.Body})
	if err != nil {
		return err
	}
	submitted, err := pluginTask.Decode[pluginTask.Submitted](raw)
	if err != nil {
		return err
	}
	if err = submitted.Validate(); err != nil {
		return err
	}
	next := *task
	next.UpstreamID = submitted.TaskID
	next.State, next.Data = nonemptyJSON(submitted.State), nonemptyJSON(submitted.TaskData)
	next.Status = Running
	// Acceptance is a known external fact even if a later immediate-result
	// hook fails. Retain its identity so reconciliation can query this job.
	*task = next
	if submitted.Immediate != nil {
		d.applyResult(&next, *submitted.Immediate)
		if err = d.complete(ctx, &next, *submitted.Immediate, nonemptyJSON(submitted.TaskData)); err != nil {
			return err
		}
	}
	*task = next
	return nil
}

// AcceptQuery applies changes only when every result/usage/artifact hook has
// succeeded. A failed poll must not persist half of a terminal observation.
func (d ScriptDriver) AcceptQuery(ctx context.Context, task *Task, response ScriptResponse) error {
	if err := d.available(task); err != nil {
		return err
	}
	if response.Status < 200 || response.Status >= 300 {
		return errors.New("task poll returned non-success HTTP status")
	}
	raw, err := d.Hooks.Call(ctx, "parseTaskResult", d.Context(task), response.Body, map[string]any{"status": response.Status, "headers": response.Headers})
	if err != nil {
		return err
	}
	result, err := pluginTask.Decode[pluginTask.Result](raw)
	if err != nil {
		return err
	}
	if err = result.Validate(); err != nil {
		return err
	}
	next := *task
	next.Data = response.Body
	d.applyResult(&next, result)
	if err = d.complete(ctx, &next, result, response.Body); err != nil {
		return err
	}
	*task = next
	return nil
}

func (d ScriptDriver) complete(ctx context.Context, task *Task, result pluginTask.Result, body json.RawMessage) error {
	if task.Status != Succeeded && task.Status != Failed {
		return nil
	}
	if d.Hooks.Has("extractUsageOnComplete") {
		view := map[string]any{"action": task.Action, "state": task.State, "data": task.Data, "model": task.Model, "upstreamModel": task.UpstreamModel, "task_id": task.ID}
		resultView := map[string]any{"status": result.Status, "plugin_state": result.State, "completionTokens": result.CompletionTokens, "totalTokens": result.TotalTokens}
		usage, err := d.Hooks.Call(ctx, "extractUsageOnComplete", view, resultView, body)
		if err != nil {
			return err
		}
		merged := make(map[string]json.RawMessage)
		_ = json.Unmarshal(task.Usage, &merged)
		if merged == nil {
			merged = make(map[string]json.RawMessage)
		}
		var observed map[string]json.RawMessage
		_ = json.Unmarshal(nonemptyJSON(usage), &observed)
		for key, value := range observed {
			merged[key] = value
		}
		// Keep observations separate: missing final numbers cannot inherit an
		// estimate, including when a script supplies a reserved-looking key.
		merged["_host_final_usage"] = nonemptyJSON(usage)
		encoded, err := json.Marshal(merged)
		if err != nil {
			return err
		}
		task.Usage = encoded
	}
	if task.Status == Succeeded && d.Hooks.Has("listArtifacts") {
		artifacts, err := d.Hooks.Call(ctx, "listArtifacts", TaskArtifactInput(task))
		if err != nil {
			return err
		}
		items, err := pluginTask.DecodeArtifacts(artifacts)
		if err != nil {
			return err
		}
		task.DeliveryStatus = DeliveryPending
		nonVideo := task.Protocol == ProtocolSunoTask || task.Protocol == ProtocolGenericTask || task.Protocol == ProtocolOpenAIImage || task.Protocol == ProtocolOpenAIResponses || strings.HasPrefix(task.Protocol, ProtocolNativePrefix)
		for _, item := range items {
			if nonVideo || item.Type == "video" && item.Key == "video" {
				task.DeliveryStatus = DeliveryReady
			}
		}
	}
	if d.OnResultURL != nil {
		d.OnResultURL(task, result.URL)
	}
	return nil
}

func nonemptyJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}
	return raw
}

func (d ScriptDriver) applyResult(task *Task, result pluginTask.Result) {
	task.Progress = result.ProgressPercent()
	if len(result.State) > 0 {
		task.State = result.State
	}
	switch result.Status {
	case "SUCCESS":
		task.Status, task.Progress = Succeeded, 100
	case "FAILURE":
		task.Status = Failed
		task.ErrorCode, task.ErrorMessage = "video_generation_failed", "The upstream video generation failed."
		if task.Protocol == ProtocolSunoTask {
			task.ErrorCode, task.ErrorMessage = "audio_generation_failed", "The upstream audio task failed."
		}
	case "UNKNOWN":
		task.BillingStatus = BillingReview
		task.ErrorCode, task.ErrorMessage = "upstream_status_unknown", "The upstream task status requires reconciliation."
	default:
		task.Status = Running
	}
	if (task.Status == Succeeded || task.Status == Failed) && task.FinishedAt == nil {
		now := time.Now().UTC()
		if d.Now != nil {
			now = d.Now().UTC()
		}
		task.FinishedAt = &now
		task.BillingStatus = BillingPending
	}
}
