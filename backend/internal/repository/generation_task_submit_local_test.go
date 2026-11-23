package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGenerationTaskSubmitLocalRechecksBoundPluginAndAccountBeforeFreezing(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	task, command := f.task(t, &service.GenerationFile{})
	task.Files = nil
	plugins := NewNewAPIPluginRepository(f.db)
	require.NoError(t, plugins.Activate(ctx, f.version, true))
	credentials, err := json.Marshal(map[string]any{service.NewAPIPluginRevisionKey: f.version, "base_url": task.Connection.BaseURL})
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE accounts SET credentials=$2::jsonb WHERE id=$1`, f.account, credentials)
	require.NoError(t, err)
	_, err = f.db.Exec(`INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, f.account, f.group)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, plugin, statement string
	}{
		{"wrong plugin", "different-plugin", ""},
		{"connection changed", "sora", `UPDATE accounts SET credentials=jsonb_set(credentials,'{base_url}','"https://changed.example.invalid"') WHERE id=$1`},
		{"model mapping changed", "sora", `UPDATE accounts SET credentials=credentials||'{"model_mapping":{"sora-2":"sora-2-other"}}'::jsonb WHERE id=$1`},
		{"group binding removed", "sora", `DELETE FROM account_groups WHERE account_id=$1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.statement != "" {
				_, err := f.db.Exec(tc.statement, f.account)
				require.NoError(t, err)
			}
			_, err := f.repo.CreateHeld(service.WithGenerationTaskSubmit(ctx, tc.plugin), task, command)
			require.ErrorIs(t, err, service.ErrGenerationOrigin)
			var balance, frozen float64
			require.NoError(t, f.db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, f.user).Scan(&balance, &frozen))
			require.Equal(t, 100.0, balance)
			require.Zero(t, frozen)
			_, err = f.repo.GetOwned(ctx, task.ID, f.user, f.key)
			require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
			_, err = f.db.Exec(`UPDATE accounts SET credentials=$2::jsonb WHERE id=$1`, f.account, credentials)
			require.NoError(t, err)
			_, err = f.db.Exec(`INSERT INTO account_groups(account_id,group_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, f.account, f.group)
			require.NoError(t, err)
		})
	}
	accepted, err := f.repo.CreateHeld(service.WithGenerationTaskSubmit(ctx, "sora"), task, command)
	require.NoError(t, err)
	require.Equal(t, task.ID, accepted.ID)
}
