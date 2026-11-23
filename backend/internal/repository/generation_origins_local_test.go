package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func generationOriginSQLFixture(t *testing.T) (*generationFileSQLFixture, *service.GenerationTask, *service.UsageBillingCommand) {
	t.Helper()
	f := newGenerationFileSQLFixture(t)
	file := &service.GenerationFile{ID: "vfile_" + strings.Repeat("1", 40), Filename: "input.png", MimeType: "image/png", Size: 1, SHA256: strings.Repeat("c", 64)}
	parent, command := f.task(t, file)
	parent.Files = nil
	credentials, err := json.Marshal(map[string]any{service.NewAPIPluginRevisionKey: f.version, "base_url": parent.Connection.BaseURL, "api_key": "not-a-real-key"})
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE accounts SET credentials=$2::jsonb WHERE id=$1`, f.account, credentials)
	require.NoError(t, err)
	_, err = f.db.Exec(`INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,1)`, f.account, f.group)
	require.NoError(t, err)
	require.NoError(t, NewNewAPIPluginRepository(f.db).Activate(context.Background(), f.version, true))
	_, err = f.repo.CreateHeld(context.Background(), parent, command)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET upstream_id='private-upstream-parent',status='succeeded',data='{"n":9007199254740993,"private":"parent-data"}'::jsonb WHERE id=$1`, parent.ID)
	require.NoError(t, err)
	parent, err = f.repo.GetOwned(context.Background(), parent.ID, f.user, f.key)
	require.NoError(t, err)
	return f, parent, command
}

func generationOriginSQLChild(t *testing.T, parent *service.GenerationTask) *service.GenerationTask {
	t.Helper()
	child := *parent
	var err error
	child.ID, err = service.NewGenerationTaskID()
	require.NoError(t, err)
	child.IdempotencyKey = child.ID
	child.Origins = []service.GenerationOriginTask{{TaskID: parent.ID, UpstreamTaskID: parent.UpstreamID, Action: parent.Action, Status: "SUCCESS", Data: append(json.RawMessage(nil), parent.Data...)}}
	return &child
}

func TestGenerationOriginLocalSnapshotRestartReplayAndDecoderHistory(t *testing.T) {
	f, parent, command := generationOriginSQLFixture(t)
	ctx := context.Background()
	child := generationOriginSQLChild(t, parent)
	accepted, err := f.repo.CreateHeld(ctx, child, command)
	require.NoError(t, err)
	require.Equal(t, child.ID, accepted.ID)
	restarted := &generationTaskRepository{db: f.db}
	stored, err := restarted.GetOwned(ctx, child.ID, f.user, f.key)
	require.NoError(t, err)
	require.Equal(t, child.Origins, stored.Origins)
	// Updating a parent after acceptance cannot change the private snapshot.
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET data='{"changed":true}'::jsonb WHERE id=$1`, parent.ID)
	require.NoError(t, err)
	stored, err = restarted.GetOwned(ctx, child.ID, f.user, f.key)
	require.NoError(t, err)
	require.Contains(t, string(stored.Origins[0].Data), "9007199254740993")
	public, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NotContains(t, string(public), "private-upstream-parent")
	require.NotContains(t, string(public), "parent-data")
	// Replay remains independent of a now-changed/disabled parent connection.
	_, err = f.db.Exec(`UPDATE accounts SET schedulable=false,credentials='{}'::jsonb WHERE id=$1`, f.account)
	require.NoError(t, err)
	replayed, err := restarted.CreateHeld(ctx, child, command)
	require.NoError(t, err)
	require.Equal(t, child.ID, replayed.ID)
	var balance float64
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
	require.InDelta(t, 98, balance, .00001, "only the parent and one child reserve funds")
	candidates, err := restarted.ListVideoDecodeCandidates(ctx, f.user, f.key, f.group, "openai", parent.Model)
	require.NoError(t, err)
	require.Len(t, candidates, 1, "historical version remains discoverable without a live binding")
	require.Equal(t, f.version, candidates[0].VersionID)
	require.Equal(t, parent.UpstreamModel, candidates[0].UpstreamModel)
	for _, identity := range [][3]int64{{f.user + 1, f.key, f.group}, {f.user, f.key + 1, f.group}, {f.user, f.key, f.group + 1}} {
		_, err := restarted.GetVideoOrigin(ctx, parent.ID, identity[0], identity[1], identity[2])
		require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
		candidates, err := restarted.ListVideoDecodeCandidates(ctx, identity[0], identity[1], identity[2], "openai", parent.Model)
		require.NoError(t, err)
		require.Empty(t, candidates)
	}
}

func TestGenerationOriginLocalAdmissionRechecksAndRollsBack(t *testing.T) {
	f, parent, command := generationOriginSQLFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		edit func(*service.GenerationTask)
	}{
		{"foreign user", func(c *service.GenerationTask) { c.UserID++ }},
		{"foreign key", func(c *service.GenerationTask) { c.APIKeyID++ }},
		{"foreign group", func(c *service.GenerationTask) { c.GroupID++ }},
		{"foreign account", func(c *service.GenerationTask) { c.AccountID++ }},
		{"private id changed", func(c *service.GenerationTask) { c.Origins[0].UpstreamTaskID = "other-private-id" }},
		{"status changed", func(c *service.GenerationTask) { c.Origins[0].Status = "IN_PROGRESS" }},
		{"large integer changed", func(c *service.GenerationTask) {
			c.Origins[0].Data = json.RawMessage(`{"n":9007199254740992,"private":"parent-data"}`)
		}},
		{"connection changed", func(c *service.GenerationTask) { c.Connection.BaseURL = "https://other.example" }},
		{"config changed", func(c *service.GenerationTask) { c.ConfigEncrypted = "changed" }},
		{"self reference", func(c *service.GenerationTask) { c.Origins[0].TaskID = c.ID }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := generationOriginSQLChild(t, parent)
			tc.edit(child)
			cmd := *command
			cmd.UserID, cmd.APIKeyID, cmd.AccountID = child.UserID, child.APIKeyID, child.AccountID
			_, err := f.repo.CreateHeld(ctx, child, &cmd)
			require.Error(t, err)
			var count int
			require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM sub2api_generation_tasks WHERE id=$1`, child.ID).Scan(&count))
			require.Zero(t, count)
			var balance float64
			require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, f.user).Scan(&balance))
			require.InDelta(t, 99, balance, .00001)
		})
	}
	for _, tc := range []struct{ name, mutate, restore string }{
		{"removed group", `DELETE FROM account_groups WHERE account_id=$1`, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,1)`},
		{"disabled account", `UPDATE accounts SET schedulable=false WHERE id=$1`, `UPDATE accounts SET schedulable=true WHERE id=$1 AND $2::bigint>0`},
		{"changed address", `UPDATE accounts SET credentials=jsonb_set(credentials,'{base_url}','"https://other.example"') WHERE id=$1`, `UPDATE accounts SET credentials=jsonb_set(credentials,'{base_url}','"https://fixture.example.invalid"') WHERE id=$1 AND $2::bigint>0`},
		{"changed mapping", `UPDATE accounts SET credentials=credentials||'{"model_mapping":{"sora-2":"private-alias"}}'::jsonb WHERE id=$1`, `UPDATE accounts SET credentials=credentials-'model_mapping' WHERE id=$1 AND $2::bigint>0`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.db.Exec(tc.mutate, f.account)
			require.NoError(t, err)
			_, err = f.repo.CreateHeld(ctx, generationOriginSQLChild(t, parent), command)
			require.ErrorIs(t, err, service.ErrGenerationOrigin)
			_, err = f.db.Exec(tc.restore, f.account, f.group)
			require.NoError(t, err)
		})
	}
	plugins := NewNewAPIPluginRepository(f.db)
	require.NoError(t, plugins.Activate(ctx, f.version, false))
	_, err := f.repo.CreateHeld(ctx, generationOriginSQLChild(t, parent), command)
	require.ErrorIs(t, err, service.ErrGenerationOrigin)
}

func TestGenerationRemixLocalParentSnapshotAndSingleHold(t *testing.T) {
	f, parent, command := generationOriginSQLFixture(t)
	ctx := context.Background()
	child := generationOriginSQLChild(t, parent)
	child.Action = "remix"
	child.PublicFields = service.GenerationVideoFields{Seconds: "8", Size: "1280x720", Prompt: "Make it blue"}
	child.Request = json.RawMessage(`{"model":"sora-2","prompt":"Make it blue"}`)
	accepted, err := f.repo.CreateHeld(ctx, child, command)
	require.NoError(t, err)
	restarted := NewGenerationTaskRepository(f.db)
	stored, err := restarted.GetOwned(ctx, accepted.ID, f.user, f.key)
	require.NoError(t, err)
	require.Equal(t, "remix", stored.Action)
	require.Equal(t, child.PublicFields, stored.PublicFields)
	require.Equal(t, child.Origins, stored.Origins)
	require.Equal(t, parent.ID, stored.VideoView()["remixed_from_video_id"])
	// An accepted request retains its parent snapshot after a source connection
	// is disabled, and repeating admission never creates another reservation.
	_, err = f.db.Exec(`UPDATE accounts SET schedulable=false WHERE id=$1`, f.account)
	require.NoError(t, err)
	for range 3 {
		replayed, err := restarted.CreateHeld(ctx, child, command)
		require.NoError(t, err)
		require.Equal(t, stored.ID, replayed.ID)
	}
	var balance, frozen float64
	require.NoError(t, f.db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, f.user).Scan(&balance, &frozen))
	require.Equal(t, 98.0, balance)
	require.Equal(t, 2.0, frozen, "one parent and one remix hold survive restart and replay")
	_, err = restarted.GetOwned(ctx, child.ID, f.user+1, f.key)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
}
