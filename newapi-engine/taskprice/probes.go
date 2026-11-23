// SPDX-License-Identifier: AGPL-3.0-or-later
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f,
// pkg/billingexpr/compile.go and run.go. Durable probe freezing is host-specific.
package taskprice

import (
	"errors"
	"math"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/newapi-engine/billingexpr"
	"github.com/tidwall/gjson"
)

// RequestRuleTrace records one request-dependent multiplier rule from the AST.
type RequestRuleTrace = billingexpr.RequestRuleTrace

// ProbeSnapshot stores ONLY the scalar request parameters and non-secret
// headers explicitly referenced by literal param("...") / header("...") calls.
// Raw request bodies and credential headers never enter pricing snapshots.
type ProbeSnapshot struct {
	Params  map[string]any    `json:"params,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// EvaluateResult carries the evaluated USD amount, matched tier, and any
// request-rule multiplier traces observed during evaluation.
type EvaluateResult struct {
	Amount       float64            `json:"amount"`
	Tier         string             `json:"tier,omitempty"`
	RequestRules []RequestRuleTrace `json:"request_rules,omitempty"`
}

// IsSensitiveHeaderName reports whether a header name can carry credentials
// and must be excluded from Task pricing probes.
func IsSensitiveHeaderName(name string) bool {
	return billingexpr.IsSensitiveHeaderName(name)
}

// ValidParamPath allows bounded dot-separated JSON selector paths while
// rejecting gjson wildcard, query, and modifier syntax.
func ValidParamPath(path string) bool {
	path = strings.TrimSpace(path)
	if len(path) == 0 || len(path) > 128 {
		return false
	}
	if path[0] == '.' || path[len(path)-1] == '.' || strings.Contains(path, "..") {
		return false
	}
	for i := 0; i < len(path); i++ {
		ch := path[i]
		ok := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-'
		if !ok {
			return false
		}
	}
	return true
}

// ValidHeaderName allows bounded non-sensitive HTTP header names.
func ValidHeaderName(name string) bool {
	name = strings.TrimSpace(name)
	if len(name) == 0 || len(name) > 128 || IsSensitiveHeaderName(name) {
		return false
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		ok := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_'
		if !ok {
			return false
		}
	}
	return true
}

func (p *Program) ParamPaths() []string  { return slices.Clone(p.paramPaths) }
func (p *Program) HeaderNames() []string { return slices.Clone(p.headerNames) }
func (p *Program) HasProbes() bool       { return len(p.paramPaths) > 0 || len(p.headerNames) > 0 }

// CaptureProbes freezes only the referenced scalar JSON parameters and
// non-sensitive headers at admission time so restarts and edits replay identically.
func (p *Program) CaptureProbes(body []byte, headers map[string]string) (*ProbeSnapshot, error) {
	if !p.HasProbes() {
		return nil, nil
	}
	snap := &ProbeSnapshot{}
	if len(p.paramPaths) > 0 {
		snap.Params = make(map[string]any, len(p.paramPaths))
		for _, path := range p.paramPaths {
			if len(body) == 0 {
				continue
			}
			res := gjson.GetBytes(body, path)
			if !res.Exists() || res.Type == gjson.Null {
				continue
			}
			val, err := validateProbeParamValue(res.Value())
			if err != nil {
				return nil, err
			}
			snap.Params[path] = val
		}
	}
	if len(p.headerNames) > 0 {
		normalized := make(map[string]string, len(headers))
		for k, v := range headers {
			lk := strings.ToLower(strings.TrimSpace(k))
			if lk == "" || IsSensitiveHeaderName(lk) {
				continue
			}
			normalized[lk] = strings.TrimSpace(v)
		}
		snap.Headers = make(map[string]string, len(p.headerNames))
		for _, name := range p.headerNames {
			v := normalized[name]
			if len(v) > 256 {
				return nil, errors.New("task price header probe value exceeds limit")
			}
			snap.Headers[name] = v
		}
	}
	return snap, nil
}

func validateProbeParamValue(value any) (any, error) {
	switch typed := value.(type) {
	case nil, bool:
		return typed, nil
	case string:
		if len(typed) > 256 {
			return nil, errors.New("task price param string exceeds limit")
		}
		return typed, nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed < -1e12 || typed > 1e12 {
			return nil, errors.New("task price param number must be finite and bounded")
		}
		return typed, nil
	default:
		return nil, errors.New("task price param must resolve to a scalar value")
	}
}

func (p *Program) validateProbes(probes *ProbeSnapshot) error {
	if !p.HasProbes() {
		return nil
	}
	if probes == nil {
		return errors.New("task price request probe snapshot is missing")
	}
	for path, val := range probes.Params {
		if !ValidParamPath(path) {
			return errors.New("task price param snapshot contains an invalid path")
		}
		if _, err := validateProbeParamValue(val); err != nil {
			return err
		}
	}
	for name, val := range probes.Headers {
		if !ValidHeaderName(name) {
			return errors.New("task price header snapshot contains an invalid or sensitive header")
		}
		if len(val) > 256 {
			return errors.New("task price header snapshot value exceeds limit")
		}
	}
	return nil
}
