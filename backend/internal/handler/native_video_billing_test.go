//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNativeVideoBillingCreateRecoveryAndFailedClaimRelease(t *testing.T) {
	for _, protocol := range []string{service.GenerationProtocolXAI, service.GenerationProtocolArk} {
		t.Run(protocol, func(t *testing.T) {
			platform := service.PlatformGrok
			if protocol == service.GenerationProtocolArk {
				platform = service.PlatformOpenAI
			}
			h, slots, bindings, upstream := newGrokMediaSlotHandlerWithBilling(t, false, false, service.NewBillingService(&config.Config{}, nil), platform)
			repo := &nativeVideoRecoveryFixture{}
			attachNativeVideoRecovery(t, h, repo)
			upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
				require.Equal(t, http.MethodPost, req.Method)
				body := `{"request_id":"task","status":"pending"}`
				if protocol == service.GenerationProtocolArk {
					body = `{"id":"task","status":"running"}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}
			c, w := grokMediaSlotContext(context.Background(), true)
			key, _ := middleware.GetAPIKeyFromContext(c)
			key.Group.Platform, key.Group.RateMultiplier = platform, 2
			price := .037
			key.Group.VideoPrice720P = &price
			key.Group.VideoRateMultiplier, key.Group.ImageRateMultiplier = 1, 1
			if protocol == service.GenerationProtocolArk {
				output := .00001
				key.Group.ModelPricing = []service.ChannelModelPricing{{Models: []string{"video-model"}, BillingMode: service.BillingModeToken, OutputPrice: &output}}
				c.Request = nativeVideoRecoveryCreateRequest(`{"model":"video-model","content":[{"type":"text","text":"waves"}]}`)
				h.SeedanceTasks(c)
			} else {
				c.Request = nativeVideoRecoveryCreateRequest(`{"model":"grok-imagine-video","prompt":"waves","duration":6,"resolution":"720p"}`)
				h.GrokVideoGeneration(c)
			}
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.NotNil(t, repo.recovery)
			pending := repo.recovery.PendingBilling
			require.NotNil(t, pending.Financial)
			require.NoError(t, pending.ValidateNativeVideoSnapshot())
			require.NotContains(t, w.Body.String(), "financial")
			require.NotContains(t, w.Body.String(), "unit_actual")
			cached, err := h.gatewayService.LoadGrokVideoPendingBilling(context.Background(), repo.lookup.UpstreamID, key.UserID, key.ID)
			require.NoError(t, err)
			require.Equal(t, pending, cached)
			// Completion sees edited settings, but carries the accepted price.
			price, key.Group.RateMultiplier, key.Group.ModelPricing = 99, 99, nil
			for _, cache := range []string{"original", "legacy input", "reobserved cache", "empty", "unavailable", "wrong owner"} {
				t.Run(cache, func(t *testing.T) {
					bindings.billed, bindings.pendingErr = nil, nil
					encoded, err := json.Marshal(pending)
					require.NoError(t, err)
					switch cache {
					case "legacy input":
						legacy := *pending
						legacy.Financial = nil
						encoded, err = json.Marshal(legacy)
						require.NoError(t, err)
					case "empty":
						encoded = nil
					case "reobserved cache":
						later := *pending
						financial := *pending.Financial
						financial.UnitActual = financial.UnitActual.Mul(decimal.NewFromInt(2))
						later.Financial = &financial
						encoded, err = json.Marshal(later)
						require.NoError(t, err)
					case "unavailable":
						bindings.pendingErr = errors.New("Redis unavailable")
					case "wrong owner":
						bad := *pending
						financial := *pending.Financial
						financial.APIKeyID++
						bad.Financial = &financial
						encoded, err = json.Marshal(bad)
						require.NoError(t, err)
					}
					bindings.pending = map[string][]byte{"10:20:" + repo.lookup.UpstreamID: encoded}
					result := &service.OpenAIForwardResult{ResponseID: repo.lookup.UpstreamID, VideoCount: 1, VideoDurationSeconds: 6}
					subject := middleware.AuthSubject{UserID: key.UserID}
					prepare := func() *service.OpenAIForwardResult {
						if protocol == service.GenerationProtocolArk {
							result.VideoCount, result.Usage.OutputTokens = 0, 12345
							return prepareSeedanceCompletionBilling(context.Background(), h, key, subject, repo.recovery.AccountID, nil, result.ResponseID, result)
						}
						return prepareGrokVideoCompletionBilling(context.Background(), h, zap.NewNop(), key, subject, repo.recovery.AccountID, nil, result.ResponseID, result)
					}
					claims := bindings.claims
					bill := prepare()
					if cache == "wrong owner" {
						require.Nil(t, bill)
						require.Equal(t, claims, bindings.claims, "invalid evidence must not consume a claim")
						return
					}
					require.NotNil(t, bill)
					require.Equal(t, pending.Financial, bill.NativeVideoBilling)
					require.Nil(t, prepare(), "status and content share one claim")
					// Missing durable ledger must release the claim, so a later
					// attempt can settle instead of silently losing the charge.
					recordGrokMediaUsage(c, h, zap.NewNop(), key, subject, nil, &service.Account{ID: repo.recovery.AccountID, Type: service.AccountTypeAPIKey, Platform: platform}, bill, pending.Model, nil, result.ResponseID)
					require.NotNil(t, prepare(), "failed fixed-price billing must allow retry")
				})
			}
			require.Equal(t, 1, upstream.calls, "completion preparation never resubmits creation")
			slots.assertReleased(t)
		})
	}
}

func TestNativeVideoBillingPreservesLegacyCacheOnlyWithRedisOwner(t *testing.T) {
	h, _, bindings, _ := newGrokMediaSlotHandler(t, false, false)
	c, _ := grokMediaSlotContext(context.Background(), false)
	key, _ := middleware.GetAPIKeyFromContext(c)
	q := service.NativeVideoLookup{UserID: 10, APIKeyID: 20, GroupID: 24, Protocol: service.GenerationProtocolXAI, UpstreamID: "task"}
	repo := &nativeVideoRecoveryFixture{lookup: q, recovery: &service.NativeVideoRecovery{AccountID: 1}}
	attachNativeVideoRecovery(t, h, repo)
	legacy := service.GrokVideoPendingBilling{Model: "grok-imagine-video", VideoResolution: "720p", VideoDurationSeconds: 6}
	require.NoError(t, h.gatewayService.StoreGrokVideoPendingBilling(context.Background(), "task", 10, 20, legacy))
	pending, err := h.loadNativeVideoPendingBilling(context.Background(), key, 10, 1, nil, "task", q.Protocol)
	require.NoError(t, err)
	require.NotNil(t, pending)
	require.Nil(t, pending.Financial)
	// If Redis ownership was lost, leftover parameters cannot establish price.
	bindings.owner = 0
	pending, err = h.loadNativeVideoPendingBilling(context.Background(), key, 10, 1, repo.recovery, "task", q.Protocol)
	require.ErrorIs(t, err, service.ErrNativeVideoBillingSnapshotMissing)
	require.Nil(t, pending)
}
