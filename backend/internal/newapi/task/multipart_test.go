package task

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMultipartCodecPreservesScalarTextAndBoundary(t *testing.T) {
	parts := []Part{{Name: "prompt", Value: json.RawMessage(`"ocean\n sunrise"`)}, {Name: "seconds", Value: json.RawMessage(`8`)}, {Name: "audio", Value: json.RawMessage(`true`)}}
	body, contentType, err := EncodeMultipart(parts)
	require.NoError(t, err)
	_, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for index, expected := range []string{"ocean\n sunrise", "8", "true"} {
		part, err := reader.NextPart()
		require.NoError(t, err)
		require.Equal(t, parts[index].Name, part.FormName())
		text, err := io.ReadAll(part)
		require.NoError(t, err)
		require.Equal(t, expected, string(text))
	}
	_, err = reader.NextPart()
	require.ErrorIs(t, err, io.EOF)
}

func TestMultipartCodecRejectsUnresolvedFilesAndOversize(t *testing.T) {
	for _, parts := range [][]Part{
		{{Name: "image", FileRef: "/etc/passwd"}},
		{{Name: "bad\r\nname", Value: json.RawMessage(`"value"`)}},
		{{Name: "object", Value: json.RawMessage(`{"field":"value"}`)}},
		{{Name: "prompt", Value: json.RawMessage(`"` + strings.Repeat("x", MaxSnapshotBytes) + `"`)}},
	} {
		_, _, err := EncodeMultipart(parts)
		require.Error(t, err)
	}
}
