package taskprotocol

import (
	"errors"
	"testing"
)

func TestNormalizeImageResponseAndInlineBase64(t *testing.T) {
	resp, err := NormalizeImageResponse(map[string]any{
		"data": []any{
			map[string]any{"url": "https://cdn.example/a.png"},
			map[string]any{"url": "https://cdn.example/b.png"},
		},
	}, 1710000123)
	if err != nil {
		t.Fatalf("NormalizeImageResponse failed: %v", err)
	}
	if resp["created"] != int64(1710000123) {
		t.Fatalf("expected created=1710000123, got %v", resp["created"])
	}

	InlineImageBase64(resp, func(rawURL string) (string, error) {
		if rawURL == "https://cdn.example/a.png" {
			return "QUFB", nil
		}
		return "", errors.New("unreachable")
	})
	items := resp["data"].([]any)
	if items[0].(map[string]any)["b64_json"] != "QUFB" {
		t.Fatalf("expected b64_json inlined for item 0")
	}
	if _, exists := items[1].(map[string]any)["b64_json"]; exists {
		t.Fatalf("expected failed download to leave item 1 without b64_json")
	}

	if _, err := NormalizeImageResponse(map[string]any{}, 1); err == nil {
		t.Fatalf("expected missing data error")
	}
}
