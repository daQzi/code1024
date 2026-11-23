package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/stretchr/testify/require"
)

func TestNewAPIPluginVersionInfoDoesNotExecuteOrMutateArtifact(t *testing.T) {
	version := &NewAPIPluginVersion{ID: 91, Key: "historical-fixture", Version: "0.9.0", Source: "throw Error('never execute metadata');"}
	s := &NewAPIPluginService{repo: &pluginSourceRepo{version: version}}
	info, err := s.VersionInfo(context.Background(), 91)
	require.NoError(t, err)
	require.NotSame(t, version, info)
	require.EqualValues(t, 91, info.ID)
	require.Empty(t, info.Source)
	require.NotEmpty(t, version.Source)
	raw, err := json.Marshal(info)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "never execute")
	require.NotContains(t, string(raw), `"source":`)
}

func TestNewAPIPluginVersionInfoGuards(t *testing.T) {
	for _, scenario := range []string{"invalid-id", "missing", "db-missing", "db-error", "wrong-id", "capacity", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			repo := &pluginSourceRepo{version: &NewAPIPluginVersion{ID: 91}}
			id, capacity := int64(91), int64(newAPIProviderMemory)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "invalid-id":
				id = 0
			case "missing":
				repo.version = nil
			case "db-missing":
				repo.err = sql.ErrNoRows
			case "db-error":
				repo.err = errors.New("unavailable")
			case "wrong-id":
				repo.version.ID = 92
			case "capacity":
				capacity = 1
			case "cancelled":
				cancel()
			}
			budget := pkghttputil.NewBodyMemoryBudget(capacity)
			ctx, release := pkghttputil.WithRequestBodyMemoryBudget(ctx, budget)
			defer release()
			info, err := (&NewAPIPluginService{repo: repo}).VersionInfo(ctx, id)
			require.Error(t, err)
			require.Nil(t, info)
			if scenario == "db-missing" || scenario == "missing" || scenario == "wrong-id" {
				require.ErrorIs(t, err, errPluginSourceMissing)
			}
			if scenario == "cancelled" {
				require.ErrorIs(t, err, context.Canceled)
			}
			check, done := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
			defer done()
			require.NoError(t, pkghttputil.ReserveRequestBodyMemory(check, capacity))
		})
	}
}

type pluginVersionInfoBlockedRepo struct {
	NewAPIPluginRepository
	started chan time.Time
}

func (r *pluginVersionInfoBlockedRepo) Get(ctx context.Context, _ int64) (*NewAPIPluginVersion, error) {
	deadline, _ := ctx.Deadline()
	r.started <- deadline
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestNewAPIPluginVersionInfoCancellationReleasesAdmission(t *testing.T) {
	repo := &pluginVersionInfoBlockedRepo{started: make(chan time.Time, 1)}
	budget := pkghttputil.NewBodyMemoryBudget(newAPIProviderMemory)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(ctx, budget)
	defer release()
	done := make(chan error, 1)
	go func() { _, err := (&NewAPIPluginService{repo: repo}).VersionInfo(ctx, 91); done <- err }()
	select {
	case deadline := <-repo.started:
		require.WithinDuration(t, time.Now().Add(5*time.Second), deadline, time.Second)
	case <-time.After(time.Second):
		t.Fatal("metadata read did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled metadata read did not stop")
	}
	check, finish := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
	defer finish()
	require.NoError(t, pkghttputil.ReserveRequestBodyMemory(check, newAPIProviderMemory))
}
