package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/builtin"
	"github.com/Wei-Shaw/sub2api/internal/newapi/jsplugin"
	"github.com/Wei-Shaw/sub2api/internal/newapi/runner"
)

const NewAPIPluginRevisionKey = "newapi_plugin_version_id"
const NewAPIPluginConfigKey = "newapi_plugin_config"
const newAPIPluginConfigEncryptedKey = "newapi_plugin_config_encrypted"
const newAPIPluginVideoModelsKey = "newapi_plugin_video_models"

// Plugin-bound accounts must never fall through to a native wire protocol.
type newAPIPluginRoutingKey struct{}

func WithNewAPIPluginRouting(ctx context.Context) context.Context {
	return context.WithValue(ctx, newAPIPluginRoutingKey{}, true)
}

func (a *Account) NewAPIPluginVersionID() int64 {
	if a == nil || a.Credentials == nil {
		return 0
	}
	value, _ := json.Marshal(a.Credentials[NewAPIPluginRevisionKey])
	id, _ := strconv.ParseInt(string(value), 10, 64)
	return id
}

func (a *Account) HasNewAPIPlugin() bool {
	return a != nil && a.Credentials != nil && a.Credentials[NewAPIPluginRevisionKey] != nil
}

func pluginVideoModels(meta jsplugin.Meta) []string {
	for _, protocol := range meta.Protocols {
		if protocol.Name == "openai_video" {
			if len(protocol.Models) > 0 {
				return slices.Clone(protocol.Models)
			}
			return slices.Clone(meta.Models)
		}
	}
	return nil
}

func (a *Account) SupportsNewAPIVideoModel(model string) bool {
	if !a.HasNewAPIPlugin() || !a.isModelMappingSupported(model) {
		return false
	}
	var models []string
	raw, _ := json.Marshal(a.Credentials[newAPIPluginVideoModelsKey])
	if json.Unmarshal(raw, &models) != nil {
		return false
	}
	mapped := a.GetMappedModel(model)
	return slices.ContainsFunc(models, func(candidate string) bool {
		return pluginVideoModelMatches(candidate, mapped) || pluginVideoModelMatches(candidate, model)
	})
}

// A trailing wildcard declares a model family; only explicit account mappings
// are advertised for that family, never the wildcard itself.
func pluginVideoModelMatches(pattern, model string) bool {
	if strings.Contains(model, "*") || model == "" {
		return false
	}
	if strings.HasSuffix(pattern, "*") && strings.Count(pattern, "*") == 1 && len(pattern) > 1 {
		return strings.HasPrefix(strings.ToLower(model), strings.ToLower(strings.TrimSuffix(pattern, "*")))
	}
	return strings.EqualFold(pattern, model)
}

// NewAPIVideoModels projects the pinned manifest through the same explicit
// aliases/wildcards used by scheduling. It never probes a legacy /models path.
func (a *Account) NewAPIVideoModels() []string {
	var declared []string
	raw, _ := json.Marshal(a.Credentials[newAPIPluginVideoModelsKey])
	_ = json.Unmarshal(raw, &declared)
	candidates := slices.Clone(declared)
	for name := range a.GetModelMapping() {
		if !strings.Contains(name, "*") {
			candidates = append(candidates, name)
		}
	}
	result := make([]string, 0, len(candidates))
	for _, name := range candidates {
		if a.SupportsNewAPIVideoModel(name) && !slices.Contains(result, name) {
			result = append(result, name)
		}
	}
	slices.Sort(result)
	return result
}

type NewAPIPluginVersion struct {
	ID           int64             `json:"id"`
	Key          string            `json:"key"`
	Version      string            `json:"version"`
	SHA256       string            `json:"sha256"`
	Origin       string            `json:"origin"`
	Source       string            `json:"-"`
	Meta         jsplugin.Meta     `json:"meta"`
	Capabilities runner.Descriptor `json:"capabilities"`
	Enabled      bool              `json:"enabled"`
	Active       bool              `json:"active"`
	CreatedAt    time.Time         `json:"created_at"`
}

type NewAPIPluginRepository interface {
	List(context.Context) ([]NewAPIPluginVersion, error)
	Versions(context.Context, string) ([]NewAPIPluginVersion, error)
	Get(context.Context, int64) (*NewAPIPluginVersion, error)
	Install(context.Context, *NewAPIPluginVersion, *int64) (*NewAPIPluginVersion, error)
	Activate(context.Context, int64, bool) error
}

// NewAPIPluginService keeps JS lifecycle separate from OAuth Transport v1.
// Both are managed in the same admin page; account auth types stay unchanged.
type NewAPIPluginService struct {
	repo      NewAPIPluginRepository
	encryptor SecretEncryptor
	pool      *runner.Pool
	gateway   *OpenAIGatewayService
}

func NewNewAPIPluginService(repo NewAPIPluginRepository, encryptor SecretEncryptor) (*NewAPIPluginService, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &NewAPIPluginService{repo: repo, encryptor: encryptor, pool: runner.NewPool(executable, []string{"plugin", "worker"})}, nil
}

func (s *NewAPIPluginService) Stop() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func normalizeNewAPIPluginAccount(ctx context.Context, s *NewAPIPluginService, account *Account, incoming map[string]any) error {
	if incoming == nil {
		return nil
	}
	if account.Credentials == nil {
		return nil
	}
	if s == nil {
		if account.Credentials[NewAPIPluginRevisionKey] != nil || incoming[NewAPIPluginConfigKey] != nil || incoming[newAPIPluginConfigEncryptedKey] != nil {
			return errors.New("插件服务不可用")
		}
		return nil
	}
	return s.NormalizeAccountBinding(ctx, account, incoming)
}
func (s *NewAPIPluginService) List(ctx context.Context) ([]NewAPIPluginVersion, error) {
	return s.repo.List(ctx)
}
func (s *NewAPIPluginService) Versions(ctx context.Context, key string) ([]NewAPIPluginVersion, error) {
	if !jsplugin.ValidPluginKey(key) {
		return nil, errors.New("invalid plugin key")
	}
	return s.repo.Versions(ctx, key)
}

func (s *NewAPIPluginService) Install(ctx context.Context, source, builtinKey string, actor *int64) (*NewAPIPluginVersion, error) {
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
		origin = "QuantumNous/new-api@48f9df96ea7c68eae04a0f3687869f232f48929f:" + builtinKey
	}
	version := &NewAPIPluginVersion{Source: source, Origin: origin}
	err := s.pool.With(ctx, source, func(client *runner.Client) error {
		version.Meta = client.Meta
		version.Capabilities = client.Descriptor
		version.Key, version.Version = client.Meta.Key, client.Meta.Version
		return nil
	})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(source))
	version.SHA256 = hex.EncodeToString(hash[:])
	return s.repo.Install(ctx, version, actor)
}

func (s *NewAPIPluginService) Activate(ctx context.Context, id int64, enabled bool) error {
	version, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if enabled {
		for _, capability := range version.Capabilities.RequiredCapabilities {
			// These are documented compatibility no-ops in the pinned API v1.
			if !runner.SupportsCapability(capability) {
				return fmt.Errorf("宿主尚未实现该插件要求的能力: %s", capability)
			}
		}
		if err = s.pool.With(ctx, version.Source, func(*runner.Client) error { return nil }); err != nil {
			return err
		}
	}
	return s.repo.Activate(ctx, id, enabled)
}

// NormalizeAccountBinding validates and encrypts before the account write. The
// version and config live in the existing credentials JSON, so there is no
// half-created account / separate binding transaction to repair.
func (s *NewAPIPluginService) NormalizeAccountBinding(ctx context.Context, account *Account, incoming map[string]any) error {
	value, exists := account.Credentials[NewAPIPluginRevisionKey]
	if !exists || value == nil || value == "" {
		delete(account.Credentials, NewAPIPluginConfigKey)
		delete(account.Credentials, newAPIPluginConfigEncryptedKey)
		delete(account.Credentials, newAPIPluginVideoModelsKey)
		return nil
	}
	encoded, _ := json.Marshal(value)
	var id int64
	if json.Unmarshal(encoded, &id) != nil || id <= 0 {
		return errors.New("插件版本 ID 无效")
	}
	if account.Type != AccountTypeAPIKey || account.IsShadow() {
		return errors.New("插件接入请选择普通 API Key 账号")
	}
	version, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if !version.Enabled {
		return errors.New("请先在插件管理启用该插件")
	}
	if !version.Capabilities.Task {
		return errors.New("账号的视频接入请选择 Task 协议插件")
	}
	if kind := account.GetCredential("newapi_upstream_kind"); kind != "" && kind != "vendor" && kind != "new_api" {
		return errors.New("上游类型需要选择原厂或 New API")
	} else if kind == "new_api" && !slices.Contains(version.Meta.Upstreams, "new_api") {
		return errors.New("该插件没有声明 New API 上游支持")
	}
	models := pluginVideoModels(version.Meta)
	if len(models) == 0 {
		return errors.New("该插件没有声明 OpenAI Videos 模型")
	}
	if configured := account.Credentials[NewAPIVideoPricingKey]; configured != nil {
		raw, err := json.Marshal(configured)
		if err != nil || len(raw) > 64<<10 {
			return errors.New("视频定价配置不能超过 64 KiB")
		}
		var rules map[string]GenerationPriceRule
		if err = json.Unmarshal(raw, &rules); err != nil || len(rules) > 128 {
			return errors.New("视频定价配置无效")
		}
		for model, rule := range rules {
			if model == "" || len(model) > 256 {
				return errors.New("视频定价模型名无效")
			}
			if err = rule.Validate(); err != nil {
				return fmt.Errorf("模型 %s: %w", model, err)
			}
		}
	}
	base, _ := account.Credentials["base_url"].(string)
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("插件账号需要有效的上游 HTTP(S) 地址")
	}
	// Never accept an encrypted blob supplied by an API caller. Preserve only
	// the server's existing value; a fresh config must use the input field.
	if _, supplied := incoming[newAPIPluginConfigEncryptedKey]; supplied {
		return errors.New("插件加密配置仅可由服务端生成")
	}
	config, provided := incoming[NewAPIPluginConfigKey]
	if !provided {
		if previous, _ := account.Credentials[newAPIPluginConfigEncryptedKey].(string); previous != "" {
			plain, err := s.encryptor.Decrypt(previous)
			if err != nil {
				return errors.New("插件配置无法解密")
			}
			if err = json.Unmarshal([]byte(plain), &config); err != nil {
				return errors.New("插件配置无效")
			}
		}
	}
	if config == nil {
		config = map[string]any{}
	}
	raw, err := json.Marshal(config)
	if err != nil || len(raw) > 64<<10 {
		return errors.New("插件配置必须是 64 KiB 以内的 JSON 对象")
	}
	var validated json.RawMessage
	err = s.pool.With(ctx, version.Source, func(client *runner.Client) error {
		var e error
		validated, e = client.ValidateConfig(ctx, raw, false)
		return e
	})
	if err != nil {
		return err
	}
	sealed, err := s.encryptor.Encrypt(string(validated))
	if err != nil {
		return err
	}
	account.Credentials[NewAPIPluginRevisionKey] = id
	account.Credentials[newAPIPluginVideoModelsKey] = models
	account.Credentials[newAPIPluginConfigEncryptedKey] = sealed
	delete(account.Credentials, NewAPIPluginConfigKey)
	return nil
}
