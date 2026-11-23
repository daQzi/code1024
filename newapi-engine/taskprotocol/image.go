// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f.
// See ../NOTICE.md for provenance and local adaptations.

package taskprotocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	maxImageResponseBytes = 16 << 20
	maxImageResponseDepth = 16
	maxImageItems         = 64
)

// NormalizeImageResponse validates a plugin's protocols.openai_image.render
// return value and enforces the host OpenAI Images wire shape ({created, data}).
func NormalizeImageResponse(payload any, createdAt int64) (map[string]any, error) {
	var encoded []byte
	var err error
	if raw, ok := payload.(json.RawMessage); ok {
		encoded = raw
	} else {
		encoded, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("image response is not JSON-compatible: %w", err)
		}
	}
	if len(encoded) > maxImageResponseBytes {
		return nil, fmt.Errorf("image response exceeds %d bytes", maxImageResponseBytes)
	}
	depth, err := pluginJSONDepth(encoded)
	if err != nil || depth > maxImageResponseDepth {
		return nil, fmt.Errorf("image response exceeds depth limit of %d", maxImageResponseDepth)
	}
	var response map[string]any
	if err := json.Unmarshal(encoded, &response); err != nil || response == nil {
		return nil, errors.New("image response must be a JSON object")
	}
	rawData, exists := response["data"]
	if !exists {
		return nil, errors.New("image response data is required")
	}
	items, ok := rawData.([]any)
	if !ok {
		return nil, errors.New("image response data must be an array")
	}
	if len(items) > maxImageItems {
		return nil, fmt.Errorf("image response data exceeds limit of %d items", maxImageItems)
	}
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, errors.New("image response data entries must be objects")
		}
		if urlVal, hasURL := item["url"]; hasURL && urlVal != nil {
			if _, ok := urlVal.(string); !ok {
				return nil, errors.New("image response url must be a string")
			}
		}
		if b64Val, hasB64 := item["b64_json"]; hasB64 && b64Val != nil {
			if _, ok := b64Val.(string); !ok {
				return nil, errors.New("image response b64_json must be a string")
			}
		}
	}
	if created, ok := response["created"].(float64); !ok || created <= 0 {
		response["created"] = createdAt
	}
	return response, nil
}

// InlineImageBase64 populates b64_json for items that only carry url when the
// client requested response_format=b64_json. Fetch errors leave the item's url
// intact, matching pinned New API fallback behavior.
func InlineImageBase64(response map[string]any, fetch func(rawURL string) (string, error)) {
	if response == nil || fetch == nil {
		return
	}
	items, _ := response["data"].([]any)
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		if existing, _ := item["b64_json"].(string); strings.TrimSpace(existing) != "" {
			continue
		}
		rawURL, _ := item["url"].(string)
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			continue
		}
		encoded, err := fetch(rawURL)
		if err != nil || strings.TrimSpace(encoded) == "" {
			continue
		}
		item["b64_json"] = encoded
	}
}
