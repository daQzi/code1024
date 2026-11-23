// SPDX-License-Identifier: AGPL-3.0-or-later
// Adapted from QuantumNous/new-api setting/billing_setting/tiered_billing.go
// at 48f9df96ea7c68eae04a0f3687869f232f48929f. See ../NOTICE.md.
package taskprice

import (
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/codec"
	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/limits"
)

const maxSchemaVectors = 64

type usageDimension struct {
	name   string
	values []any
	enum   bool
}

// ValidateSchema checks every literal usage reference, including unchosen AST
// branches, and evaluates bounded boundary/selector vectors before a rule is
// accepted. It samples the referenced schema only; unused fields cannot change
// the expression and must not consume its coverage budget. No facts or prices
// are invented for an actual request, and this is not an exhaustive proof.
func ValidateSchema(expression string, schema map[string]jsplugin.UsageFieldSchema) error {
	program, err := Compile(expression)
	if err != nil {
		return err
	}
	dimensions := make([]usageDimension, 0, len(program.keys))
	for _, name := range program.keys {
		field, ok := schema[name]
		if !ok {
			return fmt.Errorf("task price usage key %q is not declared by the plugin", name)
		}
		dimension := usageDimension{name: name}
		switch {
		case len(field.Enum) > 0:
			if field.Type != "" || field.Unit != "" {
				return errors.New("task price enum cannot declare a type or unit")
			}
			dimension.enum = true
			if len(field.Enum) > maxSchemaVectors {
				// This dimension alone exceeds the total budget. Read its
				// endpoints without first allocating an unbounded value list.
				dimension.values = []any{field.Enum[0], field.Enum[len(field.Enum)-1]}
			} else {
				for _, value := range field.Enum {
					dimension.values = append(dimension.values, value)
				}
			}
		case field.Type == "boolean" && field.Unit == "" && field.Enum == nil:
			dimension.values = []any{false, true}
		case field.Type == "number" && field.Enum == nil:
			var ceiling float64
			switch field.Unit {
			case "second":
				ceiling = limits.MaxTaskDurationSeconds
			case "count":
				ceiling = contract.MaxImageN
			case "token", "credit":
				ceiling = codec.MaxQuota
			default:
				return errors.New("unsupported task price numeric unit")
			}
			dimension.values = []any{float64(0), float64(1), ceiling}
		default:
			return errors.New("unsupported task price usage schema")
		}
		dimensions = append(dimensions, dimension)
	}
	if usageCombinations(dimensions) > maxSchemaVectors {
		for i := range dimensions {
			if dimension := &dimensions[i]; dimension.enum && len(dimension.values) > 2 {
				dimension.values = []any{dimension.values[0], dimension.values[len(dimension.values)-1]}
			}
		}
	}
	vectors := make([]map[string]any, 0, maxSchemaVectors)
	var visit func(int, map[string]any)
	visit = func(index int, current map[string]any) {
		if len(vectors) >= maxSchemaVectors {
			return
		}
		if index == len(dimensions) {
			vectors = append(vectors, maps.Clone(current))
			return
		}
		dimension := dimensions[index]
		for _, value := range dimension.values {
			current[dimension.name] = value
			visit(index+1, current)
		}
		delete(current, dimension.name)
	}
	visit(0, make(map[string]any, len(dimensions)))
	if usageCombinations(dimensions) > maxSchemaVectors {
		last := make(map[string]any, len(dimensions))
		for _, dimension := range dimensions {
			last[dimension.name] = dimension.values[len(dimension.values)-1]
		}
		vectors[len(vectors)-1] = last
	}
	clock, err := program.CaptureClock(time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		return err
	}
	var probes *ProbeSnapshot
	if program.HasProbes() {
		probes = &ProbeSnapshot{Params: map[string]any{}, Headers: map[string]string{}}
	}
	for _, facts := range vectors {
		if _, err := program.EvaluateWithOptions(facts, clock, probes); err != nil {
			return fmt.Errorf("task price expression fails a schema boundary or selector: %w", err)
		}
	}
	for _, clock := range program.schemaClocks() {
		for _, facts := range []map[string]any{vectors[0], vectors[len(vectors)-1]} {
			if _, err := program.EvaluateWithOptions(facts, clock, probes); err != nil {
				return fmt.Errorf("task price expression fails a calendar boundary: %w", err)
			}
		}
	}
	return nil
}

func usageCombinations(dimensions []usageDimension) int {
	total := 1
	for _, dimension := range dimensions {
		if len(dimension.values) == 0 {
			return 0
		}
		if total > maxSchemaVectors/len(dimension.values) {
			return maxSchemaVectors + 1
		}
		total *= len(dimension.values)
	}
	return total
}
