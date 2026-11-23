package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type mediaPerformanceFixture struct {
	samples []RelayPerformanceSample
}

func (s *mediaPerformanceFixture) Record(sample RelayPerformanceSample) {
	s.samples = append(s.samples, sample)
}

func TestMediaWSPerformanceTerminalFacts(t *testing.T) {
	for _, tc := range []struct {
		name, platform, terminal string
		count                    int
		success                  bool
	}{
		{"completed", PlatformOpenAI, `{"type":"response.done","response":{"id":"r1","status":"completed","usage":{"output_tokens":200}}}`, 1, true},
		{"grok done", PlatformGrok, `{"type":"response.done","response":{"id":"r1"}}`, 1, true},
		{"missing Live status", PlatformOpenAI, `{"type":"response.done","response":{"id":"r1"}}`, 1, false},
		{"failed", PlatformOpenAI, `{"type":"response.done","response":{"id":"r1","status":"failed","status_details":{"error":{"code":"server_error"}}}}`, 1, false},
		{"bad input", PlatformOpenAI, `{"type":"response.done","response":{"id":"r1","status":"failed","status_details":{"error":{"code":"invalid_request_error"}}}}`, 0, false},
		{"cancelled", PlatformOpenAI, `{"type":"response.done","response":{"id":"r1","status":"cancelled"}}`, 0, false},
		{"cancel event", PlatformGrok, `{"type":"response.cancelled","response":{"id":"r1"}}`, 0, false},
		{"token bound", PlatformOpenAI, `{"type":"response.done","response":{"id":"r1","status":"incomplete","status_details":{"reason":"max_output_tokens"}}}`, 1, true},
		{"unknown incomplete", PlatformOpenAI, `{"type":"response.done","response":{"id":"r1","status":"incomplete"}}`, 1, false},
		{"filter", PlatformOpenAI, `{"type":"response.done","response":{"id":"r1","status":"incomplete","status_details":{"reason":"content_filter"}}}`, 0, false},
		{"upstream alias", PlatformOpenAI, `{"type":"response.done","response":{"id":"r1","status":"completed","model":"private-upstream-alias"}}`, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &mediaPerformanceFixture{}
			p := &mediaWSPerformance{sink: sink, session: "call-hash", group: 3, platform: tc.platform, model: "public-voice"}
			now := time.Now().UTC()
			p.observe([]byte(`{"type":"session.created"}`), now)
			p.observe([]byte(`{"type":"response.created","response":{"id":"r1"}}`), now)
			p.observe([]byte(`{"type":"response.created","response":{"id":"r1"}}`), now.Add(time.Second))
			p.observe([]byte(tc.terminal), now.Add(2*time.Second))
			p.observe([]byte(tc.terminal), now.Add(3*time.Second))
			p.observe([]byte(`{"type":"session.closed"}`), now.Add(4*time.Second))
			require.Len(t, sink.samples, tc.count)
			if tc.count > 0 {
				require.Equal(t, tc.success, sink.samples[0].Success)
				require.Equal(t, "public-voice", sink.samples[0].Model)
				require.EqualValues(t, 2000, sink.samples[0].DurationMs)
				require.Nil(t, sink.samples[0].FirstTokenMs)
				require.Zero(t, sink.samples[0].OutputTokens)
				require.True(t, sink.samples[0].Valid())
			}
		})
	}
}

func TestMediaWSPerformanceBoundedAndStableAcrossReconnects(t *testing.T) {
	sink := &mediaPerformanceFixture{}
	p := &mediaWSPerformance{sink: sink, session: "same-call", group: 3, platform: PlatformOpenAI, model: "public-voice"}
	now := time.Now().UTC()
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.observe([]byte(fmt.Sprintf(`{"type":"response.created","response":{"id":"r%d"}}`, i)), now)
		}()
	}
	wg.Wait()
	for i := range 12 {
		p.observe([]byte(fmt.Sprintf(`{"type":"response.done","response":{"id":"r%d","status":"completed"}}`, i)), now.Add(time.Second))
	}
	require.Len(t, sink.samples, 8)
	p.observe([]byte(`{"type":"response.done","response":{"id":"unobserved","status":"completed"}}`), now)
	require.Len(t, sink.samples, 8)
	first := sink.samples[0]
	// Reconnect replay uses exactly the same database idempotency key.
	replayed := &mediaPerformanceFixture{}
	p2 := &mediaWSPerformance{sink: replayed, session: "same-call", group: 3, platform: PlatformOpenAI, model: "public-voice"}
	var id string
	for i := range 12 {
		candidate := fmt.Sprintf("r%d", i)
		p2.observe([]byte(fmt.Sprintf(`{"type":"response.created","response":{"id":%q}}`, candidate)), now)
		p2.observe([]byte(fmt.Sprintf(`{"type":"response.done","response":{"id":%q,"status":"completed"}}`, candidate)), now.Add(time.Second))
		if replayed.samples[len(replayed.samples)-1].ID == first.ID {
			id = candidate
		}
	}
	require.NotEmpty(t, id)
	// Oversized/invalid IDs and unfinished turns never create outcomes.
	p2.observe([]byte(`{"type":"response.created","response":{"id":"`+strings.Repeat("x", 129)+`"}}`), now)
	require.Len(t, replayed.samples, 12)
}

func TestGrokRealtimePerformanceObservesActualForwardedTurns(t *testing.T) {
	repo := &relayPerformanceRepoFixture{}
	recorder := NewRelayPerformanceRecorder(repo)
	t.Cleanup(recorder.Stop)
	svc := &OpenAIGatewayService{performanceRecorder: recorder}
	upstream := newLiveTestFrameConn()
	defer upstream.Close()
	result := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		client, err := coderws.Accept(writer, req, nil)
		if err != nil {
			result <- err
			return
		}
		defer client.CloseNow()
		c, _ := gin.CreateTestContext(writer)
		c.Request = req
		group := int64(7)
		c.Set("api_key", &APIKey{GroupID: &group})
		_, err = svc.ProxyGrokRealtimeConn(req.Context(), c, client, &GrokRealtimeUpstream{conn: upstream, model: "public-voice"})
		result <- err
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer client.CloseNow()
	for _, body := range []string{`{"type":"response.created","response":{"id":"r1"}}`, `{"type":"response.output_audio.delta","delta":"audio"}`, `{"type":"response.done","response":{"id":"r1"}}`} {
		upstream.reads <- liveTestFrame{messageType: coderws.MessageText, payload: []byte(body)}
		_, got, readErr := client.Read(ctx)
		require.NoError(t, readErr)
		require.JSONEq(t, body, string(got))
	}
	upstream.reads <- liveTestFrame{err: coderws.CloseError{Code: coderws.StatusNormalClosure}}
	select {
	case <-result:
	case <-ctx.Done():
		t.Fatal("realtime relay did not finish")
	}
	recorder.Stop()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.rows, 1)
	require.Equal(t, "public-voice", repo.rows[0].Model)
	require.Equal(t, PlatformGrok, repo.rows[0].Platform)
	require.EqualValues(t, 7, repo.rows[0].GroupID)
	require.True(t, repo.rows[0].Success)
}

func TestLiveObserverPerformanceDoesNotCountSessionClosure(t *testing.T) {
	for _, publicModel := range []string{"public-live", ""} {
		t.Run(publicModel, func(t *testing.T) {
			repo := &relayPerformanceRepoFixture{}
			recorder := NewRelayPerformanceRecorder(repo)
			t.Cleanup(recorder.Stop)
			svc := &OpenAIGatewayService{cache: &liveTestStore{}, performanceRecorder: recorder}
			upstream := newLiveTestFrameConn()
			defer upstream.Close()
			record := &LiveCallRecord{PublicModel: publicModel, Model: "private-upstream-alias", CallHash: "call-hash", GroupID: 3, ExpiresAt: time.Now().Add(time.Minute)}
			upstream.reads <- liveTestFrame{messageType: coderws.MessageText, payload: []byte(`{"type":"response.created","response":{"id":"r1"}}`)}
			upstream.reads <- liveTestFrame{messageType: coderws.MessageText, payload: []byte(`{"type":"response.done","response":{"id":"r1","status":"completed"}}`)}
			upstream.reads <- liveTestFrame{messageType: coderws.MessageText, payload: []byte(`{"type":"session.closed"}`)}
			require.ErrorIs(t, svc.runLiveObserverConnection(record, upstream), ErrLiveCallNotFound)
			recorder.Stop()
			repo.mu.Lock()
			defer repo.mu.Unlock()
			if publicModel == "" {
				require.Empty(t, repo.rows, "legacy records cannot prove a public alias")
			} else {
				require.Len(t, repo.rows, 1)
				require.Equal(t, publicModel, repo.rows[0].Model)
			}
		})
	}
}
