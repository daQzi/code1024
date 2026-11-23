// SPDX-License-Identifier: AGPL-3.0-or-later
// Attachment shapes, projection and streamed reference expansion follow
// QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f,
// relay/requestplugin/{media,attachments}.go. Resource limits are host-owned.
package mediahost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/tidwall/gjson"
	_ "golang.org/x/image/webp"
)

const MaxBody = 1 << 20

type item struct {
	data, mime, path, mimePath, filename string
	size                                 int
	dataURL                              bool
	readonly                             bool
	metadataOnly                         bool
	open                                 func(context.Context) (io.ReadCloser, error)
	digest                               [sha256.Size]byte
}

// Source belongs to one hook invocation. It never resolves remote URLs,
// arbitrary IDs, paths or credentials. An explicit Task-bound opener may
// supply bounded private files. Payloads stay in the parent process.
type Source struct {
	view        []byte
	items       []item
	attachments []jsplugin.Attachment
	listed      bool
	marks       map[string]Record
	textCache   *TextCache
	admitMemory MemoryAdmission
}

// Record intentionally excludes file names, paths, URLs, refs and text.
type Record struct {
	Kind      string `json:"kind"`
	Mime      string `json:"mime,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Rewritten bool   `json:"rewritten,omitempty"`
}

func New(body []byte, options ...Option) (*Source, error) {
	return NewWithCache(body, nil, options...)
}

func NewWithCache(body []byte, cache *TextCache, options ...Option) (*Source, error) {
	return newSource(body, cache, nil, options...)
}

func newSource(body []byte, cache *TextCache, resolve func(gjson.Result) (item, error), options ...Option) (*Source, error) {
	if len(body) > MaxBody || !utf8.Valid(body) || !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil, errors.New("invalid media request body")
	}
	if cache == nil {
		cache = &TextCache{}
	}
	source := &Source{textCache: cache}
	for _, option := range options {
		if option != nil {
			option(source)
		}
	}
	var view bytes.Buffer
	view.Grow(len(body))
	nodes := 0
	var walk func(gjson.Result, string, string, int) error
	walk = func(value gjson.Result, path, parent string, depth int) error {
		nodes++
		if nodes > 8192 || depth > 64 {
			return errors.New("media request structure exceeds limit")
		}
		switch {
		case value.IsObject():
			if value.Get("__fileRef").Exists() && resolve != nil {
				media, err := resolve(value)
				if err != nil {
					return err
				}
				media.path, media.readonly = path, true
				return source.add(&view, media)
			}
			mimeMember, filename := "", value.Get("filename").String()
			if value.Get("type").String() == "base64" {
				mimeMember = "media_type"
			} else if parent == "inlineData" || parent == "inline_data" {
				mimeMember = "mimeType"
				if !value.Get(mimeMember).Exists() {
					mimeMember = "mime_type"
				}
			} else if parent == "input_audio" {
				mimeMember = "format"
			}
			mediaMime := value.Get(mimeMember).String()
			if mimeMember == "format" && mediaMime != "" {
				mediaMime = "audio/" + strings.ToLower(mediaMime)
			}
			view.WriteByte('{')
			seen := map[string]bool{}
			var failure error
			first := true
			own := len(source.items)
			value.ForEach(func(key, child gjson.Result) bool {
				if seen[key.Str] {
					failure = errors.New("ambiguous media request")
					return false
				}
				seen[key.Str] = true
				if !first {
					view.WriteByte(',')
				}
				first = false
				view.WriteString(key.Raw)
				view.WriteByte(':')
				pointer := path + "/" + escapePointer(key.Str)
				if key.Str == "data" && mimeMember != "" && mediaMime != "" && child.Type == gjson.String {
					failure = source.add(&view, item{data: child.Str, mime: mediaMime, path: pointer, mimePath: path + "/" + mimeMember, filename: filename})
				} else {
					failure = walk(child, pointer, key.Str, depth+1)
				}
				return failure == nil
			})
			view.WriteByte('}')
			for i := own; filename != "" && i < len(source.items); i++ {
				if strings.LastIndexByte(source.items[i].path, '/') == len(path) {
					source.items[i].filename = filename
				}
			}
			return failure
		case value.IsArray():
			view.WriteByte('[')
			index := 0
			var failure error
			value.ForEach(func(_, child gjson.Result) bool {
				if index != 0 {
					view.WriteByte(',')
				}
				failure = walk(child, path+"/"+strconv.Itoa(index), "", depth+1)
				index++
				return failure == nil
			})
			view.WriteByte(']')
			return failure
		case value.Type == gjson.String:
			if header, data, found := strings.Cut(value.Str, ","); found && strings.HasPrefix(header, "data:") && strings.HasSuffix(header, ";base64") {
				return source.add(&view, item{data: data, mime: strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64"), path: path, dataURL: true})
			}
			view.WriteString(value.Raw)
		default:
			view.WriteString(value.Raw)
		}
		return nil
	}
	if err := walk(gjson.ParseBytes(body), "", "", 0); err != nil {
		return nil, err
	}
	source.view = view.Bytes()
	source.collect(gjson.ParseBytes(source.view), "", "", "")
	if len(source.attachments) > 64 {
		return nil, errors.New("media attachment count exceeds limit")
	}
	return source, nil
}

func (s *Source) add(view *bytes.Buffer, value item) error {
	if len(s.items) >= 64 {
		return errors.New("media count exceeds limit")
	}
	value.data = strings.NewReplacer("\r", "", "\n", "").Replace(value.data)
	value.size = base64.StdEncoding.DecodedLen(len(value.data)) - strings.Count(value.data[max(0, len(value.data)-2):], "=")
	value.size = max(0, value.size)
	fmt.Fprintf(view, `"[media:%d]"`, len(s.items))
	s.items = append(s.items, value)
	return nil
}

func escapePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func kind(mediaMime, filename string) string {
	mediaMime, _, _ = mime.ParseMediaType(mediaMime)
	for _, prefix := range []string{"image", "audio", "video"} {
		if strings.HasPrefix(mediaMime, prefix+"/") {
			return prefix
		}
	}
	if textFormat(mediaMime, filename) != "" || slices.Contains([]string{".doc", ".xls", ".xlsx", ".ppt", ".pptx", ".odt", ".ods", ".odp", ".rtf", ".epub", ".pages", ".numbers", ".key"}, strings.ToLower(path.Ext(filename))) ||
		mediaMime == "application/msword" || mediaMime == "application/rtf" || mediaMime == "application/epub+zip" ||
		strings.HasPrefix(mediaMime, "application/vnd.openxmlformats-officedocument.") || strings.HasPrefix(mediaMime, "application/vnd.ms-") || strings.HasPrefix(mediaMime, "application/vnd.oasis.opendocument.") {
		return "document"
	}
	return "other"
}

func (s *Source) View() []byte { return s.view }

func (s *Source) Projection() []jsplugin.TransformMedia {
	projection := make([]jsplugin.TransformMedia, len(s.items))
	for i, value := range s.items {
		projection[i] = jsplugin.TransformMedia{Index: i, Path: value.path, MimeType: value.mime, Bytes: value.size}
	}
	return projection
}

func (s *Source) get(ref string) (item, bool) {
	if !strings.HasPrefix(ref, "[media:") || !strings.HasSuffix(ref, "]") {
		return item{}, false
	}
	index, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(ref, "[media:"), "]"))
	if err != nil || index < 0 || index >= len(s.items) || ref != fmt.Sprintf("[media:%d]", index) {
		return item{}, false
	}
	return s.items[index], true
}

func (s *Source) collect(value gjson.Result, path, key, parentType string) {
	if len(s.attachments) > 64 {
		return
	}
	if value.IsObject() {
		block := value.Get("type").String()
		fileKind := "document"
		if block == "input_image" || parentType == "image" || key == "image_url" {
			fileKind = "image"
		}
		if key == "image_url" || key == "source" && block == "url" {
			address := value.Get("url").String()
			if strings.HasPrefix(address, "https://") || strings.HasPrefix(address, "http://") {
				s.attachments = append(s.attachments, jsplugin.Attachment{Kind: fileKind, URL: address, Path: path + "/url"})
			}
		}
		for _, member := range []string{"file_id", "file_url", "fileUri", "file_uri"} {
			if ref := value.Get(member); ref.Type == gjson.String {
				address := ""
				if member != "file_id" {
					address = ref.Str
				}
				s.attachments = append(s.attachments, jsplugin.Attachment{Kind: fileKind, URL: address, Filename: value.Get("filename").String(), Path: path + "/" + member})
			}
		}
		value.ForEach(func(name, child gjson.Result) bool {
			s.collect(child, path+"/"+escapePointer(name.Str), name.Str, block)
			return len(s.attachments) <= 64
		})
	} else if value.IsArray() {
		index := 0
		value.ForEach(func(_, child gjson.Result) bool {
			s.collect(child, path+"/"+strconv.Itoa(index), "", parentType)
			index++
			return len(s.attachments) <= 64
		})
	} else if value.Type == gjson.String {
		if media, ok := s.get(value.Str); ok {
			s.attachments = append(s.attachments, jsplugin.Attachment{Ref: value.Str, Kind: kind(media.mime, media.filename), Mime: media.mime, Size: media.size, Filename: media.filename, Path: path, Rewritable: rewritable(media)})
		} else if key == "image_url" && (strings.HasPrefix(value.Str, "https://") || strings.HasPrefix(value.Str, "http://")) {
			s.attachments = append(s.attachments, jsplugin.Attachment{Kind: "image", URL: value.Str, Path: path})
		}
	}
}

func (s *Source) Attachments() []jsplugin.Attachment {
	s.listed = true
	return append([]jsplugin.Attachment(nil), s.attachments...)
}

func (s *Source) Media(ref string) (jsplugin.MediaInfo, bool) {
	value, ok := s.get(ref)
	if !ok {
		return jsplugin.MediaInfo{}, false
	}
	info := jsplugin.MediaInfo{Kind: kind(value.mime, value.filename), Mime: value.mime, Size: value.size, Filename: value.filename, Rewritable: rewritable(value)}
	if info.Kind == "image" && !value.metadataOnly {
		var src io.Reader = base64.NewDecoder(base64.StdEncoding, strings.NewReader(value.data))
		if value.open != nil {
			// These openers are local and request-owned. Bound header inspection
			// so an image with a long prefix never streams through the worker.
			reader, err := value.open(context.Background())
			if reader != nil {
				defer reader.Close()
			}
			if err != nil || reader == nil {
				return info, true
			}
			src = io.LimitReader(reader, int64(min(value.size, 1<<20)))
		}
		config, _, err := image.DecodeConfig(src)
		if err == nil && config.Width > 0 && config.Height > 0 {
			info.Width, info.Height = config.Width, config.Height
		}
	}
	return info, true
}

func (s *Source) MediaText(ctx context.Context, ref string, maxChars int) (jsplugin.MediaText, bool) {
	value, ok := s.get(ref)
	if !ok {
		return jsplugin.MediaText{}, false
	}
	result := jsplugin.MediaText{Unchecked: "limit"}
	if value.metadataOnly {
		result.Unchecked = "reference"
	} else if ctx.Err() == nil && maxChars >= 1 && maxChars <= jsplugin.MaxMediaTextChars {
		result = s.textCache.read(ctx, value, s.admitMemory)
		if result.Unchecked == "" {
			text, _, truncated := cutText(result.Text, maxChars)
			result.Text, result.Truncated = text, result.Truncated || truncated
		}
	}
	status := "checked"
	if result.Unchecked != "" {
		status = "unchecked"
	} else if result.Truncated {
		status = "truncated"
	}
	s.mark(ref, status, result.Unchecked, false)
	return result, true
}

func rewritable(value item) bool {
	return !value.readonly && textFormat(value.mime, value.filename) == formatText
}

func cutText(text string, limit int) (string, int, bool) {
	chars := 0
	for offset, r := range text {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if chars+width > limit {
			return text[:offset], chars, true
		}
		chars += width
	}
	return text, chars, false
}

func (s *Source) mark(ref, status, reason string, rewritten bool) {
	if s.marks == nil {
		s.marks = map[string]Record{}
	}
	previous := s.marks[ref]
	rank := map[string]int{"unchecked": 1, "truncated": 2, "checked": 3}
	if rank[status] >= rank[previous.Status] {
		previous.Status, previous.Reason = status, reason
	}
	previous.Rewritten = previous.Rewritten || rewritten
	s.marks[ref] = previous
}

func (s *Source) Records() ([]Record, int) {
	var records []Record
	for _, attachment := range s.attachments {
		record, used := s.marks[attachment.Ref]
		if !s.listed && !used {
			continue
		}
		record.Kind, record.Mime = attachment.Kind, recordMime(attachment.Mime)
		if record.Status == "" {
			record.Status = "unchecked"
			if attachment.Ref == "" {
				record.Reason = "reference"
			}
		}
		records = append(records, record)
	}
	more := max(0, len(records)-16)
	return records[:min(16, len(records))], more
}

func recordMime(value string) string {
	if value == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || len(mediaType) > 128 {
		return "application/octet-stream"
	}
	return mediaType
}

// FetchBody replaces only complete JSON string values, never keys or partial
// matches. Reader parts point at already-owned strings; no expanded data URL
// or attachment bytes are allocated in the child process.
func (s *Source) FetchBody(body string) (io.Reader, int64, error) {
	if len(body) > MaxBody || !json.Valid([]byte(body)) || !strings.Contains(body, "[media:") {
		return nil, 0, nil
	}
	readers := []io.Reader{}
	offset, count, total := 0, 0, int64(len(body))
	for i := 0; i < len(body); i++ {
		if body[i] != '"' {
			continue
		}
		start := i
		for i++; i < len(body); i++ {
			if body[i] == '\\' {
				i++
			} else if body[i] == '"' {
				break
			}
		}
		end := i + 1
		j := end
		for j < len(body) && strings.ContainsRune(" \r\n\t", rune(body[j])) {
			j++
		}
		if j < len(body) && body[j] == ':' {
			continue
		}
		var ref string
		if json.Unmarshal([]byte(body[start:end]), &ref) != nil {
			return nil, 0, errors.New("invalid media fetch string")
		}
		value, ok := s.get(ref)
		if !ok {
			continue
		}
		if value.open != nil || value.metadataOnly {
			return nil, 0, errors.New("read-only attachments cannot be expanded into fetch requests")
		}
		count++
		if count > 8 {
			return nil, 0, errors.New("media fetch count exceeds limit")
		}
		if _, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(value.data))); err != nil {
			return nil, 0, errors.New("invalid media base64")
		}
		data := strings.NewReplacer("\r", "", "\n", "").Replace(value.data)
		mediaType, _, err := mime.ParseMediaType(value.mime)
		if err != nil {
			mediaType = "application/octet-stream"
		}
		header, _ := json.Marshal("data:" + mediaType + ";base64,")
		prefix := string(header[:len(header)-1])
		total += int64(len(prefix) + len(data) + 1 - (end - start))
		if total > 32<<20 {
			return nil, 0, errors.New("media fetch bytes exceed limit")
		}
		readers = append(readers, strings.NewReader(body[offset:start]), strings.NewReader(prefix), strings.NewReader(data), strings.NewReader(`"`))
		s.mark(ref, "checked", "", false)
		offset = end
	}
	if count == 0 {
		return nil, 0, nil
	}
	readers = append(readers, strings.NewReader(body[offset:]))
	return io.MultiReader(readers...), total, nil
}
