package mediahost

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func testMemoryAdmission(budget *memoryBudget) MemoryAdmission {
	return func(ctx context.Context, n int64) (context.Context, func(), error) {
		release, err := budget.reserve(n)
		return ctx, release, err
	}
}

func TestWorkingMemoryReservesBothBudgetsAndReleasesOnce(t *testing.T) {
	global, host := &memoryBudget{limit: 100}, &memoryBudget{limit: 50}
	type key struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "identity"))
	defer cancel()
	child, release, err := reserveWorkingMemory(ctx, 50, global, testMemoryAdmission(host))
	require.NoError(t, err)
	require.Equal(t, "identity", child.Value(key{}))
	require.EqualValues(t, 50, global.used)
	require.EqualValues(t, 50, host.used)
	_, _, err = reserveWorkingMemory(ctx, 1, global, testMemoryAdmission(host))
	require.ErrorIs(t, err, ErrMemoryCapacity)
	require.EqualValues(t, 50, global.used, "failed host admission refunds its global reservation")
	cancel()
	require.ErrorIs(t, child.Err(), context.Canceled)
	release()
	release()
	require.Zero(t, global.used)
	require.Zero(t, host.used)
}

func TestWorkingMemoryRejectsInvalidAdmissionAndPreservesHostError(t *testing.T) {
	for _, missingContext := range []bool{false, true} {
		global := &memoryBudget{limit: 100}
		failure := errors.New("host exhausted")
		releases := 0
		_, release, err := reserveWorkingMemory(context.Background(), 50, global, func(ctx context.Context, n int64) (context.Context, func(), error) {
			if missingContext {
				return nil, func() { releases++ }, nil
			}
			return ctx, func() { releases++ }, failure
		})
		require.Error(t, err)
		if !missingContext {
			require.ErrorIs(t, err, failure)
		}
		release()
		require.Equal(t, 1, releases)
		require.Zero(t, global.used)
	}
	global := &memoryBudget{limit: 1}
	_, release, err := reserveWorkingMemory(context.Background(), 2, global, func(context.Context, int64) (context.Context, func(), error) {
		t.Fatal("global rejection must happen before invoking host admission")
		return nil, nil, nil
	})
	require.ErrorIs(t, err, ErrMemoryCapacity)
	release()
}

func TestWorkingMemoryStandaloneLimitsAndConcurrentRelease(t *testing.T) {
	global := &memoryBudget{limit: 100}
	for _, n := range []int64{-1, 0, 101, 1<<63 - 1} {
		_, err := global.reserve(n)
		require.ErrorIs(t, err, ErrMemoryCapacity)
	}
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			_, release, err := reserveWorkingMemory(context.Background(), 50, global, nil)
			if err != nil {
				require.ErrorIs(t, err, ErrMemoryCapacity)
				return
			}
			release()
			release()
		})
	}
	wg.Wait()
	require.Zero(t, global.used)
}
