//go:build unit

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type nativeVideoRecoveryFixture struct {
	taskLogFixture
	lookup                service.NativeVideoLookup
	recovery              *service.NativeVideoRecovery
	lookupErr, observeErr error
	beforePersist         func()
	observed              []service.NativeVideoObservation
	deadline              time.Time
}

func (r *nativeVideoRecoveryFixture) ObserveNativeVideo(_ context.Context, o service.NativeVideoObservation) error {
	if r.beforePersist != nil {
		r.beforePersist()
	}
	if r.observeErr != nil {
		return r.observeErr
	}
	r.observed = append(r.observed, o)
	if o.Create && r.recovery == nil {
		r.lookup = service.NativeVideoLookup{UserID: o.UserID, APIKeyID: o.APIKeyID, GroupID: o.GroupID, Protocol: o.Protocol, UpstreamID: o.UpstreamID}
		r.recovery = &service.NativeVideoRecovery{AccountID: o.AccountID, PendingBilling: o.PendingBilling}
	}
	return nil
}

func (r *nativeVideoRecoveryFixture) GetNativeVideoRecovery(ctx context.Context, q service.NativeVideoLookup) (*service.NativeVideoRecovery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.deadline, _ = ctx.Deadline()
	if r.lookupErr != nil {
		return nil, r.lookupErr
	}
	if q != r.lookup || r.recovery == nil {
		return nil, service.ErrGenerationTaskNotFound
	}
	return r.recovery, nil
}

func attachNativeVideoRecovery(t *testing.T, h *OpenAIGatewayHandler, repo *nativeVideoRecoveryFixture) {
	t.Helper()
	h.generations = service.NewGenerationService(repo, nil, nil, nil, nil, nil)
	t.Cleanup(h.generations.Stop)
}

func TestNativeVideoRecoveryCreateThenCacheLoss(t *testing.T) {
	for _, operation := range []string{"generate", "edit", "extend", "ark"} {
		t.Run(operation, func(t *testing.T) {
			platform := service.PlatformGrok
			if operation == "ark" {
				platform = service.PlatformOpenAI
			}
			h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, platform)
			repo := &nativeVideoRecoveryFixture{}
			attachNativeVideoRecovery(t, h, repo)
			creates := 0
			upstream.call = func(req *http.Request, account int64) (*http.Response, error) {
				body := `{"request_id":"task","status":"pending"}`
				if operation == "ark" {
					body = `{"id":"task","status":"running"}`
				}
				if req.Method == http.MethodPost {
					creates++
				}
				if repo.recovery != nil {
					require.Equal(t, repo.recovery.AccountID, account)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}
			c, w := grokMediaSlotContext(context.Background(), true)
			key, _ := middleware.GetAPIKeyFromContext(c)
			key.Group.Platform = platform
			create := h.GrokVideoGeneration
			switch operation {
			case "edit":
				create = h.GrokVideoEdit
				c.Request = nativeVideoRecoveryCreateRequest(`{"model":"grok-imagine-video","video":{"url":"https://media.example/source.mp4"},"prompt":"waves","duration":6,"resolution":"720p"}`)
			case "extend":
				create = h.GrokVideoExtension
				c.Request = nativeVideoRecoveryCreateRequest(`{"model":"grok-imagine-video","video":{"url":"https://media.example/source.mp4"},"prompt":"waves","duration":6,"resolution":"720p"}`)
			case "ark":
				create = h.SeedanceTasks
				c.Request = nativeVideoRecoveryCreateRequest(`{"model":"doubao-seedance","content":[{"type":"text","text":"waves"}],"duration":-1}`)
			default:
				c.Request = nativeVideoRecoveryCreateRequest(`{"model":"grok-imagine-video","prompt":"waves","duration":6,"resolution":"720p"}`)
			}
			repo.beforePersist = func() {
				require.False(t, w.Flushed)
				require.Equal(t, -1, c.Writer.Size(), "snapshot must precede the create response")
			}
			create(c)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.NotNil(t, repo.recovery)
			require.NotNil(t, repo.recovery.PendingBilling)
			require.NoError(t, repo.recovery.PendingBilling.ValidateNativeVideoSnapshot())
			cachePending, err := h.gatewayService.LoadGrokVideoPendingBilling(context.Background(), repo.lookup.UpstreamID, key.UserID, key.ID)
			require.NoError(t, err)
			require.Equal(t, cachePending, repo.recovery.PendingBilling, "SQL and Redis must share identical normalized inputs")
			if operation != "ark" {
				require.Equal(t, "720p", cachePending.VideoResolution)
				require.Equal(t, 6, cachePending.VideoDurationSeconds)
			}
			bindings.owner, bindings.pending, bindings.writes = 0, nil, 0
			repo.beforePersist = nil
			for _, cacheState := range []string{"empty", "unavailable"} {
				if cacheState == "unavailable" {
					bindings.lookupErr, bindings.pendingErr = errors.New("Redis unavailable"), errors.New("Redis unavailable")
				}
				c, w = grokMediaSlotContext(context.Background(), false)
				key, _ = middleware.GetAPIKeyFromContext(c)
				key.Group.Platform = platform
				if operation == "ark" {
					c.Params = gin.Params{{Key: "task_id", Value: "task"}}
					h.SeedanceTasks(c)
				} else {
					h.GrokVideoStatus(c)
				}
				require.Equal(t, http.StatusOK, w.Code, cacheState+": "+w.Body.String())
				slots.assertReleased(t)
			}
			require.Equal(t, 1, creates, "recovery must never submit another create")
			require.Zero(t, bindings.writes, "recovery must not shorten or overwrite ownership bindings")
			require.WithinDuration(t, time.Now().Add(3*time.Second), repo.deadline, time.Second)
			if operation != "ark" {
				// Content uses the same recovered owner and preserves the native
				// signed-download/Range rules, without sending the upstream Key to CDN.
				upstream.call = func(req *http.Request, id int64) (*http.Response, error) {
					require.Equal(t, repo.recovery.AccountID, id)
					if req.URL.Hostname() == "vidgen.x.ai" {
						require.Empty(t, req.Header.Get("Authorization"))
						require.Equal(t, "bytes=0-3", req.Header.Get("Range"))
						return &http.Response{StatusCode: 206, Header: http.Header{"Content-Type": []string{"video/mp4"}, "Content-Range": []string{"bytes 0-3/4"}}, Body: io.NopCloser(strings.NewReader("clip"))}, nil
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":"done","video":{"url":"https://vidgen.x.ai/private.mp4?signature=hidden","duration":6}}`))}, nil
				}
				bindings.claimErr = errors.New("claim unavailable")
				c, w = grokMediaSlotContext(context.Background(), false)
				c.Request.Header.Set("Range", "bytes=0-3")
				h.GrokVideoContent(c)
				require.Equal(t, http.StatusPartialContent, w.Code, w.Body.String())
				require.Equal(t, "clip", w.Body.String())
				slots.assertReleased(t)
			}
			// Discovery still requires the shared Redis claim. The SQL fallback
			// restores facts, not permission to bypass concurrency protection.
			subject := middleware.AuthSubject{UserID: key.UserID}
			result := &service.OpenAIForwardResult{ResponseID: repo.lookup.UpstreamID, VideoCount: 1}
			prepare := func() *service.OpenAIForwardResult {
				if operation == "ark" {
					result.Usage.OutputTokens = 12345
					result.VideoCount = 0
					return prepareSeedanceCompletionBilling(context.Background(), h, key, subject, repo.recovery.AccountID, nil, result.ResponseID, result)
				}
				return prepareGrokVideoCompletionBilling(context.Background(), h, zap.NewNop(), key, subject, repo.recovery.AccountID, nil, result.ResponseID, result)
			}
			bindings.claimErr = errors.New("Redis claim unavailable")
			require.Nil(t, prepare())
			bindings.claimErr = nil
			bill := prepare()
			require.NotNil(t, bill)
			require.Equal(t, service.StableGrokVideoBillingRequestID(repo.lookup.UpstreamID), bill.RequestID)
			require.Equal(t, repo.recovery.PendingBilling.BillingModel, bill.BillingModel)
			if operation != "ark" {
				require.Equal(t, "720p", bill.VideoResolution)
				require.Equal(t, 6, bill.VideoDurationSeconds)
			} else {
				require.Equal(t, 12345, bill.Usage.OutputTokens)
			}
			require.Nil(t, prepare(), "repeated polls must share the original claim")
			require.NoError(t, h.gatewayService.ReleaseGrokVideoBilling(context.Background(), repo.lookup.UpstreamID, key.UserID, key.ID))
			require.NotNil(t, prepare(), "failed native billing can release its claim and retry")
		})
	}
}

func nativeVideoRecoveryCreateRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestNativeVideoRecoveryRejectsUnprovenOwnerAndBilling(t *testing.T) {
	for _, scenario := range []string{"user", "key", "group", "protocol", "task", "unrecorded", "ambiguous", "database failure", "cancelled", "account missing", "account full", "snapshot missing", "billing account mismatch", "bad cache"} {
		t.Run(scenario, func(t *testing.T) {
			h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false)
			bindings.owner, bindings.pending, bindings.writes = 0, nil, 0
			pending := service.GrokVideoPendingBilling{Model: "mapped", BillingModel: "billing", VideoResolution: "1080p", VideoDurationSeconds: 12, CreatedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)}
			repo := &nativeVideoRecoveryFixture{lookup: service.NativeVideoLookup{UserID: 10, APIKeyID: 20, GroupID: 24, Protocol: service.GenerationProtocolXAI, UpstreamID: "task"}, recovery: &service.NativeVideoRecovery{AccountID: 1, PendingBilling: &pending}}
			attachNativeVideoRecovery(t, h, repo)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, w := grokMediaSlotContext(ctx, false)
			key, _ := middleware.GetAPIKeyFromContext(c)
			switch scenario {
			case "user":
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 11})
			case "key":
				key.ID++
			case "group":
				group := int64(25)
				key.GroupID = &group
			case "protocol":
				repo.lookup.Protocol = service.GenerationProtocolArk
			case "task":
				c.Params = gin.Params{{Key: "request_id", Value: "other"}}
			case "unrecorded":
				repo.recovery = nil
			case "ambiguous":
				repo.lookupErr = service.ErrNativeVideoRecoveryAmbiguous
			case "database failure":
				repo.lookupErr = errors.New("SQL unavailable")
			case "cancelled":
				cancel()
			case "account missing":
				repo.recovery.AccountID = 999
			case "account full":
				slots.full = true
			case "snapshot missing":
				repo.recovery.PendingBilling = nil
			case "bad cache":
				bindings.pending = map[string][]byte{"10:20:task": []byte(`broken`)}
				repo.recovery = nil
			}
			billingCase := scenario == "snapshot missing" || scenario == "billing account mismatch" || scenario == "bad cache"
			if billingCase {
				accountID := int64(1)
				if scenario == "billing account mismatch" {
					accountID = 2
				}
				result := &service.OpenAIForwardResult{ResponseID: "task", VideoCount: 1, VideoDurationSeconds: 8}
				require.Nil(t, prepareGrokVideoCompletionBilling(ctx, h, zap.NewNop(), key, middleware.AuthSubject{UserID: 10}, accountID, nil, "task", result))
				require.Zero(t, bindings.claims, "unproven pricing must not burn the shared billing claim")
			} else {
				h.GrokVideoStatus(c)
				require.GreaterOrEqual(t, w.Code, 400, w.Body.String())
			}
			require.Zero(t, upstream.calls)
			require.Zero(t, bindings.writes)
			slots.assertReleased(t)
		})
	}
}

func TestNativeVideoRecoveryPartialEvictionUsesSameDurableSnapshot(t *testing.T) {
	for _, protocol := range []string{service.GenerationProtocolXAI, service.GenerationProtocolArk} {
		for _, missing := range []bool{false, true} {
			t.Run(protocol+"/missing="+strconv.FormatBool(missing), func(t *testing.T) {
				h, _, bindings, _ := newGrokMediaSlotHandler(t, false, false)
				c, _ := grokMediaSlotContext(context.Background(), false)
				key, _ := middleware.GetAPIKeyFromContext(c)
				taskID, endpoint := "task", service.GrokMediaEndpointVideoStatus
				if protocol == service.GenerationProtocolArk {
					taskID, endpoint = service.SeedanceTaskKey("task"), service.SeedanceEndpointStatus
				}
				pending := service.GrokVideoPendingBilling{Model: "mapped", BillingModel: "original-billing-model", VideoResolution: "1080p", VideoDurationSeconds: 12, CreatedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)}
				repo := &nativeVideoRecoveryFixture{lookup: service.NativeVideoLookup{UserID: 10, APIKeyID: 20, GroupID: 24, UpstreamID: taskID, Protocol: protocol}, recovery: &service.NativeVideoRecovery{AccountID: 1, PendingBilling: &pending}}
				attachNativeVideoRecovery(t, h, repo)
				// Pending belongs to an older group/account that reused this raw
				// ID. Only its owner binding was evicted. Do not trust this cache.
				require.NoError(t, h.gatewayService.StoreGrokVideoPendingBilling(context.Background(), taskID, key.UserID, key.ID, service.GrokVideoPendingBilling{Model: "stale-model", BillingModel: "stale-model", VideoResolution: "480p", VideoDurationSeconds: 2}))
				bindings.owner = 0
				if missing {
					repo.recovery.PendingBilling = nil
				}
				accountID, recovered, err := h.resolveNativeVideoRequestAccount(context.Background(), key, key.UserID, endpoint, taskID)
				require.NoError(t, err)
				require.NotNil(t, recovered)
				result := &service.OpenAIForwardResult{ResponseID: taskID, VideoCount: 1, VideoDurationSeconds: 12, Usage: service.OpenAIUsage{OutputTokens: 12345}}
				subject := middleware.AuthSubject{UserID: key.UserID}
				var bill *service.OpenAIForwardResult
				if protocol == service.GenerationProtocolArk {
					bill = prepareSeedanceCompletionBilling(context.Background(), h, key, subject, accountID, recovered, taskID, result)
				} else {
					bill = prepareGrokVideoCompletionBilling(context.Background(), h, zap.NewNop(), key, subject, accountID, recovered, taskID, result)
				}
				if missing {
					require.Nil(t, bill, "leftover Redis cannot fill an unproven durable snapshot")
					require.Zero(t, bindings.claims)
				} else {
					require.NotNil(t, bill)
					require.Equal(t, "original-billing-model", bill.BillingModel)
					if protocol == service.GenerationProtocolXAI {
						require.Equal(t, "1080p", bill.VideoResolution)
						require.Equal(t, 12, bill.VideoDurationSeconds)
					}
				}
			})
		}
	}
}

func TestNativeVideoRecoveryFailedPersistenceDoesNotReplayCreate(t *testing.T) {
	h, slots, _, upstream := newGrokMediaSlotHandler(t, false, false)
	repo := &nativeVideoRecoveryFixture{observeErr: errors.New("database unavailable")}
	attachNativeVideoRecovery(t, h, repo)
	c, w := grokMediaSlotContext(context.Background(), true)
	h.GrokVideoGeneration(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "task")
	require.NotContains(t, w.Body.String(), "database unavailable")
	require.Equal(t, 1, upstream.calls)
	require.Nil(t, repo.recovery)
	slots.assertReleased(t)
}
