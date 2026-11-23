//go:build unit

package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func nativePriceFixture(t *testing.T, protocol string) (*OpenAIGatewayService, *APIKey, *Account, *openAIRecordUsageBillingRepoStub, *openAIRecordUsageLogRepoStub) {
	t.Helper()
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	billing := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: false}}
	s := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	price, accountRate := 0.037, 0.5
	key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, GroupID: i64p(3), Quota: 100, RateLimit5h: 20,
		Group: &Group{ID: 3, Platform: PlatformGrok, RateMultiplier: 2, VideoPrice720P: &price, VideoRateMultiplier: 1, ImageRateMultiplier: 1}}
	account := &Account{ID: 4, Type: AccountTypeAPIKey, Platform: PlatformGrok, RateMultiplier: &accountRate,
		Credentials: map[string]any{"api_key": "private-fixture-key"}, Extra: map[string]any{"quota_limit": float64(100)}}
	if protocol == GenerationProtocolArk {
		output := 10.0 / 1_000_000 // Stored as USD per token.
		key.Group.Platform, account.Platform = PlatformOpenAI, PlatformOpenAI
		key.Group.ModelPricing = []ChannelModelPricing{{Models: []string{"video-model"}, BillingMode: BillingModeToken, OutputPrice: &output}}
		s.resolver = NewModelPricingResolver(nil, s.billingService)
	}
	return s, key, account, billing, logs
}

func captureNativePrice(t *testing.T, s *OpenAIGatewayService, key *APIKey, account *Account, sub *UserSubscription, protocol string) *NativeVideoBillingSnapshot {
	t.Helper()
	p, err := s.CaptureNativeVideoBilling(context.Background(), key, account, sub, protocol, "video-model", "public-model", "720p", account.Platform, time.Now().UTC())
	require.NoError(t, err)
	p.TaskID = "fixture-task"
	if protocol == GenerationProtocolArk {
		p.TaskID = "seedance:" + p.TaskID
	}
	encoded, err := json.Marshal(p)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-fixture-key")
	var restored NativeVideoBillingSnapshot
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.NoError(t, restored.Validate())
	return &restored
}

func nativePriceResult(p *NativeVideoBillingSnapshot) *OpenAIForwardResult {
	r := &OpenAIForwardResult{Model: "upstream-changed-model", ResponseID: p.TaskID, NativeVideoBilling: p, Duration: 30 * time.Second}
	if p.Protocol == GenerationProtocolArk {
		r.Usage.OutputTokens = 100_000
	} else {
		r.VideoCount, r.VideoDurationSeconds, r.VideoResolution = 1, 8, "720p"
	}
	return r
}

func TestNativeVideoBillingFreezesPriceAndSource(t *testing.T) {
	for _, protocol := range []string{GenerationProtocolXAI, GenerationProtocolArk} {
		for _, subscription := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/balance", true: "/subscription"}[subscription], func(t *testing.T) {
				s, key, account, billing, logs := nativePriceFixture(t, protocol)
				var sub *UserSubscription
				if subscription {
					key.Group.SubscriptionType = "subscription"
					sub = &UserSubscription{ID: 9, UserID: key.UserID, GroupID: *key.GroupID}
				}
				p := captureNativePrice(t, s, key, account, sub, protocol)
				require.Equal(t, subscription, p.SubscriptionID != nil)
				// Current prices, source, multipliers, quota switches and even the
				// resolver can change without repricing this accepted task.
				key.Group.RateMultiplier, key.Group.VideoRateMultiplier = 90, 80
				key.Group.VideoRateIndependent = true
				*key.Group.VideoPrice720P = 9
				key.Group.ModelPricing = nil
				key.Group.SubscriptionType = "standard"
				key.Quota, key.RateLimit5h = 0, 0
				*account.RateMultiplier = 20
				account.Extra = nil
				s.resolver, s.billingService = nil, nil
				s.cfg.RunMode = config.RunModeSimple
				r := nativePriceResult(p)
				err := s.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: r, APIKey: key, User: key.User, Account: account, Subscription: &UserSubscription{ID: 99}})
				require.NoError(t, err)
				want := 0.592 // 8 seconds * .037 * 2.
				if protocol == GenerationProtocolArk {
					want = 2
				} // 100k * $10/M * 2.
				cmd := billing.lastCmd
				require.NotNil(t, cmd)
				require.Equal(t, StableGrokVideoBillingRequestID(p.TaskID), cmd.RequestID)
				require.Equal(t, "video-model", cmd.Model)
				require.InDelta(t, want, cmd.APIKeyQuotaCost, 1e-12)
				require.Equal(t, cmd.APIKeyQuotaCost, cmd.APIKeyRateLimitCost)
				require.InDelta(t, want/4, cmd.AccountQuotaCost, 1e-12)
				if subscription {
					require.EqualValues(t, 9, *cmd.SubscriptionID)
					require.InDelta(t, want, cmd.SubscriptionCost, 1e-12)
					require.Zero(t, cmd.BalanceCost)
				} else {
					require.Nil(t, cmd.SubscriptionID)
					require.InDelta(t, want, cmd.BalanceCost, 1e-12)
					require.Zero(t, cmd.SubscriptionCost)
				}
				require.InDelta(t, want, logs.lastLog.ActualCost, 1e-12)
				require.Equal(t, "public-model", logs.lastLog.RequestedModel)
				// A status/content replay has the same durable command even if a
				// different request-local body fingerprint was provided.
				fingerprint := cmd.RequestFingerprint
				require.NoError(t, s.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: r, APIKey: key, User: key.User, Account: account, RequestPayloadHash: "different-client-query"}))
				require.Equal(t, fingerprint, billing.lastCmd.RequestFingerprint)
			})
		}
	}
}

func TestNativeVideoBillingModesAndFreePricing(t *testing.T) {
	for _, mode := range []string{"standard", "log_only", "rate_only", "free"} {
		t.Run(mode, func(t *testing.T) {
			s, key, account, billing, logs := nativePriceFixture(t, GenerationProtocolXAI)
			if mode == "log_only" || mode == "rate_only" {
				s.cfg.RunMode = config.RunModeSimple
				s.cfg.SimpleModeKeyRateLimitEnabled = mode == "rate_only"
			}
			if mode == "free" {
				*key.Group.VideoPrice720P = 0
			}
			p := captureNativePrice(t, s, key, account, nil, GenerationProtocolXAI)
			s.cfg.RunMode = "standard"
			s.cfg.SimpleModeKeyRateLimitEnabled = false
			require.NoError(t, s.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: nativePriceResult(p), APIKey: key, User: key.User, Account: account}))
			require.NotNil(t, logs.lastLog)
			if mode == "log_only" {
				require.Equal(t, 1, billing.calls)
				require.True(t, billing.lastCmd.NativeVideoLogOnly)
				require.Zero(t, billing.lastCmd.BalanceCost)
				require.Zero(t, billing.lastCmd.SubscriptionCost)
				return
			}
			require.Equal(t, 1, billing.calls)
			if mode == "rate_only" {
				require.Positive(t, billing.lastCmd.APIKeyRateLimitCost)
				require.Zero(t, billing.lastCmd.BalanceCost)
				require.Zero(t, billing.lastCmd.AccountQuotaCost)
				require.Zero(t, billing.lastCmd.APIKeyQuotaCost)
			}
			if mode == "free" {
				require.Zero(t, billing.lastCmd.BalanceCost)
				require.Zero(t, logs.lastLog.ActualCost)
			}
		})
	}
}

func TestNativeVideoBillingChannelPerRequestAndGroupPriority(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "channel request", true: "group seconds"}[override], func(t *testing.T) {
			s, key, account, billing, _ := nativePriceFixture(t, GenerationProtocolXAI)
			s.resolver = newOpenAIImageChannelPricingResolverForTest(t, *key.GroupID, "video-model", 0.2)
			if !override {
				key.Group.VideoPrice720P = nil
			}
			p := captureNativePrice(t, s, key, account, nil, GenerationProtocolXAI)
			require.NoError(t, s.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: nativePriceResult(p), APIKey: key, User: key.User, Account: account}))
			if override {
				require.Equal(t, "seconds", p.Unit)
				require.InDelta(t, 0.592, billing.lastCmd.BalanceCost, 1e-12)
			} else {
				require.Equal(t, "request", p.Unit)
				require.InDelta(t, 0.4, billing.lastCmd.BalanceCost, 1e-12)
			}
		})
	}
}

func TestNativeVideoBillingRejectsCorruptOrMismatchedEvidence(t *testing.T) {
	for _, scenario := range []string{"version", "negative", "exponent", "nan", "user", "key user", "key", "group", "account", "task", "protocol", "unit", "billing mode", "resolution", "missing completion", "missing ledger", "subscription", "stats", "cache input"} {
		t.Run(scenario, func(t *testing.T) {
			protocol := GenerationProtocolXAI
			if scenario == "cache input" {
				protocol = GenerationProtocolArk
			}
			s, key, account, billing, _ := nativePriceFixture(t, protocol)
			p := captureNativePrice(t, s, key, account, nil, protocol)
			r := nativePriceResult(p)
			switch scenario {
			case "version":
				p.Version = 99
			case "negative":
				p.UnitActual = decimal.NewFromInt(-1)
			case "exponent":
				p.UnitActual = decimal.New(1, math.MaxInt32)
			case "nan":
				p.Multiplier = math.NaN()
			case "user":
				p.UserID++
			case "key user":
				key.UserID++
			case "key":
				p.APIKeyID++
			case "group":
				p.GroupID++
			case "account":
				p.AccountID++
			case "task":
				p.TaskID = "different-task"
			case "protocol":
				p.Protocol = "invented"
			case "unit":
				p.Unit = "output_tokens"
			case "billing mode":
				p.BillingMode = "invented"
			case "resolution":
				r.VideoResolution = "1080p"
			case "missing completion":
				r.VideoCount = 0
			case "missing ledger":
				s.usageBillingRepo = nil
			case "subscription":
				p.SubscriptionID = i64p(-1)
			case "stats":
				v := decimal.NewFromInt(-1)
				p.StatsBase, p.StatsUnit = &v, &v
			case "cache input":
				r.Usage.InputTokens = 1
			}
			require.Error(t, s.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: r, APIKey: key, User: key.User, Account: account}))
			require.Zero(t, billing.calls)
		})
	}
}

func TestNativeVideoBillingRoundsAfterMultiplyingActualTokens(t *testing.T) {
	s, key, account, billing, _ := nativePriceFixture(t, GenerationProtocolArk)
	output := 0.000000004
	key.Group.ModelPricing[0].OutputPrice = &output
	p := captureNativePrice(t, s, key, account, nil, GenerationProtocolArk)
	r := nativePriceResult(p)
	r.Usage.OutputTokens = 3
	require.NoError(t, s.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: r, APIKey: key, User: key.User, Account: account}))
	require.Equal(t, 0.00000002, billing.lastCmd.BalanceCost, "quantize the final .000000024, not each token")
}

func TestNativeVideoBillingStatisticsStayIndependentOfCustomerUnits(t *testing.T) {
	for _, customerRequest := range []bool{false, true} {
		for _, statsRequest := range []bool{false, true} {
			t.Run(map[bool]string{false: "customer tokens/", true: "customer request/"}[customerRequest]+map[bool]string{false: "stats tokens", true: "stats request"}[statsRequest], func(t *testing.T) {
				s, key, account, _, logs := nativePriceFixture(t, GenerationProtocolArk)
				perRequest, output := .3, .000003
				pricing := ChannelModelPricing{Models: []string{"video-model"}, OutputPrice: &output, BillingMode: BillingModeToken}
				if statsRequest {
					pricing.BillingMode, pricing.PerRequestPrice = BillingModePerRequest, &perRequest
				}
				channel := &Channel{ID: 1, Status: StatusActive, AccountStatsPricingRules: []AccountStatsPricingRule{{GroupIDs: []int64{*key.GroupID}, Pricing: []ChannelModelPricing{pricing}},
					// At zero output, a token rule falls through to this source.
					{GroupIDs: []int64{*key.GroupID}, Pricing: []ChannelModelPricing{{Models: []string{"video-model"}, BillingMode: BillingModePerRequest, PerRequestPrice: &perRequest}}}}}
				s.channelService = newTestChannelServiceForStats(t, channel, *key.GroupID, PlatformOpenAI)
				if customerRequest {
					key.Group.ModelPricing[0].BillingMode, key.Group.ModelPricing[0].PerRequestPrice = BillingModePerRequest, &perRequest
				}
				p := captureNativePrice(t, s, key, account, nil, GenerationProtocolArk)
				s.channelService, s.billingService, s.resolver = nil, nil, nil
				require.NoError(t, s.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: nativePriceResult(p), APIKey: key, User: key.User, Account: account}))
				require.NotNil(t, logs.lastLog.AccountStatsCost)
				require.InDelta(t, .3, *logs.lastLog.AccountStatsCost, 1e-12)
			})
		}
	}
}

func TestNativeVideoBillingDeclinesUnsupportedCapture(t *testing.T) {
	for _, scenario := range []string{"cancelled", "missing price", "xai token", "tiered stats", "foreign subscription"} {
		t.Run(scenario, func(t *testing.T) {
			protocol := GenerationProtocolArk
			if scenario == "xai token" {
				protocol = GenerationProtocolXAI
			}
			s, key, account, billing, _ := nativePriceFixture(t, protocol)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var sub *UserSubscription
			switch scenario {
			case "cancelled":
				cancel()
			case "missing price":
				key.Group.ModelPricing = nil
			case "xai token":
				output := .00001
				key.Group.ModelPricing = []ChannelModelPricing{{Models: []string{"video-model"}, BillingMode: BillingModeToken, OutputPrice: &output}}
				s.resolver = NewModelPricingResolver(nil, s.billingService)
			case "tiered stats":
				output := .00001
				channel := &Channel{ID: 1, Status: StatusActive, AccountStatsPricingRules: []AccountStatsPricingRule{{GroupIDs: []int64{*key.GroupID}, Pricing: []ChannelModelPricing{{Models: []string{"video-model"}, Intervals: []PricingInterval{{MinTokens: 100, OutputPrice: &output}}}}}}}
				s.channelService = newTestChannelServiceForStats(t, channel, *key.GroupID, PlatformOpenAI)
			case "foreign subscription":
				key.Group.SubscriptionType = SubscriptionTypeSubscription
				sub = &UserSubscription{ID: 9, UserID: 99, GroupID: *key.GroupID}
			}
			p, err := s.CaptureNativeVideoBilling(ctx, key, account, sub, protocol, "video-model", "public-model", "720p", account.Platform, time.Now().UTC())
			require.Error(t, err)
			require.Nil(t, p)
			require.Zero(t, billing.calls, "capture never reserves or charges funds")
		})
	}
}
