//go:build unit

package service

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNativeVideoForwardObservesBeforeResponse(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint GrokMediaEndpoint
		body     string
		response string
	}{
		{"xai create", GrokMediaEndpointVideosGenerations, `{"model":"grok-imagine-video","prompt":"waves"}`, `{"request_id":"task-1"}`},
		{"xai status", GrokMediaEndpointVideoStatus, "", `{"status":"done","video":{"url":"https://relay.example/v1/videos/task-1/content","duration":8}}`},
		{"xai edit", GrokMediaEndpointVideosEdits, `{"model":"grok-imagine-video","prompt":"waves","video":{"url":"https://media.example/input.mp4"}}`, `{"request_id":"task-1"}`},
		{"xai extend", GrokMediaEndpointVideosExtensions, `{"model":"grok-imagine-video","prompt":"waves","video":{"url":"https://media.example/input.mp4"}}`, `{"request_id":"task-1"}`},
		{"ark create", SeedanceEndpointCreate, `{"model":"video-model","content":[{"type":"text","text":"waves"}]}`, `{"id":"task-1"}`},
		{"ark status", SeedanceEndpointStatus, "", `{"status":"succeeded","content":{"video_url":"https://media.example/video.mp4"},"usage":{"completion_tokens":100}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(tc.response)}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			c, recorder := grokMediaContentTestContext(http.MethodPost, "https://api.example/v1/videos/generations", nil)
			c.Request.Body = http.NoBody
			account := grokMediaContentTestAccount()
			taskID := "task-1"
			if tc.endpoint.IsSeedance() {
				account = seedanceTestAccount()
				taskID = SeedanceTaskKey(taskID)
			}
			calls := 0
			ctx := WithNativeVideoObserver(context.Background(), func(result *OpenAIForwardResult) {
				calls++
				require.False(t, c.Writer.Written(), "persist creation before the client can start polling")
				require.Zero(t, recorder.Body.Len())
				if tc.endpoint == GrokMediaEndpointVideoStatus || tc.endpoint == SeedanceEndpointStatus {
					require.Equal(t, &NativeVideoState{Status: GenerationSucceeded, DeliveryStatus: GenerationDeliveryReady}, result.NativeVideo)
				} else {
					require.NotEmpty(t, result.ResponseID)
					require.Zero(t, result.VideoCount, "creation must still not bill")
				}
			})
			var result *OpenAIForwardResult
			var err error
			if tc.endpoint.IsSeedance() {
				result, err = svc.ForwardSeedance(ctx, c, account, tc.endpoint, taskID, []byte(tc.body))
			} else {
				result, err = svc.ForwardGrokMedia(ctx, c, account, tc.endpoint, taskID, []byte(tc.body), "application/json")
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 1, calls)
			require.True(t, c.Writer.Written())
		})
	}
}

func TestNativeVideoContentObservesBeforeStreaming(t *testing.T) {
	upstream := &grokMediaContentUpstreamStub{responses: []*http.Response{
		grokMediaContentStatusResponse(`{"status":"done","video":{"url":"https://relay.example/v1/videos/task-1/content","duration":8}}`),
		grokMediaContentStatusResponse("video-payload"),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	c, recorder := grokMediaContentTestContext(http.MethodGet, "https://api.example/v1/videos/task-1/content", nil)
	calls := 0
	ctx := WithNativeVideoObserver(context.Background(), func(result *OpenAIForwardResult) {
		calls++
		require.False(t, c.Writer.Written())
		require.Equal(t, &NativeVideoState{Status: GenerationSucceeded, DeliveryStatus: GenerationDeliveryReady}, result.NativeVideo)
	})
	_, err := svc.ForwardGrokMedia(ctx, c, grokMediaContentTestAccount(), GrokMediaEndpointVideoContent, "task-1", nil, "")
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.True(t, bytes.Contains(recorder.Body.Bytes(), []byte("video-payload")))
}
