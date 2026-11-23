// SPDX-License-Identifier: AGPL-3.0-or-later
// Outcome semantics follow QuantumNous/new-api pkg/perf_metrics/outcome.go at
// 48f9df96ea7c68eae04a0f3687869f232f48929f. Copyright (C) 2023-2026 QuantumNous.
package relaymetrics

import (
	"strings"

	"github.com/tidwall/gjson"
)

// Outcome is a scalar observation, independent of usage settlement. Unknown
// means that no terminal protocol evidence was observed; it is never success.
type Outcome uint8

const (
	Unknown Outcome = iota
	Success
	Failure
	Ignored
)

// Terminal reads an existing bounded Responses frame. It retains no payload or
// provider error strings, including when a stream is drained after disconnect.
func Terminal(body []byte) Outcome {
	if !gjson.ValidBytes(body) {
		return Unknown
	}
	event := gjson.GetBytes(body, "type").String()
	status := gjson.GetBytes(body, "response.status").String()
	switch event {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "error":
	default:
		return Unknown
	}
	if event == "response.cancelled" || event == "response.canceled" || status == "cancelled" || status == "canceled" {
		return Ignored
	}
	code := firstString(body, "response.error.code", "error.code", "code")
	errorType := firstString(body, "response.error.type", "error.type")
	if code != "" || errorType != "" || event == "error" || event == "response.failed" || status == "failed" {
		if IgnoreFailure(code, errorType, 0) {
			return Ignored
		}
		return Failure
	}
	if event == "response.incomplete" || status == "incomplete" {
		switch firstString(body, "response.incomplete_details.reason", "incomplete_details.reason") {
		case "max_output_tokens", "max_tokens":
			return Success
		case "content_filter", "safety", "content_policy_violation":
			return Ignored
		default:
			return Failure
		}
	}
	return Success
}

func firstString(body []byte, paths ...string) string {
	for _, path := range paths {
		if value := gjson.GetBytes(body, path); value.Type == gjson.String && value.Str != "" {
			return value.Str
		}
	}
	return ""
}

func HTTPFailure(body []byte, status int) Outcome {
	if IgnoreFailure(firstString(body, "response.error.code", "error.code", "code"), firstString(body, "response.error.type", "error.type"), status) {
		return Ignored
	}
	return Failure
}

func IgnoreFailure(code, errorType string, status int) bool {
	values := [2]string{strings.ToLower(code), strings.ToLower(errorType)}
	// A specific upstream credential/capacity error overrides a generic
	// invalid_request_error, even when the provider incorrectly uses HTTP 400.
	for _, value := range values {
		switch value {
		case "invalid_api_key", "api_key_invalid", "api_key_expired", "api_key_service_blocked", "invalid_authentication", "authentication_error", "unauthenticated", "permission_denied", "permission_error", "access_denied", "insufficient_quota", "quota_exceeded", "resource_exhausted", "rate_limit_exceeded", "rate_limit_error", "overloaded_error", "server_error", "internal_error", "service_unavailable", "model_not_found", "insufficient_user_quota", "pre_consume_token_quota_failed":
			return false
		}
	}
	for _, value := range values {
		switch value {
		case "context_length_exceeded", "invalid_request", "invalid_request_error", "invalid_argument", "sensitive_words_detected", "prompt_blocked", "content_filter", "content_policy_violation", "safety", "context_canceled", "context_cancelled":
			return true
		}
		if strings.HasPrefix(value, "violation_fee.") {
			return true
		}
	}
	switch status {
	case 400, 405, 409, 413, 415, 422, 499:
		return true
	}
	return false
}
