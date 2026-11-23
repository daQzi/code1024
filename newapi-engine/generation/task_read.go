package generation

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	CanceledBeforeSubmit = "canceled_before_submit"
	CanceledUpstream     = "canceled_upstream"
)

// PublicTaskStatus is the New API task status (shared by public queries and
// the plugin view). Unknown local state never looks like an accepted task.
func PublicTaskStatus(status string) string {
	switch status {
	case Queued:
		return "QUEUED"
	case Submitting:
		return "SUBMITTED"
	case Running:
		return "IN_PROGRESS"
	case Succeeded:
		return "SUCCESS"
	case Failed:
		return "FAILURE"
	default:
		return "UNKNOWN"
	}
}

func IsPublicTaskID(id string) bool {
	if len(id) != 46 || !strings.HasPrefix(id, "video_") {
		return false
	}
	for _, char := range id[6:] {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

type TaskReadView struct {
	TaskID     string `json:"task_id"`
	Platform   string `json:"platform"`
	Status     string `json:"status"`
	Progress   string `json:"progress"`
	FailReason string `json:"fail_reason"`
	CreatedAt  int64  `json:"created_at"`
	FinishedAt int64  `json:"finished_at"`
}

func TaskArtifactInput(t *Task) map[string]any {
	return map[string]any{"data": t.Data, "status": PublicTaskStatus(t.Status), "task_id": t.ID, "state": t.State}
}

func (t *Task) PublicTaskReadView() TaskReadView {
	view := TaskReadView{TaskID: t.ID, Platform: t.Platform, Status: PublicTaskStatus(t.Status), Progress: strconv.Itoa(max(0, min(t.Progress, 100))) + "%"}
	if !t.CreatedAt.IsZero() {
		view.CreatedAt = t.CreatedAt.Unix()
	}
	if t.FinishedAt != nil && !t.FinishedAt.IsZero() {
		view.FinishedAt = t.FinishedAt.Unix()
	}
	if t.Status == Failed {
		view.FailReason = "Task failed"
		if t.ErrorCode == CanceledBeforeSubmit {
			view.FailReason = "The task was canceled before submission."
		} else if t.ErrorCode == CanceledUpstream {
			view.FailReason = "The task was canceled upstream."
		}
	}
	return view
}

type TaskLyric struct {
	Title string `json:"title,omitempty"`
	Text  string `json:"text"`
}

// SunoLyrics returns only the generated text from the stored Suno batch item.
// Raw result fields, provider IDs and download URLs are not part of this view.
func (t *Task) SunoLyrics() []TaskLyric {
	if t.Status != Succeeded || t.Protocol != ProtocolSunoTask || t.Action != "LYRICS" || len(t.Data) > 1<<20 {
		return nil
	}
	var entries []TaskLyric
	if err := json.Unmarshal(t.Data, &entries); err != nil {
		var entry TaskLyric
		if json.Unmarshal(t.Data, &entry) != nil {
			return nil
		}
		entries = []TaskLyric{entry}
	}
	result := make([]TaskLyric, 0, min(16, len(entries)))
	for _, entry := range entries {
		if strings.TrimSpace(entry.Text) != "" && len(entry.Text) <= 16<<10 && len(entry.Title) <= 1024 {
			result = append(result, entry)
			if len(result) == 16 {
				break
			}
		}
	}
	return result
}
