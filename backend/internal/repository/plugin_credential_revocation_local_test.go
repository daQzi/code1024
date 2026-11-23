package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func seedRetiredGrant(t *testing.T, db *sql.DB, repo service.PluginCredentialRepository, expected *service.PluginCredentialState, sealed string) *service.PluginCredentialState {
	t.Helper()
	require.NoError(t, db.QueryRow(`SELECT `+pluginCredentialConnectionSQL+` FROM accounts a WHERE id=$1`, expected.AccountID).Scan(&expected.Connection))
	state, err := repo.ClaimPluginCredential(context.Background(), expected, 0)
	require.NoError(t, err)
	state.Encrypted, state.Public, state.NextRunAt = sealed, json.RawMessage(`{"status":"active","credential":{"account_id":"retirement-identity"}}`), time.Now().Add(time.Hour)
	require.NoError(t, repo.FinishPluginCredential(context.Background(), state))
	return state
}

func TestPluginCredentialRetirementLocalPostgresConnectionEdits(t *testing.T) {
	for _, scenario := range []struct {
		name, edit string
		retire     bool
	}{
		{"name", `UPDATE accounts SET name='renamed' WHERE id=$1`, false},
		{"metadata", `UPDATE accounts SET credentials=credentials||'{"model_mapping":{"a":"b"},"newapi_plugin_auto_disable":true}'::jsonb WHERE id=$1`, false},
		{"key", `UPDATE accounts SET credentials=jsonb_set(credentials,'{api_key}','"changed"') WHERE id=$1`, true},
		{"base", `UPDATE accounts SET credentials=jsonb_set(credentials,'{base_url}','"https://other.example.invalid"') WHERE id=$1`, true},
		{"config", `UPDATE accounts SET credentials=jsonb_set(credentials,'{newapi_plugin_config_encrypted}','"new-cipher"') WHERE id=$1`, true},
		{"detach", `UPDATE accounts SET credentials=credentials-'newapi_plugin_version_id' WHERE id=$1`, true},
		{"platform", `UPDATE accounts SET platform='gemini' WHERE id=$1`, true},
		{"type", `UPDATE accounts SET type='oauth' WHERE id=$1`, true},
		{"soft-delete", `UPDATE accounts SET deleted_at=NOW() WHERE id=$1`, true},
		{"hard-delete", `DELETE FROM accounts WHERE id=$1`, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db, live, expected := credentialLocalFixture(t)
			seedRetiredGrant(t, db, live, expected, "old-sealed-grant")
			lease, err := live.ClaimPluginCredential(context.Background(), expected, 0)
			require.NoError(t, err)
			_, err = db.Exec(scenario.edit, expected.AccountID)
			require.NoError(t, err)
			cleanup := NewNewAPIPluginRepository(db).(service.PluginCredentialRevocationRepository)
			jobs, err := cleanup.ListPluginCredentialRevocations(context.Background())
			require.NoError(t, err)
			state, err := live.ReadPluginCredential(context.Background(), expected.AccountID)
			require.NoError(t, err)
			if !scenario.retire {
				require.Empty(t, jobs)
				require.NotNil(t, state)
				return
			}
			require.Len(t, jobs, 1)
			require.Nil(t, state)
			require.Empty(t, jobs[0].StateEncrypted, "list selects public fields only")
			require.ErrorIs(t, live.FinishPluginCredential(context.Background(), lease), service.ErrPluginCredentialConflict)
			job, err := cleanup.ClaimPluginCredentialRevocation(context.Background(), jobs[0].ID)
			require.NoError(t, err)
			require.Equal(t, "old-sealed-grant", job.StateEncrypted)
			require.Equal(t, "fixture-ciphertext", job.ConfigEncrypted)
			require.Equal(t, "https://fixture.example.invalid/v1", job.BaseURL)
			raw, err := json.Marshal(job)
			require.NoError(t, err)
			for _, private := range []string{job.StateEncrypted, job.ConfigEncrypted, job.BaseURL, job.LeaseToken} {
				require.NotContains(t, string(raw), private)
			}
		})
	}
}

func TestPluginCredentialRetirementLocalPostgresRollbackAndRebind(t *testing.T) {
	db, live, expected := credentialLocalFixture(t)
	ctx := context.Background()
	seedRetiredGrant(t, db, live, expected, "grant-one")
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.Exec(`UPDATE accounts SET credentials=jsonb_set(credentials,'{api_key}','"rotated"') WHERE id=$1`, expected.AccountID)
	require.NoError(t, err)
	var count int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM sub2api_plugin_credential_revocations`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, tx.Rollback())
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sub2api_plugin_credential_revocations`).Scan(&count))
	require.Zero(t, count)
	read, err := live.ReadPluginCredential(ctx, expected.AccountID)
	require.NoError(t, err)
	require.Equal(t, "grant-one", read.Encrypted)
	cleanup := NewNewAPIPluginRepository(db).(service.PluginCredentialRevocationRepository)
	for i := range 2 {
		_, err = db.Exec(`UPDATE accounts SET credentials=jsonb_set(credentials,'{api_key}',to_jsonb($2::text)) WHERE id=$1`, expected.AccountID, fmt.Sprint("key-", i))
		require.NoError(t, err)
		if i == 0 {
			seedRetiredGrant(t, db, live, expected, "grant-two")
		}
	}
	jobs, err := cleanup.ListPluginCredentialRevocations(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 2, "a reset revision must not swallow a later grant")
	require.NoError(t, ApplyMigrations(ctx, db))
}

func TestPluginCredentialRetirementLocalPostgresLeasesRetryAndIdentity(t *testing.T) {
	db, live, expected := credentialLocalFixture(t)
	ctx := context.Background()
	seedRetiredGrant(t, db, live, expected, "sealed")
	_, err := db.Exec(`UPDATE accounts SET credentials=jsonb_set(credentials,'{api_key}','"rotated"') WHERE id=$1`, expected.AccountID)
	require.NoError(t, err)
	cleanup := NewNewAPIPluginRepository(db).(service.PluginCredentialRevocationRepository)
	ids, err := cleanup.DuePluginCredentialRevocations(ctx)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	var wg sync.WaitGroup
	claims := make(chan *service.PluginCredentialRevocation, 8)
	for range 8 {
		wg.Go(func() {
			job, err := cleanup.ClaimPluginCredentialRevocation(ctx, ids[0])
			if err == nil {
				claims <- job
			} else {
				require.ErrorIs(t, err, service.ErrPluginCredentialConflict)
			}
		})
	}
	wg.Wait()
	close(claims)
	var winners []*service.PluginCredentialRevocation
	for job := range claims {
		winners = append(winners, job)
	}
	require.Len(t, winners, 1)
	old := winners[0]
	require.ErrorIs(t, cleanup.RetryPluginCredentialRevocation(ctx, old.ID), service.ErrPluginCredentialConflict)
	// A newly finished login cannot be attached while the same identity is
	// leased for revocation, even when there is no other live account.
	require.NoError(t, db.QueryRow(`SELECT `+pluginCredentialConnectionSQL+` FROM accounts a WHERE id=$1`, expected.AccountID).Scan(&expected.Connection))
	state, err := live.ClaimPluginCredential(ctx, expected, 0)
	require.NoError(t, err)
	state.Encrypted, state.Public, state.NextRunAt = "new-grant", json.RawMessage(`{"credential":{"account_id":"retirement-identity"}}`), time.Now().Add(time.Hour)
	require.ErrorIs(t, live.FinishPluginCredential(ctx, state), service.ErrPluginCredentialConflict)
	_, err = db.Exec(`UPDATE sub2api_plugin_credential_revocations SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, old.ID)
	require.NoError(t, err)
	job, err := cleanup.ClaimPluginCredentialRevocation(ctx, old.ID)
	require.NoError(t, err)
	require.NotEqual(t, old.LeaseToken, job.LeaseToken)
	require.ErrorIs(t, cleanup.FinishPluginCredentialRevocation(ctx, old, "completed", ""), service.ErrPluginCredentialConflict)
	_, err = db.Exec(`UPDATE sub2api_plugin_credential_revocations SET attempts=9 WHERE id=$1`, job.ID)
	require.NoError(t, err)
	require.NoError(t, cleanup.FinishPluginCredentialRevocation(ctx, job, "pending", "upstream_revoke_failed"))
	jobs, err := cleanup.ListPluginCredentialRevocations(ctx)
	require.NoError(t, err)
	require.Equal(t, "attention", jobs[0].Status)
	require.Equal(t, 10, jobs[0].Attempts)
	require.NoError(t, cleanup.RetryPluginCredentialRevocation(ctx, job.ID))
	job, err = cleanup.ClaimPluginCredentialRevocation(ctx, job.ID)
	require.NoError(t, err)
	require.Zero(t, job.Attempts)
	require.NoError(t, cleanup.FinishPluginCredentialRevocation(ctx, job, "protected", "identity_in_use"))
	require.ErrorIs(t, cleanup.FinishPluginCredentialRevocation(ctx, job, "completed", ""), service.ErrPluginCredentialConflict)
	require.ErrorIs(t, cleanup.RetryPluginCredentialRevocation(ctx, job.ID), service.ErrPluginCredentialConflict)
	var cipher, config string
	require.NoError(t, db.QueryRow(`SELECT state_encrypted,config_encrypted FROM sub2api_plugin_credential_revocations WHERE id=$1`, job.ID).Scan(&cipher, &config))
	require.Empty(t, cipher)
	require.Empty(t, config)
	require.NoError(t, live.FinishPluginCredential(ctx, state), "finished cleanup releases the identity guard")
}

func TestPluginCredentialRetirementLocalPostgresProxySnapshot(t *testing.T) {
	db, live, expected := credentialLocalFixture(t)
	ctx := context.Background()
	var proxyID int64
	require.NoError(t, db.QueryRow(`INSERT INTO proxies(name,protocol,host,port,username,password) VALUES('fixture','socks5','proxy.example.invalid',1080,'fixture-user','fixture-pass') RETURNING id`).Scan(&proxyID))
	seedRetiredGrant(t, db, live, expected, "direct-grant")
	_, err := db.Exec(`UPDATE accounts SET proxy_id=$2 WHERE id=$1`, expected.AccountID, proxyID)
	require.NoError(t, err)
	seedRetiredGrant(t, db, live, expected, "proxy-grant")
	_, err = db.Exec(`UPDATE accounts SET credentials=credentials-'newapi_plugin_version_id' WHERE id=$1`, expected.AccountID)
	require.NoError(t, err)
	cleanup := NewNewAPIPluginRepository(db).(service.PluginCredentialRevocationRepository)
	jobs, err := cleanup.ListPluginCredentialRevocations(ctx)
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	job, err := cleanup.ClaimPluginCredentialRevocation(ctx, jobs[0].ID)
	require.NoError(t, err)
	require.NotNil(t, job.Proxy)
	require.Equal(t, "socks5://fixture-user:fixture-pass@proxy.example.invalid:1080", job.Proxy.URL())
	require.NoError(t, cleanup.FinishPluginCredentialRevocation(ctx, job, "pending", "upstream_revoke_failed"))
	_, err = db.Exec(`UPDATE proxies SET password='rotated' WHERE id=$1`, proxyID)
	require.NoError(t, err)
	require.NoError(t, cleanup.RetryPluginCredentialRevocation(ctx, job.ID))
	job, err = cleanup.ClaimPluginCredentialRevocation(ctx, job.ID)
	require.NoError(t, err)
	require.Nil(t, job.Proxy, "never use the edited proxy or direct egress")
}
