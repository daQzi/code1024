// Package templates contains local examples for the supported New API v1 host.
// They describe fictional upstreams and are not production vendor adapters.
package templates

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

//go:embed *.js *.fixture.json
var sources embed.FS

func Source(kind, key string) (string, error) {
	if kind != "task" && kind != "provider" && kind != "ingress" {
		return "", fmt.Errorf("unknown plugin template %q", kind)
	}
	if !jsplugin.ValidPluginKey(key) {
		return "", fmt.Errorf("plugin key must contain 1 to 30 lowercase letters, digits, underscores or hyphens")
	}
	raw, err := sources.ReadFile(kind + ".js")
	if err != nil {
		return "", err
	}
	return strings.Replace(string(raw), "example-plugin-key", key, 1), nil
}

// Fixture is the offline golden file shipped with the same template revision.
// It contains fictional connection values, never credentials or live I/O.
func Fixture(kind string) ([]byte, error) {
	if kind != "task" && kind != "provider" && kind != "ingress" {
		return nil, fmt.Errorf("unknown plugin template %q", kind)
	}
	return sources.ReadFile(kind + ".fixture.json")
}

// Example keeps the browser playground and CLI scaffold on the same input.
func Example(kind string) (string, []json.RawMessage, error) {
	raw, err := Fixture(kind)
	if err != nil {
		return "", nil, err
	}
	var fixture jsplugin.Fixture
	if err = json.Unmarshal(raw, &fixture); err != nil {
		return "", nil, err
	}
	if len(fixture.Cases) == 0 {
		return "", nil, fmt.Errorf("plugin template has no fixture example")
	}
	return fixture.Cases[0].Hook, fixture.Cases[0].Args, nil
}
