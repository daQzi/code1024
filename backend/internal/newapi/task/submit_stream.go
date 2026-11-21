// SPDX-License-Identifier: AGPL-3.0-or-later
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f,
// relay/channel/task/jsplugin/submit_stream.go. See ../NOTICE.md.
package task

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/jsplugin"
)

type SubmitEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
	Data  string `json:"data"`
}

type SubmitEventHook func(context.Context, SubmitEvent, json.RawMessage) (json.RawMessage, error)

// ReadSubmitEvents returns only a result explicitly completed by the plugin.
// An EOF, timeout or invalid delta never turns partial billable work into a
// successful task. The caller owns reconciliation after an accepted HTTP POST.
func ReadSubmitEvents(parent context.Context, body io.ReadCloser, delta bool, idle time.Duration, hook SubmitEventHook) (json.RawMessage, error) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	stopClose := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stopClose()
	if idle <= 0 {
		idle = 30 * time.Second
	}
	timer := time.AfterFunc(idle, func() { cancel(errors.New("task submit stream idle timeout")) })
	defer timer.Stop()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), MaxSnapshotBytes+2)
	var accumulator *jsplugin.JSONState
	if delta {
		accumulator = jsplugin.NewJSONState(MaxSnapshotBytes)
	}
	state := json.RawMessage("null")
	var data strings.Builder
	var name, id string
	frameBytes, totalBytes, events := 0, 0, 0
	firstLine, hasData := true, false
	for scanner.Scan() {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		timer.Reset(idle)
		line := scanner.Text()
		if firstLine {
			line = strings.TrimPrefix(line, "\ufeff")
			firstLine = false
		}
		frameBytes += len(line) + 1
		totalBytes += len(line) + 1
		if frameBytes > MaxSnapshotBytes || totalBytes > 32<<20 {
			return nil, errors.New("task submit stream exceeds byte limit")
		}
		if line != "" {
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "data":
				if hasData {
					data.WriteByte('\n')
				}
				data.WriteString(value)
				hasData = true
			case "event":
				name = value
			case "id":
				if !strings.ContainsRune(value, '\x00') {
					id = value
				}
			}
			continue
		}
		frameBytes = 0
		events++
		if events > 4096 {
			return nil, errors.New("task submit stream exceeds event limit")
		}
		if !hasData {
			name = ""
			continue
		}
		if name == "" {
			name = "message"
		}
		raw, err := hook(ctx, SubmitEvent{Event: name, ID: id, Data: data.String()}, state)
		if err != nil {
			return nil, err
		}
		var result map[string]json.RawMessage
		if len(raw) > MaxSnapshotBytes || json.Unmarshal(raw, &result) != nil {
			return nil, errors.New("invalid submit event result")
		}
		var done bool
		if len(result["state"]) == 0 || string(result["done"]) != "true" && string(result["done"]) != "false" || json.Unmarshal(result["done"], &done) != nil {
			return nil, errors.New("submit event must return state and boolean done")
		}
		if delta {
			if len(result) != 3 || len(result["changes"]) == 0 || len(result["state"]) > 64<<10 {
				return nil, errors.New("invalid submit delta result")
			}
			var changes any
			if json.Unmarshal(result["changes"], &changes) != nil {
				return nil, errors.New("invalid submit delta changes")
			}
			if err := accumulator.Apply(ctx, changes); err != nil {
				return nil, err
			}
		}
		state = result["state"]
		if done {
			if err := context.Cause(ctx); err != nil {
				return nil, err
			}
			if delta {
				value, err := accumulator.Value()
				if err != nil {
					return nil, err
				}
				return json.Marshal(value)
			}
			return state, nil
		}
		data.Reset()
		hasData, name = false, ""
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("task submit stream ended before completion")
}
