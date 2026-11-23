package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

func TestPluginFactoryCatalogAndHostCompatibility(t *testing.T) {
	factories, err := PluginFactories()
	require.NoError(t, err)
	require.Len(t, factories, 21)
	for _, factory := range factories {
		require.NotEmpty(t, factory.Version)
		require.Len(t, factory.SHA256, 64)
		if factory.Key == "sub2api-official5" || factory.Key == "sub2api-dreamina" {
			require.True(t, factory.Compatibility.Runnable)
		}
		if factory.Key == "sub2api-openai-relay" {
			require.True(t, factory.Compatibility.Runnable)
			require.Equal(t, []string{"provider"}, factory.Compatibility.Kinds)
		}
		if factory.Key == "sub2api-openai-video" {
			require.True(t, factory.Compatibility.Runnable)
			require.Equal(t, []string{"task"}, factory.Compatibility.Kinds)
		}
		if factory.Capabilities.Transform && !factory.Capabilities.Task {
			if factory.Key == "request-filter" || factory.Key == "request-redact" || factory.Key == "auto-reasoning" || factory.Key == "param-override" {
				require.True(t, factory.Compatibility.Runnable)
			}
		}
	}
	compat := AssessPluginHost(jsplugin.Meta{Models: []string{"test"}, Protocols: []jsplugin.ProtocolClaim{{Name: "openai_video"}}}, runner.Descriptor{Task: true, RequiredCapabilities: []string{"unimplemented"}})
	require.False(t, compat.Runnable)
	require.Contains(t, compat.Blockers, "required capability unavailable: unimplemented")
	egress := AssessPluginHost(jsplugin.Meta{}, runner.Descriptor{Transform: true, Hooks: []string{"egress"}})
	require.True(t, egress.Runnable)
	require.Equal(t, []string{"transform"}, egress.Kinds)
}
