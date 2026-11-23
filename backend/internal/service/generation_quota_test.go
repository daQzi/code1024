package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type generationQuotaDeliveryRepo struct {
	event     *GenerationQuotaEvent
	completed []bool
	err       error
}

func (r *generationQuotaDeliveryRepo) ClaimGenerationQuota(_ context.Context, token string, _ time.Duration) (*GenerationQuotaEvent, error) {
	r.event.LeaseToken = token
	return r.event, nil
}
func (r *generationQuotaDeliveryRepo) CompleteGenerationQuota(_ context.Context, _ *GenerationQuotaEvent, delivered bool) error {
	r.completed = append(r.completed, delivered)
	return r.err
}

type generationQuotaDeliveryCache struct {
	BillingCache
	calls int
	err   error
}

func (c *generationQuotaDeliveryCache) ApplyGenerationQuota(context.Context, *GenerationQuotaEvent, time.Duration, bool) error {
	c.calls++
	return c.err
}

func TestGenerationQuotaDeliveryOnlyAcknowledgesSuccessfulRedisApply(t *testing.T) {
	cache := &generationQuotaDeliveryCache{err: errors.New("cache offline")}
	repo := &generationQuotaDeliveryRepo{event: &GenerationQuotaEvent{ID: 1}}
	s := &GenerationService{cache: &BillingCacheService{cache: cache}}
	require.Error(t, s.deliverGenerationQuota(context.Background(), repo))
	require.Equal(t, []bool{false}, repo.completed)
	cache.err = nil
	require.NoError(t, s.deliverGenerationQuota(context.Background(), repo))
	require.Equal(t, []bool{false, true}, repo.completed)
	require.Equal(t, 2, cache.calls)
	repo.err = errors.New("database acknowledgement failed")
	require.Error(t, s.deliverGenerationQuota(context.Background(), repo))
	require.NotEmpty(t, repo.event.LeaseToken)
	// A missing cache leaves durable work pending instead of pretending that
	// delivery succeeded or applying the old unprotected increment fallback.
	s.cache = nil
	require.Error(t, s.deliverGenerationQuota(context.Background(), repo))
	require.False(t, repo.completed[len(repo.completed)-1])
}
