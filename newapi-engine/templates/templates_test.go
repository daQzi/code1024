package templates

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/require"
)

func TestTemplatesKeepScaffoldAndPlaygroundExamplesAligned(t *testing.T) {
	for _, kind := range []string{"task", "provider", "ingress"} {
		source, err := Source(kind, "community-1-example")
		require.NoError(t, err)
		fixture, err := Fixture(kind)
		require.NoError(t, err)
		report, err := jsplugin.ReplayFixture(context.Background(), source, fixture)
		require.NoError(t, err)
		require.Equal(t, report.Total, report.Passed)
		hook, args, err := Example(kind)
		require.NoError(t, err)
		require.NotEmpty(t, hook)
		require.NotEmpty(t, args)
		args[0][0] = '['
		_, next, err := Example(kind)
		require.NoError(t, err)
		require.Equal(t, byte('{'), next[0][0], "template callers cannot mutate a shared example")
	}
	for _, key := range []string{"", "Example", `bad"key`, strings.Repeat("x", 31)} {
		_, err := Source("task", key)
		require.Error(t, err)
	}
	_, _, err := Example("unknown")
	require.Error(t, err)
}
