package task

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskFileMultipartStreamsOpaqueReferences(t *testing.T) {
	content := strings.Repeat("0123456789", 200000)
	closed := 0
	open := func(ctx context.Context, ref string) (File, io.ReadCloser, error) {
		require.NoError(t, ctx.Err())
		require.Equal(t, "request_file:input_reference", ref)
		return File{Filename: "ocean.png", MimeType: "image/png", Size: int64(len(content))}, &fileTestReader{Reader: strings.NewReader(content), close: func() { closed++ }}, nil
	}
	var out bytes.Buffer
	kind, err := WriteMultipart(context.Background(), &out, []Part{{Name: "prompt", Value: json.RawMessage(`"a quiet ocean"`)}, {Name: "input_reference", FileRef: "request_file:input_reference"}}, open)
	require.NoError(t, err)
	require.Equal(t, 1, closed)
	_, params, err := mime.ParseMediaType(kind)
	require.NoError(t, err)
	reader := multipart.NewReader(&out, params["boundary"])
	part, err := reader.NextPart()
	require.NoError(t, err)
	data, err := io.ReadAll(part)
	require.NoError(t, err)
	require.Equal(t, "a quiet ocean", string(data))
	part, err = reader.NextPart()
	require.NoError(t, err)
	require.Equal(t, "input_reference", part.FormName())
	require.Equal(t, "ocean.png", part.FileName())
	data, err = io.ReadAll(part)
	require.NoError(t, err)
	require.Equal(t, content, string(data))
}

func BenchmarkTaskFileMultipart(b *testing.B) {
	for _, size := range []int64{64 << 10, 2 << 20, 32 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			open := func(context.Context, string) (File, io.ReadCloser, error) {
				return File{Filename: "reference.png", MimeType: "image/png", Size: size}, io.NopCloser(io.LimitReader(fileZeroReader{}, size)), nil
			}
			b.ReportAllocs()
			b.SetBytes(size)
			for b.Loop() {
				_, err := WriteMultipart(context.Background(), io.Discard, []Part{{Name: "input_reference", FileRef: "request_file:input_reference"}}, open)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type fileZeroReader struct{}

func (fileZeroReader) Read(dst []byte) (int, error) { clear(dst); return len(dst), nil }

type fileTestReader struct {
	io.Reader
	close func()
}

func (r *fileTestReader) Close() error { r.close(); return nil }

func TestTaskFilesStrictPlaceholdersAndResourceLimits(t *testing.T) {
	open := func(_ context.Context, ref string) (File, io.ReadCloser, error) {
		if ref != "request_file:input_reference" {
			return File{}, nil, errors.New("unknown file")
		}
		return File{MimeType: "image/png", Size: 3}, io.NopCloser(strings.NewReader("abc")), nil
	}
	out, err := ResolveFileJSON(context.Background(), json.RawMessage(`{"images":[{"url":{"__fileRef":"request_file:input_reference","encoding":"dataUrl"}}]}`), open)
	require.NoError(t, err)
	require.JSONEq(t, `{"images":[{"url":"data:image/png;base64,YWJj"}]}`, string(out))
	out, err = ResolveFileJSON(context.Background(), json.RawMessage(`{"__fileRef":"request_file:input_reference","encoding":"base64","maxBytes":20971520}`), open)
	require.NoError(t, err)
	require.JSONEq(t, `"YWJj"`, string(out))
	for _, body := range []string{
		`{"__fileRef":"/etc/passwd","encoding":"base64"}`,
		`{"__fileRef":"request_file:input_reference","encoding":"url"}`,
		`{"__fileRef":"request_file:input_reference","encoding":"base64","other":true}`,
		`{"__fileRef":"request_file:input_reference","encoding":"base64","maxBytes":2}`,
		`{"__fileRef":"request_file:input_reference","encoding":"base64","maxBytes":1.5}`,
		`{"__fileRef":"request_file:input_reference","encoding":"dataUrl","mimeType":"text/plain;evil=1"}`,
	} {
		t.Run(body, func(t *testing.T) {
			_, err := ResolveFileJSON(context.Background(), json.RawMessage(body), open)
			require.Error(t, err)
		})
	}
	oversize := func(context.Context, string) (File, io.ReadCloser, error) {
		return File{Size: MaxInlineFileBytes + 1}, io.NopCloser(strings.NewReader("")), nil
	}
	_, err = ResolveFileJSON(context.Background(), json.RawMessage(`{"__fileRef":"ref","encoding":"base64"}`), oversize)
	require.ErrorContains(t, err, "inline file")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = WriteMultipart(ctx, io.Discard, []Part{{Name: "f", FileRef: "ref"}}, open)
	require.ErrorIs(t, err, context.Canceled)
	for _, size := range []int64{2, 4, MaxFileBytes + 1} {
		closed := false
		bad := func(context.Context, string) (File, io.ReadCloser, error) {
			return File{Size: size, Filename: "x.png", MimeType: "image/png"}, &fileTestReader{Reader: strings.NewReader("abc"), close: func() { closed = true }}, nil
		}
		_, err = WriteMultipart(context.Background(), io.Discard, []Part{{Name: "f", FileRef: "ref"}}, bad)
		require.Error(t, err)
		require.True(t, closed)
	}
}
