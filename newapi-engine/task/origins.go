// SPDX-License-Identifier: AGPL-3.0-or-later
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f,
// middleware/task_plugin.go. See ../NOTICE.md.
package task

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

const MaxOriginTasks = 16

// OriginTaskIDs validates the decoder's intent before channel selection. Only
// explicit decoder references count; the host never scans arbitrary user text.
func OriginTaskIDs(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var values []json.RawMessage
	if len(raw) > 16<<10 || json.Unmarshal(raw, &values) != nil || values == nil || len(values) > MaxOriginTasks {
		return nil, errors.New("invalid origin task ids")
	}
	ids := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, raw := range values {
		var id string
		if json.Unmarshal(raw, &id) != nil {
			return nil, errors.New("invalid origin task ids")
		}
		id = strings.TrimSpace(id)
		if id == "" || !utf8.ValidString(id) || utf8.RuneCountInString(id) > 128 {
			return nil, errors.New("invalid origin task ids")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, nil
}
