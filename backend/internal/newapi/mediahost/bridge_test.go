package mediahost

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"io"
	"testing"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/stretchr/testify/require"
)

var _ jsplugin.MediaSource = (*Source)(nil)

func TestMediaBridgeSharesIngressBudgetForEveryConstructor(t *testing.T) {
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 8, 8))))
	body := []byte(`{"image":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(encoded.Bytes()) + `"}`)
	for _, cached := range []bool{false, true} {
		for _, denied := range []bool{false, true} {
			var source *Source
			var err error
			if cached {
				source, err = NewWithCache(body, &TextCache{})
			} else {
				source, err = New(body)
			}
			require.NoError(t, err)
			limit := int64(32 << 20)
			if denied {
				limit = 1 << 20
			}
			ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), pkghttputil.NewBodyMemoryBudget(limit))
			require.NoError(t, pkghttputil.ReserveRequestBodyMemory(ctx, 17), "ingress already owns a representation")
			_, err = source.PrepareEffects(ctx, []requestpatch.Effect{{Type: requestpatch.EffectMediaReencode, Media: 0, Format: "jpeg"}})
			if denied {
				require.ErrorIs(t, err, pkghttputil.ErrRequestBodyMemoryBudget)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, pkghttputil.ReserveRequestBodyMemory(ctx, limit-17), "success and refusal release only their temporary working memory")
			require.ErrorIs(t, pkghttputil.ReserveRequestBodyMemory(ctx, 1), pkghttputil.ErrRequestBodyMemoryBudget, "original ingress reservation remains owned")
			release()
		}
	}
}

func TestMediaBridgeTaskFileDocumentUsesHostAdmission(t *testing.T) {
	var document bytes.Buffer
	archive := zip.NewWriter(&document)
	entry, err := archive.Create("word/document.xml")
	require.NoError(t, err)
	_, err = io.WriteString(entry, `<w:document xmlns:w="urn:word"><w:body><w:p><w:r><w:t>private document</w:t></w:r></w:p></w:body></w:document>`)
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	for _, denied := range []bool{false, true} {
		limit := int64(256 << 20)
		if denied {
			limit = 1 << 20
		}
		ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), pkghttputil.NewBodyMemoryBudget(limit))
		source, err := NewWithFileReferences(ctx, []byte(`{"file":{"__fileRef":"private-id","encoding":"base64"}}`), nil, func(context.Context, string) (pluginTask.File, io.ReadCloser, error) {
			return pluginTask.File{Size: int64(document.Len()), MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}, io.NopCloser(bytes.NewReader(document.Bytes())), nil
		})
		require.NoError(t, err)
		text, ok := source.MediaText(ctx, "[media:0]", 100)
		require.True(t, ok)
		if denied {
			require.Equal(t, "busy", text.Unchecked)
			require.Empty(t, text.Text)
		} else {
			require.Empty(t, text.Unchecked)
			require.Contains(t, text.Text, "private document")
		}
		require.NotContains(t, string(source.View()), "private-id")
		require.NotContains(t, string(source.View()), "private document")
		require.NoError(t, pkghttputil.ReserveRequestBodyMemory(ctx, limit))
		release()
	}
}
