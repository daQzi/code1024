package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGenerationSunoTaskOriginalDriverLifecycle(t *testing.T) {
	for _, tc := range []struct{ model, action, field, data, artifact string }{
		{"suno_music", "MUSIC", "gpt_description_prompt", `[{"id":"song","audio_url":"https://media.example.com/song.mp3"}]`, "audio"},
		{"suno_lyrics", "LYRICS", "prompt", `[{"id":"lyrics","text":"some lyrics"}]`, ""},
	} {
		t.Run(tc.model, func(t *testing.T) {
			plugins, account, upstream := generationPluginFixture(t, "sunoapi")
			plugins.encryptor = pluginTokenEncryptor{}
			account.Credentials[NewAPIVideoPricingKey] = map[string]any{tc.model: map[string]any{"mode": "expression", "expression": `u("clips") * 0.2`, "maximum": "1"}}
			require.NoError(t, plugins.NormalizeAccountBinding(context.Background(), account, nil))
			account.Status, account.Schedulable = StatusActive, true
			repo := &generationOriginFixture{candidates: []GenerationVideoDecodeCandidate{{VersionID: 1}}}
			svc := &GenerationService{repo: repo, plugins: plugins, accounts: &stubOpenAIAccountRepo{accounts: []Account{*account}}}
			group := &Group{ID: 3, RateMultiplier: 1, VideoRateIndependent: true, VideoRateMultiplier: 5}
			key := &APIKey{ID: 2, UserID: 1, User: &User{ID: 1}, GroupID: &group.ID, Group: group}
			ctx := WithNativeVideoPluginRouting(WithGenerationTaskSubmit(context.Background(), "sunoapi"), GenerationProtocolSunoTask)
			body := json.RawMessage(`{"model":"` + tc.model + `","input":"write a song"}`)
			ctx, err := svc.PrepareVideoRouting(ctx, key, PlatformOpenAI, tc.model, body, GenerationProtocolSunoTask)
			require.NoError(t, err)
			require.True(t, account.SupportsOpenAIEndpointCapability("plugin_suno_task"))
			require.False(t, account.SupportsOpenAIEndpointCapability("plugin_task"))
			task, err := svc.CreateVideo(ctx, key, nil, account, tc.model, body, "music-once", GenerationProtocolSunoTask)
			require.NoError(t, err)
			require.Equal(t, "1", task.Price.Hold.String(), "music uses the standard group rate")
			require.Equal(t, tc.action, task.Action)
			require.Empty(t, upstream.requests)
			upstream.response = `{"code":"success","data":"private-task"}`
			require.NoError(t, plugins.SubmitTask(ctx, account, task))
			require.Equal(t, "/suno/submit/"+tc.action, upstream.requests[0].URL.Path)
			sent, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.Equal(t, "write a song", gjson.GetBytes(sent, tc.field).Str)
			before := *task
			upstream.response = `{"code":"success","data":[{"task_id":"another-task","status":"SUCCESS","data":` + tc.data + `}]}`
			require.Error(t, plugins.PollTask(ctx, account, task))
			require.Equal(t, before, *task)
			upstream.response = `{"code":"success","data":[{"task_id":"private-task","status":"SUCCESS","data":` + tc.data + `}]}`
			require.NoError(t, plugins.PollTask(ctx, account, task))
			require.Equal(t, GenerationSucceeded, task.Status)
			require.Equal(t, "/suno/fetch", upstream.requests[2].URL.Path)
			require.Equal(t, http.MethodPost, upstream.requests[2].Method)
			sent, err = io.ReadAll(upstream.requests[2].Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"ids":["private-task"]}`, string(sent))
			charge, err := task.Price.SettleExpression(json.RawMessage(gjson.GetBytes(task.Usage, "_host_final_usage").Raw))
			require.NoError(t, err)
			require.Equal(t, "0.2", charge.Amount.String())
			artifacts, err := svc.ListTaskArtifacts(ctx, task)
			require.NoError(t, err)
			if tc.artifact == "" {
				require.Empty(t, artifacts)
			} else {
				require.Len(t, artifacts, 1)
				require.Equal(t, tc.artifact, artifacts[0].Type)
				require.True(t, strings.HasPrefix(artifacts[0].Key, "audio-"))
			}
		})
	}
}
