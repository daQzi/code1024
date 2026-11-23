package plugin_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/plugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

type runtimeFixture struct {
	calls int
	key   string
}

func (r *runtimeFixture) With(_ context.Context, _ string, fn func(*runner.Client) error) error {
	r.calls++
	return fn(&runner.Client{Meta: jsplugin.Meta{Key: r.key, Version: "1.0.0"}, Descriptor: runner.Descriptor{Task: true}})
}

type registryFixture struct {
	plugin.Repository
	stored      *plugin.Version
	actor       *int64
	activations int
}

func (r *registryFixture) Install(_ context.Context, version *plugin.Version, actor *int64) (*plugin.Version, error) {
	copy := *version
	copy.ID = 7
	r.stored, r.actor = &copy, actor
	return &copy, nil
}
func (r *registryFixture) Get(context.Context, int64) (*plugin.Version, error) {
	copy := *r.stored
	return &copy, nil
}
func (r *registryFixture) List(context.Context) ([]plugin.Version, error) {
	return []plugin.Version{*r.stored}, nil
}
func (r *registryFixture) Activate(_ context.Context, _ int64, enabled bool) error {
	r.activations++
	r.stored.Enabled = enabled
	return nil
}

func TestInstallPinsDigestBeforeRuntimeAndIdentityBeforeStorage(t *testing.T) {
	runtime := &runtimeFixture{key: "community-7-demo"}
	repo := &registryFixture{}
	manager := plugin.NewManager(repo, runtime, nil)
	source := "// fixed offline fixture"
	hash := sha256.Sum256([]byte(source))
	digest := hex.EncodeToString(hash[:])
	_, err := manager.Install(context.Background(), source, "", nil, plugin.InstallOptions{ExpectedSHA256: strings.Repeat("0", 64)})
	require.Error(t, err)
	require.Zero(t, runtime.calls, "digest mismatches must not evaluate untrusted source")
	_, err = manager.Install(context.Background(), source, "", nil, plugin.InstallOptions{ExpectedKey: "another-key"})
	require.Error(t, err)
	require.Nil(t, repo.stored)
	author := int64(7)
	version, err := manager.Install(context.Background(), source, "", &author, plugin.InstallOptions{ExpectedKey: runtime.key, ExpectedVersion: "1.0.0", ExpectedSHA256: digest, Origin: "community:42"})
	require.NoError(t, err)
	require.Equal(t, digest, version.SHA256)
	require.Equal(t, "community:42", version.Origin)
	require.Equal(t, &author, repo.actor)
	require.False(t, version.Enabled, "installing a script never activates it")
	require.False(t, version.Compatibility.Runnable, "a host must explicitly supply supported execution paths")
	require.Error(t, manager.Activate(context.Background(), version.ID, true))
	require.Zero(t, repo.activations)
	require.NoError(t, manager.Activate(context.Background(), version.ID, false))
	require.Equal(t, 1, repo.activations)
}

func TestShippedKeysRequireExplicitOverride(t *testing.T) {
	repo := &registryFixture{}
	runtime := &runtimeFixture{key: "xai"}
	manager := plugin.NewManager(repo, runtime, nil)
	_, err := manager.Install(context.Background(), "// changed xai source", "", nil)
	require.ErrorContains(t, err, "允许覆盖")
	require.Nil(t, repo.stored)
	version, err := manager.Install(context.Background(), "// changed xai source", "", nil, plugin.InstallOptions{AllowBuiltinOverride: true})
	require.NoError(t, err)
	require.Equal(t, "xai", version.Key)
	require.False(t, version.Enabled)
}

func TestFactoryCatalogMetadataDoesNotLeakMutationBetweenHosts(t *testing.T) {
	items, err := plugin.Factories()
	require.NoError(t, err)
	require.Len(t, items, 21)
	key, name := items[0].Key, items[0].Meta.Name
	items[0].Key, items[0].Meta.Name = "changed", "changed"
	for i := range items {
		if len(items[i].Meta.Models) > 0 {
			items[i].Meta.Models[0] = "changed"
		}
	}
	next, err := plugin.Factories()
	require.NoError(t, err)
	require.Equal(t, key, next[0].Key)
	require.Equal(t, name, next[0].Meta.Name)
	for _, item := range next {
		require.NotContains(t, item.Meta.Models, "changed")
	}
}

func TestInspectionAndArtifactsDoNotRequireHostDatabase(t *testing.T) {
	manager := plugin.NewManager(nil, &runtimeFixture{key: "community-7-demo"}, nil)
	version, err := manager.Inspect(context.Background(), "// read-only inspection")
	require.NoError(t, err)
	source, err := plugin.CheckedSource(plugin.Source{Key: version.Key, Version: version.Version, SHA256: version.SHA256, Source: version.Source})
	require.NoError(t, err)
	source.Source += " changed"
	_, err = plugin.CheckedSource(*source)
	require.ErrorContains(t, err, "digest mismatch")
	_, err = manager.Inspect(context.Background(), "")
	var invalid *plugin.ValidationError
	require.ErrorAs(t, err, &invalid)
	_, err = manager.List(context.Background())
	require.ErrorIs(t, err, plugin.ErrRegistryUnavailable)
}
