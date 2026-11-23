package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestNativeVideoObservationPreservesPublicIdentityAndProtocol(t *testing.T) {
	key := &service.APIKey{ID: 2, UserID: 1, Group: &service.Group{ID: 3}}
	account := &service.Account{ID: 4, Platform: service.PlatformGrok}
	started := time.Now().UTC()
	info := service.GrokMediaRequestInfo{Prompt: "Ocean", DurationSeconds: 8, Size: "1280x720"}
	result := &service.OpenAIForwardResult{ResponseID: "native-request", Model: "mapped-model", UpstreamModel: "vendor-model"}
	observation := nativeVideoObservation(key, account, service.GrokMediaEndpointVideosGenerations, "", "public-alias", info, nil, result, started, started.Add(time.Second))
	require.NotNil(t, observation)
	require.NoError(t, observation.Validate())
	require.Equal(t, "public-alias", observation.Model)
	require.Equal(t, "native-request", observation.UpstreamID)
	require.Equal(t, "xai_video", observation.Protocol)
	require.Equal(t, "text_to_video", observation.Action)
	require.Equal(t, "8", observation.Fields.Seconds)
	require.Equal(t, "Ocean", observation.Fields.Prompt)
	require.Equal(t, service.GenerationQueued, observation.Status)
	for endpoint, action := range map[service.GrokMediaEndpoint]string{service.GrokMediaEndpointVideosEdits: "edit", service.GrokMediaEndpointVideosExtensions: "extend"} {
		o := nativeVideoObservation(key, account, endpoint, "", "public-alias", info, nil, result, started, started)
		require.Equal(t, action, o.Action)
	}
	account.Platform = service.PlatformOpenAI
	result.ResponseID = service.SeedanceTaskKey("native-request")
	result.NativeVideo = &service.NativeVideoState{Status: service.GenerationRunning, DeliveryStatus: service.GenerationDeliveryPending}
	o := nativeVideoObservation(key, account, service.SeedanceEndpointCreate, "", "ark-alias", service.GrokMediaRequestInfo{}, []byte(`{"duration":12}`), result, started, started)
	require.NoError(t, o.Validate())
	require.Equal(t, "ark_video", o.Protocol)
	require.Equal(t, "seedance:native-request", o.UpstreamID)
	require.Equal(t, "12", o.Fields.Seconds)
	// Lookup identity comes from the authenticated request, never a different ID
	// returned by the upstream. Unknown polls, deletes and images add no state.
	o = nativeVideoObservation(key, account, service.SeedanceEndpointStatus, "seedance:owned-task", "", info, nil, result, started, started)
	require.False(t, o.Create)
	require.Empty(t, o.Fields.Prompt)
	require.Equal(t, "seedance:owned-task", o.UpstreamID)
	require.Nil(t, nativeVideoObservation(key, account, service.SeedanceEndpointDelete, "seedance:owned-task", "", info, nil, result, started, started))
	require.Nil(t, nativeVideoObservation(key, account, service.GrokMediaEndpointImagesGenerations, "", "image", info, nil, result, started, started))
	result.NativeVideo = nil
	require.Nil(t, nativeVideoObservation(key, account, service.SeedanceEndpointStatus, "seedance:owned-task", "", info, nil, result, started, started))
	result.ResponseID = ""
	require.Nil(t, nativeVideoObservation(key, account, service.SeedanceEndpointCreate, "", "ark", info, nil, result, started, started))
	result.ResponseID = "bounded"
	info.Prompt = strings.Repeat("p", 8193)
	o = nativeVideoObservation(key, account, service.SeedanceEndpointCreate, "", "ark", info, nil, result, started, started)
	require.Empty(t, o.Fields.Prompt)
	o = nativeVideoObservation(key, account, service.SeedanceEndpointCreate, "", "ark", service.GrokMediaRequestInfo{}, []byte(`{"duration":-1}`), result, started, started)
	require.Empty(t, o.Fields.Seconds, "automatic duration is not minus one second")
}

type nativeVideoHistoryFixture struct {
	taskLogFixture
	err      error
	observed []service.NativeVideoObservation
	context  context.Context
}

func (r *nativeVideoHistoryFixture) ObserveNativeVideo(ctx context.Context, o service.NativeVideoObservation) error {
	r.context = ctx
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.observed = append(r.observed, o)
	return r.err
}

func TestNativeVideoHistoryOutlivesClientCancellationWithoutBilling(t *testing.T) {
	repo := &nativeVideoHistoryFixture{}
	svc := service.NewGenerationService(repo, nil, nil, nil, nil, nil)
	defer svc.Stop()
	h := &OpenAIGatewayHandler{generations: svc}
	now := time.Now().UTC()
	o := &service.NativeVideoObservation{UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: 4, UpstreamID: "owned", Protocol: "xai_video", Status: service.GenerationRunning, DeliveryStatus: service.GenerationDeliveryPending, ObservedAt: now}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, h.recordNativeVideoObservation(ctx, o))
	require.Len(t, repo.observed, 1)
	deadline, ok := repo.context.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, now.Add(3*time.Second), deadline, time.Second)
	repo.err = errors.New("database unavailable")
	require.ErrorIs(t, h.recordNativeVideoObservation(ctx, o), repo.err)
	// The embedded funding/execution methods are nil. Calling one would panic:
	// the observer only writes lifecycle history, even when storage fails.
	require.NoError(t, h.recordNativeVideoObservation(ctx, nil))
	require.NoError(t, (&OpenAIGatewayHandler{}).recordNativeVideoObservation(ctx, o))
}

type nativeVideoContentFixture struct{ taskLogFixture }

func (r *nativeVideoContentFixture) GetTaskLog(_ context.Context, id string, owner int64) (*service.TaskLogDetail, error) {
	if owner != 0 && owner != 1 {
		return nil, service.ErrGenerationTaskNotFound
	}
	return &service.TaskLogDetail{TaskLog: service.TaskLog{ID: id, UserID: 1, APIKeyID: 2, Source: "native", Status: service.GenerationSucceeded, DeliveryStatus: service.GenerationDeliveryReady}}, nil
}

func TestNativeVideoConsoleContentDoesNotEnterPluginExecution(t *testing.T) {
	svc := service.NewGenerationService(&nativeVideoContentFixture{}, nil, nil, nil, nil, nil)
	defer svc.Stop()
	// The fixture has no GetOwned or plugin runtime. Authorization must run
	// first, then native content must stop before reaching either dependency.
	_, _, err := svc.TaskLogContent(context.Background(), "native_video_fixture", 99, false, http.Header{})
	require.ErrorIs(t, err, service.ErrGenerationTaskNotFound)
	_, _, err = svc.TaskLogContent(context.Background(), "native_video_fixture", 1, false, http.Header{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "original native video content endpoint")
}
