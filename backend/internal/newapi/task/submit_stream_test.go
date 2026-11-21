package task

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSubmitStreamCompletionAndFraming(t *testing.T) {
	stream := "\ufeffid: first\r\nevent: update\r\ndata: one\r\ndata: two\r\n\r\ndata: done\n\n"
	calls := 0
	value, err := ReadSubmitEvents(context.Background(), io.NopCloser(strings.NewReader(stream)), false, time.Second, func(_ context.Context, event SubmitEvent, state json.RawMessage) (json.RawMessage, error) {
		calls++
		require.Equal(t, "first", event.ID)
		if calls == 1 {
			require.Equal(t, "null", string(state))
			require.Equal(t, "one\ntwo", event.Data)
			require.Equal(t, "update", event.Event)
			return json.RawMessage(`{"state":{"request_id":"fixture"},"done":false}`), nil
		}
		require.JSONEq(t, `{"request_id":"fixture"}`, string(state))
		require.Equal(t, "message", event.Event)
		return json.RawMessage(`{"state":{"request_id":"fixture"},"done":true}`), nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.JSONEq(t, `{"request_id":"fixture"}`, string(value))
}

func TestSubmitStreamDeltaAndInvalidPartialState(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		value, err := ReadSubmitEvents(context.Background(), io.NopCloser(strings.NewReader("data: result\n\n")), true, time.Second, func(context.Context, SubmitEvent, json.RawMessage) (json.RawMessage, error) {
			if invalid {
				return json.RawMessage(`{"state":{},"done":true,"changes":[{"op":"set","path":[],"value":{}},{"op":"append","path":["missing"],"value":1}]}`), nil
			}
			return json.RawMessage(`{"state":{"sequence":1},"done":true,"changes":[{"op":"set","path":[],"value":{"request_id":"delta-fixture"}}]}`), nil
		})
		if invalid {
			require.Error(t, err)
			require.Nil(t, value, "a partially applied delta cannot be published")
		} else {
			require.NoError(t, err)
			require.JSONEq(t, `{"request_id":"delta-fixture"}`, string(value))
		}
	}
}

func TestSubmitStreamRejectsTruncationAndLimits(t *testing.T) {
	for _, stream := range []string{
		"data: partial\n\n",
		"data: unterminated",
		strings.Repeat(": short comment\n", MaxSnapshotBytes/10),
		strings.Repeat(": heartbeat\n\n", 4097),
	} {
		value, err := ReadSubmitEvents(context.Background(), io.NopCloser(strings.NewReader(stream)), false, time.Second, func(context.Context, SubmitEvent, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"state":{},"done":false}`), nil
		})
		require.Error(t, err)
		require.Nil(t, value)
	}
}

func TestSubmitStreamCancellationAndIdleCloseBody(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		reader, writer := io.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			cancel()
		}
		_, err := ReadSubmitEvents(ctx, reader, false, 10*time.Millisecond, nil)
		cancel()
		require.Error(t, err)
		_, writeErr := writer.Write([]byte("data: late\n\n"))
		require.Error(t, writeErr, "cancellation must close and release the upstream body")
		writer.Close()
	}
}
