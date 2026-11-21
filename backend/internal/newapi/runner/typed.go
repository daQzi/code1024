package runner

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/Wei-Shaw/sub2api/internal/newapi/jsplugin"
)

// These are the baseline's typed host calls. Keeping result validation in the
// original Go contract avoids accepting raw unvalidated effects over RPC.
var typedCalls = map[string]bool{
	"CallLogin": true, "CallRefresh": true, "CallScheduled": true, "CallRevoke": true,
	"CallSignRequest": true, "CallPrepareRequest": true, "CallCredentialRejected": true,
	"CallConvertRequest": true, "CallConvertResponse": true, "CallConvertStreamEvent": true,
	"CallExchangeKey": true, "CallCheckKey": true, "CallBuildBalanceRequest": true, "CallParseBalance": true,
	"CallTransform": true, "CallReport": true, "CallReply": true,
}

func callTyped(ctx context.Context, plugin *jsplugin.LoadedPlugin, method string, args []json.RawMessage) (json.RawMessage, error) {
	if !typedCalls[method] {
		return nil, errors.New("unknown typed plugin operation")
	}
	call := reflect.ValueOf(plugin).MethodByName(method)
	if !call.IsValid() || call.Type().NumIn() != len(args)+1 {
		return nil, errors.New("invalid typed plugin arguments")
	}
	values := []reflect.Value{reflect.ValueOf(ctx)}
	for i, raw := range args {
		value := reflect.New(call.Type().In(i + 1))
		if err := json.Unmarshal(raw, value.Interface()); err != nil {
			return nil, errors.New("invalid typed plugin argument shape")
		}
		values = append(values, value.Elem())
	}
	results := call.Call(values)
	if last := results[len(results)-1]; !last.IsNil() {
		return nil, last.Interface().(error)
	}
	out := make([]any, 0, len(results)-1)
	for _, value := range results[:len(results)-1] {
		out = append(out, value.Interface())
	}
	return json.Marshal(out)
}

func (c *Client) Typed(ctx context.Context, fetcher jsplugin.Fetcher, method string, args ...any) ([]json.RawMessage, error) {
	if !typedCalls[method] || len(args) > 8 {
		return nil, errors.New("unknown typed plugin operation")
	}
	req := request{Operation: "typed", Hook: method, AllowFetch: fetcher != nil, Args: make([]json.RawMessage, len(args))}
	for i, arg := range args {
		raw, err := json.Marshal(arg)
		if err != nil {
			return nil, err
		}
		if len(raw) > MaxResultBytes {
			return nil, errors.New("typed argument exceeds limit")
		}
		req.Args[i] = raw
	}
	res, err := c.exchangeWithFetcher(ctx, req, fetcher)
	if err != nil {
		return nil, err
	}
	var values []json.RawMessage
	if err = json.Unmarshal(res.Result, &values); err != nil {
		return nil, err
	}
	return values, nil
}
