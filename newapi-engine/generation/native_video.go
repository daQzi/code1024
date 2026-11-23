package generation

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

const (
	ProtocolOpenAI          = "openai_video"
	ProtocolSunoTask        = "suno_task"
	ProtocolGenericTask     = "generic_task"
	ProtocolOpenAIResponses = "openai_responses"
	ProtocolOpenAIImage     = "openai_image"
	ProtocolNativePrefix    = "native:"
	ProtocolXAI             = "xai_video"
	ProtocolXAIEdit         = "xai_video_edit"
	ProtocolXAIExtend       = "xai_video_extension"
	ProtocolArk             = "ark_video"
)

func (t *Task) ContentURL() string { return "/v1/videos/" + t.ID + "/content" }

// NativeVideoView is the host-owned response and privacy boundary for native
// rendering. Opaque plugin data, state and signed upstream URLs never escape.
func (t *Task) NativeVideoView(create bool) (int, map[string]any) {
	if t.Protocol == ProtocolArk {
		view := map[string]any{"id": t.ID}
		if create {
			return http.StatusOK, view
		}
		status := map[string]string{Queued: "queued", Submitting: "queued", Running: "running", Uncertain: "running", Succeeded: "succeeded", Failed: "failed"}[t.Status]
		if status == "" {
			status = "queued"
		}
		view["status"], view["model"] = status, t.Model
		view["created_at"], view["updated_at"] = t.CreatedAt.Unix(), t.UpdatedAt.Unix()
		if t.Status == Succeeded && t.DeliveryStatus == DeliveryReady {
			view["content"] = map[string]any{"video_url": t.ContentURL()}
		}
		if t.Status == Failed {
			view["error"] = map[string]any{"code": t.ErrorCode, "message": t.ErrorMessage}
		}
		if t.Status == Succeeded {
			usage := nativeVideoPublicNumbers(t.Data, "usage.completion_tokens", "usage.total_tokens")
			if len(usage) > 0 {
				view["usage"] = usage
			}
		}
		return http.StatusOK, view
	}
	if create {
		return http.StatusOK, map[string]any{"request_id": t.ID}
	}
	if t.Status == Failed {
		return http.StatusOK, map[string]any{"status": "failed", "error": map[string]any{"code": t.ErrorCode, "message": t.ErrorMessage}}
	}
	if t.Status == Succeeded {
		video := map[string]any{}
		if t.DeliveryStatus == DeliveryReady {
			video["url"] = t.ContentURL()
		}
		if t.DeliveryStatus == "failed" || gjson.GetBytes(t.Data, "video.respect_moderation").Type == gjson.False {
			video["respect_moderation"] = false
		}
		if duration := gjson.GetBytes(t.Data, "video.duration"); duration.Type == gjson.Number && duration.Num > 0 && duration.Num <= 3600 {
			video["duration"] = duration.Num
		}
		view := map[string]any{"status": "done", "video": video}
		if usage := nativeVideoPublicNumbers(t.Data, "usage.cost_in_usd_ticks"); len(usage) > 0 {
			view["usage"] = usage
		}
		// The driver marks a vendor-confirmed charged failure as a billable
		// success. Keep the native failed+usage shape for downstream gateways.
		if gjson.GetBytes(t.Data, "status").String() == "failed" {
			view["status"] = "failed"
			view["error"] = map[string]any{"code": "video_generation_failed", "message": "The upstream video was not delivered."}
			delete(view, "video")
		}
		return http.StatusOK, view
	}
	return http.StatusAccepted, map[string]any{"status": "pending", "progress": min(max(t.Progress, 0), 99)}
}

func nativeVideoPublicNumbers(raw json.RawMessage, paths ...string) map[string]any {
	values := make(map[string]any)
	for _, path := range paths {
		value := gjson.GetBytes(raw, path)
		if value.Type == gjson.Number && value.Num >= 0 && value.Num <= 1<<53 && value.Num == float64(value.Int()) {
			values[path[strings.LastIndex(path, ".")+1:]] = value.Int()
		}
	}
	return values
}
