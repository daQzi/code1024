// SPDX-License-Identifier: AGPL-3.0-or-later
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f,
// pkg/billingexpr/compile.go, run.go and task_usage_test.go. See ../NOTICE.md.
// This bounded Task-only environment returns USD, not relay token quota.
package taskprice

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // Independent hosts need the same IANA names in minimal images.

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/vm"
)

const MaxExpressionBytes = 4096

const (
	requestRuleTraceFunction    = "_trace"
	requestRuleTraceIntFunction = "_trace_int"
)

type Program struct {
	program      *vm.Program
	keys         []string
	paramPaths   []string
	headerNames  []string
	requestRules []RequestRuleTrace
	zones        map[string]*time.Location
}

var cache = struct {
	sync.RWMutex
	entries map[string]*Program
}{entries: make(map[string]*Program)}

func environment(facts map[string]any, tier *string, clock *ClockSnapshot, probes *ProbeSnapshot, rules []RequestRuleTrace) map[string]any {
	return map[string]any{
		"u": func(name string) any { return facts[strings.TrimSpace(name)] },
		"param": func(path string) any {
			if probes == nil || probes.Params == nil {
				return nil
			}
			return probes.Params[strings.TrimSpace(path)]
		},
		"header": func(name string) string {
			if probes == nil || probes.Headers == nil {
				return ""
			}
			return probes.Headers[strings.ToLower(strings.TrimSpace(name))]
		},
		"tier": func(name string, value float64) float64 {
			if tier != nil {
				*tier = name
			}
			return value
		},
		"has": func(source any, part string) bool {
			return source != nil && part != "" && strings.Contains(fmt.Sprint(source), part)
		},
		requestRuleTraceFunction: func(ruleIndex int, matched bool, multiplier float64) float64 {
			if matched && ruleIndex >= 0 && ruleIndex < len(rules) {
				rules[ruleIndex].Matched = true
			}
			if matched {
				return multiplier
			}
			return 1
		},
		requestRuleTraceIntFunction: func(ruleIndex int, matched bool, multiplier int) int {
			if matched && ruleIndex >= 0 && ruleIndex < len(rules) {
				rules[ruleIndex].Matched = true
			}
			if matched {
				return multiplier
			}
			return 1
		},
		"max": math.Max, "min": math.Min, "abs": math.Abs, "ceil": math.Ceil, "floor": math.Floor,
		"hour":    func(zone string) int { return clock.inZone(zone).Hour() },
		"minute":  func(zone string) int { return clock.inZone(zone).Minute() },
		"weekday": func(zone string) int { return int(clock.inZone(zone).Weekday()) },
		"month":   func(zone string) int { return int(clock.inZone(zone).Month()) },
		"day":     func(zone string) int { return clock.inZone(zone).Day() },
	}
}

// Compile keeps the baseline's u/tier/param/header/time arithmetic and
// conditional syntax with explicit host limits. Sensitive credential headers,
// relay token variables, fixed(), dynamic probe names, collections, ranges,
// predicates and methods are rejected.
func Compile(expression string) (*Program, error) {
	if len(expression) == 0 || len(expression) > MaxExpressionBytes {
		return nil, errors.New("task price expression must contain 1 to 4096 bytes")
	}
	cache.RLock()
	compiled := cache.entries[expression]
	cache.RUnlock()
	if compiled != nil {
		return compiled, nil
	}
	body := strings.TrimPrefix(expression, "v1:")
	patcher := &taskRequestRulePatcher{}
	program, err := expr.Compile(body, expr.Env(environment(nil, nil, nil, nil, nil)), expr.Patch(patcher), expr.AsFloat64(), expr.Optimize(false), expr.DisableAllBuiltins(), expr.MaxNodes(512))
	if err != nil || patcher.restrictedIdentifier != "" {
		return nil, errors.New("invalid or unsupported task price expression")
	}
	visitor := &scalarVisitor{
		keys:    make(map[string]bool),
		params:  make(map[string]bool),
		headers: make(map[string]bool),
		zones:   make(map[string]*time.Location),
	}
	node := program.Node()
	ast.Walk(&node, visitor)
	if visitor.invalid {
		return nil, errors.New("task pricing supports scalar expressions with literal usage names, safe request probes and time zones only")
	}
	keys := sortedKeys(visitor.keys)
	paramPaths := sortedKeys(visitor.params)
	headerNames := sortedKeys(visitor.headers)
	compiled = &Program{
		program:      program,
		keys:         keys,
		paramPaths:   paramPaths,
		headerNames:  headerNames,
		requestRules: patcher.requestRules,
		zones:        visitor.zones,
	}
	cache.Lock()
	if len(cache.entries) >= 128 {
		cache.entries = make(map[string]*Program)
	}
	cache.entries[expression] = compiled
	cache.Unlock()
	return compiled, nil
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

type scalarVisitor struct {
	keys    map[string]bool
	params  map[string]bool
	headers map[string]bool
	zones   map[string]*time.Location
	invalid bool
}

func (v *scalarVisitor) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.NilNode, *ast.BoolNode, *ast.IntegerNode, *ast.FloatNode, *ast.IdentifierNode:
	case *ast.StringNode:
		if len(n.Value) > 256 {
			v.invalid = true
		}
	case *ast.UnaryNode:
		if !slices.Contains([]string{"+", "-", "!", "not"}, n.Operator) {
			v.invalid = true
		}
	case *ast.BinaryNode:
		if !slices.Contains([]string{"+", "-", "*", "/", "%", "^", "**", "==", "!=", ">", ">=", "<", "<=", "&&", "||", "and", "or", "??"}, n.Operator) {
			v.invalid = true
		}
	case *ast.ConditionalNode:
	case *ast.CallNode:
		callee, ok := n.Callee.(*ast.IdentifierNode)
		if !ok || !slices.Contains([]string{"u", "param", "header", "tier", "has", "max", "min", "abs", "ceil", "floor", "hour", "minute", "weekday", "month", "day", requestRuleTraceFunction, requestRuleTraceIntFunction}, callee.Value) {
			v.invalid = true
			return
		}
		switch callee.Value {
		case "u":
			if len(n.Arguments) != 1 {
				v.invalid = true
				return
			}
			literal, ok := n.Arguments[0].(*ast.StringNode)
			if !ok || strings.TrimSpace(literal.Value) == "" || len(literal.Value) > 64 {
				v.invalid = true
				return
			}
			v.keys[strings.TrimSpace(literal.Value)] = true
		case "param":
			if len(n.Arguments) != 1 {
				v.invalid = true
				return
			}
			literal, ok := n.Arguments[0].(*ast.StringNode)
			if !ok || !ValidParamPath(literal.Value) {
				v.invalid = true
				return
			}
			v.params[strings.TrimSpace(literal.Value)] = true
		case "header":
			if len(n.Arguments) != 1 {
				v.invalid = true
				return
			}
			literal, ok := n.Arguments[0].(*ast.StringNode)
			if !ok || !ValidHeaderName(literal.Value) {
				v.invalid = true
				return
			}
			v.headers[strings.ToLower(strings.TrimSpace(literal.Value))] = true
		case "hour", "minute", "weekday", "month", "day":
			if len(n.Arguments) != 1 {
				v.invalid = true
				return
			}
			literal, ok := n.Arguments[0].(*ast.StringNode)
			if !ok || len(literal.Value) > 128 {
				v.invalid = true
				return
			}
			zone := strings.TrimSpace(literal.Value)
			location, err := time.LoadLocation(zone)
			if err != nil || zone == "" {
				location = time.UTC // The pinned New API fallback, not the host's local zone.
			}
			v.zones[zone] = location
		}
	default:
		v.invalid = true
	}
}

type taskRequestRulePatcher struct {
	requestRules         []RequestRuleTrace
	restrictedIdentifier string
}

func (p *taskRequestRulePatcher) Visit(node *ast.Node) {
	if identifier, ok := (*node).(*ast.IdentifierNode); ok {
		switch identifier.Value {
		case requestRuleTraceFunction, requestRuleTraceIntFunction:
			p.restrictedIdentifier = identifier.Value
		}
		return
	}
	conditional, ok := (*node).(*ast.ConditionalNode)
	if !ok || !conditional.Ternary || !usesTaskRequestProbe(conditional.Cond) {
		return
	}
	multiplier, ok := taskRuleNumber(conditional.Exp1)
	fallback, fallbackOK := taskRuleNumber(conditional.Exp2)
	if !ok || !fallbackOK || fallback != 1 {
		return
	}
	ruleIndex := len(p.requestRules)
	p.requestRules = append(p.requestRules, RequestRuleTrace{
		Cond:       conditional.Cond.String(),
		Multiplier: multiplier,
	})
	traceFunction := requestRuleTraceFunction
	var multiplierNode ast.Node = &ast.FloatNode{Value: multiplier}
	if _, multiplierIsInt := conditional.Exp1.(*ast.IntegerNode); multiplierIsInt {
		if _, fallbackIsInt := conditional.Exp2.(*ast.IntegerNode); fallbackIsInt {
			traceFunction = requestRuleTraceIntFunction
			multiplierNode = conditional.Exp1
		}
	}
	ast.Patch(node, &ast.CallNode{
		Callee: &ast.IdentifierNode{Value: traceFunction},
		Arguments: []ast.Node{
			&ast.IntegerNode{Value: ruleIndex},
			conditional.Cond,
			multiplierNode,
		},
	})
}

func taskRuleNumber(node ast.Node) (float64, bool) {
	switch value := node.(type) {
	case *ast.IntegerNode:
		return float64(value.Value), true
	case *ast.FloatNode:
		return value.Value, true
	default:
		return 0, false
	}
}

func usesTaskRequestProbe(node ast.Node) bool {
	return ast.Find(node, func(part ast.Node) bool {
		id, ok := part.(*ast.IdentifierNode)
		return ok && slices.Contains([]string{"param", "header", "hour", "minute", "weekday", "month", "day"}, id.Value)
	}) != nil
}

func (p *Program) UsageKeys() []string { return slices.Clone(p.keys) }

func (p *Program) Evaluate(facts map[string]any) (float64, string, error) {
	clock, err := p.CaptureClock(time.Now().UTC())
	if err != nil {
		return 0, "", err
	}
	return p.EvaluateWithClock(facts, clock)
}

// EvaluateWithClock is used for both reservation and durable settlement. Time
// probes must never read the worker's current clock when finishing an old task.
func (p *Program) EvaluateWithClock(facts map[string]any, clock *ClockSnapshot) (float64, string, error) {
	res, err := p.EvaluateWithOptions(facts, clock, nil)
	return res.Amount, res.Tier, err
}

// EvaluateWithOptions runs the compiled program against bounded scalar usage
// facts, a frozen admission clock, and frozen safe request probes, returning
// the USD amount, matched tier, and request-rule multiplier traces.
func (p *Program) EvaluateWithOptions(facts map[string]any, clock *ClockSnapshot, probes *ProbeSnapshot) (EvaluateResult, error) {
	if err := p.validateClock(clock); err != nil {
		return EvaluateResult{}, err
	}
	if err := p.validateProbes(probes); err != nil {
		return EvaluateResult{}, err
	}
	// Only bounded primitive values enter the VM. Never expose a host object,
	// method, account config, mutable map or slice to an expression.
	values := make(map[string]any, len(p.keys))
	for _, key := range p.keys {
		value := facts[key]
		switch typed := value.(type) {
		case nil, bool:
		case string:
			if len(typed) > 256 {
				return EvaluateResult{}, errors.New("task price selector exceeds limit")
			}
		case float64:
			if math.IsNaN(typed) || math.IsInf(typed, 0) || typed < 0 || typed > 1e12 {
				return EvaluateResult{}, errors.New("task price usage must be finite and between zero and 1e12")
			}
		default:
			return EvaluateResult{}, errors.New("task price usage must contain scalar facts only")
		}
		values[key] = value
	}
	var tier string
	rules := append([]RequestRuleTrace(nil), p.requestRules...)
	machine := vm.VM{MemoryBudget: 2048}
	result, err := machine.Run(p.program, environment(values, &tier, clock, probes, rules))
	if err != nil {
		return EvaluateResult{}, errors.New("task price expression cannot evaluate its usage facts")
	}
	amount, ok := result.(float64)
	if !ok || math.IsInf(amount, 0) || math.IsNaN(amount) || amount < 0 || amount > 1e12 || len(tier) > 256 {
		return EvaluateResult{}, errors.New("task price expression must return a finite non-negative USD amount")
	}
	return EvaluateResult{Amount: amount, Tier: tier, RequestRules: rules}, nil
}
