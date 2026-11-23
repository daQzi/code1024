package paramoverride

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/MoeForge/moego/regexp"
	"github.com/stretchr/testify/require"
)

func TestParameterOverrideRejectsAmplificationBeforeProducingOutput(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		rules      map[string]any
	}{
		{"sparse-array", `{}`, map[string]any{"operations": []any{map[string]any{"mode": "set", "path": "items.9999999999999999999999999999", "value": 1}}}},
		{"deep-path", `{}`, map[string]any{"operations": []any{map[string]any{"mode": "set", "path": strings.Repeat("x.", 65) + "y", "value": 1}}}},
		{"literal-replace", `{"text":"` + strings.Repeat("x", 32<<10) + `"}`, map[string]any{"operations": []any{map[string]any{"mode": "replace", "path": "text", "from": "x", "to": strings.Repeat("z", 1024)}}}},
		{"regex-capture", `{"text":"` + strings.Repeat("x", 256<<10) + `"}`, map[string]any{"operations": []any{map[string]any{"mode": "regex_replace", "path": "text", "from": "(.*)", "to": strings.Repeat("${1}", 8)}}}},
		{"document-growth", `{"text":"` + strings.Repeat("x", 600<<10) + `"}`, map[string]any{"operations": []any{map[string]any{"mode": "copy", "from": "text", "to": "duplicate"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ApplyParamOverride([]byte(tc.body), tc.rules, nil)
			require.Error(t, err)
			require.Nil(t, result)
		})
	}
	operations := make([]any, maxOperations+1)
	for i := range operations {
		operations[i] = map[string]any{"mode": "set", "path": "x", "value": i}
	}
	_, err := ApplyParamOverride([]byte(`{}`), map[string]any{"operations": operations}, nil)
	require.Error(t, err)
	_, err = ApplyParamOverride([]byte(`{}`), map[string]any{"large": strings.Repeat("x", MaxRuleBytes)}, nil)
	require.Error(t, err)
}

func TestBoundedRegexReplacementMatchesEngineSyntax(t *testing.T) {
	for _, tc := range []struct{ pattern, input, replacement string }{
		{`(?P<word>[a-z]+)-(\d+)`, "hello-12 world-34", `${word}:$2 $$ $0`},
		{`(a)`, "aba", `$1x ${1}x $01 ${01} $unknown $ {bad} ${`},
		{`a*`, "aab", `($0)`},
		{`^|$`, "abc", `[$0]`},
		{`x`, "nothing", `$$`},
		{`(a)?b`, "b ab", `${1}:$0`},
	} {
		t.Run(tc.pattern+"/"+tc.replacement, func(t *testing.T) {
			re := regexp.MustCompile(tc.pattern)
			actual, err := boundedRegexReplace(re, tc.input, tc.replacement)
			require.NoError(t, err)
			require.Equal(t, re.ReplaceAllString(tc.input, tc.replacement), actual)
		})
	}
}

func TestParameterOverridePreservesNumericObjectKeys(t *testing.T) {
	raw, err := ApplyParamOverride([]byte(`{"calendar":{}}`), map[string]any{"operations": []any{map[string]any{"mode": "set", "path": "calendar.20261122", "value": true}}}, nil)
	require.NoError(t, err)
	require.JSONEq(t, `{"calendar":{"20261122":true}}`, string(raw))
}

func BenchmarkParameterOverrideDocument(b *testing.B) {
	for _, size := range []int{64 << 10, 900 << 10} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			body, _ := json.Marshal(map[string]any{"text": strings.Repeat("x", size), "temperature": 0.8})
			rules := map[string]any{"operations": []any{map[string]any{"mode": "set", "path": "temperature", "value": 0.5}}}
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for b.Loop() {
				if _, err := ApplyParamOverride(body, rules, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
