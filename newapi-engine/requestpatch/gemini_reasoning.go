// SPDX-License-Identifier: AGPL-3.0-or-later
// Native controls follow the fixed New API RelayKit renderer, as for Claude.
package requestpatch

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func geminiGenerationPaths(body []byte) (string, string, string, bool) {
	camel, snake := gjson.GetBytes(body, "generationConfig"), gjson.GetBytes(body, "generation_config")
	if camel.Exists() && snake.Exists() {
		return "", "", "", false
	}
	path, thinking, limit := "generationConfig", "thinkingConfig", "maxOutputTokens"
	if snake.Exists() {
		path, thinking, limit = "generation_config", "thinking_config", "max_output_tokens"
	}
	config := gjson.GetBytes(body, path)
	if config.Get("thinkingConfig").Exists() {
		thinking = "thinkingConfig"
	} else if config.Get("thinking_config").Exists() {
		thinking = "thinking_config"
	}
	if config.Get("maxOutputTokens").Exists() {
		limit = "maxOutputTokens"
	} else if config.Get("max_output_tokens").Exists() {
		limit = "max_output_tokens"
	}
	return path, thinking, limit, true
}

// Reject aliases that could give the hook and a case-insensitive native DTO
// different controls. Both documented JSON spellings remain supported.
func UnambiguousGeminiReasoning(body []byte) bool {
	path, thinking, _, valid := geminiGenerationPaths(body)
	if !valid {
		return false
	}
	fields := map[string][]string{
		"":                    {"generationConfig", "generation_config"},
		path:                  {"thinkingConfig", "thinking_config", "maxOutputTokens", "max_output_tokens", "candidateCount", "candidate_count", "responseModalities", "response_modalities", "imageConfig", "image_config"},
		path + "." + thinking: {"thinkingBudget", "thinking_budget", "thinkingLevel", "thinking_level", "includeThoughts", "include_thoughts"},
	}
	for object, names := range fields {
		value := gjson.ParseBytes(body)
		if object != "" {
			value = value.Get(object)
		}
		seen := map[string]bool{}
		value.ForEach(func(key, _ gjson.Result) bool {
			for _, name := range names {
				if strings.EqualFold(key.Str, name) {
					canonical := strings.ToLower(strings.ReplaceAll(name, "_", ""))
					if key.Str != name || seen[canonical] {
						valid = false
					}
					seen[canonical] = true
					break
				}
			}
			return valid
		})
		if !valid {
			return false
		}
	}
	return true
}

func GeminiReasoningEffort(body []byte) (string, bool) {
	hasControl := false
	for _, path := range []string{"generationConfig.thinkingConfig", "generationConfig.thinking_config", "generation_config.thinkingConfig", "generation_config.thinking_config"} {
		for _, field := range []string{"thinkingLevel", "thinking_level"} {
			if value := gjson.GetBytes(body, path+"."+field); value.Exists() {
				hasControl = true
				if value.Type == gjson.String && strings.TrimSpace(value.Str) != "" {
					return strings.TrimSpace(value.Str), true
				}
			}
		}
		for _, field := range []string{"thinkingBudget", "thinking_budget"} {
			if value := gjson.GetBytes(body, path+"."+field); value.Exists() {
				if value.Type != gjson.Number || value.Float() != float64(value.Int()) {
					return "", true
				}
				return string(reasoning.EffortFromBudget(int(value.Int()))), true
			}
		}
	}
	return "", hasControl
}

// SetGeminiReasoning changes native strength controls using the selected
// model's renderer, retaining includeThoughts, response schema and unknown JSON.
func SetGeminiReasoning(body []byte, model, effort string) ([]byte, error) {
	parsed, err := reasoning.ParseEffort(effort)
	if err != nil || parsed == "" || !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() || !UnambiguousGeminiReasoning(body) {
		return nil, errors.New("invalid Gemini reasoning effect")
	}
	path, thinking, limitField, _ := geminiGenerationPaths(body)
	for _, object := range []string{path, path + "." + thinking} {
		if value := gjson.GetBytes(body, object); value.Exists() && value.Type != gjson.Null && !value.IsObject() {
			return nil, errors.New("invalid Gemini reasoning object")
		}
	}
	intent := reasoning.Intent{Mode: reasoning.ModeEnabled, Effort: parsed, Source: reasoning.SourceNative}
	if parsed == reasoning.EffortNone {
		intent.Mode = reasoning.ModeDisabled
	}
	for _, field := range []string{"includeThoughts", "include_thoughts"} {
		if value := gjson.GetBytes(body, path+"."+thinking+"."+field); value.Exists() {
			if value.Type != gjson.True && value.Type != gjson.False {
				return nil, errors.New("invalid Gemini includeThoughts")
			}
			include := value.Bool()
			intent.IncludeThoughts = &include
		}
	}
	var maxTokens *uint
	if value := gjson.GetBytes(body, path+"."+limitField); value.Exists() {
		if value.Type != gjson.Number || value.Float() < 0 || value.Float() > 1e9 || value.Float() != float64(value.Int()) {
			return nil, errors.New("invalid Gemini maxOutputTokens")
		}
		limit := uint(value.Int())
		maxTokens = &limit
	}
	rendered, err := reasoning.RenderGemini(model, intent, maxTokens, 0.8)
	if err != nil {
		return nil, errors.New("cannot render Gemini reasoning effect")
	}
	for _, diagnostic := range rendered.Diagnostics {
		if diagnostic.Code == "gemini_unknown_capability" {
			return body, nil // The fixed renderer cannot synthesize unknown vendor controls.
		}
	}
	for _, field := range []string{"thinkingLevel", "thinking_level", "thinkingBudget", "thinking_budget", "includeThoughts", "include_thoughts"} {
		body, err = sjson.DeleteBytes(body, path+"."+thinking+"."+field)
		if err != nil {
			return nil, err
		}
	}
	if rendered.Config != nil {
		raw, marshalErr := json.Marshal(rendered.Config)
		if marshalErr != nil {
			return nil, marshalErr
		}
		gjson.ParseBytes(raw).ForEach(func(key, value gjson.Result) bool {
			field := key.Str
			if thinking == "thinking_config" {
				field = strings.NewReplacer("thinkingLevel", "thinking_level", "thinkingBudget", "thinking_budget", "includeThoughts", "include_thoughts").Replace(field)
			}
			body, err = sjson.SetBytes(body, path+"."+thinking+"."+field, value.Value())
			return err == nil
		})
	}
	return body, err
}
