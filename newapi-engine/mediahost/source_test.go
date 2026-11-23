package mediahost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSourceProjectsInlineAndReferencesInBodyOrder(t *testing.T) {
	body := `{"model":"public","messages":[{"content":[{"type":"file","file":{"file_id":"file-remote","filename":"remote.txt"}},{"type":"image_url","image_url":{"url":"https://example.com/image"}},{"type":"file","file":{"filename":"local.txt","file_data":"data:text/plain;base64,SGVsbG8="}},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1hZ2U="}},{"inlineData":{"mimeType":"audio/wav","data":"YXVkaW8="}},{"input_audio":{"format":"wav","data":"d2F2"}}]}]}`
	source, err := New([]byte(body))
	require.NoError(t, err)
	require.NotContains(t, string(source.View()), "SGVsbG8=")
	items := source.Attachments()
	require.Len(t, items, 6)
	require.Empty(t, items[0].Ref)
	require.Empty(t, items[1].Ref)
	require.Equal(t, "https://example.com/image", items[1].URL)
	require.Equal(t, "[media:0]", items[2].Ref)
	require.Equal(t, "local.txt", items[2].Filename)
	require.Equal(t, "[media:1]", items[3].Ref)
	require.Equal(t, "audio/wav", items[5].Mime)
	info, ok := source.Media("[media:0]")
	require.True(t, ok)
	require.Equal(t, 5, info.Size)
	require.True(t, info.Rewritable)
	_, ok = source.Media("[media:00]")
	require.False(t, ok)
	items[2].Ref = "changed"
	require.Equal(t, "[media:0]", source.Attachments()[2].Ref)
}

func TestSourceTextUTF16AndUnsupportedAreExplicit(t *testing.T) {
	body := `{"files":["data:text/plain;base64,` + base64.StdEncoding.EncodeToString([]byte("A😀B")) + `","data:application/pdf;base64,cGRm"]}`
	source, err := New([]byte(body))
	require.NoError(t, err)
	text, ok := source.MediaText(context.Background(), "[media:0]", 3)
	require.True(t, ok)
	require.Equal(t, "A😀", text.Text)
	require.True(t, text.Truncated)
	text, ok = source.MediaText(context.Background(), "[media:1]", 10)
	require.True(t, ok)
	require.Equal(t, "unreadable", text.Unchecked)
	_, ok = source.MediaText(context.Background(), "file-other", 10)
	require.False(t, ok)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	text, ok = source.MediaText(ctx, "[media:0]", 10)
	require.True(t, ok)
	require.Equal(t, "limit", text.Unchecked)
}

func TestSourceFetchBodyStreamsExactValuesAndPreservesJSON(t *testing.T) {
	source, err := New([]byte(`{"file":"data:text/plain;base64,SGVs\r\nbG8="}`))
	require.NoError(t, err)
	reader, size, err := source.FetchBody(`{"[media:0]":"key unchanged","a":"\u005bmedia:0]","partial":"prefix[media:0]","other":"[media:9]"}`)
	require.NoError(t, err)
	require.NotNil(t, reader)
	raw, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.EqualValues(t, len(raw), size)
	require.True(t, json.Valid(raw), string(raw))
	require.Equal(t, "data:text/plain;base64,SGVsbG8=", gjson.GetBytes(raw, "a").String())
	require.Equal(t, "prefix[media:0]", gjson.GetBytes(raw, "partial").String())
	_, _, err = source.FetchBody(`[` + strings.Repeat(`"[media:0]",`, 8) + `"[media:0]"]`)
	require.Error(t, err)
	reader, _, err = source.FetchBody(`{"literal":"prefix[media:0]"}`)
	require.NoError(t, err)
	require.Nil(t, reader)
}

func TestSourceBoundsAndInvalidBase64(t *testing.T) {
	for _, body := range []string{`{"a":1,"a":2}`, `{"padding":"` + strings.Repeat("x", MaxBody) + `"}`, `{"items":[` + strings.Repeat(`"data:text/plain;base64,YQ==",`, 64) + `"data:text/plain;base64,YQ=="]}`, `{"values":[` + strings.Repeat(`0,`, 8192) + `0]}`} {
		_, err := New([]byte(body))
		require.Error(t, err)
	}
	source, err := New([]byte(`{"file":"data:text/plain;base64,<invalid>"}`))
	require.NoError(t, err)
	_, _, err = source.FetchBody(`{"a":"[media:0]"}`)
	require.Error(t, err)
	text, ok := source.MediaText(context.Background(), "[media:0]", 10)
	require.True(t, ok)
	require.Equal(t, "unreadable", text.Unchecked)
}
