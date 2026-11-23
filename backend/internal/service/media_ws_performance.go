package service

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/relaymetrics"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type mediaPerformanceSink interface{ Record(RelayPerformanceSample) }

// A voice session can contain many turns. Keep at most eight simultaneous
// response IDs, never audio, transcripts, credentials or connection objects.
// A stable session/response ID also deduplicates repeated Live sideband events.
type mediaWSPerformance struct {
	mu       sync.Mutex
	sink     mediaPerformanceSink
	session  string
	group    int64
	platform string
	model    string
	turns    [8]mediaWSTurn
}

type mediaWSTurn struct {
	id      string
	started time.Time
}

func (s *OpenAIGatewayService) newMediaWSPerformance(session string, group int64, platform, model string) *mediaWSPerformance {
	if s == nil || s.performanceRecorder == nil || group <= 0 || len(model) == 0 || len(model) > 100 || !utf8.ValidString(model) {
		return nil
	}
	return &mediaWSPerformance{sink: s.performanceRecorder, session: strings.Clone(session), group: group, platform: platform, model: strings.Clone(model)}
}

// observe is called only after forwarding a frame, or by Live's authenticated
// background observer. SDP acceptance/session.close is never generation proof.
func (p *mediaWSPerformance) observe(body []byte, now time.Time) {
	if p == nil || int64(len(body)) > openAIWSMessageReadLimitBytes || !gjson.ValidBytes(body) {
		return
	}
	event := gjson.GetBytes(body, "type").String()
	switch event {
	case "response.created", "response.done", "response.completed", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
	default:
		return
	}
	id := gjson.GetBytes(body, "response.id").String()
	if id == "" {
		id = gjson.GetBytes(body, "response_id").String()
	}
	if len(id) == 0 || len(id) > 128 || !utf8.ValidString(id) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	index, empty := -1, -1
	for i := range p.turns {
		if p.turns[i].id == id {
			index = i
		}
		if empty < 0 && p.turns[i].id == "" {
			empty = i
		}
	}
	if event == "response.created" {
		if index < 0 && empty >= 0 {
			p.turns[empty] = mediaWSTurn{id: strings.Clone(id), started: now}
		}
		return
	}
	if index < 0 {
		return // Missing start/reconnect, duplicate terminal or overflow: unknown.
	}
	turn := p.turns[index]
	p.turns[index] = mediaWSTurn{}
	outcome := mediaWSTerminal(body, p.platform)
	if outcome == relaymetrics.Success || outcome == relaymetrics.Failure {
		p.record(turn, outcome, now)
	}
}

func (p *mediaWSPerformance) record(turn mediaWSTurn, outcome relaymetrics.Outcome, now time.Time) {
	durationMs := now.Sub(turn.started).Milliseconds()
	if durationMs < 0 {
		durationMs = 0
	}
	p.sink.Record(RelayPerformanceSample{
		ID:      uuid.NewSHA1(uuid.NameSpaceOID, []byte(p.session+"\x00"+turn.id)).String(),
		GroupID: p.group, Platform: p.platform, Model: p.model,
		Success: outcome == relaymetrics.Success, DurationMs: durationMs, CreatedAt: now,
	})
}

// Only Realtime calls this on a known upstream interruption. A Live control
// outage cannot prove that independent WebRTC media has failed.
func (p *mediaWSPerformance) failActive(now time.Time) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, turn := range p.turns {
		if turn.id != "" {
			p.turns[i] = mediaWSTurn{}
			p.record(turn, relaymetrics.Failure, now)
		}
	}
}

func mediaWSTerminal(body []byte, platform string) relaymetrics.Outcome {
	event := gjson.GetBytes(body, "type").String()
	status := gjson.GetBytes(body, "response.status").String()
	if status == "cancelled" || status == "canceled" || event == "response.cancelled" || event == "response.canceled" {
		return relaymetrics.Ignored
	}
	// Realtime's incomplete reason/error live under status_details, unlike
	// the HTTP Responses DTO. Never promote an unknown final status to success.
	if status == "failed" {
		if relaymetrics.IgnoreFailure(gjson.GetBytes(body, "response.status_details.error.code").String(), gjson.GetBytes(body, "response.status_details.error.type").String(), 0) {
			return relaymetrics.Ignored
		}
		return relaymetrics.Failure
	}
	if status == "incomplete" {
		switch gjson.GetBytes(body, "response.status_details.reason").String() {
		case "max_output_tokens", "max_tokens":
			return relaymetrics.Success
		case "content_filter", "safety", "content_policy_violation":
			return relaymetrics.Ignored
		default:
			return relaymetrics.Failure
		}
	}
	if status != "completed" && (platform != PlatformGrok || status != "" || gjson.GetBytes(body, "type").String() != "response.done") {
		return relaymetrics.Failure
	}
	return relaymetrics.Terminal(body)
}
