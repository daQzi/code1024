// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// HTTP report records follow QuantumNous/new-api relay/requestplugin/report.go
// at 48f9df96ea7c68eae04a0f3687869f232f48929f. Billing rendezvous, admission and
// account placement are local host code; see internal/newapi/NOTICE.md.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

const relayReportMemory = 12 << 20
const relayReportResponseBytes = 1 << 20
const relayReportMaxRecords = 256
const relayReportSettlementWait = 15 * time.Second

// Only this small, immutable-after-completion object crosses into billing.
// It never retains a request, plan, writer, account, credentials or Gin context.
type relayReportBilling struct {
	once      sync.Once
	done      chan struct{}
	amount    decimal.Decimal
	confirmed bool
}

func (b *relayReportBilling) complete(amount decimal.Decimal, confirmed bool) {
	if b == nil {
		return
	}
	b.once.Do(func() { b.amount, b.confirmed = amount, confirmed && !amount.IsNegative(); close(b.done) })
}

type relayReportTarget struct {
	id           int64
	name, source string
	config       json.RawMessage
	hosts        []string
}

type relayReportResponse struct {
	Status      int                `json:"status"`
	ContentType string             `json:"contentType"`
	Stream      bool               `json:"stream"`
	Body        json.RawMessage    `json:"body,omitempty"`
	Events      []relayReportEvent `json:"events,omitempty"`
	Bytes       int64              `json:"bytes"`
	Truncated   bool               `json:"truncated"`
}
type relayReportEvent struct {
	Event string          `json:"event,omitempty"`
	Data  json.RawMessage `json:"data"`
}
type relayReportRecord struct {
	RequestID       string                  `json:"requestId"`
	User            jsplugin.TransformUser  `json:"user"`
	Token           jsplugin.TransformToken `json:"token"`
	StartedAt       int64                   `json:"startedAt"`
	DurationMS      int64                   `json:"durationMs"`
	FirstResponseMS *int64                  `json:"firstResponseMs,omitempty"`
	Request         struct {
		Body  json.RawMessage           `json:"body"`
		Media []jsplugin.TransformMedia `json:"media,omitempty"`
	} `json:"request"`
	Response relayReportResponse    `json:"response"`
	Usage    map[string]int         `json:"usage,omitempty"`
	Cost     map[string]json.Number `json:"cost"`
	Error    map[string]any         `json:"error,omitempty"`
	Records  []RequestPluginRecord  `json:"requestPlugins"`
}
type relayReportJob struct {
	targets  []relayReportTarget
	call     jsplugin.TransformContext
	record   relayReportRecord
	response []byte
	billing  *relayReportBilling
}

// RequestPluginHTTPReport is owned exclusively by the HTTP handler. Finish
// snapshots it before the handler returns; no worker may retain this object.
type RequestPluginHTTPReport struct {
	gin.ResponseWriter
	service              *NewAPIPluginService
	plan                 *RequestPluginPlan
	started              time.Time
	first                *int64
	kept                 []byte
	written              int64
	accountID            int64
	channelType          int
	upstreamModel        string
	billing              *relayReportBilling
	usage                OpenAIUsage
	imageCount           int
	webSearchCalls       int
	audioChars           int
	audioDurationMs      int
	requestBody          json.RawMessage
	requestMedia         []jsplugin.TransformMedia
	hasRequestProjection bool
	targets              []RequestPluginInstance
	release              func()
	username             string
	wsMode               bool
	wsStatus             int
	finished             bool
	disabled             bool
}

func (s *NewAPIPluginService) BeginRequestPluginHTTPReport(ctx context.Context, writer gin.ResponseWriter, username string) *RequestPluginHTTPReport {
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	p := pinned.plan
	if p == nil || !slices.Contains([]string{"openai", "openai_responses", "claude", "gemini"}, p.call.Format) || !slices.ContainsFunc(p.items, func(i RequestPluginInstance) bool { return slices.Contains(i.Stages, "report") }) {
		return nil
	}
	if len(username) > 1024 || len(p.call.TokenName) > 1024 || len(p.call.RequestID) > 1024 || len(p.call.Path) > 8192 || len(p.call.Model) > 4096 || len(p.call.Group) > 1024 {
		s.dropTaskReport("http_metadata_limit")
		return nil
	}
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, relayReportMemory); err != nil {
		release()
		s.dropTaskReport("http_ingress_capacity")
		return nil
	}
	r := &RequestPluginHTTPReport{ResponseWriter: writer, service: s, plan: p, started: time.Now(), release: release, username: strings.Clone(username)}
	r.targets = p.httpReportItems(0, 0)
	p.report = r
	return r
}

// BeginRequestPluginWSReport starts a per-turn report capture for Responses
// WebSocket turns without wrapping an HTTP ResponseWriter. Events are fed via
// RecordWSEvent and serialized into the standard report.response.events array.
func (s *NewAPIPluginService) BeginRequestPluginWSReport(ctx context.Context, username string) *RequestPluginHTTPReport {
	r := s.BeginRequestPluginHTTPReport(ctx, nil, username)
	if r == nil {
		return nil
	}
	r.wsMode = true
	r.wsStatus = http.StatusOK
	return r
}

// RecordWSEvent appends one JSON WebSocket event to the bounded report buffer.
func (r *RequestPluginHTTPReport) RecordWSEvent(event []byte) {
	if r == nil || len(event) == 0 {
		return
	}
	payload := bytes.TrimSpace(event)
	if bytes.ContainsAny(payload, "\r\n") {
		var compacted bytes.Buffer
		if err := json.Compact(&compacted, payload); err == nil {
			payload = compacted.Bytes()
		} else {
			payload = bytes.ReplaceAll(bytes.ReplaceAll(payload, []byte("\r"), []byte(" ")), []byte("\n"), []byte(" "))
		}
	}
	frame := make([]byte, 0, len(payload)+8)
	frame = append(frame, "data: "...)
	frame = append(frame, payload...)
	frame = append(frame, "\n\n"...)
	r.capture(frame, len(frame))
}

// SetWSStatus sets the status code reported for a WebSocket turn.
func (r *RequestPluginHTTPReport) SetWSStatus(status int) {
	if r == nil || status <= 0 {
		return
	}
	r.wsStatus = status
}

// CancelUnbilledPluginReport completes any pending billing rendezvous with zero
// cost when a handler path exits without invoking RecordUsage. Idempotent.
func CancelUnbilledPluginReport(result *OpenAIForwardResult) {
	if result == nil || result.pluginReportBilling == nil {
		return
	}
	result.pluginReportBilling.complete(decimal.Zero, true)
}

func (w *RequestPluginHTTPReport) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *RequestPluginHTTPReport) capture(data []byte, n int) {
	if n <= 0 {
		return
	}
	if w.first == nil {
		ms := time.Since(w.started).Milliseconds()
		w.first = &ms
	}
	w.written += int64(n)
	w.kept = append(w.kept, data[:min(n, relayReportResponseBytes-len(w.kept))]...)
}
func (w *RequestPluginHTTPReport) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.capture(data, n)
	return n, err
}
func (w *RequestPluginHTTPReport) WriteString(text string) (int, error) {
	n, err := w.ResponseWriter.WriteString(text)
	if n > 0 {
		if w.first == nil {
			ms := time.Since(w.started).Milliseconds()
			w.first = &ms
		}
		w.written += int64(n)
		w.kept = append(w.kept, text[:min(n, relayReportResponseBytes-len(w.kept))]...)
	}
	return n, err
}

// Each final routing attempt replaces placement, including a fallback to a
// native account. An earlier failed Provider must never receive its result.
func beginRelayReportAttempt(ctx context.Context, account *Account, upstreamModel string, provider bool) *RequestPluginHTTPReport {
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	if pinned.plan == nil {
		return nil
	}
	// Provider counts each rebuild (including credential renewal) at its stage
	// boundary. A native Forward is one attempt, even without a report target.
	if !provider {
		pinned.plan.nextAttempt++
	}
	if pinned.plan.report == nil {
		return nil
	}
	r := pinned.plan.report
	r.accountID, r.channelType = account.ID, requestPluginChannelType(account, provider)
	r.upstreamModel, r.billing, r.usage = upstreamModel, nil, OpenAIUsage{}
	r.imageCount, r.webSearchCalls, r.audioChars, r.audioDurationMs = 0, 0, 0, 0
	r.targets = pinned.plan.httpReportItems(account.ID, r.channelType)
	return r
}

// BeginNativeHTTPReportAttempt records the selected native account attempt on
// an already-attached HTTP report plan (used by standalone handlers like
// Grok WebSearch/XSearch that invoke RecordUsage directly).
func BeginNativeHTTPReportAttempt(ctx context.Context, account *Account, upstreamModel string) *RequestPluginHTTPReport {
	if account == nil {
		return nil
	}
	return beginRelayReportAttempt(ctx, account, upstreamModel, false)
}

// SetMultipart records a sanitized text-field view and attachment metadata for
// multipart requests so raw binary bytes are never retained or exposed to report.
func (r *RequestPluginHTTPReport) SetMultipart(form *RequestPluginMultipart) {
	if r == nil || form == nil {
		return
	}
	r.SetRequestProjection(form.View, form.ReportMedia())
}

// SetRequestProjection overrides the request payload snapshotted by Finish.
func (r *RequestPluginHTTPReport) SetRequestProjection(body []byte, media []jsplugin.TransformMedia) {
	if r == nil {
		return
	}
	r.requestBody = bytes.Clone(body)
	r.requestMedia = slices.Clone(media)
	r.hasRequestProjection = true
}

// p.items is already pinned, model/format matched and ordered global → group
// → account. Both native and Provider accounts execute their pinned policies;
// unsupported native attempt protocols fail before the HTTP request is sent.
func (p *RequestPluginPlan) httpReportItems(accountID int64, channelType int) []RequestPluginInstance {
	var items []RequestPluginInstance
	for _, item := range p.items {
		if slices.Contains(item.Stages, "report") && (item.Scope != "account" || accountID > 0) && p.itemMatchesChannel(item, accountID, channelType) {
			items = append(items, item)
		}
	}
	return items
}

// Fixed New API channel constants; platforms without an equivalent stay 0
// (Unknown), rather than being labelled as a JS Provider or official OpenAI.
func requestPluginChannelType(account *Account, provider bool) int {
	if provider {
		return 61
	}
	if account.IsBedrock() {
		return 33
	}
	if account.IsOpenAIOAuthLike() {
		return 57 // Codex / ChatGPT inference, distinct from official API keys.
	}
	switch account.Platform {
	case PlatformOpenAI:
		return 1
	case PlatformAnthropic:
		return 14
	case PlatformGemini:
		return 24
	case PlatformKimi:
		return 25
	case PlatformZhipu:
		return 26
	case PlatformMiniMax:
		return 35
	case PlatformDeepseek:
		return 43
	case PlatformGrok:
		return 48
	default:
		return 0
	}
}

func (r *RequestPluginHTTPReport) result(result *OpenAIForwardResult) {
	if r == nil || result == nil || len(r.targets) == 0 {
		return
	}
	r.billing = &relayReportBilling{done: make(chan struct{})}
	result.pluginReportBilling = r.billing
	r.usage = result.Usage
	r.imageCount = result.ImageCount
	r.webSearchCalls = result.WebSearchCalls
	r.audioChars, r.audioDurationMs = 0, 0
	if result.AudioUsage != nil {
		switch strings.ToLower(strings.TrimSpace(result.AudioUsage.Mode)) {
		case "tts":
			if chars := int(math.Round(result.AudioUsage.DurationOrUnits * 1_000_000)); chars > 0 {
				r.audioChars = chars
			}
		case "stt":
			if ms := int(math.Round(result.AudioUsage.DurationOrUnits * 3600 * 1000)); ms > 0 {
				r.audioDurationMs = ms
			}
		case "realtime":
			if ms := int(math.Round(result.AudioUsage.DurationOrUnits * 60 * 1000)); ms > 0 {
				r.audioDurationMs = ms
			}
		}
	}
	if result.UpstreamModel != "" {
		r.upstreamModel = result.UpstreamModel
	}
}

// Native Messages and Gemini retain their existing converters and billing
// semantics. Only the small completion projection crosses into reporting.
func beginNativeProtocolReport(ctx context.Context, account *Account, model string) *RequestPluginHTTPReport {
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	if pinned.plan == nil || !slices.Contains([]string{"claude", "gemini"}, pinned.plan.call.Format) || account == nil {
		return nil
	}
	return beginRelayReportAttempt(ctx, account, model, false)
}

func (r *RequestPluginHTTPReport) nativeResult(result *ForwardResult) {
	if r == nil || result == nil {
		return
	}
	projection := &OpenAIForwardResult{UpstreamModel: result.UpstreamModel, ImageCount: result.ImageCount, WebSearchCalls: result.SearchCount, Usage: OpenAIUsage{
		InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens,
		CacheReadInputTokens: result.Usage.CacheReadInputTokens, CacheCreationInputTokens: result.Usage.CacheCreationInputTokens,
		CacheCreation5mTokens: result.Usage.CacheCreation5mTokens, CacheCreation1hTokens: result.Usage.CacheCreation1hTokens,
		ImageOutputTokens: result.Usage.ImageOutputTokens,
	}}
	r.result(projection)
	result.pluginReportBilling = projection.pluginReportBilling
}

// NativeResult wires confirmed-ledger billing rendezvous for ForwardResult.
func (r *RequestPluginHTTPReport) NativeResult(result *ForwardResult) {
	r.nativeResult(result)
}

func (p *RequestPluginPlan) appendReportRecords(records []RequestPluginRecord) {
	if p == nil || p.report == nil || p.report.disabled {
		return
	}
	if len(p.records)+len(records) > relayReportMaxRecords {
		p.report.targets = nil
		p.report.service.dropTaskReport("http_record_limit")
		p.report.disabled = true
		return
	}
	p.records = append(p.records, records...)
}

// Finish must run once, after downstream writes and before recycling the Gin
// context. Admission reserves queued AND in-flight ownership before copies.
func (r *RequestPluginHTTPReport) Finish(body []byte) {
	if r.finished {
		return
	}
	r.finished = true
	defer r.release()
	defer func() { r.plan.report = nil }()
	if len(r.targets) == 0 || r.disabled {
		return
	}
	e := r.service.taskReports
	if e == nil {
		return
	}
	if len(r.targets) > 16 || len(r.plan.records) > relayReportMaxRecords {
		r.service.dropTaskReport("http_target_limit")
		return
	}
	if (!r.wsMode && r.ResponseWriter != nil && len(r.Header().Get("Content-Type")) > 1024) || len(r.upstreamModel) > 4096 {
		r.service.dropTaskReport("http_metadata_limit")
		return
	}
	// Account for retained source even if a concurrent cache refresh releases
	// the original version. Config, metadata, parsed values and record encoding
	// also count; source strings remain immutable and are never copied.
	size := int64(4*len(r.kept) + 4*maxTaskReportRequest + 128<<10)
	for _, item := range r.targets {
		size += int64(len(item.Version.Source) + len(item.Name) + 4*len(item.Config) + 16<<10)
		for _, host := range item.Version.Meta.AllowedHosts {
			size += int64(len(host) + 16)
		}
	}
	e.mu.Lock()
	if e.closed || e.bytes+size > taskReportQueueBytes || len(e.queue) == cap(e.queue) {
		e.mu.Unlock()
		r.service.dropTaskReport("http_capacity")
		return
	}
	e.bytes += size
	e.mu.Unlock()
	job := &relayReportJob{billing: r.billing, response: bytes.Clone(r.kept)}
	if job.billing == nil {
		job.billing = &relayReportBilling{done: make(chan struct{})}
		job.billing.complete(decimal.Zero, true) // No result, thus no usage submission.
	}
	call := r.plan.call
	job.record = relayReportRecord{RequestID: strings.Clone(call.RequestID), User: jsplugin.TransformUser{ID: int(call.UserID), Username: r.username}, Token: jsplugin.TransformToken{ID: int(call.TokenID), Name: strings.Clone(call.TokenName)}, StartedAt: r.started.UnixMilli(), DurationMS: time.Since(r.started).Milliseconds(), FirstResponseMS: r.first, Records: slices.Clone(r.plan.records)}
	if job.record.Records == nil {
		job.record.Records = []RequestPluginRecord{}
	}
	if r.hasRequestProjection {
		projected, inlineMedia := taskReportRequest(r.requestBody)
		job.record.Request.Body = projected
		combined := inlineMedia
		for _, item := range r.requestMedia {
			item.Index = len(combined)
			combined = append(combined, item)
		}
		if len(combined) > 0 {
			job.record.Request.Media = combined
		}
	} else {
		job.record.Request.Body, job.record.Request.Media = taskReportRequest(body)
	}
	if r.wsMode {
		status := r.wsStatus
		if status <= 0 {
			status = http.StatusOK
		}
		job.record.Response = relayReportResponse{Status: status, ContentType: "text/event-stream", Stream: true, Bytes: r.written, Truncated: r.written > int64(len(r.kept))}
	} else {
		job.record.Response = relayReportResponse{Status: r.Status(), ContentType: strings.Clone(r.Header().Get("Content-Type")), Stream: strings.HasPrefix(r.Header().Get("Content-Type"), "text/event-stream"), Bytes: r.written, Truncated: r.written > int64(len(r.kept))}
	}
	job.record.Usage = map[string]int{"promptTokens": r.usage.InputTokens, "completionTokens": r.usage.OutputTokens, "cacheTokens": r.usage.CacheReadInputTokens}
	for name, value := range map[string]int{"imageCount": r.imageCount, "imageOutput": r.usage.ImageOutputTokens, "imageCacheTokens": r.usage.ImageCacheReadTokens,
		"cacheCreationTokens": r.usage.CacheCreationInputTokens, "cacheCreation5mTokens": r.usage.CacheCreation5mTokens, "cacheCreation1hTokens": r.usage.CacheCreation1hTokens,
		"webSearchCalls": r.webSearchCalls, "audioChars": r.audioChars, "audioDurationMs": r.audioDurationMs} {
		if value > 0 {
			job.record.Usage[name] = value
		}
	}
	job.call = jsplugin.TransformContext{Stage: "report", Format: strings.Clone(call.Format), Path: strings.Clone(call.Path), Model: strings.Clone(call.Model), UpstreamModel: strings.Clone(r.upstreamModel), Group: strings.Clone(call.Group), Attempt: max(0, r.plan.nextAttempt-1), User: job.record.User, Token: &job.record.Token}
	if r.accountID > 0 {
		job.call.Channel = &jsplugin.TransformChannel{ID: int(r.accountID), Type: r.channelType}
	}
	for _, item := range r.targets {
		job.targets = append(job.targets, relayReportTarget{id: item.ID, name: strings.Clone(item.Name), source: item.Version.Source, config: bytes.Clone(item.Config), hosts: slices.Clone(item.Version.Meta.AllowedHosts)})
	}
	e.mu.Lock()
	if e.closed || len(e.queue) == cap(e.queue) {
		e.bytes -= size
		e.mu.Unlock()
		r.service.dropTaskReport("http_capacity")
		return
	}
	e.queue <- taskReportJob{relay: job, size: size}
	e.accepted.Add(1)
	e.mu.Unlock()
}

func (e *taskReportExecutor) runRelay(job *relayReportJob) {
	if !e.awaitRelayBilling(job, relayReportSettlementWait) {
		return
	}
	response := &job.record.Response
	if response.Stream {
		response.Events, response.Truncated = relayReportEvents(job.response, response.Truncated)
	} else if !response.Truncated {
		response.Body = relayReportBody(response.ContentType, job.response)
	}
	if response.Status >= 400 {
		// No upstream exception is consulted. Only client-visible error fields
		// are copied, without response headers, URLs or private service facts.
		v := gjson.GetBytes(response.Body, "error")
		if !v.IsObject() && len(response.Events) > 0 {
			for i := len(response.Events) - 1; i >= 0; i-- {
				if candidate := gjson.GetBytes(response.Events[i].Data, "error"); candidate.IsObject() {
					v = candidate
					break
				}
			}
		}
		if v.IsObject() {
			job.record.Error = map[string]any{"status": response.Status, "type": v.Get("type").String(), "code": v.Get("code").Value(), "message": v.Get("message").String()}
		}
	}
	job.record.Cost = newAPIReportCost(job.billing.amount)
	raw, err := json.Marshal(job.record)
	if err != nil || len(raw) > runner.MaxResultBytes {
		e.service.dropTaskReport("http_record_limit")
		return
	}
	for _, target := range job.targets {
		if e.ctx.Err() != nil {
			return
		}
		callCtx, cancel := context.WithTimeout(e.ctx, 35*time.Second)
		err = e.service.pool.With(callCtx, target.source, func(client *runner.Client) error {
			config, err := client.ValidateConfig(callCtx, target.config, true)
			if err != nil {
				return errors.New("invalid report config")
			}
			call := job.call
			call.Instance = jsplugin.TransformInstance{ID: strconv.FormatInt(target.id, 10), Name: target.name}
			call.Config = config
			hosts := append(slices.Clone(target.hosts), client.Meta.ConfigSchema.URLHosts(config)...)
			_, err = client.Typed(callCtx, e.service.requestPluginFetcher(hosts), "CallReport", call, json.RawMessage(raw))
			return err
		})
		cancel()
		if err != nil {
			e.reportFailure(target.id, "http_execution")
		}
	}
}

// New API quota is an integer compatibility unit. USD retains the exact
// settled local amount; the rounded quota is never written back to billing.
func newAPIReportCost(amount decimal.Decimal) map[string]json.Number {
	return map[string]json.Number{"usd": json.Number(amount.String()), "quota": json.Number(amount.Mul(decimal.NewFromInt(500000)).Round(0).String())}
}

func (e *taskReportExecutor) awaitRelayBilling(job *relayReportJob, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-e.ctx.Done():
		e.service.dropTaskReport("http_shutdown")
		return false
	case <-timer.C:
		e.service.dropTaskReport("http_settlement_timeout")
		return false
	case <-job.billing.done:
		if !job.billing.confirmed {
			e.service.dropTaskReport("http_settlement_unconfirmed")
			return false
		}
		return true
	}
}

func relayReportBody(contentType string, body []byte) json.RawMessage {
	if len(body) == 0 {
		return nil
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	isJSON := mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
	if isJSON && relayReportJSON(body) {
		return body
	}
	if (!isJSON && !strings.HasPrefix(mediaType, "text/") && mediaType != "application/xml" && !strings.HasSuffix(mediaType, "+xml")) || !utf8.Valid(body) {
		return nil
	}
	raw, _ := json.Marshal(string(body))
	return raw
}

// Keep complete SSE frames only. A final unfinished frame is never promoted
// into an event, even on a clean EOF. Event count also bounds parsed overhead.
func relayReportEvents(body []byte, truncated bool) ([]relayReportEvent, bool) {
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	events := []relayReportEvent{}
	for text != "" {
		block, rest, complete := strings.Cut(text, "\n\n")
		if !complete {
			return events, true
		}
		text = rest
		var event relayReportEvent
		var lines []string
		lineCount := 0
		for line := range strings.SplitSeq(block, "\n") {
			lineCount++
			if lineCount > 4096 {
				return events, true
			}
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				event.Event = value
			case "data":
				lines = append(lines, value)
			}
		}
		if lines == nil {
			continue
		}
		payload := strings.Join(lines, "\n")
		if payload == "[DONE]" {
			continue
		}
		if len(events) == 1024 {
			return events, true
		}
		if relayReportJSON([]byte(payload)) {
			event.Data = json.RawMessage(payload)
		} else {
			event.Data, _ = json.Marshal(payload)
		}
		events = append(events, event)
	}
	return events, truncated
}

func relayReportJSON(body []byte) bool {
	if !json.Valid(body) {
		return false
	}
	nodes := 0
	var visit func(gjson.Result, int) bool
	visit = func(value gjson.Result, depth int) bool {
		nodes++
		if nodes > 16384 || depth > 64 {
			return false
		}
		valid := true
		if value.IsObject() || value.IsArray() {
			seen := map[string]bool{}
			value.ForEach(func(key, child gjson.Result) bool {
				if value.IsObject() {
					name := key.String()
					if seen[name] {
						valid = false
						return false
					}
					seen[name] = true
				}
				valid = visit(child, depth+1)
				return valid
			})
		}
		return valid
	}
	return visit(gjson.ParseBytes(body), 0)
}
