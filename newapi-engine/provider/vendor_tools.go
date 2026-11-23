// SPDX-License-Identifier: AGPL-3.0-or-later
// Vendor field names and cumulative replacement follow the pinned New API
// relay/channel/claude/relay-claude.go and the xai/ali/openai web_search.go.
// Unlike permissive DTO integer decoding, invalid billing facts fail closed.
package provider

import (
	"errors"
	"math"

	"github.com/tidwall/gjson"
)

type VendorProtocol string

const (
	VendorOpenAI    VendorProtocol = "openai"
	VendorAnthropic VendorProtocol = "anthropic"
	VendorGemini    VendorProtocol = "gemini"
)

// ObserveVendor reads the upstream protocol before a codec drops its private
// usage fields. Reports are cumulative snapshots, never hook-style deltas.
// The caller supplies the actual selected protocol, never a model-name guess.
// Explicit plugin facts still override vendor facts in Combined, including 0.
func (p *ProtocolTools) ObserveVendor(protocol VendorProtocol, raw []byte, stream bool) error {
	if p == nil || !gjson.ValidBytes(raw) {
		return nil
	}
	root := gjson.ParseBytes(raw)
	if !root.IsObject() || root.Get("error").Type != gjson.Null || root.Get("type").String() == "error" || root.Get("type").String() == "response.failed" {
		return nil
	}
	var fields map[string]string
	var usage gjson.Result
	var err error
	switch protocol {
	case VendorGemini:
		if err := p.observeGeminiGrounding(root); err != nil {
			return err
		}
		p.upstream = VendorGemini
		return nil
	case VendorAnthropic:
		switch kind := root.Get("type").String(); {
		case !stream && kind == "message", stream && kind == "message_delta":
			usage, err = vendorToolPath(root, "usage", "server_tool_use")
		case stream && kind == "message_start":
			usage, err = vendorToolPath(root, "message", "usage", "server_tool_use")
		default:
			return nil
		}
		fields = map[string]string{"web_search_requests": "web_search", "web_fetch_requests": "web_fetch", "code_execution_requests": "code_execution", "tool_search_requests": "tool_search"}
	case VendorOpenAI:
		if kind := root.Get("type").String(); kind != "" {
			if !stream || kind != "response.completed" && kind != "response.incomplete" && kind != "response.done" {
				return nil
			}
			root, err = vendorToolPath(root, "response")
			if err != nil {
				return err
			}
			status := root.Get("status").String()
			if status != "completed" && status != "incomplete" || root.Get("error").Type != gjson.Null {
				return nil
			}
		}
		if status := root.Get("status").String(); status != "" && status != "completed" && status != "incomplete" {
			return nil
		}
		usage, err = vendorToolPath(root, "usage", "server_side_tool_usage_details")
		fields = map[string]string{"web_search_calls": "web_search", "x_posts_fetched": "x_search_posts", "x_users_fetched": "x_search_profiles"}
	default:
		return nil
	}
	if err != nil {
		return err
	}
	counts := make(map[string]int)
	searchVendor := ""
	for field, name := range fields {
		value, err := vendorToolPath(usage, field)
		if err != nil {
			return err
		}
		if !value.Exists() {
			continue
		}
		count, err := vendorToolCount(value)
		if err != nil {
			return err
		}
		counts[name] = count
		if protocol == VendorOpenAI && name == "web_search" {
			searchVendor = name
			// xAI's billed count replaces either public hosted search spelling.
			counts["web_search_preview"] = 0
		}
	}
	if protocol == VendorOpenAI {
		// These namespaces are actual vendor-reported request counts. They
		// select their baseline tool-price keys without guessing a vendor from
		// an endpoint or model name. Conflicting reports are ambiguous.
		for _, report := range []struct {
			name string
			path []string
		}{
			{"search_strategy_agent", []string{"usage", "x_tools", "web_search", "count"}},
			{"bing_web_search", []string{"tool_usage", "web_search", "num_requests"}},
		} {
			value, err := vendorToolPath(root, report.path...)
			if err != nil {
				return err
			}
			if !value.Exists() {
				continue
			}
			if searchVendor != "" {
				return errors.New("conflicting provider vendor search reports")
			}
			count, err := vendorToolCount(value)
			if err != nil {
				return err
			}
			counts[report.name] = count
			searchVendor = report.name
			counts["web_search"], counts["web_search_preview"] = 0, 0
		}
		// GLM Chat non-stream responses report search results as a top-level
		// web_search array (listing results, not a count -> bill 1 call).
		if p.glmSearchKey != "" {
			ws, err := vendorToolPath(root, "web_search")
			if err != nil {
				return err
			}
			if ws.IsArray() && len(ws.Array()) > 0 {
				if searchVendor != "" {
					return errors.New("conflicting provider vendor search reports")
				}
				counts[p.glmSearchKey] = 1
				searchVendor = p.glmSearchKey
				counts["web_search"], counts["web_search_preview"] = 0, 0
			}
		}
	}
	if searchVendor != "" && p.searchVendor != "" && searchVendor != p.searchVendor {
		return errors.New("provider vendor search report changed within request")
	}
	if err := p.replaceVendorCounts(counts); err != nil {
		return err
	}
	if searchVendor != "" {
		p.searchVendor = searchVendor
	}
	if protocol == VendorAnthropic && len(counts) > 0 {
		p.upstream = VendorAnthropic
	}
	return nil
}

func vendorToolCount(value gjson.Result) (int, error) {
	if value.Type != gjson.Number || math.IsNaN(value.Float()) || math.IsInf(value.Float(), 0) || value.Float() < 0 || value.Float() > MaxToolCalls || math.Trunc(value.Float()) != value.Float() {
		return 0, errors.New("invalid provider vendor tool count")
	}
	return int(value.Float()), nil
}

// Reject ambiguous billing paths without retaining unrelated upstream data.
// A missing or nullable optional object is not a reported zero count.
func vendorToolPath(root gjson.Result, path ...string) (gjson.Result, error) {
	for _, field := range path {
		if !root.Exists() || root.Type == gjson.Null {
			return gjson.Result{}, nil
		}
		if !root.IsObject() {
			return gjson.Result{}, errors.New("invalid provider vendor tool usage object")
		}
		var value gjson.Result
		seen := false
		duplicate := false
		root.ForEach(func(key, child gjson.Result) bool {
			if key.String() == field {
				duplicate = seen
				seen, value = true, child
			}
			return !duplicate
		})
		if duplicate {
			return gjson.Result{}, errors.New("ambiguous provider vendor tool usage")
		}
		root = value
	}
	return root, nil
}

func (p *ProtocolTools) replaceVendorCounts(counts map[string]int) error {
	merged := p.vendor.Counts()
	if merged == nil {
		merged = make(map[string]int, len(counts))
	}
	for name, count := range counts {
		merged[name] = count
	}
	var validated ToolUsage
	if err := validated.Add(merged); err != nil {
		return err
	}
	p.vendor = validated
	return nil
}
