package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

type Manager struct {
	repo    Repository
	runtime Runtime
	assess  AssessHost
}

func NewManager(repo Repository, runtime Runtime, assess AssessHost) *Manager {
	if assess == nil {
		assess = func(jsplugin.Meta, runner.Descriptor) Compatibility {
			return Compatibility{Kinds: []string{}, Warnings: []string{}, Blockers: []string{"host execution capabilities were not supplied"}}
		}
	}
	return &Manager{repo: repo, runtime: runtime, assess: assess}
}

func (m *Manager) List(ctx context.Context) ([]Version, error) {
	if m.repo == nil {
		return nil, ErrRegistryUnavailable
	}
	items, err := m.repo.List(ctx)
	for i := range items {
		items[i].Compatibility = m.assess(items[i].Meta, items[i].Capabilities)
	}
	return items, err
}
func (m *Manager) Versions(ctx context.Context, key string) ([]Version, error) {
	if !jsplugin.ValidPluginKey(key) {
		return nil, errors.New("invalid plugin key")
	}
	if m.repo == nil {
		return nil, ErrRegistryUnavailable
	}
	items, err := m.repo.Versions(ctx, key)
	for i := range items {
		items[i].Compatibility = m.assess(items[i].Meta, items[i].Capabilities)
	}
	return items, err
}

func (m *Manager) Install(ctx context.Context, source, builtinKey string, actor *int64, options ...InstallOptions) (*Version, error) {
	origin := "uploaded"
	if builtinKey != "" {
		if source != "" {
			return nil, errors.New("choose source or builtin, not both")
		}
		var err error
		source, err = builtin.Source(builtinKey)
		if err != nil {
			return nil, err
		}
		origin = "builtin:" + Baseline + ":" + builtinKey
	}
	version := &Version{Source: source, Origin: origin}
	var option InstallOptions
	if len(options) > 0 {
		option = options[0]
	}
	hash := sha256.Sum256([]byte(source))
	version.SHA256 = hex.EncodeToString(hash[:])
	if option.ExpectedSHA256 != "" && !strings.EqualFold(option.ExpectedSHA256, version.SHA256) {
		return nil, errors.New("插件源码 SHA256 不匹配")
	}
	if m.runtime == nil {
		return nil, errors.New("plugin runtime unavailable")
	}
	err := m.runtime.With(ctx, source, func(client *runner.Client) error {
		version.Meta = client.Meta
		version.Capabilities = client.Descriptor
		version.Key, version.Version = client.Meta.Key, client.Meta.Version
		return nil
	})
	if err != nil {
		return nil, err
	}
	if option.Origin != "" {
		version.Origin = option.Origin
	}
	if option.ExpectedKey != "" && option.ExpectedKey != version.Key || option.ExpectedVersion != "" && option.ExpectedVersion != version.Version {
		return nil, errors.New("插件名称或版本与市场条目不匹配")
	}
	if builtinKey == "" && !option.AllowBuiltinOverride {
		factories, err := m.Factories()
		if err != nil {
			return nil, err
		}
		for _, f := range factories {
			if f.Key == version.Key && f.SHA256 != version.SHA256 {
				return nil, errors.New("该 key 属于系统插件，覆盖前请明确勾选允许覆盖内置插件")
			}
		}
	}
	if m.repo == nil {
		return nil, ErrRegistryUnavailable
	}
	installed, err := m.repo.Install(ctx, version, actor)
	if err == nil {
		installed.Compatibility = m.assess(installed.Meta, installed.Capabilities)
	}
	return installed, err
}

func (m *Manager) Activate(ctx context.Context, id int64, enabled bool) error {
	if m.repo == nil {
		return ErrRegistryUnavailable
	}
	version, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if enabled {
		compatibility := m.assess(version.Meta, version.Capabilities)
		if !compatibility.Runnable {
			return fmt.Errorf("插件无法启用: %s", strings.Join(compatibility.Blockers, "; "))
		}
		for _, capability := range version.Capabilities.RequiredCapabilities {
			// These are documented compatibility no-ops in the pinned API v1.
			if !runner.SupportsCapability(capability) {
				return fmt.Errorf("宿主尚未实现该插件要求的能力: %s", capability)
			}
		}
		if m.runtime == nil {
			return errors.New("plugin runtime unavailable")
		}
		if err = m.runtime.With(ctx, version.Source, func(*runner.Client) error { return nil }); err != nil {
			return err
		}
	}
	return m.repo.Activate(ctx, id, enabled)
}
