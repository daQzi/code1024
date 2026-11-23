package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

type marketplaceSettingsFixture struct {
	SettingRepository
	raw string
	err error
}

func (r *marketplaceSettingsFixture) GetValue(context.Context, string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if r.raw == "" {
		return "", ErrSettingNotFound
	}
	return r.raw, nil
}
func (r *marketplaceSettingsFixture) Set(_ context.Context, key, value string) error {
	if r.err != nil {
		return r.err
	}
	r.raw = value
	return nil
}

type marketplaceInstallFixture struct {
	NewAPIPluginRepository
	stored *NewAPIPluginVersion
}

func (r *marketplaceInstallFixture) Install(_ context.Context, v *NewAPIPluginVersion, actor *int64) (*NewAPIPluginVersion, error) {
	r.stored = v
	v.ID = 77
	return v, nil
}

func TestPluginMarketplaceSourcesPersistenceAndValidation(t *testing.T) {
	ctx := context.Background()
	repo := &marketplaceSettingsFixture{}
	s := &NewAPIPluginService{marketSettings: repo}
	defaults, err := s.MarketplaceSources(ctx)
	require.NoError(t, err)
	require.Len(t, defaults, 3)
	for _, bad := range []string{"https://u:p@example.test/index", "https://example.test/index#fragment", "https://example.test/index?key=x", "//example.test/index", "file:///index", "https://example.test\\@other.test/index"} {
		_, err := s.SaveMarketplaceSources(ctx, []PluginMarketplaceSource{{Name: "fixture", IndexURL: bad}})
		require.Error(t, err, bad)
		require.Empty(t, repo.raw)
	}
	custom := []PluginMarketplaceSource{{Name: " New API official (untrusted claim) ", IndexURL: "https://fixture.example/index.json"}}
	stored, err := s.SaveMarketplaceSources(ctx, custom)
	require.NoError(t, err)
	read, err := s.MarketplaceSources(ctx)
	require.NoError(t, err)
	require.Equal(t, stored, read)
	require.Equal(t, "third_party", PluginMarketplaceKind(read[0].IndexURL))
	_, err = s.SaveMarketplaceSources(ctx, append(read, read[0]))
	require.Error(t, err)
	_, err = s.SaveMarketplaceSources(ctx, []PluginMarketplaceSource{})
	require.NoError(t, err)
	read, err = s.MarketplaceSources(ctx)
	require.NoError(t, err)
	require.Empty(t, read, "an explicitly empty list must not restore defaults")
	repo.err = errors.New("database down")
	_, err = s.MarketplaceSources(ctx)
	require.Error(t, err)
}

func TestPluginMarketplaceAdmissionPinsIdentityAndPreservesFactoryProtection(t *testing.T) {
	s, _, _ := generationPluginFixture(t, "xai")
	repo := &marketplaceInstallFixture{}
	s.repo, s.marketSettings = repo, &marketplaceSettingsFixture{}
	source, err := builtin.Source("xai")
	require.NoError(t, err)
	hash := sha256.Sum256([]byte(source))
	pin := PluginMarketplacePin{IndexURL: PluginMarketplaceOfficial, Key: "xai", Version: "1.0.0", SHA256: hex.EncodeToString(hash[:])}
	// Read the actual baseline version rather than assuming a future script release.
	var actualVersion string
	require.NoError(t, s.pool.With(context.Background(), source, func(c *runner.Client) error { actualVersion = c.Meta.Version; return nil }))
	pin.Version = actualVersion
	for _, scenario := range []string{"sha", "key", "version", "removed", "incomplete"} {
		t.Run(scenario, func(t *testing.T) {
			bad := pin
			switch scenario {
			case "sha":
				bad.SHA256 = strings.Repeat("a", 64)
			case "key":
				bad.Key = "other"
			case "version":
				bad.Version = "99.0.0"
			case "removed":
				bad.IndexURL = "https://removed.example/index"
			case "incomplete":
				bad.SHA256 = ""
			}
			_, err := s.InstallMarketplace(context.Background(), source, bad, nil, false)
			require.Error(t, err)
			require.Nil(t, repo.stored)
		})
	}
	item, err := s.InstallMarketplace(context.Background(), source, pin, nil, false)
	require.NoError(t, err)
	require.False(t, item.Enabled)
	require.False(t, item.Active)
	require.Equal(t, "market:official:"+PluginMarketplaceOfficial, item.Origin)
	repo.stored = nil
	changed := source + "\n// marketplace override"
	hash = sha256.Sum256([]byte(changed))
	pin.SHA256 = hex.EncodeToString(hash[:])
	_, err = s.InstallMarketplace(context.Background(), changed, pin, nil, false)
	require.ErrorContains(t, err, "允许覆盖")
	require.Nil(t, repo.stored)
	_, err = s.InstallMarketplace(context.Background(), changed, pin, nil, true)
	require.NoError(t, err)
}

func TestPluginMarketplaceRejectsDigestBeforeEvaluatingSource(t *testing.T) {
	// No worker or repository is configured: a hash mismatch must return before
	// compilation can run untrusted top-level JavaScript.
	s := &NewAPIPluginService{}
	_, err := s.Install(context.Background(), "while (true) {}", "", nil, PluginInstallOptions{ExpectedSHA256: strings.Repeat("0", 64)})
	require.ErrorContains(t, err, "SHA256")
}
