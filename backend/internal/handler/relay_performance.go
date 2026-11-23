package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/relaymetrics"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const relayPerformanceAttemptKey = "relay_performance_attempt"

type relayPerformanceAttempt struct {
	completed    bool
	disconnected bool
	duration     time.Duration
	first        *int64
	output       int64
	outcome      relaymetrics.Outcome
}

// These calls replace attempt evidence synchronously. Only the outer request
// boundary emits a sample, so retries and partial billing cannot count twice.
func setForwardPerformance(c *gin.Context, result *service.ForwardResult, err error) {
	p := relayPerformanceAttempt{completed: err == nil && result != nil}
	if result != nil {
		p.disconnected = result.ClientDisconnect
		p.duration = result.Duration
		p.output = int64(result.Usage.OutputTokens)
		if result.FirstTokenMs != nil {
			n := int64(*result.FirstTokenMs)
			p.first = &n
		}
	}
	c.Set(relayPerformanceAttemptKey, p)
}
func setOpenAIForwardPerformance(c *gin.Context, result *service.OpenAIForwardResult, err error) {
	p := relayPerformanceAttempt{completed: err == nil && result != nil}
	if result != nil {
		// HTTP may use upstream WS. The terminal outcome, rather than a
		// partial usage record, determines the final client-request sample.
		p.outcome = result.PerformanceOutcome
		if result.OpenAIWSMode && p.outcome == relaymetrics.Unknown {
			p.completed = false
		}
		p.disconnected = result.ClientDisconnect
		p.duration = result.Duration
		p.output = int64(result.Usage.OutputTokens)
		if result.FirstTokenMs != nil {
			n := int64(*result.FirstTokenMs)
			p.first = &n
		}
	}
	c.Set(relayPerformanceAttemptKey, p)
}

type relayPerformanceSink interface {
	RecordRelayPerformance(service.RelayPerformanceSample)
}

func relayPerformancePath(c *gin.Context) bool {
	if c.Request == nil {
		return false
	}
	path := c.Request.URL.Path
	if c.Request.Method == http.MethodGet {
		return NormalizeInboundEndpoint(path) == EndpointRealtime
	}
	if c.Request.Method != http.MethodPost {
		return false
	}
	if strings.HasSuffix(path, "/live") || strings.HasSuffix(path, "/realtime/calls") {
		return true // Only failures sample here; SDP acceptance is not a turn.
	}
	return relayMediaHTTPPath(path) || strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/messages") || strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent")
}

func relayMediaHTTPPath(path string) bool {
	switch NormalizeInboundEndpoint(path) {
	case EndpointEmbeddings, EndpointImagesGenerations, EndpointImagesEdits, EndpointVoiceTTS, EndpointVoiceSTT:
		return true
	}
	return false
}

func recordFinalRelayPerformance(c *gin.Context, w *opsCaptureWriter, sink relayPerformanceSink, start time.Time) {
	if sink == nil || !relayPerformancePath(c) {
		return
	}
	if w.deliveryFailed() {
		return // Count only results actually written to the downstream.
	}
	key := getOpsAPIKey(c)
	if key == nil || key.GroupID == nil || *key.GroupID <= 0 {
		return
	}
	model := clientRequestedModel(c, c.GetString(opsModelKey))
	platform := resolveOpsPlatform(c.Request.Context(), key, guessPlatformFromPath(c.Request.URL.Path))
	if actual, ok := c.Request.Context().Value(ctxkey.Platform).(string); ok && actual != "" {
		platform = actual
	}
	if model == "" || platform == "" {
		return
	}
	if service.GetOpsCyberPolicy(c) != nil || service.HasOpsClientBusinessLimited(c) {
		return
	}
	value, _ := c.Get(relayPerformanceAttemptKey)
	p, _ := value.(relayPerformanceAttempt)
	if p.outcome == relaymetrics.Ignored {
		return
	}
	if requestType, _ := c.Get(opsRequestTypeKey); (requestType == int16(service.RequestTypeWSV2) || requestType == int(service.RequestTypeWSV2)) && NormalizeInboundEndpoint(c.Request.URL.Path) != EndpointRealtime {
		return
	}
	status := c.Writer.Status()
	parsed := parseOpsErrorResponse(w.capturedBytes())
	if terminal, ok := w.capturedTerminalError(); ok {
		parsed = terminal
	}
	if marked := service.GetOpsStreamErrors(c); len(marked) > 0 {
		first := marked[0]
		parsed = parsedOpsError{ErrorType: first.ErrType, Code: first.Code, Message: first.Message, StreamFailure: true}
		if first.IntendedStatus >= 400 {
			status = first.IntendedStatus
		}
	}
	failed := status >= 400 || parsed.StreamFailure || !p.completed || p.outcome == relaymetrics.Failure
	// A completed response remains a sample if a proxy cancels after receipt.
	// Interrupted/failed streams on an actually canceled request do not.
	if p.disconnected || errors.Is(c.Request.Context().Err(), context.Canceled) && failed {
		return
	}
	if failed {
		if localType, code := service.OpsLocalErrorType(c); parsed.ErrorType == "" && localType != "" {
			parsed.ErrorType, parsed.Code = localType, code
		}
		_, business, _, _ := classifyOpsErrorLog(c, normalizeOpsErrorType(parsed.ErrorType, parsed.Code), parsed.Message, parsed.Code, status)
		if business {
			return
		}
		if ignoreRelayPerformanceFailure(parsed.Code, parsed.ErrorType, status) {
			return
		}
		// Only observe attempted generation or explicit upstream/routing failure.
		// This avoids turning local empty/mock responses into healthy upstreams.
		if value == nil && status < 400 && !parsed.StreamFailure {
			return
		}
	}
	elapsed := time.Since(start).Milliseconds()
	first := p.first
	if relayMediaHTTPPath(c.Request.URL.Path) {
		// An image event/audio byte/vector is not a generated text token. Do
		// not advertise image-reasoning or billable tokens as text throughput.
		first, p.output = nil, 0
	}
	if first != nil {
		// TTFT from Forward is relative to that attempt. Align it with the total
		// request clock so weighted generation throughput excludes routing/queue.
		adjusted := *first + max(int64(0), elapsed-p.duration.Milliseconds())
		first = &adjusted
	}
	sample := service.RelayPerformanceSample{ID: uuid.NewString(), GroupID: *key.GroupID, Platform: platform, Model: model, Success: !failed, DurationMs: elapsed, FirstTokenMs: first, OutputTokens: max(int64(0), p.output), CreatedAt: time.Now().UTC()}
	sink.RecordRelayPerformance(sample)
}

func ignoreRelayPerformanceFailure(code, errorType string, status int) bool {
	return relaymetrics.IgnoreFailure(code, errorType, status)
}

func (h *ModelPlazaHandler) RecordRelayPerformance(sample service.RelayPerformanceSample) {
	if h != nil {
		h.plazaService.RecordRelayPerformance(sample)
	}
}
