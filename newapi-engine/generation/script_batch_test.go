package generation_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/stretchr/testify/require"
)

func sunoScriptDriver(t *testing.T) generation.ScriptDriver {
	t.Helper()
	source, err := builtin.Source("sunoapi")
	require.NoError(t, err)
	plugin, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	return generation.ScriptDriver{
		Hooks: generation.ScriptHooks{
			Has: plugin.Engine.HasExport,
			Call: func(ctx context.Context, hook string, args ...any) (json.RawMessage, error) {
				var raw json.RawMessage
				err := plugin.Engine.CallInto(ctx, &raw, hook, args...)
				return raw, err
			},
		},
		Context: func(task *generation.Task) map[string]any {
			return map[string]any{
				"baseUrl": "https://suno.example.test", "apiKey": "offline-fixture",
				"model": task.Model, "upstreamModel": task.UpstreamModel, "action": task.Action,
				"taskId": task.UpstreamID, "requestBody": task.Request, "state": task.State, "data": task.Data,
			}
		},
	}
}

func TestScriptDriverSunoBatchQueryBoundariesAndLyrics(t *testing.T) {
	ctx := context.Background()
	driver := sunoScriptDriver(t)
	task := &generation.Task{
		ID: "video_" + "0123456789abcdef0123456789abcdef01234567",
		Protocol: generation.ProtocolSunoTask, Model: "suno_lyrics", UpstreamModel: "suno_lyrics",
		Action: "LYRICS", Status: generation.Running, UpstreamID: "upstream-lyrics",
		Data: json.RawMessage(`[{"id":"stale","text":"stale lyrics"}]`),
	}
	req, err := driver.BuildBatchQuery(ctx, task)
	require.NoError(t, err)
	require.Equal(t, "POST", req.Method)
	require.Equal(t, "https://suno.example.test/suno/fetch", req.URL)
	require.JSONEq(t, `{"ids":["upstream-lyrics"]}`, string(req.Body))

	before := cloneTask(task)
	for _, bad := range []string{
		`{"code":"success","data":[]}`,
		`{"code":"success","data":[{"task_id":"other","status":"SUCCESS","data":[{"id":"1","text":"x"}]}]}`,
		`{"code":"success","data":[{"task_id":"upstream-lyrics","status":"SUCCESS","data":[]},{"task_id":"extra","status":"SUCCESS","data":[]}]}`,
	} {
		require.Error(t, driver.AcceptBatchQuery(ctx, task, generation.ScriptResponse{Status: 200, Body: json.RawMessage(bad)}))
		require.Equal(t, before, task)
	}

	require.NoError(t, driver.AcceptBatchQuery(ctx, task, generation.ScriptResponse{
		Status: 200,
		Body:   json.RawMessage(`{"code":"success","data":[{"task_id":"upstream-lyrics","status":"SUCCESS","data":[{"id":"l1","title":"Dawn","text":"First line","audio_url":"https://private.example/ignored"}]}]}`),
	}))
	require.Equal(t, generation.Succeeded, task.Status)
	lyrics := task.SunoLyrics()
	require.Equal(t, []generation.TaskLyric{{Title: "Dawn", Text: "First line"}}, lyrics)

	failed := &generation.Task{
		Protocol: generation.ProtocolSunoTask, Model: "suno_music", UpstreamModel: "suno_music",
		Action: "MUSIC", Status: generation.Running, UpstreamID: "upstream-fail",
		Data: json.RawMessage(`[{"id":"stale","audio_url":"https://stale.example/a.mp3"}]`),
	}
	require.NoError(t, driver.AcceptBatchQuery(ctx, failed, generation.ScriptResponse{
		Status: 200,
		Body:   json.RawMessage(`{"code":"success","data":[{"task_id":"upstream-fail","status":"FAILURE","fail_reason":"upstream rejected"}]}`),
	}))
	require.Equal(t, generation.Failed, failed.Status)
	require.Equal(t, "audio_generation_failed", failed.ErrorCode)
	require.Equal(t, json.RawMessage(`{}`), failed.Data)
	require.Nil(t, failed.SunoLyrics())
}
