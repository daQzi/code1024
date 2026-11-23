package replyfilter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const replyFixtureMeta = `export const meta={apiVersion:1,key:"reply-fixture",name:"Reply fixture",version:"1.0.0",author:{name:"Fixture"}};`
const maskReply = `export function reply(ctx,res){const mask=[];for(const o of res.outputs){let at=o.text.indexOf("secret");if(at>=0)mask.push({index:o.index,start:at,end:at+6,category:"test"});}return {action:"continue",mask,hold:res.stream?6:0};}`

func fixtureInstance(t *testing.T, hook string, optional bool) *Instance {
	t.Helper()
	p, err := jsplugin.CompilePlugin(replyFixtureMeta+hook, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	return &Instance{ID: "7", Name: "Fixture", Optional: optional, Call: func(ctx context.Context, stream bool, outputs []jsplugin.ReplyOutput) (jsplugin.ReplyResult, error) {
		return p.CallReply(ctx, jsplugin.TransformContext{Format: "openai", Model: "test"}, stream, outputs)
	}}
}

func TestReplyOfflineRewriteBoundsAndFailure(t *testing.T) {
	instance := fixtureInstance(t, maskReply, false)
	body, records, failure, err := Rewrite(context.Background(), "openai", []byte(`{"choices":[{"message":{"content":"secret"}}]}`), []*Instance{instance})
	require.NoError(t, err)
	require.Nil(t, failure)
	require.Equal(t, "***", gjson.GetBytes(body, "choices.0.message.content").String())
	require.Equal(t, 1, records[0].Masked)
	_, records, failure, err = Rewrite(context.Background(), "openai", []byte(strings.Repeat("x", MaxBodyBytes+1)), []*Instance{instance})
	require.NoError(t, err)
	require.NotNil(t, failure)
	require.True(t, failure.Failed)
	require.Equal(t, "limit", records[0].ErrorClass)
	instances := make([]*Instance, MaxInstances+1)
	for i := range instances {
		instances[i] = instance
	}
	_, _, _, err = Rewrite(context.Background(), "openai", []byte(`{}`), instances)
	require.Error(t, err)
}

func TestReplyIncompleteEventNeverLeaksUninspectedText(t *testing.T) {
	w, recorder, _, _ := fixtureWriter(t, "openai", "text/event-stream", fixtureInstance(t, maskReply, false))
	_, err := w.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"secret\"}}]}\n")
	require.NoError(t, err)
	records, stopped := w.Finish()
	require.True(t, stopped)
	require.NotContains(t, recorder.Body.String(), "secret")
	require.Contains(t, recorder.Body.String(), "response_processing_failed")
	require.Equal(t, "incomplete_event", records[0].ErrorClass)
}

func TestReplyAmbiguousAndInvalidJSONFailClosed(t *testing.T) {
	for _, body := range []string{
		`{"choices":[],"choices":[{"message":{"content":"secret"}}]}`,
		`{"choices":[{"message":{"content":"ok","content":"secret"}}]}`,
		strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66),
	} {
		_, records, failure, err := Rewrite(context.Background(), "openai", []byte(body), []*Instance{fixtureInstance(t, maskReply, false)})
		require.NoError(t, err)
		require.NotNil(t, failure)
		require.Equal(t, "invalid_response", records[0].ErrorClass)
	}
	w, recorder, _, _ := fixtureWriter(t, "openai", "text/event-stream", fixtureInstance(t, maskReply, false))
	_, err := w.WriteString("data: not JSON secret\n\n")
	require.NoError(t, err)
	records, stopped := w.Finish()
	require.True(t, stopped)
	require.NotContains(t, recorder.Body.String(), "secret")
	require.Equal(t, "invalid_event", records[0].ErrorClass)
}

type disconnectedReplyWriter struct{ http.ResponseWriter }

func (disconnectedReplyWriter) Write([]byte) (int, error) {
	return 0, errors.New("client disconnected")
}

func (disconnectedReplyWriter) Flush() {}

func TestReplyWriteFailureCancelsUpstream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		recorder := httptest.NewRecorder()
		w, ctx, err := New(context.Background(), disconnectedReplyWriter{recorder}, "openai", []*Instance{fixtureInstance(t, maskReply, false)}, nil)
		require.NoError(t, err)
		body := `{"choices":[{"message":{"content":"answer"}}]}`
		w.Header().Set("Content-Type", "application/json")
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			body = "data: {\"choices\":[{\"delta\":{\"content\":\"answer is long enough\"}}]}\n\n"
		}
		_, err = w.WriteString(body)
		require.ErrorContains(t, err, "client disconnected")
		require.ErrorIs(t, ctx.Err(), context.Canceled)
		w.Finish()
	}
}

func BenchmarkReplyRewrite(b *testing.B) {
	for _, size := range []int{64 << 10, 900 << 10} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			body, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": strings.Repeat("x", size)}}}})
			require.NoError(b, err)
			instances := []*Instance{{ID: "pass", Call: func(context.Context, bool, []jsplugin.ReplyOutput) (jsplugin.ReplyResult, error) {
				return jsplugin.ReplyResult{Action: "continue"}, nil
			}}}
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for b.Loop() {
				_, _, _, _ = Rewrite(context.Background(), "openai", body, instances)
			}
		})
	}
}

func fixtureWriter(t *testing.T, format, kind string, instance *Instance) (*Writer, *httptest.ResponseRecorder, context.Context, *int) {
	t.Helper()
	recorder := httptest.NewRecorder()
	stops := new(int)
	w, ctx, err := New(context.Background(), recorder, format, []*Instance{instance}, func() { *stops++ })
	require.NoError(t, err)
	w.Header().Set("Content-Type", kind)
	return w, recorder, ctx, stops
}

func TestReplyWholeFormatsAndUTF16(t *testing.T) {
	for _, tc := range []struct{ format, body, path, want string }{
		{"openai", `{"choices":[{"index":0,"message":{"content":"你好🚀secret end"}}],"usage":{"prompt_tokens":2}}`, "choices.0.message.content", "你好🚀*** end"},
		{"openai_responses", `{"output":[{"type":"message","content":[{"type":"output_text","text":"secret end"}]}]}`, "output.0.content.0.text", "*** end"},
		{"claude", `{"content":[{"type":"thinking","thinking":"secret"},{"type":"text","text":"secret end"}]}`, "content.1.text", "*** end"},
		{"gemini", `{"candidates":[{"content":{"parts":[{"text":"thought secret","thought":true},{"text":"sec"},{"text":"ret end"}]}}]}`, "candidates.0.content.parts.1.text", "***"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			w, recorder, _, stops := fixtureWriter(t, tc.format, "application/json", fixtureInstance(t, maskReply, false))
			w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
			_, err := w.WriteString(tc.body)
			require.NoError(t, err)
			records, stopped := w.Finish()
			require.False(t, stopped)
			require.Zero(t, *stops)
			require.Equal(t, tc.want, gjson.Get(recorder.Body.String(), tc.path).String())
			require.Equal(t, strconv.Itoa(recorder.Body.Len()), recorder.Header().Get("Content-Length"))
			require.Len(t, records, 1)
			require.Equal(t, 1, records[0].Masked)
			if tc.format == "openai" {
				require.Equal(t, int64(2), gjson.Get(recorder.Body.String(), "usage.prompt_tokens").Int())
			}
			if tc.format == "gemini" {
				require.Equal(t, " end", gjson.Get(recorder.Body.String(), "candidates.0.content.parts.2.text").String())
			}
		})
	}
}

func TestReplyStreamHoldsAcrossChunksAndRewritesFullText(t *testing.T) {
	for _, format := range []string{"openai", "openai_responses"} {
		t.Run(format, func(t *testing.T) {
			w, recorder, _, _ := fixtureWriter(t, format, "text/event-stream", fixtureInstance(t, maskReply, false))
			first, second, last := `{"choices":[{"index":0,"delta":{"content":"🚀sec"}}]}`, `{"choices":[{"index":0,"delta":{"content":"ret end"}}]}`, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"completion_tokens":3}}`
			if format == "openai_responses" {
				first = `{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"m","delta":"🚀sec"}`
				second = `{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"m","delta":"ret end"}`
				last = `{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"🚀secret end"}`
			}
			_, err := w.WriteString("data: " + first + "\n\n")
			require.NoError(t, err)
			require.NotContains(t, recorder.Body.String(), "sec")
			_, err = w.WriteString("data: " + second + "\n\n" + "data: " + last + "\n\ndata: [DONE]\n\n")
			require.NoError(t, err)
			_, stopped := w.Finish()
			require.False(t, stopped)
			output := recorder.Body.String()
			require.NotContains(t, output, "secret")
			require.Contains(t, output, "***")
			if format == "openai_responses" {
				require.Contains(t, output, `"text":"🚀*** end"`)
			} else {
				require.Contains(t, output, `"completion_tokens":3`)
			}
		})
	}
}

func TestReplyStopsWithoutLeakingFailureAndOptionalPasses(t *testing.T) {
	for _, tc := range []struct {
		name, hook                string
		optional, stream, stopped bool
	}{
		{"stop-json", `export function reply(){return {action:"stop",code:"private",message:"PRIVATE"}}`, false, false, true},
		{"fail-json", `export function reply(){throw Error("PRIVATE")}`, false, false, true},
		{"optional-json", `export function reply(){throw Error("PRIVATE")}`, true, false, false},
		{"stop-stream", `export function reply(){return {action:"stop",code:"private",message:"PRIVATE"}}`, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind, data := "application/json", `{"choices":[{"message":{"content":"secret"}}]}`
			if tc.stream {
				kind, data = "text/event-stream", "data: {\"choices\":[{\"delta\":{\"content\":\"secret\"}}]}\n\n"
			}
			w, recorder, ctx, stops := fixtureWriter(t, "openai", kind, fixtureInstance(t, tc.hook, tc.optional))
			_, err := w.WriteString(data)
			require.NoError(t, err)
			records, stopped := w.Finish()
			require.Equal(t, tc.stopped, stopped)
			require.NotContains(t, recorder.Body.String(), "PRIVATE")
			if tc.stopped {
				require.Equal(t, 1, *stops)
				require.NotContains(t, recorder.Body.String(), "secret")
				require.NotContains(t, recorder.Body.String(), "[DONE]")
				require.Error(t, ctx.Err())
			} else {
				require.Contains(t, recorder.Body.String(), "secret")
				require.Equal(t, "skipped", records[0].Action)
			}
		})
	}
}

func TestReplyResourceBoundsAndCancellation(t *testing.T) {
	hold := fixtureInstance(t, `export function reply(){return {action:"continue",hold:8192}}`, false)
	for _, boundary := range []string{"frame", "queue", "outputs"} {
		t.Run(boundary, func(t *testing.T) {
			w, recorder, ctx, _ := fixtureWriter(t, "openai", "text/event-stream", hold)
			switch boundary {
			case "frame":
				_, _ = w.WriteString("data: " + strings.Repeat("x", MaxBodyBytes))
			case "queue":
				_, _ = w.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"held\"}}]}\n\n")
				for range maxQueueItems + 1 {
					_, _ = w.WriteString("data: {\"choices\":[],\"usage\":{}}\n\n")
				}
			case "outputs":
				choices := make([]map[string]any, maxOutputs+1)
				for i := range choices {
					choices[i] = map[string]any{"index": i, "delta": map[string]any{"content": "x"}}
				}
				data, err := json.Marshal(map[string]any{"choices": choices})
				require.NoError(t, err)
				_, _ = w.WriteString("data: " + string(data) + "\n\n")
			}
			records, stopped := w.Finish()
			require.True(t, stopped)
			require.Error(t, ctx.Err())
			require.Contains(t, recorder.Body.String(), "response_processing_failed")
			require.NotContains(t, recorder.Body.String(), "[DONE]")
			require.Equal(t, "limit", records[0].ErrorClass)
			require.LessOrEqual(t, len(w.check.outputs), maxOutputs)
			require.Zero(t, w.queuedBytes)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	instance := &Instance{ID: "cancel", Call: func(ctx context.Context, _ bool, _ []jsplugin.ReplyOutput) (jsplugin.ReplyResult, error) {
		return jsplugin.ReplyResult{}, ctx.Err()
	}}
	recorder := httptest.NewRecorder()
	w, _, err := New(ctx, recorder, "openai", []*Instance{instance}, nil)
	require.NoError(t, err)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.WriteString(`{"choices":[{"message":{"content":"x"}}]}`)
	_, stopped := w.Finish()
	require.True(t, stopped)
}

func TestReplyPassAndNonAnswersPreserveWire(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		status   int
		wantCall bool
	}{{"application/json", 429, false}, {"video/mp4", 200, false}, {"application/json", 200, true}} {
		calls := 0
		instance := &Instance{ID: "pass", Call: func(context.Context, bool, []jsplugin.ReplyOutput) (jsplugin.ReplyResult, error) {
			calls++
			return jsplugin.ReplyResult{Action: "pass"}, nil
		}}
		w, recorder, _, _ := fixtureWriter(t, "openai", tc.kind, instance)
		w.WriteHeader(tc.status)
		body := `{"choices":[{"message":{"content":"untouched"}}]}`
		_, err := w.WriteString(body)
		require.NoError(t, err)
		_, stopped := w.Finish()
		require.False(t, stopped)
		require.Equal(t, body, recorder.Body.String())
		require.Equal(t, tc.wantCall, calls > 0)
	}
	_, _, err := New(context.Background(), nil, "openai_realtime", []*Instance{{}}, nil)
	require.Error(t, err)
}

func TestHTTPWriterBuffersHeadersAndCommitsEmptyResponse(t *testing.T) {
	w, recorder, _, _ := fixtureWriter(t, "openai", "application/json", fixtureInstance(t, maskReply, false))
	require.Equal(t, -1, w.Size())
	require.False(t, w.Written())
	w.WriteHeader(http.StatusCreated)
	_, err := w.WriteString(`{"choices":[{"message":{"content":"sec`)
	require.NoError(t, err)
	w.Flush()
	require.Empty(t, recorder.Body.String(), "partial answers and their status stay buffered")
	require.False(t, recorder.Flushed)
	_, err = w.WriteString(`ret"}}]}`)
	require.NoError(t, err)
	_, stopped := w.Finish()
	require.False(t, stopped)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, "***", gjson.Get(recorder.Body.String(), "choices.0.message.content").String())
	require.Equal(t, recorder.Body.Len(), w.Size())
	require.True(t, w.Written())
	_, stopped = w.Finish()
	require.False(t, stopped, "finishing twice never duplicates client output")
	require.Equal(t, recorder.Body.Len(), w.Size())

	empty := httptest.NewRecorder()
	w, _, err = New(context.Background(), empty, "openai", []*Instance{fixtureInstance(t, maskReply, false)}, nil)
	require.NoError(t, err)
	w.WriteHeader(http.StatusNoContent)
	w.Finish()
	require.Equal(t, http.StatusNoContent, empty.Code)
	require.Empty(t, empty.Body.String())
	require.Zero(t, w.Size())

	_, _, err = New(context.Background(), nil, "openai", []*Instance{fixtureInstance(t, maskReply, false)}, nil)
	require.Error(t, err)
}

func TestReplySocketFilterMasksStopsAndRejectsInvalidJSON(t *testing.T) {
	var emitted []string
	f, _, err := NewSocket(context.Background(), "openai_responses", []*Instance{fixtureInstance(t, maskReply, false)}, func(msg []byte) {
		emitted = append(emitted, string(msg))
	}, nil)
	require.NoError(t, err)
	f.Event([]byte(`{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`))
	f.Event([]byte(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"a sec"}`))
	f.Event([]byte(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"ret ok"}`))
	term := f.Terminal([]byte(`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"a secret ok"}]}]}}`))
	require.NotNil(t, term)
	require.Nil(t, f.Stopped())
	require.Equal(t, "a *** ok", gjson.GetBytes(term, "response.output.0.content.0.text").Str)
	joined := strings.Join(emitted, "\n")
	require.NotContains(t, joined, "secret")
	require.Contains(t, joined, "***")
	records, stopped := f.Finish()
	require.False(t, stopped)
	require.Len(t, records, 1)
	require.Equal(t, 1, records[0].Masked)

	stopHook := `export function reply(ctx,res){for(const o of res.outputs){if(o.text.includes("stopword"))return {action:"stop",code:"blocked"};}return {action:"continue"};}`
	var stoppedOut []string
	stopCalled := 0
	sf, _, err := NewSocket(context.Background(), "openai_responses", []*Instance{fixtureInstance(t, stopHook, false)}, func(msg []byte) {
		stoppedOut = append(stoppedOut, string(msg))
	}, func() { stopCalled++ })
	require.NoError(t, err)
	sf.Event([]byte(`{"type":"response.created","response":{"id":"resp_2","status":"in_progress"}}`))
	sf.Event([]byte(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"say stopword now"}`))
	fail := sf.Stopped()
	require.NotNil(t, fail)
	require.False(t, fail.Failed)
	require.Equal(t, http.StatusForbidden, fail.StatusCode)
	require.Equal(t, 1, stopCalled)
	require.Nil(t, sf.Terminal([]byte(`{"type":"response.completed","response":{"id":"resp_2"}}`)))
	require.NotContains(t, strings.Join(stoppedOut, "\n"), "stopword")
	records, stopped = sf.Finish()
	require.True(t, stopped)
	require.Len(t, records, 1)
	require.Equal(t, "reject", records[0].Action)

	badFilter, _, err := NewSocket(context.Background(), "openai_responses", []*Instance{fixtureInstance(t, maskReply, false)}, func([]byte) {}, nil)
	require.NoError(t, err)
	badFilter.Event([]byte(`{"type":"response.output_text.delta","delta":"a","delta":"b"}`))
	badFail := badFilter.Stopped()
	require.NotNil(t, badFail)
	require.True(t, badFail.Failed)
	_, stopped = badFilter.Finish()
	require.True(t, stopped)
}
