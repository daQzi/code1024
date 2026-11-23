// Package builtin contains the original compatibility-baseline scripts and
// code1024 channel adapters. Embedding a script does not enable its upstream.
package builtin

import (
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

//go:embed */plugin.js
var scripts embed.FS

func Keys() []string {
	entries, _ := fs.ReadDir(scripts, ".")
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			keys = append(keys, entry.Name())
		}
	}
	slices.Sort(keys)
	return keys
}

func Source(key string) (string, error) {
	if key == "" || strings.ContainsAny(key, "/\\.") {
		return "", fmt.Errorf("invalid builtin plugin key")
	}
	data, err := scripts.ReadFile(key + "/plugin.js")
	if err != nil {
		return "", fmt.Errorf("unknown builtin plugin %q", key)
	}
	return string(data), nil
}
