// SPDX-License-Identifier: AGPL-3.0-or-later
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f,
// pkg/billingexpr/round.go and common/quota_math.go. See ../NOTICE.md.
package billingexpr

import (
	"fmt"
	"math"

	"github.com/shopspring/decimal"
)

// Single-request charges stay bounded to int32; wallet-priced purchases use a
// JavaScript-safe 64-bit domain. Cache creation fallback multipliers match the
// pinned baseline types/price_data.go.
const (
	MaxQuota                         = math.MaxInt32
	MinQuota                         = math.MinInt32
	MaxWalletQuota                   = 1<<53 - 1
	DefaultQuotaPerUnit              = 500_000
	ClaudeCacheCreation1hMultiplier  = 6 / 3.75
	OpenAICacheCreation12hMultiplier = 2
)

// ValidateWalletQuota enforces the upper bound shared by wallet mutations.
func ValidateWalletQuota(quota int) error {
	if quota > MaxWalletQuota {
		return fmt.Errorf("wallet quota exceeds %d", MaxWalletQuota)
	}
	return nil
}

// QuotaClampKind identifies why a quota conversion had to be saturated.
type QuotaClampKind string

const (
	QuotaClampOverflow  QuotaClampKind = "overflow"
	QuotaClampUnderflow QuotaClampKind = "underflow"
	QuotaClampNaN       QuotaClampKind = "nan"
)

// QuotaClamp describes a single saturation event: a quota conversion whose
// input fell outside its supported range (or was NaN) and was clamped.
type QuotaClamp struct {
	Op       string         `json:"op"`
	Kind     QuotaClampKind `json:"kind"`
	Original float64        `json:"original"`
	Clamped  int            `json:"clamped"`
}

func (c *QuotaClamp) Error() string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("quota conversion (%s) %s: original=%g, clamped=%d", c.Op, c.Kind, c.Original, c.Clamped)
}

// AuditMap renders the clamp in the exact admin_info.quota_saturation shape.
func (c *QuotaClamp) AuditMap() map[string]any {
	if c == nil {
		return nil
	}
	return map[string]any{
		"op":       c.Op,
		"kind":     c.Kind,
		"original": c.Original,
		"clamped":  c.Clamped,
	}
}

func saturateQuota(value float64, op string) (int, *QuotaClamp) {
	return saturateQuotaBounded(value, op, MaxQuota, MinQuota)
}

func saturateQuotaBounded(value float64, op string, maxQuota int, minQuota int) (int, *QuotaClamp) {
	switch {
	case math.IsNaN(value):
		clamp := &QuotaClamp{Op: op, Kind: QuotaClampNaN, Original: value, Clamped: 0}
		return clamp.Clamped, clamp
	case value > float64(maxQuota):
		clamp := &QuotaClamp{Op: op, Kind: QuotaClampOverflow, Original: value, Clamped: maxQuota}
		return clamp.Clamped, clamp
	case value < float64(minQuota):
		clamp := &QuotaClamp{Op: op, Kind: QuotaClampUnderflow, Original: value, Clamped: minQuota}
		return clamp.Clamped, clamp
	default:
		return int(value), nil
	}
}

func strictQuota(quota int, clamp *QuotaClamp) (int, error) {
	if clamp != nil {
		return 0, clamp
	}
	return quota, nil
}

func QuotaFromFloat(value float64) int {
	quota, _ := QuotaFromFloatChecked(value)
	return quota
}

func QuotaFromFloatChecked(value float64) (int, *QuotaClamp) {
	return saturateQuota(value, "QuotaFromFloat")
}

func QuotaFromFloatStrict(value float64) (int, error) {
	return strictQuota(QuotaFromFloatChecked(value))
}

// QuotaRound converts a float64 quota value to int using half-away-from-zero
// rounding with int32 saturation.
func QuotaRound(f float64) int {
	quota, _ := QuotaRoundChecked(f)
	return quota
}

func QuotaRoundChecked(f float64) (int, *QuotaClamp) {
	return saturateQuota(math.Round(f), "QuotaRound")
}

// QuotaRoundStrict rejects an unrepresentable pre-consume estimate.
func QuotaRoundStrict(f float64) (int, error) {
	return strictQuota(QuotaRoundChecked(f))
}

func QuotaFromDecimal(d decimal.Decimal) int {
	quota, _ := QuotaFromDecimalChecked(d)
	return quota
}

func QuotaFromDecimalChecked(d decimal.Decimal) (int, *QuotaClamp) {
	f, _ := d.Round(0).Float64()
	return saturateQuota(f, "QuotaFromDecimal")
}

func QuotaFromDecimalStrict(d decimal.Decimal) (int, error) {
	return strictQuota(QuotaFromDecimalChecked(d))
}

func WalletQuotaFromDecimalStrict(d decimal.Decimal) (int, error) {
	f, _ := d.Round(0).Float64()
	return strictQuota(saturateQuotaBounded(f, "WalletQuotaFromDecimal", MaxWalletQuota, -MaxWalletQuota))
}
