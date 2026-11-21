package task

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/newapi/builtin"
	"github.com/Wei-Shaw/sub2api/internal/newapi/jsplugin"
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
