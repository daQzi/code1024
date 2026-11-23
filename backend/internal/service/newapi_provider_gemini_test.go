//go:build unit

package service

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

const providerGeminiFixture = `export const meta={apiVersion:1,key:"gemini-fixture",name:"Gemini fixture",version:"1.0.0",author:{name:"Fixture"},models:["vendor-text"],
relay:[{incoming_path:"/v1beta/models/{model}:generateContent",upstream_path:"/native/models/{model}:generateContent",models:["vendor-text"],auth:{type:"header",name:"x-goog-api-key",value:"{api_key}"}}]};`

func TestNewAPIProviderGeminiJSONAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		action := "generateContent"
		if stream {
			action = "streamGenerateContent"
		}
		t.Run(action, func(t *testing.T) {
			s, account, upstream := providerFixture(t, providerGeminiFixture)
			path := "/v1beta/models/public-text:" + action
			body := `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
			upstream.response = `{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":12,"cachedContentTokenCount":3,"candidatesTokenCount":5,"thoughtsTokenCount":2}}`
			if stream {
				upstream.contentType = "text/event-stream"
				upstream.response = "data: {\"usageMetadata\":{\"promptTokenCount\":12}}\n\ndata: " + upstream.response + "\n\n"
			}
			c, recorder := providerContext(path, body)
			require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityPluginProviderGemini))
			require.True(t, account.SupportsNewAPIProviderModel(path, "public-text"))
			result, err := s.gateway.ForwardNewAPIProviderGemini(c.Request.Context(), c, account, []byte(body), path, "")
			require.NoError(t, err, recorder.Body.String())
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, "public-text", result.Model)
			require.Equal(t, "vendor-text", result.UpstreamModel)
			require.Equal(t, 12, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.CacheReadInputTokens)
			require.Equal(t, 7, result.Usage.OutputTokens)
			require.Len(t, upstream.requests, 1)
			out := upstream.requests[0]
			require.Equal(t, "/prefix/native/models/vendor-text:"+action, out.URL.Path)
			require.Equal(t, "upstream-fixture-key", out.Header.Get("x-goog-api-key"))
			require.NotContains(t, out.URL.RawQuery, "client_secret")
			if stream {
				require.Equal(t, "sse", out.URL.Query().Get("alt"))
			}
			wire, err := io.ReadAll(out.Body)
			require.NoError(t, err)
			require.JSONEq(t, body, string(wire))
			require.Contains(t, recorder.Body.String(), "answer")
		})
	}
}
