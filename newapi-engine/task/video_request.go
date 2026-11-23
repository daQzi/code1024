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
	"regexp"
)

var ErrVideoMultipartTooLarge = errors.New("video multipart text exceeds its limit")

var videoFileFieldPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.\[\]-]{0,127}$`)

// File field names are opaque protocol inputs. Routing and text-only names
// cannot be replaced by an upload; actual model support is the decoder's job.
func ValidVideoFileField(name string) bool {
	if !videoFileFieldPattern.MatchString(name) {
		return false
	}
	switch name {
	case "model", "prompt", "seconds", "duration", "size", "resolution", "metadata", "file_references", "stream", "user", "constructor", "prototype":
		return false
	}
	return true
}

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
	return ParseVideoMultipart(contentType, bytes.NewReader(body), func(name, filename string, source io.Reader) (any, error) {
		value, err := io.ReadAll(io.LimitReader(source, 512<<10+1))
		if err != nil {
			return nil, err
		}
		if len(value) > 512<<10 {
			return nil, errors.New("multipart file exceeds 512 KiB; use an input_reference URL")
		}
		kind := http.DetectContentType(value)
		if kind != "image/png" && kind != "image/jpeg" && kind != "image/webp" {
			return nil, errors.New("input_reference must be a PNG, JPEG or WebP image")
		}
		return "data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(value), nil
	})
}

// ParseVideoMultipart shares the Videos field/metadata contract between the
// small in-memory codec and the streaming host. File ownership stays with the
// callback; the codec never reads an entire file or exposes a disk path to JS.
func ParseVideoMultipart(contentType string, body io.Reader, fileValue func(field, filename string, source io.Reader) (any, error)) (json.RawMessage, error) {
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil || media != "multipart/form-data" || params["boundary"] == "" || len(params["boundary"]) > 70 {
		return nil, errors.New("invalid multipart boundary")
	}
	reader := multipart.NewReader(body, params["boundary"])
	fields := map[string]any{}
	fileFields := map[string]int{}
	fileCount := 0
	textBytes := 0
	for count := 0; ; count++ {
		// Do not silently decode Content-Transfer-Encoding: identity and size
		// must describe the bytes actually uploaded by the client.
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if count >= 64 {
			return nil, errors.New("invalid or excessive multipart fields")
		}
		name := part.FormName()
		if name == "" || len(name) > 128 || len(part.Header.Get("Content-Disposition")) > 2048 || part.Header.Get("Content-Transfer-Encoding") != "" {
			return nil, errors.New("multipart field name is required")
		}
		if part.FileName() != "" {
			if !ValidVideoFileField(name) || fileCount >= MaxTaskFiles {
				return nil, errors.New("invalid or excessive multipart file fields")
			}
			if _, exists := fields[name]; exists && fileFields[name] == 0 {
				return nil, errors.New("file and text field conflict: " + name)
			}
			if fileValue == nil {
				return nil, errors.New("multipart files unavailable")
			}
			value, err := fileValue(name, part.FileName(), part)
			if err != nil {
				return nil, err
			}
			switch fileFields[name] {
			case 0:
				fields[name] = value
			case 1:
				fields[name] = []any{fields[name], value}
			default:
				fields[name] = append(fields[name].([]any), value)
			}
			fileFields[name]++
			fileCount++
			continue
		}
		if _, exists := fields[name]; exists {
			return nil, errors.New("duplicate multipart field: " + name)
		}
		value, err := io.ReadAll(io.LimitReader(part, int64(min(512<<10, (1<<20)-textBytes))+1))
		if err != nil {
			return nil, err
		}
		textBytes += len(value)
		if len(value) > 512<<10 || textBytes > 1<<20 {
			return nil, ErrVideoMultipartTooLarge
		}
		if name == "metadata" {
			var metadata map[string]any
			if json.Unmarshal(value, &metadata) != nil || metadata == nil {
				return nil, errors.New("metadata must be a JSON object")
			}
			fields[name] = metadata
		} else {
			fields[name] = string(value)
		}
	}
	result, err := json.Marshal(fields)
	if err == nil && len(result) > 1<<20 {
		return nil, ErrVideoMultipartTooLarge
	}
	return result, err
}
