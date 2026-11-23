package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGenerationLocalOrderedRequestSurvivesReloadAndLegacy(t *testing.T) {
	f := newGenerationFileSQLFixture(t)
	ctx := context.Background()
	task, command := f.task(t, &service.GenerationFile{})
	task.Files = nil
	task.Request = json.RawMessage(`{"z":{"y":1,"a":"<>&"},"a":9007199254740993}`)
	created, err := f.repo.CreateHeld(ctx, task, command)
	require.NoError(t, err)
	require.Equal(t, string(task.Request), string(created.Request))
	// A new repository reads persistent bytes, not an in-memory task pointer.
	restarted := NewGenerationTaskRepository(f.db)
	loaded, err := restarted.GetOwned(ctx, task.ID, f.user, f.key)
	require.NoError(t, err)
	require.Equal(t, string(task.Request), string(loaded.Request))
	claimed, err := restarted.Claim(ctx, "ordered-worker", time.Minute)
	require.NoError(t, err)
	require.Equal(t, string(task.Request), string(claimed.Request))
	var normalized string
	require.NoError(t, f.db.QueryRow(`SELECT request::text FROM sub2api_generation_tasks WHERE id=$1`, task.ID).Scan(&normalized))
	require.NotEqual(t, string(task.Request), normalized, "JSONB alone cannot preserve client order")
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET request_text='{"different":true}' WHERE id=$1`, task.ID)
	require.Error(t, err, "the raw text must represent the same accepted request")
	require.NoError(t, ApplyMigrations(ctx, f.db), "migration path must be repeatable")
	_, err = f.db.Exec(`UPDATE sub2api_generation_tasks SET request_text=NULL WHERE id=$1`, task.ID)
	require.NoError(t, err)
	legacy, err := restarted.GetOwned(ctx, task.ID, f.user, f.key)
	require.NoError(t, err)
	require.JSONEq(t, string(task.Request), string(legacy.Request), "legacy requests remain readable; lost ordering is not invented")
	var balance, frozen float64
	require.NoError(t, f.db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, f.user).Scan(&balance, &frozen))
	require.Equal(t, 99.0, balance)
	require.Equal(t, 1.0, frozen)
}
