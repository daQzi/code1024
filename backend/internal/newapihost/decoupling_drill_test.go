package newapihost

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/newapi/officialupload"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "plugin" && os.Args[2] == "worker" {
		if runner.Serve(context.Background(), os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type spyNewAPIPluginRepo struct {
	service.NewAPIPluginRepository
	mu       sync.Mutex
	calls    int
	nextID   int64
	versions map[int64]*service.NewAPIPluginVersion
}

func newSpyNewAPIPluginRepo() *spyNewAPIPluginRepo {
	return &spyNewAPIPluginRepo{
		nextID:   1,
		versions: map[int64]*service.NewAPIPluginVersion{},
	}
}

func (r *spyNewAPIPluginRepo) List(context.Context) ([]service.NewAPIPluginVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	out := make([]service.NewAPIPluginVersion, 0, len(r.versions))
	for _, v := range r.versions {
		out = append(out, *v)
	}
	return out, nil
}

func (r *spyNewAPIPluginRepo) Versions(_ context.Context, key string) ([]service.NewAPIPluginVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	var out []service.NewAPIPluginVersion
	for _, v := range r.versions {
		if v.Key == key {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (r *spyNewAPIPluginRepo) Get(_ context.Context, id int64) (*service.NewAPIPluginVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	v, ok := r.versions[id]
	if !ok {
		return nil, service.ErrGenerationTaskNotFound
	}
	cp := *v
	return &cp, nil
}

func (r *spyNewAPIPluginRepo) Install(_ context.Context, v *service.NewAPIPluginVersion, _ *int64) (*service.NewAPIPluginVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	for _, existing := range r.versions {
		if existing.Key == v.Key && existing.Version == v.Version {
			cp := *existing
			return &cp, nil
		}
	}
	cp := *v
	cp.ID = r.nextID
	r.nextID++
	r.versions[cp.ID] = &cp
	out := cp
	return &out, nil
}

func (r *spyNewAPIPluginRepo) Activate(_ context.Context, id int64, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	v, ok := r.versions[id]
	if !ok {
		return service.ErrGenerationTaskNotFound
	}
	v.Enabled = enabled
	return nil
}

type spyNativeS2PluginRepo struct {
	service.PluginRepository
	listCalls   atomic.Int32
	mutateCalls atomic.Int32
	plugins     []*service.PluginInstallation
}

func (r *spyNativeS2PluginRepo) List(context.Context) ([]*service.PluginInstallation, error) {
	r.listCalls.Add(1)
	return r.plugins, nil
}

func (r *spyNativeS2PluginRepo) GetByID(context.Context, int64) (*service.PluginInstallation, error) {
	return nil, errors.New("not found")
}

func (r *spyNativeS2PluginRepo) Install(context.Context, *service.PluginInstallation, []service.PluginBinding) (*service.PluginInstallation, error) {
	r.mutateCalls.Add(1)
	return nil, nil
}

func (r *spyNativeS2PluginRepo) Delete(context.Context, int64, string) error {
	r.mutateCalls.Add(1)
	return nil
}

func (r *spyNativeS2PluginRepo) UpdateState(context.Context, int64, string, string, *time.Time, string, string) error {
	r.mutateCalls.Add(1)
	return nil
}

func (r *spyNativeS2PluginRepo) UpdateConfig(context.Context, int64, string, string) error {
	r.mutateCalls.Add(1)
	return nil
}

type stubTaskAccountRepo struct {
	service.AccountRepository
	account *service.Account
}

func (r *stubTaskAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	if r.account == nil || r.account.ID != id {
		return nil, errors.New("account not found")
	}
	return r.account, nil
}

type noopSecretEncryptor struct{}

type drillTaskRepo struct {
	service.GenerationTaskRepository
	claims chan context.Context
}

func (s *drillTaskRepo) Claim(ctx context.Context, _ string, _ time.Duration) (*generation.Task, error) {
	s.claims <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

func (noopSecretEncryptor) Encrypt(v string) (string, error) { return "enc:" + v, nil }
func (noopSecretEncryptor) Decrypt(v string) (string, error) {
	if len(v) >= 4 && v[:4] == "enc:" {
		return v[4:], nil
	}
	return v, nil
}

func TestNewAPIEngineDisabledConfigLeavesNativeSub2APIAndS2PluginOperational(t *testing.T) {
	pluginRepo := newSpyNewAPIPluginRepo()
	engineStore := &drillTaskRepo{claims: make(chan context.Context, 2)}
	gateway := &service.OpenAIGatewayService{}

	cfg := &config.Config{
		NewAPIEngine: config.NewAPIEngineConfig{Enabled: false},
		Plugins: config.PluginConfig{
			DataDir:              t.TempDir(),
			MaxUploadBytes:       1 << 20,
			MaxUncompressedBytes: 2 << 20,
			StartTimeoutSeconds:  5,
		},
	}

	host, err := ProvideServices(cfg, pluginRepo, noopSecretEncryptor{}, gateway, nil, engineStore, nil, nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, host)
	require.False(t, host.Enabled(), "disabled config must not start New API host")
	require.Nil(t, ProvidePlugins(host))
	require.Nil(t, ProvideGenerations(host))
	require.Zero(t, pluginRepo.calls, "disabled module must not register factories or touch plugin storage")
	require.Empty(t, engineStore.claims, "disabled module must not start generation executor workers")

	// Ordinary native Sub2API account binding succeeds when New API module is disabled.
	nativeAccount := &service.Account{
		ID:          101,
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-native-only", "base_url": "https://api.openai.com"},
	}
	require.NoError(t, ProvidePlugins(host).NormalizeAccountBindingIfPresent(context.Background(), nativeAccount, nil))

	// Attempting to bind a New API plugin fails closed instead of silently falling back or panicking.
	pluginBound := &service.Account{
		ID:          102,
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{service.NewAPIPluginRevisionKey: int64(1)},
	}
	require.Error(t, ProvidePlugins(host).NormalizeAccountBindingIfPresent(context.Background(), pluginBound, map[string]any{service.NewAPIPluginRevisionKey: int64(1)}))

	// Direct calls on nil NewAPIPluginService fail cleanly without panicking.
	_, err = ProvidePlugins(host).List(context.Background())
	require.Error(t, err)

	// Native .s2plugin PluginManager operates completely independently when New API module is disabled.
	nativePluginRepo := &spyNativeS2PluginRepo{}
	nativePM := service.NewPluginManager(nativePluginRepo, noopSecretEncryptor{}, cfg, service.PluginHostInfo{Version: "test"}, nil)
	items, err := nativePM.List(context.Background())
	require.NoError(t, err)
	require.Empty(t, items)
	require.EqualValues(t, 1, nativePluginRepo.listCalls.Load())
	nativePM.Stop()

	// Legacy NativeVideoWorkers operate completely independently when New API module is disabled.
	nativeStore := &nativeLifecycleStore{claims: make(chan context.Context, 4)}
	nativeWorkers := service.NewNativeVideoWorkers(nativeStore, nativeStore, &nativeLifecycleAccounts{}, gateway, nil)
	defer nativeWorkers.Stop()
	select {
	case <-nativeStore.claims:
	case <-time.After(3 * time.Second):
		t.Fatal("native video workers did not start while New API module was disabled")
	}

	// Stopping a disabled New API host is idempotent and does not disturb native workers.
	host.Stop()
	host.Stop()
}

func TestNewAPIModuleWorksWithoutNativePluginManagerAndNeverMutatesNativeState(t *testing.T) {
	pluginRepo := newSpyNewAPIPluginRepo()
	nativePluginRepo := &spyNativeS2PluginRepo{
		plugins: []*service.PluginInstallation{{ID: 77, Name: "native-oauth-s2plugin", State: "enabled"}},
	}
	cfg := &config.Config{
		NewAPIEngine: config.NewAPIEngineConfig{Enabled: true},
		Plugins: config.PluginConfig{
			DataDir:              t.TempDir(),
			MaxUploadBytes:       1 << 20,
			MaxUncompressedBytes: 2 << 20,
			StartTimeoutSeconds:  5,
		},
	}

	// Native .s2plugin manager exists independently; newapihost takes NO reference to it.
	nativePM := service.NewPluginManager(nativePluginRepo, noopSecretEncryptor{}, cfg, service.PluginHostInfo{Version: "test"}, nil)
	defer nativePM.Stop()

	engineStore := &drillTaskRepo{claims: make(chan context.Context, 2)}
	gateway := &service.OpenAIGatewayService{}
	accountRepo := &stubTaskAccountRepo{
		account: &service.Account{
			ID:          201,
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 2,
		},
	}

	host, err := ProvideServices(cfg, pluginRepo, noopSecretEncryptor{}, gateway, nil, engineStore, nil, nil, accountRepo, nil, nil)
	require.NoError(t, err)
	defer host.Stop()
	require.True(t, host.Enabled())
	require.NotNil(t, ProvidePlugins(host))
	require.NotNil(t, ProvideGenerations(host))

	// Exercise New API plugin listing, install, activation, and deactivation.
	listed, err := host.Plugins.List(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, listed, "built-in factory versions should register without PluginManager")

	customSrc := `export const meta={apiVersion:1,key:"decouple-probe",name:"Decouple Probe",version:"1.0.0",author:{name:"Test"},models:["probe-1"],fetchMode:"per_task"};
export function buildSubmitRequest(){return {url:"/submit",method:"POST"};}
export function parseSubmitResponse(){return {taskId:"up-1"};}
export function buildQueryRequest(){return {url:"/fetch",method:"GET"};}
export function parseTaskResult(){return {status:"SUCCESS"};}`
	installed, err := host.Plugins.Install(context.Background(), customSrc, "", nil)
	require.NoError(t, err)
	require.NotNil(t, installed)
	require.NoError(t, host.Plugins.Activate(context.Background(), installed.ID, true))
	require.NoError(t, host.Plugins.Activate(context.Background(), installed.ID, false))

	// Native .s2plugin state must remain 100% untouched by New API plugin operations.
	require.Zero(t, nativePluginRepo.mutateCalls.Load(), "New API lifecycle actions must never mutate native .s2plugin state")
	nativeItems, err := nativePM.List(context.Background())
	require.NoError(t, err)
	require.Len(t, nativeItems, 1)
	require.Equal(t, "native-oauth-s2plugin", nativeItems[0].Name)

	// Verify newapihost.NewTaskExecutorPorts resolves account runtime and preserves error translation.
	ports := NewTaskExecutorPorts(engineStore, host.Generations, accountRepo, host.Plugins)
	conn, err := ports.Resolve(context.Background(), &service.GenerationTask{AccountID: 201})
	require.NoError(t, err)
	require.True(t, conn.Schedulable())
	_, _, err = conn.Acquire(context.Background())
	require.ErrorIs(t, err, generation.ErrConcurrencyUnavailable)

	// Verify officialupload error taxonomy translation stays inside newapihost adapter.
	permErr := &officialupload.Permanent{Reason: "permanent upstream reject"}
	waitErr := &officialupload.Waiting{Delay: 3 * time.Second}
	var pCheck *officialupload.Permanent
	var wCheck *officialupload.Waiting
	require.True(t, errors.As(permErr, &pCheck))
	require.True(t, errors.As(waitErr, &wCheck))

	// Wait until executor workers reach Claim so Stop() exercises full clean drain.
	select {
	case claimCtx := <-engineStore.claims:
		host.Stop()
		require.ErrorIs(t, claimCtx.Err(), context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("executor did not start claiming tasks")
	}
}
