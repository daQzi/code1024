package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/relaymetrics"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type wsPerformanceSinkFixture struct {
	samples chan service.RelayPerformanceSample
}

func (s *wsPerformanceSinkFixture) RecordRelayPerformance(sample service.RelayPerformanceSample) {
	s.samples <- sample
}

func wsPerformanceFixture(t *testing.T) (*wsRelayPerformance, *wsPerformanceSinkFixture) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	sink := &wsPerformanceSinkFixture{make(chan service.RelayPerformanceSample, 16)}
	c.Set(relayPerformanceSinkKey, sink)
	group := int64(7)
	return newWSRelayPerformance(c, &group, "openai", "public-first", time.Now().Add(-time.Second)), sink
}

func TestWSRelayPerformanceLogicalTurns(t *testing.T) {
	p, sink := wsPerformanceFixture(t)
	first := 100
	p.attemptFailed(&service.UpstreamFailoverError{StatusCode: 429})
	p.begin("upstream-secret-alias", time.Now()) // new account, same logical turn
	p.complete(&service.OpenAIForwardResult{PerformanceOutcome: relaymetrics.Success, Duration: 500 * time.Millisecond, FirstTokenMs: &first, Usage: service.OpenAIUsage{OutputTokens: 20}}, nil)
	p.next("public-second", time.Now().Add(-200*time.Millisecond))
	p.complete(&service.OpenAIForwardResult{PerformanceOutcome: relaymetrics.Failure, Usage: service.OpenAIUsage{OutputTokens: 3}}, nil)
	p.finish(context.Background()) // idle disconnect does not invent a third turn
	require.Len(t, sink.samples, 2)
	a, b := <-sink.samples, <-sink.samples
	require.True(t, a.Success)
	require.Equal(t, "public-first", a.Model)
	require.GreaterOrEqual(t, a.DurationMs, int64(1000))
	require.GreaterOrEqual(t, *a.FirstTokenMs, int64(600))
	require.EqualValues(t, 20, a.OutputTokens)
	require.EqualValues(t, 7, a.GroupID)
	require.False(t, b.Success)
	require.Equal(t, "public-second", b.Model)
}

func TestWSRelayPerformancePendingBareErrorKeepsSuccessor(t *testing.T) {
	p, sink := wsPerformanceFixture(t)
	p.next("public-next", time.Now())
	p.complete(&service.OpenAIForwardResult{PerformanceOutcome: relaymetrics.Failure}, nil)
	p.complete(&service.OpenAIForwardResult{PerformanceOutcome: relaymetrics.Success}, nil)
	require.Len(t, sink.samples, 2)
	require.Equal(t, "public-first", (<-sink.samples).Model)
	require.Equal(t, "public-next", (<-sink.samples).Model)
}

func TestWSRelayPerformanceCancellationPolicyAndIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*wsRelayPerformance)
		want int
	}{
		{"final failover exhaustion", func(p *wsRelayPerformance) {
			p.attemptFailed(&service.UpstreamFailoverError{StatusCode: 503})
			p.finish(context.Background())
		}, 1},
		{"credential failure with bad status", func(p *wsRelayPerformance) {
			p.attemptFailed(&service.UpstreamFailoverError{StatusCode: 400, ResponseBody: []byte(`{"error":{"code":"invalid_api_key","type":"invalid_request_error"}}`)})
			p.finish(context.Background())
		}, 1},
		{"client cancellation", func(p *wsRelayPerformance) { p.attemptFailed(context.Canceled); p.finish(context.Background()) }, 0},
		{"completed after disconnect drain", func(p *wsRelayPerformance) {
			p.complete(&service.OpenAIForwardResult{PerformanceOutcome: relaymetrics.Success, ClientDisconnect: true}, nil)
		}, 0},
		{"ignored terminal with bridge error", func(p *wsRelayPerformance) {
			p.complete(&service.OpenAIForwardResult{PerformanceOutcome: relaymetrics.Ignored}, errors.New("bad request"))
			p.finish(context.Background())
		}, 0},
		{"completed then caller cancellation", func(p *wsRelayPerformance) {
			p.complete(&service.OpenAIForwardResult{PerformanceOutcome: relaymetrics.Success}, nil)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			p.finish(ctx)
		}, 1},
		{"partial bill without terminal", func(p *wsRelayPerformance) {
			p.complete(&service.OpenAIForwardResult{Usage: service.OpenAIUsage{OutputTokens: 10}}, nil)
			p.attemptFailed(errors.New("upstream truncated"))
			p.finish(context.Background())
		}, 1},
		{"local admission", func(p *wsRelayPerformance) {
			p.attemptFailed(service.NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "busy", nil))
			p.finish(context.Background())
		}, 0},
		{"local business rejection", func(p *wsRelayPerformance) {
			p.ignore()
			p.complete(&service.OpenAIForwardResult{PerformanceOutcome: relaymetrics.Success}, nil)
		}, 0},
		{"ignored attempt then healthy retry", func(p *wsRelayPerformance) {
			p.attemptFailed(&service.UpstreamFailoverError{StatusCode: 400})
			p.complete(&service.OpenAIForwardResult{PerformanceOutcome: relaymetrics.Success}, nil)
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, sink := wsPerformanceFixture(t)
			tc.run(p)
			require.Len(t, sink.samples, tc.want)
		})
	}
}

func TestWSRelayPerformanceBoundsAndConcurrentClose(t *testing.T) {
	p, sink := wsPerformanceFixture(t)
	p.complete(&service.OpenAIForwardResult{PerformanceOutcome: relaymetrics.Ignored}, nil)
	p.next(strings.Repeat("m", 101), time.Now())
	require.False(t, p.active)
	p.next("last-public-model", time.Now())
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.complete(&service.OpenAIForwardResult{PerformanceOutcome: relaymetrics.Success}, nil)
			p.finish(context.Background())
		}()
	}
	wg.Wait()
	require.Len(t, sink.samples, 1)
	require.True(t, (<-sink.samples).Valid())
}

func TestWSRelayPerformanceRealIngressModes(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModeDedicated, service.OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			sink := &wsPerformanceSinkFixture{make(chan service.RelayPerformanceSample, 16)}
			got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				performanceSink: sink, ingressMode: mode,
				firstPayload:        `{"type":"response.create","model":"sol","stream":false}`,
				secondPayload:       `{"type":"response.create","model":"terra","stream":false}`,
				channelMapping:      map[string]string{"sol": "sol-channel", "terra": "terra-channel"},
				accountModelMapping: map[string]any{"sol": "gpt-5.6-sol", "terra": "gpt-5.6-terra", "sol-channel": "gpt-5.6-sol", "terra-channel": "gpt-5.6-terra"},
			})
			require.Len(t, got.logs, 2)
			for _, model := range []string{"sol", "terra"} {
				select {
				case sample := <-sink.samples:
					require.Equal(t, model, sample.Model)
					require.True(t, sample.Success)
					require.EqualValues(t, 1, sample.OutputTokens)
					require.True(t, sample.Valid())
				case <-time.After(3 * time.Second):
					t.Fatal("missing independent WS turn sample")
				}
			}
			require.Empty(t, sink.samples, "outer HTTP logger must not duplicate WS turns")
		})
	}
}

func TestWSRelayPerformanceRealGrokHTTPBridge(t *testing.T) {
	sink := &wsPerformanceSinkFixture{make(chan service.RelayPerformanceSample, 16)}
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		performanceSink: sink, accountPlatform: service.PlatformGrok,
		group:             compositeWSGroup("public-alias"),
		compositeResolver: compositeWSResolver(service.PlatformGrok, service.CompositeRouteEndpointResponses, "grok-4.3"),
		firstPayload:      `{"type":"response.create","model":"public-alias","input":"hi"}`,
	})
	require.NotNil(t, got.log)
	select {
	case sample := <-sink.samples:
		require.Equal(t, "grok", sample.Platform)
		require.Equal(t, "public-alias", sample.Model)
		require.True(t, sample.Success)
	case <-time.After(3 * time.Second):
		t.Fatal("missing Grok WS/HTTP bridge outcome")
	}
	require.Empty(t, sink.samples)
}
