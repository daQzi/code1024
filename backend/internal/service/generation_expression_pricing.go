package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/taskprice"
	"github.com/shopspring/decimal"
)

type GenerationExpressionField = generation.ExpressionField
type GenerationExpressionSnapshot = generation.ExpressionSnapshot
type GenerationExpressionCharge = generation.ExpressionCharge

func NewGenerationExpressionSnapshot(rule GenerationPriceRule, metadata map[string]jsplugin.UsageFieldSchema, usage json.RawMessage, multiplier decimal.Decimal) (GenerationPriceSnapshot, error) {
	return generation.NewExpressionSnapshot(rule, metadata, usage, multiplier)
}

func NewGenerationExpressionSnapshotWithRequest(rule GenerationPriceRule, metadata map[string]jsplugin.UsageFieldSchema, usage json.RawMessage, requestBody []byte, requestHeaders map[string]string, multiplier decimal.Decimal) (GenerationPriceSnapshot, error) {
	return generation.NewExpressionSnapshotWithRequest(rule, metadata, usage, requestBody, requestHeaders, multiplier)
}

type generationRequestHeadersKey struct{}

// WithGenerationRequestHeaders captures bounded, non-sensitive HTTP request
// headers for task price expressions (header("...")). Credential-bearing
// headers are stripped before crossing the service boundary.
func WithGenerationRequestHeaders(ctx context.Context, headers http.Header) context.Context {
	if len(headers) == 0 {
		return ctx
	}
	safe := make(map[string]string)
	for name, values := range headers {
		if len(safe) >= 128 {
			break
		}
		lower := strings.ToLower(strings.TrimSpace(name))
		if !taskprice.ValidHeaderName(lower) || len(values) == 0 {
			continue
		}
		val := strings.TrimSpace(values[0])
		// Preserve values up to 257 bytes so CaptureProbes fails closed if a
		// referenced header exceeds its 256-byte limit instead of truncating.
		if len(val) > 257 {
			val = val[:257]
		}
		safe[lower] = val
	}
	if len(safe) == 0 {
		return ctx
	}
	return context.WithValue(ctx, generationRequestHeadersKey{}, safe)
}

func generationRequestHeaders(ctx context.Context) map[string]string {
	headers, _ := ctx.Value(generationRequestHeadersKey{}).(map[string]string)
	return headers
}

// Account edits may preserve an unchanged legacy/stale rule. New tasks always
// validate their actual current model schema again before reserving funds.
func validateAccountTaskExpressions(account *Account, version *NewAPIPluginVersion, models []string, rules map[string]GenerationPriceRule, previous []*Account) error {
	var saved map[string]GenerationPriceRule
	if len(previous) > 0 && previous[0] != nil {
		if raw, err := json.Marshal(previous[0].Credentials[NewAPIVideoPricingKey]); err == nil {
			_ = json.Unmarshal(raw, &saved)
		}
	}
	projected := *account
	projected.Credentials = maps.Clone(account.Credentials)
	projected.Credentials[newAPIPluginVideoModelsKey] = models
	publicModels := projected.NewAPIVideoModels()
	slices.Sort(publicModels)
	keys := make([]string, 0, len(rules))
	for key := range rules {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		rule := rules[key]
		if rule.Mode != "expression" {
			continue
		}
		if old, exists := saved[key]; exists && reflect.DeepEqual(old, rule) {
			continue
		}
		matched := false
		for _, model := range publicModels {
			if key != model && key != "*" {
				continue
			}
			if key == "*" {
				if _, overridden := rules[model]; overridden {
					continue
				}
			}
			matched = true
			schema, _ := version.Meta.UsageForModels(projected.GetMappedModel(model), model)
			if err := taskprice.ValidateSchema(rule.Expression, schema); err != nil {
				return fmt.Errorf("模型 %s 插件 %s: %w", model, version.Meta.Key, err)
			}
		}
		if !matched {
			// A default that is fully covered by exact rules is dormant. Its
			// schema is checked when a future task actually selects it.
			if key == "*" && len(publicModels) > 0 {
				continue
			}
			return errors.New("新增或修改的 Task 表达式需要匹配当前账号的具体视频模型")
		}
	}
	return nil
}
