// Package protocolconv hosts the pinned RelayKit codecs. It owns only protocol
// state: HTTP, credentials, routing, memory admission and billing stay in the host.
package protocolconv

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/Wei-Shaw/sub2api/newapi-engine/modelmodifier"
	"github.com/tidwall/gjson"
)

// Limits apply before typed decoding. A byte limit alone does not bound the
// allocation overhead of a large number of tiny DTO/content/tool objects.
const (
	MaxBody  = 1 << 20
	MaxNodes = 8192
	MaxDepth = 64

	// ConverterClaudeMessagesToGeminiContent and ConverterGeminiContentToClaudeMessages
	// mirror RelayKit's registered IDs for the two converters whose constants are
	// unexported in relayconvert.
	ConverterClaudeMessagesToGeminiContent = "claude_messages_to_gemini_generate_content"
	ConverterGeminiContentToClaudeMessages = "gemini_generate_content_to_claude_messages"
)

// Bridge belongs to one attempt, including its explicit follow-up chain. It
// must not be shared by concurrent requests or used after credential replay.
type Bridge struct {
	meta     convmeta.Values
	from, to types.RelayFormat
	state    *relayconvert.ResponseStreamState
	bytes    int
	terminal bool
	wireDone bool
	closed   bool
}

type Event struct {
	Name string
	Data string
}

// Supported lists all 12 RelayKit text protocol converters. Host ingress
// endpoints still gate which incoming paths are publicly exposed.
func Supported(converter string) bool {
	switch strings.TrimSpace(converter) {
	case relayconvert.ConverterOpenAIChatToClaudeMessages,
		relayconvert.ConverterOpenAIChatToOpenAIResponses,
		relayconvert.ConverterOpenAIChatToGeminiContent,
		relayconvert.ConverterOpenAIResponsesToOpenAIChat,
		relayconvert.ConverterOpenAIResponsesToClaudeMessages,
		relayconvert.ConverterOpenAIResponsesToGemini,
		relayconvert.ConverterClaudeMessagesToOpenAIChat,
		relayconvert.ConverterClaudeMessagesToOpenAIResponses,
		ConverterClaudeMessagesToGeminiContent,
		relayconvert.ConverterGeminiContentToOpenAIChat,
		relayconvert.ConverterGeminiContentToOpenAIResponses,
		ConverterGeminiContentToClaudeMessages:
		return true
	}
	return false
}

// New converts the request once, before any vendor-specific plugin hook. The
// caller must hold its shared memory reservation while using the bridge.
func New(ctx context.Context, converter, model string, body []byte, stream bool) (*Bridge, []byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !Supported(converter) || !validJSON(body) {
		return nil, nil, errors.New("unsupported or invalid preset conversion request")
	}
	spec, ok := relayconvert.LookupRequestConverter(converter)
	if !ok {
		return nil, nil, errors.New("preset converter is unavailable")
	}
	b := &Bridge{from: spec.From, to: spec.To, meta: convmeta.Values{
		OriginModelName: model, UpstreamModelName: model,
		ChannelMetaAttached: true, IsStream: stream,
	}}
	var request any
	switch spec.From {
	case types.RelayFormatOpenAI:
		request = &dto.GeneralOpenAIRequest{}
	case types.RelayFormatOpenAIResponses:
		request = &dto.OpenAIResponsesRequest{}
	case types.RelayFormatClaude:
		request = &dto.ClaudeRequest{}
	case types.RelayFormatGemini:
		request = &dto.GeminiChatRequest{}
	default:
		return nil, nil, errors.New("unsupported preset client protocol")
	}
	if json.Unmarshal(body, request) != nil {
		return nil, nil, errors.New("invalid preset client request")
	}
	// Exact modifier budgets and adaptive intent are private DTO state, not
	// extra JSON fields sent to the vendor or the conversion plugin.
	if intent, ok := modelmodifier.Intent(ctx); ok {
		state := reasoning.StateFromIntent(intent)
		b.meta.ReasoningConversion = state
		switch typed := request.(type) {
		case *dto.GeneralOpenAIRequest:
			typed.ReasoningConversion = state
		case *dto.OpenAIResponsesRequest:
			typed.ReasoningConversion = state
		}
	}
	result, err := relayconvert.ConvertRequestByID(ctx, &b.meta, converter, request)
	if err != nil {
		return nil, nil, errors.New("preset request conversion failed")
	}
	// Propagate streaming explicitly across protocols (e.g. Gemini has no JSON
	// stream field) and always request usage on cross-protocol Chat streams.
	if stream {
		streamTrue := true
		switch typed := result.Value.(type) {
		case *dto.GeneralOpenAIRequest:
			typed.Stream = &streamTrue
			typed.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
		case *dto.OpenAIResponsesRequest:
			typed.Stream = &streamTrue
		case *dto.ClaudeRequest:
			typed.Stream = &streamTrue
		}
	}
	raw, err := marshal(result.Value)
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return b, raw, nil
}

func (b *Bridge) From() types.RelayFormat {
	if b == nil {
		return ""
	}
	return b.from
}
func (b *Bridge) To() types.RelayFormat {
	if b == nil {
		return ""
	}
	return b.to
}
func (b *Bridge) Responses() bool { return b != nil && b.to == types.RelayFormatOpenAIResponses }
func (b *Bridge) Gemini() bool    { return b != nil && b.to == types.RelayFormatGemini }
func (b *Bridge) Claude() bool    { return b != nil && b.to == types.RelayFormatClaude }

func (b *Bridge) JSON(ctx context.Context, body []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.closed || !validJSON(body) || !terminalJSON(b.to, gjson.ParseBytes(body), false) {
		return nil, errors.New("preset upstream response is invalid or incomplete")
	}
	response, err := decodeResponse(b.to, body, false)
	if err != nil {
		return nil, err
	}
	result, err := relayconvert.ConvertResponse(ctx, &b.meta, b.from, response)
	if err != nil {
		return nil, errors.New("preset response conversion failed")
	}
	return marshal(result.Value)
}

// Stream only finalizes after clean EOF and observed vendor terminal evidence.
// RelayKit alone may synthesize a terminator on EOF; the host must not turn a
// truncated generation into a paid success by invoking that finalizer early.
func (b *Bridge) Stream(ctx context.Context, data string, done bool) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.closed {
		return nil, errors.New("preset stream already finalized")
	}
	if b.state == nil {
		var err error
		b.state, err = relayconvert.NewResponseStreamState(b.to, b.from, relayconvert.ResponseStreamOptions{
			Model: b.meta.UpstreamModelName, IncludeUsage: true, EmitSequenceNumber: true,
		})
		if err != nil {
			return nil, errors.New("preset stream converter is unavailable")
		}
	}
	var results []relayconvert.ResponseResult
	if done {
		b.closed = true
		if !b.terminal {
			return nil, errors.New("preset upstream stream ended without a terminal result")
		}
		var err error
		results, err = relayconvert.FinalizeStreamResponse(ctx, &b.meta, b.state)
		if err != nil {
			return nil, errors.New("preset stream finalization failed")
		}
	} else {
		if b.wireDone || len(data) > MaxBody-b.bytes {
			return nil, errors.New("preset stream exceeds cumulative limit or follows its terminator")
		}
		b.bytes += len(data)
		if data == "[DONE]" && b.to == types.RelayFormatOpenAI {
			if !b.terminal {
				return nil, errors.New("preset Chat stream ended without a finish reason")
			}
			b.wireDone = true
			return nil, nil
		}
		if !validJSON([]byte(data)) {
			return nil, errors.New("invalid preset upstream stream event")
		}
		root := gjson.Parse(data)
		if root.Get("error").Type != gjson.Null || root.Get("type").String() == "error" || root.Get("type").String() == "response.failed" {
			return nil, errors.New("preset upstream stream reported failure")
		}
		b.terminal = b.terminal || terminalJSON(b.to, root, true)
		response, err := decodeResponse(b.to, []byte(data), true)
		if err != nil {
			return nil, err
		}
		results, err = relayconvert.ConvertStreamResponseChunk(ctx, &b.meta, b.state, response)
		if err != nil {
			return nil, errors.New("preset stream conversion failed")
		}
	}
	events := make([]Event, 0, len(results)+1)
	outputBytes := 0
	for _, result := range results {
		value := result.Value
		// RelayKit's Responses stream result is an envelope for the host,
		// rather than the JSON wire DTO. The pinned host writes its Payload.
		switch event := value.(type) {
		case relayconvert.ChatToResponsesStreamEvent:
			value = event.Payload
		case *relayconvert.ChatToResponsesStreamEvent:
			value = event.Payload
		}
		// Anthropic sends input and output usage in separate events. Reuse
		// RelayKit's canonical nonzero merge so a late output-only event does
		// not overwrite the input count in the host's standard Chat parser.
		if chunk, ok := value.(*dto.ChatCompletionsStreamResponse); ok && chunk.Usage != nil {
			chunk.Usage = b.state.Usage()
		}
		raw, err := marshal(value)
		if err != nil || len(raw) > MaxBody-outputBytes {
			return nil, errors.New("preset stream output exceeds limit")
		}
		outputBytes += len(raw)
		name := ""
		if b.from == types.RelayFormatOpenAIResponses || b.from == types.RelayFormatClaude {
			name = gjson.GetBytes(raw, "type").String()
		}
		events = append(events, Event{Name: name, Data: string(raw)})
	}
	if done && b.from == types.RelayFormatOpenAI {
		events = append(events, Event{Data: "[DONE]"})
	}
	return events, ctx.Err()
}

func decodeResponse(format types.RelayFormat, body []byte, stream bool) (any, error) {
	var response any
	switch format {
	case types.RelayFormatOpenAI:
		if stream {
			response = &dto.ChatCompletionsStreamResponse{}
		} else {
			response = &dto.OpenAITextResponse{}
		}
	case types.RelayFormatOpenAIResponses:
		if stream {
			response = &dto.ResponsesStreamResponse{}
		} else {
			response = &dto.OpenAIResponsesResponse{}
		}
	case types.RelayFormatClaude:
		response = &dto.ClaudeResponse{}
	case types.RelayFormatGemini:
		response = &dto.GeminiChatResponse{}
	default:
		return nil, errors.New("unsupported preset upstream protocol")
	}
	if json.Unmarshal(body, response) != nil {
		return nil, errors.New("invalid preset upstream response")
	}
	return response, nil
}

func terminalJSON(format types.RelayFormat, root gjson.Result, stream bool) bool {
	if root.Get("error").Type != gjson.Null {
		return false
	}
	switch format {
	case types.RelayFormatOpenAI:
		choices := root.Get("choices").Array()
		if len(choices) == 0 {
			return false
		}
		for _, choice := range choices {
			if choice.Get("finish_reason").Type != gjson.String || choice.Get("finish_reason").String() == "" {
				return false
			}
		}
		return true
	case types.RelayFormatOpenAIResponses:
		if stream {
			kind := root.Get("type").String()
			if kind != "response.completed" && kind != "response.incomplete" && kind != "response.done" {
				return false
			}
			root = root.Get("response")
		}
		status := root.Get("status").String()
		return (status == "completed" || status == "incomplete") && root.Get("output").IsArray() && root.Get("error").Type == gjson.Null
	case types.RelayFormatClaude:
		if stream {
			return root.Get("type").String() == "message_stop"
		}
		return root.Get("type").String() == "message" && root.Get("content").IsArray() && root.Get("stop_reason").String() != ""
	case types.RelayFormatGemini:
		candidates := root.Get("candidates").Array()
		if len(candidates) == 0 {
			return false
		}
		for _, candidate := range candidates {
			if candidate.Get("finishReason").String() == "" {
				return false
			}
		}
		return true
	}
	return false
}

func marshal(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil || !validJSON(raw) {
		return nil, errors.New("preset output is invalid or exceeds limits")
	}
	return raw, nil
}

func validJSON(raw []byte) bool {
	if len(raw) > MaxBody || !utf8.Valid(raw) || !gjson.ValidBytes(raw) {
		return false
	}
	root := gjson.ParseBytes(raw)
	if !root.IsObject() {
		return false
	}
	nodes := 0
	var visit func(gjson.Result, int) bool
	visit = func(value gjson.Result, depth int) bool {
		nodes++
		if nodes > MaxNodes || depth > MaxDepth {
			return false
		}
		valid := true
		var keys map[string]struct{}
		if value.IsObject() {
			keys = make(map[string]struct{})
		}
		if value.IsObject() || value.IsArray() {
			value.ForEach(func(key, child gjson.Result) bool {
				if keys != nil {
					name := key.String()
					if _, exists := keys[name]; exists {
						valid = false
						return false
					}
					keys[name] = struct{}{}
				}
				valid = visit(child, depth+1)
				return valid
			})
		}
		return valid
	}
	return visit(root, 0)
}
