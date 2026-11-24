package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	engineplugin "github.com/Wei-Shaw/sub2api/newapi-engine/plugin"
)

var ErrPluginVersionNotFound = infraerrors.New(http.StatusNotFound, "PLUGIN_VERSION_NOT_FOUND", "插件版本不存在")
var ErrPluginBuiltinProtected = infraerrors.New(http.StatusBadRequest, "PLUGIN_BUILTIN_PROTECTED", "系统内置版本不能删除，仅支持删除自定义上传或市场安装的版本")

func PluginVersionInUseError(message string) error {
	return infraerrors.New(http.StatusConflict, "PLUGIN_VERSION_IN_USE", message)
}

type PluginDeleteResult struct {
	DeletedID         int64  `json:"deleted_id"`
	Key               string `json:"key"`
	Version           string `json:"version"`
	FallbackVersionID *int64 `json:"fallback_version_id,omitempty"`
	RemovedPlugin     bool   `json:"removed_plugin"`
}

type PluginVersionDeleteRepository interface {
	DeleteVersion(ctx context.Context, id int64, isRunnable func(NewAPIPluginVersion) bool) (*PluginDeleteResult, error)
}

type PluginAccountPricingSummary struct {
	AccountID    int64                          `json:"account_id"`
	AccountName  string                         `json:"account_name"`
	Status       string                         `json:"status"`
	Schedulable  bool                           `json:"schedulable"`
	VersionID    int64                          `json:"version_id"`
	Version      string                         `json:"version"`
	PricingRules map[string]GenerationPriceRule `json:"pricing_rules"`
}

type PluginPricingSummaryRepository interface {
	ListPluginAccountPricing(ctx context.Context, pluginKey string) ([]PluginAccountPricingSummary, error)
}

func (s *NewAPIPluginService) DeleteVersion(ctx context.Context, id int64) (*PluginDeleteResult, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("new API plugin service is disabled")
	}
	if id <= 0 {
		return nil, pluginInputError("插件版本 ID 无效")
	}
	version, err := s.repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPluginVersionNotFound
		}
		return nil, pluginControlError(err)
	}
	if version == nil || version.ID != id {
		return nil, ErrPluginVersionNotFound
	}
	if strings.HasPrefix(version.Origin, "builtin:") {
		return nil, ErrPluginBuiltinProtected
	}
	factories, err := PluginFactories()
	if err == nil {
		for _, factory := range factories {
			if factory.Key == version.Key && factory.SHA256 == version.SHA256 {
				return nil, ErrPluginBuiltinProtected
			}
		}
	}
	deleter, ok := s.repo.(PluginVersionDeleteRepository)
	if !ok {
		return nil, errors.New("当前插件仓库暂不支持删除自定义版本")
	}
	result, err := deleter.DeleteVersion(ctx, id, func(candidate NewAPIPluginVersion) bool {
		item := candidate
		item.Compatibility = AssessPluginHost(item.Meta, item.Capabilities)
		applySunoTaskCompatibility(&item)
		return item.Compatibility.Runnable
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPluginVersionNotFound
		}
		return nil, pluginControlError(err)
	}
	return result, nil
}

func (s *NewAPIPluginService) DryRunInstalledVersion(ctx context.Context, id int64, hook string, args []json.RawMessage) (*PluginPlaygroundResult, error) {
	if s == nil || s.repo == nil || s.pool == nil {
		return nil, errors.New("new API plugin service is disabled")
	}
	if id <= 0 {
		return nil, pluginInputError("插件版本 ID 无效")
	}
	ctx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, newAPIProviderMemory); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	version, err := s.repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPluginVersionNotFound
		}
		return nil, pluginControlError(err)
	}
	if version == nil || version.ID != id {
		return nil, ErrPluginVersionNotFound
	}
	checked, err := engineplugin.CheckedSource(engineplugin.Source{
		ID:      version.ID,
		Key:     version.Key,
		Version: version.Version,
		SHA256:  version.SHA256,
		Source:  version.Source,
	})
	if err != nil {
		return nil, err
	}
	return s.DryRunPlugin(ctx, PluginPlaygroundInput{
		Source: checked.Source,
		Hook:   hook,
		Args:   args,
	})
}

func (s *NewAPIPluginService) ListAccountPricing(ctx context.Context, pluginKey string) ([]PluginAccountPricingSummary, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("new API plugin service is disabled")
	}
	if !jsplugin.ValidPluginKey(pluginKey) {
		return nil, pluginInputError("插件 key 无效")
	}
	reader, ok := s.repo.(PluginPricingSummaryRepository)
	if !ok {
		return []PluginAccountPricingSummary{}, nil
	}
	items, err := reader.ListPluginAccountPricing(ctx, pluginKey)
	if err != nil {
		return nil, err
	}
	return items, nil
}
