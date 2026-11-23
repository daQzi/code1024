package officialupload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func uploadFile(data string) File {
	digest := sha256.Sum256([]byte(data))
	return File{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data)), Kind: "image", ContentType: "image/png", IdempotencyKey: "stable-upload"}
}

func uploadJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"code": 0, "data": value})
	return raw
}

func referenceFor(file File) Reference {
	return Reference{ObjectID: "private-object", Kind: file.Kind, SHA256: file.SHA256, Size: file.Size}
}

func TestUploadSingleResumesCompletionWithoutResendingBytes(t *testing.T) {
	ctx := context.Background()
	file, state := uploadFile("first-image"), &State{}
	var checkpoints, relays, completes int
	saved := []byte{}
	save := func(context.Context) error { checkpoints++; saved, _ = json.Marshal(state); return nil }
	do := func(_ context.Context, method, path string, headers map[string]string, body io.Reader, length int64) (json.RawMessage, error) {
		data, err := io.ReadAll(body)
		require.NoError(t, err)
		require.Len(t, data, int(length))
		switch path {
		case Root:
			require.Equal(t, http.MethodPost, method)
			require.Contains(t, string(data), `"idempotency_key":"stable-upload"`)
			return uploadJSON(map[string]any{"disposition": "owner", "upload": map[string]any{"session_token": "secret-session", "generation": 7, "mode": "single", "upload_url": "https://never-fetch.example/object?secret=1"}}), nil
		case Root + "/relay":
			relays++
			require.Positive(t, checkpoints, "must persist owner before sending bytes")
			require.Equal(t, "secret-session", headers["X-Upload-Session"])
			require.Equal(t, "7", headers["X-Upload-Generation"])
			require.Equal(t, "0", headers["X-Upload-Part"])
			require.Equal(t, "first-image", string(data))
			return uploadJSON(map[string]any{}), nil
		case Root + "/complete":
			completes++
			require.NotContains(t, string(data), "parts")
			if completes == 1 {
				return nil, errors.New("completion response lost")
			}
			return uploadJSON(referenceFor(file)), nil
		default:
			t.Fatalf("unexpected path %s", path)
			return nil, nil
		}
	}
	require.Error(t, Ensure(ctx, file, strings.NewReader("first-image"), state, do, save))
	state = &State{}
	require.NoError(t, json.Unmarshal(saved, state))
	require.True(t, state.Relayed)
	require.NoError(t, Ensure(ctx, file, strings.NewReader("first-image"), state, do, save))
	require.Equal(t, 1, relays)
	require.Equal(t, 2, completes)
	require.NotNil(t, state.Reference)
	require.Nil(t, state.Upload)
}

func TestUploadMultipartCheckpointsAndResumesExactSlices(t *testing.T) {
	file, state := uploadFile("abcdEFGhi"), &State{}
	saved := []byte{}
	var first, second, third, complete int
	save := func(context.Context) error { saved, _ = json.Marshal(state); return nil }
	do := func(_ context.Context, method, path string, headers map[string]string, body io.Reader, length int64) (json.RawMessage, error) {
		data, err := io.ReadAll(body)
		require.NoError(t, err)
		switch path {
		case Root:
			return uploadJSON(map[string]any{"disposition": "owner", "upload": Upload{Session: "session", Generation: 1, Mode: "multipart", Parts: []Part{{3, 2}, {1, 4}, {2, 3}}}}), nil
		case Root + "/relay":
			require.Equal(t, http.MethodPut, method)
			require.EqualValues(t, len(data), length)
			part := headers["X-Upload-Part"]
			switch part {
			case "1":
				first++
				require.Equal(t, "abcd", string(data))
			case "2":
				second++
				require.Equal(t, "EFG", string(data))
				if second == 1 {
					return nil, errors.New("interrupted")
				}
			case "3":
				third++
				require.Equal(t, "hi", string(data))
			default:
				t.Fatal(part)
			}
			return uploadJSON(map[string]string{"etag": `"etag-` + part + `"`}), nil
		case Root + "/complete":
			complete++
			var completion struct {
				Parts []CompletedPart `json:"parts"`
			}
			require.NoError(t, json.Unmarshal(data, &completion))
			require.Equal(t, []CompletedPart{{1, `"etag-1"`}, {2, `"etag-2"`}, {3, `"etag-3"`}}, completion.Parts)
			return uploadJSON(referenceFor(file)), nil
		default:
			t.Fatal(path)
			return nil, nil
		}
	}
	require.Error(t, Ensure(context.Background(), file, strings.NewReader("abcdEFGhi"), state, do, save))
	state = &State{}
	require.NoError(t, json.Unmarshal(saved, state))
	require.Len(t, state.Completed, 1)
	require.NoError(t, Ensure(context.Background(), file, strings.NewReader("abcdEFGhi"), state, do, save))
	require.Equal(t, 1, first)
	require.Equal(t, 2, second)
	require.Equal(t, 1, third)
	require.Equal(t, 1, complete)
}

func TestUploadReusedIdentityAndInstructionValidation(t *testing.T) {
	file := uploadFile("file")
	for _, test := range []struct {
		name string
		data any
	}{
		{"wrong-owner", map[string]any{"disposition": "reused", "reference": Reference{ObjectID: "other", Kind: "image", SHA256: strings.Repeat("0", 64), Size: 4}}},
		{"foreign-size", map[string]any{"disposition": "reused", "reference": Reference{ObjectID: "other", Kind: "image", SHA256: file.SHA256, Size: 5}}},
		{"bad-mode", map[string]any{"disposition": "owner", "upload": Upload{Session: "s", Generation: 1, Mode: "other"}}},
		{"missing-session", map[string]any{"disposition": "owner", "upload": Upload{Generation: 1, Mode: "single"}}},
		{"gaps", map[string]any{"disposition": "owner", "upload": Upload{Session: "s", Generation: 1, Mode: "multipart", Parts: []Part{{1, 2}, {3, 2}}}}},
		{"overrun", map[string]any{"disposition": "owner", "upload": Upload{Session: "s", Generation: 1, Mode: "multipart", Parts: []Part{{1, 5}}}}},
		{"duplicate", map[string]any{"disposition": "owner", "upload": Upload{Session: "s", Generation: 1, Mode: "multipart", Parts: []Part{{1, 2}, {1, 2}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			do := func(_ context.Context, _, path string, _ map[string]string, _ io.Reader, _ int64) (json.RawMessage, error) {
				calls++
				require.Equal(t, Root, path)
				return uploadJSON(test.data), nil
			}
			err := Ensure(context.Background(), file, strings.NewReader("file"), &State{}, do, func(context.Context) error { return nil })
			var rejected *Permanent
			require.ErrorAs(t, err, &rejected)
			require.Equal(t, 1, calls)
		})
	}
	for _, kind := range []string{"video", "audio"} {
		file.Kind, file.ContentType = kind, kind+"/fixture"
		require.Error(t, file.Validate())
		file.DurationSeconds = 1.25
		require.NoError(t, file.Validate())
		file.DurationSeconds = 0
	}
}

func TestUploadCheckpointFailureStopsBeforeNetworkAndAbortIsRepeatable(t *testing.T) {
	file := uploadFile("file")
	state, calls := &State{}, 0
	do := func(_ context.Context, _, path string, _ map[string]string, _ io.Reader, _ int64) (json.RawMessage, error) {
		calls++
		switch path {
		case Root:
			return uploadJSON(map[string]any{"disposition": "owner", "upload": Upload{Session: "secret", Generation: 1, Mode: "single"}}), nil
		case Root + "/abort":
			return uploadJSON(map[string]any{}), nil
		default:
			return nil, fmt.Errorf("must not send bytes without a checkpoint")
		}
	}
	err := Ensure(context.Background(), file, strings.NewReader("file"), state, do, func(context.Context) error { return errors.New("lease lost") })
	require.EqualError(t, err, "lease lost")
	require.Equal(t, 1, calls)
	require.NoError(t, Abort(context.Background(), state, do, func(context.Context) error { return nil }))
	require.NoError(t, Abort(context.Background(), state, do, func(context.Context) error { return nil }))
	require.Equal(t, 2, calls)
}
