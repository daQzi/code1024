package service

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/shopspring/decimal"
)

// NativeVideoBillingSnapshot contains no account, key, request body or mutable
// pricing object. Native completions have one linear unit: seconds, output
// tokens, or one request. Resolve that unit through the existing price engine
// before submitting, rather than looking up today's price on completion.
type NativeVideoBillingSnapshot struct {
	Version           int              `json:"version"`
	UserID            int64            `json:"user_id"`
	APIKeyID          int64            `json:"api_key_id"`
	GroupID           int64            `json:"group_id"`
	AccountID         int64            `json:"account_id"`
	Protocol          string           `json:"protocol"`
	Model             string           `json:"model"`
	UpstreamModel     string           `json:"upstream_model"`
	TaskID            string           `json:"task_id"`
	OriginalModel     string           `json:"original_model"`
	Resolution        string           `json:"resolution"`
	Unit              string           `json:"unit"`
	BillingMode       string           `json:"billing_mode"`
	PricingAt         time.Time        `json:"pricing_at"`
	UnitTotal         decimal.Decimal  `json:"unit_total"`
	UnitActual        decimal.Decimal  `json:"unit_actual"`
	UnitOutput        decimal.Decimal  `json:"unit_output"`
	Multiplier        float64          `json:"multiplier"`
	AccountMultiplier float64          `json:"account_multiplier"`
	StatsBase         *decimal.Decimal `json:"stats_base,omitempty"`
	StatsUnit         *decimal.Decimal `json:"stats_unit,omitempty"`
	SubscriptionID    *int64           `json:"subscription_id,omitempty"`
	QuotaPlatform     string           `json:"quota_platform"`
	Mode              string           `json:"mode"`
	KeyQuota          bool             `json:"key_quota"`
	KeyRateLimit      bool             `json:"key_rate_limit"`
	AccountQuota      bool             `json:"account_quota"`
	AccountDaily      bool             `json:"account_daily"`
	AccountWeekly     bool             `json:"account_weekly"`
}

func (p *NativeVideoBillingSnapshot) Validate() error {
	if p == nil || p.Version != 1 || p.UserID <= 0 || p.APIKeyID <= 0 || p.GroupID <= 0 || p.AccountID <= 0 || p.PricingAt.IsZero() || p.Model == "" || len(p.Model) > 512 || len(p.UpstreamModel) > 512 || len(p.OriginalModel) > 512 || len(p.TaskID) > 512 || strings.ContainsRune(p.Model+p.UpstreamModel+p.OriginalModel+p.TaskID+p.QuotaPlatform, 0) {
		return errors.New("invalid native video financial snapshot")
	}
	if p.Protocol != GenerationProtocolXAI && p.Protocol != GenerationProtocolArk || p.Unit != "seconds" && p.Unit != "output_tokens" && p.Unit != "request" || p.Protocol == GenerationProtocolArk && p.Unit == "seconds" || p.Protocol == GenerationProtocolXAI && p.Unit == "output_tokens" {
		return errors.New("invalid native video billing unit")
	}
	if p.Mode != "standard" && p.Mode != "log_only" && p.Mode != "rate_only" || p.SubscriptionID != nil && *p.SubscriptionID <= 0 {
		return errors.New("invalid native video funding source")
	}
	if p.BillingMode != string(BillingModeToken) && p.BillingMode != string(BillingModeVideo) && p.BillingMode != string(BillingModePerRequest) && p.BillingMode != string(BillingModeImage) {
		return errors.New("invalid native video billing mode")
	}
	for _, v := range []decimal.Decimal{p.UnitTotal, p.UnitActual, p.UnitOutput} {
		if !validNativeVideoPrice(v) {
			return errors.New("invalid native video unit price")
		}
	}
	for _, v := range []float64{p.Multiplier, p.AccountMultiplier} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return errors.New("invalid native video multiplier")
		}
	}
	if (p.StatsBase == nil) != (p.StatsUnit == nil) || p.StatsBase != nil && (!validNativeVideoPrice(*p.StatsBase) || !validNativeVideoPrice(*p.StatsUnit)) {
		return errors.New("invalid native video statistics price")
	}
	return nil
}

func validNativeVideoPrice(v decimal.Decimal) bool {
	// Bound scale before comparing decimals so corrupt private JSON cannot
	// force an enormous exponent alignment allocation.
	return v.Exponent() >= -18 && v.Exponent() <= 18 && !v.IsNegative() && v.LessThanOrEqual(decimal.NewFromInt(1_000_000))
}

// CaptureNativeVideoBilling fixes the price, multiplier and original payer.
// Unsupported/missing prices return an error; the caller keeps the pre-existing
// native flow without inventing a free price or enrolling automatic billing.
func (s *OpenAIGatewayService) CaptureNativeVideoBilling(ctx context.Context, key *APIKey, account *Account, sub *UserSubscription, protocol, model, original, resolution, platform string, at time.Time) (*NativeVideoBillingSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.billingService == nil || key == nil || key.User == nil || key.GroupID == nil || key.Group == nil || account == nil || account.Type != AccountTypeAPIKey || account.HasNewAPIPlugin() || key.User.ID != key.UserID || *key.GroupID != key.Group.ID {
		return nil, errors.New("native video billing context unavailable")
	}
	key = s.apiKeyWithFreshGroupMediaPricing(ctx, key)
	base := s.ResolveUserGroupRateMultiplier(ctx, key.UserID, *key.GroupID, key.Group.RateMultiplier)
	tokenRate, _ := computePeakAwareMultipliers(key, base, at)
	videoRate := resolveVideoRateMultiplier(key, base)
	for _, value := range []float64{tokenRate, videoRate, account.BillingRateMultiplier()} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return nil, errors.New("invalid native video multiplier")
		}
	}
	p := &NativeVideoBillingSnapshot{Version: 1, UserID: key.UserID, APIKeyID: key.ID, GroupID: *key.GroupID, AccountID: account.ID,
		Protocol: protocol, Model: model, UpstreamModel: account.GetMappedModel(model), OriginalModel: original,
		Resolution: NormalizeVideoBillingResolutionOrDefault(resolution), PricingAt: at.UTC(), AccountMultiplier: account.BillingRateMultiplier(),
		QuotaPlatform: platform, Mode: "standard", KeyQuota: key.Quota > 0, KeyRateLimit: key.HasRateLimits(),
		AccountQuota: account.SupportsAccountQuota() && account.HasAnyQuotaLimit(), AccountDaily: account.GetQuotaDailyLimit() > 0, AccountWeekly: account.GetQuotaWeeklyLimit() > 0}
	if sub != nil && key.Group.IsSubscriptionType() {
		if sub.ID <= 0 || sub.UserID != key.UserID || sub.GroupID != *key.GroupID {
			return nil, errors.New("native video subscription ownership mismatch")
		}
		id := sub.ID
		p.SubscriptionID = &id
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		p.Mode = "log_only"
		if simpleModeKeyRateLimitBillingEnabled(s.cfg, key) {
			p.Mode = "rate_only"
		}
	}
	result := &OpenAIForwardResult{Model: model, BillingModel: model, UpstreamModel: p.UpstreamModel}
	resolved := s.resolveOpenAIChannelPricing(ctx, model, key)
	switch protocol {
	case GenerationProtocolXAI:
		if resolved != nil && resolved.Mode == BillingModeToken {
			return nil, errors.New("native xai token pricing requires the legacy completion path")
		}
		result.VideoCount, result.VideoDurationSeconds, result.VideoResolution = 1, 1, p.Resolution
		p.Unit, p.Multiplier = "seconds", videoRate
		// Group video overrides take priority over channel per-request pricing,
		// exactly as calculateOpenAIVideoCost does.
		if !apiKeyHasConfiguredVideoPrice(key, model, p.Resolution) && resolved != nil && resolved.Source == PricingSourceChannel && (resolved.Mode == BillingModePerRequest || resolved.Mode == BillingModeImage) {
			p.Unit = "request"
		}
	case GenerationProtocolArk:
		result.Usage.OutputTokens = 1
		p.Unit, p.Multiplier = "output_tokens", tokenRate
	default:
		return nil, errors.New("unsupported native video financial protocol")
	}
	gate := openAILongContextBillingGate(account)
	cost, err := s.calculateOpenAIRecordUsageCost(ctx, result, key, []string{model}, tokenRate, base, videoRate, base, result.Usage.billingUsageTokens(), "", gate, false, at)
	if err != nil || cost == nil {
		return nil, errors.New("native video price unavailable")
	}
	for _, value := range []float64{cost.TotalCost, cost.ActualCost, cost.OutputCost} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return nil, errors.New("invalid native video unit price")
		}
	}
	if protocol == GenerationProtocolArk && cost.BillingMode != string(BillingModeToken) {
		p.Unit = "request"
	}
	p.BillingMode = cost.BillingMode
	p.UnitTotal, p.UnitActual, p.UnitOutput = decimal.NewFromFloat(cost.TotalCost), decimal.NewFromFloat(cost.ActualCost), decimal.NewFromFloat(cost.OutputCost)
	// Statistics can have an independent per-request component and a linear
	// component. Customer context tiers depend on input tokens (always zero),
	// but custom statistics intervals include output tokens: do not approximate
	// those rules with a linear snapshot.
	if protocol == GenerationProtocolArk && s.channelService != nil {
		channel, err := s.channelService.GetChannelForGroup(ctx, *key.GroupID)
		if err != nil {
			return nil, errors.New("native video statistics pricing unavailable")
		}
		if channel != nil {
			platform := s.channelService.GetGroupPlatform(ctx, *key.GroupID)
			for _, rule := range channel.AccountStatsPricingRules {
				pricing := findPricingForModel(rule.Pricing, platform, strings.ToLower(p.UpstreamModel))
				if matchAccountStatsRule(&rule, account.ID, *key.GroupID) && pricing != nil && len(pricing.Intervals) > 0 && pricing.BillingMode != BillingModePerRequest && pricing.BillingMode != BillingModeImage && pricing.BillingMode != BillingModeVideo {
					return nil, errors.New("native video tiered statistics require the legacy completion path")
				}
			}
		}
	}
	// Use positive quantities: zero-token rules may fall through to a different
	// statistics source, although that source never applies to a completion.
	tokens2 := result.Usage.billingUsageTokens()
	tokens2.OutputTokens *= 2
	total2 := cost.TotalCost
	if p.Unit != "request" {
		total2 *= 2
	}
	stats1 := resolveAccountStatsCost(ctx, s.channelService, s.billingService, account.ID, *key.GroupID, p.UpstreamModel, result.Usage.billingUsageTokens(), 1, cost.TotalCost, "", at, accountStatsLongContextPricingEnabled(gate), 1)
	stats2 := resolveAccountStatsCost(ctx, s.channelService, s.billingService, account.ID, *key.GroupID, p.UpstreamModel, tokens2, 1, total2, "", at, accountStatsLongContextPricingEnabled(gate), 1)
	if stats1 != nil || stats2 != nil {
		if stats1 == nil || stats2 == nil || math.IsNaN(*stats1) || math.IsInf(*stats1, 0) || math.IsNaN(*stats2) || math.IsInf(*stats2, 0) {
			return nil, errors.New("invalid native video statistics price")
		}
		one, two := decimal.NewFromFloat(*stats1), decimal.NewFromFloat(*stats2)
		unit := two.Sub(one)
		base := one.Sub(unit)
		p.StatsBase, p.StatsUnit = &base, &unit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *NativeVideoBillingSnapshot) quantity(r *OpenAIForwardResult) (decimal.Decimal, error) {
	if err := p.Validate(); err != nil {
		return decimal.Zero, err
	}
	if r == nil || p.TaskID == "" || r.ResponseID != p.TaskID || p.Protocol == GenerationProtocolXAI && r.VideoCount != 1 || p.Protocol == GenerationProtocolArk && (r.Usage.OutputTokens <= 0 || r.Usage.InputTokens != 0) {
		return decimal.Zero, errors.New("native video final usage unavailable")
	}
	switch p.Unit {
	case "seconds":
		if NormalizeVideoBillingResolutionOrDefault(r.VideoResolution) != p.Resolution {
			return decimal.Zero, errors.New("native video resolution changed")
		}
		return decimal.NewFromInt(int64(NormalizeVideoBillingDurationSecondsOrDefault(r.VideoDurationSeconds))), nil
	case "output_tokens":
		return decimal.NewFromInt(int64(r.Usage.OutputTokens)), nil
	default:
		return decimal.NewFromInt(1), nil
	}
}

func (p *NativeVideoBillingSnapshot) command(log *UsageLog, cost *CostBreakdown, payload string) *UsageBillingCommand {
	c := &UsageBillingCommand{NativeVideoLog: log, NativeVideoLogOnly: p.Mode == "log_only", RequestID: log.RequestID, UserID: p.UserID, APIKeyID: p.APIKeyID, AccountID: p.AccountID, AccountType: AccountTypeAPIKey,
		Model: log.Model, BillingType: log.BillingType, OutputTokens: log.OutputTokens, RequestPayloadHash: payload}
	switch p.Mode {
	case "rate_only":
		if p.KeyRateLimit {
			c.APIKeyRateLimitCost = cost.ActualCost
		}
	case "standard":
		if p.SubscriptionID != nil {
			id := *p.SubscriptionID
			c.SubscriptionID, c.SubscriptionCost = &id, cost.ActualCost
		} else {
			c.BalanceCost = cost.ActualCost
		}
		if p.KeyQuota {
			c.APIKeyQuotaCost = cost.ActualCost
		}
		if p.KeyRateLimit {
			c.APIKeyRateLimitCost = cost.ActualCost
		}
		if p.AccountQuota {
			c.AccountQuotaCost = cost.TotalCost * p.AccountMultiplier
			c.AccountQuotaHasDailyLimit, c.AccountQuotaHasWeeklyLimit = p.AccountDaily, p.AccountWeekly
		}
	}
	c.Normalize()
	return c
}

func (s *OpenAIGatewayService) recordNativeVideoFrozenUsage(ctx context.Context, in *OpenAIRecordUsageInput) error {
	r, p := in.Result, in.Result.NativeVideoBilling
	if _, err := p.quantity(r); err != nil {
		return err
	}
	if in.APIKey == nil || in.User == nil || in.Account == nil || in.APIKey.GroupID == nil || p.UserID != in.User.ID || p.UserID != in.APIKey.UserID || p.APIKeyID != in.APIKey.ID || p.GroupID != *in.APIKey.GroupID || p.AccountID != in.Account.ID {
		return errors.New("native video financial ownership mismatch")
	}
	if s.usageBillingRepo == nil {
		return errors.New("native video durable billing unavailable")
	}
	if repo, ok := s.usageBillingRepo.(NativeVideoFinalUsageRepository); ok {
		observed := NativeVideoUsageObservation(r, time.Now().UTC())
		u, err := repo.ResolveNativeVideoFinalUsage(ctx, p, observed)
		if err != nil {
			return err
		}
		r = u.Result(p, p.PricingAt)
	}
	q, err := p.quantity(r)
	if err != nil {
		return err
	}
	amount := func(v decimal.Decimal) float64 { return v.Mul(q).Round(UsageBillingMonetaryScale).InexactFloat64() }
	cost := &CostBreakdown{TotalCost: amount(p.UnitTotal), ActualCost: amount(p.UnitActual), OutputCost: amount(p.UnitOutput), BillingMode: p.BillingMode}
	if cost.TotalCost > 999_999_999_999 || cost.ActualCost > 999_999_999_999 || cost.TotalCost*p.AccountMultiplier > 999_999_999_999 {
		return errors.New("native video amount exceeds ledger limits")
	}
	requestID := StableGrokVideoBillingRequestID(r.ResponseID)
	if requestID == "" {
		return ErrUsageBillingRequestIDRequired
	}
	duration := int(r.Duration.Milliseconds())
	log := &UsageLog{UserID: p.UserID, APIKeyID: p.APIKeyID, AccountID: p.AccountID, GroupID: &p.GroupID, RequestID: requestID,
		Model: p.Model, RequestedModel: p.OriginalModel, UpstreamModel: optionalTrimmedStringPtr(p.UpstreamModel),
		TotalCost: cost.TotalCost, ActualCost: cost.ActualCost, OutputCost: cost.OutputCost, OutputTokens: r.Usage.OutputTokens,
		RateMultiplier: p.Multiplier, AccountRateMultiplier: &p.AccountMultiplier, BillingMode: &cost.BillingMode, DurationMs: &duration, CreatedAt: time.Now(),
		InboundEndpoint: optionalTrimmedStringPtr(in.InboundEndpoint), UpstreamEndpoint: optionalTrimmedStringPtr(in.UpstreamEndpoint),
		UserAgent: optionalTrimmedStringPtr(in.UserAgent), IPAddress: optionalTrimmedStringPtr(in.IPAddress), SessionID: optionalTrimmedStringPtr(in.SessionID)}
	if p.StatsBase != nil {
		statsQ := q
		if p.Protocol == GenerationProtocolArk {
			statsQ = decimal.NewFromInt(int64(r.Usage.OutputTokens))
		}
		value := p.StatsBase.Add(p.StatsUnit.Mul(statsQ)).Round(UsageBillingMonetaryScale).InexactFloat64()
		if value > 999_999_999_999 {
			return errors.New("native video statistics amount exceeds ledger limits")
		}
		log.AccountStatsCost = &value
	}
	if p.Protocol == GenerationProtocolXAI {
		seconds := NormalizeVideoBillingDurationSecondsOrDefault(r.VideoDurationSeconds)
		log.VideoCount, log.VideoDurationSeconds, log.VideoResolution = 1, &seconds, &p.Resolution
	}
	if p.SubscriptionID != nil {
		log.SubscriptionID, log.BillingType = p.SubscriptionID, BillingTypeSubscription
	}
	// Cache/notification work uses captured source and quota policy too. No Key
	// value or account credential is part of the stored financial snapshot.
	key := *in.APIKey
	key.Quota, key.RateLimit5h, key.RateLimit1d, key.RateLimit7d = 0, 0, 0, 0
	if p.KeyRateLimit {
		key.RateLimit5h = 1
	}
	var sub *UserSubscription
	if p.SubscriptionID != nil {
		sub = &UserSubscription{ID: *p.SubscriptionID}
	}
	_, err = applyUsageBilling(ctx, requestID, log, &postUsageBillingParams{Cost: cost, User: in.User, APIKey: &key, Account: in.Account,
		Subscription: sub, IsSubscriptionBill: p.SubscriptionID != nil, AccountRateMultiplier: p.AccountMultiplier,
		Platform: p.QuotaPlatform, APIKeyService: in.APIKeyService, SimpleModeKeyRateLimitOnly: p.Mode == "rate_only",
		RequestPayloadHash: HashUsageRequestPayload([]byte(strings.TrimSpace(r.ResponseID))), NativeVideoBilling: p}, s.billingDeps(), s.usageBillingRepo)
	if err == nil {
		writeUsageLogBestEffort(ctx, s.usageLogRepo, log, "service.native_video")
	}
	return err
}
