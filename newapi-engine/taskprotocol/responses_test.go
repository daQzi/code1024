package taskprotocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskIDFromResponseID(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"resp_video_0123456789abcdef0123456789abcdef01234567", "video_0123456789abcdef0123456789abcdef01234567", true},
		{"resp_abc123", "task_abc123", true},
		{"resp_", "", false},
		{"video_123", "", false},
	}
	for _, tc := range cases {
		got, ok := TaskIDFromResponseID(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("TaskIDFromResponseID(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPluginResponsesMachineStreamLifecycleAndFinal(t *testing.T) {
	m := NewPluginResponsesMachine("video_0123456789abcdef0123456789abcdef01234567", "deep-research", 1710000000, PluginProtocolLimits{})
	if m.ResponseID() != "resp_video_0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("unexpected response ID: %s", m.ResponseID())
	}
	created, err := m.CreatedEvent()
	if err != nil || created.Type != "response.created" || created.SequenceNumber != 0 {
		t.Fatalf("CreatedEvent failed: %+v %v", created, err)
	}

	res1, err := DecodePluginProtocolEventResult(json.RawMessage(`{"events":[{"type":"progress","progress":40,"message":"working"}],"state":{"step":1},"done":false}`), PluginProtocolLimits{})
	if err != nil || !res1.State.Present || res1.State.Null {
		t.Fatalf("DecodePluginProtocolEventResult tick 1 failed: %+v %v", res1, err)
	}
	evs1, err := m.ApplyTick(res1, "IN_PROGRESS")
	if err != nil || len(evs1) != 1 || evs1[0].Type != "response.in_progress" || evs1[0].SequenceNumber != 1 {
		t.Fatalf("ApplyTick 1 failed: %+v %v", evs1, err)
	}

	res2, err := DecodePluginProtocolEventResult(json.RawMessage(`{"events":[{"type":"output","data":"hello world"}],"done":true}`), PluginProtocolLimits{})
	if err != nil {
		t.Fatalf("DecodePluginProtocolEventResult tick 2 failed: %v", err)
	}
	evs2, err := m.ApplyTick(res2, "SUCCESS")
	if err != nil || len(evs2) != 7 {
		t.Fatalf("ApplyTick 2 expected 7 events, got %d (%v)", len(evs2), err)
	}
	if evs2[len(evs2)-1].Type != "response.completed" || !m.Terminal() {
		t.Fatalf("expected completed terminal state, got %+v", evs2[len(evs2)-1])
	}

	// Unknown fields fail closed
	if _, err := DecodePluginProtocolEventResult(json.RawMessage(`{"events":[],"done":false,"extra":1}`), PluginProtocolLimits{}); err == nil {
		t.Fatalf("expected unknown field error")
	}
}

func TestPluginResponsesMachineFinalResponseAndFinalFromEvents(t *testing.T) {
	m := NewPluginResponsesMachine("video_0123456789abcdef0123456789abcdef01234567", "m1", 100, PluginProtocolLimits{})
	final, err := m.FinalResponse(map[string]any{
		"id":     "forged",
		"status": "failed",
		"output": []any{map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{map[string]any{
				"type":        "output_text",
				"text":        "done",
				"annotations": []any{},
				"logprobs":    []any{},
			}},
		}},
		"metadata": map[string]any{"task_id": "forged", "custom": "ok"},
	}, "SUCCESS")
	if err != nil {
		t.Fatalf("FinalResponse failed: %v", err)
	}
	if final["id"] != m.ResponseID() || final["status"] != "completed" {
		t.Fatalf("host fields not enforced: %+v", final)
	}
	meta := final["metadata"].(map[string]string)
	if meta["task_id"] != m.TaskID() || meta["custom"] != "ok" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}

	// FinalFromEvents for stream-only plugin
	m2 := NewPluginResponsesMachine("video_0123456789abcdef0123456789abcdef01234567", "m1", 100, PluginProtocolLimits{})
	evRes, err := DecodePluginProtocolEventResult(json.RawMessage(`{"events":[{"type":"output","data":"streamed"}],"done":true}`), PluginProtocolLimits{})
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	synth, err := m2.FinalFromEvents(evRes, "SUCCESS")
	if err != nil || synth["status"] != "completed" {
		t.Fatalf("FinalFromEvents failed: %+v %v", synth, err)
	}

	// PendingResponse maps background flag cleanly
	m3 := NewPluginResponsesMachine("video_0123456789abcdef0123456789abcdef01234567", "m1", 100, PluginProtocolLimits{})
	m3.SetBackground(true)
	pending := m3.PendingResponse("QUEUED")
	if pending["status"] != "queued" || pending["background"] != true {
		t.Fatalf("unexpected pending response: %+v", pending)
	}
	if !strings.HasPrefix(pending["metadata"].(map[string]string)["retrieval_path"], "/v1/responses/resp_video_") {
		t.Fatalf("unexpected retrieval_path: %+v", pending["metadata"])
	}
}
