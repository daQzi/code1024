// SPDX-License-Identifier: AGPL-3.0-or-later
// Credential lifecycle follows QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f, using account-bound host storage.
package service

import (
	"context"
	"sync"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
)

// Two fixed workers, a bounded queue and database claims are shared by timer,
// request-time refresh and administrator operations. Queues retain IDs only.
type pluginCredentialMaintenance struct {
	service *NewAPIPluginService
	ctx     context.Context
	cancel  context.CancelFunc
	queue   chan pluginCredentialWork
	wg      sync.WaitGroup
	stop    sync.Once
	budget  *pkghttputil.BodyMemoryBudget
}

type pluginCredentialTimerKey struct{}

type pluginCredentialWork struct {
	accountID    int64
	revocationID int64
}

func (s *NewAPIPluginService) startCredentialMaintenance() {
	if _, err := s.credentialRepository(); err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &pluginCredentialMaintenance{service: s, ctx: ctx, cancel: cancel, queue: make(chan pluginCredentialWork, 32), budget: pkghttputil.NewBodyMemoryBudget(2 * newAPIProviderMemory)}
	s.credentialMaintenance = m
	m.wg.Add(3)
	go func() {
		defer m.wg.Done()
		timer := time.NewTicker(15 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				m.enqueue()
			}
		}
	}()
	for range 2 {
		go func() {
			defer m.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case work := <-m.queue:
					m.run(work)
				}
			}
		}()
	}
}

func (m *pluginCredentialMaintenance) enqueue() {
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()
	repo, _ := m.service.credentialRepository()
	ids, _ := repo.DuePluginCredentials(ctx)
	var retired []int64
	if cleanup, ok := m.service.repo.(PluginCredentialRevocationRepository); ok {
		retired, _ = cleanup.DuePluginCredentialRevocations(ctx)
	}
	// Interleave the two classes. A busy live account must not starve grants
	// whose owning account was removed, or vice versa.
	for i := range 16 {
		if i < len(ids) {
			select {
			case m.queue <- pluginCredentialWork{accountID: ids[i]}:
			default:
			}
		}
		if i < len(retired) {
			select {
			case m.queue <- pluginCredentialWork{revocationID: retired[i]}:
			default:
			}
		}
	}
}

func (m *pluginCredentialMaintenance) run(work pluginCredentialWork) {
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, pluginCredentialTimerKey{}, true)
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(ctx, m.budget)
	defer release()
	if ctx.Err() != nil {
		return
	}
	if work.revocationID > 0 {
		if pkghttputil.ReserveRequestBodyMemory(ctx, newAPIProviderMemory) != nil {
			return
		}
		m.service.runCredentialRevocation(ctx, work.revocationID)
		return
	}
	id := work.accountID
	repo, _ := m.service.credentialRepository()
	state, err := repo.ReadPluginCredential(ctx, id)
	if err != nil {
		return
	}
	action, revision := "scheduled", int64(0)
	if state != nil {
		if state.NextRunAt.After(time.Now()) {
			return
		}
		revision = state.Revision
		payload, err := m.service.readCredentialPayload(state)
		if err != nil {
			return
		}
		if payload.View.Status == "revoke_pending" {
			action = "revoke"
		}
	}
	_, _ = m.service.ManagePluginCredential(ctx, id, revision, "", "", action, PluginLoginInput{})
}

func (m *pluginCredentialMaintenance) close() {
	if m != nil {
		m.stop.Do(func() { m.cancel(); m.wg.Wait() })
	}
}
