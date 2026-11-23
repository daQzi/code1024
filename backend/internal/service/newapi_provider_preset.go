package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
)

// Presets surround, rather than replace, the vendor hooks. A plugin on a
// composed route sees the preset's upstream DTO and returns that protocol;
// RelayKit then converts the final response into the original public format.
func (s *NewAPIPluginService) convertProviderPresetResponse(ctx context.Context, exchange *newAPIProviderExchange, resp *http.Response) error {
	if exchange.stream {
		if !isEventStreamResponse(resp.Header) {
			return errors.New("preset Provider requires an SSE upstream response")
		}
		stream := newNewAPIProviderStream(ctx, s, exchange, resp.Body, true)
		stream.preset = exchange.protocol
		resp.Body = stream
		resp.ContentLength = -1
	} else {
		if !newAPIProviderJSONContentType(resp.Header.Get("Content-Type")) {
			return errors.New("preset Provider requires a JSON upstream response")
		}
		body := resp.Body
		defer func() { _ = body.Close() }()
		raw, err := io.ReadAll(io.LimitReader(body, MaxRequestPluginBody+1))
		if err != nil || len(raw) > MaxRequestPluginBody {
			return errors.New("preset Provider response read failed or exceeds limit")
		}
		converted, err := exchange.protocol.JSON(ctx, raw)
		if err != nil {
			return err
		}
		if err := exchange.tools.observeVendor(exchange.protocol, raw, false); err != nil {
			return err
		}
		resp.Body = io.NopCloser(bytes.NewReader(converted))
		resp.ContentLength = int64(len(converted))
	}
	resp.Header = resp.Header.Clone()
	resp.Header.Del("Content-Length")
	resp.Header.Del("Content-Encoding")
	if exchange.stream {
		resp.Header.Set("Content-Type", "text/event-stream")
	} else {
		resp.Header.Set("Content-Type", "application/json")
	}
	return nil
}
