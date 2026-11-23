package service

import (
	"context"
	"encoding/json"
)

type PluginCredentialDisable struct {
	AccountID       int64
	PluginVersionID int64
	Connection      json.RawMessage
	Reason          string
}

type PluginCredentialErrorRepository interface {
	SetPluginCredentialError(context.Context, PluginCredentialDisable, string) error
}

func (s *RateLimitService) setUpstreamAccountError(ctx context.Context, account *Account, message, reason string) error {
	if len(account.newAPIProviderBinding().Routes) > 0 {
		if repo, ok := s.accountRepo.(PluginCredentialErrorRepository); ok {
			return repo.SetPluginCredentialError(ctx, PluginCredentialDisable{AccountID: account.ID, PluginVersionID: account.NewAPIPluginVersionID(), Connection: pluginCredentialConnection(account), Reason: reason}, message)
		}
	}
	return s.accountRepo.SetError(ctx, account.ID, message)
}

func pluginCredentialNativeDisabled(account *Account, state *PluginCredentialState) bool {
	return account != nil && state != nil && account.Status == StatusError && !account.Schedulable && state.NativeDisabledAt != nil && state.NativeDisabledAt.Equal(account.UpdatedAt)
}
