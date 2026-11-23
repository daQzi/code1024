package service

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/newapi-engine/market"
)

const pluginMarketplaceSettingKey = market.SettingKey
const PluginMarketplaceOfficial = market.Official
const PluginMarketplaceGitHub = market.GitHub
const PluginMarketplaceCommunity = market.Community

type PluginMarketplaceSource = market.Source
type PluginMarketplacePin = market.Pin

func PluginMarketplaceKind(address string) string { return market.Kind(address) }
func normalizeMarketplaceSources(items []PluginMarketplaceSource) ([]PluginMarketplaceSource, error) {
	return market.NormalizeSources(items)
}

type marketplaceSettingsAdapter struct{ SettingRepository }

func (a marketplaceSettingsAdapter) GetValue(ctx context.Context, key string) (string, error) {
	value, err := a.SettingRepository.GetValue(ctx, key)
	if errors.Is(err, ErrSettingNotFound) {
		return "", market.ErrSettingNotFound
	}
	return value, err
}

func (s *NewAPIPluginService) marketplace() *market.Service {
	var settings market.Settings
	if s.marketSettings != nil {
		settings = marketplaceSettingsAdapter{s.marketSettings}
	}
	return market.NewService(settings, s.pluginManager())
}

func (s *NewAPIPluginService) MarketplaceSources(ctx context.Context) ([]PluginMarketplaceSource, error) {
	return s.marketplace().Sources(ctx)
}
func (s *NewAPIPluginService) SaveMarketplaceSources(ctx context.Context, items []PluginMarketplaceSource) ([]PluginMarketplaceSource, error) {
	return s.marketplace().SaveSources(ctx, items)
}
func (s *NewAPIPluginService) InstallMarketplace(ctx context.Context, source string, pin PluginMarketplacePin, actor *int64, allowOverride bool) (*NewAPIPluginVersion, error) {
	return s.marketplace().Install(ctx, source, pin, actor, allowOverride)
}
