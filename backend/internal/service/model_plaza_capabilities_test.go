//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlazaCapabilitiesGroupIntersectionAndPrivacy(t *testing.T) {
	reasoning := true
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, GroupIDs: []int64{10}, Credentials: map[string]any{"model_mapping": map[string]any{"routing-alias": "private-a"}}},
		{ID: 2, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, GroupIDs: []int64{10}, Credentials: map[string]any{"model_mapping": map[string]any{"routing-alias": "private-b"}}},
		{ID: 3, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, GroupIDs: []int64{20}, Credentials: map[string]any{"model_mapping": map[string]any{"routing-alias": "private-c"}}},
	}
	for i := range accounts {
		upstream := accounts[i].GetMappedModel("routing-alias")
		input := []string{"text", "image"}
		window := int64(128000)
		output := int64(8000)
		if i == 1 {
			input = []string{"text"}
			window = 64000
			output = 4000
		}
		accounts[i].SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "private-url-or-credential", Models: map[string]UpstreamModelMetadata{upstream: {ID: upstream, DisplayName: "private-title", Description: "private-description", Reasoning: &reasoning, InputModalities: input, ContextWindow: window, MaxOutputTokens: output, CodexToolCapabilities: map[string]json.RawMessage{"private_setting": json.RawMessage(`"secret"`)}}}})
	}
	channel := plazaPricedChannel(1, "aliases", []int64{10, 20}, PlatformOpenAI, "public-model")
	channel.ModelMapping = map[string]map[string]string{PlatformOpenAI: {"public-model": "routing-alias"}}
	svc := newPlazaService([]Channel{channel}, []Group{{ID: 10, Platform: PlatformOpenAI}, {ID: 20, Platform: PlatformOpenAI}}, nil)
	svc.accountRepo = plazaAccountCatalogStub{accounts: accounts}
	groups, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, groups, 2)
	byID := map[int64]*PlazaCapabilities{}
	for _, group := range groups {
		byID[group.ID] = group.Models[0].Capabilities
	}
	require.EqualValues(t, 64000, byID[10].ContextWindow)
	require.EqualValues(t, 4000, byID[10].MaxOutputTokens)
	require.Equal(t, []string{"text"}, byID[10].InputModalities)
	require.Equal(t, &reasoning, byID[10].Reasoning)
	require.EqualValues(t, 128000, byID[20].ContextWindow)
	require.Equal(t, []string{"text", "image"}, byID[20].InputModalities)
	encoded, err := json.Marshal(groups)
	require.NoError(t, err)
	for _, secret := range []string{"private-a", "private-b", "private-c", "private-title", "private-description", "private-url-or-credential", "private_setting", "secret"} {
		require.NotContains(t, string(encoded), secret)
	}
	// An eligible channel without metadata removes that claim from the shared
	// capability; a disabled channel must not change the result.
	accounts[1].Extra = nil
	accounts[1].Schedulable = false
	svc.accountRepo = plazaAccountCatalogStub{accounts: accounts}
	groups, err = svc.ListGroups(context.Background())
	require.NoError(t, err)
	for _, group := range groups {
		if group.ID == 10 {
			require.EqualValues(t, 128000, group.Models[0].Capabilities.ContextWindow)
		}
	}
	accounts[1].Schedulable = true
	svc.accountRepo = plazaAccountCatalogStub{accounts: accounts}
	groups, err = svc.ListGroups(context.Background())
	require.NoError(t, err)
	for _, group := range groups {
		if group.ID == 10 {
			require.Nil(t, group.Models[0].Capabilities)
		}
	}
}
