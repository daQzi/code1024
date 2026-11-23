package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPluginCredentialDisabledLocalPostgresAtomicEventAndRecovery(t *testing.T) {
	db, live, expected := credentialLocalFixture(t)
	ctx := context.Background()
	_, err := db.Exec(`UPDATE accounts SET credentials=credentials||'{"newapi_plugin_auto_disable":true}'::jsonb WHERE id=$1`, expected.AccountID)
	require.NoError(t, err)
	seedRetiredGrant(t, db, live, expected, "sealed-live-grant")
	old, err := live.ClaimPluginCredential(ctx, expected, 0)
	require.NoError(t, err)
	event := service.PluginCredentialDisable{AccountID: expected.AccountID, PluginVersionID: expected.PluginVersionID, Connection: expected.Connection, Reason: "auth_error"}
	repo := &accountRepository{sql: db}
	require.NoError(t, repo.SetPluginCredentialError(ctx, event, "native-error-private"))
	state, err := live.ReadPluginCredential(ctx, expected.AccountID)
	require.NoError(t, err)
	require.True(t, state.NativeDisablePending)
	require.NotNil(t, state.NativeDisabledAt)
	require.Equal(t, "auth_error", state.NativeDisabledReason)
	require.Equal(t, "sealed-live-grant", state.Encrypted)
	require.Empty(t, state.LeaseToken)
	require.ErrorIs(t, live.FinishPluginCredential(ctx, old), service.ErrPluginCredentialConflict)
	// Replaying a stale response is not another transition or a new event.
	require.NoError(t, repo.SetPluginCredentialError(ctx, event, "another-error"))
	read, err := live.ReadPluginCredential(ctx, expected.AccountID)
	require.NoError(t, err)
	require.Equal(t, state.Revision, read.Revision)
	ids, err := live.DuePluginCredentials(ctx)
	require.NoError(t, err)
	require.Contains(t, ids, expected.AccountID)
	expected.OnlyIfDue = true
	state, err = live.ClaimPluginCredential(ctx, expected, 0)
	require.NoError(t, err)
	now := time.Now().UTC()
	state.LastDisabledRunAt = &now
	state.NativeDisablePending = false
	state.RecoverNative = true
	state.Public = json.RawMessage(`{"status":"active","credential":{"account_id":"retirement-identity"}}`)
	state.NextRunAt = now.Add(time.Hour)
	require.NoError(t, live.FinishPluginCredential(ctx, state))
	var status, message string
	var schedulable bool
	require.NoError(t, db.QueryRow(`SELECT status,schedulable,error_message FROM accounts WHERE id=$1`, expected.AccountID).Scan(&status, &schedulable, &message))
	require.Equal(t, "active", status)
	require.True(t, schedulable)
	require.Empty(t, message)
	read, err = live.ReadPluginCredential(ctx, expected.AccountID)
	require.NoError(t, err)
	require.Nil(t, read.NativeDisabledAt)
	require.False(t, read.NativeDisablePending)
	var outbox int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`, expected.AccountID).Scan(&outbox))
	require.GreaterOrEqual(t, outbox, 2)
	// A second real transition is retained but its disabled hook is throttled.
	require.NoError(t, repo.SetPluginCredentialError(ctx, event, "third-error"))
	read, err = live.ReadPluginCredential(ctx, expected.AccountID)
	require.NoError(t, err)
	require.True(t, read.NativeDisablePending)
	require.WithinDuration(t, now.Add(time.Minute), read.NextRunAt, time.Second)
	ids, err = live.DuePluginCredentials(ctx)
	require.NoError(t, err)
	require.NotContains(t, ids, expected.AccountID)
}

func TestPluginCredentialDisabledLocalPostgresManualEditsAndRollback(t *testing.T) {
	for _, edit := range []string{"name", "manual-pause", "manual-error", "no-opt-in", "plugin-disabled"} {
		t.Run(edit, func(t *testing.T) {
			db, live, expected := credentialLocalFixture(t)
			ctx := context.Background()
			_, err := db.Exec(`UPDATE accounts SET credentials=credentials||'{"newapi_plugin_auto_disable":true}'::jsonb WHERE id=$1`, expected.AccountID)
			require.NoError(t, err)
			seedRetiredGrant(t, db, live, expected, "original-sealed")
			event := service.PluginCredentialDisable{AccountID: expected.AccountID, PluginVersionID: expected.PluginVersionID, Connection: expected.Connection, Reason: "custom_error_code"}
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			require.NoError(t, (&accountRepository{sql: tx}).SetPluginCredentialError(ctx, event, "native"))
			require.NoError(t, tx.Rollback())
			state, err := live.ReadPluginCredential(ctx, expected.AccountID)
			require.NoError(t, err)
			require.Nil(t, state.NativeDisabledAt)
			require.NoError(t, (&accountRepository{sql: db}).SetPluginCredentialError(ctx, event, "native"))
			expected.OnlyIfDue = true
			state, err = live.ClaimPluginCredential(ctx, expected, 0)
			require.NoError(t, err)
			state.Encrypted = "must-not-commit"
			state.Public = json.RawMessage(`{"status":"active"}`)
			state.NativeDisablePending = false
			state.RecoverNative = true
			state.NextRunAt = time.Now().Add(time.Hour)
			switch edit {
			case "name":
				_, err = db.Exec(`UPDATE accounts SET name='edited',updated_at=clock_timestamp() WHERE id=$1`, expected.AccountID)
			case "manual-pause":
				_, err = db.Exec(`UPDATE accounts SET status='inactive',updated_at=clock_timestamp() WHERE id=$1`, expected.AccountID)
			case "manual-error":
				_, err = db.Exec(`UPDATE accounts SET error_message='manual',updated_at=clock_timestamp() WHERE id=$1`, expected.AccountID)
			case "no-opt-in":
				_, err = db.Exec(`UPDATE accounts SET credentials=credentials||'{"newapi_plugin_auto_disable":false}'::jsonb WHERE id=$1`, expected.AccountID)
			case "plugin-disabled":
				_, err = db.Exec(`UPDATE sub2api_js_plugins SET enabled=false`)
			}
			require.NoError(t, err)
			require.ErrorIs(t, live.FinishPluginCredential(ctx, state), service.ErrPluginCredentialConflict)
			read, err := live.ReadPluginCredential(ctx, expected.AccountID)
			require.NoError(t, err)
			require.Equal(t, "original-sealed", read.Encrypted)
			if edit != "no-opt-in" {
				ids, err := live.DuePluginCredentials(ctx)
				require.NoError(t, err)
				require.NotContains(t, ids, expected.AccountID)
			}
		})
	}
}

func TestPluginCredentialDisabledLocalPostgresStaleConnectionNeverDisables(t *testing.T) {
	db, live, expected := credentialLocalFixture(t)
	ctx := context.Background()
	event := service.PluginCredentialDisable{AccountID: expected.AccountID, PluginVersionID: expected.PluginVersionID, Connection: expected.Connection, Reason: "stream_timeout_error"}
	_, err := db.Exec(`UPDATE accounts SET credentials=jsonb_set(credentials,'{api_key}','"new-key"') WHERE id=$1`, expected.AccountID)
	require.NoError(t, err)
	require.NoError(t, (&accountRepository{sql: db}).SetPluginCredentialError(ctx, event, "stale-error"))
	var status string
	require.NoError(t, db.QueryRow(`SELECT status FROM accounts WHERE id=$1`, expected.AccountID).Scan(&status))
	require.Equal(t, "active", status)
	state, err := live.ReadPluginCredential(ctx, expected.AccountID)
	require.NoError(t, err)
	require.Nil(t, state)
	// A static-key plugin without existing state still gets a durable event.
	require.NoError(t, db.QueryRow(`SELECT `+pluginCredentialConnectionSQL+` FROM accounts a WHERE id=$1`, expected.AccountID).Scan(&event.Connection))
	require.NoError(t, (&accountRepository{sql: db}).SetPluginCredentialError(ctx, event, "new-error"))
	state, err = live.ReadPluginCredential(ctx, expected.AccountID)
	require.NoError(t, err)
	require.NotNil(t, state)
	require.True(t, state.NativeDisablePending)
	require.Empty(t, state.Encrypted)
}
