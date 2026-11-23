// SPDX-License-Identifier: AGPL-3.0-or-later
// Model modifier parsing/precedence follows QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f, relay/helper/model_modifier.go.
package modelmodifier

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var ErrInvalid = errors.New("invalid model modifier; use thinking, effort, temperature or topp, or exempt the complete real model name")

type Policy struct {
	exact map[string]bool
	regex []*regexp.Regexp
}

// Allowlists, routing, pricing and hook scopes share an atomically replaced,
// immutable policy. Never install a request-specific or account-specific policy.
var policy atomic.Pointer[Policy]

func CompilePolicy(entries []string) (*Policy, error) {
	if len(entries) > 128 {
		return nil, errors.New("model modifier exemptions exceed 128 entries")
	}
	p := &Policy{exact: make(map[string]bool, len(entries))}
	for _, entry := range entries {
		if strings.TrimSpace(entry) != entry || entry == "" || len(entry) > 512 {
			return nil, errors.New("model modifier exemptions must be nonempty names or re: expressions of at most 512 bytes")
		}
		if pattern, ok := strings.CutPrefix(entry, "re:"); ok {
			re, err := regexp.Compile(pattern)
			if err != nil || pattern == "" {
				return nil, errors.New("invalid model modifier exemption regex")
			}
			p.regex = append(p.regex, re)
		} else {
			p.exact[entry] = true
		}
	}
	return p, nil
}

func Configure(entries []string) error {
	p, err := CompilePolicy(entries)
	if err == nil {
		policy.Store(p)
	}
	return err
}

func (p *Policy) Preserves(name string) bool {
	if p == nil {
		return false
	}
	if p.exact[name] {
		return true
	}
	for _, re := range p.regex {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

func Preserves(name string) bool { return policy.Load().Preserves(name) }

// Base is for lookup, not validation. Invalid modifier values still route to
// their declared base so the execution boundary can return one local 400.
func Base(name string) string {
	if Preserves(name) {
		return name
	}
	return reasoning.ParseModelModifiers(name).Base
}

type Spec struct {
	Base        string
	Thinking    bool
	Intent      reasoning.Intent
	Temperature *float64
	TopP        *float64
}

func (s Spec) HasControls() bool { return s.Thinking || s.Temperature != nil || s.TopP != nil }

func (s Spec) Effort() string { return string(reasoning.EffectiveEffort(s.Intent)) }

func (s Spec) BillingNames() []string {
	if !s.Thinking || s.Base == "" {
		return []string{s.Base}
	}
	thinking, effort := "on", string(s.Intent.Effort)
	if s.Intent.Mode == reasoning.ModeDisabled || s.Intent.Effort == reasoning.EffortNone || s.Intent.BudgetTokens != nil && *s.Intent.BudgetTokens == 0 {
		thinking, effort = "off", ""
	}
	var names []string
	if effort != "" {
		names = append(names, s.Base+"@effort:"+strings.ToLower(effort)+"@thinking:"+thinking)
	}
	return append(names, s.Base+"@thinking:"+thinking, s.Base)
}

func BillingNames(name string) []string {
	parsed, err := Parse(name)
	if err != nil || parsed.Base == name {
		return nil
	}
	return parsed.BillingNames()
}

func Parse(name string) (Spec, error) { return policy.Load().Parse(name) }

func (p *Policy) Parse(name string) (Spec, error) {
	if len(name) > 4096 {
		return Spec{}, ErrInvalid
	}
	parsed := Spec{Base: name}
	if p.Preserves(name) {
		return parsed, nil
	}
	wire := reasoning.ParseModelModifiers(name)
	parsed.Base = wire.Base
	if len(wire.Modifiers) > 32 {
		return Spec{}, ErrInvalid
	}
	last := make(map[string]int, len(wire.Modifiers))
	for i, modifier := range wire.Modifiers {
		last[modifier.Key] = i
	}
	for i, modifier := range wire.Modifiers {
		if last[modifier.Key] != i {
			continue // Pinned contract: the rightmost value wins, even if earlier ones were invalid.
		}
		switch modifier.Key {
		case "thinking":
			intent, ok := reasoning.ParseThinkingModifier(modifier.Value)
			if !ok {
				return Spec{}, ErrInvalid
			}
			if parsed.Thinking && intent.Mode != reasoning.ModeDisabled {
				parsed.Intent.Mode, parsed.Intent.Source = intent.Mode, reasoning.SourceSuffix
				if intent.BudgetTokens != nil {
					parsed.Intent.BudgetTokens, parsed.Intent.BudgetSource = intent.BudgetTokens, intent.BudgetSource
				}
			} else {
				parsed.Intent = intent
			}
			parsed.Thinking = true
		case "effort":
			effort, err := reasoning.ParseEffort(modifier.Value)
			if err != nil || effort == "" {
				return Spec{}, ErrInvalid
			}
			if effort == reasoning.EffortNone {
				parsed.Intent = reasoning.Intent{Mode: reasoning.ModeDisabled, Effort: effort, Source: reasoning.SourceSuffix}
			} else {
				if parsed.Intent.Mode == reasoning.ModeUnset || parsed.Intent.Mode == reasoning.ModeDisabled {
					parsed.Intent.Mode = reasoning.ModeEnabled
				}
				parsed.Intent.Effort, parsed.Intent.Source = effort, reasoning.SourceSuffix
			}
			parsed.Thinking = true
		case "temperature", "topp":
			value, err := strconv.ParseFloat(strings.TrimSpace(modifier.Value), 64)
			if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
				return Spec{}, ErrInvalid
			}
			if modifier.Key == "temperature" {
				parsed.Temperature = &value
			} else {
				parsed.TopP = &value
			}
		default:
			return Spec{}, ErrInvalid
		}
	}
	return parsed, nil
}

// Resolve overlays mapped controls as a complete dimension, matching the host
// contract. Routing and billing identities are retained by the caller.
func Resolve(origin, mapped string) (Spec, error) {
	client, err := Parse(origin)
	if err != nil || origin == mapped {
		return client, err
	}
	upstream, err := Parse(mapped)
	if err != nil {
		return Spec{}, err
	}
	client.Base = upstream.Base
	if upstream.Thinking {
		client.Thinking, client.Intent = true, upstream.Intent
	}
	if upstream.Temperature != nil {
		client.Temperature = upstream.Temperature
	}
	if upstream.TopP != nil {
		client.TopP = upstream.TopP
	}
	return client, nil
}

type intentKey struct{}

func (s Spec) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, intentKey{}, s)
}

func Intent(ctx context.Context) (reasoning.Intent, bool) {
	value, ok := ctx.Value(intentKey{}).(Spec)
	return value.Intent, ok && value.Thinking
}

// Reconcile discards private suffix state when an explicit host effect changes
// the actual controls. Otherwise an old exact budget could silently undo a
// request plugin's override during the next protocol conversion.
func (s Spec) Reconcile(before, after []byte, responses, force bool) (Spec, error) {
	format := "openai"
	if responses {
		format = "openai_responses"
	}
	return s.ReconcileForFormat(before, after, format, force)
}

func (s Spec) ReconcileForFormat(before, after []byte, format string, force bool) (Spec, error) {
	if !s.Thinking {
		return s, nil
	}
	changed := force
	paths := []string{"reasoning_effort", "reasoning.effort", "reasoning.enabled", "reasoning.max_tokens"}
	switch format {
	case "claude":
		paths = []string{"thinking", "output_config.effort"}
	case "gemini":
		paths = []string{
			"generationConfig.thinkingConfig",
			"generation_config.thinking_config",
			"generationConfig.thinking_config",
			"generation_config.thinkingConfig",
		}
	}
	for _, path := range paths {
		changed = changed || gjson.GetBytes(before, path).Raw != gjson.GetBytes(after, path).Raw
	}
	if !changed {
		return s, nil
	}
	if !validBody(after) {
		return Spec{}, ErrInvalid
	}
	var err error
	switch format {
	case "openai_responses":
		var req dto.OpenAIResponsesRequest
		err = json.Unmarshal(after, &req)
		req.Model = s.Base
		if err == nil {
			s.Intent, _, err = reasoning.FromOpenAIResponses(&req)
		}
	case "claude":
		var req dto.ClaudeRequest
		err = json.Unmarshal(after, &req)
		req.Model = s.Base
		if err == nil {
			s.Intent, _, err = reasoning.FromClaude(&req)
		}
	case "gemini":
		var req dto.GeminiChatRequest
		err = json.Unmarshal(after, &req)
		if err == nil {
			s.Intent, _, err = reasoning.FromGemini(&req)
		}
	default:
		var req dto.GeneralOpenAIRequest
		err = json.Unmarshal(after, &req)
		req.Model = s.Base
		if err == nil {
			s.Intent, _, err = reasoning.FromOpenAIChat(&req)
		}
	}
	if err != nil {
		return Spec{}, ErrInvalid
	}
	s.Thinking = !s.Intent.IsEmpty()
	return s, nil
}

// Apply retains unknown JSON fields and model identity. The host separately
// writes the selected upstream base; the client name remains available for
// logging, allowlists and pricing. Only the active protocol's controls change.
func (s Spec) Apply(body []byte, responses bool) ([]byte, error) {
	format := "openai"
	if responses {
		format = "openai_responses"
	}
	return s.ApplyForFormat(body, format)
}

func (s Spec) ApplyForFormat(body []byte, format string) ([]byte, error) {
	if !s.HasControls() {
		return body, nil
	}
	if len(body) > 1<<20 || !validBody(body) {
		return nil, ErrInvalid
	}
	var err error
	set := func(path string, value any) {
		if err == nil {
			body, err = sjson.SetBytes(body, path, value)
		}
	}
	del := func(path string) {
		if err == nil {
			body, err = sjson.DeleteBytes(body, path)
		}
	}
	switch format {
	case "claude":
		if s.Temperature != nil {
			set("temperature", *s.Temperature)
		}
		if s.TopP != nil {
			set("top_p", *s.TopP)
		}
		if s.Thinking {
			if current := gjson.GetBytes(body, "thinking"); current.Exists() && current.Type != gjson.Null && !current.IsObject() {
				return nil, ErrInvalid
			}
			if current := gjson.GetBytes(body, "output_config"); current.Exists() && current.Type != gjson.Null && !current.IsObject() {
				return nil, ErrInvalid
			}
			var maxTokens *uint
			if mt := gjson.GetBytes(body, "max_tokens"); mt.Type == gjson.Number && mt.Float() > 0 && math.Trunc(mt.Float()) == mt.Float() {
				v := uint(mt.Uint())
				maxTokens = &v
			}
			render, renderErr := reasoning.RenderClaude(s.Base, s.Intent, maxTokens, 0)
			if renderErr != nil {
				return nil, ErrInvalid
			}
			del("thinking")
			del("output_config.effort")
			if render.Thinking != nil {
				set("thinking", render.Thinking)
			}
			if render.OutputEffort != "" {
				set("output_config.effort", string(render.OutputEffort))
			}
			if current := gjson.GetBytes(body, "output_config"); current.IsObject() && len(current.Map()) == 0 {
				del("output_config")
			}
			if render.MaxTokens != nil {
				set("max_tokens", *render.MaxTokens)
			}
		}
	case "gemini":
		configKey := "generationConfig"
		if !gjson.GetBytes(body, "generationConfig").Exists() && gjson.GetBytes(body, "generation_config").IsObject() {
			configKey = "generation_config"
		}
		if current := gjson.GetBytes(body, configKey); current.Exists() && current.Type != gjson.Null && !current.IsObject() {
			return nil, ErrInvalid
		}
		if s.Temperature != nil {
			set(configKey+".temperature", *s.Temperature)
		}
		if s.TopP != nil {
			topPKey := "topP"
			if configKey == "generation_config" {
				topPKey = "top_p"
			}
			set(configKey+"."+topPKey, *s.TopP)
		}
		if s.Thinking {
			for _, tk := range []string{"thinkingConfig", "thinking_config"} {
				if current := gjson.GetBytes(body, configKey+"."+tk); current.Exists() && current.Type != gjson.Null && !current.IsObject() {
					return nil, ErrInvalid
				}
			}
			var maxOutputTokens *uint
			for _, mk := range []string{"maxOutputTokens", "max_output_tokens"} {
				if mt := gjson.GetBytes(body, configKey+"."+mk); mt.Type == gjson.Number && mt.Float() > 0 && math.Trunc(mt.Float()) == mt.Float() {
					v := uint(mt.Uint())
					maxOutputTokens = &v
					break
				}
			}
			render, renderErr := reasoning.RenderGemini(s.Base, s.Intent, maxOutputTokens, 0)
			if renderErr != nil {
				return nil, ErrInvalid
			}
			del(configKey + ".thinkingConfig")
			del(configKey + ".thinking_config")
			if render.Config != nil {
				thinkingKey := "thinkingConfig"
				if configKey == "generation_config" {
					thinkingKey = "thinking_config"
				}
				set(configKey+"."+thinkingKey, render.Config)
			}
		}
	case "openai_responses":
		if s.Temperature != nil {
			set("temperature", *s.Temperature)
		}
		if s.TopP != nil {
			set("top_p", *s.TopP)
		}
		if s.Thinking {
			if current := gjson.GetBytes(body, "reasoning"); current.Exists() && current.Type != gjson.Null && !current.IsObject() {
				return nil, ErrInvalid
			}
			if s.Intent.Effort != "" {
				set("reasoning.effort", string(s.Intent.Effort))
			} else if s.Intent.BudgetTokens == nil {
				del("reasoning.effort")
			}
		}
	default:
		if s.Temperature != nil {
			set("temperature", *s.Temperature)
		}
		if s.TopP != nil {
			set("top_p", *s.TopP)
		}
		if s.Thinking {
			if current := gjson.GetBytes(body, "reasoning"); current.Exists() && current.Type != gjson.Null && !current.IsObject() {
				return nil, ErrInvalid
			}
			del("reasoning_effort")
			del("reasoning.effort")
			if s.Intent.BudgetTokens != nil {
				set("reasoning.enabled", s.Intent.Mode != reasoning.ModeDisabled)
				set("reasoning.max_tokens", *s.Intent.BudgetTokens)
			} else {
				del("reasoning.enabled")
				del("reasoning.max_tokens")
				if s.Intent.Effort != "" {
					set("reasoning_effort", string(s.Intent.Effort))
				}
			}
			if current := gjson.GetBytes(body, "reasoning"); current.IsObject() && len(current.Map()) == 0 {
				del("reasoning")
			}
		}
	}
	if err != nil || len(body) > 1<<20 || !json.Valid(body) {
		return nil, ErrInvalid
	}
	return body, nil
}

func validBody(body []byte) bool {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return false
	}
	nodes := 0
	var walk func(gjson.Result, int, bool) bool
	walk = func(value gjson.Result, depth int, controls bool) bool {
		nodes++
		if nodes > 8192 || depth > 64 {
			return false
		}
		seen, valid := map[string]bool{}, true
		if value.IsObject() || value.IsArray() {
			value.ForEach(func(key, child gjson.Result) bool {
				if value.IsObject() {
					if seen[key.Str] {
						valid = false
						return false
					}
					seen[key.Str] = true
					if controls || depth == 0 {
						switch strings.ToLower(key.Str) {
						case "reasoning", "reasoning_effort", "temperature", "top_p", "enabled", "max_tokens", "effort", "thinking", "output_config", "budget_tokens":
							if key.Str != strings.ToLower(key.Str) {
								valid = false
								return false
							}
						}
					}
				}
				valid = walk(child, depth+1, depth == 0 && (key.Str == "reasoning" || key.Str == "thinking" || key.Str == "output_config"))
				return valid
			})
		}
		return valid
	}
	return walk(gjson.ParseBytes(body), 0, false)
}
