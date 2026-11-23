package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	engineplugin "github.com/Wei-Shaw/sub2api/newapi-engine/plugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

const NewAPIPluginRevisionKey = "newapi_plugin_version_id"
const NewAPIPluginConfigKey = "newapi_plugin_config"
const newAPIPluginConfigEncryptedKey = "newapi_plugin_config_encrypted"
const newAPIPluginVideoModelsKey = "newapi_plugin_video_models"
const newAPIPluginManagedCredentialsKey = "newapi_plugin_managed_credentials"

var ErrPluginVersionConflict = engineplugin.ErrVersionConflict

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
	return a.supportsDeclaredTaskModel(newAPIPluginVideoModelsKey, model)
}

func (a *Account) supportsDeclaredTaskModel(binding, model string) bool {
	if strings.Contains(model, "*") || strings.TrimSpace(model) == "" || !a.HasNewAPIPlugin() || !a.isModelMappingSupported(model) {
		return false
	}
	var models []string
	raw, _ := json.Marshal(a.Credentials[binding])
	if json.Unmarshal(raw, &models) != nil {
		return false
	}
	mapped := a.GetMappedModel(model)
	return slices.ContainsFunc(models, func(candidate string) bool {
		if candidate == "*" {
			// Generic video drivers only admit explicitly named account models.
			// A catch-all account mapping must not advertise every text model as video.
			_, explicit := a.GetModelMapping()[model]
			return explicit && pluginVideoModelMatches(candidate, mapped)
		}
		return pluginVideoModelMatches(candidate, mapped) || pluginVideoModelMatches(candidate, model)
	})
}

// A trailing wildcard declares a model family. A bare wildcard is a generic
// driver claim; account admission additionally requires an explicit model map.
func pluginVideoModelMatches(pattern, model string) bool {
	if strings.Contains(model, "*") || model == "" {
		return false
	}
	if pattern == "*" {
		return true
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

type NewAPIPluginVersion = engineplugin.Version
type NewAPIPluginRepository = engineplugin.Repository
type PluginInstallOptions = engineplugin.InstallOptions

// NewAPIPluginService keeps JS lifecycle separate from OAuth Transport v1.
// Both are managed in the same admin page; account auth types stay unchanged.
type NewAPIPluginService struct {
	repo                  NewAPIPluginRepository
	encryptor             SecretEncryptor
	pool                  *runner.Pool
	gateway               *OpenAIGatewayService
	requestPlugins        requestPluginCache
	providerTokens        newAPIProviderTokenCache
	credentialMaintenance *pluginCredentialMaintenance
	credentialNotifier    *pluginCredentialNotifier
	proxyRepo             ProxyRepository
	files                 GenerationFileRepository
	preparations          GenerationPreparationRepository
	marketSettings        SettingRepository
	modelModifiers        modelModifierSettingsCache
	providerToolPrices    providerToolPriceCache
	taskReports           *taskReportExecutor
	replyAuditFailures    atomic.Uint64
}

func NewNewAPIPluginService(repo NewAPIPluginRepository, encryptor SecretEncryptor) (*NewAPIPluginService, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	s := &NewAPIPluginService{repo: repo, encryptor: encryptor, pool: runner.NewPool(executable, []string{"plugin", "worker"})}
	s.taskReports = newTaskReportExecutor(s)
	return s, nil
}

func (s *NewAPIPluginService) Stop() {
	if s != nil && s.pool != nil {
		s.credentialMaintenance.close()
		if s.taskReports != nil {
			s.taskReports.close()
		}
		s.pool.Close()
	}
}

func normalizeNewAPIPluginAccount(ctx context.Context, s *NewAPIPluginService, account *Account, incoming map[string]any, previous ...*Account) error {
	if incoming == nil {
		if account == nil || !account.HasNewAPIPlugin() || len(previous) == 0 || previous[0] == nil || previous[0].Type == account.Type && reflect.DeepEqual(previous[0].ProxyID, account.ProxyID) {
			return nil
		}
		// A proxy/type-only edit still changes the connection identity.
		incoming = map[string]any{}
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
	return s.NormalizeAccountBinding(ctx, account, incoming, previous...)
}

// NormalizeAccountBindingIfPresent validates New API plugin account bindings
// when present and permits ordinary native accounts even when the New API
// module is disabled (s == nil).
func (s *NewAPIPluginService) NormalizeAccountBindingIfPresent(ctx context.Context, account *Account, incoming map[string]any, previous ...*Account) error {
	return normalizeNewAPIPluginAccount(ctx, s, account, incoming, previous...)
}
func (s *NewAPIPluginService) pluginManager() *engineplugin.Manager {
	var runtime engineplugin.Runtime
	if s.pool != nil {
		runtime = s.pool
	}
	return engineplugin.NewManager(s.repo, runtime, AssessPluginHost)
}

func (s *NewAPIPluginService) List(ctx context.Context) ([]NewAPIPluginVersion, error) {
	if s == nil {
		return nil, errors.New("new API plugin service is disabled")
	}
	items, err := s.pluginManager().List(ctx)
	return withSunoTaskCompatibility(items), err
}
func (s *NewAPIPluginService) Versions(ctx context.Context, key string) ([]NewAPIPluginVersion, error) {
	if s == nil {
		return nil, errors.New("new API plugin service is disabled")
	}
	items, err := s.pluginManager().Versions(ctx, key)
	return withSunoTaskCompatibility(items), err
}

func withSunoTaskCompatibility(items []NewAPIPluginVersion) []NewAPIPluginVersion {
	for i := range items {
		applySunoTaskCompatibility(&items[i])
	}
	return items
}

func applySunoTaskCompatibility(item *NewAPIPluginVersion) {
	if !sunoTaskMetadataVersion(*item) {
		return
	}
	if !slices.Contains(item.Compatibility.Kinds, "task") {
		item.Compatibility.Kinds = append(item.Compatibility.Kinds, "task")
	}
	item.Compatibility.Blockers = slices.DeleteFunc(item.Compatibility.Blockers, func(blocker string) bool { return blocker == "no supported host execution path" })
	item.Compatibility.Runnable = len(item.Compatibility.Blockers) == 0
}
func (s *NewAPIPluginService) Install(ctx context.Context, source, builtinKey string, actor *int64, options ...PluginInstallOptions) (*NewAPIPluginVersion, error) {
	if s == nil {
		return nil, errors.New("new API plugin service is disabled")
	}
	return s.pluginManager().Install(ctx, source, builtinKey, actor, options...)
}
func (s *NewAPIPluginService) Activate(ctx context.Context, id int64, enabled bool) error {
	if s == nil {
		return errors.New("new API plugin service is disabled")
	}
	return s.pluginManager().Activate(ctx, id, enabled)
}

// NormalizeAccountBinding validates and encrypts before the account write. The
// version and config live in the existing credentials JSON, so there is no
// half-created account / separate binding transaction to repair.
func (s *NewAPIPluginService) NormalizeAccountBinding(ctx context.Context, account *Account, incoming map[string]any, previous ...*Account) error {
	value, exists := account.Credentials[NewAPIPluginRevisionKey]
	if !exists || value == nil || value == "" {
		delete(account.Credentials, NewAPIPluginConfigKey)
		delete(account.Credentials, newAPIPluginConfigEncryptedKey)
		delete(account.Credentials, newAPIPluginKeyKey)
		delete(account.Credentials, newAPIPluginVideoModelsKey)
		delete(account.Credentials, newAPIPluginSunoModelsKey)
		delete(account.Credentials, newAPIPluginGenericTaskModelsKey)
		delete(account.Credentials, newAPIPluginResponsesTaskKey)
		delete(account.Credentials, newAPIPluginImageTaskKey)
		delete(account.Credentials, newAPIPluginNativeRoutesKey)
		delete(account.Credentials, newAPIPluginNativeVideosKey)
		delete(account.Credentials, newAPIPluginProviderKey)
		delete(account.Credentials, newAPIPluginManagedCredentialsKey)
		delete(account.Credentials, "newapi_plugin_auto_disable")
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
	if value, exists := account.Credentials["newapi_plugin_auto_disable"]; exists {
		if _, ok := value.(bool); !ok {
			return errors.New("插件自动暂停开关需要使用布尔值")
		}
	}
	provider := pluginProviderBinding(version.Meta, version.Capabilities)
	models := pluginVideoModels(version.Meta)
	if !version.Capabilities.Task {
		models = nil
	}
	sunoModels := taskProtocolModels(version, GenerationProtocolSunoTask)
	genericTaskModels := pluginGenericTaskModels(version.Meta, version.Capabilities)
	responsesTask := pluginResponsesTaskBinding(version.Meta, version.Capabilities)
	imageTaskModels := pluginImageTaskModels(version.Meta, version.Capabilities)
	nativeRoutes := pluginNativeRoutesBinding(version.Meta, version.Capabilities)
	hasTaskPath := len(models) > 0 || len(sunoModels) > 0 || len(genericTaskModels) > 0 || len(responsesTask.Models) > 0 || len(imageTaskModels) > 0 || len(nativeRoutes.Routes) > 0
	if len(provider.Routes) == 0 && !hasTaskPath {
		return errors.New("插件没有当前宿主支持的 Task 或 Provider 接入路径")
	}
	if slices.Contains(models, "*") {
		projected := *account
		projected.Credentials = maps.Clone(account.Credentials)
		projected.Credentials[newAPIPluginVideoModelsKey] = models
		if len(projected.NewAPIVideoModels()) == 0 {
			return errors.New("通用视频插件需要至少一个具体模型；请在账号模型映射中配置公开模型及上游模型")
		}
	}
	if len(provider.Routes) > 0 && account.Platform != PlatformOpenAI {
		return errors.New("provider 插件请选择 OpenAI 平台 API Key 账号")
	}
	if len(provider.Routes) > 0 {
		projected := *account
		projected.Credentials = maps.Clone(account.Credentials)
		projected.Credentials[newAPIPluginProviderKey] = provider
		if len(projected.NewAPIProviderModels()) == 0 {
			return errors.New("provider 需要至少一个具体模型；请在账号模型映射中配置公开模型及上游模型")
		}
	}
	if _, credential := jsplugin.ParseCredential(account.GetCredential("api_key")); credential {
		return errors.New("不要把插件凭据 JSON 填进 API Key；请先保存账号，再从插件登录入口授权")
	}
	if kind := account.GetCredential("newapi_upstream_kind"); kind != "" && kind != "vendor" && kind != "new_api" {
		return errors.New("上游类型需要选择原厂或 New API")
	} else if kind == "new_api" && len(models) > 0 && !slices.Contains(version.Meta.Upstreams, "new_api") {
		return errors.New("该插件没有声明 New API 上游支持")
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
		allTaskModels := mergeDistinctModels(models, sunoModels, genericTaskModels, responsesTask.Models, imageTaskModels)
		if err = validateAccountTaskExpressions(account, version, allTaskModels, rules, previous); err != nil {
			return err
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
	validationCtx, cancel := context.WithTimeout(ctx, newAPIProviderCredentialTimeout)
	defer cancel()
	err = s.pool.With(validationCtx, version.Source, func(client *runner.Client) error {
		var e error
		validated, e = client.ValidateConfig(validationCtx, raw, false)
		if e != nil {
			return e
		}
		if (len(provider.Routes) > 0 || version.Capabilities.Task) && (!slices.Contains(version.Capabilities.Hooks, "login") || account.GetCredential("api_key") != "") && !s.sameProviderCredential(account, base, validated, previous) {
			return s.checkProviderKey(validationCtx, client, version, account, base, validated)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Preserve the existing cipher when configuration is unchanged. Random
	// encryption nonces must not invalidate a login after a metadata-only edit.
	sealed := account.GetCredential(newAPIPluginConfigEncryptedKey)
	var storedConfig, nextConfig any
	plain, decryptErr := s.encryptor.Decrypt(sealed)
	unchanged := sealed != "" && decryptErr == nil && json.Unmarshal([]byte(plain), &storedConfig) == nil && json.Unmarshal(validated, &nextConfig) == nil && reflect.DeepEqual(storedConfig, nextConfig)
	if !unchanged {
		sealed, err = s.encryptor.Encrypt(string(validated))
		if err != nil {
			return err
		}
	}
	account.Credentials[NewAPIPluginRevisionKey] = id
	account.Credentials[newAPIPluginKeyKey] = version.Meta.Key
	account.Credentials[newAPIPluginManagedCredentialsKey] = account.GetCredential("newapi_upstream_kind") != "new_api" && (slices.Contains(version.Capabilities.Hooks, "login") || slices.Contains(version.Capabilities.Hooks, "scheduled"))
	account.Credentials[newAPIPluginVideoModelsKey] = models
	delete(account.Credentials, newAPIPluginSunoModelsKey)
	if len(sunoModels) > 0 {
		account.Credentials[newAPIPluginSunoModelsKey] = sunoModels
	}
	delete(account.Credentials, newAPIPluginGenericTaskModelsKey)
	if len(genericTaskModels) > 0 {
		account.Credentials[newAPIPluginGenericTaskModelsKey] = genericTaskModels
	}
	delete(account.Credentials, newAPIPluginResponsesTaskKey)
	if len(responsesTask.Models) > 0 {
		account.Credentials[newAPIPluginResponsesTaskKey] = responsesTask
	}
	delete(account.Credentials, newAPIPluginImageTaskKey)
	if len(imageTaskModels) > 0 {
		account.Credentials[newAPIPluginImageTaskKey] = imageTaskModels
	}
	delete(account.Credentials, newAPIPluginNativeRoutesKey)
	if len(nativeRoutes.Routes) > 0 {
		account.Credentials[newAPIPluginNativeRoutesKey] = nativeRoutes
	}
	if len(models) > 0 {
		account.Credentials[newAPIPluginNativeVideosKey] = pluginNativeVideoBinding(version.Meta)
	} else {
		delete(account.Credentials, newAPIPluginNativeVideosKey)
	}
	if len(provider.Routes) > 0 {
		account.Credentials[newAPIPluginProviderKey] = provider
	} else {
		delete(account.Credentials, newAPIPluginProviderKey)
	}
	account.Credentials[newAPIPluginConfigEncryptedKey] = sealed
	delete(account.Credentials, NewAPIPluginConfigKey)
	return nil
}
