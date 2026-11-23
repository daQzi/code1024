package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

type pluginRegistryFixture struct {
	NewAPIPluginRepository
	PluginRegistryRepository
	item  *PluginSubmission
	saved int
}

func TestPluginPlaygroundTemplatesCompileAndRunOffline(t *testing.T) {
	s, _, httpClient := generationPluginFixture(t, "sora")
	templates, err := PluginDeveloperTemplates(7)
	require.NoError(t, err)
	require.Len(t, templates, 3)
	for _, template := range templates {
		t.Run(template.Kind, func(t *testing.T) {
			item, err := s.InspectSource(context.Background(), template.Source)
			require.NoError(t, err)
			require.Equal(t, "community-7-example", item.Key)
			input := template.Fixture
			input.Source = template.Source
			result, err := s.DryRunPlugin(context.Background(), input)
			require.NoError(t, err)
			require.True(t, result.Offline)
			require.Equal(t, item.SHA256, result.SHA256)
			require.Empty(t, result.Compatibility.Blockers, "template must describe implemented host capabilities")
			require.True(t, json.Valid(result.Result))
			switch template.Kind {
			case "ingress":
				require.JSONEq(t, `{"action":"continue"}`, string(result.Result))
			case "task":
				require.Contains(t, string(result.Result), "https://upstream.example.invalid/jobs")
			case "provider":
				require.Contains(t, string(result.Result), `"deployment":"example-text"`)
			}
		})
	}
	require.Empty(t, httpClient.requests, "offline fixture cannot access an account HTTP transport")
}

func TestPluginPlaygroundLimitsAndNoNetwork(t *testing.T) {
	s, _, httpClient := generationPluginFixture(t, "sora")
	templates, err := PluginDeveloperTemplates(7)
	require.NoError(t, err)
	base := templates[0]
	for _, scenario := range []string{"unknown hook", "missing hook", "source limit", "input limit", "output limit", "network", "bad json", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			input := base.Fixture
			input.Source = base.Source
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "unknown hook":
				input.Hook = "refresh"
			case "missing hook":
				input.Hook = "buildSubmitRequest"
			case "source limit":
				input.Source = strings.Repeat("x", (1<<20)+1)
			case "input limit":
				input.Args = []json.RawMessage{json.RawMessage(`"` + strings.Repeat("x", 64<<10) + `"`)}
			case "output limit":
				input.Source = strings.Replace(base.Source, `return { action: "continue" };`, `return {action:"continue",notice:"x".repeat(65537)};`, 1)
			case "network":
				input.Source = strings.Replace(base.Source, `return { action: "continue" };`, `return utils.fetch({url:"https://upstream.example.invalid"});`, 1)
			case "bad json":
				input.Args = []json.RawMessage{json.RawMessage(`invalid`)}
			case "cancelled":
				cancel()
			}
			_, err := s.DryRunPlugin(ctx, input)
			require.Error(t, err)
		})
	}
	require.Empty(t, httpClient.requests)
}

func TestPluginPlaygroundTaskTemplateHostFixture(t *testing.T) {
	s, account, upstream := generationPluginFixture(t, "sora")
	templates, err := PluginDeveloperTemplates(7)
	require.NoError(t, err)
	loaded, err := jsplugin.CompilePlugin(templates[1].Source, jsplugin.Options{Concurrency: 1})
	require.NoError(t, err)
	s.repo = &generationPluginFixtureRepo{version: &NewAPIPluginVersion{ID: 1, Enabled: true, Source: templates[1].Source, Meta: loaded.Meta, Capabilities: runner.Describe(loaded)}}
	ctx := context.Background()
	body, action, err := s.DecodeVideo(ctx, account, 1, "example-video", json.RawMessage(`{"model":"example-video","prompt":"Ocean","seconds":"8"}`))
	require.NoError(t, err)
	task := &GenerationTask{ID: "video_" + strings.Repeat("b", 40), PluginVersionID: 1, Model: "example-video", UpstreamModel: "example-video", Action: action, Request: body, State: json.RawMessage(`{}`), Data: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()}
	task.Usage, err = s.EstimateTask(ctx, account, task)
	require.NoError(t, err)
	require.JSONEq(t, `{"seconds":8}`, string(task.Usage))
	upstream.response = `{"id":"example-upstream-task"}`
	require.NoError(t, s.SubmitTask(ctx, account, task))
	require.Equal(t, "example-upstream-task", task.UpstreamID)
	upstream.response = `{"id":"example-upstream-task","status":"completed","video_url":"https://media.example/video.mp4","seconds":8}`
	require.NoError(t, s.PollTask(ctx, account, task))
	require.Equal(t, GenerationSucceeded, task.Status)
	require.Equal(t, GenerationDeliveryReady, task.DeliveryStatus)
}

func (r *pluginRegistryFixture) GetPluginSubmission(context.Context, int64) (*PluginSubmission, error) {
	return r.item, nil
}
func (r *pluginRegistryFixture) SavePluginDraft(_ context.Context, item *PluginSubmission) (*PluginSubmission, error) {
	r.saved++
	r.item = item
	return item, nil
}

func TestPluginRegistryNamespaceAndOwnershipBeforeCompilation(t *testing.T) {
	s, _, _ := generationPluginFixture(t, "sora")
	repo := &pluginRegistryFixture{item: &PluginSubmission{AuthorID: 99, Revision: 1, Status: "draft"}}
	s.repo = repo
	_, err := s.SaveSubmission(context.Background(), 7, 1, 1, "not valid javascript", "")
	require.ErrorIs(t, err, ErrPluginSubmissionMissing)
	source, err := builtin.Source("sora")
	require.NoError(t, err)
	_, err = s.SaveSubmission(context.Background(), 7, 0, 0, source, "")
	require.ErrorContains(t, err, "community-7-")
	require.Zero(t, repo.saved, "system and other authors' keys are reserved")
	source = strings.Replace(source, `key: "sora"`, `key: "community-7-sora"`, 1)
	item, err := s.SaveSubmission(context.Background(), 7, 0, 0, source, "readme")
	require.NoError(t, err)
	require.Equal(t, "community-7-sora", item.Key)
	require.Equal(t, int64(7), item.AuthorID)
	require.Len(t, item.SHA256, 64)
	require.Equal(t, 1, repo.saved, "saving validated source only creates an inert draft")
	_, err = s.SaveSubmission(context.Background(), 7, 0, 0, `export const meta = {`, "")
	require.Error(t, err)
}
