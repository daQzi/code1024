package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPluginCredentialSchedulingLocalPostgresPublicProjection(t *testing.T) {
	db, live, expected := credentialLocalFixture(t)
	ctx := context.Background()
	repo := NewNewAPIPluginRepository(db).(service.PluginCredentialSchedulingRepository)
	rows, err := repo.ReadPluginCredentialScheduling(ctx, []int64{expected.AccountID})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.True(t, rows[0].Enabled)
	require.True(t, rows[0].RequiresLogin)
	require.True(t, rows[0].CanRefresh)
	require.JSONEq(t, string(expected.Connection), string(rows[0].Connection))
	seedRetiredGrant(t, db, live, expected, "private-encrypted-grant")
	_, err = db.Exec(`UPDATE sub2api_plugin_credential_state SET public=public||'{"login":{"url":"private-login-url"}}'::jsonb WHERE account_id=$1`, expected.AccountID)
	require.NoError(t, err)
	rows, err = repo.ReadPluginCredentialScheduling(ctx, []int64{expected.AccountID})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	raw, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-encrypted-grant")
	require.NotContains(t, string(raw), "private-login-url")
	require.Contains(t, string(rows[0].Public), "retirement-identity")
	_, err = db.Exec(`UPDATE sub2api_js_plugins SET enabled=false`)
	require.NoError(t, err)
	rows, err = repo.ReadPluginCredentialScheduling(ctx, []int64{expected.AccountID})
	require.NoError(t, err)
	require.False(t, rows[0].Enabled)
	_, err = db.Exec(`UPDATE accounts SET credentials=jsonb_set(credentials,'{api_key}','"rotated"') WHERE id=$1`, expected.AccountID)
	require.NoError(t, err)
	rows, err = repo.ReadPluginCredentialScheduling(ctx, []int64{expected.AccountID})
	require.NoError(t, err)
	require.NotContains(t, string(rows[0].Public), "retirement-identity")
	_, err = repo.ReadPluginCredentialScheduling(ctx, make([]int64, 257))
	require.Error(t, err)
}
