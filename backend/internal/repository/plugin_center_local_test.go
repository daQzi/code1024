package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGenerationLocalPostgresFactoryRegistrationPreservesOverrides(t *testing.T) {
	db := generationLocalDB(t)
	ctx := context.Background()
	repo := NewNewAPIPluginRepository(db)
	svc, err := service.NewNewAPIPluginService(repo, nil)
	require.NoError(t, err)
	t.Cleanup(svc.Stop)
	require.NoError(t, svc.RegisterFactoryVersions(ctx))
	installed, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, installed, 21)
	var factory service.NewAPIPluginVersion
	for _, item := range installed {
		require.False(t, item.Enabled)
		if item.Key == "xai" {
			factory = item
		}
	}
	require.Positive(t, factory.ID)
	original, err := repo.Get(ctx, factory.ID)
	require.NoError(t, err)
	override := *original
	override.Version, override.Meta.Version = "99.0.0", "99.0.0"
	override.SHA256, override.Source, override.Origin = strings.Repeat("b", 64), original.Source+"\n// override", "uploaded"
	custom, err := repo.Install(ctx, &override, nil)
	require.NoError(t, err)
	require.NoError(t, repo.Activate(ctx, custom.ID, true))
	require.NoError(t, svc.RegisterFactoryVersions(ctx))
	active, err := repo.Get(ctx, custom.ID)
	require.NoError(t, err)
	require.True(t, active.Active)
	require.True(t, active.Enabled)
	require.NoError(t, repo.SelectVersion(ctx, factory.ID))
	active, err = repo.Get(ctx, factory.ID)
	require.NoError(t, err)
	require.True(t, active.Enabled, "selecting a factory must preserve the stored enabled state atomically")
	require.NoError(t, repo.Activate(ctx, custom.ID, false))
	require.NoError(t, repo.SelectVersion(ctx, factory.ID))
	active, err = repo.Get(ctx, factory.ID)
	require.NoError(t, err)
	require.True(t, active.Active)
	require.False(t, active.Enabled, "restore must not implicitly enable code")
	versions, err := repo.Versions(ctx, "xai")
	require.NoError(t, err)
	require.Len(t, versions, 2, "factory registration is idempotent and historical versions are retained")
}
