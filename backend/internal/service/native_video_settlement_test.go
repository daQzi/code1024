//go:build unit

package service

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func nativeSettlementFixture(t *testing.T, protocol string) (*NativeVideoWorkers, *NativeVideoSettlement, *openAIRecordUsageBillingRepoStub) {
	t.Helper()
	gateway, key, account, billing, _ := nativePriceFixture(t, protocol)
	p := captureNativePrice(t, gateway, key, account, nil, protocol)
	now := time.Now().UTC()
	pending := &GrokVideoPendingBilling{Model: p.Model, VideoDurationSeconds: 8, CreatedAt: p.PricingAt.Format(time.RFC3339Nano), Financial: p}
	o := NativeVideoObservation{UserID: key.UserID, APIKeyID: key.ID, GroupID: *key.GroupID, AccountID: account.ID,
		Protocol: protocol, UpstreamID: p.TaskID, CreatedAt: p.PricingAt, ObservedAt: now, Status: GenerationSucceeded, DeliveryStatus: GenerationDeliveryReady}
	u := NativeVideoUsageObservation(nativePriceResult(p), now)
	return &NativeVideoWorkers{gateway: gateway}, &NativeVideoSettlement{Observation: o, Pending: pending, Usage: u, LeaseToken: "fixture"}, billing
}

type nativeSettlementRepo struct {
	GenerationTaskRepository
	claim        func(context.Context) (*NativeVideoSettlement, error)
	status, code string
	delay        time.Duration
	saves        int
}

func (r *nativeSettlementRepo) Claim(context.Context, string, time.Duration) (*GenerationTask, error) {
	return nil, ErrGenerationTaskNotFound
}
func (r *nativeSettlementRepo) ClaimNativeVideoSettlement(ctx context.Context, _ string, _ time.Duration) (*NativeVideoSettlement, error) {
	return r.claim(ctx)
}
func (r *nativeSettlementRepo) SaveNativeVideoSettlement(_ context.Context, _ *NativeVideoSettlement, status, code string, delay time.Duration) error {
	r.status, r.code, r.delay = status, code, delay
	r.saves++
	return nil
}

func TestNativeVideoSettlementFinalUsageNormalization(t *testing.T) {
	for _, protocol := range []string{GenerationProtocolXAI, GenerationProtocolArk} {
		for _, scenario := range []string{"actual", "missing", "wrong unit", "negative", "overflow", "missing time", "invalid snapshot"} {
			t.Run(protocol+"/"+scenario, func(t *testing.T) {
				_, task, _ := nativeSettlementFixture(t, protocol)
				u := *task.Usage
				switch scenario {
				case "missing":
					u.VideoCount, u.OutputTokens = 0, 0
				case "wrong unit":
					u.VideoCount, u.OutputTokens = 1, 1
				case "negative":
					u.DurationSeconds, u.OutputTokens = -1, -1
				case "overflow":
					u.OutputTokens = math.MaxInt32 + 1
				case "missing time":
					u.ObservedAt = time.Time{}
				case "invalid snapshot":
					task.Pending.CreatedAt = "broken"
				}
				normalized, err := u.Normalize(task.Pending)
				if scenario != "actual" {
					require.Error(t, err)
					require.Nil(t, normalized)
					return
				}
				require.NoError(t, err)
				require.Equal(t, u, *normalized)
				if protocol == GenerationProtocolXAI {
					u.DurationSeconds = 0
					normalized, err = u.Normalize(task.Pending)
					require.NoError(t, err)
					require.Equal(t, 8, normalized.DurationSeconds)
					task.Pending.VideoDurationSeconds = 0
					normalized, err = u.Normalize(task.Pending)
					require.NoError(t, err)
					require.Equal(t, 8, normalized.DurationSeconds)
				}
			})
		}
	}
	require.Nil(t, NativeVideoUsageObservation(nil, time.Now()))
	require.Nil(t, NativeVideoUsageObservation(&OpenAIForwardResult{}, time.Now()))
}

func TestNativeVideoSettlementChecksOwnershipBeforeBilling(t *testing.T) {
	for _, scenario := range []string{"success", "user", "key", "account", "group", "task", "protocol", "missing usage", "missing snapshot", "failed", "invalid usage"} {
		t.Run(scenario, func(t *testing.T) {
			svc, task, billing := nativeSettlementFixture(t, GenerationProtocolXAI)
			switch scenario {
			case "user":
				task.Observation.UserID++
			case "key":
				task.Observation.APIKeyID++
			case "account":
				task.Observation.AccountID++
			case "group":
				task.Observation.GroupID++
			case "task":
				task.Observation.UpstreamID = "other"
			case "protocol":
				task.Observation.Protocol = GenerationProtocolArk
			case "missing usage":
				task.Usage = nil
			case "missing snapshot":
				task.Pending = nil
			case "failed":
				task.Observation.Status, task.Observation.DeliveryStatus = GenerationFailed, "failed"
			case "invalid usage":
				task.Usage.OutputTokens = 1
			}
			repo := &nativeSettlementRepo{}
			require.NoError(t, svc.processNativeVideoSettlement(context.Background(), repo, task))
			if scenario == "success" {
				require.Equal(t, "settled", repo.status)
				require.Equal(t, 1, billing.calls)
				require.InDelta(t, .592, billing.lastCmd.BalanceCost, 1e-12)
			} else {
				require.Equal(t, "review", repo.status)
				require.Equal(t, "native_billing_evidence_invalid", repo.code)
				require.Zero(t, billing.calls)
			}
		})
	}
}

func TestNativeVideoSettlementRetriesAndShutdown(t *testing.T) {
	for _, scenario := range []string{"retry", "last retry", "conflict", "shutdown", "log_only", "rate_only"} {
		t.Run(scenario, func(t *testing.T) {
			svc, task, billing := nativeSettlementFixture(t, GenerationProtocolArk)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "retry":
				billing.err = errors.New("temporary database failure")
			case "last retry":
				billing.err, task.Failures = errors.New("temporary database failure"), 9
			case "conflict":
				billing.err = ErrUsageBillingRequestConflict
			case "shutdown":
				cancel()
			case "log_only", "rate_only":
				task.Pending.Financial.Mode = scenario
			}
			repo := &nativeSettlementRepo{}
			err := svc.processNativeVideoSettlement(ctx, repo, task)
			if scenario == "shutdown" {
				require.ErrorIs(t, err, context.Canceled)
				require.Zero(t, repo.saves, "leave durable lease recoverable")
				return
			}
			require.NoError(t, err)
			switch scenario {
			case "retry":
				require.Equal(t, "ready", repo.status)
				require.Equal(t, "native_billing_retrying", repo.code)
			case "last retry", "conflict":
				require.Equal(t, "review", repo.status)
			case "log_only", "rate_only":
				require.Equal(t, "logged", repo.status)
				require.Zero(t, billing.lastCmd.BalanceCost)
				require.Zero(t, billing.lastCmd.SubscriptionCost)
			}
			require.LessOrEqual(t, repo.delay, 4*time.Minute)
		})
	}
}

func TestNativeVideoSettlementWorkersBoundedAndStop(t *testing.T) {
	var active, peak atomic.Int32
	repo := &nativeSettlementRepo{claim: func(ctx context.Context) (*NativeVideoSettlement, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for previous := peak.Load(); n > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, n) {
				break
			}
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	svc := NewNativeVideoWorkers(nil, repo, nil, &OpenAIGatewayService{}, nil)
	t.Cleanup(svc.Stop)
	require.Eventually(t, func() bool { return active.Load() == 2 }, 3*time.Second, 10*time.Millisecond)
	svc.Stop()
	svc.Stop()
	require.EqualValues(t, 2, peak.Load())
	require.Zero(t, active.Load())
}

func TestNativeVideoSettlementPollWaitsForActualUnits(t *testing.T) {
	for _, scenario := range []string{"actual", "missing", "zero", "fractional", "negative", "overflow", "last retry", "invalid funding", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			svc, repo, _, transport, _ := nativePollFixture(t, GenerationProtocolArk)
			_, fixture, _ := nativeSettlementFixture(t, GenerationProtocolArk)
			fixture.Pending.Financial.TaskID = repo.task.Observation.UpstreamID
			repo.task.PendingBilling, repo.task.AwaitingUsage = fixture.Pending, true
			// Delivery already succeeded in a previous poll. Still query for the
			// later final billing evidence rather than stopping at the video URL.
			repo.task.Observation.Status, repo.task.Observation.DeliveryStatus = GenerationSucceeded, GenerationDeliveryReady
			usage := `,"usage":{"completion_tokens":100000}`
			switch scenario {
			case "missing", "last retry":
				usage = ""
			case "zero":
				usage = `,"usage":{"completion_tokens":0}`
			case "fractional":
				usage = `,"usage":{"completion_tokens":1.5}`
			case "negative":
				usage = `,"usage":{"completion_tokens":-1}`
			case "overflow":
				usage = `,"usage":{"completion_tokens":2147483648}`
			case "invalid funding":
				repo.task.PendingBilling.Financial.APIKeyID++
			}
			if scenario == "last retry" {
				repo.task.Failures = 9
			}
			body := `{"id":"native-task","status":"succeeded","content":{"video_url":"https://media.example/video"}` + usage + `}`
			if scenario == "failed" {
				body = `{"id":"native-task","status":"failed"}`
			}
			transport.do = func(*http.Request) (*http.Response, error) { return grokMediaContentStatusResponse(body), nil }
			require.NoError(t, svc.processNativeVideoPoll(context.Background(), repo, repo.task))
			u := <-repo.saved
			switch scenario {
			case "actual":
				require.Equal(t, NativeVideoMonitorComplete, u.MonitorStatus)
				require.Equal(t, 100_000, u.State.FinalUsage.OutputTokens)
			case "failed":
				require.Equal(t, NativeVideoMonitorComplete, u.MonitorStatus)
				require.Nil(t, u.State.FinalUsage)
			case "invalid funding":
				require.Equal(t, NativeVideoMonitorReview, u.MonitorStatus)
				require.Zero(t, transport.calls.Load())
			default:
				status := NativeVideoMonitorActive
				if scenario == "last retry" {
					status = NativeVideoMonitorReview
				}
				require.Equal(t, status, u.MonitorStatus)
				require.Equal(t, "native_monitor_usage_pending", u.ErrorCode)
				require.Nil(t, u.State.FinalUsage)
			}
		})
	}
}
