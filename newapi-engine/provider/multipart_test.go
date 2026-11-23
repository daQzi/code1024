package provider

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

func memoryClientFile(field, filename, contentType string, data []byte) ClientFile {
	copied := bytes.Clone(data)
	return ClientFile{
		Field:       field,
		Filename:    filename,
		ContentType: contentType,
		Size:        int64(len(copied)),
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(copied)), nil
		},
	}
}

func TestBuildRequestFormIndexesDuplicateFieldsAndRewritesModel(t *testing.T) {
	fields := map[string][]string{"model": {"public-img"}, "prompt": {"draw a cat"}}
	files := []ClientFile{
		memoryClientFile("image", "a.png", "image/png", []byte("png-a")),
		memoryClientFile("image", "b.png", "image/png", []byte("png-b")),
		memoryClientFile("mask", "m.png", "image/png", []byte("mask")),
	}
	form, indexed := BuildRequestForm(fields, files, "upstream-img")
	if form.Fields["model"][0] != "upstream-img" {
		t.Fatalf("expected rewritten model, got %v", form.Fields["model"])
	}
	if fields["model"][0] != "public-img" {
		t.Fatalf("caller map was mutated")
	}
	if len(form.Files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(form.Files))
	}
	for _, ref := range []string{"image", "image#1", "mask"} {
		if _, ok := indexed[ref]; !ok {
			t.Fatalf("missing indexed ref %q", ref)
		}
	}
}

func TestBuildUpstreamBodyFormAndRawFileAndJSON(t *testing.T) {
	_, files := BuildRequestForm(nil, []ClientFile{
		memoryClientFile("image", "cat.png", "image/png", []byte("PNGDATA")),
	}, "")

	// 1. Multipart form output
	formStep := jsplugin.ConvertedRequest{
		Form: &jsplugin.ConvertedForm{
			Fields: map[string][]string{"prompt": {"hello"}, "model": {"m1"}},
			Files:  []jsplugin.ConvertedFormFile{{Field: "file", From: "image", Filename: "renamed.png"}},
		},
	}
	body, ct, formOrFile, err := BuildUpstreamBody(formStep, files, true, 1<<20)
	if err != nil {
		t.Fatalf("BuildUpstreamBody form failed: %v", err)
	}
	if !formOrFile {
		t.Fatalf("expected formOrFile=true")
	}
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		t.Fatalf("unexpected content-type: %q (%v)", ct, err)
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	parsed, err := mr.ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("ReadForm failed: %v", err)
	}
	defer func() { _ = parsed.RemoveAll() }()
	if got := parsed.Value["prompt"]; len(got) != 1 || got[0] != "hello" {
		t.Fatalf("unexpected prompt field: %v", got)
	}
	fh := parsed.File["file"][0]
	if fh.Filename != "renamed.png" || fh.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("unexpected file metadata: name=%q ct=%q", fh.Filename, fh.Header.Get("Content-Type"))
	}
	f, _ := fh.Open()
	gotBytes, _ := io.ReadAll(f)
	_ = f.Close()
	if string(gotBytes) != "PNGDATA" {
		t.Fatalf("unexpected file bytes: %q", string(gotBytes))
	}

	// 2. Raw file output
	rawStep := jsplugin.ConvertedRequest{File: "image"}
	rawBody, rawCT, rawFlag, err := BuildUpstreamBody(rawStep, files, true, 1<<20)
	if err != nil || !rawFlag || string(rawBody) != "PNGDATA" || rawCT != "image/png" {
		t.Fatalf("unexpected raw file result: body=%q ct=%q flag=%v err=%v", rawBody, rawCT, rawFlag, err)
	}

	// 3. JSON body from multipart request gets default application/json
	jsonStep := jsplugin.ConvertedRequest{Body: []byte(`{"prompt":"hi"}`)}
	jb, jct, jFlag, err := BuildUpstreamBody(jsonStep, files, true, 1<<20)
	if err != nil || jFlag || jct != "application/json" || string(jb) != `{"prompt":"hi"}` {
		t.Fatalf("unexpected json result: body=%q ct=%q flag=%v err=%v", jb, jct, jFlag, err)
	}

	// 4. Unknown file reference fails closed
	if _, _, _, err := BuildUpstreamBody(jsplugin.ConvertedRequest{File: "missing"}, files, true, 1<<20); err == nil {
		t.Fatalf("expected error for unknown file reference")
	}

	// 5. Max bytes enforcement
	if _, _, _, err := BuildUpstreamBody(rawStep, files, true, 4); err == nil {
		t.Fatalf("expected size limit error")
	}
}

func TestReplaceFileMarkersBase64AndDataURLAndFailClosed(t *testing.T) {
	_, files := BuildRequestForm(nil, []ClientFile{
		memoryClientFile("image", "a.png", "image/png", []byte("abc")),
	}, "")
	input := []byte(`{"raw":{"$file":"image","encoding":"base64"},"uri":{"$file":"image","encoding":"dataUrl"},"keep":123}`)
	out, err := ReplaceFileMarkers(input, files, 1<<20)
	if err != nil {
		t.Fatalf("ReplaceFileMarkers failed: %v", err)
	}
	wantB64 := base64.StdEncoding.EncodeToString([]byte("abc"))
	want := `{"raw":"` + wantB64 + `","uri":"data:image/png;base64,` + wantB64 + `","keep":123}`
	if string(out) != want {
		t.Fatalf("got %s, want %s", out, want)
	}

	// Fails closed when files is nil
	if _, err := ReplaceFileMarkers(input, nil, 1<<20); err == nil || !strings.Contains(err.Error(), "multipart") {
		t.Fatalf("expected multipart requirement error, got %v", err)
	}
	// Fails closed on unknown ref
	if _, err := ReplaceFileMarkers([]byte(`{"x":{"$file":"nope","encoding":"base64"}}`), files, 1<<20); err == nil {
		t.Fatalf("expected unknown ref error")
	}
	// Fails closed on malformed marker (extra field or invalid encoding)
	if _, err := ReplaceFileMarkers([]byte(`{"x":{"$file":"image","encoding":"hex"}}`), files, 1<<20); err == nil {
		t.Fatalf("expected invalid encoding error")
	}
	if _, err := ReplaceFileMarkers([]byte(`{"x":{"$file":"image","encoding":"base64","extra":1}}`), files, 1<<20); err == nil {
		t.Fatalf("expected malformed marker error")
	}
	// Fails closed on size overflow
	if _, err := ReplaceFileMarkers(input, files, 20); err == nil {
		t.Fatalf("expected size overflow error")
	}
}
