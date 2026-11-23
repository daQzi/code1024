package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// This event contains public quota facts only. The database has already applied
// Amount; Redis uses the row identity/cursor to avoid applying a seeded delta
// again. Events for a quota row are delivered in order across all workers.
type GenerationQuotaEvent struct {
	ID           int64
	TaskID       string
	QuotaID      int64
	UserID       int64
	Platform     string
	Amount       string
	DailyStart   time.Time
	WeeklyStart  time.Time
	MonthlyStart time.Time
	CreatedAt    time.Time
	LeaseToken   string
}

type GenerationQuotaOutboxRepository interface {
	ClaimGenerationQuota(context.Context, string, time.Duration) (*GenerationQuotaEvent, error)
	CompleteGenerationQuota(context.Context, *GenerationQuotaEvent, bool) error
}

type GenerationQuotaCache interface {
	ApplyGenerationQuota(context.Context, *GenerationQuotaEvent, time.Duration, bool) error
}

func (s *BillingCacheService) applyGenerationQuota(ctx context.Context, event *GenerationQuotaEvent) error {
	if s == nil || s.cache == nil {
		return errors.New("generation quota cache unavailable")
	}
	cache, ok := s.cache.(GenerationQuotaCache)
	if !ok {
		return errors.New("generation quota cache does not support durable delivery")
	}
	ttl, dirty := 24*time.Hour, false
	if s.cfg != nil {
		if s.cfg.Billing.UserPlatformQuotaCacheTTLSeconds > 0 {
			ttl = time.Duration(s.cfg.Billing.UserPlatformQuotaCacheTTLSeconds) * time.Second
		}
		dirty = s.cfg.Database.UserPlatformQuotaFlusherEnabled
	}
	return cache.ApplyGenerationQuota(ctx, event, ttl, dirty)
}

func (s *GenerationService) workGenerationQuota(ctx context.Context, repo GenerationQuotaOutboxRepository) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		// One bounded batch per tick; SQL orders each quota identity across
		// instances. No unbounded in-memory queue or request context is held.
		for range 32 {
			callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := s.deliverGenerationQuota(callCtx, repo)
			cancel()
			if err != nil {
				break
			}
		}
	}
}

func (s *GenerationService) deliverGenerationQuota(ctx context.Context, repo GenerationQuotaOutboxRepository) error {
	event, err := repo.ClaimGenerationQuota(ctx, uuid.NewString(), 15*time.Second)
	if err != nil {
		return err
	}
	deliverErr := s.cache.applyGenerationQuota(ctx, event)
	if err := repo.CompleteGenerationQuota(ctx, event, deliverErr == nil); err != nil {
		return err
	}
	return deliverErr
}
