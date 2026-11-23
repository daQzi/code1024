package mediahost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
	"github.com/stretchr/testify/require"
)

func TestMultipartFilesAreLazyReadOnlyAndShareExtractionBudget(t *testing.T) {
	var opens, closed int
	text := strings.Repeat("private ", 100000)
	file := MultipartFile{Field: "document", Filename: "readme.txt", MIME: "text/plain", Size: len(text), SHA256: sha256.Sum256([]byte(text)), Open: func(context.Context) (io.ReadCloser, error) {
		opens++
		return mediaFileReader{strings.NewReader(text), &closed}, nil
	}}
	cache := &TextCache{}
	for _, prompt := range []string{"first", "second"} {
		s, err := NewWithMultipartFiles([]byte(`{"prompt":"`+prompt+`"}`), cache, []MultipartFile{file})
		require.NoError(t, err)
		require.JSONEq(t, `{"prompt":"`+prompt+`"}`, string(s.View()))
		require.Len(t, s.Attachments(), 1)
		a := s.Attachments()[0]
		require.Equal(t, "document", a.Path)
		require.Equal(t, "readme.txt", a.Filename)
		require.False(t, a.Rewritable)
		require.Equal(t, closed, opens)
		result, ok := s.MediaText(context.Background(), a.Ref, 13)
		require.True(t, ok)
		require.Equal(t, text[:13], result.Text)
		require.True(t, result.Truncated)
		require.Empty(t, result.Unchecked)
		prepared, err := s.PrepareEffects(context.Background(), []requestpatch.Effect{{Type: requestpatch.EffectMediaText, Media: 0, Text: "new"}})
		require.NoError(t, err)
		require.Empty(t, prepared.Effects)
		_, _, err = s.FetchBody(`{"file":"` + a.Ref + `"}`)
		require.Error(t, err)
		records, _ := s.Records()
		require.Equal(t, "not_rewritable", records[0].Reason)
	}
	require.Equal(t, 1, opens)
	require.Equal(t, opens, closed)
}

func TestMultipartImageMetadataUsesAClosedHeaderReader(t *testing.T) {
	var raw bytes.Buffer
	require.NoError(t, png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 7, 19))))
	closed := 0
	s, err := NewWithMultipartFiles([]byte(`{"prompt":"draw"}`), nil, []MultipartFile{{
		Field: "input_reference", MIME: "image/png", Size: raw.Len(), SHA256: sha256.Sum256(raw.Bytes()), Open: func(context.Context) (io.ReadCloser, error) {
			return mediaFileReader{bytes.NewReader(raw.Bytes()), &closed}, nil
		},
	}})
	require.NoError(t, err)
	require.Zero(t, closed)
	info, ok := s.Media(s.Attachments()[0].Ref)
	require.True(t, ok)
	require.Equal(t, 7, info.Width)
	require.Equal(t, 19, info.Height)
	require.Equal(t, 1, closed)
}
