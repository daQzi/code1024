package task

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/require"
)

// This is a complete protocol simulation with the original pinned xAI script.
// It does not call xAI or assert that an upstream generation was performed.
func TestXAITaskContractRoundTrip(t *testing.T) {
	source, err := builtin.Source("xai")
	require.NoError(t, err)
	p, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	ctx := context.Background()
	var decoded map[string]any
	err = p.Engine.CallPathInto(ctx, &decoded, "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": "grok-imagine-video", "body": map[string]any{"kind": "json", "value": map[string]any{"model": "grok-imagine-video", "prompt": "A quiet sea", "seconds": "8", "size": "1280x720"}}})
	require.NoError(t, err)
	decoded["baseUrl"] = "https://api.x.ai"
	decoded["apiKey"] = "fixture-key"
	decoded["upstreamModel"] = "grok-imagine-video"
	var submit Request
	require.NoError(t, p.Engine.CallInto(ctx, &submit, "buildSubmitRequest", decoded))
	require.NoError(t, submit.Validate("https://api.x.ai", nil, "POST"))
	require.Equal(t, "https://api.x.ai/v1/videos/generations", submit.URL)
	var body map[string]any
	require.NoError(t, json.Unmarshal(submit.Body, &body))
	require.Equal(t, float64(8), body["duration"])
	var accepted Submitted
	require.NoError(t, p.Engine.CallInto(ctx, &accepted, "parseSubmitResponse", decoded, map[string]any{"status": 200, "body": map[string]any{"request_id": "upstream-id"}}))
	require.NoError(t, accepted.Validate())
	decoded["taskId"] = accepted.TaskID
	var query Request
	require.NoError(t, p.Engine.CallInto(ctx, &query, "buildQueryRequest", decoded))
	require.NoError(t, query.Validate("https://api.x.ai", nil, "GET"))
	var result Result
	done := map[string]any{"status": "done", "video": map[string]any{"url": "https://vidgen.x.ai/fixture.mp4", "duration": 8, "respect_moderation": true}}
	require.NoError(t, p.Engine.CallInto(ctx, &result, "parseTaskResult", decoded, done))
	require.NoError(t, result.Validate())
	require.Equal(t, "SUCCESS", result.Status)
	var artifacts json.RawMessage
	require.NoError(t, p.Engine.CallInto(ctx, &artifacts, "listArtifacts", map[string]any{"data": done, "status": "SUCCESS"}))
	items, err := DecodeArtifacts(artifacts)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "video", items[0].Type)
}

func TestTaskContractRejectsUnknownAndCredentialTargets(t *testing.T) {
	require.Error(t, (Result{Status: "future-status"}).Validate())
	r := Request{URL: "https://attacker.example/submit", Method: "POST"}
	require.Error(t, r.Validate("https://api.example", nil, "POST"))
	r.URL = "file:///etc/passwd"
	require.Error(t, r.Validate("https://api.example", nil, "GET"))
	r.URL = "https://api.example/submit"
	r.Headers = map[string]string{"Transfer-Encoding": "chunked"}
	require.Error(t, r.Validate("https://api.example", nil, "POST"))
	_, err := DecodeArtifacts([]byte(`[{"key":"same","type":"video"},{"key":"same","type":"video"}]`))
	require.Error(t, err)
}

func TestEmbeddedArtifactContractOperationAndBase64(t *testing.T) {
	payload := []byte{0xfb, 0xff, 0xfe, 1}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		for _, completion := range []string{`"done":true`, `"status":"completed"`} {
			raw := []byte(`{` + completion + `,"response":{"videos":[{"bytes.Base64":"` + encoding.EncodeToString(payload) + `"}]}}`)
			reader, size, err := DecodeEmbeddedArtifact(raw, []any{"response", "videos", float64(0), "bytes.Base64"})
			require.NoError(t, err)
			require.EqualValues(t, len(payload), size)
			data, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Equal(t, payload, data)
		}
	}
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{"pending", `{"done":false,"data":"eA=="}`, ErrEmbeddedArtifactNotReady},
		{"error even if done", `{"done":true,"error":{"message":"private upstream data"},"data":"eA=="}`, nil},
		{"missing", `{"done":true}`, ErrEmbeddedArtifactGone},
		{"wrong value", `{"done":true,"data":123}`, ErrEmbeddedArtifactGone},
		{"invalid encoding", `{"done":true,"data":"!!!!"}`, nil},
		{"truncated", `{"done":true,"data":"eA=="`, nil},
		{"not an operation", `[]`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := DecodeEmbeddedArtifact([]byte(tc.body), []any{"data"})
			require.Error(t, err)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			}
			require.NotContains(t, err.Error(), "private upstream data")
		})
	}
	for _, path := range [][]any{{"data", -1}, {"data", 0.5}, {map[string]any{}}, make([]any, 33)} {
		require.Error(t, (&Request{ContentPath: path}).ValidateContent())
	}
	path := make([]any, 32)
	for i := range path {
		path[i] = "data"
	}
	require.NoError(t, (&Request{ContentPath: path, MimeType: "video/webm"}).ValidateContent())
	for _, kind := range []string{"video/mp4\r\nX-Injected: 1", strings.Repeat("x", 256), "broken"} {
		require.Error(t, (&Request{ContentPath: []any{"data"}, MimeType: kind}).ValidateContent())
	}
}

func BenchmarkEmbeddedArtifactDecode(b *testing.B) {
	for _, size := range []int{1 << 20, 16 << 20} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			raw := []byte(`{"done":true,"data":"` + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", size))) + `"}`)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for range b.N {
				reader, decoded, err := DecodeEmbeddedArtifact(raw, []any{"data"})
				if err != nil || decoded != int64(size) {
					b.Fatalf("invalid artifact: bytes=%d error=%v", decoded, err)
				}
				if _, err := io.Copy(io.Discard, reader); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
