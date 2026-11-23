package generation

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/taskprice"
	"github.com/shopspring/decimal"
)

// Only the referenced scalar schema/facts are retained. Localized descriptions,
// credentials, request bodies and unrelated usage data never enter pricing.
type ExpressionField struct {
	Type string   `json:"type,omitempty"`
	Enum []string `json:"enum,omitempty"`
}

type ExpressionSnapshot struct {
	SHA256                string                       `json:"sha256"`
	Schema                map[string]ExpressionField   `json:"schema"`
	Facts                 map[string]any               `json:"facts"`
	EstimatedAmount       decimal.Decimal              `json:"estimated_amount"`
	EstimatedTier         string                       `json:"estimated_tier"`
	Clock                 *taskprice.ClockSnapshot     `json:"clock,omitempty"`
	Probes                *taskprice.ProbeSnapshot     `json:"probes,omitempty"`
	EstimatedRequestRules []taskprice.RequestRuleTrace `json:"estimated_request_rules,omitempty"`
}

type ExpressionCharge struct {
	SHA256       string                       `json:"sha256"`
	Tier         string                       `json:"tier"`
	Amount       decimal.Decimal              `json:"amount"`
	BaseAmount   decimal.Decimal              `json:"base_amount"`
	RequestRules []taskprice.RequestRuleTrace `json:"request_rules,omitempty"`
}

func generationExpressionFacts(program *taskprice.Program, schema map[string]ExpressionField, raw json.RawMessage, fallback map[string]any) (map[string]any, error) {
	var reported map[string]json.RawMessage
	if len(raw) > 1<<20 || json.Unmarshal(raw, &reported) != nil || reported == nil {
		return nil, ErrCostPending
	}
	facts := make(map[string]any)
	for _, key := range program.UsageKeys() {
		field, declared := schema[key]
		if !declared {
			return nil, errors.New("task price expression references an undeclared usage fact")
		}
		if len(field.Enum) > 0 && field.Type != "" {
			return nil, errors.New("task price enum schema cannot declare a scalar type")
		}
		value, exists := reported[key]
		if !exists {
			// A request selector (e.g. video_input or audio) is immutable unless
			// completion explicitly corrects it. Numeric estimates are never
			// substituted for missing observed quantities.
			if field.Type != "number" && fallback[key] != nil {
				encoded, err := json.Marshal(fallback[key])
				if err != nil {
					return nil, ErrCostPending
				}
				value = encoded
			} else {
				return nil, ErrCostPending
			}
		}
		if string(value) == "null" {
			return nil, ErrCostPending
		}
		switch {
		case len(field.Enum) > 0:
			var selector string
			if json.Unmarshal(value, &selector) != nil || !slices.Contains(field.Enum, selector) {
				return nil, ErrCostPending
			}
			facts[key] = selector
		case field.Type == "boolean":
			var flag bool
			if json.Unmarshal(value, &flag) != nil {
				return nil, ErrCostPending
			}
			facts[key] = flag
		case field.Type == "number":
			var number float64
			if json.Unmarshal(value, &number) != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > 1e12 {
				return nil, ErrCostPending
			}
			facts[key] = number
		default:
			return nil, errors.New("unsupported task price usage schema")
		}
	}
	return facts, nil
}

// NewExpressionSnapshot evaluates the estimate, but reserves the
// operator's explicit USD ceiling. A non-linear tier can legitimately change
// when actual quantities arrive; it cannot authorize an unlimited later debit.
func NewExpressionSnapshot(rule PriceRule, metadata map[string]jsplugin.UsageFieldSchema, usage json.RawMessage, multiplier decimal.Decimal) (PriceSnapshot, error) {
	return newExpressionSnapshotAt(rule, metadata, usage, multiplier, time.Now().UTC())
}

// NewExpressionSnapshotWithRequest freezes both the referenced usage facts and
// any referenced scalar param("...") / non-sensitive header("...") probes.
func NewExpressionSnapshotWithRequest(rule PriceRule, metadata map[string]jsplugin.UsageFieldSchema, usage json.RawMessage, requestBody []byte, requestHeaders map[string]string, multiplier decimal.Decimal) (PriceSnapshot, error) {
	return newExpressionSnapshotWithRequestAt(rule, metadata, usage, requestBody, requestHeaders, multiplier, time.Now().UTC())
}

func newExpressionSnapshotAt(rule PriceRule, metadata map[string]jsplugin.UsageFieldSchema, usage json.RawMessage, multiplier decimal.Decimal, at time.Time) (PriceSnapshot, error) {
	return newExpressionSnapshotWithRequestAt(rule, metadata, usage, nil, nil, multiplier, at)
}

func newExpressionSnapshotWithRequestAt(rule PriceRule, metadata map[string]jsplugin.UsageFieldSchema, usage json.RawMessage, requestBody []byte, requestHeaders map[string]string, multiplier decimal.Decimal, at time.Time) (PriceSnapshot, error) {
	if rule.Mode != "expression" || rule.Validate() != nil || multiplier.IsNegative() {
		return PriceSnapshot{}, ErrPriceUnavailable
	}
	if err := taskprice.ValidateSchema(rule.Expression, metadata); err != nil {
		return PriceSnapshot{}, err
	}
	program, err := taskprice.Compile(rule.Expression)
	if err != nil {
		return PriceSnapshot{}, err
	}
	schema := make(map[string]ExpressionField)
	for _, key := range program.UsageKeys() {
		field, exists := metadata[key]
		if !exists {
			return PriceSnapshot{}, errors.New("task price expression references an undeclared usage fact")
		}
		schema[key] = ExpressionField{Type: field.Type, Enum: slices.Clone(field.Enum)}
	}
	facts, err := generationExpressionFacts(program, schema, usage, nil)
	if err != nil {
		return PriceSnapshot{}, err
	}
	clock, err := program.CaptureClock(at)
	if err != nil {
		return PriceSnapshot{}, err
	}
	probes, err := program.CaptureProbes(requestBody, requestHeaders)
	if err != nil {
		return PriceSnapshot{}, err
	}
	evaluated, err := program.EvaluateWithOptions(facts, clock, probes)
	if err != nil {
		return PriceSnapshot{}, err
	}
	amount, tier := evaluated.Amount, evaluated.Tier
	// Compare before monetary rounding: even a sub-cent excess must not
	// authorize spending more than the account's separate reservation.
	base := decimal.Max(rule.Minimum, decimal.NewFromFloat(amount).Add(rule.ServiceFee))
	if base.GreaterThan(*rule.Maximum) {
		return PriceSnapshot{}, ErrPriceExceedsHold
	}
	estimated, err := rule.priceTotal(decimal.NewFromFloat(amount), multiplier)
	if err != nil {
		return PriceSnapshot{}, err
	}
	encoded, err := json.Marshal(rule)
	if err != nil {
		return PriceSnapshot{}, err
	}
	var frozen PriceRule
	if err = json.Unmarshal(encoded, &frozen); err != nil {
		return PriceSnapshot{}, err
	}
	snapshot := PriceSnapshot{
		Rule:            frozen,
		GroupMultiplier: multiplier,
		Hold:            frozen.Maximum.Mul(multiplier).Round(MonetaryScale),
		Quantity:        decimal.NewFromInt(1),
		CreatedAt:       at.UTC(),
		Expression: &ExpressionSnapshot{
			SHA256:                HashPayload([]byte(rule.Expression)),
			Schema:                schema,
			Facts:                 facts,
			EstimatedAmount:       estimated,
			EstimatedTier:         tier,
			Clock:                 clock,
			Probes:                probes,
			EstimatedRequestRules: evaluated.RequestRules,
		},
	}
	if encoded, err := json.Marshal(snapshot.Expression); err != nil || len(encoded) > 64<<10 {
		return PriceSnapshot{}, errors.New("task expression snapshot exceeds 64 KiB")
	}
	return snapshot, nil
}

func (s PriceSnapshot) expressionUsesOnlyFrozenSelectors(program *taskprice.Program) bool {
	if s.Expression == nil {
		return false
	}
	for _, key := range program.UsageKeys() {
		field, ok := s.Expression.Schema[key]
		if !ok || field.Type == "number" || s.Expression.Facts[key] == nil {
			return false
		}
	}
	return true
}

// DeterministicWithoutFinalResponse reports whether settlement can safely finish
// without any final usage/cost observation from the upstream completion payload.
// Numeric usage and cost_plus rules always require observed final facts.
func (s PriceSnapshot) DeterministicWithoutFinalResponse() bool {
	if s.Rule.Mode == "fixed" && s.Rule.Unit == "request" {
		return s.Rule.Validate() == nil
	}
	if s.Rule.Mode == "expression" {
		_, err := s.SettleExpression(nil)
		return err == nil
	}
	return false
}

// SettleExpression must receive only the separate observed completion facts.
// Do not pass the outer usage object: it contains admission estimates too.
func (s PriceSnapshot) SettleExpression(final json.RawMessage) (*ExpressionCharge, error) {
	if s.Rule.Mode != "expression" || s.Expression == nil || s.Expression.SHA256 != HashPayload([]byte(s.Rule.Expression)) {
		return nil, ErrPriceUnavailable
	}
	if err := s.Rule.Validate(); err != nil {
		return nil, err
	}
	program, err := taskprice.Compile(s.Rule.Expression)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(final)) == 0 && s.expressionUsesOnlyFrozenSelectors(program) {
		final = json.RawMessage(`{}`) // Only literal or pure non-numeric selector expressions can settle without a final usage payload.
	}
	facts, err := generationExpressionFacts(program, s.Expression.Schema, final, s.Expression.Facts)
	if err != nil {
		return nil, err
	}
	if s.Expression.Clock != nil && !s.Expression.Clock.At.Equal(s.CreatedAt) {
		return nil, errors.New("task expression time snapshot does not match its admission instant")
	}
	evaluated, err := program.EvaluateWithOptions(facts, s.Expression.Clock, s.Expression.Probes)
	if err != nil {
		return nil, err
	}
	amount, tier := evaluated.Amount, evaluated.Tier
	base := decimal.Max(s.Rule.Minimum, decimal.NewFromFloat(amount).Add(s.Rule.ServiceFee))
	if base.GreaterThan(*s.Rule.Maximum) {
		return nil, ErrPriceExceedsHold
	}
	actual, err := s.Rule.priceTotal(decimal.NewFromFloat(amount), s.GroupMultiplier)
	if err != nil {
		return nil, err
	}
	if actual.GreaterThan(s.Hold) {
		return nil, ErrPriceExceedsHold
	}
	return &ExpressionCharge{SHA256: s.Expression.SHA256, Tier: tier, Amount: actual, BaseAmount: base, RequestRules: evaluated.RequestRules}, nil
}
