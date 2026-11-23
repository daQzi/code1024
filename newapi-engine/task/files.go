// SPDX-License-Identifier: AGPL-3.0-or-later
// File-reference semantics follow QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f, task/jsplugin/adaptor.go.
package task

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"
)

const MaxFileBytes int64 = 32 << 20
const MaxTaskFiles = 4
const MaxMultipartBytes int64 = MaxTaskFiles*MaxFileBytes + MaxSnapshotBytes
const MaxInlineFileBytes int64 = 512 << 10

// File is public-to-plugin metadata, never a local path or a storage URL.
type File struct {
	Ref      string `json:"ref"`
	Field    string `json:"field"`
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
}

type OpenFile func(context.Context, string) (File, io.ReadCloser, error)

// WireBody follows the fetch/content descriptor contract: strings are the
// plugin's own wire text, null means no body, and other JSON is already encoded.
func (r Request) WireBody(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(r.Body) > MaxSnapshotBytes {
		return nil, errors.New("task request body exceeds limit")
	}
	body := bytes.TrimSpace(r.Body)
	if len(body) == 0 || bytes.Equal(body, []byte("null")) {
		return nil, nil
	}
	if body[0] == '"' {
		var text string
		if err := json.Unmarshal(body, &text); err != nil {
			return nil, errors.New("invalid task text body")
		}
		return []byte(text), nil
	}
	if !json.Valid(r.Body) {
		return nil, errors.New("invalid task JSON body")
	}
	return r.Body, nil
}

// ResolveBody additionally expands file placeholders on generation requests.
// Fetch/content descriptors in the pinned host do not expand those references.
// The caller reserves the shared ingress memory budget before materialization.
func (r Request) ResolveBody(ctx context.Context, open OpenFile) ([]byte, error) {
	body, err := r.WireBody(ctx)
	if err != nil || r.OpaqueBody() {
		return body, err
	}
	return ResolveFileJSON(ctx, body, open)
}

func (r Request) OpaqueBody() bool {
	return r.BodyType == "multipart" || bytes.HasPrefix(bytes.TrimSpace(r.Body), []byte{'"'})
}

// WriteMultipart writes to host-owned storage. No goroutine, pipe, or whole
// file buffer is needed; callers can hash and rewind the exact signed bytes.
func WriteMultipart(ctx context.Context, dst io.Writer, parts []Part, open OpenFile) (string, error) {
	if len(parts) > 64 {
		return "", errors.New("too many multipart fields")
	}
	writer := multipart.NewWriter(dst)
	var total int64
	var textBytes int
	for _, part := range parts {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if part.Name == "" || len(part.Name) > 128 || strings.ContainsAny(part.Name, "\r\n\x00") {
			return "", errors.New("invalid multipart field name")
		}
		if part.FileRef == "" {
			text, err := multipartScalar(part.Value)
			if err != nil {
				return "", err
			}
			textBytes += len(text) + 2048
			if textBytes > MaxSnapshotBytes {
				return "", errors.New("multipart text exceeds limit")
			}
			total += int64(len(text) + 2048)
			if total > MaxMultipartBytes {
				return "", errors.New("multipart body exceeds limit")
			}
			if err = writer.WriteField(part.Name, text); err != nil {
				return "", err
			}
			continue
		}
		if open == nil || len(part.Value) != 0 && string(part.Value) != "null" {
			return "", errors.New("invalid or unresolved multipart file reference")
		}
		file, reader, err := open(ctx, part.FileRef)
		if err != nil {
			return "", err
		}
		if file.Size <= 0 || file.Size > MaxFileBytes {
			reader.Close()
			return "", errors.New("file exceeds size limit")
		}
		filename := file.Filename
		if part.Filename != "" {
			filename = part.Filename
		}
		if filename == "" || len(filename) > 255 || strings.ContainsAny(filename, "\r\n\x00/\\") {
			reader.Close()
			return "", errors.New("invalid multipart filename")
		}
		kind, _, parseErr := mime.ParseMediaType(file.MimeType)
		if parseErr != nil {
			reader.Close()
			return "", errors.New("invalid file MIME type")
		}
		total += file.Size + 2048
		if total > MaxMultipartBytes {
			reader.Close()
			return "", errors.New("multipart body exceeds limit")
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": part.Name, "filename": filename}))
		header.Set("Content-Type", kind)
		out, err := writer.CreatePart(header)
		if err == nil {
			var n int64
			n, err = io.Copy(out, io.LimitReader(reader, file.Size+1))
			if err == nil && n != file.Size {
				err = errors.New("file size differs from snapshot")
			}
		}
		closeErr := reader.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return writer.FormDataContentType(), nil
}

// ResolveFileJSON implements the strict __fileRef placeholder contract. Inline
// expansion is deliberately small; larger files use multipart outside JS.
func ResolveFileJSON(ctx context.Context, body json.RawMessage, open OpenFile) (json.RawMessage, error) {
	if len(body) == 0 {
		return body, nil
	}
	if len(body) > MaxSnapshotBytes {
		return nil, errors.New("JSON body exceeds limit")
	}
	if !json.Valid(body) {
		return nil, errors.New("invalid JSON request body")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var total int64
	changed := false
	var visit func(any, int) (any, error)
	visit = func(value any, depth int) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if depth > 64 {
			return nil, errors.New("file placeholder nesting exceeds limit")
		}
		switch object := value.(type) {
		case map[string]any:
			if raw, found := object["__fileRef"]; found {
				changed = true
				for key := range object {
					if key != "__fileRef" && key != "encoding" && key != "mimeType" && key != "maxBytes" {
						return nil, errors.New("unexpected file placeholder property")
					}
				}
				ref, ok := raw.(string)
				if !ok || ref == "" || open == nil {
					return nil, errors.New("invalid file reference")
				}
				encoding, ok := object["encoding"].(string)
				if !ok || encoding != "base64" && encoding != "dataUrl" {
					return nil, errors.New("invalid file encoding")
				}
				limit := MaxInlineFileBytes
				if raw, exists := object["maxBytes"]; exists {
					number, ok := raw.(json.Number)
					if !ok {
						return nil, errors.New("invalid file size bound")
					}
					n, err := number.Int64()
					if err != nil || n <= 0 {
						return nil, errors.New("invalid file size bound")
					}
					limit = min(n, MaxInlineFileBytes)
				}
				file, reader, err := open(ctx, ref)
				if err != nil {
					return nil, err
				}
				if file.Size <= 0 || file.Size > limit || total+int64(base64.StdEncoding.EncodedLen(int(file.Size))) > MaxSnapshotBytes {
					reader.Close()
					return nil, errors.New("inline file exceeds limit")
				}
				data, err := io.ReadAll(io.LimitReader(reader, file.Size+1))
				closeErr := reader.Close()
				if err != nil {
					return nil, err
				}
				if closeErr != nil {
					return nil, closeErr
				}
				if int64(len(data)) != file.Size {
					return nil, errors.New("file size differs from snapshot")
				}
				text := base64.StdEncoding.EncodeToString(data)
				if encoding == "dataUrl" {
					kind := file.MimeType
					if raw, exists := object["mimeType"]; exists {
						var ok bool
						kind, ok = raw.(string)
						if !ok {
							return nil, errors.New("invalid MIME override")
						}
					}
					if _, _, err := mime.ParseMediaType(kind); err != nil || len(kind) > 128 || strings.ContainsAny(kind, ",;\r\n") {
						return nil, errors.New("invalid file MIME type")
					}
					text = "data:" + kind + ";base64," + text
				}
				total += int64(len(text))
				return text, nil
			}
			for key, child := range object {
				next, err := visit(child, depth+1)
				if err != nil {
					return nil, err
				}
				object[key] = next
			}
		case []any:
			for i, child := range object {
				next, err := visit(child, depth+1)
				if err != nil {
					return nil, err
				}
				object[i] = next
			}
		}
		return value, nil
	}
	value, err := visit(value, 0)
	if err != nil {
		return nil, err
	}
	if !changed {
		return body, nil
	}
	out, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(out) > MaxSnapshotBytes {
		return nil, errors.New("expanded JSON body exceeds limit")
	}
	return out, nil
}
