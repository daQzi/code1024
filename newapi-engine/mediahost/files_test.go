package mediahost

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"

	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/stretchr/testify/require"
)

type mediaFileReader struct {
	io.Reader
	closed *int
}

func (r mediaFileReader) Close() error { *r.closed++; return nil }

func TestTaskMediaFileReferencesInspectWithoutExposingIdentity(t *testing.T) {
	for _, encoding := range []string{"base64", "dataUrl"} {
		t.Run(encoding, func(t *testing.T) {
			closed := 0
			open := func(ctx context.Context, ref string) (pluginTask.File, io.ReadCloser, error) {
				require.NoError(t, ctx.Err())
				require.Equal(t, "request_file:private", ref)
				return pluginTask.File{MimeType: "text/plain", Filename: "private.txt", Size: 6}, mediaFileReader{strings.NewReader("A😀B"), &closed}, nil
			}
			body := []byte(`{"image":{"__fileRef":"request_file:private","encoding":"` + encoding + `"}}`)
			source, err := NewWithFileReferences(context.Background(), body, nil, open)
			require.NoError(t, err)
			require.Equal(t, 1, closed)
			require.JSONEq(t, `{"image":"[media:0]"}`, string(source.View()))
			require.NotContains(t, string(source.View()), "private")
			attachments := source.Attachments()
			require.Len(t, attachments, 1)
			require.Equal(t, "/image", attachments[0].Path)
			require.False(t, attachments[0].Rewritable)
			info, ok := source.Media("[media:0]")
			require.True(t, ok)
			require.Equal(t, 6, info.Size)
			require.Equal(t, "private.txt", info.Filename)
			text, ok := source.MediaText(context.Background(), "[media:0]", 3)
			require.True(t, ok)
			require.Equal(t, "A😀", text.Text)
			require.True(t, text.Truncated)
			reader, size, err := source.FetchBody(`{"file":"[media:0]"}`)
			require.NoError(t, err)
			wire, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.EqualValues(t, len(wire), size)
			require.JSONEq(t, `{"file":"data:text/plain;base64,`+base64.StdEncoding.EncodeToString([]byte("A😀B"))+`"}`, string(wire))
			require.Equal(t, 1, closed, "fetch must reuse the bounded host copy")
			records, _ := source.Records()
			require.Len(t, records, 1)
			require.Equal(t, "checked", records[0].Status)
		})
	}
}

func TestTaskMediaFileReferencesRejectUnknownCorruptAndUnbounded(t *testing.T) {
	for _, body := range []string{
		`{"f":{"__fileRef":"foreign","encoding":"base64"}}`,
		`{"f":{"__fileRef":"owned","encoding":"base64","maxBytes":2}}`,
		`{"f":{"__fileRef":"owned","__fileRef":"owned","encoding":"base64"}}`,
		`{"f":{"__fileRef":"owned","encoding":"dataUrl","mimeType":"text/plain;bad=1"}}`,
		`{"f":{"__fileRef":"owned","encoding":"url"}}`,
		`{"f":{"__fileRef":"owned","encoding":"base64","extra":1}}`,
	} {
		t.Run(body, func(t *testing.T) {
			open := func(_ context.Context, ref string) (pluginTask.File, io.ReadCloser, error) {
				if ref != "owned" {
					return pluginTask.File{}, nil, errors.New("not found")
				}
				return pluginTask.File{MimeType: "text/plain", Size: 3}, io.NopCloser(strings.NewReader("abc")), nil
			}
			_, err := NewWithFileReferences(context.Background(), []byte(body), nil, open)
			require.Error(t, err)
		})
	}
	for _, size := range []int64{0, pluginTask.MaxInlineFileBytes + 1, 2, 4} {
		closed := 0
		_, err := NewWithFileReferences(context.Background(), []byte(`{"f":{"__fileRef":"owned","encoding":"base64"}}`), nil, func(context.Context, string) (pluginTask.File, io.ReadCloser, error) {
			return pluginTask.File{Size: size}, mediaFileReader{strings.NewReader("abc"), &closed}, nil
		})
		require.Error(t, err)
		require.Equal(t, 1, closed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewWithFileReferences(ctx, []byte(`{"f":{"__fileRef":"owned","encoding":"base64"}}`), nil, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestTaskMediaFileReferencesBoundCombinedExpansionBeforeReading(t *testing.T) {
	closed, opened := 0, 0
	open := func(context.Context, string) (pluginTask.File, io.ReadCloser, error) {
		opened++
		return pluginTask.File{Size: pluginTask.MaxInlineFileBytes}, mediaFileReader{strings.NewReader(strings.Repeat("a", int(pluginTask.MaxInlineFileBytes))), &closed}, nil
	}
	_, err := NewWithFileReferences(context.Background(), []byte(`{"files":[{"__fileRef":"same","encoding":"base64"},{"__fileRef":"same","encoding":"base64"}]}`), nil, open)
	require.ErrorContains(t, err, "inline limit")
	require.Equal(t, opened, closed)
}
