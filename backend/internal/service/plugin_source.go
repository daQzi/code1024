package service

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	engineplugin "github.com/Wei-Shaw/sub2api/newapi-engine/plugin"
)

type PluginSource = engineplugin.Source

var errPluginSourceMissing = infraerrors.NotFound("PLUGIN_SOURCE_NOT_FOUND", "插件源码版本不存在")

// Source is an immutable registry artifact, never a script evaluation. Runtime
// state, account config and credentials cannot be included in this DTO.
func (s *NewAPIPluginService) PluginSource(ctx context.Context, id int64) (*PluginSource, error) {
	if id <= 0 {
		return nil, pluginInputError("插件版本 ID 无效")
	}
	v, err := s.repo.Get(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errPluginSourceMissing
	}
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, errPluginSourceMissing
	}
	return checkedPluginSource(PluginSource{ID: v.ID, Key: v.Key, Version: v.Version, SHA256: v.SHA256, Source: v.Source})
}

func (s *NewAPIPluginService) FactorySource(key string) (*PluginSource, error) {
	if !jsplugin.ValidPluginKey(key) {
		return nil, pluginInputError("插件 key 无效")
	}
	items, err := PluginFactories()
	if err != nil {
		return nil, err
	}
	index := slices.IndexFunc(items, func(item PluginFactory) bool { return item.Key == key })
	if index < 0 {
		return nil, errPluginSourceMissing
	}
	source, err := builtin.Source(key)
	if err != nil {
		return nil, errPluginSourceMissing
	}
	item := items[index]
	return checkedPluginSource(PluginSource{Key: item.Key, Version: item.Version, SHA256: item.SHA256, Source: source})
}

func checkedPluginSource(source PluginSource) (*PluginSource, error) {
	return engineplugin.CheckedSource(source)
}
