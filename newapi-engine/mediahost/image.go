// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// Adapted from QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f, relay/requestplugin/media.go.
// See ../NOTICE.md. The host additionally bounds output, memory and admission.
package mediahost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	"github.com/MoeForge/moego/webp"
	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
	"golang.org/x/image/draw"
	"golang.org/x/image/riff"
	"golang.org/x/image/vp8"
	"golang.org/x/image/vp8l"
)

const maxReencodePixels = 40_000_000

var imageSlots = make(chan struct{}, 2)
var imageMemory = &memoryBudget{limit: 512 << 20}

type MediaForm struct {
	Mime  string `json:"mime"`
	Bytes int    `json:"bytes"`
}
type Change struct {
	Index    int       `json:"index"`
	From     MediaForm `json:"from"`
	To       MediaForm `json:"to"`
	Lossless bool      `json:"lossless"`
}

type boundedImageBuffer struct {
	buffer bytes.Buffer
	ctx    context.Context
}

func (b *boundedImageBuffer) Len() int      { return b.buffer.Len() }
func (b *boundedImageBuffer) Bytes() []byte { return b.buffer.Bytes() }
func (b *boundedImageBuffer) Write(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > MaxBody-b.Len() {
		return 0, errors.New("reencoded image exceeds host body limit")
	}
	return b.buffer.Write(p)
}

func encodeImage(ctx context.Context, value item, effect requestpatch.Effect, admit MemoryAdmission) ([]requestpatch.Operation, *Change, error) {
	if value.readonly {
		return nil, nil, errors.New("persistent task media is read-only")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	config, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding.Strict(), bytes.NewBufferString(value.data)))
	if err != nil {
		return nil, nil, errors.New("media is not a supported image")
	}
	pixels := int64(config.Width) * int64(config.Height)
	if config.Width <= 0 || config.Height <= 0 || pixels > maxReencodePixels {
		return nil, nil, nil
	}
	select {
	case imageSlots <- struct{}{}:
		defer func() { <-imageSlots }()
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	// Decode, scale, alpha flattening and codec scratch may coexist. Admission
	// happens before decoding pixels, even when the compressed body is tiny.
	memoryBytes := pixels*20 + (16 << 20)
	ctx, release, err := reserveWorkingMemory(ctx, memoryBytes, imageMemory, admit)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	ops, change, err := reencodeMedia(ctx, value, effect)
	if err == nil {
		err = ctx.Err()
	}
	return ops, change, err
}

// reencodeMedia decodes an inline image and encodes it again as a
// media.reencode effect asks. It returns the json.patch operations that
// replace the media string and its mime type, and the change for the public
// record; none when the new bytes are not smaller or the image is an
// animated GIF or over the limits.
func reencodeMedia(ctx context.Context, item item, effect requestpatch.Effect) ([]requestpatch.Operation, *Change, error) {
	raw, err := base64.StdEncoding.DecodeString(item.data)
	if err != nil {
		return nil, nil, fmt.Errorf("media %s is not valid base64", item.path)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("media %s is not a jpeg, png, gif or webp image", item.path)
	}
	if int64(config.Width)*int64(config.Height) > maxReencodePixels || format == "gif" && gifAnimated(raw) {
		return nil, nil, nil
	}
	// With a VP8X chunk the size read above is the canvas the chunk declares,
	// while the decoder decodes the frame at its own size: the two must agree
	// for the pixel bound to hold.
	if format == "webp" {
		if width, height, err := webpFrameSize(raw); err != nil || width != config.Width || height != config.Height {
			return nil, nil, fmt.Errorf("media %s cannot be decoded", item.path)
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("media %s cannot be decoded", item.path)
	}
	bounds := img.Bounds()
	if longest := max(bounds.Dx(), bounds.Dy()); effect.MaxSide > 0 && longest > effect.MaxSide {
		width := max(1, bounds.Dx()*effect.MaxSide/longest)
		height := max(1, bounds.Dy()*effect.MaxSide/longest)
		scaled := image.NewNRGBA(image.Rect(0, 0, width, height))
		draw.CatmullRom.Scale(scaled, scaled.Bounds(), img, bounds, draw.Src, nil)
		img = scaled
	}
	opaque, known := img.(interface{ Opaque() bool })
	transparent := !known || !opaque.Opaque()
	target := effect.Format
	if target == "auto" {
		target = "jpeg"
		if transparent {
			target = "png"
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, nil, err
	}
	encoded := boundedImageBuffer{ctx: ctx}
	switch target {
	case "jpeg":
		if transparent {
			canvas := image.NewRGBA(img.Bounds())
			draw.Draw(canvas, canvas.Bounds(), image.White, image.Point{}, draw.Src)
			draw.Draw(canvas, canvas.Bounds(), img, img.Bounds().Min, draw.Over)
			img = canvas
		}
		quality := effect.Quality
		if quality == 0 {
			quality = jpeg.DefaultQuality
		}
		err = jpeg.Encode(&encoded, img, &jpeg.Options{Quality: quality})
	case "png":
		err = png.Encode(&encoded, img)
	case "webp":
		err = webp.Encode(&encoded, img)
	default:
		return nil, nil, fmt.Errorf("format %q is not supported", effect.Format)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("media %s cannot be encoded as %s", item.path, target)
	}
	if encoded.Len() >= len(raw) {
		return nil, nil, nil
	}

	mime := "image/" + target
	payload := base64.StdEncoding.EncodeToString(encoded.Bytes())
	if item.mimePath == "" {
		payload = "data:" + mime + ";base64," + payload
	}
	value, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	ops := []requestpatch.Operation{{Op: "replace", Path: item.path, Value: value}}
	if item.mimePath != "" {
		mimeValue, err := json.Marshal(mime)
		if err != nil {
			return nil, nil, err
		}
		ops = append(ops, requestpatch.Operation{Op: "replace", Path: item.mimePath, Value: mimeValue})
	}
	return ops, &Change{
		From:     MediaForm{Mime: item.mime, Bytes: len(raw)},
		To:       MediaForm{Mime: mime, Bytes: encoded.Len()},
		Lossless: target != "jpeg",
	}, nil
}

// webpFrameSize reads the size of a WebP image's frame from the header of
// its first VP8 or VP8L chunk.
func webpFrameSize(data []byte) (int, int, error) {
	_, chunks, err := riff.NewReader(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	for {
		id, length, chunk, err := chunks.Next()
		if err != nil {
			return 0, 0, err
		}
		switch id {
		case riff.FourCC{'V', 'P', '8', ' '}:
			decoder := vp8.NewDecoder()
			decoder.Init(chunk, int(length))
			header, err := decoder.DecodeFrameHeader()
			return header.Width, header.Height, err
		case riff.FourCC{'V', 'P', '8', 'L'}:
			config, err := vp8l.DecodeConfig(chunk)
			return config.Width, config.Height, err
		}
	}
}

// gifAnimated reports a GIF with more than one image. It walks the block
// structure without decoding any pixels.
func gifAnimated(data []byte) bool {
	if len(data) < 13 {
		return false
	}
	position := 13
	if data[10]&0x80 != 0 {
		position += 3 << (data[10]&7 + 1)
	}
	frames := 0
	for position < len(data) {
		switch data[position] {
		case 0x21: // extension: introducer and label, then data sub-blocks
			position += 2
		case 0x2C: // image descriptor
			if frames++; frames > 1 {
				return true
			}
			if position+10 > len(data) {
				return false
			}
			flags := data[position+9]
			position += 10
			if flags&0x80 != 0 {
				position += 3 << (flags&7 + 1)
			}
			position++ // LZW minimum code size
		default: // trailer
			return false
		}
		for position < len(data) && data[position] != 0 {
			position += int(data[position]) + 1
		}
		position++
	}
	return false
}
