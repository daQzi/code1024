// SPDX-License-Identifier: AGPL-3.0-or-later
// Tool-count accumulation follows QuantumNous/new-api at
// 48f9df96ea7c68eae04a0f3687869f232f48929f,
// relay/channel/providerplugin/adaptor.go and relay/common/tool_usage.go.
package provider

import (
	"errors"
	"maps"
	"strings"
	"unicode"
)

const MaxToolNames = 64
const MaxToolCalls = 10_000

// ToolUsage accumulates per-hook deltas across one logical Provider request.
// A reported zero remains present so it can replace a protocol-inferred count.
// Each request owns its instance; never share it between requests or WS turns.
type ToolUsage struct{ counts map[string]int }

func (u *ToolUsage) Add(counts map[string]int) error {
	if u == nil || len(counts) > MaxToolNames {
		return errors.New("provider tool counts exceed limit")
	}
	newNames := 0
	for name, count := range counts {
		if strings.TrimSpace(name) == "" || len(name) > 128 || strings.IndexFunc(name, unicode.IsControl) >= 0 || count < 0 || count > MaxToolCalls {
			return errors.New("invalid provider tool count")
		}
		if _, exists := u.counts[name]; !exists {
			newNames++
		}
	}
	if len(u.counts)+newNames > MaxToolNames {
		return errors.New("provider request tool names exceed limit")
	}
	if len(counts) == 0 {
		return nil
	}
	if u.counts == nil {
		u.counts = make(map[string]int, len(counts))
	}
	for name, count := range counts {
		u.counts[name] = min(u.counts[name]+count, MaxToolCalls)
	}
	return nil
}

func (u *ToolUsage) Counts() map[string]int {
	if u == nil {
		return nil
	}
	return maps.Clone(u.counts)
}
