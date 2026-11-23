package billingexpr_test

import (
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/Wei-Shaw/sub2api/newapi-engine/billingexpr"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(v int) *int { return &v }

func TestFixedPriceBranchesAndValidation(t *testing.T) {
	const expression = `(len <= 32000 ? tier("short", fixed(0.01)) : tier("long", p * 2 + c * 8)) * (param("fast") == true ? 2 : 1)`
	for _, tc := range []struct {
		name   string
		params billingexpr.TokenParams
		cost   float64
		tier   string
		unit   billingexpr.BillingUnit
	}{
		{"zero usage still charges once", billingexpr.TokenParams{}, 20000, "short", billingexpr.BillingUnitRequest},
		{"fixed branch ignores token prices", billingexpr.TokenParams{P: 32000, C: 9000, Len: 32000}, 20000, "short", billingexpr.BillingUnitRequest},
		{"token branch uses actual usage", billingexpr.TokenParams{P: 32001, C: 100, Len: 32001}, 129604, "long", billingexpr.BillingUnitToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost, trace, err := billingexpr.RunExprWithRequest(expression, tc.params, billingexpr.RequestInput{Body: []byte(`{"fast":true}`)})
			require.NoError(t, err)
			assert.Equal(t, tc.cost, cost)
			assert.Equal(t, tc.tier, trace.MatchedTier)
			assert.Equal(t, tc.unit, trace.BillingUnit)
			require.Len(t, trace.RequestRules, 1)
			assert.True(t, trace.RequestRules[0].Matched)
		})
	}

	cost, _, err := billingexpr.RunExpr(`tier("free", fixed(0))`, billingexpr.TokenParams{P: 1000})
	require.NoError(t, err)
	assert.Zero(t, cost)

	for _, count := range []int{-1, 0, 129} {
		_, _, err := billingexpr.RunExprWithRequest(`tier("image", fixed(0.04)) * image_count`, billingexpr.TokenParams{}, billingexpr.RequestInput{ImageCount: intPtr(count)})
		require.ErrorContains(t, err, "image_count")
	}

	cost, trace, err := billingexpr.RunExprWithRequest(`tier("image", fixed(0.04)) * image_count`, billingexpr.TokenParams{}, billingexpr.RequestInput{ImageCount: intPtr(3)})
	require.NoError(t, err)
	assert.InDelta(t, 120000, cost, 1e-6)
	require.NotNil(t, trace.ImageCount)
	assert.Equal(t, 3, *trace.ImageCount)

	for _, invalid := range []string{
		`true ? tier("ok", p) : tier("bad", fixed(-0.01))`,
		`tier("bad", fixed(p))`,
		`tier("bad", fixed(0.01 + 0.02))`,
		`tier("bad", fixed(1e308))`,
		`tier("bad", fixed(0.01) + p * 2)`,
		`tier("bad", p * fixed(0.01))`,
		`tier("a", fixed(0.01)) + tier("b", p * 2)`,
		`fixed(0.01)`,
		`tier("bad", fixed(0.01)) * p`,
		`tier("bad", fixed(0.01)) * (param("fast") == true ? -2 : 1)`,
		`_trace(0, true, 2.0)`,
		`_trace_int(0, true, 2)`,
	} {
		t.Run("reject/"+invalid, func(t *testing.T) {
			_, err := billingexpr.CompileFromCache(invalid)
			require.Error(t, err)
		})
	}
}

func TestFixedPriceQuotaBoundariesAndTaskRejection(t *testing.T) {
	for _, tc := range []struct {
		name, expression string
		group            float64
		want             int
		clamped          bool
	}{
		{"normal group", `tier("request", fixed(0.01))`, 1.5, 7500, false},
		{"free group", `tier("request", fixed(0.01))`, 0, 0, false},
		{"explicit free price", `tier("free", fixed(0))`, 1, 0, false},
		{"oversized saturates", `tier("huge", fixed(1e20))`, 1, billingexpr.MaxQuota, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := &billingexpr.BillingSnapshot{ExprString: tc.expression, ExprHash: billingexpr.ExprHashString(tc.expression), GroupRatio: tc.group, QuotaPerUnit: billingexpr.DefaultQuotaPerUnit}
			result, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{})
			require.NoError(t, err)
			assert.Equal(t, tc.want, result.ActualQuotaAfterGroup)
			assert.Equal(t, billingexpr.BillingUnitRequest, result.BillingUnit)
			assert.Equal(t, tc.clamped, result.Clamp != nil)
			_, err = billingexpr.QuotaRoundStrict(result.ActualQuotaBeforeGroup * tc.group)
			if tc.clamped {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			snap.TaskUsageBilling = true
			_, err = billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{})
			require.ErrorContains(t, err, "task usage")
		})
	}
	const optimized = `true ? tier("tokens", p) : tier("request", fixed(0.01))`
	assert.True(t, billingexpr.UsesFixedPricing(optimized))
	_, trace, err := billingexpr.RunExpr(optimized, billingexpr.TokenParams{P: 10})
	require.NoError(t, err)
	assert.Equal(t, billingexpr.BillingUnitToken, trace.BillingUnit)
	assert.Nil(t, trace.FixedPrice)
}

func TestTieredSettlementAndClampAuditing(t *testing.T) {
	exprStr := `tier("base", p * 1000000000000)`
	snap := &billingexpr.BillingSnapshot{
		BillingMode:  "tiered_expr",
		ExprString:   exprStr,
		ExprHash:     billingexpr.ExprHashString(exprStr),
		GroupRatio:   1.0,
		QuotaPerUnit: billingexpr.DefaultQuotaPerUnit,
	}
	result, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 1_000_000_000})
	require.NoError(t, err)
	assert.Equal(t, billingexpr.MaxQuota, result.ActualQuotaAfterGroup)
	require.NotNil(t, result.Clamp)
	assert.Equal(t, billingexpr.QuotaClampOverflow, result.Clamp.Kind)
	assert.Equal(t, billingexpr.MaxQuota, result.Clamp.Clamped)
	assert.Equal(t, "QuotaRound", result.Clamp.AuditMap()["op"])

	normalExpr := `v1:len <= 200000 ? tier("standard", p * 1.5 + c * 7.5) : tier("long_context", p * 3.0 + c * 11.25)`
	normalSnap := &billingexpr.BillingSnapshot{
		BillingMode:   "tiered_expr",
		ExprString:    normalExpr,
		ExprHash:      billingexpr.ExprHashString(normalExpr),
		GroupRatio:    1.0,
		QuotaPerUnit:  billingexpr.DefaultQuotaPerUnit,
		EstimatedTier: "standard",
		ExprVersion:   1,
	}
	res, err := billingexpr.ComputeTieredQuota(normalSnap, billingexpr.TokenParams{P: 250000, C: 1000, Len: 250000})
	require.NoError(t, err)
	assert.Nil(t, res.Clamp)
	assert.Equal(t, "long_context", res.MatchedTier)
	assert.True(t, res.CrossedTier)
	// (250000*3 + 1000*11.25) / 1e6 * 500000 = 761250 * 0.5 = 380625
	assert.Equal(t, 380625, res.ActualQuotaAfterGroup)

	usd, err := billingexpr.USDFromExprOutput(761250, normalSnap)
	require.NoError(t, err)
	assert.Equal(t, "0.76125", usd.String())

	taskSnap := &billingexpr.BillingSnapshot{TaskUsageBilling: true, ExprVersion: 1, QuotaPerUnit: billingexpr.DefaultQuotaPerUnit}
	taskUSD, err := billingexpr.USDFromExprOutput(4.25, taskSnap)
	require.NoError(t, err)
	assert.Equal(t, "4.25", taskUSD.String())

	for _, bad := range []float64{-0.1, math.NaN(), math.Inf(1)} {
		_, err := billingexpr.USDFromExprOutput(bad, taskSnap)
		require.Error(t, err)
	}
}

func TestTaskUsageAndLiteralInspectors(t *testing.T) {
	expression := `tier("1080p", u("seconds") * (u("resolution") == "1080p" ? 0.4 : 0.2)) * (header("X-Tier") == "priority" ? 1.5 : 1) * (param("audio.hq") == true ? 2 : 1)`
	assert.False(t, billingexpr.UsesChatQuantities(expression))
	assert.True(t, billingexpr.UsesChatQuantities(`p * 2 + c * 4`))
	assert.Equal(t, map[string]bool{"seconds": true, "resolution": true}, billingexpr.UsedUsageKeys(expression))
	assert.Equal(t, map[string]bool{"audio.hq": true}, billingexpr.UsedParamPaths(expression))
	assert.Equal(t, map[string]bool{"x-tier": true}, billingexpr.UsedHeaderNames(expression))

	snap := &billingexpr.BillingSnapshot{
		ExprString:       expression,
		ExprHash:         billingexpr.ExprHashString(expression),
		GroupRatio:       2,
		QuotaPerUnit:     billingexpr.DefaultQuotaPerUnit,
		ExprVersion:      1,
		TaskUsageBilling: true,
	}
	result, err := billingexpr.ComputeTieredQuotaWithRequest(snap, billingexpr.TokenParams{}, billingexpr.RequestInput{
		Usage:   map[string]any{"seconds": 10.0, "resolution": "1080p"},
		Headers: map[string]string{"x-tier": "priority"},
		Body:    []byte(`{"audio":{"hq":true}}`),
	})
	require.NoError(t, err)
	// 10 * 0.4 * 1.5 * 2 = 12 USD; * 500000 quota/USD * 2 groupRatio = 12,000,000
	assert.Equal(t, 12_000_000, result.ActualQuotaAfterGroup)
	require.Len(t, result.RequestRules, 2)
	assert.True(t, result.RequestRules[0].Matched)
	assert.True(t, result.RequestRules[1].Matched)
}

func TestDeterministicTimeProbesAndSensitiveHeaderRejection(t *testing.T) {
	instant := time.Date(2026, time.November, 21, 23, 59, 0, 0, time.UTC)
	exprStr := `(hour("Asia/Shanghai") == 7 && minute("UTC") == 59 && weekday("Asia/Shanghai") == 0 && day("Asia/Shanghai") == 22 && month("UTC") == 11 ? 2 : 1) * tier("base", p * 1)`
	cost, trace, err := billingexpr.RunExprWithRequest(exprStr, billingexpr.TokenParams{P: 100}, billingexpr.RequestInput{Now: &instant})
	require.NoError(t, err)
	assert.Equal(t, 200.0, cost)
	require.Len(t, trace.RequestRules, 1)
	assert.True(t, trace.RequestRules[0].Matched)

	// Sensitive header filtering when requested by host.
	hdrExpr := `(has(header("Authorization"), "Bearer") ? 5 : 1) * (header("X-Service-Tier") == "flex" ? 0.5 : 1) * tier("base", p * 1)`
	cost, _, err = billingexpr.RunExprWithRequest(hdrExpr, billingexpr.TokenParams{P: 100}, billingexpr.RequestInput{
		Headers:                map[string]string{"Authorization": "Bearer secret", "X-Service-Tier": "flex"},
		RejectSensitiveHeaders: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 50.0, cost, "Authorization must be invisible when RejectSensitiveHeaders is enabled")
}

func TestBuildTokenParamsSubCategoryExclusion(t *testing.T) {
	t.Run("non-claude with separate cr, cc12h, img, img_cr, ai, img_o, ao", func(t *testing.T) {
		usage := &dto.Usage{
			PromptTokens:     1000,
			CompletionTokens: 500,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens: 300,
				CachedTokensDetails: &dto.CachedTokenDetails{
					ImageTokens: intPtr(100),
					TextTokens:  intPtr(200),
				},
				CacheWriteTokens: 150,
				CacheWriteTokensDetails: &dto.CacheWriteTokenDetails{
					TTL30mTokens: 50,
					TTL12hTokens: 100,
				},
				ImageTokens: 250,
				AudioTokens: 50,
			},
			CompletionTokenDetails: dto.OutputTokenDetails{
				ImageTokens: 100,
				AudioTokens: 80,
			},
		}
		used := billingexpr.UsedVars(`tier("t", p * 1 + cr * 0.1 + cc * 1.2 + cc12h * 2 + img * 3 + img_cr * 0.3 + ai * 4 + c * 5 + img_o * 6 + ao * 7)`)
		params, invalid := billingexpr.BuildTokenParams(usage, false, used)
		assert.False(t, invalid)
		// img_cr=100 carved out of cr(300->200) and img(250->150)
		// p = 1000 - cr(200) - cc12h(100) - cc(50) - img(150) - img_cr(100) - ai(50) = 350
		assert.Equal(t, 350.0, params.P)
		assert.Equal(t, 320.0, params.C) // 500 - 100 - 80
		assert.Equal(t, 1000.0, params.Len)
		assert.Equal(t, 200.0, params.CR)
		assert.Equal(t, 50.0, params.CC)
		assert.Equal(t, 100.0, params.CC12h)
		assert.Equal(t, 150.0, params.Img)
		assert.Equal(t, 100.0, params.ImgCR)
		assert.Equal(t, 50.0, params.AI)
		assert.Equal(t, 100.0, params.ImgO)
		assert.Equal(t, 80.0, params.AO)
	})

	t.Run("non-claude invalid img_cr falls back cleanly", func(t *testing.T) {
		usage := &dto.Usage{
			PromptTokens: 200,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens: 100,
				ImageTokens:  50,
				CachedTokensDetails: &dto.CachedTokenDetails{
					ImageTokens: intPtr(80), // exceeds ImageTokens
				},
			},
		}
		used := map[string]bool{"p": true, "cr": true, "img": true, "img_cr": true}
		params, invalid := billingexpr.BuildTokenParams(usage, false, used)
		assert.True(t, invalid)
		assert.Zero(t, params.ImgCR)
		assert.Equal(t, 100.0, params.CR)
		assert.Equal(t, 50.0, params.Img)
		assert.Equal(t, 50.0, params.P)
	})

	t.Run("non-claude unpriced cc12h folds into cc at 2x or p", func(t *testing.T) {
		usage := &dto.Usage{
			PromptTokens: 500,
			PromptTokensDetails: dto.InputTokenDetails{
				CacheWriteTokens: 150,
				CacheWriteTokensDetails: &dto.CacheWriteTokenDetails{
					TTL30mTokens: 50,
					TTL12hTokens: 100,
				},
			},
		}
		// Expression prices cc but not cc12h: cc = 50 + 100*2 = 250, p = 500 - 150 = 350.
		params, _ := billingexpr.BuildTokenParams(usage, false, map[string]bool{"p": true, "cc": true})
		assert.Equal(t, 350.0, params.P)
		assert.Equal(t, 250.0, params.CC)
		assert.Zero(t, params.CC12h)

		// Expression prices neither cc nor cc12h: extra 100 from 2x-1x stays in p => 600.
		params, _ = billingexpr.BuildTokenParams(usage, false, map[string]bool{"p": true})
		assert.Equal(t, 600.0, params.P)
		assert.Zero(t, params.CC)
	})

	t.Run("claude semantics with 5m/1h cache split and fallback", func(t *testing.T) {
		usage := &dto.Usage{
			UsageSemantic:               "anthropic",
			PromptTokens:                1000,
			CompletionTokens:            200,
			PromptTokensDetails:         dto.InputTokenDetails{CachedTokens: 400, CachedCreationTokens: 300},
			ClaudeCacheCreation5mTokens: 100,
			ClaudeCacheCreation1hTokens: 200,
		}
		// With cr, cc, cc1h all priced:
		params, invalid := billingexpr.BuildTokenParams(usage, true, map[string]bool{"p": true, "c": true, "cr": true, "cc": true, "cc1h": true})
		assert.False(t, invalid)
		assert.Equal(t, 1000.0, params.P)
		assert.Equal(t, 1700.0, params.Len) // 1000 + 400 + 100 + 200
		assert.Equal(t, 400.0, params.CR)
		assert.Equal(t, 100.0, params.CC)
		assert.Equal(t, 200.0, params.CC1h)

		// Without cc1h: 200 * (6/3.75) = 320 folds into cc => 420.
		params, _ = billingexpr.BuildTokenParams(usage, true, map[string]bool{"p": true, "c": true, "cr": true, "cc": true})
		assert.Equal(t, 1000.0, params.P)
		assert.InDelta(t, 420.0, params.CC, 1e-9)
		assert.Zero(t, params.CC1h)

		// Without cr, cc, cc1h: all fold into p => 1000 + 400 + 420 = 1820.
		params, _ = billingexpr.BuildTokenParams(usage, true, map[string]bool{"p": true, "c": true})
		assert.InDelta(t, 1820.0, params.P, 1e-9)
		assert.Zero(t, params.CC)
		assert.Zero(t, params.CC1h)
	})
}

func TestQuotaMathHelpers(t *testing.T) {
	assert.Equal(t, 3, billingexpr.QuotaRound(2.5))
	assert.Equal(t, -3, billingexpr.QuotaRound(-2.5))
	q, clamp := billingexpr.QuotaRoundChecked(math.NaN())
	assert.Equal(t, 0, q)
	require.NotNil(t, clamp)
	assert.Equal(t, billingexpr.QuotaClampNaN, clamp.Kind)

	q, clamp = billingexpr.QuotaFromFloatChecked(-1e20)
	assert.Equal(t, billingexpr.MinQuota, q)
	require.NotNil(t, clamp)
	assert.Equal(t, billingexpr.QuotaClampUnderflow, clamp.Kind)

	dq, err := billingexpr.QuotaFromDecimalStrict(decimal.RequireFromString("123.5"))
	require.NoError(t, err)
	assert.Equal(t, 124, dq)

	wq, err := billingexpr.WalletQuotaFromDecimalStrict(decimal.NewFromInt(billingexpr.MaxWalletQuota))
	require.NoError(t, err)
	assert.Equal(t, billingexpr.MaxWalletQuota, wq)
	require.Error(t, billingexpr.ValidateWalletQuota(billingexpr.MaxWalletQuota+1))
}
