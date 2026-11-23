package service

import (
	"context"
	"sync"
)

// NativeVideoWorkers maintains already accepted native videos independently of
// the New API engine. It can only claim native GET/settlement work; it cannot
// submit a generation, execute a JS plugin or claim an engine task.
type NativeVideoWorkers struct {
	accounts AccountRepository
	gateway  *OpenAIGatewayService
	keys     *APIKeyService
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	once     sync.Once
}

func NewNativeVideoWorkers(polls NativeVideoPollRepository, settlements NativeVideoSettlementRepository, accounts AccountRepository, gateway *OpenAIGatewayService, keys *APIKeyService) *NativeVideoWorkers {
	ctx, cancel := context.WithCancel(context.Background())
	s := &NativeVideoWorkers{accounts: accounts, gateway: gateway, keys: keys, cancel: cancel}
	if polls != nil && gateway != nil && accounts != nil {
		for range 2 {
			s.wg.Add(1)
			go s.workNativeVideos(ctx, polls)
		}
	}
	if settlements != nil && gateway != nil {
		for range 2 {
			s.wg.Add(1)
			go s.workNativeVideoSettlements(ctx, settlements)
		}
	}
	return s
}

func (s *NativeVideoWorkers) Stop() {
	if s != nil {
		s.once.Do(func() {
			if s.cancel != nil {
				s.cancel()
			}
			s.wg.Wait()
		})
	}
}
