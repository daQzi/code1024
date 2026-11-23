package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/shopspring/decimal"
)

const taskReportQueueItems = 32
const taskReportQueueBytes = 8 << 20

type taskReportJob struct {
	relay  *relayReportJob
	task   GenerationTask // explicit immutable projection below, never a handler
	amount decimal.Decimal
	size   int64
}

type TaskReportStats struct {
	Accepted  uint64 `json:"accepted"`
	Completed uint64 `json:"completed"`
	Dropped   uint64 `json:"dropped"`
	Failed    uint64 `json:"failed"`
}

type taskReportExecutor struct {
	service                              *NewAPIPluginService
	ctx                                  context.Context
	cancel                               context.CancelFunc
	queue                                chan taskReportJob
	mu                                   sync.Mutex
	bytes                                int64 // queued + in-flight jobs, released after the whole job
	closed                               bool
	wg                                   sync.WaitGroup
	once                                 sync.Once
	accepted, completed, dropped, failed atomic.Uint64
	warningAt                            atomic.Int64
	failureWarningAt                     atomic.Int64
}

func newTaskReportExecutor(service *NewAPIPluginService) *taskReportExecutor {
	ctx, cancel := context.WithCancel(context.Background())
	e := &taskReportExecutor{service: service, ctx: ctx, cancel: cancel, queue: make(chan taskReportJob, taskReportQueueItems)}
	for range 2 {
		e.wg.Add(1)
		go e.work()
	}
	return e
}

func (e *taskReportExecutor) close() {
	e.once.Do(func() {
		e.mu.Lock()
		e.closed = true
		e.cancel()
		e.mu.Unlock()
	})
	e.wg.Wait()
	for {
		select {
		case job := <-e.queue:
			e.release(job.size)
			e.dropped.Add(1)
		default:
			return
		}
	}
}

func (s *NewAPIPluginService) TaskReportStats() TaskReportStats {
	if s == nil || s.taskReports == nil {
		return TaskReportStats{}
	}
	e := s.taskReports
	return TaskReportStats{Accepted: e.accepted.Load(), Completed: e.completed.Load(), Dropped: e.dropped.Load(), Failed: e.failed.Load()}
}

func (s *NewAPIPluginService) dropTaskReport(class string) {
	if s == nil || s.taskReports == nil {
		return
	}
	e := s.taskReports
	e.dropped.Add(1)
	now := time.Now().Unix()
	previous := e.warningAt.Load()
	if now-previous >= 60 && e.warningAt.CompareAndSwap(previous, now) {
		slog.Warn("task_plugin_report_dropped", "class", class, "dropped", e.dropped.Load())
	}
}

func (s *NewAPIPluginService) enqueueTaskReport(task *GenerationTask, amount decimal.Decimal) {
	if task.RequestReportEncrypted == "" {
		return
	}
	if s.taskReports == nil {
		return
	}
	e := s.taskReports
	size := int64(len(task.RequestReportEncrypted) + len(task.Data) + 4096)
	if len(task.RequestReportEncrypted) > maxTaskReportEncrypted || len(task.Data) > MaxRequestPluginBody || amount.IsNegative() || task.BillingStatus != GenerationBillingSettled || task.Status != GenerationSucceeded && task.Status != GenerationFailed || task.FinishedAt == nil {
		s.dropTaskReport("snapshot")
		return
	}
	e.mu.Lock()
	if e.closed || e.bytes+size > taskReportQueueBytes || len(e.queue) == cap(e.queue) {
		e.mu.Unlock()
		s.dropTaskReport("capacity")
		return
	}
	finished := *task.FinishedAt
	// Reserve before copying. The queue owns only these facts, with no
	// original request, funding, state, credentials, connection or file data.
	e.bytes += size
	job := taskReportJob{size: size, amount: amount, task: GenerationTask{
		ID: task.ID, UserID: task.UserID, APIKeyID: task.APIKeyID, GroupID: task.GroupID, AccountID: task.AccountID,
		Model: task.Model, UpstreamModel: task.UpstreamModel, Status: task.Status, ErrorCode: task.ErrorCode, ErrorMessage: task.ErrorMessage,
		CreatedAt: task.CreatedAt, FinishedAt: &finished, RequestReportEncrypted: task.RequestReportEncrypted, Data: bytes.Clone(task.Data),
	}}
	e.queue <- job // capacity checked under the sole producer lock; never waits
	e.accepted.Add(1)
	e.mu.Unlock()
}

func (e *taskReportExecutor) release(size int64) { e.mu.Lock(); e.bytes -= size; e.mu.Unlock() }
func (e *taskReportExecutor) work() {
	defer e.wg.Done()
	for {
		if e.ctx.Err() != nil {
			return
		}
		select {
		case <-e.ctx.Done():
			return
		case job := <-e.queue:
			if e.ctx.Err() != nil {
				e.release(job.size)
				e.dropped.Add(1)
				return
			}
			e.run(job)
			e.release(job.size)
			e.completed.Add(1)
		}
	}
}

func (e *taskReportExecutor) run(job taskReportJob) {
	if job.relay != nil {
		e.runRelay(job.relay)
		return
	}
	s := e.service
	snapshot, err := s.openTaskReport(&job.task)
	if err != nil {
		s.dropTaskReport("snapshot")
		return
	}
	repo, err := s.requestPluginRepo()
	if err != nil {
		s.dropTaskReport("repository")
		return
	}
	requestID := snapshot.Call.RequestID
	if requestID == "" {
		requestID = job.task.ID
	}
	status := "SUCCESS"
	if job.task.Status == GenerationFailed {
		status = "FAILURE"
	}
	task := map[string]any{"id": job.task.ID, "status": status, "finishedAt": job.task.FinishedAt.UnixMilli(), "result": projectTaskReportResult(job.task.Data, snapshot.ResultURL)}
	if status == "FAILURE" {
		task["reason"] = job.task.ErrorMessage
	}
	user := jsplugin.TransformUser{ID: int(job.task.UserID), Username: snapshot.Username}
	token := jsplugin.TransformToken{ID: int(job.task.APIKeyID), Name: snapshot.Call.TokenName}
	request := map[string]any{"body": snapshot.Body}
	if len(snapshot.Media) > 0 {
		request["media"] = snapshot.Media
	}
	if snapshot.Records == nil {
		snapshot.Records = []RequestPluginRecord{}
	}
	duration := job.task.FinishedAt.Sub(job.task.CreatedAt).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	record := map[string]any{
		"requestId": requestID, "user": user, "token": token, "startedAt": job.task.CreatedAt.UnixMilli(),
		"durationMs": duration,
		"request":    request, "task": task,
		"cost": newAPIReportCost(job.amount), "requestPlugins": snapshot.Records,
	}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > runner.MaxResultBytes {
		s.dropTaskReport("record_limit")
		return
	}
	for _, id := range snapshot.Instances {
		if e.ctx.Err() != nil {
			return
		}
		lookupCtx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
		instance, lookupErr := repo.GetRequestPluginInstance(lookupCtx, id)
		cancel()
		if lookupErr != nil {
			e.reportFailure(id, "lookup")
			continue
		}
		// Match placement only at submission. Current enable/config is checked
		// now, as in New API Task reports; disabling stops future callbacks.
		if !instance.Enabled || instance.Version == nil || !slices.Contains(instance.Stages, "report") || !slices.Contains(instance.Version.Capabilities.Hooks, "report") {
			continue
		}
		config, configErr := s.decryptRequestPluginConfig(instance.ConfigEncrypted)
		if configErr != nil || len(config) > 64<<10 {
			e.reportFailure(id, "config")
			continue
		}
		callCtx, callCancel := context.WithTimeout(e.ctx, 35*time.Second)
		err = s.pool.With(callCtx, instance.Version.Source, func(client *runner.Client) error {
			validated, err := client.ValidateConfig(callCtx, config, true)
			if err != nil {
				return errors.New("invalid report config")
			}
			hosts := append(slices.Clone(instance.Version.Meta.AllowedHosts), client.Meta.ConfigSchema.URLHosts(validated)...)
			_, err = client.Typed(callCtx, s.requestPluginFetcher(hosts), "CallReport", jsplugin.TransformContext{
				Stage: "report", Format: "task", Path: snapshot.Call.Path, Model: job.task.Model, UpstreamModel: job.task.UpstreamModel,
				Group: snapshot.Call.Group, Channel: &jsplugin.TransformChannel{ID: int(job.task.AccountID), Type: 61}, Attempt: 0, User: user, Token: &token,
				Instance: jsplugin.TransformInstance{ID: strconv.FormatInt(instance.ID, 10), Name: instance.Name}, Config: validated,
			}, json.RawMessage(raw))
			return err
		})
		callCancel()
		if err != nil {
			e.reportFailure(id, "execution")
		}
	}
}

func (e *taskReportExecutor) reportFailure(id int64, class string) {
	e.failed.Add(1)
	// No exception, body, config, URL, account Key or plugin output is logged.
	now := time.Now().Unix()
	previous := e.failureWarningAt.Load()
	if now-previous >= 60 && e.failureWarningAt.CompareAndSwap(previous, now) {
		slog.Warn("task_plugin_report_failed", "instance_id", id, "class", class, "failed", e.failed.Load())
	}
}

func (s *NewAPIPluginService) requestPluginFetcher(hosts []string) jsplugin.Fetcher {
	return func(ctx context.Context, f jsplugin.FetchRequest) (jsplugin.FetchResponse, error) {
		parsed, parseErr := url.Parse(f.URL)
		if parseErr != nil || parsed.User != nil || parsed.Fragment != "" || parsed.Scheme != "https" && parsed.Scheme != "http" {
			return jsplugin.FetchResponse{}, errors.New("invalid request plugin service target")
		}
		if s.gateway == nil || s.gateway.httpUpstream == nil || f.BodyStream != nil && (f.BodySize < 0 || f.BodySize > 32<<20) || len(f.URL) > 8192 || jsplugin.ValidateServiceURL(f.URL, hosts) != nil || jsplugin.ValidateRequestHeaders(f.Headers) != nil {
			return jsplugin.FetchResponse{}, errors.New("request plugin service target unavailable")
		}
		if _, err := s.gateway.validateOutboundURL(f.URL); err != nil {
			return jsplugin.FetchResponse{}, errors.New("request plugin service target unavailable")
		}
		if f.Method == "" {
			f.Method = http.MethodGet
		}
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		reader := io.Reader(bytes.NewBufferString(f.Body))
		if f.BodyStream != nil {
			reader = io.LimitReader(f.BodyStream, f.BodySize)
		}
		req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(callCtx)), f.Method, f.URL, reader)
		if err != nil {
			return jsplugin.FetchResponse{}, errors.New("invalid request plugin service request")
		}
		if f.BodyStream != nil {
			req.ContentLength = f.BodySize
		}
		for name, value := range f.Headers {
			req.Header.Set(name, value)
		}
		resp, err := s.gateway.httpUpstream.Do(req, "", 0, 2)
		if err != nil {
			return jsplugin.FetchResponse{}, errors.New("request plugin service request failed")
		}
		defer func() { _ = resp.Body.Close() }()
		stop := context.AfterFunc(callCtx, func() { _ = resp.Body.Close() })
		defer stop()
		body, err := io.ReadAll(io.LimitReader(resp.Body, jsplugin.MaxFetchBodyBytes+1))
		if err != nil || len(body) > jsplugin.MaxFetchBodyBytes {
			return jsplugin.FetchResponse{}, errors.New("request plugin service response unavailable or too large")
		}
		return jsplugin.FetchResponse{Status: resp.StatusCode, Headers: pluginResponseHeaders(resp.Header), Body: body}, nil
	}
}
