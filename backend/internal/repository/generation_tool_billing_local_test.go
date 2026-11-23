package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/provider"
	"github.com/stretchr/testify/require"
)

func TestGenerationProviderToolBillingPostgresUpgradeAndInsertPaths(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	// Rehearse the additive upgrade with an actual pre-column usage row.
	_, err := db.Exec(`ALTER TABLE usage_logs DROP COLUMN provider_tool_billing;
DELETE FROM schema_migrations WHERE filename='288_usage_log_provider_tool_billing.sql'`)
	require.NoError(t, err)
	var user, key, account, historical int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES('tool-billing@example.invalid','fixture',100) RETURNING id`).Scan(&user))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name) VALUES($1,'tool-billing-key','fixture') RETURNING id`, user).Scan(&key))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials) VALUES('tool-billing','openai','apikey','{}') RETURNING id`).Scan(&account))
	require.NoError(t, db.QueryRow(`INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,total_cost,actual_cost) VALUES($1,$2,$3,'tool-history','public-model',0.02,0.04) RETURNING id`, user, key, account).Scan(&historical))
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, ApplyMigrations(ctx, db))
	repo := newUsageLogRepositoryWithSQL(nil, db)
	old, err := repo.GetByID(ctx, historical)
	require.NoError(t, err)
	require.Empty(t, old.ProviderToolBilling, "old costs cannot reconstruct historical tool prices")
	require.InDelta(t, 0.04, old.ActualCost, 1e-12)
	var counts provider.ToolUsage
	require.NoError(t, counts.Add(map[string]int{"file_search": 3, "free": 2, "unused": 0}))
	charges, err := counts.Charges(func(name string) (float64, bool) {
		return map[string]float64{"file_search": 2.5, "free": 0}[name], name != "unused"
	})
	require.NoError(t, err)
	snapshot, err := charges.Snapshot(2)
	require.NoError(t, err)
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	for _, path := range []string{"single", "batch", "best-effort", "no-result"} {
		t.Run(path, func(t *testing.T) {
			log := &service.UsageLog{UserID: user, APIKeyID: key, AccountID: account, RequestID: "tool-" + path, Model: "public-model", TotalCost: 0.0075, ActualCost: 0.015, ProviderToolBilling: encoded, CreatedAt: time.Now()}
			switch path {
			case "single":
				inserted, insertErr := repo.createSingle(ctx, db, log)
				require.NoError(t, insertErr)
				require.True(t, inserted)
			case "batch":
				inserted, insertErr := repo.Create(ctx, log)
				require.NoError(t, insertErr)
				require.True(t, inserted)
			case "best-effort":
				require.NoError(t, repo.CreateBestEffort(ctx, log))
			case "no-result":
				require.NoError(t, execUsageLogInsertNoResult(ctx, db, prepareUsageLogInsert(log)))
			}
			var id int64
			require.NoError(t, db.QueryRow(`SELECT id FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, log.RequestID, key).Scan(&id))
			saved, readErr := repo.GetByID(ctx, id)
			require.NoError(t, readErr)
			require.JSONEq(t, string(encoded), string(saved.ProviderToolBilling))
			log.ProviderToolBilling = json.RawMessage(`{"schema_version":1,"items":[],"actual_usd":999}`)
			inserted, insertErr := repo.Create(ctx, log)
			require.NoError(t, insertErr)
			require.False(t, inserted)
			saved, readErr = repo.GetByID(ctx, id)
			require.NoError(t, readErr)
			require.JSONEq(t, string(encoded), string(saved.ProviderToolBilling), "deduplication must retain the original price trace")
		})
	}
	// The trace is part of the same row and transaction, never a follow-up
	// metadata write that could survive its usage record's rollback.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	log := &service.UsageLog{UserID: user, APIKeyID: key, AccountID: account, RequestID: "tool-rollback", Model: "public-model", ProviderToolBilling: encoded}
	_, err = repo.createSingle(ctx, tx, log)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM usage_logs WHERE request_id=$1`, log.RequestID).Scan(&n))
	require.Zero(t, n)
	// Old binaries name their INSERT columns explicitly. Keeping this nullable
	// addition permits a rollback without dropping the recorded snapshots.
	_, err = db.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model) VALUES($1,$2,$3,$4,'public-model')`, user, key, account, fmt.Sprintf("old-writer-%d", time.Now().UnixNano()))
	require.NoError(t, err)
}
