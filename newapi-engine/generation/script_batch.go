package generation

import (
	"context"
	"encoding/json"
	"errors"

	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
)

// The executor owns one task lease at a time. A batch-capable driver can use
// its original hooks for a single task without borrowing another task's
// connection, credentials or lease.
func (d ScriptDriver) BuildBatchQuery(ctx context.Context, task *Task) (pluginTask.Request, error) {
	if err := d.available(task); err != nil {
		return pluginTask.Request{}, err
	}
	if task.UpstreamID == "" || !d.Hooks.Has("buildBatchQueryRequest") || !d.Hooks.Has("parseBatchResult") {
		return pluginTask.Request{}, errors.New("batch task query is unavailable")
	}
	call, tasks := d.batchQueryContext(task)
	raw, err := d.Hooks.Call(ctx, "buildBatchQueryRequest", call, tasks)
	if err != nil {
		return pluginTask.Request{}, err
	}
	return pluginTask.Decode[pluginTask.Request](raw)
}

func (d ScriptDriver) batchQueryContext(task *Task) (map[string]any, []map[string]any) {
	call := d.Context(task)
	tasks := []map[string]any{d.Context(task)}
	call["tasks"] = tasks
	return call, tasks
}

func (d ScriptDriver) AcceptBatchQuery(ctx context.Context, task *Task, response ScriptResponse) error {
	if err := d.available(task); err != nil {
		return err
	}
	if response.Status < 200 || response.Status >= 300 || task.UpstreamID == "" || !d.Hooks.Has("parseBatchResult") {
		return errors.New("invalid batch task query response")
	}
	call, _ := d.batchQueryContext(task)
	raw, err := d.Hooks.Call(ctx, "parseBatchResult", call, response.Body, map[string]any{"status": response.Status, "headers": response.Headers})
	if err != nil {
		return err
	}
	type batchResult struct {
		pluginTask.Result
		TaskID string          `json:"taskId"`
		Data   json.RawMessage `json:"data"`
	}
	items, err := pluginTask.Decode[[]batchResult](raw)
	if err != nil || len(items) != 1 || items[0].TaskID != task.UpstreamID {
		return errors.New("batch result does not identify exactly the requested task")
	}
	item := items[0]
	if err := item.Result.Validate(); err != nil {
		return err
	}
	next := *task
	// Absence is not the previous poll's completion data. In particular,
	// never pass stale data or implicit {} to usage extraction as a result.
	next.Data = nonemptyJSON(item.Data)
	d.applyResult(&next, item.Result)
	if err := d.complete(ctx, &next, item.Result, item.Data); err != nil {
		return err
	}
	*task = next
	return nil
}
