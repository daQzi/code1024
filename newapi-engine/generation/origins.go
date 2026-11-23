package generation

import (
	"encoding/json"
	"strings"

	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
)

// Only the validated private parent projection is passed to task hooks. Never
// expose it in task logs, model statistics or client response serialization.
type OriginTask struct {
	TaskID         string          `json:"taskId"`
	UpstreamTaskID string          `json:"upstreamTaskId"`
	Action         string          `json:"action"`
	Status         string          `json:"status"`
	Data           json.RawMessage `json:"data"`
}

func ValidateOrigins(origins []OriginTask) error {
	if len(origins) > pluginTask.MaxOriginTasks {
		return ErrOrigin
	}
	seen := make(map[string]bool, len(origins))
	for _, origin := range origins {
		if len(origin.TaskID) != 46 || !strings.HasPrefix(origin.TaskID, "video_") || len(origin.UpstreamTaskID) == 0 || len(origin.UpstreamTaskID) > 512 || len(origin.Action) > 128 || seen[origin.TaskID] || !json.Valid(origin.Data) {
			return ErrOrigin
		}
		switch origin.Status {
		case "QUEUED", "SUBMITTED", "IN_PROGRESS", "UNKNOWN", "SUCCESS", "FAILURE":
		default:
			return ErrOrigin
		}
		seen[origin.TaskID] = true
	}
	raw, err := json.Marshal(origins)
	if err != nil || len(raw) > pluginTask.MaxSnapshotBytes {
		return ErrOrigin
	}
	return nil
}
