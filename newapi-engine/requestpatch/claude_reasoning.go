// SPDX-License-Identifier: AGPL-3.0-or-later
// Native reasoning follows QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f, relay/common/override.go,
// using the same relaykit renderer. Unknown business JSON remains untouched.
package requestpatch

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// UnambiguousClaudeReasoning rejects case aliases before a renderer adds a
// canonical member. Full duplicate-key/depth limits remain the host's job.
func UnambiguousClaudeReasoning(body []byte) bool {
	valid := true
	controls := map[string][]string{
		"":              {"thinking", "output_config", "max_tokens", "temperature", "top_p", "top_k"},
		"thinking":      {"type", "budget_tokens", "display"},
		"output_config": {"effort"},
	}
	for path, names := range controls {
		value := gjson.ParseBytes(body)
		if path != "" {
			value = value.Get(path)
		}
		value.ForEach(func(key, _ gjson.Result) bool {
			if slices.Contains(names, strings.ToLower(key.Str)) && key.Str != strings.ToLower(key.Str) {
				valid = false
			}
			return valid
		})
		if !valid {
			return false
		}
	}
	return true
}

// ClaudeReasoningEffort distinguishes an explicit native control from an
// absent one, so a non-override request effect preserves client intent.
func ClaudeReasoningEffort(body []byte) (string, bool) {
	if value := gjson.GetBytes(body, "output_config.effort"); value.Exists() && strings.TrimSpace(value.String()) != "" {
		return strings.TrimSpace(value.String()), true
	}
	kind := gjson.GetBytes(body, "thinking.type")
	if kind.String() == "disabled" {
		return "none", true
	}
	if budget := gjson.GetBytes(body, "thinking.budget_tokens"); budget.Exists() {
		if budget.Type != gjson.Number || budget.Float() != float64(budget.Int()) {
			return "", true
		}
		return string(reasoning.EffortFromBudget(int(budget.Int()))), true
	}
	if kind.String() == "enabled" || kind.String() == "adaptive" {
		return "high", true
	}
	return "", kind.Exists() || gjson.GetBytes(body, "output_config.effort").Exists()
}

// SetClaudeReasoning renders one portable request effect for the selected
// upstream model. Only native control fields change; output format schemas,
// thinking display options and all unknown business fields retain their JSON.
func SetClaudeReasoning(body []byte, model, effort string) ([]byte, error) {
	parsed, err := reasoning.ParseEffort(effort)
	if err != nil || parsed == "" || !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() || !UnambiguousClaudeReasoning(body) {
		return nil, errors.New("invalid Claude reasoning effect")
	}
	for _, field := range []string{"thinking", "output_config"} {
		if value := gjson.GetBytes(body, field); value.Exists() && value.Type != gjson.Null && !value.IsObject() {
			return nil, errors.New("invalid Claude reasoning object")
		}
	}
	intent := reasoning.Intent{Mode: reasoning.ModeEnabled, Effort: parsed, Source: reasoning.SourceNative}
	if parsed == reasoning.EffortNone {
		intent.Mode = reasoning.ModeDisabled
	}
	var maxTokens *uint
	if value := gjson.GetBytes(body, "max_tokens"); value.Exists() {
		if value.Type != gjson.Number || value.Float() < 0 || value.Float() > 1e9 || value.Float() != float64(value.Int()) {
			return nil, errors.New("invalid Claude max_tokens")
		}
		limit := uint(value.Int())
		maxTokens = &limit
	}
	// Bedrock's routing identity includes the provider/cross-region prefix;
	// the shared renderer expects the Claude family name, as for Vertex dates.
	renderModel := model
	if index := strings.LastIndex(renderModel, "anthropic.claude-"); index >= 0 {
		renderModel = renderModel[index+len("anthropic."):]
	}
	rendered, err := reasoning.RenderClaude(renderModel, intent, maxTokens, 0.8)
	if err != nil {
		return nil, errors.New("cannot render Claude reasoning effect")
	}
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
	// Clear obsolete strength controls while preserving unrelated options.
	del("thinking.budget_tokens")
	del("thinking.type")
	del("output_config.effort")
	if rendered.Thinking != nil {
		raw, marshalErr := json.Marshal(rendered.Thinking)
		if marshalErr != nil {
			return nil, marshalErr
		}
		gjson.ParseBytes(raw).ForEach(func(key, value gjson.Result) bool {
			set("thinking."+key.String(), value.Value())
			return err == nil
		})
	}
	if rendered.OutputEffort != "" {
		set("output_config.effort", string(rendered.OutputEffort))
	}
	if rendered.MaxTokens != nil {
		set("max_tokens", *rendered.MaxTokens)
	}
	if rendered.ClearSampling {
		del("temperature")
		del("top_p")
		del("top_k")
	} else if rendered.ConstrainThinkingSampling {
		set("temperature", 1)
		del("top_p")
		del("top_k")
	}
	return body, err
}
