package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type relayPerformanceRepoFixture struct {
	mu        sync.Mutex
	rows      []RelayPerformanceSample
	writes    int
	failFirst bool
	block     chan struct{}
	started   chan struct{}
	startOnce sync.Once
}

func (r *relayPerformanceRepoFixture) WriteRelayPerformance(ctx context.Context, samples []RelayPerformanceSample) error {
	if r.started != nil {
		r.startOnce.Do(func() { close(r.started) })
	}
	if r.block != nil {
		select {
		case <-r.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes++
	if r.failFirst && r.writes == 1 {
		return errors.New("ambiguous write")
	}
	r.rows = append(r.rows, samples...)
	return nil
}
func (*relayPerformanceRepoFixture) CleanupRelayPerformance(context.Context, time.Time, int) error {
	return nil
}
func relaySample() RelayPerformanceSample {
	return RelayPerformanceSample{ID: uuid.NewString(), GroupID: 1, Platform: "openai", Model: "model", Success: true, DurationMs: 200, CreatedAt: time.Now()}
}
func TestRelayPerformanceRecorderDrainsRetriesAndOwnsData(t *testing.T) {
	repo := &relayPerformanceRepoFixture{failFirst: true}
	recorder := NewRelayPerformanceRecorder(repo)
	sample := relaySample()
	first := int64(100)
	sample.FirstTokenMs = &first
	recorder.Record(sample)
	first = 9999
	recorder.Stop()
	recorder.Stop()
	require.EqualValues(t, 1, recorder.Stats().Persisted)
	require.EqualValues(t, 0, recorder.Stats().Dropped)
	require.Len(t, repo.rows, 1)
	require.EqualValues(t, 100, *repo.rows[0].FirstTokenMs)
	require.Equal(t, 2, repo.writes)
	recorder.Record(relaySample())
	require.EqualValues(t, 1, recorder.Stats().Dropped)
}
func TestRelayPerformanceRecorderBoundsAndConcurrentShutdown(t *testing.T) {
	repo := &relayPerformanceRepoFixture{block: make(chan struct{}), started: make(chan struct{})}
	recorder := NewRelayPerformanceRecorder(repo)
	for i := 0; i < 128; i++ {
		recorder.Record(relaySample())
	}
	<-repo.started
	for i := 0; i < 1200; i++ {
		recorder.Record(relaySample())
	}
	require.EqualValues(t, 1152, recorder.Stats().Accepted)
	require.EqualValues(t, 176, recorder.Stats().Dropped)
	close(repo.block)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				recorder.Record(relaySample())
			}
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); recorder.Stop() }()
	wg.Wait()
	require.Equal(t, recorder.Stats().Accepted, recorder.Stats().Persisted)
	require.LessOrEqual(t, len(recorder.queue), 1024)
}
func TestRelayPerformanceRecorderCancellationReleasesWorker(t *testing.T) {
	repo := &relayPerformanceRepoFixture{block: make(chan struct{}), started: make(chan struct{})}
	recorder := NewRelayPerformanceRecorder(repo)
	for i := 0; i < 128; i++ {
		recorder.Record(relaySample())
	}
	<-repo.started
	recorder.mu.Lock()
	recorder.closed = true
	close(recorder.queue)
	recorder.mu.Unlock()
	recorder.cancel()
	select {
	case <-recorder.done:
	case <-time.After(time.Second):
		t.Fatal("worker did not cancel database work")
	}
	require.EqualValues(t, 128, recorder.Stats().Dropped)
}
func TestRelayPerformanceSampleValidation(t *testing.T) {
	s := relaySample()
	require.True(t, s.Valid())
	s.Model = string(make([]byte, 101))
	require.False(t, s.Valid())
	s = relaySample()
	s.DurationMs = -1
	require.False(t, s.Valid())
	s = relaySample()
	s.OutputTokens = -1
	require.False(t, s.Valid())
}
