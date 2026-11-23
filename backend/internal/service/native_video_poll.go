package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	NativeVideoMonitorActive   = "active"
	NativeVideoMonitorComplete = "complete"
	NativeVideoMonitorReview   = "review"
	nativeVideoPollBodyLimit   = 64 << 10
)

type NativeVideoPollTask struct {
	Observation                NativeVideoObservation
	ConnectionHash, LeaseToken string
	Failures                   int
	ExpiresAt                  time.Time
	PendingBilling             *GrokVideoPendingBilling
	AwaitingUsage              bool
}

type NativeVideoPollUpdate struct {
	State                    *NativeVideoState
	ObservedAt               time.Time
	MonitorStatus, ErrorCode string
	Delay                    time.Duration
}

type NativeVideoPollRepository interface {
	ClaimNativeVideoPoll(context.Context, string, time.Duration) (*NativeVideoPollTask, error)
	SaveNativeVideoPoll(context.Context, *NativeVideoPollTask, NativeVideoPollUpdate) error
}

// The hash pins the original GET target, account type, proxy and header policy,
// without persisting a URL, key, proxy password or headers. The account's API
// key can rotate; other connection edits require human review instead of
// silently polling a different upstream. OAuth/CLIProxy are not enrolled.
func (s *OpenAIGatewayService) NativeVideoPollConnectionHash(a *Account, protocol, taskID string) (string, error) {
	target, proxy, err := s.nativeVideoPollTarget(a, protocol, taskID)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(struct {
		AccountID                               int64
		Protocol, Platform, Type, Target, Proxy string
		ProxyID                                 *int64
		Headers                                 map[string]string
	}{a.ID, protocol, a.Platform, a.Type, target, proxy, a.ProxyID, a.GetHeaderOverrides()})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (s *OpenAIGatewayService) nativeVideoPollTarget(a *Account, protocol, taskID string) (string, string, error) {
	if s == nil || a == nil || a.ID <= 0 || a.Type != AccountTypeAPIKey || a.HasNewAPIPlugin() {
		return "", "", errors.New("native video monitoring requires an unbound API key account")
	}
	var target string
	var err error
	switch protocol {
	case GenerationProtocolXAI:
		if a.Platform != PlatformGrok {
			return "", "", errors.New("native video account platform changed")
		}
		target, err = buildGrokMediaURL(a, s.cfg, GrokMediaEndpointVideoStatus, taskID)
	case GenerationProtocolArk:
		if !a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance) || !strings.HasPrefix(taskID, "seedance:") {
			return "", "", errors.New("native video account platform changed")
		}
		var base string
		base, err = s.validateUpstreamBaseURL(a.GetCredential("base_url"))
		if err == nil {
			target, err = buildSeedanceURL(base, SeedanceEndpointStatus, strings.TrimPrefix(taskID, "seedance:"))
		}
	default:
		return "", "", errors.New("unsupported native video monitoring protocol")
	}
	if err != nil {
		return "", "", errors.New("native video monitoring target invalid")
	}
	proxy := ""
	if a.ProxyID != nil {
		if a.Proxy == nil || a.Proxy.ID != *a.ProxyID {
			return "", "", errors.New("native video monitoring proxy unavailable")
		}
		proxy = a.Proxy.URL()
	}
	return target, proxy, nil
}

func (s *NativeVideoWorkers) workNativeVideos(ctx context.Context, repo NativeVideoPollRepository) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		task, err := repo.ClaimNativeVideoPoll(callCtx, uuid.NewString(), 2*time.Minute)
		if err == nil {
			err = s.processNativeVideoPoll(callCtx, repo, task)
		}
		cancel()
		if err != nil && !errors.Is(err, ErrGenerationTaskNotFound) && !errors.Is(err, context.Canceled) {
			// No upstream error/URL/credential is logged, including transport errors.
			slog.Warn("native_video_monitor", "code", "poll_persistence_failed")
		}
	}
}

func nativeVideoPollDone(status, delivery string) bool {
	return status == GenerationFailed || status == GenerationSucceeded && delivery != GenerationDeliveryPending
}

func (s *NativeVideoWorkers) processNativeVideoPoll(ctx context.Context, repo NativeVideoPollRepository, task *NativeVideoPollTask) error {
	if task == nil {
		return errors.New("native video poll missing")
	}
	u := NativeVideoPollUpdate{ObservedAt: time.Now().UTC(), MonitorStatus: NativeVideoMonitorActive, Delay: 15 * time.Second}
	if nativeVideoPollDone(task.Observation.Status, task.Observation.DeliveryStatus) && (!task.AwaitingUsage || task.Observation.Status == GenerationFailed) {
		u.MonitorStatus = NativeVideoMonitorComplete
	} else if task.AwaitingUsage && ValidateNativeVideoFunding(task.Observation, task.PendingBilling) != nil {
		u.MonitorStatus, u.ErrorCode = NativeVideoMonitorReview, "native_monitor_usage_pending"
	} else if !u.ObservedAt.Before(task.ExpiresAt) {
		u.MonitorStatus, u.ErrorCode = NativeVideoMonitorReview, "native_monitor_timeout"
	} else {
		// Leave part of the worker's 30-second budget for persistence even if
		// upstream I/O times out. Service shutdown still cancels both stages.
		fetchCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		state, code, permanent := s.fetchNativeVideoPoll(fetchCtx, task)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		} // leave the lease recoverable on shutdown
		u.ObservedAt, u.State, u.ErrorCode = time.Now().UTC(), state, code
		if permanent || code != "" && code != "native_monitor_capacity" && task.Failures >= 9 {
			u.MonitorStatus = NativeVideoMonitorReview
		} else if code != "" && code != "native_monitor_capacity" {
			u.Delay = 15 * time.Second * time.Duration(1<<min(task.Failures, 4))
		} else if state != nil && nativeVideoPollDone(state.Status, state.DeliveryStatus) && (!task.AwaitingUsage || state.FinalUsage != nil || state.Status == GenerationFailed) {
			u.MonitorStatus = NativeVideoMonitorComplete
		} else if state != nil && state.Status == GenerationSucceeded && task.AwaitingUsage && state.FinalUsage == nil {
			u.ErrorCode = "native_monitor_usage_pending"
			u.Delay = 15 * time.Second * time.Duration(1<<min(task.Failures, 4))
			if task.Failures >= 9 {
				u.MonitorStatus = NativeVideoMonitorReview
			}
		}
	}
	return repo.SaveNativeVideoPoll(ctx, task, u)
}

func (s *NativeVideoWorkers) fetchNativeVideoPoll(ctx context.Context, task *NativeVideoPollTask) (*NativeVideoState, string, bool) {
	a, err := s.accounts.GetByID(ctx, task.Observation.AccountID)
	if err != nil {
		return nil, "native_monitor_account_unavailable", false
	}
	if a == nil || a.ID != task.Observation.AccountID || !a.IsActive() {
		return nil, "native_monitor_account_unavailable", true
	}
	hash, err := s.gateway.NativeVideoPollConnectionHash(a, task.Observation.Protocol, task.Observation.UpstreamID)
	if err != nil || hash != task.ConnectionHash {
		return nil, "native_monitor_connection_changed", true
	}
	token := strings.TrimSpace(a.GetCredential("api_key"))
	if token == "" {
		return nil, "native_monitor_auth_failed", false
	}
	if s.gateway.concurrencyService == nil || s.gateway.httpUpstream == nil {
		return nil, "native_monitor_capacity", false
	}
	slot, err := s.gateway.concurrencyService.AcquireAccountSlot(ctx, a.ID, a.Concurrency)
	if err != nil || slot == nil || !slot.Acquired {
		return nil, "native_monitor_capacity", false
	}
	defer slot.ReleaseFunc()
	target, proxy, err := s.gateway.nativeVideoPollTarget(a, task.Observation.Protocol, task.Observation.UpstreamID)
	if err != nil {
		return nil, "native_monitor_connection_changed", true
	}
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(ctx), http.MethodGet, target, nil)
	if err != nil {
		return nil, "native_monitor_connection_changed", true
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	a.ApplyHeaderOverrides(req.Header)
	resp, err := s.gateway.httpUpstream.Do(req, proxy, a.ID, a.Concurrency)
	if err != nil {
		return nil, "native_monitor_upstream_error", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, "native_monitor_auth_failed", false
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, "native_monitor_upstream_error", false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, nativeVideoPollBodyLimit+1))
	if err != nil {
		return nil, "native_monitor_upstream_error", false
	}
	if len(body) > nativeVideoPollBodyLimit {
		return nil, "native_monitor_response_limit", false
	}
	endpoint, idPath, expectedID := GrokMediaEndpointVideoStatus, "request_id", task.Observation.UpstreamID
	if task.Observation.Protocol == GenerationProtocolArk {
		endpoint, idPath, expectedID = SeedanceEndpointStatus, "id", strings.TrimPrefix(expectedID, "seedance:")
	}
	if id := gjson.GetBytes(body, idPath); id.Exists() && (id.Type != gjson.String || id.String() != expectedID) {
		return nil, "native_monitor_unrecognized", false
	}
	state := nativeVideoResponseState(endpoint, body)
	if state == nil {
		return nil, "native_monitor_unrecognized", false
	}
	if task.AwaitingUsage && task.PendingBilling != nil && task.PendingBilling.Financial != nil && state.Status == GenerationSucceeded {
		var result *OpenAIForwardResult
		if task.Observation.Protocol == GenerationProtocolXAI {
			result = ExtractGrokVideoBillingFromStatusBody(body, task.PendingBilling, task.Observation.UpstreamID)
		} else if value := gjson.GetBytes(body, "usage.completion_tokens"); value.Type == gjson.Number && value.Int() > 0 && value.Float() == float64(value.Int()) {
			result = &OpenAIForwardResult{Usage: OpenAIUsage{OutputTokens: int(value.Int())}}
		}
		if usage := NativeVideoUsageObservation(result, time.Now().UTC()); usage != nil {
			state.FinalUsage, _ = usage.Normalize(task.PendingBilling)
		}
	}
	return state, "", false
}
