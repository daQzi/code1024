package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/tidwall/gjson"
)

// Follow the pinned streamConverter body/chunks contract without interpreting
// vendor deltas in the gateway. The plugin returns the final standard JSON;
// the existing Chat/Responses handlers validate it and account for it once.
// The caller holds the shared Provider memory reservation throughout this read.
func (s *NewAPIPluginService) collectProviderStreamJSON(ctx context.Context, exchange *newAPIProviderExchange, resp *http.Response, sse bool) error {
	stream := newNewAPIProviderStream(ctx, s, exchange, resp.Body, sse)
	defer stream.Close()
	var chunks bytes.Buffer
	chunked := false
	contentType := ""
	var body []byte
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		event, readErr := stream.next()
		if readErr != nil && readErr != io.EOF {
			return readErr // Never invoke the clean-EOF hook after transport failure.
		}
		done := readErr == io.EOF
		output, err := stream.convertResult(event, done)
		if err != nil {
			var rejected *jsplugin.UpstreamError
			if errors.As(err, &rejected) {
				if stream.events > 1 && exchange.request != nil && exchange.request.Method != http.MethodGet && exchange.request.Method != http.MethodHead {
					exchange.credentialReplayUnsafe = true
				}
				status := rejected.Status
				if status < 400 || status > 599 {
					status = http.StatusBadGateway
				}
				resp.StatusCode = status
				resp.Header = resp.Header.Clone()
				resp.Header.Del("Content-Length")
				resp.Header.Del("Content-Encoding")
				resp.Header.Set("Content-Type", "application/json")
				body := []byte(`{"error":{"message":"Provider stream rejected the request","type":"upstream_error"}}`)
				resp.Body = io.NopCloser(bytes.NewReader(body))
				resp.ContentLength = int64(len(body))
				return nil
			}
			return err
		}
		if len(output.Events) > 0 {
			return errors.New("Provider JSON clients require a body or chunks, not events")
		}
		if output.HasBody {
			if chunked {
				return errors.New("Provider body cannot follow chunks")
			}
			body, contentType = output.Body, output.ContentType
			if contentType == "" {
				contentType = "application/json"
			}
			break // The baseline's whole-body result ends and closes the upstream.
		}
		if len(output.Chunks) > 0 {
			if !chunked {
				contentType, chunked = output.ContentType, true
			}
			// This public entry only serves Chat/Responses JSON, so binary
			// chunks are still unsupported even though the typed codec has them.
			if !newAPIProviderJSONContentType(contentType) {
				return errors.New("Provider collected chunks must keep a JSON content type")
			}
			for _, chunk := range output.Chunks {
				if len(chunk) > MaxRequestPluginBody-chunks.Len() {
					return errors.New("Provider collected body exceeds limit")
				}
				chunks.Write(chunk)
			}
		}
		if done {
			if !chunked {
				return errors.New("Provider stream ended without a JSON body")
			}
			body = chunks.Bytes()
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !newAPIProviderJSONContentType(contentType) || len(body) > MaxRequestPluginBody || !utf8.Valid(body) || !gjson.ValidBytes(body) || !newAPIProviderUnambiguousJSON(gjson.ParseBytes(body), 0) {
		return errors.New("Provider collected response must be bounded unambiguous JSON")
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.Header = resp.Header.Clone()
	resp.Header.Del("Content-Length")
	resp.Header.Del("Content-Encoding")
	resp.Header.Set("Content-Type", "application/json")
	resp.ContentLength = int64(len(body))
	return nil
}
