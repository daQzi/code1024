//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type nativePollAccounts struct {
	AccountRepository
	account *Account
	err     error
}

func (r *nativePollAccounts) GetByID(context.Context, int64) (*Account, error) {
	return r.account, r.err
}

type nativePollTransport struct {
	HTTPUpstream
	do    func(*http.Request) (*http.Response, error)
	calls atomic.Int32
}

func (u *nativePollTransport) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	return u.do(req)
}

type nativePollSlots struct {
	ConcurrencyCache
	full               bool
	acquired, released atomic.Int32
}

func (s *nativePollSlots) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	if s.full {
		return false, nil
	}
	s.acquired.Add(1)
	return true, nil
}
func (s *nativePollSlots) ReleaseAccountSlot(context.Context, int64, string) error {
	s.released.Add(1)
	return nil
}

type nativePollRepo struct {
	GenerationTaskRepository
	mu      sync.Mutex
	task    *NativeVideoPollTask
	claimed bool
	saved   chan NativeVideoPollUpdate
}

func (r *nativePollRepo) Claim(context.Context, string, time.Duration) (*GenerationTask, error) {
	return nil, ErrGenerationTaskNotFound
}
func (r *nativePollRepo) ClaimNativeVideoPoll(_ context.Context, token string, _ time.Duration) (*NativeVideoPollTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimed || r.task == nil {
		return nil, ErrGenerationTaskNotFound
	}
	r.claimed = true
	copy := *r.task
	copy.LeaseToken = token
	return &copy, nil
}
func (r *nativePollRepo) SaveNativeVideoPoll(_ context.Context, _ *NativeVideoPollTask, u NativeVideoPollUpdate) error {
	r.saved <- u
	return nil
}

func nativePollFixture(t *testing.T, protocol string) (*NativeVideoWorkers, *nativePollRepo, *nativePollAccounts, *nativePollTransport, *nativePollSlots) {
	t.Helper()
	account := &Account{ID: 4, Platform: PlatformGrok, Type: AccountTypeAPIKey, Status: StatusActive, Concurrency: 2,
		Credentials: map[string]any{"api_key": "original-key", "base_url": "https://upstream.example/v1"}}
	id := "native-task"
	if protocol == GenerationProtocolArk {
		account.Platform = PlatformOpenAI
		account.Credentials["base_url"] = "https://upstream.example/api/v3"
		account.Credentials["openai_capabilities"] = []string{"seedance"}
		id = SeedanceTaskKey(id)
	}
	slots := &nativePollSlots{}
	transport := &nativePollTransport{do: func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "Bearer original-key", req.Header.Get("Authorization"))
		body := `{"status":"done","video":{"url":"https://vidgen.x.ai/private?signature=secret"}}`
		if protocol == GenerationProtocolArk {
			body = `{"id":"native-task","status":"succeeded","content":{"video_url":"https://media.example/private?signature=secret"}}`
		}
		return grokMediaContentStatusResponse(body), nil
	}}
	gateway := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: transport, concurrencyService: NewConcurrencyService(slots)}
	hash, err := gateway.NativeVideoPollConnectionHash(account, protocol, id)
	require.NoError(t, err)
	now := time.Now().UTC()
	task := &NativeVideoPollTask{ConnectionHash: hash, LeaseToken: "fixture", ExpiresAt: now.Add(time.Hour), Observation: NativeVideoObservation{
		UserID: 1, APIKeyID: 2, GroupID: 3, AccountID: account.ID, UpstreamID: id, Protocol: protocol, Platform: account.Platform,
		Status: GenerationQueued, DeliveryStatus: GenerationDeliveryPending, CreatedAt: now, ObservedAt: now,
	}}
	repo := &nativePollRepo{task: task, saved: make(chan NativeVideoPollUpdate, 8)}
	accounts := &nativePollAccounts{account: account}
	svc := &NativeVideoWorkers{accounts: accounts, gateway: gateway}
	return svc, repo, accounts, transport, slots
}

func TestNativeVideoPollTransitionsAndLimits(t *testing.T) {
	for _, protocol := range []string{GenerationProtocolXAI, GenerationProtocolArk} {
		for _, scenario := range []string{"success", "running", "failed", "delivery pending", "already terminal", "expired", "unknown status", "wrong id", "wrong id type", "bad json", "oversized", "truncated", "redirect", "auth", "upstream error", "transport error", "full", "unavailable account", "changed connection", "rotated key", "last retry"} {
			t.Run(protocol+"/"+scenario, func(t *testing.T) {
				svc, repo, accounts, upstream, slots := nativePollFixture(t, protocol)
				body, status := "", http.StatusOK
				wantStatus, wantTask, wantCode := NativeVideoMonitorActive, "", ""
				switch scenario {
				case "success":
					wantStatus, wantTask = NativeVideoMonitorComplete, GenerationSucceeded
				case "running":
					if protocol == GenerationProtocolArk {
						body = `{"status":"running"}`
					} else {
						body = `{"status":"pending"}`
					}
					wantTask = GenerationRunning
				case "failed":
					body = `{"status":"failed"}`
					wantStatus, wantTask = NativeVideoMonitorComplete, GenerationFailed
				case "delivery pending":
					if protocol == GenerationProtocolArk {
						body = `{"status":"succeeded"}`
					} else {
						body = `{"status":"done"}`
					}
					wantTask = GenerationSucceeded
				case "already terminal":
					repo.task.Observation.Status, repo.task.Observation.DeliveryStatus = GenerationSucceeded, GenerationDeliveryReady
					wantStatus = NativeVideoMonitorComplete
				case "expired":
					repo.task.ExpiresAt = time.Now().Add(-time.Second)
					wantStatus, wantCode = NativeVideoMonitorReview, "native_monitor_timeout"
				case "unknown status":
					body = `{"status":"invented"}`
					wantCode = "native_monitor_unrecognized"
				case "wrong id":
					body = `{"request_id":"other","id":"other","status":"failed"}`
					wantCode = "native_monitor_unrecognized"
				case "wrong id type":
					body = `{"request_id":123,"id":123,"status":"failed"}`
					wantCode = "native_monitor_unrecognized"
				case "bad json":
					body = `broken`
					wantCode = "native_monitor_unrecognized"
				case "oversized":
					body = strings.Repeat("x", nativeVideoPollBodyLimit+1)
					wantCode = "native_monitor_response_limit"
				case "truncated":
					wantCode = "native_monitor_upstream_error"
				case "redirect":
					status, wantCode = 302, "native_monitor_upstream_error"
				case "auth":
					status, wantCode = 401, "native_monitor_auth_failed"
				case "upstream error":
					status, wantCode = 503, "native_monitor_upstream_error"
				case "transport error":
					wantCode = "native_monitor_upstream_error"
				case "full":
					slots.full, repo.task.Failures = true, 9
					wantCode = "native_monitor_capacity"
				case "unavailable account":
					accounts.account.Status = StatusDisabled
					wantStatus, wantCode = NativeVideoMonitorReview, "native_monitor_account_unavailable"
				case "changed connection":
					accounts.account.Credentials["base_url"] = "https://other.example/v1"
					wantStatus, wantCode = NativeVideoMonitorReview, "native_monitor_connection_changed"
				case "rotated key":
					accounts.account.Credentials["api_key"] = "rotated-key"
					wantStatus, wantTask = NativeVideoMonitorComplete, GenerationSucceeded
				case "last retry":
					repo.task.Failures, status, wantStatus, wantCode = 9, 500, NativeVideoMonitorReview, "native_monitor_upstream_error"
				}
				original := upstream.do
				upstream.do = func(req *http.Request) (*http.Response, error) {
					require.Equal(t, "upstream.example", req.URL.Hostname())
					if protocol == GenerationProtocolArk {
						require.Equal(t, "/api/v3/contents/generations/tasks/native-task", req.URL.Path)
					} else {
						require.Equal(t, "/v1/videos/native-task", req.URL.Path)
					}
					if scenario == "transport error" {
						return nil, errors.New("private upstream transport information")
					}
					if scenario == "truncated" {
						return &http.Response{StatusCode: 200, Body: nativePollBrokenBody{}}, nil
					}
					if scenario == "rotated key" {
						require.Equal(t, "Bearer rotated-key", req.Header.Get("Authorization"))
						req.Header.Set("Authorization", "Bearer original-key")
					}
					resp, err := original(req)
					resp.StatusCode = status
					if body != "" {
						resp.Body = io.NopCloser(strings.NewReader(body))
					}
					return resp, err
				}
				require.NoError(t, svc.processNativeVideoPoll(context.Background(), repo, repo.task))
				u := <-repo.saved
				require.Equal(t, wantStatus, u.MonitorStatus)
				require.Equal(t, wantCode, u.ErrorCode)
				if wantTask == "" {
					require.Nil(t, u.State)
				} else {
					require.Equal(t, wantTask, u.State.Status)
				}
				if scenario == "full" || scenario == "changed connection" || scenario == "unavailable account" || scenario == "expired" || scenario == "already terminal" {
					require.Zero(t, upstream.calls.Load())
				}
				require.Equal(t, slots.acquired.Load(), slots.released.Load())
				if scenario == "full" {
					require.Equal(t, 15*time.Second, u.Delay, "busy accounts do not exhaust retry allowance")
				}
			})
		}
	}
}

type nativePollBrokenBody struct{}

func (nativePollBrokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (nativePollBrokenBody) Close() error             { return nil }

func TestNativeVideoPollConnectionIdentity(t *testing.T) {
	svc, repo, accounts, _, _ := nativePollFixture(t, GenerationProtocolXAI)
	for _, change := range []func(*Account){
		func(a *Account) { a.Type = AccountTypeOAuth },
		func(a *Account) { a.Platform = PlatformOpenAI },
		func(a *Account) { a.Credentials["newapi_plugin_version_id"] = int64(1) },
		func(a *Account) {
			a.Credentials["header_override_enabled"] = true
			a.Credentials["header_overrides"] = map[string]any{"x-route": "changed"}
		},
		func(a *Account) {
			id := int64(9)
			a.ProxyID = &id
			a.Proxy = &Proxy{ID: id, Protocol: "http", Host: "proxy.example", Port: 8080, Username: "user", Password: "secret"}
		},
	} {
		copy := *accounts.account
		copy.Credentials = map[string]any{"api_key": "original-key", "base_url": "https://upstream.example/v1"}
		change(&copy)
		hash, err := svc.gateway.NativeVideoPollConnectionHash(&copy, GenerationProtocolXAI, repo.task.Observation.UpstreamID)
		if err == nil {
			require.NotEqual(t, repo.task.ConnectionHash, hash)
		}
	}
}

func TestNativeVideoPollWorkerLifecycle(t *testing.T) {
	for _, cancelDuringHTTP := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "shutdown during HTTP"}[cancelDuringHTTP], func(t *testing.T) {
			svc, repo, accounts, upstream, slots := nativePollFixture(t, GenerationProtocolXAI)
			entered := make(chan struct{}, 1)
			if cancelDuringHTTP {
				upstream.do = func(req *http.Request) (*http.Response, error) {
					entered <- struct{}{}
					<-req.Context().Done()
					return nil, req.Context().Err()
				}
			}
			worker := NewNativeVideoWorkers(repo, nil, accounts, svc.gateway, nil)
			t.Cleanup(worker.Stop)
			if cancelDuringHTTP {
				select {
				case <-entered:
				case <-time.After(4 * time.Second):
					t.Fatal("worker did not start")
				}
			} else {
				select {
				case u := <-repo.saved:
					require.Equal(t, NativeVideoMonitorComplete, u.MonitorStatus)
				case <-time.After(4 * time.Second):
					t.Fatal("worker did not save progress")
				}
			}
			stopped := make(chan struct{})
			go func() { worker.Stop(); close(stopped) }()
			select {
			case <-stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("worker shutdown did not cancel I/O")
			}
			require.EqualValues(t, 1, upstream.calls.Load(), "multiple workers must not process the same lease")
			require.Equal(t, slots.acquired.Load(), slots.released.Load())
			if cancelDuringHTTP {
				require.Empty(t, repo.saved, "cancelled work leaves its durable lease recoverable")
			}
		})
	}
}
