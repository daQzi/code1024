// Package runner executes JavaScript hooks across an isolated process boundary.
// It does not give a plugin the server's environment, filesystem or credentials.
// The process boundary is not an operating-system sandbox.
package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/jsplugin"
)

const (
	MaxSourceBytes = 1 << 20
	MaxFrameBytes  = 8 << 20
	MaxResultBytes = 4 << 20
	MaxCallTime    = 40 * time.Second
)

func SupportsCapability(name string) bool {
	return name == jsplugin.CapabilityJSONClone || name == jsplugin.CapabilityDurationAuto || name == jsplugin.CapabilitySubmitSSEDelta
}

type request struct {
	ID          uint64                  `json:"id"`
	Operation   string                  `json:"operation,omitempty"`
	Source      string                  `json:"source,omitempty"`
	Hook        string                  `json:"hook,omitempty"`
	Path        []string                `json:"path,omitempty"`
	Args        []json.RawMessage       `json:"args,omitempty"`
	AllowFetch  bool                    `json:"allow_fetch,omitempty"`
	FetchResult *jsplugin.FetchResponse `json:"fetch_result,omitempty"`
	FetchError  string                  `json:"fetch_error,omitempty"`
}

// Descriptor carries capability facts which Meta intentionally keeps private.
// It must be produced by the worker, never inferred after a JSON round trip.
type Descriptor struct {
	Task                 bool     `json:"task"`
	Provider             bool     `json:"provider"`
	Transform            bool     `json:"transform"`
	RequiredCapabilities []string `json:"required_capabilities"`
	Hooks                []string `json:"hooks"`
}

type response struct {
	ID         uint64                 `json:"id"`
	Meta       *jsplugin.Meta         `json:"meta,omitempty"`
	Descriptor *Descriptor            `json:"descriptor,omitempty"`
	Result     json.RawMessage        `json:"result,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Fetch      *jsplugin.FetchRequest `json:"fetch,omitempty"`
}

// Serve handles one loaded plugin serially. The parent controls admission and
// kills this process if input/output or execution fails to respect its deadline.
// No network fetcher is installed here: fetching requires a separately scoped
// host service, and a missing service fails explicitly inside the engine.
func Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), MaxFrameBytes)
	encoder := json.NewEncoder(output)
	var plugin *jsplugin.LoadedPlugin
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			return errors.New("invalid plugin frame")
		}
		res := response{ID: req.ID}
		if plugin == nil {
			if len(req.Source) == 0 || len(req.Source) > MaxSourceBytes || req.Hook != "" {
				return errors.New("invalid plugin initialization")
			}
			var err error
			plugin, err = jsplugin.CompilePlugin(req.Source, jsplugin.Options{Concurrency: 1, Log: func(string) {}, HostCapability: SupportsCapability})
			if err != nil {
				res.Error = err.Error()
			} else {
				meta := plugin.Meta
				res.Meta = &meta
				res.Descriptor = &Descriptor{Task: meta.HasTaskPart(), Provider: meta.HasProviderPart(), Transform: len(meta.TransformHooks()) > 0, RequiredCapabilities: meta.RequiredCapabilities}
				for _, hook := range []string{"buildSubmitRequest", "parseSubmitResponse", "parseSubmitEvent", "parseSubmitEventDelta", "buildQueryRequest", "parseTaskResult", "extractUsage", "extractUsageOnComplete", "listArtifacts", "buildContentRequest", "signRequest", "exchangeKey", "checkKey", "prepareRequest", "credentialRejected", "convertRequest", "convertResponse", "convertStreamEvent", "login", "refresh", "scheduled", "revoke", "buildBalanceRequest", "parseBalance", "ingress", "request", "outbound", "egress", "reply", "report"} {
					if plugin.Engine.HasExport(hook) {
						res.Descriptor.Hooks = append(res.Descriptor.Hooks, hook)
					}
				}
			}
		} else if req.Operation == "channel_config" || req.Operation == "instance_config" {
			if len(req.Args) != 1 || len(req.Args[0]) > 64<<10 {
				res.Error = "invalid plugin config"
			} else {
				var validated json.RawMessage
				var err error
				if req.Operation == "channel_config" {
					validated, err = plugin.ValidateChannelConfig(req.Args[0])
					if err == nil {
						validated = plugin.ChannelConfigFor(validated)
					}
				} else {
					validated, err = plugin.ValidateConfig(req.Args[0])
				}
				if err != nil {
					res.Error = err.Error()
				} else {
					res.Result = validated
				}
			}
		} else if req.Source != "" || req.Hook == "" || len(req.Path) > 2 || len(req.Args) > 8 {
			res.Error = "invalid plugin call"
		} else {
			args := make([]any, len(req.Args))
			for i, raw := range req.Args {
				args[i] = jsplugin.RawJSON(raw)
			}
			callCtx, cancel := context.WithTimeout(ctx, MaxCallTime)
			if req.AllowFetch {
				callCtx = jsplugin.WithFetcher(callCtx, func(ctx context.Context, fetch jsplugin.FetchRequest) (jsplugin.FetchResponse, error) {
					if fetch.BodyStream != nil {
						return jsplugin.FetchResponse{}, errors.New("streaming media fetch requires the media host extension")
					}
					if err := encoder.Encode(response{ID: req.ID, Fetch: &fetch}); err != nil {
						return jsplugin.FetchResponse{}, err
					}
					if !scanner.Scan() {
						return jsplugin.FetchResponse{}, io.ErrUnexpectedEOF
					}
					var answer request
					if json.Unmarshal(scanner.Bytes(), &answer) != nil || answer.ID != req.ID || answer.Operation != "fetch_result" {
						return jsplugin.FetchResponse{}, errors.New("invalid host fetch reply")
					}
					if answer.FetchError != "" {
						return jsplugin.FetchResponse{}, errors.New(answer.FetchError)
					}
					if answer.FetchResult == nil || len(answer.FetchResult.Body) > jsplugin.MaxFetchBodyBytes {
						return jsplugin.FetchResponse{}, errors.New("host fetch result exceeds limit")
					}
					return *answer.FetchResult, nil
				})
			}
			var result json.RawMessage
			var err error
			if req.Operation == "typed" {
				result, err = callTyped(callCtx, plugin, req.Hook, req.Args)
			} else {
				err = plugin.Engine.CallPathInto(callCtx, &result, req.Hook, req.Path, args...)
			}
			cancel()
			if err != nil {
				res.Error = err.Error()
			} else if len(result) > MaxResultBytes {
				res.Error = "plugin result exceeds limit"
			} else {
				res.Result = result
			}
		}
		if err := encoder.Encode(res); err != nil {
			return fmt.Errorf("write plugin frame: %w", err)
		}
		if plugin == nil {
			return errors.New("plugin initialization failed")
		}
	}
	return scanner.Err()
}
