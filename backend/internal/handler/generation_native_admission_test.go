//go:build unit

package handler

import (
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type nativeAdmissionPluginRepo struct {
	service.NewAPIPluginRepository
	version *service.NewAPIPluginVersion
}

func (r *nativeAdmissionPluginRepo) Get(context.Context, int64) (*service.NewAPIPluginVersion, error) {
	return r.version, nil
}
func (r *nativeAdmissionPluginRepo) Install(_ context.Context, v *service.NewAPIPluginVersion, _ *int64) (*service.NewAPIPluginVersion, error) {
	return v, nil
}

type nativeAdmissionTaskRepo struct {
	service.GenerationTaskRepository
	task              *service.GenerationTask
	parent            *service.GenerationTask
	candidates        []service.GenerationVideoDecodeCandidate
	holds, nativeLogs int
}

func (r *nativeAdmissionTaskRepo) ListVideoDecodeCandidates(context.Context, int64, int64, int64, string, string) ([]service.GenerationVideoDecodeCandidate, error) {
	return r.candidates, nil
}
func (r *nativeAdmissionTaskRepo) GetVideoOrigin(_ context.Context, id string, user, key, group int64) (*service.GenerationTask, error) {
	if p := r.parent; p != nil && p.ID == id && p.UserID == user && p.APIKeyID == key && p.GroupID == group {
		return p, nil
	}
	return nil, service.ErrGenerationTaskNotFound
}

func (r *nativeAdmissionTaskRepo) GetOwned(ctx context.Context, id string, user, key int64) (*service.GenerationTask, error) {
	if r.parent == nil {
		return nil, service.ErrGenerationTaskNotFound
	}
	return r.GetVideoOrigin(ctx, id, user, key, r.parent.GroupID)
}

func (r *nativeAdmissionTaskRepo) FindIdempotent(_ context.Context, user, key int64, idempotency string) (*service.GenerationTask, error) {
	if r.task != nil && r.task.UserID == user && r.task.APIKeyID == key && r.task.IdempotencyKey == idempotency {
		return r.task, nil
	}
	return nil, service.ErrGenerationTaskNotFound
}
func (r *nativeAdmissionTaskRepo) Claim(context.Context, string, time.Duration) (*service.GenerationTask, error) {
	return nil, service.ErrGenerationTaskNotFound
}
func (r *nativeAdmissionTaskRepo) CreateHeld(_ context.Context, task *service.GenerationTask, _ *service.UsageBillingCommand) (*service.GenerationTask, error) {
	r.holds++
	task.Status, task.CreatedAt = service.GenerationQueued, time.Now().UTC()
	r.task = task
	return task, nil
}
func (r *nativeAdmissionTaskRepo) ObserveNativeVideo(context.Context, service.NativeVideoObservation) error {
	r.nativeLogs++
	return nil
}

func TestGenerationNativeAdmissionUsesBoundDriverAndOneReservation(t *testing.T) {
	for _, tc := range []struct{ plugin, platform, protocol, path, model, body string }{
		{"xai", service.PlatformGrok, service.GenerationProtocolXAI, "/v1/videos/generations", "grok-imagine-video", `{"model":"public-video","prompt":"ocean","duration":8}`},
		{"xai", service.PlatformGrok, service.GenerationProtocolXAIEdit, "/v1/videos/edits", "grok-imagine-video", `{"model":"public-video","prompt":"ocean","video":{"url":"https://media.example/input.mp4"}}`},
		{"doubao", service.PlatformOpenAI, service.GenerationProtocolArk, "/api/v3/contents/generations/tasks", "doubao-seedance-2-5-260628", `{"model":"public-video","content":[{"type":"text","text":"ocean"}],"duration":8}`},
		{"doubao", service.PlatformOpenAI, service.GenerationProtocolArk, "/api/v3/contents/generations/tasks", "doubao-seedance-2-5-260628", `{"model":"public-video","content":[{"type":"draft_task","draft_task":{"id":"video_` + strings.Repeat("1", 40) + `"}},{"type":"text","text":"continue"}],"duration":8}`},
		{"sora", service.PlatformOpenAI, service.GenerationProtocolOpenAI, "/v1/videos/video_" + strings.Repeat("1", 40) + "/remix", "sora-2", `{"prompt":"Make it blue"}`},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			source, err := builtin.Source(tc.plugin)
			require.NoError(t, err)
			loaded, err := jsplugin.CompilePlugin(source, jsplugin.Options{Concurrency: 1})
			require.NoError(t, err)
			version := &service.NewAPIPluginVersion{ID: 1, Source: source, Meta: loaded.Meta, Enabled: true, Capabilities: runner.Descriptor{Task: true}}
			group := &service.Group{ID: 24, Platform: tc.platform, RateMultiplier: 1, AllowImageGeneration: true}
			account := service.Account{ID: 7, Platform: tc.platform, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 2, GroupIDs: []int64{group.ID}, Credentials: map[string]any{service.NewAPIPluginRevisionKey: 1, "base_url": "https://upstream.example", "api_key": "fixture-key", "model_mapping": map[string]any{"public-video": tc.model}, service.NewAPIVideoPricingKey: map[string]any{"*": map[string]any{"mode": "fixed", "unit": "request", "unit_price": "0.10"}}}}
			slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
			concurrency := service.NewConcurrencyService(slots)
			cfg := &config.Config{RunMode: config.RunModeSimple}
			// A higher-priority task account offers this alias but does not
			// declare the requested native route. It must be excluded up front.
			wrong := account
			wrong.ID, wrong.Priority = 6, -10
			wrong.Credentials = map[string]any{service.NewAPIPluginRevisionKey: 2, "newapi_plugin_video_models": []string{tc.model}, "model_mapping": map[string]any{"public-video": tc.model}, "base_url": "https://wrong.example"}
			remix := strings.HasSuffix(tc.path, "/remix")
			derived := strings.Contains(tc.body, `"draft_task"`) || remix
			if derived {
				wrong.Credentials = maps.Clone(account.Credentials)
			}
			repo := openAIImagesFailoverAccountRepo{accounts: []service.Account{wrong, account}}
			upstream := &grokMediaSlotUpstream{call: func(*http.Request, int64) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			}}
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, &grokMediaSlotBindings{}, cfg, nil, concurrency, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
			plugins, err := service.ProvideNewAPIPluginService(&nativeAdmissionPluginRepo{version: version}, requestHTTPEncryptor{}, gateway, nil, nil, nil, nil)
			require.NoError(t, err)
			defer plugins.Stop()
			require.NoError(t, plugins.NormalizeAccountBinding(context.Background(), &repo.accounts[1], map[string]any{}))
			if derived {
				require.NoError(t, plugins.NormalizeAccountBinding(context.Background(), &repo.accounts[0], map[string]any{}))
			}
			// Credentials share the same map with the scheduler repository.
			tasks := &nativeAdmissionTaskRepo{}
			if derived {
				tasks.candidates = []service.GenerationVideoDecodeCandidate{{VersionID: 1, Mapping: map[string]any{"public-video": tc.model}}}
				tasks.parent = &service.GenerationTask{ID: "video_" + strings.Repeat("1", 40), UserID: 10, APIKeyID: 20, GroupID: group.ID, AccountID: account.ID, PluginVersionID: 1, Platform: tc.platform, Model: "public-video", UpstreamModel: tc.model, Status: service.GenerationSucceeded, UpstreamID: "private-parent", Action: "text_to_video", Data: []byte(`{}`), Connection: service.GenerationConnection{BaseURL: "https://upstream.example", Kind: "vendor"}, ConfigEncrypted: account.GetCredential("newapi_plugin_config_encrypted")}
				tasks.parent.PublicFields = service.GenerationVideoFields{Seconds: "8", Size: "1280x720"}
			}
			generations := service.NewGenerationService(tasks, plugins, repo, gateway, nil, nil)
			defer generations.Stop()
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			defer billing.Stop()
			h := NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
			h.generations = generations
			for range 2 {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Request.Header.Set("Idempotency-Key", "native-once")
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 20, UserID: 10, User: &service.User{ID: 10}, GroupID: &group.ID, Group: group})
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 5})
				if remix {
					h.GenerationRemixIngress(c)
				}
				if !c.IsAborted() {
					require.True(t, h.TryPluginNativeVideo(c, tc.platform, tc.protocol))
				}
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				require.NotNil(t, tasks.task)
				require.Equal(t, account.ID, tasks.task.AccountID)
				require.Equal(t, tc.protocol, tasks.task.Protocol)
				require.Contains(t, w.Body.String(), tasks.task.ID)
				require.NotContains(t, w.Body.String(), "fixture-key")
				slots.assertReleased(t)
			}
			require.Equal(t, 1, tasks.holds)
			if derived {
				require.Len(t, tasks.task.Origins, 1)
				require.Equal(t, 1, slots.acquired, "the original account is chosen before acquiring a slot; replay acquires none")
			}
			require.Zero(t, tasks.nativeLogs)
			require.Zero(t, upstream.calls, "HTTP admission must only enqueue, never submit twice")
		})
	}
}
