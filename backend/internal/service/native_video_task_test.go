package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNativeVideoResponseState(t *testing.T) {
	for _, tc := range []struct {
		name, body, status, delivery string
		endpoint                     GrokMediaEndpoint
	}{
		{"xai pending", `{"status":"pending"}`, GenerationRunning, GenerationDeliveryPending, GrokMediaEndpointVideoStatus},
		{"xai done", `{"status":"done","video":{"url":"https://video.example/private?signature=secret"}}`, GenerationSucceeded, GenerationDeliveryReady, GrokMediaEndpointVideoStatus},
		{"xai moderation", `{"status":"done","video":{"url":"https://video.example/private","respect_moderation":false}}`, GenerationSucceeded, "failed", GrokMediaEndpointVideoStatus},
		{"xai missing output", `{"status":"done"}`, GenerationSucceeded, GenerationDeliveryPending, GrokMediaEndpointVideoStatus},
		{"xai failed", `{"status":"failed","error":{"message":"private upstream text"}}`, GenerationFailed, "failed", GrokMediaEndpointVideoStatus},
		{"xai expired", `{"status":"expired"}`, GenerationFailed, "failed", GrokMediaEndpointVideoStatus},
		{"unknown remains unknown", `{"status":"unknown"}`, "", "", GrokMediaEndpointVideoStatus},
		{"accepted without state", `{"request_id":"upstream"}`, "", "", GrokMediaEndpointVideosGenerations},
		{"error is not success", `{"error":{"status":"done"}}`, "", "", GrokMediaEndpointVideoStatus},
		{"invalid JSON", `invalid`, "", "", GrokMediaEndpointVideoStatus},
		{"image excluded", `{"status":"done"}`, "", "", GrokMediaEndpointImagesGenerations},
		{"ark queued", `{"status":"queued"}`, GenerationQueued, GenerationDeliveryPending, SeedanceEndpointStatus},
		{"ark running", `{"status":"running"}`, GenerationRunning, GenerationDeliveryPending, SeedanceEndpointStatus},
		{"ark done", `{"status":"succeeded","content":{"video_url":"https://video.example/private?token=secret"}}`, GenerationSucceeded, GenerationDeliveryReady, SeedanceEndpointStatus},
		{"ark no output", `{"status":"succeeded"}`, GenerationSucceeded, GenerationDeliveryPending, SeedanceEndpointStatus},
		{"ark failed", `{"status":"failed"}`, GenerationFailed, "failed", SeedanceEndpointStatus},
		{"ark cancelled", `{"status":"cancelled"}`, GenerationFailed, "failed", SeedanceEndpointStatus},
		{"ark expired", `{"status":"expired"}`, GenerationFailed, "failed", SeedanceEndpointStatus},
		{"ark delete not inferred", `{"status":"cancelled"}`, "", "", SeedanceEndpointDelete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := nativeVideoResponseState(tc.endpoint, []byte(tc.body))
			if tc.status == "" {
				require.Nil(t, state)
				return
			}
			require.Equal(t, &NativeVideoState{Status: tc.status, DeliveryStatus: tc.delivery}, state)
		})
	}
}

func TestNativeVideoTaskIdentityAndValidation(t *testing.T) {
	now := time.Now().UTC()
	o := NativeVideoObservation{UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: 4, UpstreamID: "native-task", Model: "public-model", Platform: PlatformGrok, Protocol: "xai_video", Action: "text_to_video", Status: GenerationQueued, DeliveryStatus: GenerationDeliveryPending, CreatedAt: now, ObservedAt: now, Create: true}
	require.NoError(t, o.Validate())
	id := NativeVideoTaskID(o)
	require.Len(t, id, 77)
	require.NotContains(t, id, o.UpstreamID)
	require.Equal(t, id, NativeVideoTaskID(o))
	for _, mutate := range []func(*NativeVideoObservation){
		func(o *NativeVideoObservation) { o.UserID++ },
		func(o *NativeVideoObservation) { o.APIKeyID++ },
		func(o *NativeVideoObservation) { o.GroupID++ },
		func(o *NativeVideoObservation) { o.AccountID++ },
		func(o *NativeVideoObservation) { o.Protocol = "ark_video" },
		func(o *NativeVideoObservation) { o.UpstreamID += "-other" },
	} {
		copy := o
		mutate(&copy)
		require.NotEqual(t, id, NativeVideoTaskID(copy))
	}
	for _, mutate := range []func(*NativeVideoObservation){
		func(o *NativeVideoObservation) { o.UserID = 0 },
		func(o *NativeVideoObservation) { o.UpstreamID = "" },
		func(o *NativeVideoObservation) { o.UpstreamID += "\x00" },
		func(o *NativeVideoObservation) { o.Protocol = "image" },
		func(o *NativeVideoObservation) { o.Status = "done" },
		func(o *NativeVideoObservation) { o.DeliveryStatus = "unknown" },
		func(o *NativeVideoObservation) { o.DeliveryStatus = GenerationDeliveryReady },
		func(o *NativeVideoObservation) { o.Platform = PlatformOpenAI },
		func(o *NativeVideoObservation) { o.Action = "unknown" },
		func(o *NativeVideoObservation) { o.ObservedAt = time.Time{} },
		func(o *NativeVideoObservation) { o.CreatedAt = now.Add(time.Second) },
		func(o *NativeVideoObservation) { o.Fields.Prompt = strings.Repeat("p", 8193) },
		func(o *NativeVideoObservation) { o.Fields.Prompt = "\x00" },
	} {
		copy := o
		mutate(&copy)
		require.Error(t, copy.Validate())
	}
	// A poll carries no model/prompt/start time; it can only update an existing
	// identity, never synthesize historical parameters.
	o.Create, o.Model, o.CreatedAt = false, "", time.Time{}
	require.NoError(t, o.Validate())
}

func TestNativeVideoObserverUsesTheForwardingContext(t *testing.T) {
	result := &OpenAIForwardResult{ResponseID: "accepted"}
	calls := 0
	ctx := WithNativeVideoObserver(context.Background(), func(received *OpenAIForwardResult) {
		require.Same(t, result, received)
		calls++
	})
	observeNativeVideoResult(context.Background(), result)
	require.Zero(t, calls)
	observeNativeVideoResult(ctx, result)
	require.Equal(t, 1, calls)
}

func TestNativeVideoRecoverySnapshotValidation(t *testing.T) {
	q := NativeVideoLookup{UserID: 1, APIKeyID: 2, GroupID: 3, Protocol: GenerationProtocolXAI, UpstreamID: "task"}
	require.NoError(t, q.Validate())
	for _, mutate := range []func(*NativeVideoLookup){
		func(q *NativeVideoLookup) { q.UserID = 0 },
		func(q *NativeVideoLookup) { q.APIKeyID = 0 },
		func(q *NativeVideoLookup) { q.GroupID = 0 },
		func(q *NativeVideoLookup) { q.Protocol = "unsupported" },
		func(q *NativeVideoLookup) { q.UpstreamID = strings.Repeat("a", 513) },
		func(q *NativeVideoLookup) { q.UpstreamID = "task\x00" },
	} {
		copy := q
		mutate(&copy)
		require.Error(t, copy.Validate())
	}
	now := time.Now().UTC()
	pending := NormalizeGrokVideoPendingBilling(GrokVideoPendingBilling{Model: " mapped ", BillingModel: " billing ", UpstreamModel: " vendor ", OriginalModel: " public ", VideoResolution: "HD", VideoDurationSeconds: 40})
	require.NoError(t, pending.ValidateNativeVideoSnapshot())
	require.Equal(t, "mapped", pending.Model)
	require.Equal(t, "billing", pending.BillingModel)
	require.Equal(t, "vendor", pending.UpstreamModel)
	require.Equal(t, "public", pending.OriginalModel)
	require.Equal(t, "720p", pending.VideoResolution)
	require.Equal(t, 15, pending.VideoDurationSeconds, "same normalization as the original native cache")
	for _, mutate := range []func(*GrokVideoPendingBilling){
		func(p *GrokVideoPendingBilling) { p.Model = "" },
		func(p *GrokVideoPendingBilling) { p.CreatedAt = "broken" },
		func(p *GrokVideoPendingBilling) { p.BillingModel = strings.Repeat("b", 513) },
		func(p *GrokVideoPendingBilling) { p.UpstreamModel = "vendor\x00" },
		func(p *GrokVideoPendingBilling) { p.VideoResolution = strings.Repeat("r", 33) },
		func(p *GrokVideoPendingBilling) { p.VideoDurationSeconds = -1 },
	} {
		copy := pending
		mutate(&copy)
		require.Error(t, copy.ValidateNativeVideoSnapshot())
	}
	o := NativeVideoObservation{UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: 4, UpstreamID: "task", Model: "public", Platform: PlatformGrok, Protocol: GenerationProtocolXAI, Action: "text_to_video", Status: GenerationQueued, DeliveryStatus: GenerationDeliveryPending, CreatedAt: now, ObservedAt: now, Create: true, PendingBilling: &pending}
	require.NoError(t, o.Validate())
	o.Create = false
	require.Error(t, o.Validate(), "polls cannot replace private billing inputs")
	o.PendingBilling = nil
	require.NoError(t, o.Validate())
	missing := NormalizeGrokVideoPendingBilling(GrokVideoPendingBilling{Model: "model"})
	require.Empty(t, missing.VideoResolution)
	require.Zero(t, missing.VideoDurationSeconds, "do not invent native usage from omitted request fields")
}
