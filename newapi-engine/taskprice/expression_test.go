package taskprice

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/require"
)

func TestTaskExpressionBaselineSyntaxAndUSD(t *testing.T) {
	for _, tc := range []struct {
		expression string
		facts      map[string]any
		amount     float64
		tier       string
	}{
		{`tier("1080p", u("seconds") * (u("resolution") == "1080p" ? 0.4 : 0.2))`, map[string]any{"seconds": float64(10), "resolution": "1080p"}, 4, "1080p"},
		{`v1:tier("audio", (u("audio") ? 0.4 : 0.2) * u("seconds"))`, map[string]any{"seconds": float64(3), "audio": true}, 1.2, "audio"},
		{`u("seconds") > 5 ? tier("long", 2) : tier("short", 1)`, map[string]any{"seconds": float64(6)}, 2, "long"},
		{`tier("basic", max(0.5, ceil(u("seconds") / 5) * 0.2))`, map[string]any{"seconds": float64(6)}, .5, "basic"},
		{`tier("request", 0)`, nil, 0, "request"},
		{`0.25`, nil, .25, ""},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			p, err := Compile(tc.expression)
			require.NoError(t, err)
			amount, tier, err := p.Evaluate(tc.facts)
			require.NoError(t, err)
			require.InDelta(t, tc.amount, amount, 1e-12)
			require.Equal(t, tc.tier, tier)
		})
	}
}

func TestTaskExpressionSchemaBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		expression string
		schema     map[string]jsplugin.UsageFieldSchema
		valid      bool
	}{
		{"constant", `tier("free", 0)`, nil, true},
		{"undeclared unchosen branch", `false ? u("secret") : 1`, nil, false},
		{"seconds boundary", `u("n") == 3600 ? -1 : 1`, map[string]jsplugin.UsageFieldSchema{"n": {Type: "number", Unit: "second"}}, false},
		{"count boundary", `u("n") == 128 ? -1 : 1`, map[string]jsplugin.UsageFieldSchema{"n": {Type: "number", Unit: "count"}}, false},
		{"token boundary", `u("n") == 2147483647 ? -1 : 1`, map[string]jsplugin.UsageFieldSchema{"n": {Type: "number", Unit: "token"}}, false},
		{"credit boundary", `u("n") == 2147483647 ? -1 : 1`, map[string]jsplugin.UsageFieldSchema{"n": {Type: "number", Unit: "credit"}}, false},
		{"divide zero", `1 / u("n")`, map[string]jsplugin.UsageFieldSchema{"n": {Type: "number", Unit: "count"}}, false},
		{"measured zero", `u("n") * 0.2`, map[string]jsplugin.UsageFieldSchema{"n": {Type: "number", Unit: "count"}}, true},
		{"boolean enum combination", `u("audio") && u("size") == "4k" ? -1 : 1`, map[string]jsplugin.UsageFieldSchema{"audio": {Type: "boolean"}, "size": {Enum: []string{"720p", "1080p", "4k"}}}, false},
		{"enum type conflict", `u("size") == "4k" ? 1 : 2`, map[string]jsplugin.UsageFieldSchema{"size": {Enum: []string{"4k"}, Type: "string"}}, false},
		{"enum unit conflict", `u("size") == "4k" ? 1 : 2`, map[string]jsplugin.UsageFieldSchema{"size": {Enum: []string{"4k"}, Unit: "count"}}, false},
		{"empty enum", `u("size") == "4k" ? 1 : 2`, map[string]jsplugin.UsageFieldSchema{"size": {Enum: []string{}}}, false},
		{"boolean unit", `u("audio") ? 1 : 2`, map[string]jsplugin.UsageFieldSchema{"audio": {Type: "boolean", Unit: "count"}}, false},
		{"unknown unit", `u("n")`, map[string]jsplugin.UsageFieldSchema{"n": {Type: "number", Unit: "dollar"}}, false},
		{"missing unit", `u("n")`, map[string]jsplugin.UsageFieldSchema{"n": {Type: "number"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSchema(tc.expression, tc.schema)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestTaskExpressionSchemaSamplingBudget(t *testing.T) {
	schema := make(map[string]jsplugin.UsageFieldSchema)
	var conditions []string
	for i := range 7 {
		name := fmt.Sprintf("selector%d", i)
		schema[name] = jsplugin.UsageFieldSchema{Enum: []string{"first", "middle", "last"}}
		conditions = append(conditions, fmt.Sprintf(`u(%q) == "last"`, name))
	}
	// Even after 3^7 combinations collapse and truncate to 64, all final
	// selectors must be exercised together.
	require.Error(t, ValidateSchema(strings.Join(conditions, " && ")+` ? -1 : 1`, schema))
	for i := range 200 {
		schema[fmt.Sprintf("unused%d", i)] = jsplugin.UsageFieldSchema{Type: "boolean"}
	}
	// An unreferenced schema must not squeeze a referenced middle selector
	// out of the budget. These unused fields do not affect the expression.
	require.Error(t, ValidateSchema(`u("selector0") == "middle" ? -1 : 1`, schema))
	values := make([]string, 10000)
	for i := range values {
		values[i] = fmt.Sprintf("v%d", i)
	}
	schema["large"] = jsplugin.UsageFieldSchema{Enum: values}
	require.Error(t, ValidateSchema(`u("large") == "v9999" ? -1 : 1`, schema))
	// Sampling does not prove arbitrary branch safety. The actual request
	// still evaluates its real facts and applies the frozen charge ceiling.
	require.NoError(t, ValidateSchema(`u("selector0") == "first" ? 0 : 1`, schema))
}

func TestTaskExpressionRejectsUnboundedOrUnsupportedLanguage(t *testing.T) {
	for _, expression := range []string{
		``, strings.Repeat("1+", 2048) + `1`, `v2:tier("request", 1)`,
		`fixed(1)`, `p * 2 + c * 3`, `header("Authorization") == "x" ? 1 : 0`, `header("Cookie") == "x" ? 1 : 0`, `header("X-Api-Key") == "x" ? 1 : 0`,
		`header("X-Auth-Token") == "x" ? 1 : 0`, `header("")`, `header(u("h"))`,
		`param("")`, `param("items.*.id")`, `param("..bad")`, `param(u("key"))`, `hour(u("zone"))`,
		`u(u("key"))`, `u(" ")`, `u("` + strings.Repeat("a", 65) + `")`,
		`false ? sum(1..1000000000) : 0`, `len([1,2,3])`, `let x = 3; x + 1`,
		`[u("a")][0]`, `{"a": 1}.a`, `"a" matches "a.*" ? 1 : 0`,
		`_trace(0, true, 2.0)`, `_trace_int(0, true, 2)`,
	} {
		t.Run(expression, func(t *testing.T) {
			_, err := Compile(expression)
			require.Error(t, err)
		})
	}
	p, err := Compile(`1 / u("n")`)
	require.NoError(t, err)
	_, _, err = p.Evaluate(map[string]any{"n": float64(0)})
	require.Error(t, err)
	p, err = Compile(`u("n")`)
	require.NoError(t, err)
	for _, value := range []any{map[string]any{"method": "no"}, []any{1}, -1.0, 1e13, strings.Repeat("a", 257)} {
		_, _, err = p.Evaluate(map[string]any{"n": value})
		require.Error(t, err)
	}
}

func TestTaskExpressionTimeProbesUseOneFrozenInstant(t *testing.T) {
	at := time.Date(2026, time.November, 21, 23, 59, 0, 0, time.UTC)
	for _, tc := range []struct {
		expression string
		want       float64
	}{
		{`hour("Asia/Shanghai")`, 7}, {`minute("UTC")`, 59},
		{`weekday("Asia/Shanghai")`, 0}, {`day("Asia/Shanghai")`, 22}, {`month("UTC")`, 11},
		{`hour("")`, 23}, {`hour(" Invalid/Zone ")`, 23}, {`hour(" UTC ")`, 23},
		{`hour("Asia/Shanghai") < 12 ? tier("morning", u("seconds") * 0.1) : tier("later", u("seconds") * 0.2)`, .5},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			p, err := Compile(tc.expression)
			require.NoError(t, err)
			clock, err := p.CaptureClock(at)
			require.NoError(t, err)
			amount, _, err := p.EvaluateWithClock(map[string]any{"seconds": float64(5)}, clock)
			require.NoError(t, err)
			require.InDelta(t, tc.want, amount, 1e-12)
			_, _, err = p.EvaluateWithClock(nil, nil)
			require.Error(t, err, "missing snapshots must not fall back to the settlement clock")
			_, err = p.CaptureClock(time.Time{})
			require.Error(t, err)
		})
	}
	for _, expression := range []string{`hour()`, `hour("UTC", "UTC")`, `hour("` + strings.Repeat("x", 129) + `")`, `day(u("tz"))`} {
		_, err := Compile(expression)
		require.Error(t, err)
	}
}

func TestTaskExpressionClockOffsetsAndSnapshotsAreIsolated(t *testing.T) {
	p, err := Compile(`hour("America/New_York")`)
	require.NoError(t, err)
	winter, err := p.CaptureClock(time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	summer, err := p.CaptureClock(time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, -5*3600, winter.Offsets["America/New_York"])
	require.Equal(t, -4*3600, summer.Offsets["America/New_York"])
	for _, tc := range []struct {
		clock *ClockSnapshot
		want  float64
	}{{winter, 7}, {summer, 8}} {
		amount, _, err := p.EvaluateWithClock(nil, tc.clock)
		require.NoError(t, err)
		require.Equal(t, tc.want, amount)
	}
	for _, clock := range []*ClockSnapshot{{At: winter.At}, {At: winter.At, Offsets: map[string]int{"UTC": 0}}, {At: winter.At, Offsets: map[string]int{"America/New_York": 25 * 3600}}} {
		_, _, err := p.EvaluateWithClock(nil, clock)
		require.Error(t, err)
	}
	for _, expression := range []string{`hour("UTC") == 23 ? -1 : 1`, `minute("UTC") == 59 ? -1 : 1`, `weekday("UTC") == 6 ? -1 : 1`, `month("UTC") == 12 ? -1 : 1`, `day("UTC") == 31 ? -1 : 1`} {
		require.Error(t, ValidateSchema(expression, nil), "either usage or calendar boundary checks may reject the rule")
	}
}

func TestTaskExpressionRequestProbesAndRuleTraces(t *testing.T) {
	expr := `tier("video", u("seconds") * 0.2) * (param("metadata.priority") == "high" ? 1.5 : 1) * (header("X-Service-Tier") == "express" ? 2 : 1)`
	schema := map[string]jsplugin.UsageFieldSchema{"seconds": {Type: "number", Unit: "second"}}
	require.NoError(t, ValidateSchema(expr, schema))

	p, err := Compile(expr)
	require.NoError(t, err)
	require.True(t, p.HasProbes())
	require.Equal(t, []string{"metadata.priority"}, p.ParamPaths())
	require.Equal(t, []string{"x-service-tier"}, p.HeaderNames())

	// Missing probe snapshot must fail closed.
	_, err = p.EvaluateWithOptions(map[string]any{"seconds": float64(5)}, nil, nil)
	require.Error(t, err)

	probes, err := p.CaptureProbes(
		[]byte(`{"metadata":{"priority":"high","secret":"ignored"},"prompt":"unused"}`),
		map[string]string{"X-Service-Tier": "express", "Authorization": "Bearer secret"},
	)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"metadata.priority": "high"}, probes.Params)
	require.Equal(t, map[string]string{"x-service-tier": "express"}, probes.Headers)

	res, err := p.EvaluateWithOptions(map[string]any{"seconds": float64(5)}, nil, probes)
	require.NoError(t, err)
	require.InDelta(t, 3.0, res.Amount, 1e-12)
	require.Equal(t, "video", res.Tier)
	require.Len(t, res.RequestRules, 2)
	require.True(t, res.RequestRules[0].Matched)
	require.InDelta(t, 1.5, res.RequestRules[0].Multiplier, 1e-12)
	require.True(t, res.RequestRules[1].Matched)
	require.InDelta(t, 2.0, res.RequestRules[1].Multiplier, 1e-12)

	// Non-scalar param and oversized header must fail closed.
	_, err = p.CaptureProbes([]byte(`{"metadata":{"priority":{"nested":true}}}`), nil)
	require.Error(t, err)
	_, err = p.CaptureProbes([]byte(`{}`), map[string]string{"X-Service-Tier": strings.Repeat("x", 257)})
	require.Error(t, err)
	// Unsafely dividing by nil param must be caught by schema validation.
	require.Error(t, ValidateSchema(`1 / param("count")`, nil))
}

func TestTaskExpressionConcurrentTiersAreIsolated(t *testing.T) {
	p, err := Compile(`u("seconds") > 5 ? tier("long", 2) : tier("short", 1)`)
	require.NoError(t, err)
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seconds := float64(i % 10)
			amount, tier, err := p.Evaluate(map[string]any{"seconds": seconds})
			require.NoError(t, err)
			if seconds > 5 {
				require.Equal(t, 2.0, amount)
				require.Equal(t, "long", tier)
			} else {
				require.Equal(t, 1.0, amount)
				require.Equal(t, "short", tier)
			}
		}()
	}
	wg.Wait()
}
