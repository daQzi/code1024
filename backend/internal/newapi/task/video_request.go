package task

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
)

// NormalizeVideoRequest converts the bounded HTTP envelope into the shared
// plugin protocol representation. Callers reserve ingress memory before use.
func NormalizeVideoRequest(contentType string, body []byte) (json.RawMessage, error) {
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, errors.New("use application/json or multipart/form-data")
	}
	if media == "application/json" {
		var object map[string]json.RawMessage
		if json.Unmarshal(body, &object) != nil || object == nil {
			return nil, errors.New("a JSON object is required")
		}
		return body, nil
	}
	if media != "multipart/form-data" || params["boundary"] == "" {
		return nil, errors.New("use application/json or multipart/form-data")
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	fields := map[string]any{}
	for count := 0; ; count++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil || count >= 64 {
			return nil, errors.New("invalid or excessive multipart fields")
		}
		name := part.FormName()
		if name == "" {
			return nil, errors.New("multipart field name is required")
		}
		if _, exists := fields[name]; exists {
			return nil, errors.New("duplicate multipart field: " + name)
		}
		value, err := io.ReadAll(io.LimitReader(part, 512<<10+1))
		part.Close()
		if err != nil || len(value) > 512<<10 {
			return nil, errors.New("multipart field exceeds 512 KiB; use an input_reference URL")
		}
		if part.FileName() != "" {
			if name != "input_reference" {
				return nil, errors.New("unexpected file field")
			}
			kind := http.DetectContentType(value)
			if kind != "image/png" && kind != "image/jpeg" && kind != "image/webp" {
				return nil, errors.New("input_reference must be a PNG, JPEG or WebP image")
			}
			fields[name] = "data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(value)
		} else if name == "metadata" {
			var metadata map[string]any
			if json.Unmarshal(value, &metadata) != nil || metadata == nil {
				return nil, errors.New("metadata must be a JSON object")
			}
			fields[name] = metadata
		} else {
			fields[name] = string(value)
		}
	}
	return json.Marshal(fields)
}
