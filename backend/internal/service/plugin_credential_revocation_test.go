package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/stretchr/testify/require"
)

type credentialRevocationFixture struct {
	*credentialFixtureRepo
	job                *PluginCredentialRevocation
	status, diagnostic string
	claims, finishes   int
}

func (r *credentialRevocationFixture) DuePluginCredentialRevocations(context.Context) ([]int64, error) {
	return []int64{r.job.ID}, nil
}
func (r *credentialRevocationFixture) ClaimPluginCredentialRevocation(_ context.Context, id int64) (*PluginCredentialRevocation, error) {
	r.claims++
	if id != r.job.ID {
		return nil, ErrPluginCredentialConflict
	}
	copy := *r.job
	return &copy, nil
}
func (r *credentialRevocationFixture) FinishPluginCredentialRevocation(_ context.Context, _ *PluginCredentialRevocation, status, diagnostic string) error {
	r.finishes++
	r.status, r.diagnostic = status, diagnostic
	return nil
}
func (r *credentialRevocationFixture) ListPluginCredentialRevocations(context.Context) ([]PluginCredentialRevocation, error) {
	return nil, nil
}
func (r *credentialRevocationFixture) RetryPluginCredentialRevocation(context.Context, int64) error {
	return nil
}

func TestPluginCredentialRetirementUsesOriginalConnectionAndProtectsIdentity(t *testing.T) {
	for _, scenario := range []string{"success", "protected", "proxy-changed", "disabled", "no-hook", "bad-state", "bad-config", "upstream-error", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			s, account, live := lifecycleFixture(t, providerFixtureSource+lifecycleHooks)
			fixtureLogin(t, s, account)
			job := &PluginCredentialRevocation{ID: 3, AccountID: account.ID, PluginVersionID: 1, BaseURL: account.GetCredential("base_url"), ConfigEncrypted: account.GetCredential(newAPIPluginConfigEncryptedKey), StateEncrypted: live.state.Encrypted}
			live.state = nil
			repo := &credentialRevocationFixture{credentialFixtureRepo: live, job: job}
			s.repo = repo
			switch scenario {
			case "protected":
				live.state = &PluginCredentialState{Public: json.RawMessage(`{"credential":{"account_id":"upstream-identity"}}`)}
			case "proxy-changed":
				id := int64(4)
				job.ProxyID = &id
			case "disabled":
				repo.versions[1].Enabled = false
			case "no-hook":
				repo.versions[1].Capabilities.Hooks = []string{"login"}
			case "bad-state":
				job.StateEncrypted = "unreadable"
			case "bad-config":
				job.ConfigEncrypted = "unreadable"
			}
			// The live account is edited/deleted; cleanup must not read it.
			account.Credentials["base_url"] = "https://changed.example.invalid"
			account.Credentials[newAPIPluginConfigEncryptedKey] = "different-config"
			calls := 0
			s.gateway.httpUpstream = &providerChainHTTP{send: func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "https://api.example.com/prefix/revoke", req.URL.String())
				require.True(t, HTTPUpstreamPublicHostsOnly(req.Context()))
				require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
				body, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.JSONEq(t, `{"token":"grant-1","refresh":"refresh-secret"}`, string(body))
				if scenario == "upstream-error" {
					return chainResponse(500, `private-secret`), nil
				}
				return chainResponse(200, `{}`), nil
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			s.runCredentialRevocation(ctx, job.ID)
			if scenario == "success" {
				require.Equal(t, 1, calls)
				require.Equal(t, "completed", repo.status)
			} else if scenario == "upstream-error" {
				require.Equal(t, 1, calls)
				require.Equal(t, "pending", repo.status)
			} else {
				require.Zero(t, calls)
			}
			if scenario == "protected" {
				require.Equal(t, "protected", repo.status)
			}
			if scenario == "no-hook" {
				require.Equal(t, "completed", repo.status)
			}
			require.Equal(t, repo.claims, repo.finishes)
			require.NotContains(t, repo.diagnostic, "secret")
		})
	}
}

func TestPluginCredentialRetirementMaintenanceAdmission(t *testing.T) {
	s, _, live := lifecycleFixture(t, providerFixtureSource+lifecycleHooks)
	repo := &credentialRevocationFixture{credentialFixtureRepo: live, job: &PluginCredentialRevocation{ID: 5}}
	s.repo = repo
	m := &pluginCredentialMaintenance{service: s, ctx: context.Background(), queue: make(chan pluginCredentialWork, 32), budget: pkghttputil.NewBodyMemoryBudget(1)}
	m.enqueue()
	require.Len(t, m.queue, 2)
	first, second := <-m.queue, <-m.queue
	require.Positive(t, first.accountID)
	require.EqualValues(t, 5, second.revocationID)
	m.run(second)
	require.Zero(t, repo.claims, "memory admission precedes loading the encrypted grant")
	require.Error(t, s.RetryCredentialRevocation(context.Background(), 0))
	view, _ := json.Marshal(repo.job)
	require.False(t, strings.Contains(string(view), "StateEncrypted"))
}
