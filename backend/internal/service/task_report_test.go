package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

const taskReportSource = `export const meta={apiVersion:1,key:"report-fixture",name:"Report",version:"1.0.0",author:{name:"Fixture"},configSchema:{type:"object",properties:{url:{type:"string",title:"URL",format:"url",default:"https://reports.example.com/callback"}}}};
export function report(ctx,record){
 if(ctx.stage!=="report"||ctx.format!=="task"||record.response!==undefined||record.usage!==undefined)throw new Error("wrong report");
 utils.fetch({url:ctx.config.url,method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({ctx,record})});
}`

type taskReportHTTP struct {
	HTTPUpstream
	requests chan map[string]json.RawMessage
	started  chan struct{}
	block    bool
}

func (h *taskReportHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	if h.started != nil {
		select {
		case h.started <- struct{}{}:
		default:
		}
	}
	if h.block {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	var payload map[string]json.RawMessage
	if err = json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if req.Header.Get("Authorization") != "" {
		return nil, errors.New("account authorization leaked")
	}
	h.requests <- payload
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}

func taskReportFixture(t *testing.T) (*NewAPIPluginService, *requestPluginFixtureRepo, *GenerationTask, *taskReportHTTP) {
	t.Helper()
	s, repo := requestPluginFixture(t, taskReportSource)
	item := requestFixtureItem(3, "global", `{"url":"https://reports.example.com/callback"}`, repo.versions[1])
	item.Stages, item.Formats = []string{"report"}, []string{"task"}
	repo.items = []RequestPluginInstance{item}
	httpClient := &taskReportHTTP{requests: make(chan map[string]json.RawMessage, 16)}
	s.gateway.httpUpstream = httpClient
	call := RequestPluginCall{RequestID: "client-report", UserID: 4, TokenID: 5, TokenName: "client-key-name", GroupID: 7, Group: "7", Model: "public-video", Format: "task", Path: "/v1/videos"}
	plan, err := s.NewRequestPluginPlan(context.Background(), call)
	require.NoError(t, err)
	key := &APIKey{ID: 5, UserID: 4, Group: &Group{ID: 7}}
	sealed := s.snapshotTaskReport(WithRequestPluginPlan(context.Background(), plan), key, call.Model, json.RawMessage(`{"model":"public-video","prompt":"A sea"}`), false)
	require.NotEmpty(t, sealed)
	finished := time.Now().UTC()
	task := &GenerationTask{ID: "video_" + strings.Repeat("a", 40), UserID: 4, APIKeyID: 5, GroupID: 7, AccountID: 9, Model: call.Model, UpstreamModel: "vendor-model", Status: GenerationSucceeded, BillingStatus: GenerationBillingSettled, CreatedAt: finished.Add(-time.Minute), FinishedAt: &finished, RequestReportEncrypted: sealed, Data: json.RawMessage(`{"output":{"url":"https://video.example.com/result.mp4","caption":"A sea"}}`), State: json.RawMessage(`{"api_key":"state-secret"}`), Connection: GenerationConnection{BaseURL: "https://private-upstream.invalid"}}
	s.taskReports = newTaskReportExecutor(s)
	t.Cleanup(s.taskReports.close)
	return s, repo, task, httpClient
}

func awaitTaskReport(t *testing.T, h *taskReportHTTP) map[string]json.RawMessage {
	t.Helper()
	select {
	case payload := <-h.requests:
		return payload
	case <-time.After(5 * time.Second):
		t.Fatal("report not delivered")
		return nil
	}
}

func TestTaskReportAfterSettlementAndCurrentInstanceEnabled(t *testing.T) {
	s, repo, task, h := taskReportFixture(t)
	// Placement changed after submission: IDs, not the new placement, match.
	repo.items[0].Scope = "group"
	repo.items[0].GroupIDs = []int64{99}
	repo.items[0].Models = []string{"different-model"}
	repo.items[0].ConfigEncrypted = `ENC:{"url":"https://reports.example.com/changed"}`
	s.enqueueTaskReport(task, decimal.RequireFromString("0.16"))
	payload := awaitTaskReport(t, h)
	var record map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload["record"], &record))
	require.JSONEq(t, `{"usd":0.16,"quota":80000}`, string(record["cost"]))
	require.Equal(t, `"client-report"`, string(record["requestId"]))
	require.Equal(t, `60000`, string(record["durationMs"]))
	require.NotContains(t, record, "response")
	require.NotContains(t, record, "usage")
	raw, _ := json.Marshal(payload)
	for _, secret := range []string{"state-secret", "private-upstream", "ConfigEncrypted", "RequestReportEncrypted"} {
		require.NotContains(t, string(raw), secret)
	}
	require.Contains(t, string(payload["ctx"]), "changed", "report uses current config as in the New API Task report contract")
	require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 1 }, time.Second, time.Millisecond*5)
	repo.items[0].Enabled = false
	s.enqueueTaskReport(task, decimal.Zero)
	require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 2 }, time.Second, time.Millisecond*5)
	require.Empty(t, h.requests)
}

func TestTaskReportSettlementNotSubmitOwnsCallback(t *testing.T) {
	s, _, task, h := taskReportFixture(t)
	task.BillingStatus = GenerationBillingPending
	task.Usage = json.RawMessage(`{"_host_final_usage":{"seconds":8}}`)
	unit := decimal.RequireFromString("0.02")
	price, err := NewGenerationPriceSnapshot(GenerationPriceRule{Mode: "fixed", Unit: "second", UnitPrice: &unit}, decimal.NewFromInt(8), nil, decimal.NewFromInt(1))
	require.NoError(t, err)
	task.Price = price
	repo := &generationSettlementFixture{}
	generations := &GenerationService{plugins: s, repo: repo}
	require.Empty(t, h.requests)
	require.NoError(t, generations.settle(context.Background(), task))
	awaitTaskReport(t, h)
	require.NoError(t, generations.settle(context.Background(), task))
	require.Eventually(t, func() bool { return s.TaskReportStats().Completed == 1 }, time.Second, time.Millisecond*5)
	require.EqualValues(t, 1, s.TaskReportStats().Accepted)
	// A failed/fully refunded task has the same final-only report contract.
	task.Status = GenerationFailed
	task.BillingStatus = GenerationBillingPending
	task.ErrorMessage = "Request rejected by request-processing policy."
	require.NoError(t, generations.settle(context.Background(), task))
	payload := awaitTaskReport(t, h)
	require.Contains(t, string(payload["record"]), `"status":"FAILURE"`)
	require.Contains(t, string(payload["record"]), `"usd":0`)
}

func TestTaskReportCapacityShutdownAndImmutableJob(t *testing.T) {
	s, _, task, h := taskReportFixture(t)
	h.block = true
	h.started = make(chan struct{}, 2)
	s.enqueueTaskReport(task, decimal.Zero)
	s.enqueueTaskReport(task, decimal.Zero)
	for range 2 {
		select {
		case <-h.started:
		case <-time.After(5 * time.Second):
			t.Fatal("report worker not started")
		}
	}
	// In-flight payloads and queued snapshots share the byte limit too.
	for range taskReportQueueItems + 5 {
		s.enqueueTaskReport(task, decimal.Zero)
	}
	require.EqualValues(t, taskReportQueueItems+2, s.TaskReportStats().Accepted)
	require.EqualValues(t, 5, s.TaskReportStats().Dropped)
	done := make(chan struct{})
	go func() { s.taskReports.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel blocked report HTTP")
	}
	s.taskReports.mu.Lock()
	require.Zero(t, s.taskReports.bytes)
	s.taskReports.mu.Unlock()
	s.enqueueTaskReport(task, decimal.Zero)
	require.EqualValues(t, taskReportQueueItems+2, s.TaskReportStats().Accepted)
}

func TestTaskReportQueueByteLimitAndProjectionOwnership(t *testing.T) {
	s, _, task, h := taskReportFixture(t)
	h.block = true
	h.started = make(chan struct{}, 2)
	for range 2 {
		s.enqueueTaskReport(task, decimal.Zero)
	}
	for range 2 {
		select {
		case <-h.started:
		case <-time.After(5 * time.Second):
			t.Fatal("worker not started")
		}
	}
	task.Data = json.RawMessage(`{"text":"` + strings.Repeat("x", 900000) + `"}`)
	for range 12 {
		s.enqueueTaskReport(task, decimal.Zero)
	}
	require.Less(t, s.TaskReportStats().Accepted, uint64(taskReportQueueItems), "byte budget must reject before the item budget")
	require.Positive(t, s.TaskReportStats().Dropped)
	s.taskReports.mu.Lock()
	require.LessOrEqual(t, s.taskReports.bytes, int64(taskReportQueueBytes))
	queued := <-s.taskReports.queue
	s.taskReports.mu.Unlock()
	before := string(queued.task.Data)
	task.Data[10] = 'z'
	require.Equal(t, before, string(queued.task.Data), "queue owns immutable result bytes")
	require.Empty(t, queued.task.State)
	require.Empty(t, queued.task.Connection)
	s.taskReports.release(queued.size)
}

func TestTaskReportProjectionLimitsAndMedia(t *testing.T) {
	body, media := taskReportRequest(json.RawMessage(`{"prompt":"sea","input_reference":"data:image/png;base64,YWJj"}`))
	require.JSONEq(t, `{"prompt":"sea","input_reference":"[media:0]"}`, string(body))
	require.Equal(t, []jsplugin.TransformMedia{{Index: 0, Path: "/input_reference", MimeType: "image/png", Bytes: 3}}, media)
	body, media = taskReportRequest(json.RawMessage(`{"prompt":"` + strings.Repeat("x", maxTaskReportRequest) + `"}`))
	require.Empty(t, body)
	require.Empty(t, media)
	data, _ := json.Marshal(map[string]any{"url": "https://example.com/video", "again": "https://example.com/video", "media": "data:video/mp4;base64,AAAA", "base64": strings.Repeat("a", 1024), "caption": "Sea"})
	result := projectTaskReportResult(data)
	require.Equal(t, []string{"https://example.com/video"}, result.URLs)
	require.Equal(t, []taskReportText{{Path: "caption", Text: "Sea"}}, result.Texts)
	result = projectTaskReportResult(data, "https://example.com/primary")
	require.Equal(t, []string{"https://example.com/primary", "https://example.com/video"}, result.URLs)
	texts := make([]string, 257)
	for i := range texts {
		texts[i] = "ordinary text"
	}
	data, _ = json.Marshal(texts)
	result = projectTaskReportResult(data)
	require.True(t, result.Truncated)
	require.Len(t, result.Texts, 256)
}

func TestTaskReportDryRunNeverInvokesNetworkAndUnsafeTargetRejected(t *testing.T) {
	s, _, _, h := taskReportFixture(t)
	_, err := s.DryRunRequestPluginStage(context.Background(), 1, json.RawMessage(`{}`), json.RawMessage(`{}`), RequestPluginCall{Format: "task"}, "report")
	require.Error(t, err)
	_, err = s.requestPluginFetcher([]string{"reports.example.com"})(context.Background(), jsplugin.FetchRequest{URL: "https://foreign.example.com/leak", Method: "POST"})
	require.Error(t, err)
	require.Empty(t, h.requests)
	for _, address := range []string{"https://name:secret@reports.example.com/callback", "https://reports.example.com/callback#fragment", "file:///tmp/report"} {
		_, err = s.requestPluginFetcher([]string{"reports.example.com"})(context.Background(), jsplugin.FetchRequest{URL: address})
		require.Error(t, err)
	}
}

type taskReportFailedSettlement struct{ GenerationTaskRepository }

func (taskReportFailedSettlement) Settle(context.Context, *GenerationTask, decimal.Decimal, *GenerationCost) error {
	return errors.New("billing unavailable")
}

func TestTaskReportFinancialFailureNeverQueuesAndConfigScopeIsExplicit(t *testing.T) {
	s, _, task, h := taskReportFixture(t)
	task.BillingStatus = GenerationBillingPending
	task.Status = GenerationFailed
	generations := &GenerationService{plugins: s, repo: taskReportFailedSettlement{}}
	require.Error(t, generations.settle(context.Background(), task))
	require.Zero(t, s.TaskReportStats().Accepted)
	require.Empty(t, h.requests)
	input := RequestPluginInstance{Name: "Report", PluginVersionID: 1, Scope: "global", Stages: []string{"report"}, Formats: []string{"task"}, Config: json.RawMessage(`{}`)}
	_, err := s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.NoError(t, err)
	input.Formats = nil
	_, err = s.SaveRequestPluginInstance(context.Background(), input, 4)
	require.Error(t, err)
}

func TestTaskReportRecordsSurviveEgressAndLargeOrFormRequestStaysNull(t *testing.T) {
	s, _, task, _ := taskReportFixture(t)
	record := RequestPluginRecord{InstanceID: 7, VersionID: 2, Stage: "egress", Action: "reject"}
	s.appendTaskReportRecords(task, []RequestPluginRecord{record})
	snapshot, err := s.openTaskReport(task)
	require.NoError(t, err)
	require.Equal(t, []RequestPluginRecord{record}, snapshot.Records)
	key := &APIKey{ID: 5, UserID: 4, Group: &Group{ID: 7}}
	plan, err := s.NewRequestPluginPlan(context.Background(), snapshot.Call)
	require.NoError(t, err)
	for _, form := range []bool{false, true} {
		body := json.RawMessage(`{"prompt":"` + strings.Repeat("x", maxTaskReportRequest) + `"}`)
		task.RequestReportEncrypted = s.snapshotTaskReport(WithRequestPluginPlan(context.Background(), plan), key, task.Model, body, form)
		snapshot, err = s.openTaskReport(task)
		require.NoError(t, err)
		require.JSONEq(t, `null`, string(snapshot.Body))
	}
	s.setTaskReportResultURL(task, "https://video.example.com/main.mp4")
	snapshot, err = s.openTaskReport(task)
	require.NoError(t, err)
	require.Equal(t, "https://video.example.com/main.mp4", snapshot.ResultURL)
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), pkghttputil.NewBodyMemoryBudget(1<<20))
	defer release()
	before := s.TaskReportStats().Dropped
	sealed := s.snapshotTaskReport(WithRequestPluginPlan(ctx, plan), key, task.Model, json.RawMessage(`{}`), false)
	require.Empty(t, sealed)
	require.Equal(t, before+1, s.TaskReportStats().Dropped)
}
