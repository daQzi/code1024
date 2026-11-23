package officialupload

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUploadWaitReusesOnlyVerifiedReference(t *testing.T) {
	file := uploadFile("file")
	calls, saves := 0, 0
	do := func(_ context.Context, _, path string, _ map[string]string, body io.Reader, _ int64) (json.RawMessage, error) {
		calls++
		if calls == 1 {
			require.Equal(t, Root, path)
			return uploadJSON(map[string]any{"disposition": "waiting", "ticket": "private-ticket", "retry_after_seconds": 1}), nil
		}
		require.Equal(t, Root+"/wait", path)
		request, err := io.ReadAll(body)
		require.NoError(t, err)
		require.JSONEq(t, `{"ticket":"private-ticket"}`, string(request))
		return uploadJSON(map[string]any{"disposition": "reused", "reference": referenceFor(file)}), nil
	}
	state := &State{}
	require.NoError(t, Ensure(context.Background(), file, strings.NewReader("file"), state, do, func(context.Context) error { saves++; return nil }))
	require.Equal(t, 2, calls)
	require.Equal(t, 1, saves)
	require.NotNil(t, state.Reference)
}

func TestUploadWaitingCancellationStopsBeforeWaitHTTP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	do := func(_ context.Context, _, path string, _ map[string]string, _ io.Reader, _ int64) (json.RawMessage, error) {
		calls++
		cancel()
		return uploadJSON(map[string]any{"disposition": "waiting", "ticket": "ticket", "retry_after_seconds": 15}), nil
	}
	started := time.Now()
	err := Ensure(ctx, uploadFile("file"), strings.NewReader("file"), &State{}, do, func(context.Context) error { return nil })
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
	require.Less(t, time.Since(started), time.Second)
}

func TestUploadAbortAcceptsDocumentedSuccessEnvelopes(t *testing.T) {
	for _, raw := range []string{`{"code":0,"data":null}`, `{"code":0,"data":{}}`, `{"code":0}`} {
		state := &State{Upload: &Upload{Session: "session", Generation: 1, Mode: "single"}}
		do := func(context.Context, string, string, map[string]string, io.Reader, int64) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		}
		require.NoError(t, Abort(context.Background(), state, do, func(context.Context) error { return nil }))
		require.Nil(t, state.Upload)
	}
}
