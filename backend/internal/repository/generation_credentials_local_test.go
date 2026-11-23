package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestGenerationCredentialLocalPostgresBindingAndMaintenance(t *testing.T) {
	db, credentials, expected := credentialLocalFixture(t)
	ctx := context.Background()
	// Task-only grants on a non-OpenAI account must participate in the same
	// bounded maintenance and scheduler projection as ordinary Providers.
	_, err := db.Exec(`UPDATE sub2api_js_plugin_versions SET capabilities='{"task":true,"hooks":["login","refresh","scheduled"]}'::jsonb WHERE id=$1`, expected.PluginVersionID)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET platform='grok',credentials=credentials||'{"newapi_plugin_managed_credentials":true}'::jsonb WHERE id=$1`, expected.AccountID)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT `+pluginCredentialConnectionSQL+` FROM accounts a WHERE a.id=$1`, expected.AccountID).Scan(&expected.Connection))
	due, err := credentials.DuePluginCredentials(ctx)
	require.NoError(t, err)
	require.Contains(t, due, expected.AccountID)
	identity := strings.Repeat("a", 64)
	state, err := credentials.ClaimPluginCredential(ctx, expected, 0)
	require.NoError(t, err)
	state.Public = json.RawMessage(`{"status":"active","task_binding":"` + identity + `","credential":{"account_id":"upstream-identity","expires_at":0}}`)
	state.Encrypted, state.NextRunAt = "private-cipher", time.Now().Add(time.Hour)
	require.NoError(t, credentials.FinishPluginCredential(ctx, state))
	projections, err := NewNewAPIPluginRepository(db).(service.PluginCredentialSchedulingRepository).ReadPluginCredentialScheduling(ctx, []int64{expected.AccountID})
	require.NoError(t, err)
	require.Len(t, projections, 1)
	require.True(t, projections[0].RequiresLogin)
	require.NotContains(t, string(projections[0].Public), identity, "routing never needs a grant or private task binding")
	var user, key, group int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES('task-login@example.invalid','fixture',100) RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('task-login','grok') RETURNING id`).Scan(&group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'task-login','fixture',$2) RETURNING id`, user, group).Scan(&key))
	unit := decimal.NewFromInt(1)
	price, err := service.NewGenerationPriceSnapshot(service.GenerationPriceRule{Mode: "fixed", Unit: "request", UnitPrice: &unit}, unit, nil, unit)
	require.NoError(t, err)
	id, err := service.NewGenerationTaskID()
	require.NoError(t, err)
	task := &service.GenerationTask{ID: id, UserID: user, APIKeyID: key, GroupID: group, AccountID: expected.AccountID, PluginVersionID: expected.PluginVersionID, IdempotencyKey: "task-login", Fingerprint: strings.Repeat("b", 64), Model: "video-model", UpstreamModel: "video-model", Platform: "grok", Protocol: "openai_video", Action: "text_to_video", Request: json.RawMessage(`{"prompt":"fixture"}`), Price: price, Connection: service.GenerationConnection{BaseURL: "https://fixture.example.invalid/v1", Kind: "vendor"}, ConfigEncrypted: "fixture-ciphertext", CredentialBinding: service.GenerationCredentialBinding{Mode: "login", Identity: identity, Connection: expected.Connection}}
	command := &service.UsageBillingCommand{UserID: user, APIKeyID: key, AccountID: expected.AccountID, AccountType: service.AccountTypeAPIKey, BalanceCost: 1, APIKeyQuotaCost: 1, APIKeyRateLimitCost: 1}
	repo := NewGenerationTaskRepository(db)
	held, err := repo.CreateHeld(ctx, task, command)
	require.NoError(t, err)
	require.Equal(t, identity, held.CredentialBinding.Identity)
	reloaded, err := NewGenerationTaskRepository(db).GetOwned(ctx, held.ID, user, key)
	require.NoError(t, err)
	require.JSONEq(t, string(expected.Connection), string(reloaded.CredentialBinding.Connection))
	public, err := json.Marshal(reloaded)
	require.NoError(t, err)
	require.NotContains(t, string(public), identity)
	// Allow another admission, but revoke between its estimate and transaction.
	_, err = db.Exec(`UPDATE sub2api_generation_tasks SET status='succeeded',upstream_id='parent-id' WHERE id=$1`, held.ID)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE sub2api_plugin_credential_state SET public=public||'{"status":"revoke_pending"}'::jsonb WHERE account_id=$1`, expected.AccountID)
	require.NoError(t, err)
	next := *task
	next.ID, err = service.NewGenerationTaskID()
	require.NoError(t, err)
	next.IdempotencyKey = "revoked-before-freeze"
	_, err = repo.CreateHeld(ctx, &next, command)
	require.ErrorIs(t, err, service.ErrGenerationCredential)
	var balance, frozen decimal.Decimal
	require.NoError(t, db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, user).Scan(&balance, &frozen))
	require.Equal(t, "99", balance.String())
	require.Equal(t, "1", frozen.String())
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sub2api_generation_tasks WHERE id=$1`, next.ID).Scan(&count))
	require.Zero(t, count, "credential races roll back the task insert and all funding effects")
	// Even with the same account/URL/plugin, a parent from another login is
	// rejected before freezing a derived task.
	_, err = db.Exec(`UPDATE sub2api_plugin_credential_state SET public=public||jsonb_build_object('status','active','task_binding',$2::text) WHERE account_id=$1`, expected.AccountID, strings.Repeat("c", 64))
	require.NoError(t, err)
	next.CredentialBinding.Identity = strings.Repeat("c", 64)
	next.Origins = []service.GenerationOriginTask{{TaskID: held.ID, UpstreamTaskID: "parent-id", Action: task.Action, Status: "SUCCESS", Data: json.RawMessage(`{}`)}}
	_, err = repo.CreateHeld(ctx, &next, command)
	require.ErrorIs(t, err, service.ErrGenerationOrigin)
	parent, err := repo.(service.GenerationVideoRoutingRepository).GetVideoOrigin(ctx, held.ID, user, key, group)
	require.NoError(t, err)
	require.Equal(t, identity, parent.CredentialBinding.Identity)
	// JWT/static snapshots that explicitly pin a hashed connection are checked
	// even without a managed grant. A gateway-kind or key edit cannot race hold.
	next.Origins = nil
	next.CredentialBinding = service.GenerationCredentialBinding{Mode: "static", Connection: expected.Connection}
	_, err = db.Exec(`DELETE FROM sub2api_plugin_credential_state WHERE account_id=$1`, expected.AccountID)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET credentials=credentials||'{"newapi_upstream_kind":"new_api"}'::jsonb WHERE id=$1`, expected.AccountID)
	require.NoError(t, err)
	_, err = repo.CreateHeld(ctx, &next, command)
	require.ErrorIs(t, err, service.ErrGenerationCredential)
	_, err = db.Exec(`UPDATE accounts SET credentials=credentials||'{"newapi_upstream_kind":"vendor","api_key":"replacement-static-key"}'::jsonb WHERE id=$1`, expected.AccountID)
	require.NoError(t, err)
	_, err = repo.CreateHeld(ctx, &next, command)
	require.ErrorIs(t, err, service.ErrGenerationCredential)
	require.NoError(t, db.QueryRow(`SELECT `+pluginCredentialConnectionSQL+` FROM accounts a WHERE a.id=$1`, expected.AccountID).Scan(&next.CredentialBinding.Connection))
	_, err = repo.CreateHeld(ctx, &next, command)
	require.NoError(t, err)
}
