package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/relaymetrics"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type relaySinkFixture struct {
	samples []service.RelayPerformanceSample
}

type failedRelayResponseWriter struct {
	*httptest.ResponseRecorder
	short bool
}

func (w failedRelayResponseWriter) Write(body []byte) (int, error) {
	if w.short {
		return len(body) / 2, nil
	}
	return 0, io.ErrClosedPipe
}

func TestRelayPerformanceMediaFinalBoundary(t *testing.T) {
	for _, path := range []string{"/v1/embeddings", "/openai/v1/images/generations", "/v1/images/edits", "/v1/tts", "/v1/stt"} {
		for _, tc := range []struct {
			name    string
			outcome relaymetrics.Outcome
			want    int
		}{
			{"output", relaymetrics.Success, 1},
			{"no output", relaymetrics.Failure, 1},
			{"refusal", relaymetrics.Ignored, 0},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				sink := &relaySinkFixture{}
				router := gin.New()
				router.Use(OpsErrorLoggerMiddleware(nil, sink))
				router.POST(path, func(c *gin.Context) {
					group := int64(3)
					c.Set("api_key", &service.APIKey{ID: 4, GroupID: &group, Group: &service.Group{ID: group, Platform: "openai"}})
					setOpsRequestContext(c, "public-media", false)
					setOpsSelectedAccount(c, 5, "openai")
					first := 1
					setOpenAIForwardPerformance(c, &service.OpenAIForwardResult{PerformanceOutcome: tc.outcome, FirstTokenMs: &first, Usage: service.OpenAIUsage{OutputTokens: 100}}, nil)
					c.JSON(200, gin.H{"result": "media"})
				})
				router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, nil))
				require.Len(t, sink.samples, tc.want)
				if tc.want != 0 {
					require.Equal(t, tc.outcome == relaymetrics.Success, sink.samples[0].Success)
					require.Nil(t, sink.samples[0].FirstTokenMs)
					require.Zero(t, sink.samples[0].OutputTokens)
				}
			})
		}
	}
}

func TestRelayPerformanceFailedDeliveryIsExcluded(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(map[bool]string{true: "short write", false: "write error"}[short], func(t *testing.T) {
			sink := &relaySinkFixture{}
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(nil, sink))
			router.POST("/v1/embeddings", func(c *gin.Context) {
				group := int64(3)
				c.Set("api_key", &service.APIKey{ID: 4, GroupID: &group, Group: &service.Group{ID: group, Platform: "openai"}})
				setOpsRequestContext(c, "public-media", false)
				setOpenAIForwardPerformance(c, &service.OpenAIForwardResult{PerformanceOutcome: relaymetrics.Success}, nil)
				_, _ = c.Writer.Write([]byte(`{"data":[{"embedding":[1]}]}`))
			})
			router.ServeHTTP(failedRelayResponseWriter{ResponseRecorder: httptest.NewRecorder(), short: short}, httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil))
			require.Empty(t, sink.samples)
		})
	}
}

func (s *relaySinkFixture) RecordRelayPerformance(p service.RelayPerformanceSample) {
	s.samples = append(s.samples, p)
}

func TestRelayPerformanceVoiceHandshakeIsNotGeneration(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/realtime"},
		{http.MethodPost, "/v1/live"},
		{http.MethodPost, "/backend-api/codex/realtime/calls"},
	} {
		for _, status := range []int{200, 502} {
			t.Run(tc.method+tc.path+http.StatusText(status), func(t *testing.T) {
				sink := &relaySinkFixture{}
				router := gin.New()
				router.Use(OpsErrorLoggerMiddleware(nil, sink))
				router.Handle(tc.method, tc.path, func(c *gin.Context) {
					group := int64(3)
					c.Set("api_key", &service.APIKey{ID: 4, GroupID: &group, Group: &service.Group{ID: group, Platform: "openai"}})
					setOpsRequestContext(c, "public-voice", false)
					if tc.method == http.MethodGet {
						setOpsEndpointContext(c, "", int16(service.RequestTypeWSV2))
					} else {
						setOpsEndpointContext(c, "", int16(service.RequestTypeLive))
					}
					c.Status(status)
				})
				router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, nil))
				if status < 400 {
					require.Empty(t, sink.samples, "successful handshake/SDP proves no completed turn")
				} else {
					require.Len(t, sink.samples, 1)
					require.False(t, sink.samples[0].Success)
				}
			})
		}
	}
}

func TestRelayPerformanceFinalBoundaryIndependentOfLogging(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name     string
		run      func(*gin.Context)
		expected int
		success  bool
	}{
		{"zero cost success", func(c *gin.Context) {
			setOpenAIForwardPerformance(c, &service.OpenAIForwardResult{}, nil)
			c.JSON(200, gin.H{"ok": true})
		}, 1, true},
		{"retry then success once", func(c *gin.Context) {
			setOpenAIForwardPerformance(c, nil, errors.New("attempt failed"))
			setOpenAIForwardPerformance(c, &service.OpenAIForwardResult{}, nil)
			c.Status(200)
		}, 1, true},
		{"partial usage with truncated stream", func(c *gin.Context) {
			setOpenAIForwardPerformance(c, &service.OpenAIForwardResult{Stream: true, Usage: service.OpenAIUsage{OutputTokens: 5}}, errors.New("truncated"))
			c.Header("Content-Type", "text/event-stream")
			c.String(200, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		}, 1, false},
		{"HTTP200 explicit SSE failure", func(c *gin.Context) {
			setOpenAIForwardPerformance(c, &service.OpenAIForwardResult{Stream: true}, nil)
			c.Header("Content-Type", "text/event-stream")
			c.String(200, "event: error\ndata: {\"error\":{\"type\":\"server_error\",\"message\":\"failed\"}}\n\n")
		}, 1, false},
		{"marked in-band failure", func(c *gin.Context) {
			setOpenAIForwardPerformance(c, &service.OpenAIForwardResult{Stream: true}, nil)
			service.MarkOpsStreamFailure(c, "server_error", "server_error", "failed", 502)
			c.Status(200)
		}, 1, false},
		{"upstream limit", func(c *gin.Context) { c.JSON(429, gin.H{"error": gin.H{"type": "rate_limit_error"}}) }, 1, false},
		{"upstream credentials with generic type", func(c *gin.Context) {
			c.JSON(400, gin.H{"error": gin.H{"type": "invalid_request_error", "code": "invalid_api_key"}})
		}, 1, false},
		{"bad input", func(c *gin.Context) { c.JSON(400, gin.H{"error": gin.H{"type": "invalid_request_error"}}) }, 0, false},
		{"content filter", func(c *gin.Context) { c.JSON(403, gin.H{"error": gin.H{"code": "content_policy_violation"}}) }, 0, false},
		{"local quota", func(c *gin.Context) { service.MarkOpsClientBusinessLimited(c, "quota"); c.Status(429) }, 0, false},
		{"local reply policy with measured usage", func(c *gin.Context) {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
			setOpenAIForwardPerformance(c, &service.OpenAIForwardResult{Stream: true, Usage: service.OpenAIUsage{OutputTokens: 5}}, &service.RequestPluginFailure{Rejected: true})
			c.Header("Content-Type", "text/event-stream")
			c.String(200, "data: {\"error\":{\"type\":\"request_processing_error\",\"code\":\"response_processing_rejected\"}}\n\n")
		}, 0, false},
		{"cancelled partial", func(c *gin.Context) {
			ctx, cancel := context.WithCancel(c.Request.Context())
			c.Request = c.Request.WithContext(ctx)
			cancel()
			setOpenAIForwardPerformance(c, &service.OpenAIForwardResult{Stream: true}, errors.New("cancelled"))
			c.Status(200)
		}, 0, false},
		{"cancelled after completed response", func(c *gin.Context) {
			ctx, cancel := context.WithCancel(c.Request.Context())
			c.Request = c.Request.WithContext(ctx)
			setOpenAIForwardPerformance(c, &service.OpenAIForwardResult{}, nil)
			c.Status(200)
			cancel()
		}, 1, true},
		{"unverified HTTP200", func(c *gin.Context) { c.Status(200) }, 0, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			sink := &relaySinkFixture{}
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(nil, sink))
			router.POST("/v1/chat/completions", func(c *gin.Context) {
				group := int64(3)
				c.Set("api_key", &service.APIKey{ID: 4, GroupID: &group, Group: &service.Group{ID: group, Platform: "openai"}})
				setOpsRequestContext(c, "public-model", false)
				setOpsSelectedAccount(c, 5, "openai")
				tt.run(c)
			})
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
			require.Len(t, sink.samples, tt.expected)
			if tt.expected > 0 {
				require.Equal(t, tt.success, sink.samples[0].Success)
				require.True(t, sink.samples[0].Valid())
			}
		})
	}
}
func TestRelayPerformanceVideoSubmissionAndLookupAreNotCompletion(t *testing.T) {
	for _, path := range []string{"/v1/videos", "/v1/videos/video_x", "/v1/contents/generations/tasks", "/v1/messages/count_tokens"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, path, nil)
		require.False(t, relayPerformancePath(c))
	}
}
func TestRelayPerformanceAttemptCopiesTiming(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	first := 5
	result := &service.ForwardResult{Duration: 100 * time.Millisecond, FirstTokenMs: &first}
	setForwardPerformance(c, result, nil)
	first = 999
	snapshot, _ := c.Get(relayPerformanceAttemptKey)
	require.EqualValues(t, 5, *snapshot.(relayPerformanceAttempt).first)
}

func TestRelayPerformanceWebSocketBridgeUsesFinalSource(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome relaymetrics.Outcome
		count   int
		success bool
	}{
		{"completed", relaymetrics.Success, 1, true},
		{"terminal failure", relaymetrics.Failure, 1, false},
		{"content or cancellation", relaymetrics.Ignored, 0, false},
		{"missing terminal", relaymetrics.Unknown, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &relaySinkFixture{}
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(nil, sink))
			router.POST("/v1/responses", func(c *gin.Context) {
				group := int64(3)
				c.Set("api_key", &service.APIKey{ID: 4, GroupID: &group, Group: &service.Group{ID: group, Platform: "openai"}})
				setOpsRequestContext(c, "public-model", true)
				setOpenAIForwardPerformance(c, &service.OpenAIForwardResult{OpenAIWSMode: true, PerformanceOutcome: tc.outcome}, nil)
				c.Status(200)
			})
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
			require.Len(t, sink.samples, tc.count)
			if tc.count > 0 {
				require.Equal(t, tc.success, sink.samples[0].Success)
			}
		})
	}
}
