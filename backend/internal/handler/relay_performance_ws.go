package handler

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/relaymetrics"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const relayPerformanceSinkKey = "relay_performance_sink"

// One active slot plus one accepted successor, shared by reader/relay callbacks. A failed
// attempt remains pending across failover; only a terminal or final close emits
// a sample. No request, account, payload, credentials or growing turn map.
type wsRelayPerformance struct {
	mu          sync.Mutex
	sink        relayPerformanceSink
	group       int64
	platform    string
	model       string
	started     time.Time
	active      bool
	ignored     bool
	outcome     relaymetrics.Outcome
	nextModel   string
	nextStarted time.Time
}

func newWSRelayPerformance(c *gin.Context, groupID *int64, platform, model string, started time.Time) *wsRelayPerformance {
	sink, _ := c.Get(relayPerformanceSinkKey)
	writer, _ := sink.(relayPerformanceSink)
	if writer == nil || groupID == nil || *groupID <= 0 || len(platform) == 0 || len(platform) > 32 || !utf8.ValidString(platform) {
		return nil
	}
	p := &wsRelayPerformance{sink: writer, group: *groupID, platform: strings.Clone(platform)}
	p.begin(model, started)
	return p
}

func (p *wsRelayPerformance) begin(model string, started time.Time) {
	p.beginOrQueue(model, started, false)
}

func (p *wsRelayPerformance) next(model string, started time.Time) {
	p.beginOrQueue(model, started, true)
}

func (p *wsRelayPerformance) beginOrQueue(model string, started time.Time, successor bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(model) == 0 || len(model) > 100 || !utf8.ValidString(model) {
		return
	}
	if started.IsZero() {
		started = time.Now()
	}
	if p.active {
		// Passthrough may defer a bare error until response.failed or the next
		// response.created. Keep the newly accepted turn separate from it.
		if successor && p.nextModel == "" {
			p.nextModel, p.nextStarted = strings.Clone(model), started
		}
		return // retries keep the original acceptance clock
	}
	p.model, p.started, p.active, p.outcome = strings.Clone(model), started, true, relaymetrics.Unknown
	p.ignored = false
}

func (p *wsRelayPerformance) ignore() {
	if p != nil {
		p.mu.Lock()
		p.ignored = true
		p.mu.Unlock()
	}
}

func (p *wsRelayPerformance) attemptFailed(err error) {
	if p == nil || err == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active || p.ignored {
		return
	}
	outcome := wsFailureOutcome(err)
	if p.outcome != relaymetrics.Failure || outcome != relaymetrics.Ignored {
		p.outcome = outcome
	}
}

func wsFailureOutcome(err error) relaymetrics.Outcome {
	if errors.Is(err, context.Canceled) || service.IsOpenAIWSSessionPreemptedError(err) {
		return relaymetrics.Ignored
	}
	if service.OpenAIWSUpstreamTurnFailure(err) {
		return relaymetrics.Failure
	}
	if openAIWSIngressEndedByClient(err) {
		return relaymetrics.Ignored
	}
	var failover *service.UpstreamFailoverError
	if errors.As(err, &failover) {
		return relaymetrics.HTTPFailure(failover.ResponseBody, failover.StatusCode)
	}
	var closeErr *service.OpenAIWSClientCloseError
	if errors.As(err, &closeErr) && (closeErr.StatusCode() == coderws.StatusPolicyViolation || closeErr.Unwrap() == nil) {
		return relaymetrics.Ignored // local admission, policy or inter-turn idle
	}
	return relaymetrics.Failure
}

func (p *wsRelayPerformance) complete(result *service.OpenAIForwardResult, err error) {
	if p == nil {
		return
	}
	if result != nil && (result.ClientDisconnect || result.PerformanceOutcome == relaymetrics.Ignored) {
		p.mu.Lock()
		if p.active {
			p.emitLocked(relaymetrics.Ignored, nil)
		}
		p.mu.Unlock()
		return
	}
	if err != nil {
		p.attemptFailed(err)
		return
	}
	if result == nil || result.PerformanceOutcome == relaymetrics.Unknown {
		return // an idle socket/partial bill is not a completed generation
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active {
		return
	}
	outcome := result.PerformanceOutcome
	if p.ignored {
		outcome = relaymetrics.Ignored
	}
	p.emitLocked(outcome, result)
}

func (p *wsRelayPerformance) finish(ctx context.Context) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active {
		return
	}
	outcome := p.outcome
	if p.ignored || errors.Is(ctx.Err(), context.Canceled) {
		outcome = relaymetrics.Ignored
	}
	p.emitLocked(outcome, nil)
}

func (p *wsRelayPerformance) emitLocked(outcome relaymetrics.Outcome, result *service.OpenAIForwardResult) {
	p.active = false
	defer func() {
		if p.nextModel != "" {
			p.model, p.started, p.active = p.nextModel, p.nextStarted, true
			p.nextModel, p.nextStarted = "", time.Time{}
			p.outcome, p.ignored = relaymetrics.Unknown, false
		}
	}()
	if outcome == relaymetrics.Unknown || outcome == relaymetrics.Ignored {
		return
	}
	elapsed := max(int64(0), time.Since(p.started).Milliseconds())
	sample := service.RelayPerformanceSample{ID: uuid.NewString(), GroupID: p.group, Platform: p.platform, Model: p.model, Success: outcome == relaymetrics.Success, DurationMs: elapsed, CreatedAt: time.Now().UTC()}
	if result != nil {
		sample.OutputTokens = max(int64(0), int64(result.Usage.OutputTokens))
		if result.FirstTokenMs != nil {
			first := max(int64(0), int64(*result.FirstTokenMs)) + max(int64(0), elapsed-result.Duration.Milliseconds())
			sample.FirstTokenMs = &first
		}
	}
	if sample.Valid() {
		p.sink.RecordRelayPerformance(sample)
	}
}
