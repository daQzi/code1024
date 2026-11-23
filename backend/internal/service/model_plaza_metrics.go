package service

import (
	"context"
	"time"
)

type PlazaMetricsPoint struct {
	GroupID         int64               `json:"group_id"`
	Bucket          *time.Time          `json:"bucket,omitempty"`
	Requests        int64               `json:"requests"`
	Successes       int64               `json:"successes"`
	AverageMs       *float64            `json:"average_ms"`
	P95Ms           *float64            `json:"p95_ms"`
	FirstTokenMs    *float64            `json:"first_token_ms"`
	TokensPerSecond *float64            `json:"tokens_per_second"`
	Recent          []PlazaHealthBucket `json:"recent,omitempty"`
}

type PlazaHealthBucket struct {
	Bucket    time.Time `json:"bucket"`
	Requests  int64     `json:"requests"`
	Successes int64     `json:"successes"`
}

type PlazaMetrics struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Scope documents the available historical source. Asynchronous video
	// submissions are included only after a durable terminal event exists.
	Scope  string              `json:"scope"`
	Points []PlazaMetricsPoint `json:"points"`
}

type ModelPlazaMetricsRepository interface {
	ReadModelMetrics(context.Context, string, string, []int64, time.Time, time.Time) ([]PlazaMetricsPoint, error)
	ReadModelSummaries(context.Context, []PlazaMetricsScope, time.Time, time.Time) (map[string]PlazaMetricsPoint, error)
}

type PlazaMetricsScope struct {
	ID       string
	Model    string
	Platform string
	GroupIDs []int64
}

type PlazaMetricsSummaries struct {
	Start  time.Time                    `json:"start"`
	End    time.Time                    `json:"end"`
	Models map[string]PlazaMetricsPoint `json:"models"`
}

func (s *ModelPlazaService) PerformanceSummaries(ctx context.Context, scopes []PlazaMetricsScope) (*PlazaMetricsSummaries, error) {
	end := time.Now().UTC()
	result := &PlazaMetricsSummaries{Start: end.Add(-24 * time.Hour), End: end, Models: map[string]PlazaMetricsPoint{}}
	if s.metrics == nil || len(scopes) == 0 {
		return result, nil
	}
	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	models, err := s.metrics.ReadModelSummaries(queryCtx, scopes, result.Start, result.End)
	if err != nil {
		return nil, err
	}
	for _, scope := range scopes {
		result.Models[scope.ID] = models[scope.ID]
	}
	return result, nil
}

func (s *ModelPlazaService) Performance(ctx context.Context, model, platform string, groupIDs []int64) (*PlazaMetrics, error) {
	end := time.Now().UTC()
	result := &PlazaMetrics{Start: end.Add(-24 * time.Hour), End: end, Scope: "final_relay_and_terminal_generation_with_legacy", Points: []PlazaMetricsPoint{}}
	if s.metrics == nil || len(groupIDs) == 0 {
		return result, nil
	}
	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	points, err := s.metrics.ReadModelMetrics(queryCtx, model, platform, groupIDs, result.Start, result.End)
	if err != nil {
		return nil, err
	}
	if points != nil {
		result.Points = points
	}
	return result, nil
}
