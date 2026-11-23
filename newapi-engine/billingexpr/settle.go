// SPDX-License-Identifier: AGPL-3.0-or-later
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f,
// pkg/billingexpr/settle.go and service/tiered_settle.go. See ../NOTICE.md.
package billingexpr

import (
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/shopspring/decimal"
)

// BuildTokenParams constructs TokenParams from a dto.Usage, normalizing P and C
// so they mean "tokens not separately priced by the expression". Sub-categories
// (cache, image, audio) are only subtracted when the expression references them
// via their own variable.
//
// The returned bool reports whether an invalid img_cr breakdown was detected and
// fell back to aggregate cache billing so hosts can log/audit it without global state.
func BuildTokenParams(usage *dto.Usage, isClaudeUsageSemantic bool, usedVars map[string]bool) (TokenParams, bool) {
	if usage == nil {
		return TokenParams{}, false
	}
	p := float64(usage.PromptTokens)
	c := float64(usage.CompletionTokens)
	cr := float64(usage.PromptTokensDetails.CachedTokens)
	cc := float64(usage.PromptTokensDetails.CacheCreationTokensTotal())
	cc1h := float64(0)
	cc12h := float64(0)
	invalidImgCR := false

	if usage.UsageSemantic == "anthropic" {
		// Upstreams may report the cache-write total without a full 5m/1h
		// split; the unlabelled remainder is billed as 5m writes.
		split5m, split1h := relayconvert.NormalizeCacheCreationSplit(
			usage.PromptTokensDetails.CacheCreationTokensTotal(),
			usage.ClaudeCacheCreation5mTokens,
			usage.ClaudeCacheCreation1hTokens,
		)
		cc = float64(split5m)
		cc1h = float64(split1h)
	}

	img := float64(usage.PromptTokensDetails.ImageTokens)
	imgCR := float64(0)
	if usedVars["img_cr"] && !isClaudeUsageSemantic {
		details := usage.PromptTokensDetails.CachedTokensDetails
		if details != nil && details.ImageTokens != nil {
			cachedImage := *details.ImageTokens
			cached := usage.PromptTokensDetails.CachedTokens
			image := usage.PromptTokensDetails.ImageTokens
			valid := cachedImage >= 0 && cached >= cachedImage && image >= cachedImage &&
				cached <= usage.PromptTokens && image <= usage.PromptTokens-(cached-cachedImage)
			if valid {
				remaining := cached - cachedImage
				for _, count := range []*int{details.TextTokens, details.AudioTokens} {
					if count == nil {
						continue
					}
					if *count < 0 || *count > remaining {
						valid = false
						break
					}
					remaining -= *count
				}
			}
			if valid {
				imgCR = float64(cachedImage)
				cr -= imgCR
				img -= imgCR
			} else {
				invalidImgCR = true
			}
		}
	}
	ai := float64(usage.PromptTokensDetails.AudioTokens)
	imgO := float64(usage.CompletionTokenDetails.ImageTokens)
	ao := float64(usage.CompletionTokenDetails.AudioTokens)

	// len = total input context length for tier condition evaluation.
	// Non-Claude: prompt_tokens already includes everything.
	// Claude: input_tokens is text-only, so add cache read + cache creation.
	inputLen := p
	if isClaudeUsageSemantic {
		inputLen = p + cr + cc + cc1h
	}

	if isClaudeUsageSemantic {
		// Anthropic input excludes cache reads and writes. When they have no
		// separate price, merge those tokens into the input category instead.
		if !usedVars["cr"] {
			p += cr
		}
		if !usedVars["cc1h"] {
			cc += cc1h * ClaudeCacheCreation1hMultiplier
			cc1h = 0
		}
		if !usedVars["cc"] {
			p += cc
			cc = 0
		}
	} else {
		if usedVars["cr"] {
			p -= cr
		}
		// prompt_tokens counts every cache write once. A 12h write without
		// its own price joins cc at twice its count; ccTokens keeps the real
		// count that cc takes out of p, and an unpriced cc leaves p the extra.
		cc12h = float64(usage.PromptTokensDetails.CacheWrite12hTokens())
		cc -= cc12h
		ccTokens := cc
		if usedVars["cc12h"] {
			p -= cc12h
		} else {
			ccTokens += cc12h
			cc += cc12h * OpenAICacheCreation12hMultiplier
			cc12h = 0
		}
		if usedVars["cc"] {
			p -= ccTokens
		} else {
			p += cc - ccTokens
			cc = 0
		}
		if usedVars["cc1h"] {
			p -= cc1h
		}
		if usedVars["img"] {
			p -= img
		}
		if usedVars["img_cr"] {
			p -= imgCR
		}
		if usedVars["ai"] {
			p -= ai
		}
		if usedVars["img_o"] {
			c -= imgO
		}
		if usedVars["ao"] {
			c -= ao
		}
	}

	// OpenAI cache-write usage reports unadjusted prefix counts, so cr + cc can
	// exceed the prompt and drive the remainder negative. Clamp at zero.
	if p < 0 {
		p = 0
	}
	if c < 0 {
		c = 0
	}

	return TokenParams{
		P:     p,
		C:     c,
		Len:   inputLen,
		CR:    cr,
		CC:    cc,
		CC1h:  cc1h,
		CC12h: cc12h,
		Img:   img,
		ImgCR: imgCR,
		ImgO:  imgO,
		AI:    ai,
		AO:    ao,
	}, invalidImgCR
}

// quotaConversion converts raw expression output to quota based on the
// expression version and billing mode.
func quotaConversion(exprOutput float64, snap *BillingSnapshot) float64 {
	if snap.TaskUsageBilling {
		return exprOutput * snap.QuotaPerUnit
	}
	switch snap.ExprVersion {
	default: // v1: coefficients are $/1M tokens prices
		return exprOutput / 1_000_000 * snap.QuotaPerUnit
	}
}

// USDFromExprOutput converts raw expression output into a finite, non-negative
// USD decimal without mixing Task USD expressions and relay $/1M-token units.
func USDFromExprOutput(exprOutput float64, snap *BillingSnapshot) (decimal.Decimal, error) {
	if snap == nil {
		return decimal.Zero, errors.New("billing snapshot is required")
	}
	if math.IsNaN(exprOutput) || math.IsInf(exprOutput, 0) || exprOutput < 0 {
		return decimal.Zero, fmt.Errorf("expression output must be finite and non-negative, got %g", exprOutput)
	}
	usd := exprOutput
	if !snap.TaskUsageBilling {
		switch snap.ExprVersion {
		default: // v1: coefficients are $/1M tokens prices
			usd = exprOutput / 1_000_000
		}
	}
	if math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0 {
		return decimal.Zero, fmt.Errorf("converted USD amount must be finite and non-negative, got %g", usd)
	}
	return decimal.NewFromFloat(usd), nil
}

// ComputeTieredQuota runs the Expr from a frozen BillingSnapshot against
// actual token counts and returns the settlement result.
func ComputeTieredQuota(snap *BillingSnapshot, params TokenParams) (TieredResult, error) {
	return ComputeTieredQuotaWithRequest(snap, params, RequestInput{})
}

func ComputeTieredQuotaWithRequest(snap *BillingSnapshot, params TokenParams, request RequestInput) (TieredResult, error) {
	if snap == nil {
		return TieredResult{}, errors.New("billing snapshot is required")
	}
	if snap.TaskUsageBilling && UsesFixedPricingByHash(snap.ExprString, snap.ExprHash) {
		return TieredResult{}, fmt.Errorf("fixed pricing is not supported for task usage expressions")
	}
	cost, trace, err := RunExprByHashWithRequest(snap.ExprString, snap.ExprHash, params, request)
	if err != nil {
		return TieredResult{}, err
	}

	quotaBeforeGroup := quotaConversion(cost, snap)
	afterGroup, clamp := QuotaRoundChecked(quotaBeforeGroup * snap.GroupRatio)
	crossed := trace.MatchedTier != snap.EstimatedTier

	result := TieredResult{
		ImageCount:             trace.ImageCount,
		BillingUnit:            trace.BillingUnit,
		FixedPrice:             trace.FixedPrice,
		ActualQuotaBeforeGroup: quotaBeforeGroup,
		ActualQuotaAfterGroup:  afterGroup,
		MatchedTier:            trace.MatchedTier,
		RequestRules:           trace.RequestRules,
		CrossedTier:            crossed,
		Clamp:                  clamp,
	}
	if trace.BillingUnit == BillingUnitToken && UsedVarsByHash(snap.ExprString, snap.ExprHash)["img_cr"] {
		result.BillingTokens = &params
	}
	return result, nil
}
