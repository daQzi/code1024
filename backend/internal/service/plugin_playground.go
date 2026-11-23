package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/Wei-Shaw/sub2api/newapi-engine/templates"
)

type PluginPlaygroundInput struct {
	Source string            `json:"source"`
	Hook   string            `json:"hook"`
	Args   []json.RawMessage `json:"args"`
}

type PluginPlaygroundResult struct {
	Hook          string                  `json:"hook"`
	SHA256        string                  `json:"sha256"`
	DurationMs    int64                   `json:"duration_ms"`
	Offline       bool                    `json:"offline"`
	Result        json.RawMessage         `json:"result"`
	Compatibility PluginHostCompatibility `json:"compatibility"`
}

type PluginDeveloperTemplate struct {
	Kind    string                `json:"kind"`
	Source  string                `json:"source"`
	Fixture PluginPlaygroundInput `json:"fixture"`
}

func PluginDeveloperTemplates(actor int64) ([]PluginDeveloperTemplate, error) {
	if actor <= 0 {
		return nil, pluginInputError("Authentication required")
	}
	result := make([]PluginDeveloperTemplate, 0, 3)
	for _, kind := range []string{"ingress", "task", "provider"} {
		source, err := templates.Source(kind, PluginAuthorNamespace(actor)+"example")
		if err != nil {
			return nil, err
		}
		hook, args, err := templates.Example(kind)
		if err != nil {
			return nil, err
		}
		result = append(result, PluginDeveloperTemplate{Kind: kind, Source: source, Fixture: PluginPlaygroundInput{Hook: hook, Args: args}})
	}
	return result, nil
}

// DryRunPlugin executes only supplied fixture values in a fresh bounded child.
// It has no account/config lookup, HTTP fetch, task creation or billing path.
// This is a hook-output probe, not proof of production host compatibility.
func (s *NewAPIPluginService) DryRunPlugin(ctx context.Context, input PluginPlaygroundInput) (*PluginPlaygroundResult, error) {
	if len(input.Source) == 0 || len(input.Source) > runner.MaxSourceBytes || len(input.Args) > 4 {
		return nil, pluginInputError("Invalid playground source or argument count")
	}
	if !slices.Contains([]string{"ingress", "request", "outbound", "egress", "reply", "report", "buildSubmitRequest", "parseSubmitResponse", "parseSubmitEvent", "parseSubmitEventDelta", "buildQueryRequest", "parseTaskResult", "buildBatchQueryRequest", "parseBatchResult", "buildQuoteRequest", "parseQuoteResponse", "quotePrice", "buildCancelRequest", "parseCancelResponse", "extractUsage", "extractUsageOnComplete", "listArtifacts", "buildContentRequest", "buildBalanceRequest", "parseBalance", "convertRequest", "prepareRequest", "convertResponse", "convertStreamEvent", "signRequest", "credentialRejected"}, input.Hook) {
		return nil, pluginInputError("This hook is not available in the offline playground")
	}
	total := 0
	for _, arg := range input.Args {
		total += len(arg)
		if !json.Valid(arg) || total > 64<<10 {
			return nil, pluginInputError("Fixture arguments must be JSON and at most 64 KiB in total")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	started := time.Now()
	hash := sha256.Sum256([]byte(input.Source))
	result := &PluginPlaygroundResult{Hook: input.Hook, SHA256: hex.EncodeToString(hash[:]), Offline: true}
	err := s.pool.WithFresh(ctx, input.Source, func(client *runner.Client) error {
		if !slices.Contains(client.Descriptor.Hooks, input.Hook) {
			return errors.New("the source does not export the selected hook")
		}
		result.Compatibility = AssessPluginHost(client.Meta, client.Descriptor)
		args := make([]any, len(input.Args))
		for i := range input.Args {
			args[i] = input.Args[i]
		}
		raw, err := client.Call(ctx, input.Hook, nil, args...)
		if err != nil {
			return err
		}
		if len(raw) > 64<<10 || !json.Valid(raw) {
			return errors.New("hook output must be JSON and at most 64 KiB")
		}
		result.Result = raw
		return nil
	})
	if err != nil {
		return nil, pluginInputError(err.Error())
	}
	result.DurationMs = time.Since(started).Milliseconds()
	return result, nil
}
