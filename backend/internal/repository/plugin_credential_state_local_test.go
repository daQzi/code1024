package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

func credentialLocalFixture(t *testing.T) (*sql.DB, service.PluginCredentialRepository, *service.PluginCredentialState) {
	t.Helper()
	db := generationLocalDB(t)
	ctx := context.Background()
	plugins := NewNewAPIPluginRepository(db)
	v, err := plugins.Install(ctx, &service.NewAPIPluginVersion{Key: "credential-fixture", Version: "1.0.0", SHA256: strings.Repeat("a", 64), Source: "// inert test fixture", Meta: jsplugin.Meta{Key: "credential-fixture", Version: "1.0.0"}, Capabilities: runner.Descriptor{Provider: true, Hooks: []string{"login", "refresh", "scheduled"}}}, nil)
	require.NoError(t, err)
	require.NoError(t, plugins.Activate(ctx, v.ID, true))
	creds := map[string]any{"newapi_plugin_version_id": v.ID, "base_url": "https://fixture.example.invalid/v1", "api_key": "fixture-private-key", "newapi_plugin_config_encrypted": "fixture-ciphertext"}
	raw, err := json.Marshal(creds)
	require.NoError(t, err)
	var id int64
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('fixture','openai','apikey',$1,2,1) RETURNING id`, raw).Scan(&id))
	values := []any{"openai", "apikey", nil, v.ID}
	for _, name := range []string{"base_url", "api_key", "newapi_plugin_config_encrypted"} {
		sum := sha256.Sum256([]byte(creds[name].(string)))
		values = append(values, hex.EncodeToString(sum[:]))
	}
	connection, err := json.Marshal(values)
	require.NoError(t, err)
	var sqlConnection string
	require.NoError(t, db.QueryRow(`SELECT `+pluginCredentialConnectionSQL+` FROM accounts a WHERE a.id=$1`, id).Scan(&sqlConnection))
	require.JSONEq(t, string(connection), sqlConnection, "Go connection projection must match SQL")
	return db, plugins.(service.PluginCredentialRepository), &service.PluginCredentialState{AccountID: id, PluginVersionID: v.ID, Connection: connection}
}

func TestPluginCredentialLocalPostgresClaimRevisionRecovery(t *testing.T) {
	db, repo, expected := credentialLocalFixture(t)
	ctx := context.Background()
	state, err := repo.ReadPluginCredential(ctx, expected.AccountID)
	require.NoError(t, err)
	require.Nil(t, state)
	var wg sync.WaitGroup
	results := make(chan *service.PluginCredentialState, 8)
	for range 8 {
		wg.Go(func() {
			claimed, err := repo.ClaimPluginCredential(ctx, expected, 0)
			if err == nil {
				results <- claimed
			} else {
				require.ErrorIs(t, err, service.ErrPluginCredentialConflict)
			}
		})
	}
	wg.Wait()
	close(results)
	var claims []*service.PluginCredentialState
	for claimed := range results {
		claims = append(claims, claimed)
	}
	require.Len(t, claims, 1, "replicas must share a single database lease")
	state = claims[0]
	state.Encrypted, state.Public, state.NextRunAt = "fixture-cipher", json.RawMessage(`{"status":"active"}`), time.Now().Add(time.Hour)
	require.NoError(t, repo.FinishPluginCredential(ctx, state))
	require.ErrorIs(t, repo.FinishPluginCredential(ctx, state), service.ErrPluginCredentialConflict)
	// Recreating the repository represents a fresh process, not a local cache.
	repo = NewNewAPIPluginRepository(db).(service.PluginCredentialRepository)
	read, err := repo.ReadPluginCredential(ctx, state.AccountID)
	require.NoError(t, err)
	require.Equal(t, state.Revision+1, read.Revision)
	require.Equal(t, "fixture-cipher", read.Encrypted)
	require.Empty(t, read.LeaseToken)
	_, err = repo.ClaimPluginCredential(ctx, expected, state.Revision)
	require.ErrorIs(t, err, service.ErrPluginCredentialConflict)
	lease, err := repo.ClaimPluginCredential(ctx, expected, read.Revision)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE sub2api_plugin_credential_state SET lease_until=NOW()-INTERVAL '1 second' WHERE account_id=$1`, expected.AccountID)
	require.NoError(t, err)
	replacement, err := repo.ClaimPluginCredential(ctx, expected, read.Revision)
	require.NoError(t, err)
	require.NotEqual(t, lease.LeaseToken, replacement.LeaseToken)
	lease.Public, lease.Encrypted, lease.NextRunAt = state.Public, state.Encrypted, state.NextRunAt
	require.ErrorIs(t, repo.FinishPluginCredential(ctx, lease), service.ErrPluginCredentialConflict)
	require.NoError(t, repo.ReleasePluginCredential(ctx, lease), "late release cannot clear another replica's lease")
	_, err = repo.ClaimPluginCredential(ctx, expected, read.Revision)
	require.ErrorIs(t, err, service.ErrPluginCredentialConflict)
	require.NoError(t, repo.ReleasePluginCredential(ctx, replacement))
	require.NoError(t, ApplyMigrations(ctx, db))
}

func TestPluginCredentialLocalPostgresConnectionAndDue(t *testing.T) {
	db, repo, expected := credentialLocalFixture(t)
	ctx := context.Background()
	ids, err := repo.DuePluginCredentials(ctx)
	require.NoError(t, err)
	require.Contains(t, ids, expected.AccountID, "discover saved login accounts without local state")
	state, err := repo.ClaimPluginCredential(ctx, expected, 0)
	require.NoError(t, err)
	state.Encrypted, state.Public, state.NextRunAt = "sealed", json.RawMessage(`{"status":"active"}`), time.Now().Add(time.Hour)
	require.NoError(t, repo.FinishPluginCredential(ctx, state))
	ids, err = repo.DuePluginCredentials(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)
	expected.OnlyIfDue = true
	_, err = repo.ClaimPluginCredential(ctx, expected, 0)
	require.ErrorIs(t, err, service.ErrPluginCredentialConflict, "queued stale IDs cannot run twice")
	expected.OnlyIfDue = false
	lease, err := repo.ClaimPluginCredential(ctx, expected, 0)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET credentials=jsonb_set(credentials,'{api_key}','"rotated"') WHERE id=$1`, expected.AccountID)
	require.NoError(t, err)
	lease.Encrypted, lease.Public, lease.NextRunAt = state.Encrypted, state.Public, state.NextRunAt
	require.ErrorIs(t, repo.FinishPluginCredential(ctx, lease), service.ErrPluginCredentialConflict, "old refresh must not overwrite an edited account")
	_, err = repo.ClaimPluginCredential(ctx, expected, 0)
	require.ErrorIs(t, err, service.ErrPluginCredentialConflict)
	require.NoError(t, db.QueryRow(`SELECT `+pluginCredentialConnectionSQL+` FROM accounts a WHERE id=$1`, expected.AccountID).Scan(&expected.Connection))
	reset, err := repo.ClaimPluginCredential(ctx, expected, 0)
	require.NoError(t, err)
	require.Empty(t, reset.Encrypted)
	require.JSONEq(t, `{}`, string(reset.Public))
	require.JSONEq(t, string(expected.Connection), string(reset.Connection))
	require.NotEqual(t, lease.LeaseToken, reset.LeaseToken)
	require.ErrorIs(t, repo.FinishPluginCredential(ctx, lease), service.ErrPluginCredentialConflict)
	require.NoError(t, repo.ReleasePluginCredential(ctx, reset))
	_, err = db.Exec(`UPDATE accounts SET status='inactive' WHERE id=$1`, expected.AccountID)
	require.NoError(t, err)
	ids, err = repo.DuePluginCredentials(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)
	_, err = db.Exec(`DELETE FROM accounts WHERE id=$1`, expected.AccountID)
	require.NoError(t, err)
	read, err := repo.ReadPluginCredential(ctx, expected.AccountID)
	require.NoError(t, err)
	require.Nil(t, read, "hard deletion cascades private state")
}

func TestPluginCredentialLocalPostgresDuplicateIdentityIsAtomic(t *testing.T) {
	db, repo, first := credentialLocalFixture(t)
	ctx := context.Background()
	second := *first
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) SELECT 'second',platform,type,credentials,2,1 FROM accounts WHERE id=$1 RETURNING id`, first.AccountID).Scan(&second.AccountID))
	left, err := repo.ClaimPluginCredential(ctx, first, 0)
	require.NoError(t, err)
	right, err := repo.ClaimPluginCredential(ctx, &second, 0)
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, state := range []*service.PluginCredentialState{left, right} {
		state.Encrypted = "sealed"
		state.Public = json.RawMessage(`{"status":"active","credential":{"account_id":"same-upstream-account"}}`)
		state.NextRunAt = time.Now().Add(time.Hour)
		wg.Go(func() { results <- repo.FinishPluginCredential(ctx, state) })
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, service.ErrPluginCredentialIdentity)
			conflict++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
	inUse, err := repo.PluginCredentialInUse(ctx, first.PluginVersionID, "same-upstream-account")
	require.NoError(t, err)
	require.True(t, inUse)
	// A different revision of the same plugin still shares upstream identity.
	installed, err := NewNewAPIPluginRepository(db).Install(ctx, &service.NewAPIPluginVersion{Key: "credential-fixture", Version: "2.0.0", SHA256: strings.Repeat("b", 64), Source: "// fixture v2", Meta: jsplugin.Meta{Key: "credential-fixture", Version: "2.0.0"}, Capabilities: runner.Descriptor{Provider: true}}, nil)
	require.NoError(t, err)
	inUse, err = repo.PluginCredentialInUse(ctx, installed.ID, "same-upstream-account")
	require.NoError(t, err)
	require.True(t, inUse)
}
