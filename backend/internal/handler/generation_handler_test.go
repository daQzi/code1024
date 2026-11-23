package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type generationGatewayReplayFixture struct {
	service.GenerationTaskRepository
	task *service.GenerationTask
}

func (r *generationGatewayReplayFixture) FindIdempotent(_ context.Context, user, key int64, idempotency string) (*service.GenerationTask, error) {
	if user == r.task.UserID && key == r.task.APIKeyID && idempotency == r.task.IdempotencyKey {
		return r.task, nil
	}
	return nil, service.ErrGenerationTaskNotFound
}
func (r *generationGatewayReplayFixture) Claim(context.Context, string, time.Duration) (*service.GenerationTask, error) {
	return nil, service.ErrGenerationTaskNotFound
}

func TestGenerationGatewayReplayDoesNotSelectAnotherAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	task := &service.GenerationTask{ID: "video_original", UserID: 1, APIKeyID: 2, IdempotencyKey: "accepted", Fingerprint: service.HashUsageRequestPayload([]byte(`{"model":"sora-2","prompt":"Ocean"}`))}
	generations := service.NewGenerationService(&generationGatewayReplayFixture{task: task}, nil, nil, nil, nil, nil)
	defer generations.Stop()
	h := &OpenAIGatewayHandler{generations: generations} // No scheduler or billing services are available.
	for _, tc := range []struct {
		body   string
		status int
	}{
		{body: `{"prompt":"Ocean", "model":"sora-2"}`, status: 200},
		{body: `{"model":"sora-2","prompt":"Changed"}`, status: 409},
	} {
		router := gin.New()
		router.POST("/v1/videos", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, Group: &service.Group{ID: 3}})
			require.True(t, h.TryPluginVideo(c, service.PlatformOpenAI))
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "accepted")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		require.Equal(t, tc.status, response.Code, response.Body.String())
		if tc.status == 200 {
			require.Contains(t, response.Body.String(), task.ID)
			require.Equal(t, "/v1/videos/"+task.ID, response.Header().Get("Location"))
		}
	}
}

func TestGenerationMultipartSDKTextFields(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "video-fixture"))
	require.NoError(t, writer.WriteField("prompt", "ocean"))
	require.NoError(t, writer.WriteField("seconds", "8"))
	require.NoError(t, writer.Close())
	normalized, err := pluginTask.NormalizeVideoRequest(writer.FormDataContentType(), body.Bytes())
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(normalized, &result))
	require.Equal(t, "8", result["seconds"])
}
func TestGenerationMultipartRejectsDuplicateAndMalformedBody(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "one"))
	require.NoError(t, writer.WriteField("model", "two"))
	require.NoError(t, writer.Close())
	_, err := pluginTask.NormalizeVideoRequest(writer.FormDataContentType(), body.Bytes())
	require.ErrorContains(t, err, "duplicate")
	_, err = pluginTask.NormalizeVideoRequest("application/json", []byte(`[]`))
	require.Error(t, err)
}
