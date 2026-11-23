package provider

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProtocolToolsResponsesDeduplicateAndOverlayHookFacts(t *testing.T) {
	book, err := NewPriceBook(map[string]float64{"web_search": 10, "file_search": 2.5, "custom": 3})
	require.NoError(t, err)
	lookup := func(name string) (float64, bool) { return book.Lookup(name, "model") }
	p := NewProtocolTools([]byte(`{"tools":[{"type":"web_search"}]}`), lookup)
	item := `{"id":"search1","type":"web_search_call","status":"completed"}`
	for range 3 {
		require.NoError(t, p.Observe("/v1/responses", []byte(`{"type":"response.output_item.done","output_index":0,"item":`+item+`}`), true))
	}
	final := []byte(`{"output":[` + item + `,{"id":"file1","type":"file_search_call"},{"id":"custom1","type":"function_call","name":"custom"},{"type":"function_call","name":"file_search"}]}`)
	require.NoError(t, p.Observe("/v1/responses", final, false))
	var explicit ToolUsage
	require.NoError(t, explicit.Add(map[string]int{"web_search": 0}))
	usage, err := p.Combined(&explicit)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"web_search": 0, "file_search": 1, "custom": 1}, usage.Counts())
	charges, err := usage.Charges(lookup)
	require.NoError(t, err)
	require.InDelta(t, 0.0055, charges.TotalUSD, 1e-12)
}

func TestProtocolToolsChatFunctionsIgnoreReservedNamesAndDeclarations(t *testing.T) {
	book, err := NewPriceBook(map[string]float64{"custom": 3, "web_search": 10, "free": 0})
	require.NoError(t, err)
	p := NewProtocolTools([]byte(`{"tools":[{"type":"function","function":{"name":"custom"}}]}`), func(name string) (float64, bool) { return book.Lookup(name, "model") })
	require.Empty(t, p.usage.Counts())
	for _, raw := range []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call1","function":{"name":"custom"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"custom"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"name":"web_search"}},{"index":2,"function":{"name":"unknown"}},{"index":3,"function":{"name":"free"}}]}}]}`,
	} {
		require.NoError(t, p.Observe("/v1/chat/completions", []byte(raw), true))
	}
	usage, err := p.Combined(nil)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"custom": 1}, usage.Counts())
}

func TestProtocolToolsBoundIdentitiesAndDoNotShareRequests(t *testing.T) {
	p := NewProtocolTools(nil, nil)
	for i := range MaxObservedToolCalls {
		raw := fmt.Sprintf(`{"type":"response.output_item.done","output_index":%d,"item":{"type":"file_search_call","id":"call%d"}}`, i, i)
		require.NoError(t, p.Observe("/v1/responses", []byte(raw), true))
	}
	require.Error(t, p.Observe("/v1/responses", []byte(`{"type":"response.output_item.done","output_index":10000,"item":{"type":"file_search_call","id":"extra"}}`), true))
	require.NoError(t, p.Observe("/v1/responses", []byte(`{"type":"response.output_item.done","output_index":0,"item":{"type":"file_search_call","id":"call0"}}`), true))
	other := NewProtocolTools(nil, nil)
	require.Empty(t, other.usage.Counts())
	require.NoError(t, other.Observe("/v1/responses", []byte(`{"type":"response.output_item.done","item":{"type":"message"}}`), true))
}
