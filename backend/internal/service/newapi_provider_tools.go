package service

import (
	"errors"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/Wei-Shaw/sub2api/newapi-engine/protocolconv"
	"github.com/Wei-Shaw/sub2api/newapi-engine/provider"
	"github.com/tidwall/gjson"
)

// Operator-configured per-model prices take precedence. The explicit group
// search card remains a fallback for legacy web_search names only.
type newAPIProviderToolUsage struct {
	usage    provider.ToolUsage
	price    *float64
	prices   *provider.PriceBook
	model    string
	protocol *provider.ProtocolTools
}

// Observe the protocol returned by the preset (after a composed plugin has
// unwrapped it), before RelayKit drops private vendor billing fields.
func (u *newAPIProviderToolUsage) observeVendor(bridge *protocolconv.Bridge, raw []byte, stream bool) error {
	if u == nil || u.protocol == nil || bridge == nil {
		return nil
	}
	if bridge.Responses() && bridge.From() != types.RelayFormatOpenAIResponses {
		if err := u.protocol.ObserveUpstreamResponsesHostedTools(raw, stream); err != nil {
			return err
		}
	}
	format := provider.VendorOpenAI
	if bridge.Claude() {
		format = provider.VendorAnthropic
	} else if bridge.Gemini() {
		format = provider.VendorGemini
	}
	return u.protocol.ObserveVendor(format, raw, stream)
}

func newProviderToolUsage(key *APIKey) *newAPIProviderToolUsage {
	u := &newAPIProviderToolUsage{}
	if price := groupSearchPricePer1kFromAPIKey(key); price != nil && !math.IsNaN(*price) && !math.IsInf(*price, 0) && *price >= 0 && *price <= provider.MaxToolPrice {
		copy := *price
		u.price = &copy
	}
	return u
}

// Reject an explicitly requested hosted search/file tool before any Provider HTTP call
// (including token exchange) if the host cannot price it. Function tools named
// web_search are client functions, not evidence of a hosted, billable search.
func (u *newAPIProviderToolUsage) admitRequest(body []byte) error {
	hostedKey := func(kind string) (string, bool) {
		switch {
		case kind == "web_search_preview" || strings.HasPrefix(kind, "web_search_preview_"):
			return "web_search_preview", true
		case kind == "web_search" || strings.HasPrefix(kind, "web_search_"):
			return "web_search", true
		case kind == "file_search":
			return "file_search", true
		case kind == "web_fetch" || strings.HasPrefix(kind, "web_fetch_"):
			return "web_fetch", true
		case kind == "code_execution" || strings.HasPrefix(kind, "code_execution_"):
			return "code_execution", true
		case kind == "tool_search" || strings.HasPrefix(kind, "tool_search_"):
			return "tool_search", true
		default:
			return "", false
		}
	}
	admit := func(kind string) error {
		if _, found := u.lookup(kind); !found {
			return errors.New("Provider hosted tools require an explicit tool price")
		}
		return nil
	}
	strategy, dashEnabled, _, dashOK := provider.ParseDashScopeSearchStrategy(body)
	if !dashOK {
		return errors.New("Provider DashScope search configuration is unsupported")
	}
	if dashEnabled {
		if err := admit(strategy); err != nil {
			return err
		}
	}
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		if provider.IsGLMSearchTool(tool) {
			engine, enabled, _, ok := provider.ParseGLMSearchEngine(tool)
			if !ok {
				return errors.New("Provider GLM web_search configuration is unsupported")
			}
			if enabled {
				if err := admit(engine); err != nil {
					return err
				}
			}
			continue
		}
		if key, ok := hostedKey(tool.Get("type").String()); ok {
			if err := admit(key); err != nil {
				return err
			}
		}
	}
	if key, ok := hostedKey(gjson.GetBytes(body, "tool_choice.type").String()); ok {
		if err := admit(key); err != nil {
			return err
		}
	}
	if gjson.GetBytes(body, "web_search_options").IsObject() {
		return admit("web_search")
	}
	return nil
}

func (u *newAPIProviderToolUsage) lookup(name string) (float64, bool) {
	if u == nil {
		return 0, false
	}
	if price, found := u.prices.Lookup(name, u.model); found {
		return price, true
	}
	if (name == "web_search" || name == "web_search_preview") && u.price != nil {
		return *u.price, true
	}
	return 0, false
}

func (u *newAPIProviderToolUsage) add(counts map[string]int) error {
	if len(counts) == 0 {
		return nil
	}
	if u == nil {
		return errors.New("Provider tool accounting is unavailable")
	}
	for name, count := range counts {
		if _, found := u.lookup(name); count > 0 && !found {
			return errors.New("Provider tool requires an explicit price")
		}
	}
	return u.usage.Add(counts)
}

func (u *newAPIProviderToolUsage) apply(result *OpenAIForwardResult) error {
	if u == nil || result == nil {
		return nil
	}
	combined, err := u.protocol.Combined(&u.usage)
	if err != nil {
		return err
	}
	counts := combined.Counts()
	if len(counts) == 0 {
		return nil
	}
	charges, pricingErr := combined.Charges(u.lookup)
	if pricingErr != nil {
		// Preserve known costs on a partial failure without inventing the price
		// of an unexpected hosted tool. The caller still returns a pricing error.
		known := &provider.ToolUsage{}
		for name, count := range counts {
			if _, found := u.lookup(name); found || count == 0 {
				_ = known.Add(map[string]int{name: count})
			}
		}
		charges, _ = known.Charges(u.lookup)
	}
	// Explicit hook counts replace, rather than add to, protocol inference.
	// The engine caps each tool key at the baseline's 10,000 calls.
	result.SearchCount = counts["web_search"] + counts["web_search_preview"]
	result.providerToolCharges = &charges
	return pricingErr
}

func (u *newAPIProviderToolUsage) observe(path string, raw []byte, stream bool) error {
	if u == nil || u.protocol == nil {
		return nil
	}
	if err := u.protocol.Observe(path, raw, stream); err != nil {
		return err
	}
	combined, err := u.protocol.Combined(&u.usage)
	if err != nil {
		return err
	}
	_, err = combined.Charges(u.lookup)
	return err
}
