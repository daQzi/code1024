//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGenerationQuotaCacheIdempotentLegacyAndLargeCursors(t *testing.T) {
	cache, mr := newMiniRedisCache(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	day, week, month := timezone.StartOfDay(now), timezone.StartOfWeek(now), now.Add(-time.Hour)
	event := &service.GenerationQuotaEvent{ID: 9007199254740993, QuotaID: 7, UserID: 8, Platform: "openai", Amount: "0.20000000", DailyStart: day, WeeklyStart: week, MonthlyStart: month, CreatedAt: now}
	// An absent cache is seeded from committed DB data on the next preflight.
	require.NoError(t, cache.ApplyGenerationQuota(ctx, event, time.Hour, true))
	require.False(t, mr.Exists(userPlatformQuotaCacheKey(8, "openai")))
	entry := &service.UserPlatformQuotaCacheEntry{SchemaVersion: service.UserPlatformQuotaCacheSchemaV1, DailyUsageUSD: 1, WeeklyUsageUSD: 1, MonthlyUsageUSD: 1, DailyWindowStart: &day, WeeklyWindowStart: &week, MonthlyWindowStart: &month}
	require.NoError(t, cache.SetUserPlatformQuotaCache(ctx, 8, "openai", entry, time.Hour))
	for range 3 {
		require.NoError(t, cache.ApplyGenerationQuota(ctx, event, time.Hour, true))
	}
	read, ok, err := cache.GetUserPlatformQuotaCache(ctx, 8, "openai")
	require.NoError(t, err)
	require.True(t, ok)
	require.InDelta(t, 1.2, read.DailyUsageUSD, 1e-9)
	require.Equal(t, event.ID, read.GenerationCursor)
	require.Equal(t, event.QuotaID, read.QuotaID)
	// Adjacent int64s above 2^53 remain different in Lua.
	event.ID++
	require.NoError(t, cache.ApplyGenerationQuota(ctx, event, time.Hour, true))
	read, _, err = cache.GetUserPlatformQuotaCache(ctx, 8, "openai")
	require.NoError(t, err)
	require.InDelta(t, 1.4, read.DailyUsageUSD, 1e-9)
	keys, err := cache.PopDirtyUserPlatformQuotaKeys(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, []service.UserPlatformQuotaKey{{UserID: 8, Platform: "openai"}}, keys)
	// A late DB seed or expired-window refresh must not undo a delivered task.
	entry.QuotaID = 7
	entry.GenerationCursor = event.ID - 1
	entry.DailyUsageUSD = 0
	require.NoError(t, cache.SetUserPlatformQuotaCache(ctx, 8, "openai", entry, time.Hour))
	read, _, err = cache.GetUserPlatformQuotaCache(ctx, 8, "openai")
	require.NoError(t, err)
	require.InDelta(t, 1.4, read.DailyUsageUSD, 1e-9)
}

func TestGenerationQuotaCacheRolloverAndQuotaIdentity(t *testing.T) {
	cache, _ := newMiniRedisCache(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	day, week := timezone.StartOfDay(now), timezone.StartOfWeek(now)
	old := day.Add(-40 * 24 * time.Hour)
	entry := &service.UserPlatformQuotaCacheEntry{QuotaID: 7, SchemaVersion: service.UserPlatformQuotaCacheSchemaV1, DailyUsageUSD: 99, WeeklyUsageUSD: 99, MonthlyUsageUSD: 99, DailyWindowStart: &old, WeeklyWindowStart: &old, MonthlyWindowStart: &old}
	require.NoError(t, cache.SetUserPlatformQuotaCache(ctx, 8, "openai", entry, time.Hour))
	event := &service.GenerationQuotaEvent{ID: 1, QuotaID: 7, UserID: 8, Platform: "openai", Amount: "0.2", DailyStart: day, WeeklyStart: week, MonthlyStart: now, CreatedAt: now}
	require.NoError(t, cache.ApplyGenerationQuota(ctx, event, time.Hour, false))
	read, _, err := cache.GetUserPlatformQuotaCache(ctx, 8, "openai")
	require.NoError(t, err)
	require.InDelta(t, .2, read.DailyUsageUSD, 1e-9)
	require.InDelta(t, .2, read.WeeklyUsageUSD, 1e-9)
	require.InDelta(t, .2, read.MonthlyUsageUSD, 1e-9)
	// Recreated quota: discard stale old-row cache instead of applying a delta
	// to old limits; the committed replacement will seed its own usage/cursor.
	event.QuotaID, event.ID = 8, 2
	require.NoError(t, cache.ApplyGenerationQuota(ctx, event, time.Hour, false))
	_, found, err := cache.GetUserPlatformQuotaCache(ctx, 8, "openai")
	require.NoError(t, err)
	require.False(t, found)
	entry.QuotaID, entry.GenerationCursor = 8, 2
	entry.DailyUsageUSD = 0
	require.NoError(t, cache.SetUserPlatformQuotaCache(ctx, 8, "openai", entry, time.Hour))
	event.QuotaID, event.ID = 7, 1
	require.NoError(t, cache.ApplyGenerationQuota(ctx, event, time.Hour, false))
	read, _, err = cache.GetUserPlatformQuotaCache(ctx, 8, "openai")
	require.NoError(t, err)
	require.Zero(t, read.DailyUsageUSD)
	keys, err := cache.PopDirtyUserPlatformQuotaKeys(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, keys)
}

func TestGenerationQuotaCacheDelayedEventDoesNotRestoreResetWindow(t *testing.T) {
	cache, _ := newMiniRedisCache(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	day, week := timezone.StartOfDay(now), timezone.StartOfWeek(now)
	reset := now.Add(time.Second)
	entry := &service.UserPlatformQuotaCacheEntry{QuotaID: 7, SchemaVersion: service.UserPlatformQuotaCacheSchemaV1, DailyWindowStart: &reset, WeeklyWindowStart: &week, MonthlyWindowStart: &day}
	require.NoError(t, cache.SetUserPlatformQuotaCache(ctx, 8, "openai", entry, time.Hour))
	event := &service.GenerationQuotaEvent{ID: 1, QuotaID: 7, UserID: 8, Platform: "openai", Amount: ".2", DailyStart: day, WeeklyStart: week, MonthlyStart: day, CreatedAt: now}
	require.NoError(t, cache.ApplyGenerationQuota(ctx, event, time.Hour, false))
	read, _, err := cache.GetUserPlatformQuotaCache(ctx, 8, "openai")
	require.NoError(t, err)
	require.Zero(t, read.DailyUsageUSD)
	require.InDelta(t, .2, read.WeeklyUsageUSD, 1e-9)
	require.Equal(t, event.ID, read.GenerationCursor)
}
