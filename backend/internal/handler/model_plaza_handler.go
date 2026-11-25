package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ModelPlazaHandler 处理「模型广场」查询。
//
// 广场路由挂 OptionalJWT 中间件：匿名可访问（除非 require_auth 开启），带 token 则
// 识别用户。可见性规则（橱窗语义，与「可用渠道」的可绑定语义不同）：
//   - 匿名：仅非专属分组（订阅型照常展示）；
//   - 登录：非专属分组 + user_allowed_groups 授权或持有有效订阅的专属分组；
//     若该用户开启了公开分组限制，则公开分组同样需要落在授权集合内。
type ModelPlazaHandler struct {
	plazaService        *service.ModelPlazaService
	apiKeyService       *service.APIKeyService
	settingService      *service.SettingService
	authService         *service.AuthService
	notificationSlots   chan struct{}
	notificationContext context.Context
	notificationCancel  context.CancelFunc
}

// NewModelPlazaHandler 创建模型广场 handler。
func NewModelPlazaHandler(
	plazaService *service.ModelPlazaService,
	apiKeyService *service.APIKeyService,
	settingService *service.SettingService,
) *ModelPlazaHandler {
	ctx, cancel := context.WithCancel(context.Background())
	return &ModelPlazaHandler{
		plazaService:        plazaService,
		apiKeyService:       apiKeyService,
		settingService:      settingService,
		notificationSlots:   make(chan struct{}, 16),
		notificationContext: ctx,
		notificationCancel:  cancel,
	}
}

func ProvideModelPlazaHandler(plaza *service.ModelPlazaService, keys *service.APIKeyService, settings *service.SettingService, auth *service.AuthService) *ModelPlazaHandler {
	h := NewModelPlazaHandler(plaza, keys, settings)
	h.authService = auth
	return h
}

// modelPlazaOfficialPricing 官方参考价（USD per token，与计费目录同源）。
type modelPlazaOfficialPricing struct {
	InputPrice         *float64 `json:"input_price"`
	OutputPrice        *float64 `json:"output_price"`
	CacheWritePrice    *float64 `json:"cache_write_price"`
	CacheWrite1hPrice  *float64 `json:"cache_write_1h_price,omitempty"`
	CacheWrite12hPrice *float64 `json:"cache_write_12h_price,omitempty"`
	CacheReadPrice     *float64 `json:"cache_read_price"`
	// Intervals 官方长上下文阶梯，仅多档模型给出。
	Intervals []userPricingIntervalDTO `json:"intervals,omitempty"`
}

// modelPlazaTimePricingPeriod 分时倍率时段（配置时区当天 [start, end)）。
type modelPlazaTimePricingPeriod struct {
	StartTime  string  `json:"start_time"`
	EndTime    string  `json:"end_time"`
	Multiplier float64 `json:"multiplier"`
}

// modelPlazaTimePricing 计费会生效的分时倍率（仅倍率 ≠ 1 的时段）。
// WeekdaysOnly 为 true 时时段仅周一至周五生效，周末整天按标准价计费。
type modelPlazaTimePricing struct {
	Timezone     string                        `json:"timezone"`
	WeekdaysOnly bool                          `json:"weekdays_only,omitempty"`
	Periods      []modelPlazaTimePricingPeriod `json:"periods"`
}

// modelPlazaModel 广场模型条目：实收口径展示定价（白名单形态）+ 官方参考价。
type modelPlazaModel struct {
	Capabilities      *service.PlazaCapabilities       `json:"capabilities,omitempty"`
	Protocols         []string                         `json:"protocols,omitempty"`
	GenerationPricing []service.GenerationDisplayPrice `json:"generation_pricing,omitempty"`
	ID                string                           `json:"id"`
	Name              string                           `json:"name"`
	Platform          string                           `json:"platform"`
	Pricing           *userSupportedModelPricing       `json:"pricing"`
	OfficialPricing   *modelPlazaOfficialPricing       `json:"official_pricing"`
	// LongContextBasis 多档时的计价基准："whole_request"（整单按档）| "marginal"（仅超出部分）。
	LongContextBasis string `json:"long_context_basis,omitempty"`
	// TimePricing 分时倍率时段，落在时段内的请求整单乘倍率；无分时省略。
	TimePricing *modelPlazaTimePricing `json:"time_pricing,omitempty"`
}

// modelPlazaGroup 广场分组条目（白名单字段）。
type modelPlazaGroup struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	Platform           string   `json:"platform"`
	SubscriptionType   string   `json:"subscription_type"`
	RateMultiplier     float64  `json:"rate_multiplier"`
	UserRateMultiplier *float64 `json:"user_rate_multiplier,omitempty"`
	PeakRateEnabled    bool     `json:"peak_rate_enabled"`
	PeakStart          string   `json:"peak_start"`
	PeakEnd            string   `json:"peak_end"`
	PeakRateMultiplier float64  `json:"peak_rate_multiplier"`
	IsExclusive        bool     `json:"is_exclusive"`
	// 生图独立倍率：为 true 时图片计费模型的实付倍率取 ImageRateMultiplier，
	// 不取分组/用户专属倍率。
	ImageRateIndependent bool    `json:"image_rate_independent"`
	ImageRateMultiplier  float64 `json:"image_rate_multiplier"`
	VideoRateIndependent bool    `json:"video_rate_independent"`
	VideoRateMultiplier  float64 `json:"video_rate_multiplier"`
	// 分组是否启用长上下文阶梯计费；关闭时模型实付列只展示最低档/基础价。
	LongContextPricingEnabled bool              `json:"long_context_pricing_enabled"`
	Models                    []modelPlazaModel `json:"models"`
}

// modelPlazaResponse 广场页响应。
type modelPlazaResponse struct {
	Description string            `json:"description"`
	Groups      []modelPlazaGroup `json:"groups"`
}

// Get 返回模型广场数据。
// GET /api/v1/model-plaza
func (h *ModelPlazaHandler) Get(c *gin.Context) {
	h.get(c, "", false)
}

// GetModel serves a stable, shareable model detail resource. It uses exactly
// the same visibility and personal-price rules as the catalog.
func (h *ModelPlazaHandler) GetModel(c *gin.Context) {
	h.get(c, c.Param("model_id"), false)
}

func (h *ModelPlazaHandler) GetPerformance(c *gin.Context) {
	h.get(c, c.Param("model_id"), true)
}

func (h *ModelPlazaHandler) GetPerformanceSummaries(c *gin.Context) {
	h.get(c, "", true)
}

func (h *ModelPlazaHandler) get(c *gin.Context, modelID string, performance bool) {
	subject, authed := middleware.GetAuthSubjectFromContext(c)
	var userID int64
	if authed {
		userID = subject.UserID
	}
	rt, visible, err := h.visibleScope(c.Request.Context(), userID, c.Query("group_id"), modelID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var userRates map[int64]float64
	if authed && !performance {
		userRates, err = h.apiKeyService.GetUserGroupRates(c.Request.Context(), userID)
		if err != nil {
			slog.Warn("model_plaza_user_rates_failed", "error", err, "user_id", userID)
			userRates = nil
		}
	}

	// Prices and visibility may be personalized, even on an otherwise public
	// route. Shared caches must never reuse one user's catalog for another.
	c.Header("Cache-Control", "private, no-store")
	if performance {
		window, _, _, ok := service.ParsePlazaMetricsWindow(c.Query("window"))
		if !ok {
			response.BadRequest(c, "Invalid window")
			return
		}
		if modelID == "" {
			ids := strings.Split(c.Query("ids"), ",")
			if len(ids) > 100 {
				response.BadRequest(c, "At most 100 model IDs are allowed")
				return
			}
			requested := make(map[string]bool, len(ids))
			for _, id := range ids {
				if len(id) != 36 || !strings.HasPrefix(id, "mdl_") || strings.Trim(id[4:], "0123456789abcdef") != "" {
					response.BadRequest(c, "Invalid model ID")
					return
				}
				requested[id] = true
			}
			metrics, err := h.plazaService.PerformanceSummariesWindow(c.Request.Context(), plazaSummaryScopes(visible, requested), window)
			if err != nil {
				response.ErrorFrom(c, err)
				return
			}
			response.Success(c, metrics)
			return
		}
		if len(visible) == 0 {
			response.NotFound(c, "Model not found")
			return
		}
		model := visible[0].Models[0]
		groupIDs := make([]int64, 0, len(visible))
		for _, group := range visible {
			groupIDs = append(groupIDs, group.ID)
		}
		metrics, err := h.plazaService.PerformanceWindow(c.Request.Context(), model.Name, model.Platform, groupIDs, window)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, metrics)
		return
	}

	// 广场可匿名访问，分组文案随请求语言变化，提示中间缓存按语言区分。
	// 追加而非覆盖：CORS 中间件已为具体来源写入 Vary: Origin。
	c.Writer.Header().Add("Vary", "Accept-Language")
	out := toLocalizedModelPlazaGroups(visible, userRates, c.GetHeader("Accept-Language"))
	response.Success(c, modelPlazaResponse{
		Description: rt.Description,
		Groups:      out,
	})
}

func (h *ModelPlazaHandler) visibleScope(ctx context.Context, userID int64, rawGroup, modelID string) (service.ModelPlazaRuntime, []service.PlazaGroup, error) {
	var rt service.ModelPlazaRuntime
	if h.settingService == nil {
		return rt, nil, infraerrors.NotFound("MODEL_PLAZA_DISABLED", "Model plaza is not enabled")
	}
	rt = h.settingService.GetModelPlazaRuntime(ctx)
	if !rt.Enabled {
		return rt, nil, infraerrors.NotFound("MODEL_PLAZA_DISABLED", "Model plaza is not enabled")
	}
	if rt.RequireAuth && userID == 0 {
		return rt, nil, infraerrors.New(http.StatusUnauthorized, "AUTH_REQUIRED", "Authentication required")
	}
	groups, err := h.plazaService.ListGroups(ctx)
	if err != nil {
		return rt, nil, err
	}
	var allowed map[int64]struct{}
	var restricted bool
	if userID > 0 {
		allowed, restricted, err = h.apiKeyService.GetUserGroupVisibility(ctx, userID)
		if err != nil {
			return rt, nil, err
		}
	}
	visible := filterPlazaVisibleGroups(groups, allowed, restricted)
	if rawGroup != "" {
		groupID, err := strconv.ParseInt(rawGroup, 10, 64)
		if err != nil || groupID <= 0 {
			return rt, nil, infraerrors.BadRequest("INVALID_GROUP", "Invalid group ID")
		}
		selected := make([]service.PlazaGroup, 0, 1)
		for _, group := range visible {
			if group.ID == groupID {
				selected = append(selected, group)
				break
			}
		}
		if len(selected) == 0 {
			return rt, nil, infraerrors.NotFound("GROUP_NOT_FOUND", "Group not found")
		}
		visible = selected
	}
	if modelID != "" {
		visible = filterPlazaModel(visible, modelID)
		if len(visible) == 0 {
			return rt, nil, infraerrors.NotFound("MODEL_NOT_FOUND", "Model not found")
		}
	}
	return rt, visible, nil
}

func plazaSummaryScopes(visible []service.PlazaGroup, requested map[string]bool) []service.PlazaMetricsScope {
	indices := map[string]int{}
	scopes := make([]service.PlazaMetricsScope, 0, len(requested))
	for _, group := range visible {
		for _, model := range group.Models {
			id := plazaModelID(model.Platform, model.Name)
			if !requested[id] {
				continue
			}
			index, exists := indices[id]
			if !exists {
				index = len(scopes)
				indices[id] = index
				scopes = append(scopes, service.PlazaMetricsScope{ID: id, Model: model.Name, Platform: model.Platform})
			}
			scopes[index].GroupIDs = append(scopes[index].GroupIDs, group.ID)
		}
	}
	return scopes
}

// filterPlazaVisibleGroups 按登录态裁剪分组可见性。
// allowedGroups == nil 表示匿名（仅非专属）；非 nil 包含普通授权及有效订阅分组。
// restrictPublicGroups 为 true 时，公开分组也必须落在 allowedGroups 内，否则用户会
// 在广场看到自己实际绑定不了的分组。
func filterPlazaVisibleGroups(
	groups []service.PlazaGroup,
	allowedGroups map[int64]struct{},
	restrictPublicGroups bool,
) []service.PlazaGroup {
	visible := make([]service.PlazaGroup, 0, len(groups))
	for _, g := range groups {
		if g.IsExclusive || (restrictPublicGroups && allowedGroups != nil) {
			if allowedGroups == nil {
				continue
			}
			if _, ok := allowedGroups[g.ID]; !ok {
				continue
			}
		}
		visible = append(visible, g)
	}
	return visible
}

// toLocalizedModelPlazaGroups 转换可见分组，并把名称与描述换成请求首选语言的版本。
func toLocalizedModelPlazaGroups(visible []service.PlazaGroup, userRates map[int64]float64, acceptLanguage string) []modelPlazaGroup {
	out := make([]modelPlazaGroup, 0, len(visible))
	for i := range visible {
		group := toModelPlazaGroupDTO(&visible[i], userRates)
		group.Name, group.Description = visible[i].I18n.Localize(acceptLanguage, group.Name, group.Description)
		out = append(out, group)
	}
	return out
}

// toModelPlazaGroupDTO 将 service 层广场分组映射为白名单 DTO,并合并用户专属倍率。
func toModelPlazaGroupDTO(g *service.PlazaGroup, userRates map[int64]float64) modelPlazaGroup {
	models := make([]modelPlazaModel, 0, len(g.Models))
	for i := range g.Models {
		m := &g.Models[i]
		models = append(models, modelPlazaModel{
			Protocols:         m.Protocols,
			Capabilities:      m.Capabilities,
			GenerationPricing: m.GenerationPricing,
			ID:                plazaModelID(m.Platform, m.Name),
			Name:              m.Name,
			Platform:          m.Platform,
			Pricing:           toUserPricing(m.Pricing),
			OfficialPricing:   toModelPlazaOfficialPricing(m.OfficialPricing),
			LongContextBasis:  string(m.LongContextBasis),
			TimePricing:       toModelPlazaTimePricing(m.TimePricing),
		})
	}
	dto := modelPlazaGroup{
		ID:                        g.ID,
		Name:                      g.Name,
		Description:               g.Description,
		Platform:                  g.Platform,
		SubscriptionType:          g.SubscriptionType,
		RateMultiplier:            g.RateMultiplier,
		PeakRateEnabled:           g.PeakRateEnabled,
		PeakStart:                 g.PeakStart,
		PeakEnd:                   g.PeakEnd,
		PeakRateMultiplier:        g.PeakRateMultiplier,
		IsExclusive:               g.IsExclusive,
		ImageRateIndependent:      g.ImageRateIndependent,
		ImageRateMultiplier:       g.ImageRateMultiplier,
		VideoRateIndependent:      g.VideoRateIndependent,
		VideoRateMultiplier:       g.VideoRateMultiplier,
		LongContextPricingEnabled: g.LongContextPricingEnabled,
		Models:                    models,
	}
	if rate, ok := userRates[g.ID]; ok {
		dto.UserRateMultiplier = &rate
	}
	return dto
}

// Hash the platform and the complete model ID rather than putting arbitrary
// provider model paths (including slashes) in a route parameter.
func plazaModelID(platform, model string) string {
	digest := sha256.Sum256([]byte(platform + "\x00" + model))
	return "mdl_" + hex.EncodeToString(digest[:16])
}

func filterPlazaModel(groups []service.PlazaGroup, modelID string) []service.PlazaGroup {
	result := make([]service.PlazaGroup, 0, len(groups))
	for _, group := range groups {
		models := make([]service.PlazaModel, 0, 1)
		for _, model := range group.Models {
			if plazaModelID(model.Platform, model.Name) == modelID {
				models = append(models, model)
			}
		}
		if len(models) > 0 {
			group.Models = models
			result = append(result, group)
		}
	}
	return result
}

// toModelPlazaTimePricing 转换分时倍率；nil 透传（JSON 省略）。
func toModelPlazaTimePricing(p *service.TimePricingSchedule) *modelPlazaTimePricing {
	if p == nil || len(p.Periods) == 0 {
		return nil
	}
	periods := make([]modelPlazaTimePricingPeriod, 0, len(p.Periods))
	for _, period := range p.Periods {
		periods = append(periods, modelPlazaTimePricingPeriod{
			StartTime:  period.StartTime,
			EndTime:    period.EndTime,
			Multiplier: period.Multiplier,
		})
	}
	return &modelPlazaTimePricing{Timezone: p.Timezone, WeekdaysOnly: p.WeekdaysOnly, Periods: periods}
}

// toModelPlazaOfficialPricing 转换官方参考价；nil 透传（前端显示 "-"）。
func toModelPlazaOfficialPricing(p *service.PlazaOfficialPricing) *modelPlazaOfficialPricing {
	if p == nil {
		return nil
	}
	return &modelPlazaOfficialPricing{
		InputPrice:         p.InputPrice,
		OutputPrice:        p.OutputPrice,
		CacheWritePrice:    p.CacheWritePrice,
		CacheWrite1hPrice:  p.CacheWrite1hPrice,
		CacheWrite12hPrice: p.CacheWrite12hPrice,
		CacheReadPrice:     p.CacheReadPrice,
		Intervals:          toUserPricingIntervals(p.Intervals),
	}
}
