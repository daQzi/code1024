// SPDX-License-Identifier: AGPL-3.0-or-later
// Attachment format rules and extraction bounds follow QuantumNous/new-api
// at 48f9df96ea7c68eae04a0f3687869f232f48929f, relay/requestplugin/attachments.go.
package mediahost

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"mime"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

const (
	formatText = "text"
	formatDOCX = "docx"
	formatPDF  = "pdf"
	docxMime   = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

	uncheckedUnsupported = "unsupported"
	uncheckedTooLarge    = "too_large"
	uncheckedUnreadable  = "unreadable"
	uncheckedLimit       = "limit"
	uncheckedBusy        = "busy"
	uncheckedNoText      = "no_text"

	maxDocumentBytes = 10 << 20
	documentMemory   = 160 << 20
)

var documentSlots = make(chan struct{}, 2)
var documentBudget = &memoryBudget{limit: 2 * documentMemory}

var textExtensions = strings.Fields(".txt .text .md .markdown .csv .tsv .json .jsonl .ndjson .xml .yaml .yml .toml .ini .cfg .conf .log .html .htm .css .js .mjs .cjs .ts .tsx .jsx .vue .svelte .py .go .java .kt .kts .scala .c .h .cc .cpp .hpp .cs .rb .php .rs .swift .m .sh .bash .zsh .ps1 .sql .r .lua .pl .dart .tex .rst .srt .vtt .properties .proto .graphql .diff .patch")

var textMIMEs = strings.Fields("application/json application/xml application/yaml application/x-yaml application/toml application/x-toml application/javascript application/x-javascript application/ecmascript application/x-sh application/x-shellscript application/sql application/x-sql application/csv application/x-ndjson application/jsonl application/graphql application/x-tex application/x-latex application/x-python application/x-httpd-php")

type extractedText struct {
	text      string
	truncated bool
	unchecked string
}

type textKey struct {
	hash     [sha256.Size]byte
	format   string
	detached bool
}

// TextCache belongs to one request. Repeated instances, stages and attempts
// share extraction results and the 512 Ki UTF-16 budget, never other users.
// Its zero value is ready to use. It must not be copied after first use.
type TextCache struct {
	mu    sync.Mutex
	texts map[textKey]jsplugin.MediaText
	chars int
}

func textFormat(mediaType, filename string) string {
	mediaType, _, _ = mime.ParseMediaType(mediaType)
	mediaType = strings.ToLower(mediaType)
	extension := strings.ToLower(path.Ext(filename))
	switch {
	case strings.HasPrefix(mediaType, "image/"), strings.HasPrefix(mediaType, "audio/"), strings.HasPrefix(mediaType, "video/"):
		return ""
	case mediaType == "application/pdf", extension == ".pdf":
		return formatPDF
	case mediaType == docxMime, extension == ".docx":
		return formatDOCX
	case strings.HasPrefix(mediaType, "text/"), strings.HasSuffix(mediaType, "+json"), strings.HasSuffix(mediaType, "+xml"):
		return formatText
	}
	if slices.Contains(textMIMEs, mediaType) || slices.Contains(textExtensions, extension) {
		return formatText
	}
	return ""
}

func (c *TextCache) read(ctx context.Context, value item, admit MemoryAdmission) jsplugin.MediaText {
	format := textFormat(value.mime, value.filename)
	if format == "" {
		return jsplugin.MediaText{Unchecked: uncheckedUnsupported}
	}
	key := textKey{hash: value.digest, format: format, detached: value.open != nil}
	if value.open == nil {
		key.hash = sha256.Sum256([]byte(value.data))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		return jsplugin.MediaText{Unchecked: uncheckedLimit}
	}
	if result, ok := c.texts[key]; ok {
		return result
	}
	if c.chars >= 512<<10 || len(c.texts) >= 128 {
		return jsplugin.MediaText{Unchecked: uncheckedLimit}
	}
	limit := min(jsplugin.MaxMediaTextChars, (512<<10)-c.chars)
	var extracted extractedText
	if format == formatText {
		extracted = readAttachment(ctx, value, func(src io.Reader) extractedText { return readText(src, limit) })
	} else {
		extracted = readDocument(ctx, value, format, limit, admit)
	}
	if ctx.Err() != nil {
		return jsplugin.MediaText{Unchecked: uncheckedLimit}
	}
	result := jsplugin.MediaText{Text: extracted.text, Truncated: extracted.truncated, Unchecked: extracted.unchecked}
	if c.texts == nil {
		c.texts = map[textKey]jsplugin.MediaText{}
	}
	c.texts[key] = result
	for _, char := range result.Text {
		c.chars++
		if char > 0xffff {
			c.chars++
		}
	}
	return result
}

func readAttachment(ctx context.Context, value item, read func(io.Reader) extractedText) extractedText {
	if value.open == nil {
		return read(base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(value.data)))
	}
	if ctx.Err() != nil {
		return extractedText{unchecked: uncheckedLimit}
	}
	r, err := value.open(ctx)
	if r != nil {
		defer r.Close()
	}
	if err != nil || r == nil {
		return extractedText{unchecked: uncheckedUnreadable}
	}
	return read(io.LimitReader(attachmentReader{ctx: ctx, Reader: r}, int64(value.size)))
}

type attachmentReader struct {
	ctx context.Context
	io.Reader
}

func (r attachmentReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func readDocument(ctx context.Context, value item, format string, maxChars int, admit MemoryAdmission) extractedText {
	if value.size > maxDocumentBytes {
		return extractedText{unchecked: uncheckedTooLarge}
	}
	wait, cancelWait := context.WithTimeout(ctx, 5*time.Second)
	defer cancelWait()
	select {
	case documentSlots <- struct{}{}:
		defer func() { <-documentSlots }()
	case <-wait.Done():
		return extractedText{unchecked: uncheckedBusy}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return extractedText{unchecked: uncheckedTooLarge}
	}
	ctx, release, err := reserveWorkingMemory(ctx, documentMemory, documentBudget, admit)
	if err != nil {
		return extractedText{unchecked: uncheckedBusy}
	}
	defer release()
	return readAttachment(ctx, value, func(src io.Reader) extractedText {
		data, err := io.ReadAll(io.LimitReader(src, maxDocumentBytes+1))
		if err != nil || value.open != nil && len(data) != value.size {
			return extractedText{unchecked: uncheckedUnreadable}
		}
		if len(data) > maxDocumentBytes {
			return extractedText{unchecked: uncheckedTooLarge}
		}
		deadline, _ := ctx.Deadline()
		if format == formatDOCX {
			return docxTextContext(ctx, data, maxChars, deadline)
		}
		return pdfTextContext(ctx, data, maxChars, deadline)
	})
}
