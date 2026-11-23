package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScaffoldTemplatesPassStandaloneLintAndOfflineFixtures(t *testing.T) {
	for _, kind := range []string{"task", "provider", "ingress"} {
		t.Run(kind, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "adapter")
			var output, stderr bytes.Buffer
			require.Zero(t, Run([]string{"init", kind, directory, "--key", "community-1-example"}, nil, &output, &stderr), stderr.String())
			source := filepath.Join(directory, "plugin.js")
			fixture := filepath.Join(directory, "fixture.json")
			require.Zero(t, Run([]string{"lint", source}, nil, &output, &stderr), stderr.String())
			require.Zero(t, Run([]string{"test", source, "--fixture", fixture}, nil, &output, &stderr), stderr.String())
			require.Contains(t, output.String(), "plugin fixture passed")
			raw, err := os.ReadFile(source)
			require.NoError(t, err)
			require.Contains(t, string(raw), `key: "community-1-example"`)
			require.NotContains(t, string(raw), "example-plugin-key")
		})
	}
}

func TestScaffoldRejectsExistingPathsAndInvalidKeysWithoutChangingFiles(t *testing.T) {
	directory := t.TempDir()
	sentinel := filepath.Join(directory, "plugin.js")
	require.NoError(t, os.WriteFile(sentinel, []byte("existing source"), 0644))
	var output, stderr bytes.Buffer
	require.NotZero(t, Run([]string{"init", "task", directory, "--key", "example"}, nil, &output, &stderr))
	value, err := os.ReadFile(sentinel)
	require.NoError(t, err)
	require.Equal(t, "existing source", string(value))
	for _, args := range [][]string{
		{"init"},
		{"init", "unknown", filepath.Join(directory, "unknown"), "--key", "example"},
		{"init", "task", filepath.Join(directory, "invalid"), "--key", `bad"key`},
		{"init", "task", filepath.Join(directory, "empty"), "--key", ""},
	} {
		require.NotZero(t, Run(args, nil, &output, &stderr))
	}
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	link := filepath.Join(t.TempDir(), "existing-link")
	require.NoError(t, os.Symlink(directory, link))
	require.NotZero(t, Run([]string{"init", "task", link, "--key", "example"}, nil, &output, &stderr))
	value, err = os.ReadFile(sentinel)
	require.NoError(t, err)
	require.Equal(t, "existing source", string(value))
}
