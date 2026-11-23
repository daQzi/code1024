// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f.
// See ../NOTICE.md for provenance and local adaptations.

package taskprotocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

var nativeReplyAllowedHeaders = []string{"Location", "Retry-After"}

// NativeReply is the sanitized HTTP reply parsed from a plugin's native render
// hook output, supporting both plain JSON bodies and the {__status, __headers?,
// __body?} reply wrapper.
type NativeReply struct {
	Status  int
	Headers map[string]string
	Body    any
	HasBody bool
}

// ParseNativeReply validates and unpacks a native render hook return value
// according to the pinned New API WriteNativeReply contract.
func ParseNativeReply(defaultStatus int, value any) (NativeReply, error) {
	if raw, ok := value.(json.RawMessage); ok {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return NativeReply{}, fmt.Errorf("native reply is not valid JSON: %w", err)
		}
		value = decoded
	}
	wrapper, ok := value.(map[string]any)
	if !ok {
		return NativeReply{Status: defaultStatus, Body: value, HasBody: true}, nil
	}
	rawStatus, isWrapper := wrapper["__status"]
	if !isWrapper {
		return NativeReply{Status: defaultStatus, Body: value, HasBody: true}, nil
	}
	status := 0
	switch typed := rawStatus.(type) {
	case int64:
		status = int(typed)
	case int:
		status = typed
	case float64:
		if typed == float64(int(typed)) {
			status = int(typed)
		}
	}
	if status < 200 || status > 599 {
		return NativeReply{}, errors.New("reply wrapper __status must be an integer from 200 to 599")
	}
	headers := map[string]string{}
	for key, member := range wrapper {
		switch key {
		case "__status", "__body":
		case "__headers":
			values, ok := member.(map[string]any)
			if !ok {
				return NativeReply{}, errors.New("reply wrapper __headers must be an object")
			}
			for name, headerValue := range values {
				canonical := http.CanonicalHeaderKey(name)
				text, isString := headerValue.(string)
				if !slices.Contains(nativeReplyAllowedHeaders, canonical) || !isString {
					return NativeReply{}, errors.New("reply wrapper may set only string Location and Retry-After headers")
				}
				if strings.ContainsAny(text, "\r\n") || len(text) > 4096 {
					return NativeReply{}, errors.New("reply wrapper header value is invalid")
				}
				headers[canonical] = text
			}
		default:
			return NativeReply{}, fmt.Errorf("reply wrapper has unknown member %q", key)
		}
	}
	body, hasBody := wrapper["__body"]
	return NativeReply{
		Status:  status,
		Headers: headers,
		Body:    body,
		HasBody: hasBody,
	}, nil
}

// MatchNativeRoute finds the first declared meta.routes entry matching method
// and subpath (with exact static segments and :param placeholders). Static
// routes take precedence over parameterized ones when both appear.
func MatchNativeRoute(routes []jsplugin.Route, method, subpath string) (jsplugin.Route, map[string]string, bool) {
	normalizedMethod := strings.ToUpper(strings.TrimSpace(method))
	reqSegments, ok := splitCleanPath(subpath)
	if !ok {
		return jsplugin.Route{}, nil, false
	}
	var bestRoute jsplugin.Route
	var bestParams map[string]string
	bestStatic := -1
	for _, route := range routes {
		if !strings.EqualFold(strings.TrimSpace(route.Method), normalizedMethod) {
			continue
		}
		tplSegments, valid := splitCleanPath(route.Path)
		if !valid || len(tplSegments) != len(reqSegments) {
			continue
		}
		params := make(map[string]string)
		staticCount := 0
		matched := true
		for i := range tplSegments {
			tpl := tplSegments[i]
			actual := reqSegments[i]
			if strings.HasPrefix(tpl, ":") && len(tpl) > 1 {
				params[tpl[1:]] = actual
				continue
			}
			if tpl != actual {
				matched = false
				break
			}
			staticCount++
		}
		if matched && staticCount > bestStatic {
			bestRoute = route
			bestParams = params
			bestStatic = staticCount
		}
	}
	if bestStatic < 0 {
		return jsplugin.Route{}, nil, false
	}
	return bestRoute, bestParams, true
}

// NativeRouteTaskID extracts the public task ID parameter from matched route
// path parameters using route.TaskIDParam or standard task ID parameter names.
func NativeRouteTaskID(route jsplugin.Route, params map[string]string) string {
	if route.TaskIDParam != "" {
		return strings.TrimSpace(params[route.TaskIDParam])
	}
	for _, name := range []string{"id", "task_id", "taskId", "request_id"} {
		if value := strings.TrimSpace(params[name]); value != "" {
			return value
		}
	}
	return ""
}

func splitCleanPath(path string) ([]string, bool) {
	path = strings.TrimSpace(path)
	if path == "" || !strings.HasPrefix(path, "/") {
		return nil, false
	}
	if strings.Contains(path, "\\") || strings.Contains(path, "\x00") {
		return nil, false
	}
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return []string{}, true
	}
	parts := strings.Split(trimmed, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, false
		}
	}
	return parts, true
}
