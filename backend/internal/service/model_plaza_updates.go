package service

import (
	"context"
	"errors"
	"time"
)

type ModelPerformanceVersionRepository interface {
	ReadModelPerformanceVersion(context.Context, []PlazaMetricsScope) (string, error)
}

func (s *ModelPlazaService) PerformanceVersion(ctx context.Context, scopes []PlazaMetricsScope) (string, error) {
	reader, ok := s.metrics.(ModelPerformanceVersionRepository)
	if !ok {
		return "", errors.New("model performance notifications unavailable")
	}
	queryCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return reader.ReadModelPerformanceVersion(queryCtx, scopes)
}
