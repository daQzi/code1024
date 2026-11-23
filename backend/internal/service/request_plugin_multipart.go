// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// Multipart form projection and byte-preserving text rebuild follow
// QuantumNous/new-api relay/requestplugin/form.go at
// 48f9df96ea7c68eae04a0f3687869f232f48929f. Bounded limits, image controls and
// OAuth data-URL projection are local host code; see internal/newapi/NOTICE.md.
package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/newapi/mediahost"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	requestPluginMultipartMaxParts = 192
	requestPluginMultipartMaxFiles = 64
	requestPluginMultipartMaxField = 512 << 10
)

var (
	ErrRequestPluginMultipartInvalid  = errors.New("invalid multipart form body")
	ErrRequestPluginMultipartTooLarge = errors.New("multipart form text fields exceed limit")
)

type requestPluginMultipartKey struct{}

type requestPluginMultipartField struct {
	part  int
	name  string
	value string
}

type requestPluginMultipartFile struct {
	part     int
	field    string
	filename string
	mime     string
	size     int
	digest   [sha256.Size]byte
}

// RequestPluginMultipart projects a multipart/form-data request into a bounded
// JSON view of text fields plus read-only attachment handles. Unchanged parts
// (including all binary file parts) stay byte-for-byte identical on Rebuild.
type RequestPluginMultipart struct {
	boundary string
	raw      []byte
	fields   []requestPluginMultipartField
	files    []requestPluginMultipartFile
	parts    int
	View     []byte
}

func WithRequestPluginMultipart(ctx context.Context, form *RequestPluginMultipart) context.Context {
	if form == nil {
		return ctx
	}
	return context.WithValue(ctx, requestPluginMultipartKey{}, form)
}

func requestPluginMultipartFromContext(ctx context.Context) *RequestPluginMultipart {
	form, _ := ctx.Value(requestPluginMultipartKey{}).(*RequestPluginMultipart)
	return form
}

func ParseRequestPluginMultipart(contentType string, raw []byte) (*RequestPluginMultipart, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return nil, ErrRequestPluginMultipartInvalid
	}
	boundary := params["boundary"]
	if boundary == "" || !bytes.Contains(raw, []byte("--"+boundary+"--")) {
		return nil, ErrRequestPluginMultipartInvalid
	}
	form := &RequestPluginMultipart{boundary: boundary, raw: raw}
	reader := multipart.NewReader(bytes.NewReader(raw), boundary)
	textBytes := 0
	for ; ; form.parts++ {
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, ErrRequestPluginMultipartInvalid
		}
		if form.parts >= requestPluginMultipartMaxParts {
			_ = part.Close()
			return nil, ErrRequestPluginMultipartTooLarge
		}
		switch strings.ToLower(strings.TrimSpace(part.Header.Get("Content-Transfer-Encoding"))) {
		case "", "7bit", "8bit", "binary":
		default:
			_ = part.Close()
			continue
		}
		name := part.FormName()
		filename := part.FileName()
		if name != "" && filename != "" {
			if len(form.files) >= requestPluginMultipartMaxFiles {
				_ = part.Close()
				return nil, ErrRequestPluginMultipartTooLarge
			}
			hash := sha256.New()
			written, copyErr := io.Copy(hash, io.LimitReader(part, openAIImageMaxUploadPartSize+1))
			_ = part.Close()
			if copyErr != nil {
				return nil, ErrRequestPluginMultipartInvalid
			}
			if written > openAIImageMaxUploadPartSize {
				return nil, ErrRequestPluginMultipartTooLarge
			}
			partMime, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			var digest [sha256.Size]byte
			copy(digest[:], hash.Sum(nil))
			form.files = append(form.files, requestPluginMultipartFile{
				part:     form.parts,
				field:    name,
				filename: filename,
				mime:     strings.ToLower(strings.TrimSpace(partMime)),
				size:     int(written),
				digest:   digest,
			})
			continue
		}
		if name == "" {
			_ = part.Close()
			continue
		}
		remaining := MaxRequestPluginBody - textBytes
		if remaining < 0 {
			_ = part.Close()
			return nil, ErrRequestPluginMultipartTooLarge
		}
		value, readErr := io.ReadAll(io.LimitReader(part, int64(remaining)+1))
		_ = part.Close()
		if readErr != nil {
			return nil, ErrRequestPluginMultipartInvalid
		}
		if len(value) > remaining || len(value) > requestPluginMultipartMaxField {
			return nil, ErrRequestPluginMultipartTooLarge
		}
		if !utf8.Valid(value) {
			return nil, ErrRequestPluginMultipartInvalid
		}
		textBytes += len(value)
		form.fields = append(form.fields, requestPluginMultipartField{
			part:  form.parts,
			name:  name,
			value: string(value),
		})
	}
	if _, _, _, err := form.segments(); err != nil {
		return nil, ErrRequestPluginMultipartInvalid
	}
	var names []string
	values := make(map[string][]string, len(form.fields))
	for _, field := range form.fields {
		if _, seen := values[field.name]; !seen {
			names = append(names, field.name)
		}
		values[field.name] = append(values[field.name], field.value)
	}
	var view bytes.Buffer
	view.WriteByte('{')
	for idx, name := range names {
		if idx > 0 {
			view.WriteByte(',')
		}
		keyRaw, err := json.Marshal(name)
		if err != nil {
			return nil, ErrRequestPluginMultipartInvalid
		}
		var valRaw []byte
		if len(values[name]) == 1 {
			valRaw, err = json.Marshal(values[name][0])
		} else {
			valRaw, err = json.Marshal(values[name])
		}
		if err != nil {
			return nil, ErrRequestPluginMultipartInvalid
		}
		view.Write(keyRaw)
		view.WriteByte(':')
		view.Write(valRaw)
	}
	view.WriteByte('}')
	if view.Len() > MaxRequestPluginBody {
		return nil, ErrRequestPluginMultipartTooLarge
	}
	form.View = view.Bytes()
	return form, nil
}

func (f *RequestPluginMultipart) MediaFiles() ([]mediahost.MultipartFile, error) {
	if f == nil || len(f.files) == 0 {
		return nil, nil
	}
	_, newline, segments, err := f.segments()
	if err != nil {
		return nil, err
	}
	out := make([]mediahost.MultipartFile, 0, len(f.files))
	for _, file := range f.files {
		if file.size <= 0 {
			continue
		}
		segment := segments[file.part+1]
		start, err := multipartContentStart(segment, newline)
		if err != nil {
			return nil, err
		}
		content := segment[start:]
		if len(content) != file.size {
			return nil, ErrRequestPluginMultipartInvalid
		}
		out = append(out, mediahost.MultipartFile{
			Field:    file.field,
			Filename: file.filename,
			MIME:     file.mime,
			Size:     file.size,
			SHA256:   file.digest,
			Open: func(ctx context.Context) (io.ReadCloser, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return io.NopCloser(bytes.NewReader(content)), nil
			},
		})
	}
	return out, nil
}

func (f *RequestPluginMultipart) MediaSource(body []byte, cache *mediahost.TextCache) (*mediahost.Source, error) {
	files, err := f.MediaFiles()
	if err != nil {
		return nil, err
	}
	return mediahost.NewWithMultipartFiles(body, cache, files)
}

func (f *RequestPluginMultipart) ReportMedia() []jsplugin.TransformMedia {
	if f == nil || len(f.files) == 0 {
		return nil
	}
	media := make([]jsplugin.TransformMedia, 0, len(f.files))
	for idx, file := range f.files {
		media = append(media, jsplugin.TransformMedia{
			Index:    idx,
			Path:     file.field,
			MimeType: file.mime,
			Bytes:    file.size,
		})
	}
	return media
}

func (f *RequestPluginMultipart) CheckEffects(effects []requestpatch.Effect, allowAddUser bool) error {
	for idx, effect := range effects {
		switch effect.Type {
		case requestpatch.EffectMediaText, requestpatch.EffectHeaderPatch:
			continue
		case requestpatch.EffectJSONPatch:
			for _, op := range effect.Ops {
				if op.Op == "replace" && gjson.ParseBytes(op.Value).Type == gjson.String {
					continue
				}
				if allowAddUser && op.Op == "add" && op.Path == "/user" && gjson.ParseBytes(op.Value).Type == gjson.String {
					continue
				}
				return fmt.Errorf("effects[%d]: multipart form accepts text field string patches only", idx)
			}
		default:
			return fmt.Errorf("effects[%d]: unsupported effect for multipart form", idx)
		}
	}
	return nil
}

func (f *RequestPluginMultipart) CheckView(before, after []byte, allowAddUser bool) error {
	if f == nil {
		return ErrRequestPluginMultipartInvalid
	}
	if len(after) > MaxRequestPluginBody {
		return ErrRequestPluginMultipartTooLarge
	}
	previous, next := gjson.ParseBytes(before), gjson.ParseBytes(after)
	if !previous.IsObject() || !next.IsObject() {
		return ErrRequestPluginMultipartInvalid
	}
	prevMap := previous.Map()
	nextMap := next.Map()
	same := true
	previous.ForEach(func(name, value gjson.Result) bool {
		patched := next.Get(gjson.Escape(name.Str))
		switch {
		case value.Type == gjson.String:
			same = same && patched.Type == gjson.String && len(patched.Str) <= requestPluginMultipartMaxField
		case !patched.IsArray() || len(patched.Array()) != len(value.Array()):
			same = false
		default:
			same = same && !slices.ContainsFunc(patched.Array(), func(item gjson.Result) bool {
				return item.Type != gjson.String || len(item.Str) > requestPluginMultipartMaxField
			})
		}
		return same
	})
	if !same {
		return errors.New("a form body takes new string values for its text fields only")
	}
	for key, val := range nextMap {
		if _, existed := prevMap[key]; existed {
			continue
		}
		if !allowAddUser || key != "user" || val.Type != gjson.String || len(val.Str) > requestPluginMultipartMaxField {
			return errors.New("multipart form does not accept added fields")
		}
	}
	if !allowAddUser && len(nextMap) != len(prevMap) {
		return errors.New("multipart form field layout changed")
	}
	boundaryFound := false
	next.ForEach(func(_, val gjson.Result) bool {
		if val.Type == gjson.String && strings.Contains(val.Str, f.boundary) {
			boundaryFound = true
			return false
		}
		if val.IsArray() {
			for _, item := range val.Array() {
				if item.Type == gjson.String && strings.Contains(item.Str, f.boundary) {
					boundaryFound = true
					return false
				}
			}
		}
		return true
	})
	if boundaryFound {
		return errors.New("a form field value must not contain the form boundary")
	}
	return nil
}

func (f *RequestPluginMultipart) Rebuild(view []byte) ([]byte, error) {
	if f == nil {
		return nil, ErrRequestPluginMultipartInvalid
	}
	patched := gjson.ParseBytes(view)
	values := map[int]string{}
	seen := map[string]int{}
	hasExistingUser := false
	for _, field := range f.fields {
		if field.name == "user" {
			hasExistingUser = true
		}
		value := patched.Get(gjson.Escape(field.name))
		if value.IsArray() {
			arr := value.Array()
			idx := seen[field.name]
			if idx >= len(arr) {
				return nil, ErrRequestPluginMultipartInvalid
			}
			value = arr[idx]
		}
		seen[field.name]++
		if value.Str != field.value {
			values[field.part] = value.Str
		}
	}
	var addedUser *string
	if !hasExistingUser {
		if userVal := patched.Get("user"); userVal.Exists() && userVal.Type == gjson.String {
			v := userVal.Str
			addedUser = &v
		}
	}
	if len(values) == 0 && addedUser == nil {
		return f.raw, nil
	}
	first, newline, segments, err := f.segments()
	if err != nil {
		return nil, err
	}
	delimiter := append(slices.Clone(newline), "--"+f.boundary...)
	for index, value := range values {
		segment := segments[index+1]
		start, err := multipartContentStart(segment, newline)
		if err != nil {
			return nil, err
		}
		segments[index+1] = append(segment[:start:start], value...)
	}
	if addedUser != nil {
		if strings.Contains(*addedUser, f.boundary) {
			return nil, errors.New("a form field value must not contain the form boundary")
		}
		userSegment := make([]byte, 0, len(newline)*3+48+len(*addedUser))
		userSegment = append(userSegment, newline...)
		userSegment = append(userSegment, `Content-Disposition: form-data; name="user"`...)
		userSegment = append(userSegment, newline...)
		userSegment = append(userSegment, newline...)
		userSegment = append(userSegment, *addedUser...)
		closing := segments[len(segments)-1]
		segments = append(segments[:len(segments)-1], userSegment, closing)
	}
	rebuilt := bytes.Join(segments, delimiter)
	return append(slices.Clone(f.raw[:first]), rebuilt[len(newline):]...), nil
}

func (f *RequestPluginMultipart) segments() (int, []byte, [][]byte, error) {
	dashBoundary := []byte("--" + f.boundary)
	first := -1
	for offset := 0; first < 0; {
		index := bytes.Index(f.raw[offset:], dashBoundary)
		if index < 0 {
			return 0, nil, nil, ErrRequestPluginMultipartInvalid
		}
		if index += offset; index == 0 || f.raw[index-1] == '\n' {
			first = index
		}
		offset = index + 1
	}
	lineEnd := bytes.IndexByte(f.raw[first:], '\n')
	if lineEnd < 0 {
		return 0, nil, nil, ErrRequestPluginMultipartInvalid
	}
	newline := []byte("\r\n")
	if lineEnd == 0 || f.raw[first+lineEnd-1] != '\r' {
		newline = []byte("\n")
	}
	delimiter := append(slices.Clone(newline), dashBoundary...)
	segments := append([][]byte{nil}, bytes.Split(f.raw[first:], delimiter)...)
	segments[1] = segments[1][len(dashBoundary):]
	if len(segments) != f.parts+2 || !bytes.HasPrefix(segments[len(segments)-1], []byte("--")) {
		return 0, nil, nil, ErrRequestPluginMultipartInvalid
	}
	return first, newline, segments, nil
}

func multipartContentStart(segment, newline []byte) (int, error) {
	start := bytes.Index(segment, newline)
	if start < 0 {
		return 0, ErrRequestPluginMultipartInvalid
	}
	if bytes.HasPrefix(segment[start+len(newline):], newline) {
		return start + 2*len(newline), nil
	}
	blankLine := append(slices.Clone(newline), newline...)
	if headersEnd := bytes.Index(segment[start:], blankLine); headersEnd >= 0 {
		return start + headersEnd + len(blankLine), nil
	}
	return 0, ErrRequestPluginMultipartInvalid
}

// projectOversizedImagesOAuthBody detaches inline base64 data URLs from an
// OAuth Images JSON body (direct or Responses bridge) when the encoded binary
// payload exceeds MaxRequestPluginBody, replacing them with stable sentinel
// tokens and exposing the decoded media via bounded MultipartFile openers.
func projectOversizedImagesOAuthBody(body []byte) ([]byte, []mediahost.MultipartFile, error) {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil, nil, ErrRequestPluginMultipartInvalid
	}
	projected := body
	var files []mediahost.MultipartFile
	replacePath := func(path string) error {
		val := gjson.GetBytes(projected, path)
		if !val.Exists() || val.Type != gjson.String {
			return nil
		}
		prefix, encoded, ok := strings.Cut(val.Str, ",")
		if !ok || !strings.HasPrefix(prefix, "data:") || !strings.HasSuffix(prefix, ";base64") {
			return nil
		}
		rawBytes, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return ErrRequestPluginMultipartInvalid
		}
		if len(rawBytes) == 0 {
			return nil
		}
		if len(files) >= requestPluginMultipartMaxFiles {
			return ErrRequestPluginMultipartTooLarge
		}
		mediaType := strings.TrimSuffix(strings.TrimPrefix(prefix, "data:"), ";base64")
		digest := sha256.Sum256(rawBytes)
		index := len(files)
		files = append(files, mediahost.MultipartFile{
			Field:  path,
			MIME:   mediaType,
			Size:   len(rawBytes),
			SHA256: digest,
			Open: func(ctx context.Context) (io.ReadCloser, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return io.NopCloser(bytes.NewReader(rawBytes)), nil
			},
		})
		var setErr error
		projected, setErr = sjson.SetBytes(projected, path, fmt.Sprintf("upload://image/%d", index))
		return setErr
	}

	if arr := gjson.GetBytes(projected, "images"); arr.IsArray() {
		for i := range arr.Array() {
			if err := replacePath(fmt.Sprintf("images.%d.image_url", i)); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := replacePath("mask.image_url"); err != nil {
		return nil, nil, err
	}
	if err := replacePath("markup.image_url"); err != nil {
		return nil, nil, err
	}
	if content := gjson.GetBytes(projected, "input.0.content"); content.IsArray() {
		for i := range content.Array() {
			if err := replacePath(fmt.Sprintf("input.0.content.%d.image_url", i)); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := replacePath("tools.0.input_image_mask.image_url"); err != nil {
		return nil, nil, err
	}
	if len(projected) > MaxRequestPluginBody {
		return nil, nil, ErrRequestPluginMultipartTooLarge
	}
	return projected, files, nil
}
