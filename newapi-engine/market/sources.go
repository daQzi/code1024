package market

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/plugin"
)

var ErrSettingNotFound = errors.New("marketplace setting not found")

type Settings interface {
	GetValue(context.Context, string) (string, error)
	Set(context.Context, string, string) error
}

type Installer interface {
	Install(context.Context, string, string, *int64, ...plugin.InstallOptions) (*plugin.Version, error)
}

type Service struct {
	settings  Settings
	installer Installer
}

func NewService(settings Settings, installer Installer) *Service {
	return &Service{settings: settings, installer: installer}
}

const SettingKey = "plugin_marketplace_sources"

const (
	Official  = "https://www.newapi.ai/api/v1/plugins/index.json"
	GitHub    = "https://raw.githubusercontent.com/QuantumNous/new-api-plugins/main/index.json"
	Community = "https://www.newapi.ai/api/v1/community/index.json"
)

type Source struct {
	Name     string `json:"name"`
	IndexURL string `json:"index_url"`
}

// The browser downloads index/source bytes. The server never fetches arbitrary
// marketplace URLs, and a source label is derived from its configured address.
func Kind(address string) string {
	switch address {
	case Official, GitHub:
		return "official"
	case Community:
		return "community"
	default:
		return "third_party"
	}
}

func NormalizeSources(items []Source) ([]Source, error) {
	if len(items) > 16 {
		return nil, errors.New("插件市场最多配置 16 个来源")
	}
	result := make([]Source, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		item.Name, item.IndexURL = strings.TrimSpace(item.Name), strings.TrimSpace(item.IndexURL)
		u, err := url.Parse(item.IndexURL)
		if err != nil || len(item.IndexURL) > 2048 || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || strings.ContainsAny(item.IndexURL, "\r\n\t\\") {
			return nil, errors.New("市场地址必须是无凭据、查询参数和片段的 HTTP(S) 绝对地址")
		}
		if item.Name == "" || len(item.Name) > 120 {
			return nil, errors.New("市场名称不能为空且最多 120 字节")
		}
		if seen[item.IndexURL] {
			return nil, errors.New("市场地址不能重复")
		}
		seen[item.IndexURL] = true
		result = append(result, item)
	}
	return result, nil
}

func (s *Service) Sources(ctx context.Context) ([]Source, error) {
	if s.settings == nil {
		return nil, errors.New("市场配置服务不可用")
	}
	raw, err := s.settings.GetValue(ctx, SettingKey)
	if errors.Is(err, ErrSettingNotFound) {
		return []Source{{"New API", Official}, {"New API GitHub", GitHub}, {"New API Community", Community}}, nil
	}
	if err != nil {
		return nil, err
	}
	var items []Source
	if err = json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, errors.New("市场来源配置无效")
	}
	return NormalizeSources(items)
}

func (s *Service) SaveSources(ctx context.Context, items []Source) ([]Source, error) {
	if s.settings == nil {
		return nil, errors.New("市场配置服务不可用")
	}
	items, err := NormalizeSources(items)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	if err = s.settings.Set(ctx, SettingKey, string(raw)); err != nil {
		return nil, err
	}
	return items, nil
}

type Pin struct {
	IndexURL string `json:"index_url"`
	Key      string `json:"key"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
}

func (s *Service) Install(ctx context.Context, source string, pin Pin, actor *int64, allowOverride bool) (*plugin.Version, error) {
	digest, err := hex.DecodeString(pin.SHA256)
	if err != nil || len(digest) != 32 || !jsplugin.ValidPluginKey(pin.Key) || strings.TrimSpace(pin.Version) != pin.Version || pin.Version == "" || len(pin.Version) > 128 {
		return nil, errors.New("市场安装必须提供有效的 key、版本和 SHA256")
	}
	items, err := s.Sources(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.IndexURL == pin.IndexURL {
			if s.installer == nil {
				return nil, errors.New("plugin installer unavailable")
			}
			return s.installer.Install(ctx, source, "", actor, plugin.InstallOptions{AllowBuiltinOverride: allowOverride, ExpectedKey: pin.Key, ExpectedVersion: pin.Version, ExpectedSHA256: pin.SHA256, Origin: "market:" + Kind(pin.IndexURL) + ":" + pin.IndexURL})
		}
	}
	return nil, errors.New("插件市场来源未配置或已移除，请刷新来源后重试")
}
