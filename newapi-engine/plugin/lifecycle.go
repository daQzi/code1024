package plugin

import (
	"context"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
)

// RegisterFactoryVersions never changes an existing active version or enables
// a plugin. Executing code still requires an explicit administrator action.
func (m *Manager) RegisterFactoryVersions(ctx context.Context) error {
	if m.repo == nil {
		return ErrRegistryUnavailable
	}
	factories, err := m.Factories()
	if err != nil {
		return err
	}
	for _, f := range factories {
		source, _ := builtin.Source(f.Key)
		_, err = m.repo.Install(ctx, &Version{Key: f.Key, Version: f.Version, SHA256: f.SHA256, Origin: "builtin:" + Baseline + ":" + f.Key, Source: source, Meta: f.Meta, Capabilities: f.Capabilities}, nil)
		// A pre-existing override may have reused the factory version before
		// protected keys were introduced. Preserve it without blocking boot.
		if errors.Is(err, ErrVersionConflict) {
			continue
		}
		if err != nil {
			return fmt.Errorf("register builtin %s: %w", f.Key, err)
		}
	}
	return nil
}

func (m *Manager) RestoreFactory(ctx context.Context, key string, actor *int64) (*Version, error) {
	v, err := m.Install(ctx, "", key, actor)
	if err != nil {
		return nil, err
	}
	// Restoring selects the factory, while keeping a disabled plugin disabled.
	items, err := m.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.Key == key {
			if item.Enabled && !m.assess(v.Meta, v.Capabilities).Runnable {
				return nil, errors.New("内置版本不具备当前宿主可执行能力，请先停用该插件")
			}
			// One SQL update changes only the version, so a concurrent disable
			// cannot accidentally be undone by a stale enabled-state read.
			if err = m.repo.SelectVersion(ctx, v.ID); err != nil {
				return nil, err
			}
			return m.repo.Get(ctx, v.ID)
		}
	}
	return v, nil
}
