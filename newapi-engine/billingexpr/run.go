// SPDX-License-Identifier: AGPL-3.0-or-later
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f,
// pkg/billingexpr/run.go. See ../NOTICE.md.
package billingexpr

import (
	"fmt"
	"math"
	"strings"
	"time"
	_ "time/tzdata" // Ensure IANA time zones work in minimal container images.

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/tidwall/gjson"
)

// RunExpr compiles (cached) and evaluates a pricing expression against token counts.
func RunExpr(exprStr string, params TokenParams) (float64, TraceResult, error) {
	return RunExprWithRequest(exprStr, params, RequestInput{})
}

func RunExprWithRequest(exprStr string, params TokenParams, request RequestInput) (float64, TraceResult, error) {
	entry, err := compileEntryFromCacheByHash(exprStr, ExprHashString(exprStr))
	if err != nil {
		return 0, TraceResult{}, err
	}
	return runProgram(entry.prog, entry.requestRules, entry.usedVars, params, request)
}

// RunExprByHash is like RunExpr but accepts a pre-computed hash for the cache
// lookup, avoiding a redundant SHA-256 computation when the caller already
// holds BillingSnapshot.ExprHash.
func RunExprByHash(exprStr, hash string, params TokenParams) (float64, TraceResult, error) {
	return RunExprByHashWithRequest(exprStr, hash, params, RequestInput{})
}

func RunExprByHashWithRequest(exprStr, hash string, params TokenParams, request RequestInput) (float64, TraceResult, error) {
	entry, err := compileEntryFromCacheByHash(exprStr, hash)
	if err != nil {
		return 0, TraceResult{}, err
	}
	return runProgram(entry.prog, entry.requestRules, entry.usedVars, params, request)
}

// IsSensitiveHeaderName reports whether a header name can carry authentication
// or session credentials and must not be exposed to host-sanitized probes.
func IsSensitiveHeaderName(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return false
	}
	switch k {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key", "x-goog-api-key":
		return true
	}
	for _, marker := range []string{"secret", "password", "credential", "cookie", "auth", "token", "api-key", "apikey"} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}

func runProgram(prog *vm.Program, requestRules []RequestRuleTrace, usedVars map[string]bool, params TokenParams, request RequestInput) (float64, TraceResult, error) {
	trace := TraceResult{
		BillingUnit:  BillingUnitToken,
		RequestRules: append([]RequestRuleTrace(nil), requestRules...),
	}
	headers := normalizeHeaders(request.Headers, request.RejectSensitiveHeaders)
	imageCount := 1
	if usedVars["image_count"] {
		if request.ImageCount != nil {
			imageCount = *request.ImageCount
		}
		if imageCount < 1 || imageCount > dto.MaxImageN {
			return 0, trace, fmt.Errorf("image_count must be between 1 and %d", dto.MaxImageN)
		}
		trace.ImageCount = &imageCount
	}

	now := time.Now
	if request.Now != nil {
		pinned := *request.Now
		now = func() time.Time { return pinned }
	}

	env := map[string]any{
		"image_count": float64(imageCount),
		"p":           params.P,
		"c":           params.C,
		"len":         params.Len,
		"cr":          params.CR,
		"cc":          params.CC,
		"cc1h":        params.CC1h,
		"cc12h":       params.CC12h,
		"img":         params.Img,
		"img_cr":      params.ImgCR,
		"img_o":       params.ImgO,
		"ai":          params.AI,
		"ao":          params.AO,
		"tier": func(name string, value float64) float64 {
			trace.MatchedTier = name
			trace.Cost = value
			return value
		},
		"fixed": func(amount float64) float64 {
			trace.BillingUnit = BillingUnitRequest
			trace.FixedPrice = &amount
			return amount * 1_000_000
		},
		requestRuleTraceFunction: func(ruleIndex int, matched bool, multiplier float64) float64 {
			if matched && ruleIndex >= 0 && ruleIndex < len(trace.RequestRules) {
				trace.RequestRules[ruleIndex].Matched = true
			}
			if matched {
				return multiplier
			}
			return 1
		},
		requestRuleTraceIntFunction: func(ruleIndex int, matched bool, multiplier int) int {
			if matched && ruleIndex >= 0 && ruleIndex < len(trace.RequestRules) {
				trace.RequestRules[ruleIndex].Matched = true
			}
			if matched {
				return multiplier
			}
			return 1
		},
		"header": func(key string) string {
			k := strings.ToLower(strings.TrimSpace(key))
			if request.RejectSensitiveHeaders && IsSensitiveHeaderName(k) {
				return ""
			}
			return headers[k]
		},
		"param": func(path string) any {
			path = strings.TrimSpace(path)
			if path == "" || len(request.Body) == 0 {
				return nil
			}
			result := gjson.GetBytes(request.Body, path)
			if !result.Exists() {
				return nil
			}
			return result.Value()
		},
		"u": func(name string) any {
			if request.Usage == nil {
				return nil
			}
			return request.Usage[strings.TrimSpace(name)]
		},
		"has": func(source any, substr string) bool {
			if source == nil || substr == "" {
				return false
			}
			return strings.Contains(fmt.Sprint(source), substr)
		},
		"hour":    func(tz string) int { return timeInZone(now(), tz).Hour() },
		"minute":  func(tz string) int { return timeInZone(now(), tz).Minute() },
		"weekday": func(tz string) int { return int(timeInZone(now(), tz).Weekday()) },
		"month":   func(tz string) int { return int(timeInZone(now(), tz).Month()) },
		"day":     func(tz string) int { return timeInZone(now(), tz).Day() },
		"max":     math.Max,
		"min":     math.Min,
		"abs":     math.Abs,
		"ceil":    math.Ceil,
		"floor":   math.Floor,
	}

	out, err := expr.Run(prog, env)
	if err != nil {
		return 0, trace, fmt.Errorf("expr run error: %w", err)
	}
	f, ok := out.(float64)
	if !ok {
		return 0, trace, fmt.Errorf("expr result is %T, want float64", out)
	}
	return f, trace, nil
}

func timeInZone(instant time.Time, tz string) time.Time {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return instant.UTC()
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return instant.UTC()
	}
	return instant.In(loc)
}

func normalizeHeaders(headers map[string]string, rejectSensitive bool) map[string]string {
	if len(headers) == 0 {
		return map[string]string{}
	}
	normalized := make(map[string]string, len(headers))
	for key, value := range headers {
		k := strings.ToLower(strings.TrimSpace(key))
		v := strings.TrimSpace(value)
		if k == "" || v == "" {
			continue
		}
		if rejectSensitive && IsSensitiveHeaderName(k) {
			continue
		}
		normalized[k] = v
	}
	return normalized
}
