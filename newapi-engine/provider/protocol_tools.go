// SPDX-License-Identifier: AGPL-3.0-or-later
// Invocation counting follows the pinned New API relay/channel/openai handlers
// and relay/common/tool_usage.go. The local observer adds bounded identity
// deduplication across Responses events and their final output snapshot.
package provider

import (
	"errors"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

const MaxObservedToolCalls = 10_000

// ProtocolTools observes actual calls in the public OpenAI wire, never tool
// declarations. A function with a reserved hosted-tool name is not a hosted
// call. Only explicitly priced non-reserved client functions are billable.
type ProtocolTools struct {
	usage                 ToolUsage
	vendor                ToolUsage
	grounding             map[[32]byte]struct{}
	upstream              VendorProtocol
	searchVendor          string
	glmSearchKey          string
	requestInferredSearch string
	seen                  map[string]struct{}
	lookup                func(string) (float64, bool)
	searchKey             string
	calls                 int
	terminal              bool
}

// IsGLMSearchTool reports whether tool carries GLM's nested web_search object,
// distinguishing it from OpenAI Responses hosted web_search declarations.
func IsGLMSearchTool(tool gjson.Result) bool {
	return tool.IsObject() && tool.Get("type").String() == "web_search" && tool.Get("web_search").Exists()
}

// ParseGLMSearchEngine validates a GLM web_search tool declaration and returns
// its canonical price key, enabled flag, forced-search flag, and validity.
func ParseGLMSearchEngine(tool gjson.Result) (engine string, enabled bool, forced bool, ok bool) {
	if !IsGLMSearchTool(tool) {
		return "", false, false, true
	}
	ws := tool.Get("web_search")
	if !ws.IsObject() {
		return "", false, false, false
	}
	en := ws.Get("enable")
	switch en.Type {
	case gjson.Null:
		return "", false, false, true
	case gjson.False:
		return "", false, false, true
	case gjson.True:
		enabled = true
	default:
		return "", false, false, false
	}
	se := ws.Get("search_engine")
	if !se.Exists() || se.Type == gjson.Null || (se.Type == gjson.String && strings.TrimSpace(se.String()) == "") {
		engine = "search_std"
	} else if se.Type == gjson.String {
		switch strings.TrimSpace(se.String()) {
		case "search_std", "search_pro", "search_pro_sogou", "search_pro_quark":
			engine = strings.TrimSpace(se.String())
		default:
			return "", false, false, false
		}
	} else {
		return "", false, false, false
	}
	si := ws.Get("search_intent")
	if si.Exists() && si.Type != gjson.Null {
		switch si.Type {
		case gjson.False:
			forced = true
		case gjson.True:
			forced = false
		default:
			return "", false, false, false
		}
	}
	return engine, enabled, forced, true
}

// ParseDashScopeSearchStrategy validates DashScope enable_search / search_options
// and returns the canonical search_strategy_* price key, enabled flag,
// forced-search flag, and validity.
func ParseDashScopeSearchStrategy(body []byte) (strategy string, enabled bool, forced bool, ok bool) {
	es := gjson.GetBytes(body, "enable_search")
	if !es.Exists() || es.Type == gjson.Null || es.Type == gjson.False {
		return "", false, false, true
	}
	if es.Type != gjson.True {
		return "", false, false, false
	}
	opts := gjson.GetBytes(body, "search_options")
	if opts.Exists() && opts.Type != gjson.Null && !opts.IsObject() {
		return "", false, false, false
	}
	ss := opts.Get("search_strategy")
	if !ss.Exists() || ss.Type == gjson.Null || (ss.Type == gjson.String && strings.TrimSpace(ss.String()) == "") {
		strategy = "search_strategy_turbo"
	} else if ss.Type == gjson.String {
		switch strings.TrimSpace(ss.String()) {
		case "turbo", "search_strategy_turbo":
			strategy = "search_strategy_turbo"
		case "max", "search_strategy_max":
			strategy = "search_strategy_max"
		case "agent", "search_strategy_agent":
			strategy = "search_strategy_agent"
		case "agent_max", "search_strategy_agent_max":
			strategy = "search_strategy_agent_max"
		default:
			return "", false, false, false
		}
	} else {
		return "", false, false, false
	}
	fs := opts.Get("forced_search")
	if fs.Exists() && fs.Type != gjson.Null {
		switch fs.Type {
		case gjson.True:
			forced = true
		case gjson.False:
			forced = false
		default:
			return "", false, false, false
		}
	}
	return strategy, true, forced, true
}

func NewProtocolTools(body []byte, lookup func(string) (float64, bool)) *ProtocolTools {
	p := &ProtocolTools{lookup: lookup, searchKey: "web_search_preview", seen: make(map[string]struct{})}
	searchKeyLocked := false
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		if IsGLMSearchTool(tool) {
			if engine, enabled, forced, ok := ParseGLMSearchEngine(tool); ok && enabled {
				p.glmSearchKey = engine
				if forced && p.requestInferredSearch == "" {
					p.requestInferredSearch = engine
				}
			}
			continue
		}
		if searchKeyLocked {
			continue
		}
		switch kind := tool.Get("type").String(); {
		case kind == "web_search_preview" || strings.HasPrefix(kind, "web_search_preview_"):
			p.searchKey = "web_search_preview"
			searchKeyLocked = true
		case kind == "web_search" || strings.HasPrefix(kind, "web_search_"):
			p.searchKey = "web_search"
		}
	}
	if strategy, enabled, forced, ok := ParseDashScopeSearchStrategy(body); ok && enabled && forced && p.requestInferredSearch == "" {
		p.requestInferredSearch = strategy
	}
	return p
}

func reservedTool(name string) bool {
	switch name {
	case "web_search", "web_search_preview", "file_search", "google_search", "google_search_grounded_prompt", "image_generation", "web_fetch", "code_execution", "tool_search", "search_std", "search_pro", "search_pro_sogou", "search_pro_quark", "search_strategy_turbo", "search_strategy_max", "search_strategy_agent", "search_strategy_agent_max", "bing_web_search", "x_search_posts", "x_search_profiles", "deepseek_search", "deepseek_fetch":
		return true
	}
	return false
}

func (p *ProtocolTools) function(name string) string {
	if reservedTool(name) || p.lookup == nil {
		return ""
	}
	price, found := p.lookup(name)
	if !found || price <= 0 {
		return ""
	}
	return name
}

func (p *ProtocolTools) record(name, location, id string) error {
	if name == "" {
		return nil
	}
	if len(id) > 256 {
		return errors.New("provider tool call identity exceeds limit")
	}
	aliases := []string{name + "/" + location}
	if id != "" {
		aliases = append(aliases, name+"/id/"+id)
	}
	exists := false
	newAliases := 0
	for _, alias := range aliases {
		if _, ok := p.seen[alias]; ok {
			exists = true
		} else {
			newAliases++
		}
	}
	if len(p.seen)+newAliases > MaxObservedToolCalls*4 {
		return errors.New("provider tool identities exceed limit")
	}
	if !exists {
		if p.calls >= MaxObservedToolCalls {
			return errors.New("provider observed tool calls exceed limit")
		}
		if err := p.usage.Add(map[string]int{name: 1}); err != nil {
			return err
		}
		p.calls++
	}
	for _, alias := range aliases {
		p.seen[alias] = struct{}{}
	}
	return nil
}

func (p *ProtocolTools) responseItem(item gjson.Result, index int64) error {
	name := ""
	switch item.Get("type").String() {
	case "web_search_call":
		name = p.searchKey
	case "file_search_call":
		name = "file_search"
	case "function_call":
		name = p.function(item.Get("name").String())
	}
	return p.record(name, fmt.Sprintf("responses/%d", index), item.Get("id").String())
}

func (p *ProtocolTools) responseHostedItem(item gjson.Result, index int64) error {
	name := ""
	switch item.Get("type").String() {
	case "web_search_call":
		name = p.searchKey
	case "file_search_call":
		name = "file_search"
	default:
		return nil
	}
	return p.record(name, fmt.Sprintf("responses/%d", index), item.Get("id").String())
}

// ObserveUpstreamResponsesHostedTools counts hosted web_search_call and
// file_search_call items emitted by an upstream Responses endpoint before
// RelayKit converts the response into Chat or Claude format.
func (p *ProtocolTools) ObserveUpstreamResponsesHostedTools(raw []byte, stream bool) error {
	if p == nil || !gjson.ValidBytes(raw) {
		return nil
	}
	root := gjson.ParseBytes(raw)
	if !root.IsObject() || (root.Get("error").Exists() && root.Get("error").Type != gjson.Null) {
		return nil
	}
	if stream {
		switch root.Get("type").String() {
		case "response.output_item.done":
			kind := root.Get("item.type").String()
			if kind != "web_search_call" && kind != "file_search_call" {
				return nil
			}
			index := root.Get("output_index")
			if index.Type != gjson.Number || index.Int() < 0 || index.Int() > MaxObservedToolCalls || index.Float() != float64(index.Int()) {
				return errors.New("invalid provider tool output index")
			}
			return p.responseHostedItem(root.Get("item"), index.Int())
		case "response.completed", "response.incomplete":
			root = root.Get("response")
		default:
			return nil
		}
	}
	for i, item := range root.Get("output").Array() {
		if err := p.responseHostedItem(item, int64(i)); err != nil {
			return err
		}
	}
	return nil
}

func (p *ProtocolTools) Observe(path string, raw []byte, stream bool) error {
	if p == nil || p.terminal || !gjson.ValidBytes(raw) {
		return nil
	}
	root := gjson.ParseBytes(raw)
	if root.Get("error").Exists() && root.Get("error").Type != gjson.Null {
		return nil
	}
	if path == "/v1/messages" {
		if err := p.ObserveVendor(VendorAnthropic, raw, stream); err != nil {
			return err
		}
		if stream {
			switch root.Get("type").String() {
			case "content_block_start":
				cb := root.Get("content_block")
				if cb.Get("type").String() != "tool_use" {
					return nil
				}
				name := p.function(strings.TrimSpace(cb.Get("name").String()))
				if name == "" {
					return nil
				}
				index := root.Get("index")
				if index.Type != gjson.Number || index.Int() < 0 || index.Int() > MaxObservedToolCalls || index.Float() != float64(index.Int()) {
					return errors.New("invalid provider tool block index")
				}
				return p.record(name, fmt.Sprintf("messages/%d", index.Int()), cb.Get("id").String())
			case "message_stop":
				p.terminal = true
			}
			return nil
		}
		if root.Get("type").String() == "message" {
			for i, block := range root.Get("content").Array() {
				if block.Get("type").String() != "tool_use" {
					continue
				}
				name := p.function(strings.TrimSpace(block.Get("name").String()))
				if err := p.record(name, fmt.Sprintf("messages/%d", i), block.Get("id").String()); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := p.ObserveVendor(VendorOpenAI, raw, stream); err != nil {
		return err
	}
	if path == "/v1/responses" {
		if stream {
			switch root.Get("type").String() {
			case "response.output_item.done":
				kind := root.Get("item.type").String()
				if kind != "web_search_call" && kind != "file_search_call" && (kind != "function_call" || p.function(root.Get("item.name").String()) == "") {
					return nil
				}
				index := root.Get("output_index")
				if index.Type != gjson.Number || index.Int() < 0 || index.Int() > MaxObservedToolCalls || index.Float() != float64(index.Int()) {
					return errors.New("invalid provider tool output index")
				}
				return p.responseItem(root.Get("item"), index.Int())
			case "response.completed", "response.incomplete":
				defer func() { p.terminal = true }()
				root = root.Get("response")
			default:
				return nil
			}
		}
		for i, item := range root.Get("output").Array() {
			if err := p.responseItem(item, int64(i)); err != nil {
				return err
			}
		}
	} else if path == "/v1/chat/completions" {
		for i, choice := range root.Get("choices").Array() {
			index := choice.Get("index").Int()
			if !choice.Get("index").Exists() {
				index = int64(i)
			}
			message := choice.Get("message")
			if stream {
				message = choice.Get("delta")
			}
			for j, call := range message.Get("tool_calls").Array() {
				name := p.function(strings.TrimSpace(call.Get("function.name").String()))
				callIndex := call.Get("index").Int()
				if !call.Get("index").Exists() {
					callIndex = int64(j)
				}
				if err := p.record(name, fmt.Sprintf("chat/%d/%d/%s", index, callIndex, name), call.Get("id").String()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Combined overlays explicit hook counts on the same inferred key, including
// zero. Other inferred keys remain intact, as in SetBillableToolCount.
func (p *ProtocolTools) Combined(explicit *ToolUsage) (*ToolUsage, error) {
	counts := map[string]int{}
	if p != nil {
		if p.requestInferredSearch != "" && p.searchVendor == "" && p.upstream != VendorGemini && p.upstream != VendorAnthropic {
			counts[p.requestInferredSearch] = 1
		}
		for name, count := range p.usage.Counts() {
			// RelayKit can render Gemini grounding as OpenAI search items.
			// Those are a projection of the same queries, not additional
			// OpenAI-hosted calls. Only the actual Gemini evidence is billable.
			if p.upstream == VendorGemini && (name == "web_search" || name == "web_search_preview") {
				continue
			}
			counts[name] = count
		}
	}
	if p != nil {
		for name, count := range p.vendor.Counts() {
			counts[name] = count
		}
	}
	for name, count := range explicit.Counts() {
		counts[name] = count
	}
	result := &ToolUsage{}
	if err := result.Add(counts); err != nil {
		return nil, err
	}
	return result, nil
}
