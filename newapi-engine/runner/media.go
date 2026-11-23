package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf16"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

const maxMediaCallbacks = 128

// Only metadata and bounded text cross RPC. FetchBody always runs in the
// parent against the source attached to this call, never against child input
// naming a file, process, credential or another request.
type mediaRequest struct {
	Operation string `json:"operation"`
	Ref       string `json:"ref,omitempty"`
	MaxChars  int    `json:"max_chars,omitempty"`
}

type mediaResult struct {
	Attachments []jsplugin.Attachment `json:"attachments,omitempty"`
	Info        *jsplugin.MediaInfo   `json:"info,omitempty"`
	Text        *jsplugin.MediaText   `json:"text,omitempty"`
}

type mediaSourceKey struct{}

type remoteMedia struct {
	call func(mediaRequest) (*mediaResult, error)
	err  error
}

func fetchWithMedia(ctx context.Context, fetcher jsplugin.Fetcher, source jsplugin.MediaSource, req jsplugin.FetchRequest) (jsplugin.FetchResponse, error) {
	if source != nil && req.Body != "" {
		var err error
		req.BodyStream, req.BodySize, err = source.FetchBody(req.Body)
		if closer, ok := req.BodyStream.(io.Closer); ok {
			defer closer.Close()
		}
		if err != nil || req.BodyStream != nil && (req.BodySize < 0 || req.BodySize > 32<<20) {
			return jsplugin.FetchResponse{}, errors.New("media fetch body unavailable or too large")
		}
	}
	return fetcher(ctx, req)
}

func (m *remoteMedia) ask(req mediaRequest) *mediaResult {
	if m.err != nil {
		return &mediaResult{}
	}
	var result *mediaResult
	result, m.err = m.call(req)
	if result == nil {
		return &mediaResult{}
	}
	return result
}

func (m *remoteMedia) Attachments() []jsplugin.Attachment {
	return m.ask(mediaRequest{Operation: "attachments"}).Attachments
}

func (m *remoteMedia) Media(ref string) (jsplugin.MediaInfo, bool) {
	value := m.ask(mediaRequest{Operation: "media", Ref: ref}).Info
	if value == nil {
		return jsplugin.MediaInfo{}, false
	}
	return *value, true
}

func (m *remoteMedia) MediaText(ctx context.Context, ref string, maxChars int) (jsplugin.MediaText, bool) {
	if err := ctx.Err(); err != nil {
		m.err = err
		return jsplugin.MediaText{}, false
	}
	value := m.ask(mediaRequest{Operation: "text", Ref: ref, MaxChars: maxChars}).Text
	if value == nil {
		return jsplugin.MediaText{}, false
	}
	return *value, true
}

func (m *remoteMedia) FetchBody(string) (io.Reader, int64, error) {
	// A reader cannot cross JSON RPC. The parent expands exact media refs
	// when it receives the normal fetch request from this same hook.
	return nil, 0, m.err
}

func readMedia(ctx context.Context, source jsplugin.MediaSource, req mediaRequest) (*mediaResult, error) {
	if source == nil || len(req.Ref) > 64 || ctx.Err() != nil {
		return nil, errors.New("media source unavailable")
	}
	result := &mediaResult{}
	switch req.Operation {
	case "attachments":
		result.Attachments = source.Attachments()
		if len(result.Attachments) > 64 {
			return nil, errors.New("media attachment limit exceeded")
		}
	case "media":
		if value, ok := source.Media(req.Ref); ok {
			result.Info = &value
		}
	case "text":
		if req.MaxChars < 1 || req.MaxChars > jsplugin.MaxMediaTextChars {
			return nil, errors.New("invalid media text limit")
		}
		if value, ok := source.MediaText(ctx, req.Ref, req.MaxChars); ok {
			if len(value.Text) > 4*req.MaxChars || len(utf16.Encode([]rune(value.Text))) > req.MaxChars {
				return nil, errors.New("media text exceeds requested limit")
			}
			result.Text = &value
		}
	default:
		return nil, errors.New("unknown media operation")
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1<<20 || req.Operation != "text" && len(encoded) > 128<<10 {
		return nil, errors.New("media metadata exceeds limit")
	}
	return result, ctx.Err()
}
