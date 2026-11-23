package taskprotocol

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
)

func TestMatchNativeRouteAndTaskIDExtraction(t *testing.T) {
	routes := []jsplugin.Route{
		{Method: "POST", Path: "/v1/jobs", Type: jsplugin.RouteTypeSubmit, Decode: "decodeSubmit", Render: "renderSubmit"},
		{Method: "GET", Path: "/v1/jobs/:job_id", Type: jsplugin.RouteTypeQuery, TaskIDParam: "job_id", Render: "renderQuery"},
		{Method: "GET", Path: "/v1/jobs/latest", Type: jsplugin.RouteTypeQuery, Render: "renderLatest"},
	}
	r, params, ok := MatchNativeRoute(routes, "get", "/v1/jobs/video_abc")
	if !ok || r.Render != "renderQuery" || NativeRouteTaskID(r, params) != "video_abc" {
		t.Fatalf("unexpected param match: %+v %+v %v", r, params, ok)
	}
	r2, _, ok := MatchNativeRoute(routes, "GET", "/v1/jobs/latest")
	if !ok || r2.Render != "renderLatest" {
		t.Fatalf("expected static route precedence, got %+v %v", r2, ok)
	}
	if _, _, ok := MatchNativeRoute(routes, "GET", "/v1/../secret"); ok {
		t.Fatalf("expected path traversal rejection")
	}
}

func TestParseNativeReplyWrapperValidation(t *testing.T) {
	plain, err := ParseNativeReply(http.StatusOK, map[string]any{"task_id": "video_1"})
	if err != nil || plain.Status != http.StatusOK || !plain.HasBody {
		t.Fatalf("unexpected plain reply: %+v %v", plain, err)
	}
	wrapped, err := ParseNativeReply(http.StatusOK, map[string]any{
		"__status":  float64(202),
		"__headers": map[string]any{"retry-after": "3", "location": "/v1/tasks/video_1"},
		"__body":    map[string]any{"status": "pending"},
	})
	if err != nil || wrapped.Status != 202 || wrapped.Headers["Retry-After"] != "3" || wrapped.Headers["Location"] != "/v1/tasks/video_1" || !wrapped.HasBody {
		t.Fatalf("unexpected wrapped reply: %+v %v", wrapped, err)
	}
	noBody, err := ParseNativeReply(http.StatusOK, map[string]any{"__status": float64(204)})
	if err != nil || noBody.Status != 204 || noBody.HasBody {
		t.Fatalf("unexpected no-body reply: %+v %v", noBody, err)
	}
	// Reject forbidden header, bad status, unknown key
	if _, err := ParseNativeReply(200, map[string]any{"__status": float64(200), "__headers": map[string]any{"Set-Cookie": "x=1"}}); err == nil {
		t.Fatalf("expected forbidden header error")
	}
	if _, err := ParseNativeReply(200, map[string]any{"__status": float64(199)}); err == nil {
		t.Fatalf("expected invalid status error")
	}
	if _, err := ParseNativeReply(200, map[string]any{"__status": float64(200), "extra": true}); err == nil {
		t.Fatalf("expected unknown member error")
	}
}
