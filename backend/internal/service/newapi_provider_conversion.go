package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"github.com/Wei-Shaw/sub2api/newapi-engine/protocolconv"
	"github.com/Wei-Shaw/sub2api/newapi-engine/requestpatch"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/tidwall/gjson"
)

// A response uses the source/config/state selected for this request, even if
// the administrator changes the plugin while the upstream is generating.
// The host carries explicit ctx.state between hook calls; worker globals are
// not request state. No worker is leased while waiting on either HTTP peer.
type newAPIProviderExchange struct {
	tools              *newAPIProviderToolUsage
	request            *http.Request
	source             string
	hookCtx            jsplugin.HookContext
	convert            bool
	nativeResponse     bool
	stream             bool
	readStream         bool
	tokenKey           string
	credentialRejected bool
	// A mutating stream that already emitted vendor events may have started
	// billable work. A later credential error must not replay that request.
	credentialReplayUnsafe bool
	renew                  func(context.Context) (*newAPIProviderExchange, error)
	proxy                  string
	hostBody               []byte
	chain                  *newAPIProviderChainSpec
	followup               *jsplugin.ConvertedRequest
	followupState          json.RawMessage
	protocol               *protocolconv.Bridge
	modifier               *modelmodifier.Spec
	socket                 *jsplugin.WebSocketRequest
}

func newAPIProviderJSONContentType(value string) bool {
	kind, _, err := mime.ParseMediaType(value)
	return err == nil && (kind == "application/json" || strings.HasSuffix(kind, "+json"))
}

// Keep the baseline's billing fields immutable during prepareRequest. The
// converter may translate their representation; prepare only patches business
// parameters after conversion, without changing the price or wire mode.
var newAPIProviderBillingFields = []string{
	"model", "stream", "stream_options", "n", "generationConfig.candidateCount",
	"size", "quality", "resolution", "seconds", "duration", "service_tier",
}

func newAPIProviderProtectedHeader(route contract.AdvancedCustomRoute, name string) bool {
	return requestpatch.IsProtectedHeader(name) || route.Auth != nil && strings.TrimSpace(route.Auth.Type) == contract.AdvancedCustomAuthTypeHeader && strings.EqualFold(strings.TrimSpace(route.Auth.Name), name)
}

func prepareNewAPIProviderRequest(ctx context.Context, client *runner.Client, hookCtx jsplugin.HookContext, req *http.Request, route contract.AdvancedCustomRoute, model string, stream bool, body []byte) ([]byte, error) {
	if len(body) > MaxRequestPluginBody {
		return nil, errors.New("Provider preparation body exceeds limit")
	}
	business := map[string]string{}
	for name := range req.Header {
		if !newAPIProviderProtectedHeader(route, name) {
			business[name] = req.Header.Get(name)
		}
	}
	values, err := client.Typed(ctx, nil, "CallPrepareRequest", hookCtx, jsplugin.PrepareInput{
		Path: hookCtx.Path, UpstreamPath: req.URL.Path, Model: model,
		Stream: stream, Body: body, Headers: business,
	})
	if err != nil {
		return nil, errors.New("Provider request preparation failed")
	}
	var prepared jsplugin.PreparedRequest
	if len(values) != 1 || json.Unmarshal(values[0], &prepared) != nil {
		return nil, errors.New("invalid Provider prepared request")
	}
	if prepared.Effects != nil {
		effects, err := requestpatch.DecodeEffects(prepared.Effects)
		if err != nil {
			return nil, errors.New("invalid Provider preparation effects")
		}
		patched, headers, err := requestpatch.ApplyWithLimit(body, req.Header, effects, func(name string) bool {
			return newAPIProviderProtectedHeader(route, name)
		}, MaxRequestPluginBody)
		if err != nil || !gjson.ValidBytes(patched) || !newAPIProviderUnambiguousJSON(gjson.ParseBytes(patched), 0) || !sameNewAPIProviderBillingFields(body, patched) {
			return nil, errors.New("invalid Provider preparation effects or body limit")
		}
		body, req.Header = patched, headers
	}
	if prepared.Path != "" || prepared.URL != "" {
		target := prepared.URL
		if target == "" {
			target = prepared.Path
		}
		address, err := newAPIProviderURL(hookCtx.BaseURL, target, model)
		if err != nil {
			return nil, err
		}
		req.URL, err = url.Parse(address)
		if err != nil {
			return nil, errors.New("invalid Provider prepared target")
		}
	}
	return body, nil
}

func newAPIProviderUnambiguousJSON(value gjson.Result, depth int) bool {
	if depth > 128 || value.IsObject() && hasDuplicateJSONObjectKeys(value) {
		return false
	}
	valid := true
	if value.IsObject() || value.IsArray() {
		value.ForEach(func(_, child gjson.Result) bool {
			valid = newAPIProviderUnambiguousJSON(child, depth+1)
			return valid
		})
	}
	return valid
}

func sameNewAPIProviderBillingFields(before, after []byte) bool {
	for _, field := range newAPIProviderBillingFields {
		previous, next := gjson.GetBytes(before, field), gjson.GetBytes(after, field)
		if previous.Exists() != next.Exists() || !reflect.DeepEqual(previous.Value(), next.Value()) {
			return false
		}
	}
	// Reject alternative case spellings too. Go vendors often decode JSON
	// case-insensitively, while gjson intentionally performs an exact lookup.
	return bytes.Equal(newAPIProviderBillingSpellings(before), newAPIProviderBillingSpellings(after))
}

func newAPIProviderBillingSpellings(body []byte) []byte {
	var fields []any
	gjson.ParseBytes(body).ForEach(func(key, value gjson.Result) bool {
		name := key.String()
		for _, field := range newAPIProviderBillingFields {
			root, _, _ := strings.Cut(field, ".")
			if strings.EqualFold(name, root) && name != root {
				fields = append(fields, name, value.Value())
				break
			}
		}
		if name == "generationConfig" && value.IsObject() {
			value.ForEach(func(k, v gjson.Result) bool {
				if strings.EqualFold(k.String(), "candidateCount") && k.String() != "candidateCount" {
					fields = append(fields, name+"."+k.String(), v.Value())
				}
				return true
			})
		}
		return true
	})
	raw, _ := json.Marshal(fields)
	return raw
}

func (s *NewAPIPluginService) convertProviderResponse(ctx context.Context, exchange *newAPIProviderExchange, resp *http.Response, credentialRejected bool) error {
	exchange.followup, exchange.followupState = nil, nil
	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	if _, socket := resp.Body.(*providerSocketBody); success && socket {
		if !exchange.stream {
			return s.collectProviderStreamJSON(ctx, exchange, resp, false)
		}
		resp.Body = newNewAPIProviderStream(ctx, s, exchange, resp.Body, false)
		resp.Header = http.Header{"Content-Type": {"text/event-stream"}}
		resp.ContentLength = -1
		return nil
	}
	if success && exchange.nativeResponse {
		return nil
	}
	kind, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	eventStream := kind == "text/event-stream" || kind == "application/x-ndjson" || kind == "application/ndjson"
	if success && (exchange.readStream || eventStream) {
		if !eventStream {
			return errors.New("Provider conversion requires an SSE or NDJSON stream")
		}
		if !exchange.stream {
			return s.collectProviderStreamJSON(ctx, exchange, resp, kind == "text/event-stream")
		}
		resp.Body = newNewAPIProviderStream(ctx, s, exchange, resp.Body, kind == "text/event-stream")
		resp.Header = resp.Header.Clone()
		resp.Header.Del("Content-Length")
		resp.Header.Del("Content-Encoding")
		resp.Header.Set("Content-Type", "text/event-stream")
		resp.ContentLength = -1
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxRequestPluginBody+1))
	if err != nil || len(raw) > MaxRequestPluginBody {
		return errors.New("Provider response read failed or exceeds conversion limit")
	}
	var converted jsplugin.ConvertedResponse
	var upstreamErr *jsplugin.UpstreamError
	hookCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = s.pool.With(hookCtx, exchange.source, func(client *runner.Client) error {
		values, err := client.Typed(hookCtx, nil, "CallConvertResponse", exchange.hookCtx, resp.StatusCode, pluginResponseHeaders(resp.Header), raw)
		if err != nil || len(values) != 2 {
			return errors.New("Provider response conversion failed")
		}
		if json.Unmarshal(values[0], &converted) != nil || json.Unmarshal(values[1], &upstreamErr) != nil {
			return errors.New("invalid Provider converted response")
		}
		return nil
	})
	if err != nil {
		if !success {
			// Match the baseline: failed error conversion must not hide a 401,
			// 429 or 5xx from the host's credential and failover handling.
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(raw))
			return nil
		}
		return errors.New("Provider response conversion failed")
	}
	// A converter may return next/state even for an HTTP credential rejection.
	// The rejection owns this step: neither billing facts nor a continuation
	// may survive it, even when the host cannot renew the credential.
	if !credentialRejected && upstreamErr == nil && (success || converted.Next != nil) {
		if err := exchange.tools.add(converted.Tools); err != nil {
			return err
		}
	}
	if !credentialRejected && upstreamErr == nil && converted.Next != nil {
		if len(converted.State) > MaxRequestPluginBody {
			return errors.New("Provider chained state exceeds limit")
		}
		exchange.followup, exchange.followupState = converted.Next, converted.State
		// Keep the bounded original response available if credential renewal
		// fails. A next request may follow either a success or an error status.
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		return nil
	}
	resp.Header = resp.Header.Clone()
	resp.Header.Del("Content-Length")
	resp.Header.Del("Content-Encoding")
	switch {
	case upstreamErr != nil:
		status := upstreamErr.Status
		if status == 0 {
			status = resp.StatusCode
			if status < 400 {
				status = http.StatusBadGateway
			}
		}
		resp.StatusCode = status
		raw, _ = json.Marshal(map[string]any{"error": map[string]string{"message": upstreamErr.Message, "type": upstreamErr.Type, "code": upstreamErr.Code}})
	case !success:
		// A converter cannot turn a failed HTTP status into a paid success.
		// Keep the original status/body for the existing failover rules.
	case !newAPIProviderJSONContentType(converted.ContentType) || len(converted.Body) > MaxRequestPluginBody:
		return errors.New("Provider converted response must be bounded JSON")
	default:
		raw = converted.Body
		resp.StatusCode = http.StatusOK
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	resp.ContentLength = int64(len(raw))
	resp.Header.Set("Content-Type", "application/json")
	return nil
}

// This reader converts on demand. Each event leases a worker only for its
// bounded hook call. Reader errors propagate to the existing OpenAI stream
// handlers, including after partial output; transport failure never invokes
// the clean-EOF hook or manufactures [DONE].
type newAPIProviderStream struct {
	ctx         context.Context
	service     *NewAPIPluginService
	exchange    newAPIProviderExchange
	upstream    io.ReadCloser
	scanner     *bufio.Scanner
	sse         bool
	pending     bytes.Buffer
	done        bool
	failure     error
	events      int
	mutex       sync.Mutex
	idleTimeout time.Duration
	closed      bool
	stop        func() bool
	preset      *protocolconv.Bridge
	socket      *providerSocketBody
	socketEnded bool
}

func newNewAPIProviderStream(ctx context.Context, service *NewAPIPluginService, exchange *newAPIProviderExchange, upstream io.ReadCloser, sse bool) *newAPIProviderStream {
	s := &newAPIProviderStream{ctx: ctx, service: service, exchange: *exchange, upstream: upstream, sse: sse, idleTimeout: 30 * time.Second}
	s.socket, _ = upstream.(*providerSocketBody)
	s.scanner = bufio.NewScanner(&newAPIProviderActivityReader{stream: s})
	s.scanner.Buffer(make([]byte, 4096), MaxRequestPluginBody)
	s.mutex.Lock()
	s.stop = context.AfterFunc(ctx, func() { _ = s.Close() })
	s.mutex.Unlock()
	return s
}

type newAPIProviderActivityReader struct{ stream *newAPIProviderStream }

func (r *newAPIProviderActivityReader) Read(p []byte) (int, error) {
	s := r.stream
	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		return 0, errors.New("Provider stream closed or timed out")
	}
	s.mutex.Unlock()
	// Bound waiting for upstream data, not the time a downstream spends
	// consuming a converted event. Slow clients do not look like idle vendors.
	idle := time.AfterFunc(s.idleTimeout, func() { _ = s.Close() })
	n, err := s.upstream.Read(p)
	stopped := idle.Stop()
	s.mutex.Lock()
	closed := s.closed
	s.mutex.Unlock()
	if closed || !stopped {
		err = errors.New("Provider stream closed or timed out")
	}
	return n, err
}

func (s *newAPIProviderStream) next() (*jsplugin.StreamEvent, error) {
	if s.socket != nil {
		if s.socketEnded {
			return nil, io.EOF
		}
		return s.socket.next(s.ctx, s.idleTimeout)
	}
	event := &jsplugin.StreamEvent{}
	var data strings.Builder
	hasData, size := false, 0
	for s.scanner.Scan() {
		line := s.scanner.Text()
		size += len(line) + 1
		if size > MaxRequestPluginBody {
			return nil, errors.New("Provider stream event exceeds conversion limit")
		}
		if !s.sse {
			if strings.TrimSpace(line) != "" {
				return &jsplugin.StreamEvent{Data: line}, nil
			}
			continue
		}
		if line == "" && (hasData || event.Event != "") {
			event.Data = data.String()
			return event, nil
		}
		name, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch name {
		case "event":
			event.Event = value
		case "data":
			if hasData {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			hasData = true
		}
		if line == "" {
			event = &jsplugin.StreamEvent{}
		}
	}
	if err := s.scanner.Err(); err != nil {
		return nil, errors.New("Provider upstream stream read failed")
	}
	if hasData || event.Event != "" {
		event.Data = data.String()
		return event, nil
	}
	return nil, io.EOF
}

func (s *newAPIProviderStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for s.pending.Len() == 0 && !s.done {
		if err := s.ctx.Err(); err != nil {
			s.done, s.failure = true, err
			return 0, err
		}
		event, err := s.next()
		if err != nil && err != io.EOF {
			s.done, s.failure = true, err
			return 0, err
		}
		done := err == io.EOF
		if err := s.convert(event, done); err != nil {
			s.done, s.failure = true, err
			return 0, err
		}
		s.done = done
	}
	if s.pending.Len() > 0 {
		return s.pending.Read(p)
	}
	return 0, io.EOF
}

func (s *newAPIProviderStream) convert(event *jsplugin.StreamEvent, done bool) error {
	output, err := s.convertResult(event, done)
	if err != nil {
		return errors.New("Provider stream conversion failed")
	}
	if output.HasBody || len(output.Chunks) > 0 {
		return errors.New("Provider streaming clients require events, not a body or chunks")
	}
	for _, event := range output.Events {
		if event.Event != "" {
			s.pending.WriteString("event: " + event.Event + "\n")
		}
		for line := range strings.SplitSeq(event.Data, "\n") {
			s.pending.WriteString("data: " + strings.TrimSuffix(line, "\r") + "\n")
		}
		s.pending.WriteString("\n")
		if s.pending.Len() > MaxRequestPluginBody {
			s.pending.Reset()
			return errors.New("Provider converted event exceeds output limit")
		}
	}
	return nil
}

func (s *newAPIProviderStream) convertResult(event *jsplugin.StreamEvent, done bool) (jsplugin.StreamResult, error) {
	s.events++
	if s.events > 65536 {
		return jsplugin.StreamResult{}, errors.New("Provider stream exceeds event count limit")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	if s.preset != nil {
		data := ""
		if event != nil {
			data = event.Data
		}
		converted, err := s.preset.Stream(ctx, data, done)
		if err != nil {
			return jsplugin.StreamResult{}, err
		}
		if !done {
			if err := s.exchange.tools.observeVendor(s.preset, []byte(data), true); err != nil {
				return jsplugin.StreamResult{}, err
			}
		}
		output := jsplugin.StreamResult{}
		for _, event := range converted {
			output.Events = append(output.Events, jsplugin.StreamEvent{Event: event.Name, Data: event.Data})
		}
		return output, nil
	}
	var output jsplugin.StreamResult
	var state json.RawMessage
	var upstreamErr *jsplugin.UpstreamError
	err := s.service.pool.With(ctx, s.exchange.source, func(client *runner.Client) error {
		values, err := client.Typed(ctx, nil, "CallConvertStreamEvent", s.exchange.hookCtx, event, done)
		if err != nil || len(values) != 3 {
			return errors.New("Provider stream conversion failed")
		}
		if json.Unmarshal(values[0], &output) != nil || json.Unmarshal(values[1], &state) != nil || json.Unmarshal(values[2], &upstreamErr) != nil {
			return errors.New("invalid Provider converted stream event")
		}
		return nil
	})
	if err != nil {
		return jsplugin.StreamResult{}, errors.New("Provider stream conversion failed")
	}
	if upstreamErr != nil {
		return jsplugin.StreamResult{}, upstreamErr
	}
	if len(state) > MaxRequestPluginBody || s.socket == nil && (len(output.Send) > 0 || output.Close) || done && len(output.Send) > 0 {
		return jsplugin.StreamResult{}, errors.New("Provider stream conversion requires an unsupported output mode")
	}
	if err := s.exchange.tools.add(output.Tools); err != nil {
		return jsplugin.StreamResult{}, err
	}
	if s.socket != nil {
		if err := s.socket.send(ctx, output.Send); err != nil {
			return jsplugin.StreamResult{}, err
		}
		if output.Close {
			s.socketEnded = true
			_ = s.socket.Close()
		}
	}
	s.exchange.hookCtx.State = state
	return output, nil
}

func (s *newAPIProviderStream) Close() error {
	s.mutex.Lock()
	if s.closed {
		s.mutex.Unlock()
		return nil
	}
	s.closed = true
	if s.stop != nil {
		s.stop()
	}
	s.mutex.Unlock()
	return s.upstream.Close()
}
