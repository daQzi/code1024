package mediahost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSourceTextRewriteIsAtomicAndProtectsPartialReads(t *testing.T) {
	body := []byte(`{"model":"m","file":{"filename":"private-name.txt","file_data":"data:text/plain;base64,SGVsbG8="}}`)
	for _, partial := range []bool{false, true} {
		source, err := New(body)
		require.NoError(t, err)
		limit := 100
		if partial {
			limit = 2
		}
		_, ok := source.MediaText(context.Background(), "[media:0]", limit)
		require.True(t, ok)
		prepared, err := source.PrepareEffects(context.Background(), []requestpatch.Effect{{Type: requestpatch.EffectMediaText, Media: 0, Text: "redacted"}})
		require.NoError(t, err)
		beforeCommit, _ := source.Records()
		require.False(t, beforeCommit[0].Rewritten)
		out, _, err := requestpatch.ApplyWithLimit(body, nil, prepared.Effects, nil, MaxBody)
		require.NoError(t, err)
		prepared.Commit()
		records, _ := source.Records()
		if partial {
			require.JSONEq(t, string(body), string(out))
			require.Equal(t, "not_rewritable", records[0].Reason)
			require.False(t, records[0].Rewritten)
		} else {
			require.Equal(t, "data:text/plain;base64,"+base64.StdEncoding.EncodeToString([]byte("redacted")), gjson.GetBytes(out, "file.file_data").String())
			require.True(t, records[0].Rewritten)
		}
		audit, err := json.Marshal(records)
		require.NoError(t, err)
		for _, private := range []string{"private-name", "Hello", "SGVsbG8", "redacted", "file_data"} {
			require.NotContains(t, string(audit), private)
		}
	}
}

func imageFixture(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 256, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 256; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y * 2), B: 50, A: 255})
		}
	}
	var out bytes.Buffer
	require.NoError(t, (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&out, img))
	return out.Bytes()
}

func TestSourceImageEffectsUsePinnedCodecsAndUpdateSiblingMime(t *testing.T) {
	for _, format := range []string{"jpeg", "png", "webp", "auto"} {
		for _, sibling := range []bool{false, true} {
			t.Run(format+"/sibling="+map[bool]string{false: "no", true: "yes"}[sibling], func(t *testing.T) {
				encoded := base64.StdEncoding.EncodeToString(imageFixture(t))
				body := []byte(`{"media":"data:image/png;base64,` + encoded + `"}`)
				path, mimePath := "media", ""
				if sibling {
					body = []byte(`{"content":{"type":"base64","media_type":"image/png","data":"` + encoded + `"}}`)
					path, mimePath = "content.data", "content.media_type"
				}
				source, err := New(body)
				require.NoError(t, err)
				prepared, err := source.PrepareEffects(context.Background(), []requestpatch.Effect{{Type: requestpatch.EffectMediaReencode, Media: 0, Format: format, MaxSide: 64}})
				require.NoError(t, err)
				require.Len(t, prepared.Media, 1)
				out, _, err := requestpatch.ApplyWithLimit(body, nil, prepared.Effects, nil, MaxBody)
				require.NoError(t, err)
				value := gjson.GetBytes(out, path).String()
				if !sibling {
					_, value, _ = strings.Cut(value, ",")
				} else {
					require.Equal(t, prepared.Media[0].To.Mime, gjson.GetBytes(out, mimePath).String())
				}
				raw, err := base64.StdEncoding.DecodeString(value)
				require.NoError(t, err)
				config, actualFormat, err := image.DecodeConfig(bytes.NewReader(raw))
				require.NoError(t, err)
				require.Equal(t, 64, config.Width)
				require.Equal(t, 32, config.Height)
				wantFormat := format
				if wantFormat == "auto" {
					wantFormat = "jpeg"
				}
				require.Equal(t, wantFormat, actualFormat)
				require.Less(t, prepared.Media[0].To.Bytes, prepared.Media[0].From.Bytes)
			})
		}
	}
}

func TestSourceImageAdmissionCancellationAndAnimatedGIF(t *testing.T) {
	body := []byte(`{"image":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(imageFixture(t)) + `"}`)
	budget := &memoryBudget{limit: 1 << 20}
	source, err := New(body, WithMemoryAdmission(testMemoryAdmission(budget)))
	require.NoError(t, err)
	effects := []requestpatch.Effect{{Type: requestpatch.EffectMediaReencode, Media: 0, Format: "jpeg"}}
	_, err = source.PrepareEffects(context.Background(), effects)
	require.ErrorIs(t, err, ErrMemoryCapacity)
	release, err := budget.reserve(1 << 20)
	require.NoError(t, err, "failed image admission released its scope")
	release()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = source.PrepareEffects(canceled, effects)
	require.ErrorIs(t, err, context.Canceled)
	var animated bytes.Buffer
	frame := image.NewPaletted(image.Rect(0, 0, 8, 8), color.Palette{color.Black, color.White})
	require.NoError(t, gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{1, 1}}))
	source, err = New([]byte(`{"image":"data:image/gif;base64,` + base64.StdEncoding.EncodeToString(animated.Bytes()) + `"}`))
	require.NoError(t, err)
	prepared, err := source.PrepareEffects(context.Background(), effects)
	require.NoError(t, err)
	require.Empty(t, prepared.Media)
	require.Empty(t, prepared.Effects, "animated input stays unchanged")
}

func TestSourceRepeatedTextReadUsesSingleExtractionBudget(t *testing.T) {
	source, err := New([]byte(`{"file":"data:text/plain;base64,` + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 1000))) + `"}`))
	require.NoError(t, err)
	for range 100 {
		text, ok := source.MediaText(context.Background(), "[media:0]", 1000)
		require.True(t, ok)
		require.Empty(t, text.Unchecked)
	}
	require.Equal(t, 1000, source.textCache.chars)
	_, err = source.PrepareEffects(context.Background(), []requestpatch.Effect{{Type: requestpatch.EffectMediaText, Media: 99, Text: "bad"}})
	require.Error(t, err)
}
