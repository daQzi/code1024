// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f.
// See ../NOTICE.md for provenance and local adaptations.

package provider

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/textproto"
	"slices"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/tidwall/gjson"
)

// DefaultMaxMultipartBodyBytes is the fallback size bound for Provider
// multipart/file assembly and $file expansion when callers omit a tighter limit.
const DefaultMaxMultipartBodyBytes int64 = 128 << 20

// ClientFile is a single uploaded file from a multipart client request,
// detached from any HTTP framework type. Open must return a fresh reader over
// the immutable client upload bytes.
type ClientFile struct {
	Field       string
	Filename    string
	ContentType string
	Size        int64
	Open        func() (io.ReadCloser, error)
}

// BuildRequestForm builds the jsplugin.RequestForm that convertRequest sees
// from client multipart fields and files, rewriting the model field to the
// upstream model when present and indexing files by field name (first is
// "field", later ones are "field#1", "field#2", ...).
func BuildRequestForm(fields map[string][]string, files []ClientFile, upstreamModel string) (*jsplugin.RequestForm, map[string]ClientFile) {
	cloned := make(map[string][]string, len(fields))
	for name, values := range fields {
		cloned[name] = slices.Clone(values)
	}
	if _, exists := cloned["model"]; exists && upstreamModel != "" {
		cloned["model"] = []string{upstreamModel}
	}
	input := &jsplugin.RequestForm{Fields: cloned}
	grouped := make(map[string][]ClientFile)
	for _, file := range files {
		grouped[file.Field] = append(grouped[file.Field], file)
	}
	indexed := make(map[string]ClientFile, len(files))
	for _, field := range slices.Sorted(maps.Keys(grouped)) {
		for idx, file := range grouped[field] {
			ref := field
			if idx > 0 {
				ref = fmt.Sprintf("%s#%d", field, idx)
			}
			indexed[ref] = file
			input.Files = append(input.Files, jsplugin.RequestFile{
				Field:       field,
				Filename:    file.Filename,
				ContentType: file.ContentType,
				Size:        file.Size,
			})
		}
	}
	return input, indexed
}

// BuildUpstreamBody materializes a Provider convertRequest or next-step result
// into bytes and a Content-Type header:
//   - converted.Form != nil: multipart/form-data of sorted fields and client files
//   - converted.File != "": raw bytes of the named client file
//   - otherwise: converted.Body (defaulting Content-Type to application/json if
//     the original client request was multipart and ContentType is unset).
//
// formOrFile reports whether the step uses Form or File (in which case
// prepareRequest and $file markers do not apply).
func BuildUpstreamBody(converted jsplugin.ConvertedRequest, files map[string]ClientFile, formRequest bool, maxBytes int64) ([]byte, string, bool, error) {
	limit := effectiveMaxBytes(maxBytes)
	switch {
	case converted.Form != nil:
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		for _, name := range slices.Sorted(maps.Keys(converted.Form.Fields)) {
			for _, value := range converted.Form.Fields[name] {
				if err := writer.WriteField(name, value); err != nil {
					return nil, "", true, err
				}
				if int64(buffer.Len()) > limit {
					return nil, "", true, fmt.Errorf("converted request exceeds %d bytes", limit)
				}
			}
		}
		for _, file := range converted.Form.Files {
			clientFile, ok := files[file.From]
			if !ok {
				return nil, "", true, fmt.Errorf("convertRequest result form file %q names no client file", file.From)
			}
			if clientFile.Size < 0 || int64(buffer.Len())+clientFile.Size > limit {
				return nil, "", true, fmt.Errorf("converted request exceeds %d bytes", limit)
			}
			part, err := writer.CreatePart(textproto.MIMEHeader{
				"Content-Disposition": {multipart.FileContentDisposition(file.Field, cmp.Or(file.Filename, clientFile.Filename))},
				"Content-Type":        {cmp.Or(file.ContentType, clientFile.ContentType, "application/octet-stream")},
			})
			if err == nil {
				remaining := limit - int64(buffer.Len())
				err = copyClientFile(part, clientFile, remaining)
			}
			if err != nil {
				return nil, "", true, err
			}
		}
		if err := writer.Close(); err != nil {
			return nil, "", true, err
		}
		if int64(buffer.Len()) > limit {
			return nil, "", true, fmt.Errorf("converted request exceeds %d bytes", limit)
		}
		return buffer.Bytes(), writer.FormDataContentType(), true, nil

	case converted.File != "":
		clientFile, ok := files[converted.File]
		if !ok {
			return nil, "", true, fmt.Errorf("convertRequest result file %q names no client file", converted.File)
		}
		if clientFile.Size < 0 || clientFile.Size > limit {
			return nil, "", true, fmt.Errorf("converted request exceeds %d bytes", limit)
		}
		var buffer bytes.Buffer
		if err := copyClientFile(&buffer, clientFile, limit); err != nil {
			return nil, "", true, err
		}
		contentType := cmp.Or(converted.ContentType, clientFile.ContentType, "application/octet-stream")
		return buffer.Bytes(), contentType, true, nil
	}
	if converted.ContentType == "" && formRequest {
		return converted.Body, "application/json", false, nil
	}
	return converted.Body, converted.ContentType, false, nil
}

type fileMarker struct {
	start, end    int
	ref, encoding string
}

// ReplaceFileMarkers replaces every {"$file": "<ref>", "encoding": "base64"|"dataUrl"}
// marker inside a JSON body with the corresponding client file encoded as base64
// or a data URL, keeping every other byte verbatim. It fails closed on malformed
// markers, missing multipart files, unknown refs, or size overflow.
func ReplaceFileMarkers(body []byte, files map[string]ClientFile, maxBytes int64) ([]byte, error) {
	if !bytes.Contains(body, []byte(`"$file"`)) || !gjson.ValidBytes(body) {
		return body, nil
	}
	root := gjson.ParseBytes(body)
	markers, err := collectFileMarkers(root, nil)
	if err != nil || len(markers) == 0 {
		return body, err
	}
	if len(files) == 0 {
		return nil, errors.New("$file markers need a multipart client request")
	}
	limit := effectiveMaxBytes(maxBytes)
	offset := len(body) - len(root.Raw)
	prefixes := make([][]byte, len(markers))
	size := int64(len(body))
	for idx, marker := range markers {
		clientFile, ok := files[marker.ref]
		if !ok {
			return nil, fmt.Errorf("$file %q names no client file", marker.ref)
		}
		if clientFile.Size < 0 {
			return nil, fmt.Errorf("$file %q has invalid size", marker.ref)
		}
		prefixes[idx] = []byte(`"`)
		if marker.encoding == "dataUrl" {
			quoted, err := json.Marshal("data:" + cmp.Or(clientFile.ContentType, "application/octet-stream") + ";base64,")
			if err != nil {
				return nil, err
			}
			prefixes[idx] = quoted[:len(quoted)-1]
		}
		encodedLen := int64(base64.StdEncoding.EncodedLen(int(clientFile.Size)))
		size += int64(len(prefixes[idx])) + encodedLen + 1 - int64(marker.end-marker.start)
		if size > limit {
			return nil, fmt.Errorf("converted request exceeds %d bytes", limit)
		}
	}
	var replaced bytes.Buffer
	if size > 0 {
		replaced.Grow(int(size))
	}
	last := 0
	for idx, marker := range markers {
		replaced.Write(body[last : offset+marker.start])
		replaced.Write(prefixes[idx])
		clientFile := files[marker.ref]
		encoder := base64.NewEncoder(base64.StdEncoding, &replaced)
		if err := copyClientFile(encoder, clientFile, limit); err != nil {
			return nil, err
		}
		if err := encoder.Close(); err != nil {
			return nil, err
		}
		replaced.WriteByte('"')
		if int64(replaced.Len()) > limit {
			return nil, fmt.Errorf("converted request exceeds %d bytes", limit)
		}
		last = offset + marker.end
	}
	replaced.Write(body[last:])
	if int64(replaced.Len()) > limit {
		return nil, fmt.Errorf("converted request exceeds %d bytes", limit)
	}
	return replaced.Bytes(), nil
}

func collectFileMarkers(value gjson.Result, markers []fileMarker) ([]fileMarker, error) {
	if value.IsObject() {
		members := 0
		var ref, encoding gjson.Result
		value.ForEach(func(key, member gjson.Result) bool {
			members++
			switch key.String() {
			case "$file":
				ref = member
			case "encoding":
				encoding = member
			}
			return true
		})
		if ref.Exists() {
			if members != 2 || ref.Type != gjson.String || ref.Str == "" || encoding.Type != gjson.String || (encoding.Str != "base64" && encoding.Str != "dataUrl") {
				return nil, errors.New(`a $file marker must be {"$file": "<client file>", "encoding": "base64" or "dataUrl"}`)
			}
			return append(markers, fileMarker{
				start:    value.Index,
				end:      value.Index + len(value.Raw),
				ref:      ref.Str,
				encoding: encoding.Str,
			}), nil
		}
	}
	var err error
	value.ForEach(func(_, member gjson.Result) bool {
		if member.IsObject() || member.IsArray() {
			markers, err = collectFileMarkers(member, markers)
		}
		return err == nil
	})
	return markers, err
}

func copyClientFile(target io.Writer, file ClientFile, maxBytes int64) error {
	if file.Open == nil {
		return errors.New("client file reader is unavailable")
	}
	reader, err := file.Open()
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	n, err := io.Copy(target, io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return err
	}
	if n > maxBytes {
		return fmt.Errorf("converted request exceeds %d bytes", maxBytes)
	}
	return nil
}

func effectiveMaxBytes(maxBytes int64) int64 {
	if maxBytes > 0 {
		return maxBytes
	}
	return DefaultMaxMultipartBodyBytes
}
