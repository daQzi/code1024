package replyfilter

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/tidwall/gjson"
)

const MaxBodyBytes = 1 << 20
const MaxInstances = 16
const maxOutputs = 64
const maxQueueItems = 256
const maxQueueBytes = 4 << 20
const maxHookCalls = 4096
const maxExecutionTime = 10 * time.Second

// Instance belongs to one request. Call must use the host's bounded executor
// and the supplied context, and must return a typed/validated ReplyResult.
type Instance struct {
	ID, Name string
	Optional bool
	Call     func(context.Context, bool, []jsplugin.ReplyOutput) (jsplugin.ReplyResult, error)
}

type Record struct {
	ID, Action, ErrorClass string
	Masked, Findings       int
	DurationMS             int64
}

type Failure struct {
	StatusCode int
	Failed     bool
}

func (f *Failure) Error() string {
	if f.Failed {
		return "Response-processing policy could not be applied"
	}
	return "Response rejected by response-processing policy"
}

func New(parent context.Context, writer http.ResponseWriter, format string, instances []*Instance, onStop func()) (*Writer, context.Context, error) {
	if writer == nil {
		return nil, parent, errors.New("reply response writer is required")
	}
	check, cancel, err := newCheck(parent, format, instances)
	if err != nil {
		return nil, parent, err
	}
	return &Writer{ResponseWriter: adaptWriter(writer), format: format, check: check, cancel: cancel, onStop: onStop}, check.ctx, nil
}

func newCheck(parent context.Context, format string, instances []*Instance) (*replyCheck, context.CancelFunc, error) {
	if len(instances) == 0 || len(instances) > MaxInstances || !slices.Contains([]string{"openai", "openai_responses", "claude", "gemini"}, format) {
		return nil, nil, errors.New("unsupported reply processing scope")
	}
	ctx, cancel := context.WithCancel(parent)
	check := &replyCheck{ctx: ctx, outputs: map[int]*replyOutput{}, parts: map[[2]int]int{}}
	for _, instance := range instances {
		if instance == nil || instance.Call == nil {
			cancel()
			return nil, nil, errors.New("invalid reply instance")
		}
		check.targets = append(check.targets, &replyTarget{instance: instance})
	}
	return check, cancel, nil
}

// Rewrite is the offline, non-streaming entry point. The caller supplies the
// same bounded, typed hook executor used by the response writer; there is no
// HTTP handler, upstream request or billing side effect.
func Rewrite(ctx context.Context, format string, body []byte, instances []*Instance) ([]byte, []Record, *Failure, error) {
	check, cancel, err := newCheck(ctx, format, instances)
	if err != nil {
		return nil, nil, nil, err
	}
	defer cancel()
	if len(body) > MaxBodyBytes {
		check.failLimit()
	}
	w := &Writer{format: format, check: check}
	var answer []byte
	if check.stop == nil {
		answer = check.whole(format, body)
	}
	if check.stop != nil {
		return nil, w.records(), w.stopError(), nil
	}
	return answer, w.records(), nil, nil
}

func (r *replyCheck) callTarget(target *replyTarget, outputs []jsplugin.ReplyOutput) (jsplugin.ReplyResult, error) {
	if r.calls >= maxHookCalls || r.elapsed >= maxExecutionTime {
		target.action, target.errClass = "error", "limit"
		return jsplugin.ReplyResult{}, errors.New("reply execution limit")
	}
	r.calls++
	ctx, cancel := context.WithTimeout(r.ctx, min(5*time.Second, maxExecutionTime-r.elapsed))
	started := time.Now()
	result, err := target.instance.Call(ctx, r.stream, outputs)
	cancel()
	elapsed := time.Since(started)
	target.duration += elapsed
	r.elapsed += elapsed
	if err != nil {
		target.action, target.errClass = "error", "execution"
		return result, err
	}
	for _, finding := range result.Findings {
		target.addFinding(finding)
	}
	if result.Action == "stop" {
		target.action = "stop"
	} else if target.action == "" {
		target.action = "continue"
	}
	return result, nil
}

func (w *Writer) stopError() *Failure {
	if w.onStop != nil {
		w.onStop()
		w.onStop = nil
	}
	f := &Failure{StatusCode: http.StatusForbidden}
	if w.check.stop != nil && w.check.stop.failed {
		f.StatusCode, f.Failed = http.StatusServiceUnavailable, true
	}
	return f
}

func replyErrorBody(format string, failure *Failure) map[string]any {
	code := "response_processing_rejected"
	if failure.Failed {
		code = "response_processing_failed"
	}
	result := map[string]any{"error": map[string]any{"type": "request_processing_error", "code": code, "message": failure.Error()}}
	if format == "claude" {
		result["type"] = "error"
	}
	return result
}

func (w *Writer) records() []Record {
	out := make([]Record, 0, len(w.check.targets))
	for _, target := range w.check.targets {
		action := target.action
		class := target.errClass
		if w.check.stop != nil && w.check.stop.failed && w.check.stop.instance == "" {
			action, class = "error", w.check.stop.class
		}
		if target.unchecked {
			action = "unchecked"
		} else if target.skipped && target.errClass != "" {
			action = "skipped"
		}
		if action == "stop" {
			action = "reject"
		}
		if action == "" {
			continue
		}
		out = append(out, Record{ID: target.instance.ID, Action: action, ErrorClass: class, Masked: target.masked, Findings: len(target.findings), DurationMS: target.duration.Milliseconds()})
	}
	return out
}

// Finish runs remaining final checks before returning the aggregate audit.
// It owns no handler pointer and retains nothing once the host drops Writer.
func (w *Writer) Finish() ([]Record, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	defer w.cancel()
	if adapter, ok := w.ResponseWriter.(*httpWriter); ok {
		// Framework hosts commit their pending status when the handler ends.
		// A standalone net/http host needs that commitment here, including
		// responses with a status but no body.
		defer adapter.WriteHeaderNow()
	}
	if w.writeErr != nil {
		w.mode, w.buffer, w.queue, w.queuedBytes = replyPassing, nil, nil, 0
		return w.records(), w.stopped
	}
	switch w.mode {
	case replyWhole:
		w.finishWhole()
	case replyStream:
		if !w.stopped {
			w.endStream()
		}
	case replyUndecided:
		if w.status != 0 {
			w.ResponseWriter.WriteHeader(w.status)
			w.status = 0
		}
	}
	w.mode = replyPassing
	return w.records(), w.stopped
}

func (w *Writer) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeErr
}

func (r *replyCheck) failLimit() {
	if r.stop == nil {
		r.stop = &replyStop{failed: true, class: "limit"}
	}
}

func (w *Writer) failLimit() {
	w.check.failLimit()
	if w.mode == replyWhole || w.mode == replyUndecided {
		w.finishWhole()
	} else {
		w.stopStream()
	}
}

// JSON consumed by the filter must have one meaning for both gjson and the
// downstream decoder. Duplicate object members cannot bypass a text check.
func readableJSON(body []byte) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	nodes := 0
	var visit func(gjson.Result, int) bool
	visit = func(value gjson.Result, depth int) bool {
		nodes++
		if depth > 64 || nodes > 16384 {
			return false
		}
		valid := true
		if value.IsObject() || value.IsArray() {
			seen := map[string]bool{}
			value.ForEach(func(key, child gjson.Result) bool {
				if value.IsObject() {
					name := key.String()
					if seen[name] {
						valid = false
						return false
					}
					seen[name] = true
				}
				valid = visit(child, depth+1)
				return valid
			})
		}
		return valid
	}
	return visit(gjson.ParseBytes(body), 0)
}
