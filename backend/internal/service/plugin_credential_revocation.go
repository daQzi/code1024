package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

// A retirement job is not another usable upstream connection. Only revoke
// can consume its encrypted grant, bound to its original endpoint and proxy.
type PluginCredentialRevocation struct {
	ID               int64      `json:"id"`
	AccountID        int64      `json:"account_id"`
	PluginVersionID  int64      `json:"plugin_version_id"`
	Status           string     `json:"status"`
	Attempts         int        `json:"attempts"`
	LastError        string     `json:"last_error"`
	CreatedAt        time.Time  `json:"created_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	BaseURL          string     `json:"-"`
	ConfigEncrypted  string     `json:"-"`
	StateEncrypted   string     `json:"-"`
	ProxyID          *int64     `json:"-"`
	ProxyFingerprint string     `json:"-"`
	Proxy            *Proxy     `json:"-"`
	LeaseToken       string     `json:"-"`
}

type PluginCredentialRevocationRepository interface {
	DuePluginCredentialRevocations(context.Context) ([]int64, error)
	ClaimPluginCredentialRevocation(context.Context, int64) (*PluginCredentialRevocation, error)
	FinishPluginCredentialRevocation(context.Context, *PluginCredentialRevocation, string, string) error
	ListPluginCredentialRevocations(context.Context) ([]PluginCredentialRevocation, error)
	RetryPluginCredentialRevocation(context.Context, int64) error
}

func (s *NewAPIPluginService) ListCredentialRevocations(ctx context.Context) ([]PluginCredentialRevocation, error) {
	repo, ok := s.repo.(PluginCredentialRevocationRepository)
	if !ok {
		return nil, infraerrors.New(503, "PLUGIN_CLEANUP_UNAVAILABLE", "plugin credential cleanup is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return repo.ListPluginCredentialRevocations(ctx)
}

func (s *NewAPIPluginService) RetryCredentialRevocation(ctx context.Context, id int64) error {
	repo, ok := s.repo.(PluginCredentialRevocationRepository)
	if !ok || id <= 0 {
		return pluginInputError("invalid credential cleanup job")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return repo.RetryPluginCredentialRevocation(ctx, id)
}

func (s *NewAPIPluginService) runCredentialRevocation(ctx context.Context, id int64) {
	repo, ok := s.repo.(PluginCredentialRevocationRepository)
	if !ok {
		return
	}
	job, err := repo.ClaimPluginCredentialRevocation(ctx, id)
	if err != nil || job == nil {
		return
	}
	status, diagnostic := "pending", "cleanup_failed"
	defer func() {
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = repo.FinishPluginCredentialRevocation(finish, job, status, diagnostic)
	}()
	if job.ProxyID != nil && job.Proxy == nil {
		diagnostic = "original_proxy_changed"
		return
	}
	version, err := s.repo.Get(ctx, job.PluginVersionID)
	if err != nil || version == nil || !version.Enabled {
		diagnostic = "plugin_unavailable"
		return
	}
	if !slices.Contains(version.Capabilities.Hooks, "revoke") {
		status, diagnostic = "completed", "no_revoke_hook"
		return
	}
	payload, err := s.readCredentialPayload(&PluginCredentialState{Encrypted: job.StateEncrypted})
	if err != nil || payload.Credential == nil || payload.Credential.Plugin != version.Key {
		diagnostic = "credential_unreadable"
		return
	}
	if payload.Credential.AccountID != "" {
		credentials, err := s.credentialRepository()
		if err != nil {
			return
		}
		used, err := credentials.PluginCredentialInUse(ctx, version.ID, payload.Credential.AccountID)
		if err != nil {
			return
		}
		if used {
			status, diagnostic = "protected", "identity_in_use"
			return
		}
	}
	if s.gateway == nil || s.gateway.httpUpstream == nil || s.gateway.concurrencyService == nil {
		return
	}
	base, err := s.gateway.validateUpstreamBaseURL(job.BaseURL)
	if err != nil {
		diagnostic = "original_connection_invalid"
		return
	}
	account := &Account{ID: job.AccountID, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, ProxyID: job.ProxyID, Proxy: job.Proxy, Concurrency: 1}
	slot, err := s.gateway.concurrencyService.AcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil || slot == nil || !slot.Acquired {
		diagnostic = "account_busy"
		return
	}
	defer slot.ReleaseFunc()
	config := json.RawMessage(`{}`)
	if job.ConfigEncrypted != "" {
		plain, err := s.encryptor.Decrypt(job.ConfigEncrypted)
		if err != nil {
			diagnostic = "configuration_unreadable"
			return
		}
		config = json.RawMessage(plain)
	}
	ctx, cancel := context.WithTimeout(ctx, newAPIProviderCredentialTimeout)
	defer cancel()
	err = s.pool.With(ctx, version.Source, func(client *runner.Client) error {
		var err error
		config, err = client.ValidateConfig(ctx, config, false)
		if err != nil {
			return errors.New("invalid original credential configuration")
		}
		session := s.providerCredentialSession(client, version, account, base, config)
		_, err = client.Typed(ctx, session.fetch, "CallRevoke", jsplugin.CredentialContext{BaseURL: base, Credential: payload.Credential.Data, Config: config})
		return err
	})
	if err == nil {
		status, diagnostic = "completed", ""
	} else {
		diagnostic = "upstream_revoke_failed"
	}
}
