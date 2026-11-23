package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestGenerationNativeLocalPostgresBindingUpgrade(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	// The preceding schema is unchanged; rerun only the new data migration
	// with real pre-upgrade account/version records, then repeat normal startup.
	_, err := db.Exec(`DELETE FROM schema_migrations WHERE filename='259_native_video_plugin_bindings.sql'`)
	require.NoError(t, err)
	plugins := NewNewAPIPluginRepository(db)
	accounts := make(map[string]int64)
	for i, key := range []string{"xai", "doubao", "sub2api-openai-relay"} {
		source, err := builtin.Source(key)
		require.NoError(t, err)
		loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
		require.NoError(t, err)
		version, err := plugins.Install(ctx, &service.NewAPIPluginVersion{Key: loaded.Meta.Key, Version: loaded.Meta.Version, SHA256: strings.Repeat(fmt.Sprint(i+1), 64), Source: source, Meta: loaded.Meta, Capabilities: runner.Descriptor{Task: loaded.Meta.HasTaskPart()}}, nil)
		require.NoError(t, err)
		var account int64
		require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES($1,'openai','apikey',jsonb_build_object('newapi_plugin_version_id',$2::bigint,'api_key','keep-me'),1,1) RETURNING id`, key, version.ID).Scan(&account))
		accounts[key] = account
	}
	var oauth, malformed int64
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) SELECT 'oauth','grok','oauth',credentials,1,1 FROM accounts WHERE id=$1 RETURNING id`, accounts["xai"]).Scan(&oauth))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('malformed','openai','apikey','{"newapi_plugin_version_id":"not-a-number"}',1,1) RETURNING id`).Scan(&malformed))
	require.NoError(t, ApplyMigrations(ctx, db))
	for _, key := range []string{"xai", "doubao"} {
		var encoded []byte
		require.NoError(t, db.QueryRow(`SELECT credentials FROM accounts WHERE id=$1`, accounts[key]).Scan(&encoded))
		var credentials map[string]any
		require.NoError(t, json.Unmarshal(encoded, &credentials))
		require.Equal(t, "keep-me", credentials["api_key"])
		routes := credentials["newapi_plugin_native_videos"].(map[string]any)
		if key == "xai" {
			require.Len(t, routes, 3)
			require.Equal(t, []any{"grok-imagine-video"}, routes[service.GenerationProtocolXAIEdit])
		} else {
			require.Len(t, routes, 1)
			require.Contains(t, routes, service.GenerationProtocolArk)
		}
	}
	for _, id := range []int64{accounts["sub2api-openai-relay"], oauth, malformed} {
		var hasRoutes bool
		require.NoError(t, db.QueryRow(`SELECT credentials ? 'newapi_plugin_native_videos' FROM accounts WHERE id=$1`, id).Scan(&hasRoutes))
		require.False(t, hasRoutes)
	}
	var events, enabled int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE event_type='account_changed'`).Scan(&events))
	require.Equal(t, 2, events)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sub2api_js_plugins WHERE enabled`).Scan(&enabled))
	require.Zero(t, enabled, "upgrade must never enable an installed plugin")
	// Execute the actual migration a second time too, proving its mutation is
	// idempotent independently of the migration runner's checksum guard.
	_, err = db.Exec(`DELETE FROM schema_migrations WHERE filename='259_native_video_plugin_bindings.sql'`)
	require.NoError(t, err)
	require.NoError(t, ApplyMigrations(ctx, db))
	var repeated int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE event_type='account_changed'`).Scan(&repeated))
	require.Equal(t, events, repeated)
	require.NoError(t, ApplyMigrations(ctx, db))
}

func TestGenerationNativeLocalPostgresRecoveryAndSingleSettlement(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	var user, key, group, account int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES('native-plugin@example.invalid','fixture',100) RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES('native-plugin','grok') RETURNING id`).Scan(&group))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id,quota) VALUES($1,'native-plugin-key','fixture',$2,20) RETURNING id`, user, group).Scan(&key))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,concurrency,priority) VALUES('native-plugin','grok','apikey','{}',1,1) RETURNING id`).Scan(&account))
	source, err := builtin.Source("xai")
	require.NoError(t, err)
	loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	plugins := NewNewAPIPluginRepository(db)
	version, err := plugins.Install(ctx, &service.NewAPIPluginVersion{Key: loaded.Meta.Key, Version: loaded.Meta.Version, SHA256: strings.Repeat("a", 64), Source: source, Meta: loaded.Meta, Capabilities: runner.Descriptor{Task: true}}, nil)
	require.NoError(t, err)
	id, err := service.NewGenerationTaskID()
	require.NoError(t, err)
	unit := decimal.RequireFromString("0.02")
	price, err := service.NewGenerationPriceSnapshot(service.GenerationPriceRule{Mode: "fixed", Unit: "second", UnitPrice: &unit}, decimal.NewFromInt(8), nil, decimal.NewFromInt(1))
	require.NoError(t, err)
	task := &service.GenerationTask{ID: id, UserID: user, APIKeyID: key, GroupID: group, AccountID: account, PluginVersionID: version.ID, IdempotencyKey: "native-once", Fingerprint: strings.Repeat("b", 64), Model: "grok-imagine-video", UpstreamModel: "grok-imagine-video", Platform: "grok", Protocol: service.GenerationProtocolXAI, Action: "text_to_video", Request: json.RawMessage(`{"prompt":"ocean","duration":8}`), Price: price, Connection: service.GenerationConnection{BaseURL: "https://pinned.example.com", Kind: "vendor"}}
	command := &service.UsageBillingCommand{UserID: user, APIKeyID: key, AccountID: account, AccountType: service.AccountTypeAPIKey, BalanceCost: 0.16, APIKeyQuotaCost: 0.16, APIKeyRateLimitCost: 0.16}
	repo := NewGenerationTaskRepository(db)
	created, err := repo.CreateHeld(ctx, task, command)
	require.NoError(t, err)
	require.Equal(t, task.ID, created.ID)
	claimed, err := repo.Claim(ctx, "native-worker-before-restart", time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.GenerationSubmitting, claimed.Status)
	claimed.UpstreamID, claimed.Status = "private-upstream", service.GenerationRunning
	require.NoError(t, repo.SaveProgress(ctx, claimed, time.Second))
	// A later account edit/disable or plugin disable does not move or lose an
	// accepted task. A fresh repository instance reads the fixed snapshot.
	_, err = db.Exec(`UPDATE accounts SET credentials='{"base_url":"https://changed.example.com"}',schedulable=false WHERE id=$1`, account)
	require.NoError(t, err)
	require.NoError(t, plugins.Activate(ctx, version.ID, false))
	_, err = db.Exec(`UPDATE sub2api_generation_tasks SET next_run_at=NOW()-INTERVAL '1 second' WHERE id=$1`, id)
	require.NoError(t, err)
	restarted := NewGenerationTaskRepository(db)
	resumed, err := restarted.Claim(ctx, "native-worker-after-restart", time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.GenerationRunning, resumed.Status, "restart must query the already submitted job")
	require.Equal(t, "private-upstream", resumed.UpstreamID)
	require.Equal(t, "https://pinned.example.com", resumed.Connection.BaseURL)
	require.Equal(t, version.ID, resumed.PluginVersionID)
	finished := time.Now().UTC()
	resumed.Status, resumed.DeliveryStatus, resumed.BillingStatus = service.GenerationSucceeded, service.GenerationDeliveryReady, service.GenerationBillingPending
	resumed.FinishedAt = &finished
	resumed.Usage = json.RawMessage(`{"seconds":8,"_host_final_usage":{"seconds":8}}`)
	require.NoError(t, restarted.SaveProgress(ctx, resumed, time.Second))
	_, err = db.Exec(`UPDATE sub2api_generation_tasks SET next_run_at=NOW()-INTERVAL '1 second' WHERE id=$1`, id)
	require.NoError(t, err)
	resumed, err = restarted.Claim(ctx, "native-settlement-worker", time.Minute)
	require.NoError(t, err)
	for range 3 {
		require.NoError(t, restarted.Settle(ctx, resumed, decimal.RequireFromString("0.16"), nil))
	}
	var balance, frozen, used float64
	var records, nativeRecords int
	require.NoError(t, db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, user).Scan(&balance, &frozen))
	require.Equal(t, 99.84, balance)
	require.Zero(t, frozen)
	require.NoError(t, db.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, key).Scan(&used))
	require.Equal(t, 0.16, used)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM usage_logs WHERE user_id=$1`, user).Scan(&records))
	require.Equal(t, 1, records)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sub2api_native_video_tasks WHERE user_id=$1`, user).Scan(&nativeRecords))
	require.Zero(t, nativeRecords, "a plugin ingress must not also create a legacy native log")
	logs, total, err := restarted.(service.TaskLogRepository).ListTaskLogs(ctx, service.TaskLogFilter{UserID: user, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "plugin", logs[0].Source)
	require.Equal(t, service.GenerationProtocolXAI, logs[0].Protocol)
	_, err = restarted.GetOwned(ctx, id, user+1, key)
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
}
