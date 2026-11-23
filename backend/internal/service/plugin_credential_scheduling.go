package service

import (
	"context"
	"encoding/json"
	"slices"
	"time"
)

// Scheduling reads only the public projection, never decrypts a grant and never
// calls a hook. The actual request still validates the connection and refreshes
// its credential after it owns the account's concurrency slot.
type PluginCredentialScheduling struct {
	AccountID       int64
	PluginVersionID int64
	Connection      json.RawMessage
	Enabled         bool
	RequiresLogin   bool
	CanRefresh      bool
	Public          json.RawMessage
}

type PluginCredentialSchedulingRepository interface {
	ReadPluginCredentialScheduling(context.Context, []int64) ([]PluginCredentialScheduling, error)
}

func (state PluginCredentialScheduling) usable(account *Account, now time.Time) bool {
	if account == nil || !state.Enabled || state.PluginVersionID != account.NewAPIPluginVersionID() || !samePluginCredentialConnection(state.Connection, pluginCredentialConnection(account)) {
		return false
	}
	var view PluginCredentialView
	if json.Unmarshal(state.Public, &view) != nil {
		return false
	}
	if slices.Contains([]string{"revoked", "revoke_pending", "login_required"}, view.Status) || view.Status == "auto_disabled" && pluginCredentialAutoDisable(account) {
		return false
	}
	if view.Credential == nil {
		return !state.RequiresLogin || account.GetCredential("api_key") != ""
	}
	if view.Credential.NeedsLogin {
		return false
	}
	return view.Credential.ExpiresAt <= 0 || view.Credential.ExpiresAt > now.Unix() || state.CanRefresh
}

func (s *OpenAIGatewayService) filterPluginCredentialAccounts(ctx context.Context, accounts []Account) []Account {
	path, _ := ctx.Value(newAPIProviderScopeKey{}).(string)
	task, _ := ctx.Value(newAPIPluginRoutingKey{}).(bool)
	if !task && path != newAPIProviderChat && path != newAPIProviderResponses && path != newAPIProviderMessages && path != newAPIProviderEmbeddings {
		return accounts
	}
	usesCredential := func(account *Account) bool {
		if task {
			managed, _ := account.Credentials[newAPIPluginManagedCredentialsKey].(bool)
			return account.NewAPIPluginVersionID() > 0 && managed
		}
		return len(account.newAPIProviderBinding().Routes) > 0
	}
	var ids []int64
	for i := range accounts {
		if usesCredential(&accounts[i]) {
			ids = append(ids, accounts[i].ID)
		}
	}
	if len(ids) == 0 {
		return accounts
	}
	available := make(map[int64]PluginCredentialScheduling, len(ids))
	if s != nil && s.newAPIPlugins != nil {
		if repo, ok := s.newAPIPlugins.repo.(PluginCredentialSchedulingRepository); ok {
			// Chunk queries independently of the total account pool size.
			for start := 0; start < len(ids); start += 256 {
				readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				rows, err := repo.ReadPluginCredentialScheduling(readCtx, ids[start:min(start+256, len(ids))])
				cancel()
				if err != nil {
					continue // Fail closed for these plugin accounts only.
				}
				for _, row := range rows {
					available[row.AccountID] = row
				}
			}
		}
	}
	result := make([]Account, 0, len(accounts))
	now := time.Now()
	for i := range accounts {
		account := &accounts[i]
		if !usesCredential(account) || available[account.ID].usable(account, now) {
			result = append(result, *account)
		}
	}
	return result
}

func (s *OpenAIGatewayService) pluginCredentialAccountUsable(ctx context.Context, account *Account) bool {
	return account != nil && len(s.filterPluginCredentialAccounts(ctx, []Account{*account})) == 1
}
