package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/mediahost"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/tidwall/gjson"
)

const MaxRequestPluginBody = 1 << 20
const requestPluginCacheTTL = 2 * time.Second

var ErrRequestPluginConflict = infraerrors.Conflict("REQUEST_PLUGIN_CONFLICT", "请求处理实例已变化，请刷新后重试")
var ErrRequestPluginMissing = infraerrors.NotFound("REQUEST_PLUGIN_NOT_FOUND", "请求处理实例不存在")

// Instance activation is independent from the Task/Provider plugin switch.
// Stages and placement are explicit. Account/channel-scoped ingress waits
// until the attempt is selected instead of running against an unknown channel.
type RequestPluginInstance struct {
	ID                  int64                `json:"id"`
	Name                string               `json:"name"`
	PluginVersionID     int64                `json:"plugin_version_id"`
	PluginKey           string               `json:"plugin_key"`
	PluginVersion       string               `json:"plugin_version"`
	Enabled             bool                 `json:"enabled"`
	NetworkEnabled      bool                 `json:"network_enabled"`
	ReplyNetworkEnabled bool                 `json:"reply_network_enabled"`
	Scope               string               `json:"scope"`
	GroupIDs            []int64              `json:"group_ids"`
	AccountIDs          []int64              `json:"account_ids"`
	Models              []string             `json:"models"`
	Formats             []string             `json:"formats"`
	Position            int                  `json:"position"`
	Stages              []string             `json:"stages"`
	Revision            int64                `json:"revision"`
	UpdatedAt           time.Time            `json:"updated_at"`
	Config              json.RawMessage      `json:"config,omitempty"`
	ConfigEncrypted     string               `json:"-"`
	Version             *NewAPIPluginVersion `json:"-"`
}

// Records contain host-owned facts only, never bodies, headers, config,
// plugin exception strings, findings text or rejection messages.
type RequestPluginRecord struct {
	InstanceID      int64              `json:"instance_id"`
	VersionID       int64              `json:"version_id"`
	Stage           string             `json:"stage"`
	Action          string             `json:"action"`
	ErrorClass      string             `json:"error_class,omitempty"`
	DurationMS      int64              `json:"duration_ms"`
	Effects         int                `json:"effects"`
	Findings        int                `json:"findings"`
	AccountID       int64              `json:"account_id,omitempty"`
	Attempt         int                `json:"attempt,omitempty"`
	Attachments     []mediahost.Record `json:"attachments,omitempty"`
	MoreAttachments int                `json:"more_attachments,omitempty"`
	Media           []mediahost.Change `json:"media,omitempty"`
}

type RequestPluginRun struct {
	ID        int64                 `json:"id"`
	RequestID string                `json:"request_id"`
	UserID    int64                 `json:"user_id"`
	GroupID   int64                 `json:"group_id"`
	Model     string                `json:"model"`
	Format    string                `json:"format"`
	DryRun    bool                  `json:"dry_run"`
	Records   []RequestPluginRecord `json:"records"`
	CreatedAt time.Time             `json:"created_at"`
}

type RequestPluginRepository interface {
	ListRequestPluginInstances(context.Context, bool) ([]RequestPluginInstance, error)
	GetRequestPluginInstance(context.Context, int64) (*RequestPluginInstance, error)
	SaveRequestPluginInstance(context.Context, *RequestPluginInstance, int64) (*RequestPluginInstance, error)
	RecordRequestPluginRun(context.Context, RequestPluginRun) error
	ListRequestPluginRuns(context.Context, string, int, int) ([]RequestPluginRun, int64, error)
}

type requestPluginCache struct {
	mu         sync.Mutex
	expires    time.Time
	items      []RequestPluginInstance // immutable execution snapshots
	revision   uint64
	refreshing *requestPluginRefresh
}

type requestPluginRefresh struct {
	done  chan struct{}
	items []RequestPluginInstance
	err   error
}

func (s *NewAPIPluginService) requestPluginRepo() (RequestPluginRepository, error) {
	repo, ok := s.repo.(RequestPluginRepository)
	if !ok {
		return nil, errors.New("request processing repository unavailable")
	}
	return repo, nil
}

func (s *NewAPIPluginService) RequestPluginInstances(ctx context.Context) ([]RequestPluginInstance, error) {
	repo, err := s.requestPluginRepo()
	if err != nil {
		return nil, err
	}
	return repo.ListRequestPluginInstances(ctx, false)
}

func (s *NewAPIPluginService) RequestPluginConfig(ctx context.Context, id int64) (json.RawMessage, error) {
	repo, err := s.requestPluginRepo()
	if err != nil {
		return nil, err
	}
	item, err := repo.GetRequestPluginInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.decryptRequestPluginConfig(item.ConfigEncrypted)
}

func (s *NewAPIPluginService) decryptRequestPluginConfig(cipher string) (json.RawMessage, error) {
	if s.encryptor == nil {
		return nil, errors.New("plugin config encryption unavailable")
	}
	plain, err := s.encryptor.Decrypt(cipher)
	if err != nil {
		return nil, errors.New("request plugin config cannot be decrypted")
	}
	return json.RawMessage(plain), nil
}

func requestPluginVersionSupported(v *NewAPIPluginVersion) error {
	if v == nil || !v.Capabilities.Transform || !slices.ContainsFunc(v.Capabilities.Hooks, func(h string) bool {
		return slices.Contains([]string{"ingress", "request", "outbound", "egress", "reply", "report"}, h)
	}) {
		return pluginInputError("当前宿主支持 JSON ingress、Task egress/report 及 Chat/Responses HTTP 请求、回复和报告阶段")
	}
	for _, capability := range v.Capabilities.RequiredCapabilities {
		if !runner.SupportsCapability(capability) {
			return pluginInputError("请求处理插件要求未实现的宿主能力")
		}
	}
	return nil
}

func (s *NewAPIPluginService) SaveRequestPluginInstance(ctx context.Context, input RequestPluginInstance, actor int64) (*RequestPluginInstance, error) {
	if actor <= 0 || input.ID < 0 || input.Revision < 0 || input.PluginVersionID <= 0 || strings.TrimSpace(input.Name) == "" || len(input.Name) > 128 || input.Position < 0 || input.Position > 10000 {
		return nil, pluginInputError("请求处理实例参数无效")
	}
	if !slices.Contains([]string{"global", "group", "account"}, input.Scope) || len(input.GroupIDs) > 100 || len(input.Models) > 100 || len(input.AccountIDs) > 100 {
		return nil, pluginInputError("当前支持全局、分组或指定账号的处理实例")
	}
	if err := validateRequestPluginStages(input.Stages, input.Formats, input.Scope); err != nil {
		return nil, err
	}
	if input.NetworkEnabled && !slices.Contains(input.Stages, "ingress") && !slices.Contains(input.Stages, "egress") {
		return nil, pluginInputError("受限联网仅用于 ingress 或 egress；report 沿用其独立联网约定")
	}
	if input.ReplyNetworkEnabled && !slices.Contains(input.Stages, "reply") {
		return nil, pluginInputError("回复联网需单独授权并包含 reply 阶段")
	}
	if input.Scope == "global" && len(input.GroupIDs) > 0 || input.Scope == "group" && len(input.GroupIDs) == 0 {
		return nil, pluginInputError("分组范围无效")
	}
	if input.Scope == "account" {
		if len(input.GroupIDs) != 0 || len(input.AccountIDs) == 0 || s.gateway == nil || s.gateway.accountRepo == nil {
			return nil, pluginInputError("请选择有效的已有账号")
		}
		seen := map[int64]bool{}
		for _, id := range input.AccountIDs {
			if id <= 0 || seen[id] {
				return nil, pluginInputError("账号 ID 必须为唯一正整数")
			}
			seen[id] = true
			account, err := s.gateway.accountRepo.GetByID(ctx, id)
			if err != nil || account == nil {
				return nil, pluginInputError("请选择有效的已有账号")
			}
			nativeOutput := !slices.ContainsFunc(input.Stages, func(stage string) bool { return stage != "reply" && stage != "report" }) && slices.Contains([]string{PlatformAnthropic, PlatformGemini, PlatformAntigravity}, account.Platform) && !slices.ContainsFunc(input.Formats, func(format string) bool { return format != "claude" && format != "gemini" })
			nativeProtocol := account.Platform == PlatformAnthropic && slices.Equal(input.Formats, []string{"claude"}) || slices.Contains([]string{PlatformGemini, PlatformAntigravity}, account.Platform) && !slices.ContainsFunc(input.Formats, func(format string) bool { return format != "claude" && format != "gemini" })
			openAIProtocol := account.IsOpenAICompatible() && !slices.ContainsFunc(input.Formats, func(format string) bool { return format != "openai" && format != "openai_responses" })
			if !openAIProtocol && !nativeOutput && !nativeProtocol {
				return nil, pluginInputError("账号 HTTP 处理支持 OpenAI 网关、原生 Anthropic Messages 及 Gemini/Antigravity 的 Messages 和 Gemini 协议")
			}
		}
	} else if len(input.AccountIDs) != 0 {
		return nil, pluginInputError("仅账号作用域可以指定账号")
	}
	for _, id := range input.GroupIDs {
		if id <= 0 {
			return nil, pluginInputError("分组 ID 无效")
		}
	}
	for _, model := range input.Models {
		if strings.TrimSpace(model) != model || model == "" || len(model) > 256 || strings.Contains(model, "*") {
			return nil, pluginInputError("模型范围使用精确模型名，留空代表全部")
		}
	}
	repo, err := s.requestPluginRepo()
	if err != nil {
		return nil, err
	}
	if input.ID > 0 {
		previous, err := repo.GetRequestPluginInstance(ctx, input.ID)
		if err != nil {
			return nil, err
		}
		if previous.PluginVersionID != input.PluginVersionID {
			return nil, pluginInputError("实例固定插件版本；更换版本请新建实例并先试运行")
		}
		if previous.Revision != input.Revision {
			return nil, ErrRequestPluginConflict
		}
		if input.Config == nil {
			input.Config, err = s.decryptRequestPluginConfig(previous.ConfigEncrypted)
			if err != nil {
				return nil, err
			}
		}
	}
	v, err := s.repo.Get(ctx, input.PluginVersionID)
	if err != nil {
		return nil, err
	}
	if err = requestPluginVersionSupported(v); err != nil {
		return nil, err
	}
	if v.Meta.Scope != nil && len(v.Meta.Scope.ChannelTypes) > 0 && slices.Contains(input.Stages, "ingress") && (len(input.Formats) == 0 || slices.ContainsFunc(input.Formats, func(format string) bool {
		return format != "openai" && format != "openai_responses" && format != "claude" && format != "gemini" && format != "task"
	})) {
		return nil, pluginInputError("带 channelTypes 的入口规则需明确选择已接通晚入口的 Chat/Responses/Claude/Gemini 或 Task 格式")
	}
	for _, stage := range input.Stages {
		if !slices.Contains(v.Capabilities.Hooks, stage) {
			return nil, pluginInputError("所选插件没有导出选择的处理阶段")
		}
	}
	validated, err := s.validateRequestPluginStageConfig(ctx, v, input.Config, slices.Contains(input.Stages, "egress"), slices.Contains(input.Stages, "reply"))
	if err != nil {
		return nil, err
	}
	if s.encryptor == nil {
		return nil, errors.New("plugin config encryption unavailable")
	}
	input.ConfigEncrypted, err = s.encryptor.Encrypt(string(validated))
	if err != nil {
		return nil, err
	}
	input.Config = nil
	if input.GroupIDs == nil {
		input.GroupIDs = []int64{}
	}
	if input.AccountIDs == nil {
		input.AccountIDs = []int64{}
	}
	if input.Models == nil {
		input.Models = []string{}
	}
	if input.Formats == nil {
		input.Formats = []string{}
	}
	saved, err := repo.SaveRequestPluginInstance(ctx, &input, actor)
	if err == nil {
		s.requestPlugins.mu.Lock()
		s.requestPlugins.expires = time.Time{}
		s.requestPlugins.revision++
		s.requestPlugins.mu.Unlock()
	}
	return saved, err
}

func (s *NewAPIPluginService) validateRequestPluginConfig(ctx context.Context, v *NewAPIPluginVersion, config json.RawMessage) (json.RawMessage, error) {
	return s.validateRequestPluginStageConfig(ctx, v, config, false, false)
}

func validateRequestPluginStages(stages, formats []string, scope string) error {
	if len(stages) == 0 || len(stages) > 6 {
		return pluginInputError("选择已支持的处理阶段")
	}
	previous := -1
	for _, stage := range stages {
		order := slices.Index([]string{"ingress", "request", "outbound", "egress", "reply", "report"}, stage)
		if order < 0 || order <= previous {
			return pluginInputError("处理阶段必须唯一并按 ingress、request、outbound、egress、reply、report 顺序排列")
		}
		previous = order
	}
	seen := map[string]bool{}
	for _, format := range formats {
		if !slices.Contains([]string{"openai", "openai_responses", "claude", "gemini", "task"}, format) || seen[format] {
			return pluginInputError("请求格式范围无效")
		}
		seen[format] = true
	}
	if scope == "account" {
		if slices.ContainsFunc(stages, func(stage string) bool {
			return !slices.Contains([]string{"ingress", "request", "outbound", "egress", "reply", "report"}, stage)
		}) || len(formats) == 0 || slices.Contains(formats, "task") {
			return pluginInputError("账号实例需明确选择受支持的 HTTP 格式")
		}
		return nil
	}
	httpFormats := len(formats) > 0 && !slices.ContainsFunc(formats, func(format string) bool {
		return format != "openai" && format != "openai_responses" && format != "claude" && format != "gemini"
	})
	if slices.ContainsFunc(stages, func(stage string) bool { return stage == "request" || stage == "outbound" }) && !httpFormats {
		return pluginInputError("全局/分组 request/outbound 需明确选择 Chat/Responses/Claude/Gemini 格式")
	}
	if slices.Contains(stages, "reply") && (len(formats) == 0 || slices.Contains(formats, "task")) {
		return pluginInputError("全局/分组 reply 需明确选择受支持的 HTTP 格式")
	}
	if slices.Contains(stages, "egress") && !slices.Equal(formats, []string{"task"}) && !httpFormats {
		return pluginInputError("全局/分组 egress 需明确选择 Task 或 Chat/Responses/Claude/Gemini 格式")
	}
	if slices.Contains(stages, "report") && !slices.Equal(formats, []string{"task"}) && (len(formats) == 0 || slices.Contains(formats, "task")) {
		return pluginInputError("全局/分组 report 需明确选择 Task 或受支持的 HTTP 格式")
	}
	return nil
}

func (s *NewAPIPluginService) validateRequestPluginStageConfig(ctx context.Context, v *NewAPIPluginVersion, config json.RawMessage, egress, reply bool) (json.RawMessage, error) {
	if len(config) > 64<<10 {
		return nil, pluginInputError("实例配置超过 64 KiB")
	}
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	var validated json.RawMessage
	err := s.pool.With(ctx, v.Source, func(client *runner.Client) error {
		var err error
		validated, err = client.ValidateConfig(ctx, config, true)
		return err
	})
	if err != nil {
		return nil, pluginInputError("实例配置校验失败：" + err.Error())
	}
	if slices.Contains(v.Capabilities.Hooks, "reply") && gjson.GetBytes(validated, "replies").Bool() && !reply {
		return nil, pluginInputError("启用 replies 必须同时选择 HTTP reply 阶段")
	}
	if field := gjson.GetBytes(validated, "finalCheck"); !egress && slices.Contains(v.Capabilities.Hooks, "egress") && field.Exists() && field.String() != "off" {
		return nil, pluginInputError("未选择 egress 阶段，请显式将 finalCheck 设为 off")
	}
	return validated, nil
}

func (s *NewAPIPluginService) requestPluginSnapshots(ctx context.Context) ([]RequestPluginInstance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cache := &s.requestPlugins
	cache.mu.Lock()
	if time.Now().Before(cache.expires) {
		items := cache.items
		cache.mu.Unlock()
		return items, nil
	}
	refresh := cache.refreshing
	if refresh == nil {
		refresh = &requestPluginRefresh{done: make(chan struct{})}
		cache.refreshing = refresh
		revision := cache.revision
		// Refresh host-owned policy once for all callers. A disconnected client
		// cannot cancel everyone else's refresh; each caller can stop waiting.
		go func() {
			items, err := s.loadRequestPluginSnapshots()
			cache.mu.Lock()
			defer cache.mu.Unlock()
			if err == nil && cache.revision == revision {
				cache.items = items
				cache.expires = time.Now().Add(requestPluginCacheTTL)
			}
			refresh.items, refresh.err = items, err
			cache.refreshing = nil
			close(refresh.done)
		}()
	}
	cache.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-refresh.done:
		return refresh.items, refresh.err
	}
}

func (s *NewAPIPluginService) loadRequestPluginSnapshots() ([]RequestPluginInstance, error) {
	repo, err := s.requestPluginRepo()
	if err != nil {
		return nil, err
	}
	dbCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	items, err := repo.ListRequestPluginInstances(dbCtx, true)
	if err != nil {
		return nil, err
	} // never use expired security policy on read failure
	for i := range items {
		items[i].Config, err = s.decryptRequestPluginConfig(items[i].ConfigEncrypted)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

type RequestPluginCall struct {
	RequestID                             string
	UserID, TokenID, GroupID              int64
	TokenName, Group, Model, Format, Path string
	ModelCandidates                       []string
}

type RequestPluginPlan struct {
	service     *NewAPIPluginService
	items       []RequestPluginInstance
	call        RequestPluginCall
	ran         bool
	records     []RequestPluginRecord
	nextAttempt int
	report      *RequestPluginHTTPReport
	ingressRan  map[int64]bool
	dryRun      bool // Only the administrator's isolated, single-instance test.
	mediaText   mediahost.TextCache
}

// HasEarlyIngress excludes account rules: their account is unknown at the
// client boundary. In particular they must not impose early attachment checks
// or execute against a request that will ultimately use another account.
func (p *RequestPluginPlan) HasEarlyIngress() bool {
	return p != nil && slices.ContainsFunc(p.items, func(item RequestPluginInstance) bool {
		return p.earlyIngressItem(item)
	})
}

func (s *NewAPIPluginService) NewRequestPluginPlan(ctx context.Context, call RequestPluginCall) (*RequestPluginPlan, error) {
	items, err := s.requestPluginSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	var scoped []RequestPluginInstance
	// Stable ordering: global, then group; position and ID break ties in SQL.
	for _, scope := range []string{"global", "group", "account"} {
		for _, item := range items {
			if !item.Enabled || item.Scope != scope || scope == "group" && !slices.Contains(item.GroupIDs, call.GroupID) {
				continue
			}
			if len(item.Formats) > 0 && !slices.Contains(item.Formats, call.Format) {
				continue
			}
			candidates := call.ModelCandidates
			if len(candidates) == 0 {
				candidates = []string{call.Model}
			}
			if !slices.ContainsFunc(candidates, func(model string) bool {
				base := modelmodifier.Base(model)
				if len(item.Models) > 0 && !slices.Contains(item.Models, model) && !slices.Contains(item.Models, base) {
					return false
				}
				declared := item.Version.Meta.Scope
				return declared == nil || len(declared.Models) == 0 || slices.Contains(declared.Models, model) || slices.Contains(declared.Models, base)
			}) {
				continue
			}
			scoped = append(scoped, item)
		}
	}
	if len(scoped) == 0 {
		return nil, nil
	}
	return &RequestPluginPlan{service: s, items: scoped, call: call}, nil
}

func (s *NewAPIPluginService) RequestPluginsMayApply(ctx context.Context, groupID int64) (bool, error) {
	items, err := s.requestPluginSnapshots(ctx)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(items, func(item RequestPluginInstance) bool {
		return item.Enabled && (item.Scope == "global" || item.Scope == "account" || slices.Contains(item.GroupIDs, groupID))
	}), nil
}

type RequestPluginOutcome struct {
	Body            json.RawMessage       `json:"body"`
	Records         []RequestPluginRecord `json:"records"`
	Rejected        bool                  `json:"rejected"`
	ErrorClass      string                `json:"error_class,omitempty"`
	Headers         map[string]string     `json:"headers,omitempty"`
	ReasoningSet    bool                  `json:"-"`
	ReasoningEffort string                `json:"-"`
}

type RequestPluginFailure struct {
	Rejected bool
	Class    string
}

// TaskPolicyRejected projects the policy decision to the independent executor
// without making it depend on the host's request-processing implementation.
func (e *RequestPluginFailure) TaskPolicyRejected() bool { return e.Rejected }

func (e *RequestPluginFailure) Error() string {
	if e.Rejected {
		return "Request rejected by request-processing policy"
	}
	return "Request-processing policy could not be applied"
}

func (p *RequestPluginPlan) Ingress(ctx context.Context, body []byte) (RequestPluginOutcome, error) {
	out := RequestPluginOutcome{Body: body, Records: []RequestPluginRecord{}}
	if p == nil {
		return out, nil
	}
	if p.ran {
		return out, errors.New("request ingress already executed")
	}
	p.ran = true
	defer func() { p.records = slices.Clone(out.Records) }()
	if !p.HasEarlyIngress() {
		return out, nil
	}
	if len(body) > MaxRequestPluginBody || !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return out, &RequestPluginFailure{Class: "unsupported_body"}
	}
	// Includes worker admission, so a busy/failed worker cannot stall ingress.
	parentCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	form, _ := ctx.Value(generationMultipartBodyKey{}).(bool)
	mpForm := requestPluginMultipartFromContext(ctx)
	for _, instance := range p.items {
		if !p.earlyIngressItem(instance) {
			continue
		}
		if p.ingressRan == nil {
			p.ingressRan = map[int64]bool{}
		}
		p.ingressRan[instance.ID] = true
		record := RequestPluginRecord{InstanceID: instance.ID, VersionID: instance.PluginVersionID, Stage: "ingress", Action: "continue"}
		started := time.Now()
		var result jsplugin.TransformResult
		var media *mediahost.Source
		err := p.service.pool.With(ctx, instance.Version.Source, func(client *runner.Client) error {
			var err error
			if mpForm != nil {
				media, err = mpForm.MediaSource(out.Body, &p.mediaText)
			} else {
				media, err = requestPluginVideoSource(ctx, out.Body, &p.mediaText)
			}
			if err != nil {
				return err
			}
			values, err := p.service.callRequestPluginTransform(ctx, client, instance.NetworkEnabled, jsplugin.TransformContext{
				Stage: "ingress", Format: p.call.Format, Path: p.call.Path, Model: p.call.Model, Group: p.call.Group, Attempt: 0,
				User: jsplugin.TransformUser{ID: int(p.call.UserID)}, Token: &jsplugin.TransformToken{ID: int(p.call.TokenID), Name: p.call.TokenName},
				Instance: jsplugin.TransformInstance{ID: strconv.FormatInt(instance.ID, 10), Name: instance.Name}, Config: instance.Config, Media: media,
			}, jsplugin.TransformRequest{Body: media.View(), Media: media.Projection()})
			if err != nil {
				return err
			}
			if len(values) != 1 {
				return errors.New("invalid typed transform response")
			}
			return json.Unmarshal(values[0], &result)
		})
		class := "execution"
		if errors.Is(err, pkghttputil.ErrRequestBodyMemoryBudget) || errors.Is(err, mediahost.ErrMemoryCapacity) {
			class = "capacity"
		}
		if err == nil && result.Action == "reject" {
			record.Action = "reject"
			out.Rejected = true
		} else if err == nil {
			var effects []requestpatch.Effect
			if len(result.Effects) > 0 {
				effects, err = requestpatch.DecodeTransformEffects(result.Effects, requestpatch.EffectJSONPatch, requestpatch.EffectMediaText)
			}
			class = "invalid_effect"
			var prepared *mediahost.PreparedEffects
			if err == nil && requestPluginEffectHasMediaPlaceholder(effects) {
				err = errors.New("media placeholders cannot be inserted")
			}
			if err == nil && p.call.Format == "task" {
				err = checkVideoPolicyReferenceEffects(effects)
			}
			if err == nil {
				prepared, err = media.PrepareEffects(ctx, effects)
			}
			if err == nil && form {
				err = checkGenerationMultipartPolicyEffects(out.Body, prepared.Effects)
			}
			if err == nil && mpForm != nil {
				err = mpForm.CheckEffects(prepared.Effects, false)
			}
			if err == nil {
				var patched []byte
				patched, _, err = requestpatch.ApplyWithLimit(out.Body, nil, prepared.Effects, nil, MaxRequestPluginBody)
				if err == nil && (len(patched) > MaxRequestPluginBody || !gjson.ParseBytes(patched).IsObject() || !bytes.Equal(requestPluginRoutingFields(out.Body), requestPluginRoutingFields(patched))) {
					err = errors.New("protected routing fields or body limit changed")
					class = "guard"
				}
				if err == nil && mpForm != nil {
					if viewErr := mpForm.CheckView(out.Body, patched, false); viewErr != nil {
						err = viewErr
						class = "guard"
					}
				}
				if err == nil {
					out.Body = patched
					prepared.Commit()
					record.Media = prepared.Media
					record.Effects = len(effects)
				}
			}
		}
		if media != nil {
			record.Attachments, record.MoreAttachments = media.Records()
		}
		if errors.Is(err, pkghttputil.ErrRequestBodyMemoryBudget) || errors.Is(err, mediahost.ErrMemoryCapacity) {
			class = "capacity"
		}
		record.DurationMS = time.Since(started).Milliseconds()
		record.Findings = len(result.Findings)
		if err != nil {
			record.Action = "error"
			record.ErrorClass = class
			if instance.Version.Meta.Optional && parentCtx.Err() == nil {
				record.Action = "skipped"
			}
		}
		out.Records = append(out.Records, record)
		if out.Rejected {
			return out, &RequestPluginFailure{Rejected: true, Class: "rejected"}
		}
		if err != nil && record.Action != "skipped" {
			out.ErrorClass = class
			return out, &RequestPluginFailure{Class: class}
		}
	}
	return out, nil
}

// Preserve every spelling and duplicate of routing fields. Comparing only
// gjson.Get("model") would miss the last duplicate used by encoding/json.
func requestPluginRoutingFields(body []byte) []byte {
	var fields []string
	gjson.ParseBytes(body).ForEach(func(k, v gjson.Result) bool {
		if strings.EqualFold(k.String(), "model") || strings.EqualFold(k.String(), "stream") {
			fields = append(fields, k.String(), v.Raw)
		}
		if strings.EqualFold(k.String(), "session") {
			v.ForEach(func(sk, sv gjson.Result) bool {
				if strings.EqualFold(sk.String(), "model") || strings.EqualFold(sk.String(), "stream") {
					fields = append(fields, k.String()+"."+sk.String(), sv.Raw)
				}
				return true
			})
		}
		return true
	})
	encoded, _ := json.Marshal(fields)
	return encoded
}

func (s *NewAPIPluginService) RecordRequestPluginRun(ctx context.Context, call RequestPluginCall, out RequestPluginOutcome, dryRun bool) error {
	if len(out.Records) == 0 {
		return nil
	}
	repo, err := s.requestPluginRepo()
	if err != nil {
		return err
	}
	return repo.RecordRequestPluginRun(ctx, RequestPluginRun{RequestID: call.RequestID, UserID: call.UserID, GroupID: call.GroupID, Model: call.Model, Format: call.Format, DryRun: dryRun, Records: out.Records})
}

func (s *NewAPIPluginService) RequestPluginRuns(ctx context.Context, requestID string, page, size int) ([]RequestPluginRun, int64, error) {
	if len(requestID) > 128 || page < 1 || page > 100000 || size < 1 || size > 100 {
		return nil, 0, pluginInputError("执行记录查询无效")
	}
	repo, err := s.requestPluginRepo()
	if err != nil {
		return nil, 0, err
	}
	return repo.ListRequestPluginRuns(ctx, requestID, page, size)
}

// DryRun validates unsaved config and executes only the selected ingress
// instance against supplied JSON. It performs no fetch, upstream call or bill.
func (s *NewAPIPluginService) DryRunRequestPlugin(ctx context.Context, versionID int64, config, body json.RawMessage, call RequestPluginCall) (RequestPluginOutcome, error) {
	return s.DryRunRequestPluginStage(ctx, versionID, config, body, call, "ingress")
}

func (s *NewAPIPluginService) DryRunRequestPluginStage(ctx context.Context, versionID int64, config, body json.RawMessage, call RequestPluginCall, stage string) (RequestPluginOutcome, error) {
	ctx = context.WithValue(ctx, requestPluginOfflineKey{}, true)
	v, err := s.repo.Get(ctx, versionID)
	if err != nil {
		return RequestPluginOutcome{}, err
	}
	if err = requestPluginVersionSupported(v); err != nil {
		return RequestPluginOutcome{}, err
	}
	lateIngress := stage == "late_ingress"
	hook := stage
	if lateIngress {
		hook = "ingress"
	}
	provider := (lateIngress || stage == "request" || stage == "outbound" || stage == "egress") && slices.Contains([]string{"openai", "openai_responses", "claude", "gemini"}, call.Format)
	reply := stage == "reply" && slices.Contains([]string{"openai", "openai_responses", "claude", "gemini"}, call.Format)
	if stage != "ingress" && !(stage == "egress" && call.Format == "task") && !provider && !reply || !slices.Contains(v.Capabilities.Hooks, hook) {
		return RequestPluginOutcome{}, pluginInputError("试运行阶段或请求格式未支持")
	}
	config, err = s.validateRequestPluginStageConfig(ctx, v, config, stage == "egress", reply)
	if err != nil {
		return RequestPluginOutcome{}, err
	}
	var out RequestPluginOutcome
	var runErr error
	if reply {
		out, runErr = s.dryRunReply(ctx, v, config, body, call)
	} else if provider {
		if lateIngress {
			stage = "ingress"
		}
		p := &RequestPluginPlan{service: s, call: call, dryRun: true, items: []RequestPluginInstance{{PluginVersionID: versionID, Version: v, Config: config, Name: "Dry run", Scope: "account", AccountIDs: []int64{0}, Stages: []string{stage}}}}
		ctx = WithRequestPluginPlan(ctx, p)
		ctx = context.WithValue(ctx, requestPluginProviderBodyKey{}, []byte(body))
		ctx = context.WithValue(ctx, requestPluginProviderDryRunKey{}, stage)
		path := newAPIProviderChat
		if call.Format == "openai_responses" {
			path = newAPIProviderResponses
		}
		if call.Format == "claude" || call.Format == "gemini" {
			platform, guard := PlatformAnthropic, nativeProtocolRequestBodyGuard
			if call.Format == "gemini" {
				platform, guard = PlatformGemini, nativeGeminiRequestBodyGuard
			}
			out, runErr = s.relayRequestPluginStages(ctx, &Account{Platform: platform}, nativeRequestStagePath(call.Format, call.Model), call.Model, body, contract.AdvancedCustomRoute{}, requestPluginStageRun{stages: []string{stage}, format: call.Format, protected: nativeProtocolRequestProtectedHeader, bodyGuard: guard})
		} else {
			out, runErr = s.providerRequestPluginStages(ctx, &Account{}, path, call.Model, body, contract.AdvancedCustomRoute{})
		}
	} else if stage == "egress" {
		digest := sha256.Sum256([]byte(v.Source))
		snapshot := taskRequestPluginSnapshot{Call: call, Items: []taskRequestPluginItem{{VersionID: versionID, SHA256: hex.EncodeToString(digest[:]), Name: "Dry run", Config: config}}}
		out, runErr = s.runTaskRequestPluginEgress(ctx, &GenerationTask{Model: call.Model, UserID: call.UserID, APIKeyID: call.TokenID, GroupID: call.GroupID}, snapshot, body, false, nil)
	} else {
		p := &RequestPluginPlan{service: s, call: call, dryRun: true, items: []RequestPluginInstance{{PluginVersionID: versionID, Version: v, Config: config, Name: "Dry run", Stages: []string{"ingress"}}}}
		out, runErr = p.Ingress(ctx, body)
	}
	var failure *RequestPluginFailure
	if runErr != nil && !errors.As(runErr, &failure) {
		return out, runErr
	}
	if failure != nil {
		out.ErrorClass = failure.Class
		out.Rejected = failure.Rejected
	}
	return out, nil
}

var _ error = (*RequestPluginFailure)(nil)
