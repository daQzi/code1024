// SPDX-License-Identifier: AGPL-3.0-or-later
// Bound file projection follows the pinned New API attachment contract.
// The Task codec owns placeholder validation and the inline byte limit.
package mediahost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"

	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/tidwall/gjson"
)

// NewWithFileReferences inspects the bounded JSON body a Task would send.
// Only the caller's task-bound opener may resolve a ref. Persistent files
// remain read-only and neither their private ID nor bytes enter JavaScript.
// Multipart keeps the pinned baseline's explicit unchecked behavior.
func NewWithFileReferences(ctx context.Context, body []byte, cache *TextCache, open pluginTask.OpenFile, options ...Option) (*Source, error) {
	expanded := int64(len(body))
	return newSource(body, cache, func(value gjson.Result) (item, error) {
		seen := map[string]bool{}
		valid := true
		value.ForEach(func(key, _ gjson.Result) bool {
			if seen[key.Str] {
				valid = false
			}
			seen[key.Str] = true
			return valid
		})
		if !valid {
			return item{}, errors.New("ambiguous task file placeholder")
		}
		var file pluginTask.File
		resolved, err := pluginTask.ResolveFileJSON(ctx, json.RawMessage(value.Raw), func(call context.Context, ref string) (pluginTask.File, io.ReadCloser, error) {
			if open == nil {
				return pluginTask.File{}, nil, errors.New("task file source unavailable")
			}
			metadata, reader, err := open(call, ref)
			if err != nil {
				if reader != nil {
					_ = reader.Close()
				}
				return metadata, reader, err
			}
			if reader == nil {
				return metadata, nil, errors.New("task file source returned no reader")
			}
			// Check the complete inspection expansion before reading each file,
			// including repeated refs. The normal wire codec checks it again.
			if metadata.Size <= 0 || metadata.Size > pluginTask.MaxInlineFileBytes || expanded+int64(base64.StdEncoding.EncodedLen(int(metadata.Size)))+1024 > MaxBody {
				_ = reader.Close()
				return metadata, nil, errors.New("task media inspection exceeds inline limit")
			}
			expanded += int64(base64.StdEncoding.EncodedLen(int(metadata.Size))) + 1024
			file = metadata
			return metadata, reader, nil
		})
		if err != nil {
			return item{}, err
		}
		var encoded string
		if json.Unmarshal(resolved, &encoded) != nil {
			return item{}, errors.New("invalid task file inspection result")
		}
		media := item{data: encoded, mime: file.MimeType, filename: file.Filename}
		if value.Get("encoding").Str == "dataUrl" {
			header, data, found := strings.Cut(encoded, ",")
			if !found {
				return item{}, errors.New("invalid task file data URL")
			}
			media.data = data
			media.mime = strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
		}
		return media, nil
	}, options...)
}
