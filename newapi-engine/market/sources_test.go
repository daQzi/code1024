package market_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/market"
	"github.com/Wei-Shaw/sub2api/newapi-engine/plugin"
	"github.com/stretchr/testify/require"
)

type settingsFixture struct {
	value string
	saved bool
}

func (s *settingsFixture) GetValue(context.Context, string) (string, error) {
	if !s.saved {
		return "", market.ErrSettingNotFound
	}
	return s.value, nil
}
func (s *settingsFixture) Set(_ context.Context, _ string, value string) error {
	s.saved = true
	s.value = value
	return nil
}

type installerFixture struct {
	calls  int
	option plugin.InstallOptions
}

func (i *installerFixture) Install(_ context.Context, _, _ string, _ *int64, options ...plugin.InstallOptions) (*plugin.Version, error) {
	i.calls++
	i.option = options[0]
	return &plugin.Version{}, nil
}

func TestMarketPreservesExplicitEmptySourcesAndFixedInstallIdentity(t *testing.T) {
	settings, installer := &settingsFixture{}, &installerFixture{}
	service := market.NewService(settings, installer)
	ctx := context.Background()
	sources, err := service.Sources(ctx)
	require.NoError(t, err)
	require.Len(t, sources, 3)
	_, err = service.SaveSources(ctx, []market.Source{})
	require.NoError(t, err)
	sources, err = service.Sources(ctx)
	require.NoError(t, err)
	require.Empty(t, sources, "empty means disabled; do not restore defaults")
	pin := market.Pin{IndexURL: market.Official, Key: "fixture", Version: "1.0.0", SHA256: strings.Repeat("a", 64)}
	_, err = service.Install(ctx, "untrusted source", pin, nil, false)
	require.Error(t, err)
	require.Zero(t, installer.calls)
	_, err = service.SaveSources(ctx, []market.Source{{Name: "untrusted label", IndexURL: market.Official}})
	require.NoError(t, err)
	_, err = service.Install(ctx, "fixed source", pin, nil, true)
	require.NoError(t, err)
	require.Equal(t, pin.SHA256, installer.option.ExpectedSHA256)
	require.Equal(t, pin.Key, installer.option.ExpectedKey)
	require.Equal(t, pin.Version, installer.option.ExpectedVersion)
	require.True(t, installer.option.AllowBuiltinOverride)
	require.Equal(t, "market:official:"+market.Official, installer.option.Origin)
}

type submissionsFixture struct {
	market.RegistryRepository
	item        *market.Submission
	transitions int
}

func (r *submissionsFixture) GetPluginSubmission(context.Context, int64) (*market.Submission, error) {
	return r.item, nil
}
func (r *submissionsFixture) TransitionPluginSubmission(context.Context, int64, int64, int64, string, string, bool) error {
	r.transitions++
	return nil
}

func TestCommunityRegistryChecksOwnershipBeforeEvaluatingSource(t *testing.T) {
	repo := &submissionsFixture{item: &market.Submission{ID: 1, AuthorID: 9, Revision: 1, Status: "draft"}}
	registry := market.NewRegistry(repo, nil, nil) // No runtime: unauthorized code cannot be evaluated.
	_, err := registry.SaveSubmission(context.Background(), 7, 1, 1, "untrusted source", "")
	require.ErrorIs(t, err, market.ErrSubmissionMissing)
	_, err = registry.Submission(context.Background(), 1, 7, false)
	require.ErrorIs(t, err, market.ErrSubmissionMissing)
	require.ErrorIs(t, registry.TransitionSubmission(context.Background(), 1, 7, 1, "publish", "", false), market.ErrSubmissionMissing)
	require.Zero(t, repo.transitions)
	repo.item.AuthorID = 7
	repo.item.Status = "published"
	_, err = registry.SaveSubmission(context.Background(), 7, 1, 1, "untrusted source", "")
	require.ErrorIs(t, err, market.ErrSubmissionConflict)
	_, err = registry.InstallSubmission(context.Background(), 1, 7, "changed digest")
	require.ErrorIs(t, err, market.ErrSubmissionConflict)
	require.Equal(t, "community-7-", market.AuthorNamespace(7))
}
