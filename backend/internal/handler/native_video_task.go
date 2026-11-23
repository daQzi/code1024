package handler

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

// nativeVideoObservation bridges legacy lifecycle facts into the console. The
// account selected by the native handler remains authoritative; polls cannot
// create history for a task whose original submission was never observed.
func nativeVideoObservation(key *service.APIKey, account *service.Account, endpoint service.GrokMediaEndpoint, taskID, model string, info service.GrokMediaRequestInfo, body []byte, result *service.OpenAIForwardResult, started, observed time.Time) *service.NativeVideoObservation {
	if key == nil || key.Group == nil || account == nil || result == nil {
		return nil
	}
	create := isGrokVideoCreateEndpoint(endpoint)
	if !create && endpoint != service.GrokMediaEndpointVideoStatus && endpoint != service.GrokMediaEndpointVideoContent && endpoint != service.SeedanceEndpointStatus {
		return nil
	}
	if create {
		taskID = result.ResponseID
	}
	if strings.TrimSpace(taskID) == "" || !create && result.NativeVideo == nil {
		return nil
	}
	o := &service.NativeVideoObservation{UserID: key.UserID, APIKeyID: key.ID, GroupID: key.Group.ID, AccountID: account.ID,
		UpstreamID: strings.TrimSpace(taskID), Model: model, Platform: account.Platform,
		Protocol: "xai_video", Action: "text_to_video", Status: service.GenerationQueued, DeliveryStatus: service.GenerationDeliveryPending,
		CreatedAt: started, ObservedAt: observed, Create: create}
	if endpoint.IsSeedance() {
		o.Protocol, o.Action = "ark_video", "create"
	} else if endpoint == service.GrokMediaEndpointVideosEdits {
		o.Action = "edit"
	} else if endpoint == service.GrokMediaEndpointVideosExtensions {
		o.Action = "extend"
	} else if info.HasInputImage() {
		o.Action = "image_to_video"
	}
	if result.NativeVideo != nil {
		o.Status, o.DeliveryStatus = result.NativeVideo.Status, result.NativeVideo.DeliveryStatus
		if o.Status == service.GenerationSucceeded {
			o.FinalUsage = service.NativeVideoUsageObservation(result, observed)
		}
	}
	if create {
		if len(info.Prompt) <= 8192 {
			o.Fields.Prompt = info.Prompt
		}
		if len(info.Size) <= 32 {
			o.Fields.Size = info.Size
		}
		if info.DurationSeconds > 0 {
			o.Fields.Seconds = strconv.Itoa(info.DurationSeconds)
		} else if duration := gjson.GetBytes(body, "duration"); duration.Type == gjson.Number && duration.Num > 0 && len(duration.Raw) <= 24 {
			o.Fields.Seconds = duration.Raw
		}
	}
	return o
}

func (h *OpenAIGatewayHandler) recordNativeVideoObservation(ctx context.Context, o *service.NativeVideoObservation) error {
	if h.generations == nil || o == nil {
		return nil
	}
	// Native forwarding may already have sent a response. Persist for at most
	// three seconds independently of that client's cancellation; do not retry the
	// upstream or change its already accepted financial behavior on log failure.
	historyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	return h.generations.ObserveNativeVideo(historyCtx, *o)
}

func nativeVideoPendingBilling(model, originalModel, createdAt string, result *service.OpenAIForwardResult) service.GrokVideoPendingBilling {
	return service.NormalizeGrokVideoPendingBilling(service.GrokVideoPendingBilling{
		Model: model, BillingModel: firstNonEmptyString(result.BillingModel, model), UpstreamModel: result.UpstreamModel,
		VideoResolution: result.VideoResolution, VideoDurationSeconds: result.VideoDurationSeconds,
		OriginalModel: originalModel, CreatedAt: createdAt,
	})
}

func nativeVideoFinancialForTask(p *service.NativeVideoBillingSnapshot, taskID string) *service.NativeVideoBillingSnapshot {
	if p == nil {
		return nil
	}
	copy := *p
	copy.TaskID = taskID
	return &copy
}

func nativeVideoLookup(key *service.APIKey, userID int64, taskID, protocol string) (service.NativeVideoLookup, error) {
	if key == nil || key.UserID != userID || key.GroupID == nil {
		return service.NativeVideoLookup{}, errors.New("invalid native video task owner")
	}
	q := service.NativeVideoLookup{UserID: userID, APIKeyID: key.ID, GroupID: *key.GroupID, Protocol: protocol, UpstreamID: strings.TrimSpace(taskID)}
	return q, q.Validate()
}

func (h *OpenAIGatewayHandler) recoverNativeVideo(ctx context.Context, q service.NativeVideoLookup) (*service.NativeVideoRecovery, error) {
	if h.generations == nil {
		return nil, service.ErrGenerationTaskNotFound
	}
	recoveryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return h.generations.GetNativeVideoRecovery(recoveryCtx, q)
}

func (h *OpenAIGatewayHandler) resolveNativeVideoRequestAccount(ctx context.Context, key *service.APIKey, userID int64, endpoint service.GrokMediaEndpoint, taskID string) (int64, *service.NativeVideoRecovery, error) {
	protocol := service.GenerationProtocolXAI
	if endpoint.IsSeedance() {
		protocol = service.GenerationProtocolArk
	}
	q, err := nativeVideoLookup(key, userID, taskID, protocol)
	if err != nil {
		return 0, nil, err
	}
	accountID, cacheErr := h.gatewayService.ResolveGrokMediaVideoRequestAccount(ctx, key.GroupID, taskID, userID, key.ID)
	if cacheErr == nil && accountID > 0 {
		return accountID, nil, nil
	}
	recovered, err := h.recoverNativeVideo(ctx, q)
	if err != nil {
		return 0, nil, errors.Join(cacheErr, err)
	}
	// Do not refresh Redis or use generic sticky selection. Recovery remains
	// scoped to the recorded owner; unavailable accounts cannot escape elsewhere.
	return recovered.AccountID, recovered, nil
}

func (h *OpenAIGatewayHandler) loadNativeVideoPendingBilling(ctx context.Context, key *service.APIKey, userID, accountID int64, recovered *service.NativeVideoRecovery, taskID, protocol string) (*service.GrokVideoPendingBilling, error) {
	q, err := nativeVideoLookup(key, userID, taskID, protocol)
	if err != nil || accountID <= 0 {
		return nil, errors.New("invalid native video billing owner")
	}
	// When ownership came from SQL, use that same record's snapshot. The old
	// Redis pending key lacks group/account scope and may survive a partial
	// eviction or an API key's group change. It cannot override recovered facts.
	if recovered == nil {
		pending, cacheErr := h.gatewayService.LoadGrokVideoPendingBilling(ctx, taskID, userID, key.ID)
		if cacheErr == nil && pending != nil && pending.Financial != nil {
			if _, err := checkedNativeVideoPending(pending, q, accountID); err != nil {
				return nil, err
			}
		}
		// SQL preserves the first accepted price even when an upstream repeats
		// a task ID and a later create rewrites its Redis cache entry.
		recovered, err = h.recoverNativeVideo(ctx, q)
		if errors.Is(err, service.ErrGenerationTaskNotFound) {
			// Preserve the pre-upgrade path when no durable evidence exists. A cache
			// error still fails closed instead of pricing a corrupt/missing snapshot.
			return pending, cacheErr
		}
		if err != nil {
			return nil, errors.Join(cacheErr, err)
		}
		// Pre-snapshot history can coexist with still-valid legacy Redis input.
		// Only retain that path when Redis itself proved account ownership;
		// SQL-recovered owners must never adopt leftover unscoped cache input.
		if recovered.AccountID == accountID && recovered.PendingBilling == nil && cacheErr == nil && pending != nil {
			return checkedNativeVideoPending(pending, q, accountID)
		}
	}
	if recovered.AccountID != accountID {
		return nil, errors.New("native video billing account does not match task owner")
	}
	if recovered.PendingBilling == nil {
		return nil, service.ErrNativeVideoBillingSnapshotMissing
	}
	copy := *recovered.PendingBilling
	return checkedNativeVideoPending(&copy, q, accountID)
}

func checkedNativeVideoPending(p *service.GrokVideoPendingBilling, q service.NativeVideoLookup, accountID int64) (*service.GrokVideoPendingBilling, error) {
	if err := p.ValidateNativeVideoSnapshot(); err != nil {
		return nil, err
	}
	if f := p.Financial; f != nil && (f.UserID != q.UserID || f.APIKeyID != q.APIKeyID || f.GroupID != q.GroupID || f.AccountID != accountID || f.Protocol != q.Protocol || f.TaskID != q.UpstreamID) {
		return nil, errors.New("native video financial snapshot ownership mismatch")
	}
	return p, nil
}
