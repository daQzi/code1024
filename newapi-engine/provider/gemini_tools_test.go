package provider

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeminiGroundingUnionAcrossCandidatesAndFrames(t *testing.T) {
	p := NewProtocolTools(nil, nil)
	for _, raw := range []string{
		`{"candidates":[{"groundingMetadata":{"webSearchQueries":["alpha"," beta ","","  "]}},{"groundingMetadata":{"webSearchQueries":["beta","gamma"]}}]}`,
		`{"candidates":[{"groundingMetadata":{"webSearchQueries":["alpha","gamma"],"imageSearchQueries":["gamma","photo"]}}]}`,
		`{"candidates":[{"groundingMetadata":{"webSearchQueries":["alpha","beta"]}}]}`,
	} {
		require.NoError(t, p.ObserveVendor(VendorGemini, []byte(raw), true))
	}
	combined, err := p.Combined(nil)
	require.NoError(t, err)
	require.Equal(t, map[string]int{GoogleSearchTool: 4, GoogleSearchGroundedPromptTool: 1}, combined.Counts())
	require.Len(t, p.grounding, 4)
	// A Responses codec may project these same searches as OpenAI items.
	// They must not demand a second price or add a second search charge.
	require.NoError(t, p.Observe("/v1/responses", []byte(`{"output":[{"type":"web_search_call","id":"projected"}]}`), false))
	combined, err = p.Combined(nil)
	require.NoError(t, err)
	require.Equal(t, map[string]int{GoogleSearchTool: 4, GoogleSearchGroundedPromptTool: 1}, combined.Counts())
	encoded, err := json.Marshal(combined.Counts())
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "alpha", "neither query text nor query identities enter billing traces")
	for _, prices := range []map[string]float64{
		{GoogleSearchTool: 10, GoogleSearchGroundedPromptTool: 0},
		{GoogleSearchTool: 0, GoogleSearchGroundedPromptTool: 20},
	} {
		charges, err := combined.Charges(func(name string) (float64, bool) { price, ok := prices[name]; return price, ok })
		require.NoError(t, err)
		require.InDelta(t, (4*prices[GoogleSearchTool]+prices[GoogleSearchGroundedPromptTool])/1000, charges.TotalUSD, 1e-12)
	}
	_, err = combined.Charges(func(name string) (float64, bool) { return 10, name == GoogleSearchTool })
	require.Error(t, err, "the unused price metric must be explicitly free, not silently unpriced")
	var explicit ToolUsage
	require.NoError(t, explicit.Add(map[string]int{GoogleSearchTool: 0}))
	combined, err = p.Combined(&explicit)
	require.NoError(t, err)
	require.Equal(t, map[string]int{GoogleSearchTool: 0, GoogleSearchGroundedPromptTool: 1}, combined.Counts())
}

func TestGeminiGroundingIgnoresSourcesAndEmptyQueries(t *testing.T) {
	for _, raw := range []string{
		`{"candidates":[{"groundingMetadata":{"webSearchQueries":[""," "],"imageSearchQueries":[]}}]}`,
		`{"candidates":[{"groundingMetadata":{"groundingChunks":[{"web":{"uri":"https://example.invalid"}}],"retrievalQueries":["not-google-search"]}}]}`,
		`{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`,
		`{"candidates":[]}`,
		`{"candidates":null}`,
		`{"error":{"message":"denied"},"candidates":[{"groundingMetadata":{"webSearchQueries":["bad"]}}]}`,
	} {
		p := NewProtocolTools(nil, nil)
		require.NoError(t, p.ObserveVendor(VendorGemini, []byte(raw), false))
		require.Empty(t, p.grounding)
		require.Empty(t, p.vendor.Counts())
	}
}

func TestGeminiGroundingInvalidFramesAreAtomic(t *testing.T) {
	for _, raw := range []string{
		`{"candidates":[{"groundingMetadata":{"webSearchQueries":["new",3]}}]}`,
		`{"candidates":[{"groundingMetadata":{"webSearchQueries":"new"}}]}`,
		`{"candidates":[{"groundingMetadata":{"webSearchQueries":["new"],"webSearchQueries":["other"]}}]}`,
		`{"candidates":[{"groundingMetadata":{"webSearchQueries":["new"]}},{"groundingMetadata":{"imageSearchQueries":[null]}}]}`,
		`{"candidates":{}}`,
		`{"candidates":[{"groundingMetadata":[]}]}`,
	} {
		p := NewProtocolTools(nil, nil)
		require.NoError(t, p.ObserveVendor(VendorGemini, []byte(`{"candidates":[{"groundingMetadata":{"webSearchQueries":["existing"]}}]}`), true))
		require.Error(t, p.ObserveVendor(VendorGemini, []byte(raw), true), raw)
		require.Len(t, p.grounding, 1)
		require.Equal(t, map[string]int{GoogleSearchTool: 1, GoogleSearchGroundedPromptTool: 1}, p.vendor.Counts())
	}
}

func TestGeminiGroundingBoundedWithoutRetainingQueries(t *testing.T) {
	p := NewProtocolTools(nil, nil)
	for i := range MaxToolCalls {
		raw := fmt.Sprintf(`{"candidates":[{"groundingMetadata":{"webSearchQueries":["query-%d"]}}]}`, i)
		require.NoError(t, p.ObserveVendor(VendorGemini, []byte(raw), true))
	}
	require.NoError(t, p.ObserveVendor(VendorGemini, []byte(`{"candidates":[{"groundingMetadata":{"webSearchQueries":["query-0"]}}]}`), true))
	require.Error(t, p.ObserveVendor(VendorGemini, []byte(`{"candidates":[{"groundingMetadata":{"webSearchQueries":["too-many"]}}]}`), true))
	require.Len(t, p.grounding, MaxToolCalls)
	require.Equal(t, MaxToolCalls, p.vendor.Counts()[GoogleSearchTool])
}
