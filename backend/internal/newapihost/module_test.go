package newapihost

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/stretchr/testify/require"
)

func TestStopSupportsPartialAssemblyAndConcurrentCleanup(t *testing.T) {
	host := &Services{Generations: service.NewGenerationHostService(nil, nil, nil, nil, nil, nil)}
	var calls sync.WaitGroup
	for range 8 {
		calls.Add(1)
		go func() { defer calls.Done(); host.Stop() }()
	}
	calls.Wait()
	var absent *Services
	absent.Stop()
}

type nativeLifecycleAccounts struct{ service.AccountRepository }

// Only native claim methods are implemented. Starting native workers must not
// require a JS runtime, plugin service or the engine task repository interface.
type nativeLifecycleStore struct{ claims chan context.Context }

func (s *nativeLifecycleStore) ClaimNativeVideoPoll(ctx context.Context, _ string, _ time.Duration) (*service.NativeVideoPollTask, error) {
	s.claims <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *nativeLifecycleStore) ClaimNativeVideoSettlement(ctx context.Context, _ string, _ time.Duration) (*service.NativeVideoSettlement, error) {
	s.claims <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*nativeLifecycleStore) SaveNativeVideoPoll(context.Context, *service.NativeVideoPollTask, service.NativeVideoPollUpdate) error {
	return errors.New("no native task was claimed")
}

func (*nativeLifecycleStore) SaveNativeVideoSettlement(context.Context, *service.NativeVideoSettlement, string, string, time.Duration) error {
	return errors.New("no native settlement was claimed")
}

type engineLifecycleStore struct {
	generation.Store
	claims chan context.Context
}

func (s *engineLifecycleStore) Claim(ctx context.Context, _ string, _ time.Duration) (*generation.Task, error) {
	s.claims <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestNativeAndEngineWorkersStopIndependently(t *testing.T) {
	for _, engineFirst := range []bool{true, false} {
		name := "native first"
		if engineFirst {
			name = "engine first"
		}
		t.Run(name, func(t *testing.T) {
			nativeStore := &nativeLifecycleStore{claims: make(chan context.Context, 4)}
			native := service.NewNativeVideoWorkers(nativeStore, nativeStore, &nativeLifecycleAccounts{}, &service.OpenAIGatewayService{}, nil)
			t.Cleanup(native.Stop)
			engineStore := &engineLifecycleStore{claims: make(chan context.Context, 2)}
			host := &Services{Generations: service.NewGenerationHostService(nil, nil, nil, nil, nil, nil)}
			require.NoError(t, host.Generations.StartTaskExecutor(generation.NewExecutor(generation.ExecutorOptions{Store: engineStore})))
			t.Cleanup(host.Stop)

			capture := func(ch <-chan context.Context, count int) []context.Context {
				contexts := make([]context.Context, 0, count)
				deadline := time.NewTimer(4 * time.Second)
				defer deadline.Stop()
				for range count {
					select {
					case ctx := <-ch:
						contexts = append(contexts, ctx)
					case <-deadline.C:
						t.Fatal("worker did not reach its own claim method")
					}
				}
				return contexts
			}
			nativeContexts, engineContexts := capture(nativeStore.claims, 4), capture(engineStore.claims, 2)
			if engineFirst {
				host.Stop()
				for _, ctx := range nativeContexts {
					require.NoError(t, ctx.Err(), "stopping New API must not cancel accepted native work")
				}
				native.Stop()
			} else {
				native.Stop()
				for _, ctx := range engineContexts {
					require.NoError(t, ctx.Err(), "stopping native workers must not cancel engine work")
				}
				host.Stop()
			}
			for _, ctx := range append(nativeContexts, engineContexts...) {
				require.ErrorIs(t, ctx.Err(), context.Canceled, "each owner must cancel and drain its own I/O")
			}
		})
	}
}
