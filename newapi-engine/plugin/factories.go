package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

type Factory struct {
	Key           string            `json:"key"`
	Version       string            `json:"version"`
	SHA256        string            `json:"sha256"`
	Meta          jsplugin.Meta     `json:"meta"`
	Capabilities  runner.Descriptor `json:"capabilities"`
	Compatibility Compatibility     `json:"compatibility"`
}

var factoryCatalog struct {
	sync.Once
	items []Factory
	err   error
}

// Only embedded, reviewed scripts compile in the host. Uploaded/community
// scripts always go through the isolated worker pool.
func Factories() ([]Factory, error) {
	factoryCatalog.Do(func() {
		for _, key := range builtin.Keys() {
			source, err := builtin.Source(key)
			if err != nil {
				factoryCatalog.err = err
				return
			}
			plugin, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1, Timeout: time.Second, Log: func(string) {}, HostCapability: runner.SupportsCapability})
			if err != nil {
				factoryCatalog.err = fmt.Errorf("builtin %s: %w", key, err)
				return
			}
			hash := sha256.Sum256([]byte(source))
			entry := Factory{Key: key, Version: plugin.Meta.Version, SHA256: hex.EncodeToString(hash[:]), Meta: plugin.Meta, Capabilities: runner.Describe(plugin)}
			factoryCatalog.items = append(factoryCatalog.items, entry)
		}
	})
	if factoryCatalog.err != nil {
		return nil, factoryCatalog.err
	}
	// Callers may edit their returned metadata/config schemas without changing
	// the cached shipped catalog used by another host or request.
	raw, err := json.Marshal(factoryCatalog.items)
	if err != nil {
		return nil, err
	}
	var items []Factory
	if err = json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (m *Manager) Factories() ([]Factory, error) {
	items, err := Factories()
	for i := range items {
		items[i].Compatibility = m.assess(items[i].Meta, items[i].Capabilities)
	}
	return items, err
}
