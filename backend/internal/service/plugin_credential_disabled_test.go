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

type pluginDisableAccountFixture struct {
	AccountRepository
	event                    PluginCredentialDisable
	message                  string
	nativeCalls, pluginCalls int
	err                      error
}

func (r *pluginDisableAccountFixture) SetError(context.Context, int64, string) error {
	r.nativeCalls++
	return nil
}
func (r *pluginDisableAccountFixture) SetPluginCredentialError(_ context.Context, event PluginCredentialDisable, message string) error {
	r.pluginCalls++
	r.event = event
	r.message = message
	return r.err
}

func TestPluginCredentialDisabledUsesGuardedRepositoryOnlyForProviders(t *testing.T) {
	account := schedulingPluginAccount(17)
	repo := &pluginDisableAccountFixture{}
	s := &RateLimitService{accountRepo: repo}
	require.NoError(t, s.setUpstreamAccountError(context.Background(), &account, "fixture-message", "auth_error"))
	require.Equal(t, 1, repo.pluginCalls)
	require.Zero(t, repo.nativeCalls)
	require.JSONEq(t, string(pluginCredentialConnection(&account)), string(repo.event.Connection))
	repo.err = errors.New("fixture-write-failed")
	require.Error(t, s.setUpstreamAccountError(context.Background(), &account, "fixture-message", "auth_error"))
	require.Zero(t, repo.nativeCalls, "never retry a rejected guarded mutation with unguarded SetError")
	account.Credentials = nil
	require.NoError(t, s.setUpstreamAccountError(context.Background(), &account, "fixture-message", "auth_error"))
	require.Equal(t, 1, repo.nativeCalls)
}

func TestPluginCredentialDisabledHookContextAndRecoveryOptIn(t *testing.T) {
	for _, optIn := range []bool{false, true} {
		t.Run(fmt.Sprint(optIn), func(t *testing.T) {
			source := providerFixtureSource + `export function scheduled(ctx){if(ctx.reason!=="disabled" || ctx.keyStatus!=="auto_disabled" || ctx.disabledReason!=="auth_error")throw Error("wrong native event context");return {next:120,keyStatus:"enabled",notify:"Recovered"};}`
			s, account, repo := lifecycleFixture(t, source)
			now := time.Now().UTC()
			account.Status = StatusError
			account.Schedulable = false
			account.UpdatedAt = now
			account.Credentials["newapi_plugin_auto_disable"] = optIn
			s.gateway.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}
			repo.state = &PluginCredentialState{AccountID: account.ID, PluginVersionID: 1, Connection: pluginCredentialConnection(account), Revision: 1, Public: json.RawMessage(`{}`), NativeDisabledAt: &now, NativeDisablePending: true, NativeDisabledReason: "auth_error"}
			ctx := context.WithValue(context.Background(), pluginCredentialTimerKey{}, true)
			view, err := s.ManagePluginCredential(ctx, account.ID, 0, "", "", "scheduled", PluginLoginInput{})
			require.NoError(t, err)
			require.Equal(t, "active", view.Status)
			require.False(t, repo.state.NativeDisablePending)
			require.NotNil(t, repo.state.LastDisabledRunAt)
			require.Equal(t, optIn, repo.state.RecoverNative)
		})
	}
}

func TestPluginCredentialDisabledNeverRecoversManualOrFailedMaintenance(t *testing.T) {
	for _, scenario := range []string{"manual-name-edit", "manual-pause", "hook-failure", "invalid-credential"} {
		t.Run(scenario, func(t *testing.T) {
			body := `throw Error("private-upstream-canary")`
			if scenario == "invalid-credential" {
				body = `return {next:120,keyStatus:"enabled",credential:{token:"cannot-replace-static-key"}}`
			}
			s, account, repo := lifecycleFixture(t, providerFixtureSource+`export function scheduled(ctx){`+body+`;}`)
			now := time.Now().UTC()
			account.Status = StatusError
			account.Schedulable = false
			account.UpdatedAt = now
			account.Credentials["newapi_plugin_auto_disable"] = true
			repo.state = &PluginCredentialState{AccountID: account.ID, PluginVersionID: 1, Connection: pluginCredentialConnection(account), Revision: 1, Public: json.RawMessage(`{}`), NativeDisabledAt: &now, NativeDisablePending: true, NativeDisabledReason: "auth_error"}
			if scenario == "manual-name-edit" {
				account.UpdatedAt = now.Add(time.Millisecond)
			}
			if scenario == "manual-pause" {
				account.Status = "inactive"
			}
			s.gateway.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}
			_, err := s.ManagePluginCredential(context.WithValue(context.Background(), pluginCredentialTimerKey{}, true), account.ID, 0, "", "", "scheduled", PluginLoginInput{})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-upstream-canary")
			require.False(t, repo.state.RecoverNative)
			require.True(t, repo.state.NativeDisablePending)
		})
	}
}
