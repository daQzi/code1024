package task

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVideoMultipartRepeatedFilesAndStrictTextFields(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	require.NoError(t, w.WriteField("model", "test-video"))
	for _, field := range []string{"image[]", "image[]", "reference_audio"} {
		part, err := w.CreateFormFile(field, "fixture.png")
		require.NoError(t, err)
		_, err = part.Write([]byte("fixture"))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	var calls int
	result, err := ParseVideoMultipart(w.FormDataContentType(), &body, func(field, filename string, source io.Reader) (any, error) {
		calls++
		_, err := io.Copy(io.Discard, source)
		return field, err
	})
	require.NoError(t, err)
	require.Equal(t, 3, calls)
	require.JSONEq(t, `{"model":"test-video","image[]":["image[]","image[]"],"reference_audio":"reference_audio"}`, string(result))
	for _, fields := range [][]string{{"text:input_reference", "file:input_reference"}, {"file:input_reference", "text:input_reference"}, {"text:model", "text:model"}, {"file:model"}, {"file:__proto__"}, {"file:bad\r\nname"}, {"file:image", "file:image", "file:image", "file:image", "file:image"}} {
		body.Reset()
		w = multipart.NewWriter(&body)
		for _, item := range fields {
			kind, name, _ := strings.Cut(item, ":")
			if kind == "text" {
				require.NoError(t, w.WriteField(name, "value"))
			} else {
				_, err = w.CreateFormFile(name, "fixture.png")
				require.NoError(t, err)
			}
		}
		require.NoError(t, w.Close())
		_, err = ParseVideoMultipart(w.FormDataContentType(), &body, func(_, _ string, _ io.Reader) (any, error) { return json.RawMessage(`{}`), nil })
		require.Error(t, err, fields)
	}
}
