package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// RelayPerformanceSample contains only immutable, bounded public routing facts.
// It deliberately owns no request, response, account, credentials or user text.
type RelayPerformanceSample struct {
	ID           string    `json:"id"`
	GroupID      int64     `json:"group_id"`
	Platform     string    `json:"platform"`
	Model        string    `json:"model"`
	Success      bool      `json:"success"`
	DurationMs   int64     `json:"duration_ms"`
	FirstTokenMs *int64    `json:"first_token_ms"`
	OutputTokens int64     `json:"output_tokens"`
	CreatedAt    time.Time `json:"created_at"`
}

func (p RelayPerformanceSample) Valid() bool {
	_, err := uuid.Parse(p.ID)
	return err == nil && len(p.ID) == 36 && p.GroupID > 0 && len(p.Platform) > 0 && len(p.Platform) <= 32 && utf8.ValidString(p.Platform) && len(p.Model) > 0 && len(p.Model) <= 100 && utf8.ValidString(p.Model) && p.DurationMs >= 0 && p.DurationMs <= 604800000 && p.OutputTokens >= 0 && (p.FirstTokenMs == nil || *p.FirstTokenMs >= 0 && *p.FirstTokenMs <= 604800000) && !p.CreatedAt.IsZero()
}

type RelayPerformanceRepository interface {
	WriteRelayPerformance(context.Context, []RelayPerformanceSample) error
	CleanupRelayPerformance(context.Context, time.Time, int) error
}

type RelayPerformanceStats struct{ Accepted, Persisted, Dropped uint64 }

// One worker, at most 1024 queued + 128 in-flight samples, each with <=132
// routing bytes. Shutdown seals admission and drains under one five-second
// deadline. Telemetry failure cannot roll back a generated response or money.
type RelayPerformanceRecorder struct {
	repo        RelayPerformanceRepository
	mu          sync.RWMutex
	closed      bool
	queue       chan RelayPerformanceSample
	done        chan struct{}
	cancel      context.CancelFunc
	stopOnce    sync.Once
	accepted    atomic.Uint64
	persisted   atomic.Uint64
	dropped     atomic.Uint64
	lastWarning atomic.Int64
}

func NewRelayPerformanceRecorder(repo RelayPerformanceRepository) *RelayPerformanceRecorder {
	if repo == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &RelayPerformanceRecorder{repo: repo, queue: make(chan RelayPerformanceSample, 1024), done: make(chan struct{}), cancel: cancel}
	go s.run(ctx)
	return s
}
func (s *RelayPerformanceRecorder) Stats() RelayPerformanceStats {
	if s == nil {
		return RelayPerformanceStats{}
	}
	return RelayPerformanceStats{s.accepted.Load(), s.persisted.Load(), s.dropped.Load()}
}
func (s *RelayPerformanceRecorder) drop(count int) {
	s.dropped.Add(uint64(count))
	now := time.Now().Unix()
	previous := s.lastWarning.Load()
	if now-previous >= 30 && s.lastWarning.CompareAndSwap(previous, now) {
		slog.Warn("relay_performance_samples_dropped", "dropped_total", s.dropped.Load())
	}
}
func (s *RelayPerformanceRecorder) Record(sample RelayPerformanceSample) {
	if s == nil || !sample.Valid() {
		return
	}
	// Detach strings and optional scalar from the handler's ownership.
	sample.Platform = strings.Clone(sample.Platform)
	sample.Model = strings.Clone(sample.Model)
	sample.ID = strings.Clone(sample.ID)
	if sample.FirstTokenMs != nil {
		value := *sample.FirstTokenMs
		sample.FirstTokenMs = &value
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		s.drop(1)
		return
	}
	select {
	case s.queue <- sample:
		s.accepted.Add(1)
	default:
		s.drop(1)
	}
}
func (s *RelayPerformanceRecorder) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.queue)
		s.mu.Unlock()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-s.done:
		case <-timer.C:
			s.cancel()
			<-s.done
		}
		s.cancel()
	})
}
func (s *RelayPerformanceRecorder) run(ctx context.Context) {
	defer close(s.done)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	cleanup := time.NewTicker(10 * time.Second)
	defer cleanup.Stop()
	batch := make([]RelayPerformanceSample, 0, 128)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Retain the same IDs for an ambiguous write; ON CONFLICT makes retries safe.
		var err error
		for attempt := 0; attempt < 2; attempt++ {
			writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err = s.repo.WriteRelayPerformance(writeCtx, batch)
			cancel()
			if err == nil || ctx.Err() != nil {
				break
			}
		}
		if err != nil {
			s.drop(len(batch))
		} else {
			s.persisted.Add(uint64(len(batch)))
		}
		clear(batch)
		batch = batch[:0]
	}
	for {
		select {
		case sample, ok := <-s.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, sample)
			if len(batch) == cap(batch) {
				flush()
			}
		case <-tick.C:
			flush()
		case <-cleanup.C:
			cleanCtx, cancel := context.WithTimeout(ctx, time.Second)
			if err := s.repo.CleanupRelayPerformance(cleanCtx, time.Now().Add(-48*time.Hour), 5000); err != nil && ctx.Err() == nil {
				slog.Warn("relay_performance_cleanup_failed", "error_type", "database")
			}
			cancel()
		case <-ctx.Done():
			s.drop(len(batch))
			clear(batch)
			for range s.queue {
				s.drop(1)
			}
			return
		}
	}
}

func (s *ModelPlazaService) RecordRelayPerformance(sample RelayPerformanceSample) {
	if s != nil {
		s.performanceRecorder.Record(sample)
	}
}
func (s *ModelPlazaService) Stop() {
	if s != nil {
		s.performanceRecorder.Stop()
	}
}
