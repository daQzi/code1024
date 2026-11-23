package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenericVideoPluginRequiresExplicitAccountModels(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		NewAPIPluginRevisionKey:    1,
		newAPIPluginVideoModelsKey: []string{"*"},
	}}
	require.Empty(t, account.NewAPIVideoModels())
	require.False(t, account.SupportsNewAPIVideoModel("gpt-text"))
	account.Credentials["model_mapping"] = map[string]any{"*": "upstream-video"}
	require.Empty(t, account.NewAPIVideoModels())
	require.False(t, account.SupportsNewAPIVideoModel("anything"))
	account.Credentials["model_mapping"] = map[string]any{"my-video": "vendor-movie-v2", "*": "vendor-fallback"}
	require.Equal(t, []string{"my-video"}, account.NewAPIVideoModels())
	require.True(t, account.SupportsNewAPIVideoModel("my-video"))
	require.False(t, account.SupportsNewAPIVideoModel("vendor-movie-v2"))
	require.False(t, account.SupportsNewAPIVideoModel("gpt-text"))
	require.False(t, account.SupportsNewAPIVideoModel("*"))

	// Scheduler/database transport must retain the explicit model boundary.
	raw, err := json.Marshal(account.Credentials)
	require.NoError(t, err)
	projected := &Account{Platform: account.Platform, Type: account.Type}
	require.NoError(t, json.Unmarshal(raw, &projected.Credentials))
	require.Equal(t, []string{"my-video"}, projected.NewAPIVideoModels())
	require.False(t, projected.SupportsNewAPIVideoModel("unconfigured-video"))
}

func TestGenericVideoPluginRejectsUnconfiguredBinding(t *testing.T) {
	plugins, account, upstream := generationPluginFixture(t, "sub2api-openai-video")
	for _, mapping := range []map[string]any{nil, {}, {"*": "vendor-video"}, {"video-*": "vendor-video"}, {"public-video": "*"}} {
		account.Credentials["model_mapping"] = mapping
		err := plugins.NormalizeAccountBinding(context.Background(), account, map[string]any{})
		require.ErrorContains(t, err, "通用视频插件需要至少一个具体模型")
	}
	require.Empty(t, upstream.requests)
}

func TestGenericVideoPluginNormalizesExplicitBinding(t *testing.T) {
	plugins, account, upstream := genericVideoFixture(t)
	require.NoError(t, plugins.NormalizeAccountBinding(context.Background(), account, map[string]any{}))
	require.Equal(t, []string{"my-video"}, account.NewAPIVideoModels())
	config, err := plugins.encryptor.Decrypt(account.GetCredential(newAPIPluginConfigEncryptedKey))
	require.NoError(t, err)
	require.JSONEq(t, `{"submission_format":"multipart"}`, config)
	require.Empty(t, upstream.requests)
}

func TestGenericVideoPluginRoutingRequiresExplicitAccountModels(t *testing.T) {
	plugins, _, upstream := generationPluginFixture(t, "sub2api-openai-video")
	repo := &generationOriginFixture{candidates: []GenerationVideoDecodeCandidate{
		{VersionID: 1, Mapping: map[string]any{"my-video": "vendor-movie", "*": "vendor-fallback"}},
		{VersionID: 1, UpstreamModel: "historical-model"},
	}}
	s := &GenerationService{repo: repo, plugins: plugins}
	key := &APIKey{UserID: 1, ID: 2, Group: &Group{ID: 3}}
	for _, model := range []string{"my-video", "unknown-video", "gpt-text", "*"} {
		_, needed, err := s.VideoPluginRoutingNeeded(context.Background(), key, PlatformOpenAI, model, GenerationProtocolOpenAI)
		require.NoError(t, err)
		require.Equal(t, model == "my-video", needed, model)
	}
	require.Empty(t, upstream.requests)
}
