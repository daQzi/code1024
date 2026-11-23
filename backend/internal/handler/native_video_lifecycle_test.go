//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNativeVideoHandlerHistorySharesNativeRouting(t *testing.T) {
	for _, platform := range []string{service.PlatformGrok, service.PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			var h *OpenAIGatewayHandler
			var upstream *grokMediaSlotUpstream
			if platform == service.PlatformGrok {
				h, _, _, upstream = newGrokMediaSlotHandler(t, false, false)
			} else {
				h, _, _, upstream = newGrokMediaSlotHandler(t, false, false, platform)
			}
			history := &nativeVideoHistoryFixture{}
			h.generations = service.NewGenerationService(history, nil, nil, nil, nil, nil)
			defer h.generations.Stop()
			status := "pending"
			upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
				body := `{"request_id":"task"}`
				if req.Method != http.MethodPost {
					body = `{"status":"` + status + `","error":{"message":"private-vendor-error"}}`
				}
				if platform == service.PlatformOpenAI && req.Method == http.MethodPost {
					body = `{"id":"task"}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}
			call := func(method string) *httptest.ResponseRecorder {
				c, w := grokMediaSlotContext(context.Background(), method == http.MethodPost)
				if platform == service.PlatformOpenAI {
					key, _ := middleware.GetAPIKeyFromContext(c)
					key.Group.Platform = platform
					body := ""
					if method == http.MethodPost {
						body = `{"model":"doubao-seedance","content":[{"type":"text","text":"waves"}]}`
					}
					c.Request = httptest.NewRequest(method, "/api/v3/contents/generations/tasks", strings.NewReader(body))
					c.Params = gin.Params{{Key: "task_id", Value: "task"}}
					h.SeedanceTasks(c)
				} else if method == http.MethodPost {
					h.GrokVideoGeneration(c)
				} else {
					h.GrokVideoStatus(c)
				}
				return w
			}
			w := call(http.MethodPost)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Len(t, history.observed, 1)
			create := history.observed[0]
			require.True(t, create.Create)
			require.Equal(t, platform, create.Platform)
			if platform == service.PlatformOpenAI {
				status = "running"
			}
			w = call(http.MethodGet)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Len(t, history.observed, 2)
			require.False(t, history.observed[1].Create)
			require.Equal(t, service.GenerationRunning, history.observed[1].Status)
			require.Equal(t, service.NativeVideoTaskID(create), service.NativeVideoTaskID(history.observed[1]))
			status = "failed"
			w = call(http.MethodGet)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Len(t, history.observed, 3)
			require.Equal(t, service.GenerationFailed, history.observed[2].Status)
			require.Equal(t, "failed", history.observed[2].DeliveryStatus)
		})
	}
}
