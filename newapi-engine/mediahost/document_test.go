// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// Adapted from QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f, relay/requestplugin/.
// See ../NOTICE.md for provenance and host resource boundaries.

package mediahost

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pdfWriter writes a crafted PDF: numbered objects, then cross-reference
// sections.
type pdfWriter struct {
	bytes.Buffer
	offsets map[int]int
}

func newPDFWriter() *pdfWriter {
	writer := &pdfWriter{offsets: map[int]int{}}
	writer.WriteString("%PDF-1.5\n")
	return writer
}

func (w *pdfWriter) object(number int, body string) {
	w.offsets[number] = w.Len()
	fmt.Fprintf(w, "%d 0 obj\n%s\nendobj\n", number, body)
}

// rows are the cross-reference stream rows of the objects written in the
// file, and the free head of the list.
func (w *pdfWriter) rows(numbers ...int) map[int][3]int64 {
	rows := map[int][3]int64{0: {0, 0, 65535}}
	for _, number := range numbers {
		rows[number] = [3]int64{1, int64(w.offsets[number]), 0}
	}
	return rows
}

// xref writes a cross-reference stream of rows as object number and returns
// its offset. encode, when set, encodes the rows.
func (w *pdfWriter) xref(number int, rows map[int][3]int64, dictionary string, encode func([]byte) []byte) int {
	offset := w.Len()
	numbers := slices.Sorted(maps.Keys(rows))
	var index strings.Builder
	var data []byte
	for _, n := range numbers {
		fmt.Fprintf(&index, "%d 1 ", n)
		row := rows[n]
		data = append(data, byte(row[0]), byte(row[1]>>24), byte(row[1]>>16), byte(row[1]>>8), byte(row[1]), byte(row[2]>>8), byte(row[2]))
	}
	if encode != nil {
		data = encode(data)
	}
	fmt.Fprintf(w, "%d 0 obj\n<< /Type /XRef /Size %d /W [1 4 2] /Index [%s] /Root 1 0 R %s /Length %d >>\nstream\n", number, numbers[len(numbers)-1]+1, index.String(), dictionary, len(data))
	w.Write(data)
	w.WriteString("\nendstream\nendobj\n")
	return offset
}

// table writes a cross-reference table of rows ("offset generation n" or
// "... f" by object number) and its trailer, and returns its offset.
func (w *pdfWriter) table(rows map[int]string, trailer string) int {
	offset := w.Len()
	w.WriteString("xref\n")
	for _, number := range slices.Sorted(maps.Keys(rows)) {
		fmt.Fprintf(w, "%d 1\n%s \n", number, rows[number])
	}
	fmt.Fprintf(w, "trailer\n<< /Size 10 /Root 1 0 R %s >>\n", trailer)
	return offset
}

func (w *pdfWriter) finish(startxref int) []byte {
	fmt.Fprintf(w, "startxref\n%d\n%%%%EOF\n", startxref)
	return w.Bytes()
}

// pages writes objects 1 to 3: the catalog, the page tree and one page whose
// contents are object 4 and whose font F1 is object 5.
func (w *pdfWriter) pages() {
	w.object(1, "<< /Type /Catalog /Pages 2 0 R >>")
	w.object(2, pagesObject)
	w.object(3, pageObject)
}

const (
	pagesObject = "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"
	pageObject  = "<< /Type /Page /Parent 2 0 R /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>"
	fontObject  = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"
)

func pdfStream(dictionary, data string) string {
	return fmt.Sprintf("<< %s /Length %d >>\nstream\n%s\nendstream", dictionary, len(data), data)
}

func textContent(text string) string {
	return "BT /F1 12 Tf 72 712 Td (" + text + ") Tj ET"
}

// objectStreamData is the data of an object stream holding objects under
// numbers, and the offset of the first object in it.
func objectStreamData(numbers []int, objects []string) (string, int) {
	var header, body strings.Builder
	for index, object := range objects {
		fmt.Fprintf(&header, "%d %d ", numbers[index], body.Len())
		body.WriteString(object + "\n")
	}
	return header.String() + body.String(), header.Len()
}

// objectStream holds objects under numbers; dictionary adds members.
func objectStream(dictionary string, numbers []int, objects ...string) string {
	data, first := objectStreamData(numbers, objects)
	return pdfStream(fmt.Sprintf("/Type /ObjStm /N %d /First %d %s", len(objects), first, dictionary), data)
}

func flateBytes(data []byte) []byte {
	var out bytes.Buffer
	writer := zlib.NewWriter(&out)
	_, _ = writer.Write(data)
	_ = writer.Close()
	return out.Bytes()
}

// pngUp encodes data as PNG Up rows of columns bytes, the last padded with
// zeros.
func pngUp(data []byte, columns int) []byte {
	var out []byte
	previous := make([]byte, columns)
	for start := 0; start < len(data); start += columns {
		row := make([]byte, columns)
		copy(row, data[start:])
		out = append(out, 2)
		for index := range row {
			out = append(out, row[index]-previous[index])
		}
		previous = row
	}
	return out
}

// The cross-reference stream of most PDF writers: compressed rows behind the
// PNG Up predictor.
const xrefPredictor = "/Filter /FlateDecode /DecodeParms << /Predictor 12 /Columns 7 >>"

func xrefRows(data []byte) []byte { return flateBytes(pngUp(data, 7)) }

// A stream is decoded only when the filters and decode parameters the
// library reads for it, through references too, stay within the bounds:
// a buffer sized by predictor columns, or a chain of filters. A stream past
// them is left out, and the file is too large to check or its text is
// truncated.
func TestPDFStreamDecodeBounds(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	content := string(flateBytes([]byte(textContent("Hidden"))))
	chain := strings.Repeat("/FlateDecode ", maxPDFFilters+1)
	predicted := string(flateBytes(pngUp([]byte(textContent("Rows")), 4)))
	for _, test := range []struct {
		name     string
		contents string
		extra    string
		want     extractedText
	}{
		{"indirect parameters", pdfStream("/Filter /FlateDecode /DecodeParms 6 0 R", content), "<< /Predictor 12 /Columns 1048576 >>", extractedText{unchecked: uncheckedTooLarge}},
		{"indirect columns", pdfStream("/Filter /FlateDecode /DecodeParms << /Predictor 12 /Columns 6 0 R >>", content), "1048576", extractedText{unchecked: uncheckedTooLarge}},
		{"parameters in an array", pdfStream("/Filter [/FlateDecode] /DecodeParms [6 0 R]", content), "<< /Predictor 12 /Columns 1048576 >>", extractedText{unchecked: uncheckedTooLarge}},
		{"long filter chain", pdfStream("/Filter ["+chain+"]", content), "null", extractedText{unchecked: uncheckedTooLarge}},
		{"predictor within bounds", pdfStream("/Filter /FlateDecode /DecodeParms 6 0 R", predicted), "<< /Predictor 12 /Columns 4 >>", extractedText{text: "Rows"}},
		{"one part past the bounds", "[7 0 R 8 0 R]", "<< /Predictor 12 /Columns 1048576 >>", extractedText{text: "Shown", truncated: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := newPDFWriter()
			writer.pages()
			writer.object(4, test.contents)
			writer.object(5, fontObject)
			writer.object(6, test.extra)
			writer.object(7, pdfStream("", textContent("Shown")))
			writer.object(8, pdfStream("/Filter /FlateDecode /DecodeParms 6 0 R", content))
			data := writer.finish(writer.xref(9, writer.rows(1, 2, 3, 4, 5, 6, 7, 8), "", nil))
			assert.Equal(t, test.want, pdfText(data, 100, deadline))
		})
	}
}

// checkPDF builds the cross-reference table the library builds and holds
// every object stream the library may read objects from to bounds: /N within
// its data, decode parameters within bounds through references, no reference
// that would make the library read another object stream first, and no
// object stream that lives in another.
func TestCheckPDFObjectStreams(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	const huge = "9223372036854775807"
	pages := objectStream("", []int{2, 3, 5}, pagesObject, pageObject, fontObject)
	write := func(stream string, extra map[int]string, rows map[int][3]int64) []byte {
		writer := newPDFWriter()
		writer.object(1, "<< /Type /Catalog /Pages 2 0 R >>")
		writer.object(4, pdfStream("", textContent("Hello")))
		writer.object(6, stream)
		numbers := []int{1, 4, 6}
		for number, body := range extra {
			writer.object(number, body)
			numbers = append(numbers, number)
		}
		all := writer.rows(numbers...)
		maps.Copy(all, map[int][3]int64{2: {2, 6, 0}, 3: {2, 6, 1}, 5: {2, 6, 2}})
		maps.Copy(all, rows)
		return writer.finish(writer.xref(7, all, xrefPredictor, xrefRows))
	}

	// Objects read out of a compressed object stream, over a predicted
	// cross-reference stream, read as usual.
	valid := write(pages, nil, nil)
	assert.Equal(t, "", checkPDF(valid, deadline))
	assert.Equal(t, extractedText{text: "Hello"}, pdfText(valid, 100, deadline))
	compressed := func(dictionary string) string {
		data, first := objectStreamData([]int{2, 3, 5}, []string{pagesObject, pageObject, fontObject})
		return pdfStream(fmt.Sprintf("/Type /ObjStm /N 3 /First %d /Filter /FlateDecode %s", first, dictionary), string(flateBytes([]byte(data))))
	}
	assert.Equal(t, extractedText{text: "Hello"}, pdfText(write(compressed(""), nil, nil), 100, deadline))

	for _, test := range []struct {
		name   string
		stream string
		extra  map[int]string
		rows   map[int][3]int64
		want   string
	}{
		{"count past the data", objectStream("/N "+huge, []int{2, 3, 5}, pagesObject, pageObject, fontObject), nil, nil, uncheckedTooLarge},
		{"indirect count", objectStream("/N 9 0 R", []int{2, 3, 5}, pagesObject, pageObject, fontObject), map[int]string{9: huge}, nil, uncheckedTooLarge},
		{"length that resolves to nothing", "<< /Type /ObjStm /N 3 /First 4 /Length 99 0 R >>\nstream\n2 0 \nendstream", nil, nil, uncheckedTooLarge},
		{"indirect predictor columns", compressed("/DecodeParms 9 0 R"), map[int]string{9: "<< /Predictor 12 /Columns 1099511627776 >>"}, nil, uncheckedTooLarge},
		{"extends a stream past its count", objectStream("/Extends 9 0 R", []int{8}, "null"), map[int]string{9: objectStream("/N "+huge, []int{2}, pagesObject)}, nil, uncheckedTooLarge},
		{"count held in the stream itself", objectStream("/N 8 0 R", []int{2, 3, 5, 8}, pagesObject, pageObject, fontObject, "3"), nil, map[int][3]int64{8: {2, 6, 3}}, uncheckedUnreadable},
		{"object stream in an object stream", pages, map[int]string{8: objectStream("", []int{6}, "null")}, map[int][3]int64{6: {2, 8, 0}, 8: {2, 6, 3}}, uncheckedUnreadable},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, checkPDF(write(test.stream, test.extra, test.rows), deadline))
		})
	}

	// A newer section's row of an unknown type leaves the older row of the
	// object stream in the library's table.
	writer := newPDFWriter()
	writer.object(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writer.object(6, objectStream("/N "+huge, []int{2}, pagesObject))
	older := writer.rows(1, 6)
	older[2] = [3]int64{2, 6, 0}
	previous := writer.xref(7, older, "", nil)
	hidden := writer.finish(writer.xref(8, map[int][3]int64{6: {3, 0, 0}}, fmt.Sprintf("/Size 9 /Prev %d", previous), nil))
	assert.Equal(t, uncheckedTooLarge, checkPDF(hidden, deadline))

	// A newer table's free row leaves the older row in the library's table
	// too, so the object it places is held to the value bound.
	writer = newPDFWriter()
	writer.pages()
	writer.object(4, pdfStream("", textContent("Hello")))
	writer.object(5, fontObject)
	writer.object(6, "["+strings.Repeat("0 ", maxPDFObjectValues)+"]")
	rows := map[int]string{0: "0000000000 65535 f"}
	for number := 1; number <= 6; number++ {
		rows[number] = fmt.Sprintf("%010d 00000 n", writer.offsets[number])
	}
	previous = writer.table(rows, "")
	freed := writer.finish(writer.table(map[int]string{6: "0000000000 65535 f"}, fmt.Sprintf("/Prev %d", previous)))
	assert.Equal(t, uncheckedTooLarge, checkPDF(freed, deadline))
}

// A cross-reference stream is decoded as the library decodes it: by the
// predictor columns of its decode parameters, held to the column bound, and
// with three field widths.
func TestCheckPDFCrossReferenceStream(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	for _, test := range []struct {
		name       string
		dictionary string
		want       string
	}{
		{"predicted rows", xrefPredictor, ""},
		{"columns past the bound", "/Filter /FlateDecode /DecodeParms << /Predictor 12 /Columns 1099511627776 >>", uncheckedTooLarge},
		{"extra field widths", xrefPredictor + " /W [1 4 2 8 8 8]", uncheckedUnreadable},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := newPDFWriter()
			writer.pages()
			writer.object(4, pdfStream("", textContent("Hello")))
			writer.object(5, fontObject)
			data := writer.finish(writer.xref(6, writer.rows(1, 2, 3, 4, 5), test.dictionary, xrefRows))
			assert.Equal(t, test.want, checkPDF(data, deadline))
		})
	}
}

// A ToUnicode map is read in bounded work: a range expands to at most
// maxPDFCMapCodes codes in all, whether or not its target names text, and
// decoding stops at the deadline.
func TestPDFToUnicodeBounds(t *testing.T) {
	cmap := parseToUnicode([]byte(`1 begincodespacerange <00> <FF> endcodespacerange
2 beginbfrange <00000000> <FFFFFFFF> <> <00000000> <FFFFFFFF> [] endbfrange
1 beginbfchar <41> <0042> endbfchar`), &pdfSource{deadline: time.Now().Add(time.Minute)})
	require.NotNil(t, cmap)
	assert.Equal(t, "BB", cmap.decode("AA"))

	cmap.source = &pdfSource{deadline: time.Now().Add(-time.Second)}
	decoded := cmap.decode(strings.Repeat("A", 1<<20))
	assert.Less(t, len(decoded), 1<<20)
	assert.True(t, cmap.source.expired)
}

func documentPDF(text, trailer string) []byte {
	writer := newPDFWriter()
	writer.pages()
	writer.object(4, pdfStream("", textContent(text)))
	writer.object(5, fontObject)
	return writer.finish(writer.table(map[int]string{
		0: "0000000000 65535 f",
		1: fmt.Sprintf("%010d 00000 n", writer.offsets[1]),
		2: fmt.Sprintf("%010d 00000 n", writer.offsets[2]),
		3: fmt.Sprintf("%010d 00000 n", writer.offsets[3]),
		4: fmt.Sprintf("%010d 00000 n", writer.offsets[4]),
		5: fmt.Sprintf("%010d 00000 n", writer.offsets[5]),
	}, trailer))
}

func documentDOCX(t *testing.T, xml string, duplicates bool) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	count := 1
	if duplicates {
		count = 2
	}
	for range count {
		file, err := writer.Create("word/document.xml")
		require.NoError(t, err)
		_, err = file.Write([]byte(xml))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return body.Bytes()
}

func documentSource(t *testing.T, data []byte, mimeType, filename string, cache *TextCache) *Source {
	t.Helper()
	body, err := json.Marshal(map[string]any{"file": map[string]string{"filename": filename, "file_data": "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)}})
	require.NoError(t, err)
	source, err := NewWithCache(body, cache)
	require.NoError(t, err)
	return source
}

func TestDocumentMediaReadAndImmutableOriginal(t *testing.T) {
	xml := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Hello</w:t><w:tab/><w:t>😀</w:t></w:r></w:p><w:p><w:r><w:t>World</w:t></w:r></w:p></w:body></w:document>`
	for _, test := range []struct {
		name, mime, filename, want string
		data                       []byte
	}{
		{"pdf", "application/pdf", "private.pdf", "Hello PDF", documentPDF("Hello PDF", "")},
		{"pdf extension", "application/octet-stream", "private.PDF", "Hello PDF", documentPDF("Hello PDF", "")},
		{"docx", docxMime, "private.docx", "Hello\t😀\nWorld", documentDOCX(t, xml, false)},
		{"docx extension", "application/octet-stream", "private.DOCX", "Hello\t😀\nWorld", documentDOCX(t, xml, false)},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := documentSource(t, test.data, test.mime, test.filename, nil)
			info, ok := source.Media("[media:0]")
			require.True(t, ok)
			require.Equal(t, "document", info.Kind)
			require.False(t, info.Rewritable)
			text, ok := source.MediaText(context.Background(), "[media:0]", 100)
			require.True(t, ok)
			require.Equal(t, jsplugin.MediaText{Text: test.want}, text)
			text, _ = source.MediaText(context.Background(), "[media:0]", 3)
			require.Equal(t, jsplugin.MediaText{Text: "Hel", Truncated: true}, text)
			prepared, err := source.PrepareEffects(context.Background(), []requestpatch.Effect{{Type: requestpatch.EffectMediaText, Media: 0, Text: "replacement"}})
			require.NoError(t, err)
			require.Empty(t, prepared.Effects)
			prepared.Commit()
			records, _ := source.Records()
			require.Equal(t, "not_rewritable", records[0].Reason)
			require.False(t, records[0].Rewritten)
			audit, err := json.Marshal(records)
			require.NoError(t, err)
			require.NotContains(t, string(audit), "private")
			require.NotContains(t, string(audit), "Hello")
		})
	}
}

func TestDocumentTextFormatsAndInvalidEncoding(t *testing.T) {
	for _, test := range []struct {
		name, mime, filename string
		data                 []byte
		want                 jsplugin.MediaText
	}{
		{"utf8 BOM", "text/plain; charset=utf-8", "", []byte("\xEF\xBB\xBFhéllo"), jsplugin.MediaText{Text: "héllo"}},
		{"utf16 LE", "text/plain", "", []byte{0xFF, 0xFE, 'h', 0, 'i', 0, 0x3D, 0xD8, 0, 0xDE}, jsplugin.MediaText{Text: "hi😀"}},
		{"utf16 BE", "text/plain", "", []byte{0xFE, 0xFF, 0, 'h', 0, 'i'}, jsplugin.MediaText{Text: "hi"}},
		{"filename fallback", "application/octet-stream", "notes.md", []byte("hello"), jsplugin.MediaText{Text: "hello"}},
		{"json suffix", "application/problem+json", "", []byte(`{"ok":true}`), jsplugin.MediaText{Text: `{"ok":true}`}},
		{"binary", "text/plain", "", []byte("a\x00b"), jsplugin.MediaText{Unchecked: "unreadable"}},
		{"invalid UTF8", "text/plain", "", []byte{0xff, 'a'}, jsplugin.MediaText{Unchecked: "unreadable"}},
		{"unpaired UTF16", "text/plain", "", []byte{0xff, 0xfe, 0x3d, 0xd8}, jsplugin.MediaText{Unchecked: "unreadable"}},
		{"invalid UTF16 pair", "text/plain", "", []byte{0xff, 0xfe, 0x3d, 0xd8, 'a', 0}, jsplugin.MediaText{Unchecked: "unreadable"}},
		{"media excludes extension", "image/png", "notes.txt", []byte("text"), jsplugin.MediaText{Unchecked: "unsupported"}},
		{"unknown", "application/octet-stream", "blob.bin", []byte("text"), jsplugin.MediaText{Unchecked: "unsupported"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := documentSource(t, test.data, test.mime, test.filename, nil)
			text, ok := source.MediaText(context.Background(), "[media:0]", 100)
			require.True(t, ok)
			require.Equal(t, test.want, text)
		})
	}
}

func TestDocumentDamageEncryptionNoTextAndCancellation(t *testing.T) {
	xml := `<w:document xmlns:w="urn:word"><w:body><w:p><w:r><w:t>hello</w:t></w:r></w:p></w:body></w:document>`
	for _, test := range []struct {
		name, mime, reason string
		data               []byte
	}{
		{"encrypted", "application/pdf", "unreadable", documentPDF("secret", "/Encrypt 6 0 R")},
		{"PDF without text", "application/pdf", "no_text", documentPDF("", "")},
		{"damaged PDF", "application/pdf", "unreadable", []byte("%PDF-1.4\nnot a PDF")},
		{"damaged DOCX", docxMime, "unreadable", []byte("not a ZIP")},
		{"DOCX without text", docxMime, "no_text", documentDOCX(t, `<document/>`, false)},
		{"ambiguous DOCX", docxMime, "unreadable", documentDOCX(t, xml, true)},
		{"XML too deep", docxMime, "unreadable", documentDOCX(t, strings.Repeat("<x>", maxDOCXDepth+1)+strings.Repeat("</x>", maxDOCXDepth+1), false)},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := documentSource(t, test.data, test.mime, "", nil)
			text, _ := source.MediaText(context.Background(), "[media:0]", 100)
			require.Equal(t, jsplugin.MediaText{Unchecked: test.reason}, text)
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.Equal(t, uncheckedTooLarge, pdfTextContext(canceled, documentPDF("secret", ""), 100, time.Now().Add(time.Minute)).unchecked)
	require.Equal(t, uncheckedTooLarge, docxTextContext(canceled, documentDOCX(t, xml, false), 100, time.Now().Add(time.Minute)).unchecked)
	require.Equal(t, uncheckedTooLarge, readDocument(context.Background(), item{size: maxDocumentBytes + 1}, formatPDF, 100, nil).unchecked)
	for range cap(documentSlots) {
		documentSlots <- struct{}{}
	}
	short, cancelShort := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelShort()
	require.Equal(t, uncheckedBusy, readDocument(short, item{}, formatPDF, 100, nil).unchecked)
	for range cap(documentSlots) {
		<-documentSlots
	}
	budget := &memoryBudget{limit: documentMemory - 1}
	value := item{mime: "application/pdf", data: base64.StdEncoding.EncodeToString(documentPDF("secret", ""))}
	require.Equal(t, uncheckedBusy, readDocument(context.Background(), value, formatPDF, 100, testMemoryAdmission(budget)).unchecked)
	release, err := budget.reserve(documentMemory - 1)
	require.NoError(t, err)
	release()
}

func TestDocumentTextCacheSharedAcrossInstancesAndBounded(t *testing.T) {
	cache := &TextCache{}
	data := documentPDF("Hello PDF", "")
	var pending sync.WaitGroup
	for range 8 {
		source := documentSource(t, data, "application/pdf", "", cache)
		pending.Go(func() {
			text, ok := source.MediaText(context.Background(), "[media:0]", 100)
			assert.True(t, ok)
			assert.Equal(t, "Hello PDF", text.Text)
		})
	}
	pending.Wait()
	require.Len(t, cache.texts, 1)
	require.Equal(t, 9, cache.chars)
	other := documentSource(t, data, "application/pdf", "", nil)
	require.Empty(t, other.textCache.texts)
	cache = &TextCache{}
	for i := range 4 {
		data := []byte(strings.Repeat(string(rune('a'+i)), jsplugin.MaxMediaTextChars))
		source := documentSource(t, data, "text/plain", "", cache)
		text, _ := source.MediaText(context.Background(), "[media:0]", 1)
		require.Equal(t, string(rune('a'+i)), text.Text)
		require.True(t, text.Truncated)
	}
	require.Equal(t, 512<<10, cache.chars)
	source := documentSource(t, []byte("extra"), "text/plain", "", cache)
	text, _ := source.MediaText(context.Background(), "[media:0]", 100)
	require.Equal(t, uncheckedLimit, text.Unchecked)
}
