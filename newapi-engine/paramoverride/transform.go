// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// Adapted from the pinned relay/requestplugin/param_override.go; the request
// context intentionally exposes no client or account authentication headers.
package paramoverride

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/MoeForge/moego/regexp"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

var rejectCodePattern = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

func Transform(tctx jsplugin.TransformContext, body, override json.RawMessage) (json.RawMessage, error) {
	if len(body) > MaxBodyBytes || len(override) > MaxRuleBytes {
		return nil, errLimit
	}
	var rules map[string]any
	if json.Unmarshal(override, &rules) != nil || rules == nil {
		return nil, errors.New("utils.paramOverride override must be a JSON object")
	}
	model := tctx.UpstreamModel
	if model == "" {
		model = tctx.Model
	}
	context := map[string]any{
		"model": model, "upstream_model": model, "original_model": tctx.Model,
		"using_group": tctx.Group, "request_path": tctx.Path, "format": tctx.Format,
		"retry_index": tctx.Attempt, "is_retry": tctx.Attempt > 0,
		"retry": map[string]any{"index": tctx.Attempt, "is_retry": tctx.Attempt > 0},
	}
	if tctx.Channel != nil {
		context["channel_id"], context["channel_type"] = tctx.Channel.ID, tctx.Channel.Type
	}
	result, set, removed, err := ApplyPluginParamOverride(body, rules, context, nil)
	if err != nil {
		returned, ok := AsParamOverrideReturnError(err)
		if !ok {
			return nil, err
		}
		code := strings.ToLower(strings.TrimSpace(returned.Code))
		if !rejectCodePattern.MatchString(code) {
			code = "param_override"
		}
		message := strings.TrimSpace(returned.Message)
		if utf8.RuneCountInString(message) > 256 {
			message = string([]rune(message)[:256])
		}
		return json.Marshal(map[string]any{"reject": map[string]string{"code": code, "message": message}})
	}
	reply := map[string]any{"body": json.RawMessage(result)}
	if len(set) > 0 || len(removed) > 0 {
		headers := map[string]any{}
		if len(set) > 0 {
			headers["set"] = set
		}
		if len(removed) > 0 {
			headers["remove"] = removed
		}
		reply["headers"] = headers
	}
	return json.Marshal(reply)
}
