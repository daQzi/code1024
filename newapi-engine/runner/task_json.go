package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

func taskDecoderHook(hook string, path []string) bool {
	return hook == "protocols" && len(path) == 2 && path[1] == "decodeRequest" || hook == "native" && len(path) == 1
}

func taskBodyHook(hook string, path []string) bool {
	if taskDecoderHook(hook, path) {
		return true
	}
	if len(path) > 0 {
		return false
	}
	switch hook {
	case "buildSubmitRequest", "parseSubmitResponse", "parseSubmitEvent", "parseSubmitEventDelta", "buildQueryRequest", "parseTaskResult", "buildBatchQueryRequest", "parseBatchResult", "buildQuoteRequest", "parseQuoteResponse", "quotePrice", "buildCancelRequest", "parseCancelResponse", "extractUsage", "extractUsageOnComplete", "listArtifacts", "buildContentRequest":
		return true
	}
	return false
}

// Task decoders and driver contexts cross RPC as JSON, so normalizing just
// their body fields restores the baseline's default Go-map enumeration.
// Ordered plugins instead parse the original body at every hook invocation.
func taskHookArgument(raw json.RawMessage, ordered bool) (any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return jsplugin.RawJSON(raw), nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	changed := false
	normalize := func(body json.RawMessage) (json.RawMessage, error) {
		if len(body) > jsplugin.MaxJSONToolBytes {
			return nil, errors.New("task hook body exceeds limit")
		}
		if ordered {
			return body, nil
		}
		return sortedTaskJSON(body)
	}
	if body, ok := fields["requestBody"]; ok {
		sorted, err := normalize(body)
		if err != nil {
			return nil, err
		}
		fields["requestBody"], changed = sorted, true
	}
	if body, ok := fields["body"]; ok && bytes.HasPrefix(bytes.TrimSpace(body), []byte{'{'}) {
		var view map[string]json.RawMessage
		if err := json.Unmarshal(body, &view); err != nil {
			return nil, err
		}
		if string(view["kind"]) == `"json"` {
			if value, ok := view["value"]; ok {
				sorted, err := normalize(value)
				if err != nil {
					return nil, err
				}
				view["value"] = sorted
				fields["body"], err = json.Marshal(view)
				if err != nil {
					return nil, err
				}
				changed = true
			}
		}
	}
	if !changed || ordered {
		return jsplugin.RawJSON(raw), nil
	}
	encoded, err := json.Marshal(fields)
	return jsplugin.RawJSON(encoded), err
}

func sortedTaskJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > jsplugin.MaxJSONToolBytes {
		return nil, errors.New("task hook body exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// The pinned Task adaptor stringifies complete HTTP descriptors but captures
// only requestBody from decoder results. Other state/result members retain
// the original codec semantics, including their validation failures.
func callTaskHook(ctx context.Context, plugin *jsplugin.LoadedPlugin, req request, args []any) (json.RawMessage, error) {
	var result json.RawMessage
	if !plugin.Meta.PreservesJSONOrder() {
		err := plugin.Engine.CallPathInto(ctx, &result, req.Hook, req.Path, args...)
		return result, err
	}
	if len(req.Path) == 0 && (req.Hook == "buildSubmitRequest" || req.Hook == "buildQueryRequest" || req.Hook == "buildBatchQueryRequest" || req.Hook == "buildQuoteRequest" || req.Hook == "buildCancelRequest" || req.Hook == "buildContentRequest") {
		err := plugin.Engine.CallJSONInto(ctx, &result, req.Hook, args...)
		return result, err
	}
	if !taskDecoderHook(req.Hook, req.Path) {
		err := plugin.Engine.CallPathInto(ctx, &result, req.Hook, req.Path, args...)
		return result, err
	}
	value, body, err := plugin.Engine.CallPathWithMemberJSON(ctx, 0, "requestBody", req.Hook, req.Path, args...)
	if err != nil {
		return nil, err
	}
	if object, ok := value.(map[string]any); ok && body != nil {
		object["requestBody"] = body
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err = encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(output.Bytes(), []byte{'\n'}), nil
}
