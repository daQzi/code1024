package provider

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolUsageAccumulatesBoundedDeltasAndPreservesExplicitZero(t *testing.T) {
	var usage ToolUsage
	require.NoError(t, usage.Add(map[string]int{"web_search": 3, "file_search": 0}))
	require.NoError(t, usage.Add(map[string]int{"web_search": 4}))
	require.Equal(t, map[string]int{"web_search": 7, "file_search": 0}, usage.Counts())
	require.NoError(t, usage.Add(map[string]int{"web_search": MaxToolCalls}))
	require.Equal(t, MaxToolCalls, usage.Counts()["web_search"])
	copy := usage.Counts()
	copy["web_search"] = 0
	require.Equal(t, MaxToolCalls, usage.Counts()["web_search"])
	var other ToolUsage
	require.Empty(t, other.Counts(), "each request or turn has its own facts")
}

func TestToolUsageRejectsInvalidOrUnboundedFactsAtomically(t *testing.T) {
	var usage ToolUsage
	require.NoError(t, usage.Add(map[string]int{"existing": 1}))
	for _, counts := range []map[string]int{{"a": 1, "b": -1}, {"": 0}, {"a\nb": 0}, {"a": MaxToolCalls + 1}} {
		require.Error(t, usage.Add(counts))
		require.Equal(t, map[string]int{"existing": 1}, usage.Counts())
	}
	for i := 0; i < MaxToolNames-1; i++ {
		require.NoError(t, usage.Add(map[string]int{fmt.Sprint(i): 0}))
	}
	require.Error(t, usage.Add(map[string]int{"another": 0, "existing": 10}))
	require.Len(t, usage.Counts(), MaxToolNames)
	require.Equal(t, 1, usage.Counts()["existing"])
}
