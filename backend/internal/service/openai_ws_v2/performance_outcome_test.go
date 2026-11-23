package openai_ws_v2

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/relaymetrics"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestRelayOutcomeRunsAfterWriteAndKeepsBillingSemantics(t *testing.T) {
	completed := `{"type":"response.completed","response":{"id":"resp_one","usage":{"input_tokens":3,"output_tokens":2}}}`
	failed := `{"type":"response.failed","response":{"id":"resp_one","error":{"code":"server_error"},"usage":{"input_tokens":3,"output_tokens":2}}}`
	bareError := `{"type":"error","error":{"code":"server_error"}}`
	for _, tc := range []struct {
		name        string
		frames      []string
		binary      bool
		drop        bool
		writeErr    bool
		wantBilling int
		wantOutcome relaymetrics.Outcome
	}{
		{"completed", []string{completed}, false, false, false, 1, relaymetrics.Success},
		{"failed", []string{failed}, false, false, false, 1, relaymetrics.Failure},
		{"failed terminal write", []string{completed}, false, false, true, 1, relaymetrics.Success},
		{"disconnect drain", []string{completed}, false, true, false, 1, relaymetrics.Success},
		{"error then authoritative failure", []string{bareError, failed}, false, false, false, 1, relaymetrics.Failure},
		{"standalone error", []string{bareError}, false, false, false, 1, relaymetrics.Failure},
		{"binary terminal observes without new billing", []string{completed}, true, false, false, 0, relaymetrics.Success},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := make([]passthroughTestFrame, len(tc.frames))
			for i, body := range tc.frames {
				msgType := coderws.MessageText
				if tc.binary {
					msgType = coderws.MessageBinary
				}
				frames[i] = passthroughTestFrame{msgType: msgType, payload: []byte(body)}
			}
			state := &relayState{}
			state.setRequestModel("public-model")
			drop := &atomic.Bool{}
			drop.Store(tc.drop)
			writes, billing, outcomes := 0, 0, 0
			exit := make(chan relayExitSignal, 1)
			runUpstreamToClient(context.Background(), newPassthroughTestFrameConn(frames, true),
				func(coderws.MessageType, []byte) error {
					writes++
					if tc.writeErr {
						return errors.New("client left")
					}
					return nil
				},
				time.Now().Add(-time.Second), time.Now, state, nil,
				func(RelayTurnResult) { billing++ },
				func(turn RelayTurnResult, disconnected bool) {
					outcomes++
					require.Equal(t, tc.wantOutcome, turn.PerformanceOutcome)
					require.Equal(t, tc.drop || tc.writeErr, disconnected)
					if !tc.drop {
						require.Positive(t, writes, "health callback must follow the terminal write attempt")
					}
				},
				nil, nil, nil, nil, drop, nil, nil, func() {}, nil, exit)
			<-exit
			require.Equal(t, tc.wantBilling, billing)
			require.Equal(t, 1, outcomes, "bare error followed by response.failed must settle once")
		})
	}
}
