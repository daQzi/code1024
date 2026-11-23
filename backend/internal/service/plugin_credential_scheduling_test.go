//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type credentialSchedulingFixture struct {
	NewAPIPluginRepository
	rows    []PluginCredentialScheduling
	err     error
	batches [][]int64
}

func (r *credentialSchedulingFixture) ReadPluginCredentialScheduling(_ context.Context, ids []int64) ([]PluginCredentialScheduling, error) {
	r.batches = append(r.batches, append([]int64(nil), ids...))
	return r.rows, r.err
}

func schedulingPluginAccount(id int64) Account {
	return Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{NewAPIPluginRevisionKey: 1, "base_url": "https://fixture.example.invalid", "model_mapping": map[string]any{"public": "upstream"}, newAPIPluginProviderKey: json.RawMessage(`{"routes":[{"incoming_path":"/v1/chat/completions","upstream_path":"/chat","models":["upstream"]}]}`)}}
}

func TestPluginCredentialSchedulingProjectionPolicy(t *testing.T) {
	for _, scenario := range []struct {
		name, status                                                      string
		grant, refresh, expired, needsLogin, static, autoDisable, enabled bool
		want                                                              bool
	}{
		{name: "missing-login", enabled: true},
		{name: "static-key", enabled: true, static: true, want: true},
		{name: "valid-grant", enabled: true, status: "active", grant: true, want: true},
		{name: "login-in-progress-with-grant", enabled: true, status: "logging_in", grant: true, want: true},
		{name: "revoked", enabled: true, status: "revoked", grant: true},
		{name: "revoke-pending", enabled: true, status: "revoke_pending", grant: true},
		{name: "login-required", enabled: true, status: "login_required", static: true},
		{name: "paused", enabled: true, status: "auto_disabled", autoDisable: true, grant: true},
		{name: "pause-opt-out", enabled: true, status: "auto_disabled", grant: true, want: true},
		{name: "expired-no-refresh", enabled: true, status: "active", grant: true, expired: true},
		{name: "expired-can-refresh", enabled: true, status: "active", grant: true, expired: true, refresh: true, want: true},
		{name: "needs-login", enabled: true, status: "active", grant: true, needsLogin: true},
		{name: "disabled-plugin", status: "active", grant: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			account := schedulingPluginAccount(17)
			account.Credentials["newapi_plugin_auto_disable"] = scenario.autoDisable
			if scenario.static {
				account.Credentials["api_key"] = "fixture-static"
			}
			public := map[string]any{"status": scenario.status}
			if scenario.grant {
				expires := time.Now().Add(time.Hour).Unix()
				if scenario.expired {
					expires = time.Now().Add(-time.Minute).Unix()
				}
				public["credential"] = map[string]any{"account_id": "fixture", "expires_at": expires, "needs_login": scenario.needsLogin}
			}
			raw, err := json.Marshal(public)
			require.NoError(t, err)
			row := PluginCredentialScheduling{AccountID: 17, PluginVersionID: 1, Connection: pluginCredentialConnection(&account), Enabled: scenario.enabled, RequiresLogin: true, CanRefresh: scenario.refresh, Public: raw}
			require.Equal(t, scenario.want, row.usable(&account, time.Now()))
			row.Connection = json.RawMessage(`[]`)
			require.False(t, row.usable(&account, time.Now()))
		})
	}
}

func TestPluginCredentialSchedulingFailsClosedAndLeavesNativeAccountsAlone(t *testing.T) {
	plugin := schedulingPluginAccount(17)
	native := Account{ID: 18, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	ctx := WithNewAPIProviderRouting(context.Background(), newAPIProviderChat)
	repo := &credentialSchedulingFixture{}
	gateway := &OpenAIGatewayService{newAPIPlugins: &NewAPIPluginService{repo: repo}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{plugin, native}}}
	for _, failure := range []bool{false, true} {
		if failure {
			repo.err = errors.New("fixture storage unavailable")
		}
		accounts, err := gateway.listSchedulableAccounts(ctx, nil, PlatformOpenAI)
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		require.EqualValues(t, 18, accounts[0].ID)
		account, err := gateway.getSchedulableAccount(ctx, 17)
		require.NoError(t, err)
		require.Nil(t, account)
		require.Nil(t, gateway.resolveFreshSchedulableOpenAIAccountBeforeProfit(ctx, &plugin, PlatformOpenAI, "public", false, ""))
		require.Nil(t, gateway.recheckSelectedOpenAIAccountFromDBBeforeProfit(ctx, &plugin, nil, PlatformOpenAI, "public", false, ""))
	}
	repo.err = nil
	repo.rows = []PluginCredentialScheduling{{AccountID: 17, PluginVersionID: 1, Connection: pluginCredentialConnection(&plugin), Enabled: true, Public: json.RawMessage(`{}`)}}
	accounts, err := gateway.listSchedulableAccounts(ctx, nil, PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, accounts, 2)
	account, err := gateway.getSchedulableAccount(ctx, 17)
	require.NoError(t, err)
	require.NotNil(t, account)
	// Provider grants do not accidentally filter a Task/other protocol path.
	repo.rows = nil
	before := len(repo.batches)
	require.Len(t, gateway.filterPluginCredentialAccounts(context.Background(), []Account{plugin, native}), 2)
	require.Equal(t, before, len(repo.batches))
	gateway.newAPIPlugins = nil
	require.Len(t, gateway.filterPluginCredentialAccounts(ctx, []Account{plugin, native}), 1)
}

func TestPluginCredentialSchedulingBoundsDatabaseBatches(t *testing.T) {
	var accounts []Account
	for i := range 600 {
		accounts = append(accounts, schedulingPluginAccount(int64(i+1)))
	}
	repo := &credentialSchedulingFixture{}
	gateway := &OpenAIGatewayService{newAPIPlugins: &NewAPIPluginService{repo: repo}}
	require.Empty(t, gateway.filterPluginCredentialAccounts(WithNewAPIProviderRouting(context.Background(), newAPIProviderChat), accounts))
	require.Len(t, repo.batches, 3)
	for i, batch := range repo.batches {
		require.LessOrEqual(t, len(batch), 256, fmt.Sprint(i))
	}
}

func TestPluginCredentialEmbeddingsAdmissionUsesSamePublicState(t *testing.T) {
	plugin := schedulingPluginAccount(17)
	native := Account{ID: 18, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	repo := &credentialSchedulingFixture{}
	gateway := &OpenAIGatewayService{newAPIPlugins: &NewAPIPluginService{repo: repo}}
	ctx := WithNewAPIProviderRouting(context.Background(), newAPIProviderEmbeddings)
	for _, status := range []string{"revoked", "revoke_pending", "login_required"} {
		repo.rows = []PluginCredentialScheduling{{AccountID: 17, PluginVersionID: 1, Connection: pluginCredentialConnection(&plugin), Enabled: true, Public: json.RawMessage(`{"status":"` + status + `"}`)}}
		accounts := gateway.filterPluginCredentialAccounts(ctx, []Account{plugin, native})
		require.Len(t, accounts, 1, status)
		require.EqualValues(t, 18, accounts[0].ID)
	}
	repo.rows[0].Public = json.RawMessage(`{}`)
	require.Len(t, gateway.filterPluginCredentialAccounts(ctx, []Account{plugin, native}), 2)
	repo.err = errors.New("credential state unavailable")
	require.Len(t, gateway.filterPluginCredentialAccounts(ctx, []Account{plugin, native}), 1)
}

func TestPluginCredentialExpiredWithoutRefreshNeverReachesRelay(t *testing.T) {
	s, account, repo := lifecycleFixture(t, providerFixtureSource+lifecycleHooks)
	fixtureLogin(t, s, account)
	state, err := repo.ClaimPluginCredential(context.Background(), repo.state, 0)
	require.NoError(t, err)
	payload, err := s.readCredentialPayload(state)
	require.NoError(t, err)
	payload.Credential.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	require.NoError(t, s.saveCredentialPayload(context.Background(), state, &payload))
	hooks := repo.versions[1].Capabilities.Hooks
	for i, hook := range hooks {
		if hook == "refresh" {
			repo.versions[1].Capabilities.Hooks = append(hooks[:i:i], hooks[i+1:]...)
			break
		}
	}
	resolved, err := s.loadProviderLoginCredential(context.Background(), account, repo.versions[1], account.GetCredential("base_url"), "")
	require.Error(t, err)
	require.Nil(t, resolved)
}

func TestPluginCredentialSchedulingManagedTaskScope(t *testing.T) {
	managed := schedulingPluginAccount(17)
	delete(managed.Credentials, newAPIPluginProviderKey)
	managed.Credentials[newAPIPluginManagedCredentialsKey] = true
	static := schedulingPluginAccount(18)
	delete(static.Credentials, newAPIPluginProviderKey)
	ctx := WithNewAPIPluginRouting(context.Background())
	repo := &credentialSchedulingFixture{}
	gateway := &OpenAIGatewayService{newAPIPlugins: &NewAPIPluginService{repo: repo}}
	items := gateway.filterPluginCredentialAccounts(ctx, []Account{managed, static})
	require.Len(t, items, 1)
	require.EqualValues(t, 18, items[0].ID)
	require.False(t, gateway.pluginCredentialAccountUsable(ctx, &managed))
	repo.rows = []PluginCredentialScheduling{{AccountID: 17, PluginVersionID: 1, Connection: pluginCredentialConnection(&managed), Enabled: true, RequiresLogin: true, Public: json.RawMessage(`{"status":"active","credential":{"account_id":"fixture","expires_at":0}}`)}}
	require.Len(t, gateway.filterPluginCredentialAccounts(ctx, []Account{managed, static}), 2)
	require.True(t, gateway.pluginCredentialAccountUsable(ctx, &managed))
	repo.rows[0].Public = json.RawMessage(`{"status":"revoke_pending"}`)
	require.False(t, gateway.pluginCredentialAccountUsable(ctx, &managed))
	// A managed video projection must not affect unrelated native protocols.
	require.True(t, gateway.pluginCredentialAccountUsable(context.Background(), &managed))
}
