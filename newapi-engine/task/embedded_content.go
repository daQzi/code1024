// SPDX-License-Identifier: AGPL-3.0-or-later
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f,
// controller/video_proxy.go, writeTaskMediaContentPath/writeTaskMediaBase64.
package task

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/tidwall/gjson"
)

const MaxEmbeddedArtifactBytes int64 = 64 << 20
const MaxEmbeddedJSONBytes int64 = (MaxEmbeddedArtifactBytes*4+2)/3 + 1<<20

var ErrEmbeddedArtifactNotReady = errors.New("task artifacts are not ready")
var ErrEmbeddedArtifactGone = errors.New("artifact content is no longer available")

// DecodeEmbeddedArtifact follows the pinned long-running operation semantics.
// It returns a validating decoder over immutable JSON-owned strings, without
// materializing the decoded movie. The host owns input memory admission and
// must retain that reservation until the returned reader is closed/discarded.
func DecodeEmbeddedArtifact(raw []byte, path []any) (io.Reader, int64, error) {
	if int64(len(raw)) > MaxEmbeddedJSONBytes || len(path) == 0 || !json.Valid(raw) {
		return nil, 0, errors.New("invalid or oversized embedded artifact JSON")
	}
	if err := jsplugin.ValidateJSONPath(path); err != nil {
		return nil, 0, err
	}
	operation := gjson.ParseBytes(raw)
	if !operation.IsObject() {
		return nil, 0, errors.New("embedded artifact must be an operation object")
	}
	if failure := operation.Get("error"); failure.Exists() && failure.Type != gjson.Null {
		return nil, 0, errors.New("artifact upstream returned an error")
	}
	if operation.Get("done").Type != gjson.True && operation.Get("status").String() != "completed" {
		return nil, 0, ErrEmbeddedArtifactNotReady
	}
	content := operation
	for _, segment := range path {
		if key, ok := segment.(string); ok {
			if !content.IsObject() {
				return nil, 0, ErrEmbeddedArtifactGone
			}
			content = content.Get(gjson.Escape(key))
			continue
		}
		if !content.IsArray() {
			return nil, 0, ErrEmbeddedArtifactGone
		}
		var index string
		switch value := segment.(type) {
		case int:
			index = strconv.Itoa(value)
		case int64:
			index = strconv.FormatInt(value, 10)
		case float64:
			index = strconv.FormatFloat(value, 'f', -1, 64)
		}
		content = content.Get(index)
	}
	if content.Type != gjson.String || content.Str == "" {
		return nil, 0, ErrEmbeddedArtifactGone
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		// The pinned host uses its base64 validation wrapper. The standard Go
		// decoder gives the same full validation/count without a movie-sized
		// allocation; decoding again during delivery stays linear in size.
		size, err := io.Copy(io.Discard, io.LimitReader(base64.NewDecoder(encoding, strings.NewReader(content.Str)), MaxEmbeddedArtifactBytes+1))
		if err == nil {
			if size == 0 || size > MaxEmbeddedArtifactBytes {
				return nil, 0, errors.New("embedded artifact exceeds decoded size limit")
			}
			return base64.NewDecoder(encoding, strings.NewReader(content.Str)), size, nil
		}
	}
	return nil, 0, errors.New("invalid embedded artifact base64")
}
