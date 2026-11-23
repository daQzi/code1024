package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// NativeVideoObservation contains only host-owned lifecycle facts. It never
// contains a credential, upstream response, signed URL or billing operation.
// PendingBilling is private create-time input for the existing native ledger.
// The native protocol retains ownership of execution and its existing ledger.
type NativeVideoObservation struct {
	UserID, APIKeyID, GroupID, AccountID int64
	UpstreamID, Model, Platform          string
	Protocol, Action, Status             string
	DeliveryStatus                       string
	Fields                               GenerationVideoFields
	CreatedAt, ObservedAt                time.Time
	Create                               bool
	PendingBilling                       *GrokVideoPendingBilling `json:"-"`
	PollConnectionHash                   string                   `json:"-"`
	FinalUsage                           *NativeVideoFinalUsage   `json:"-"`
}

type NativeVideoTaskRepository interface {
	ObserveNativeVideo(context.Context, NativeVideoObservation) error
}

var (
	ErrNativeVideoRecoveryAmbiguous      = errors.New("native video task ownership is ambiguous")
	ErrNativeVideoBillingSnapshotMissing = errors.New("native video create-time billing snapshot unavailable")
)

// NativeVideoLookup deliberately includes the effective group and protocol.
// A raw upstream ID alone is not evidence of ownership.
type NativeVideoLookup struct {
	UserID, APIKeyID, GroupID int64
	Protocol, UpstreamID      string
}

func (q NativeVideoLookup) Validate() error {
	if q.UserID <= 0 || q.APIKeyID <= 0 || q.GroupID <= 0 || q.UpstreamID == "" || len(q.UpstreamID) > 512 || strings.ContainsRune(q.UpstreamID, 0) {
		return errors.New("invalid native video recovery identity")
	}
	if q.Protocol != GenerationProtocolXAI && q.Protocol != GenerationProtocolArk {
		return errors.New("invalid native video recovery protocol")
	}
	return nil
}

type NativeVideoRecovery struct {
	AccountID      int64
	PendingBilling *GrokVideoPendingBilling
}

type NativeVideoRecoveryRepository interface {
	GetNativeVideoRecovery(context.Context, NativeVideoLookup) (*NativeVideoRecovery, error)
}

func (p GrokVideoPendingBilling) ValidateNativeVideoSnapshot() error {
	if p.Financial != nil {
		if err := p.Financial.Validate(); err != nil {
			return err
		}
		if p.Financial.TaskID == "" {
			return errors.New("native video financial task identity missing")
		}
	}
	for _, model := range []string{p.Model, p.BillingModel, p.UpstreamModel, p.OriginalModel} {
		if len(model) > 512 || strings.ContainsRune(model, 0) {
			return errors.New("invalid native video billing model snapshot")
		}
	}
	created, err := time.Parse(time.RFC3339Nano, p.CreatedAt)
	if err != nil || created.IsZero() || strings.TrimSpace(p.Model) == "" || len(p.VideoResolution) > 32 || strings.ContainsRune(p.VideoResolution, 0) || p.VideoDurationSeconds < 0 {
		return errors.New("invalid native video billing snapshot")
	}
	return nil
}

type NativeVideoState struct {
	Status, DeliveryStatus string
	FinalUsage             *NativeVideoFinalUsage `json:"-"`
}

type nativeVideoObserverKey struct{}

// WithNativeVideoObserver attempts synchronous history persistence before a
// native response is sent, so successful writes precede client polling.
// Failures remain the observer's concern;
// they must never cause an upstream create to be replayed.
func WithNativeVideoObserver(ctx context.Context, observer func(*OpenAIForwardResult)) context.Context {
	return context.WithValue(ctx, nativeVideoObserverKey{}, observer)
}

func observeNativeVideoResult(ctx context.Context, result *OpenAIForwardResult) {
	if observer, ok := ctx.Value(nativeVideoObserverKey{}).(func(*OpenAIForwardResult)); ok && observer != nil {
		observer(result)
	}
}

// The raw native status is consumed beside the protocol parser. Only these
// normalized facts travel to the handler; errors and signed URLs do not.
func nativeVideoResponseState(endpoint GrokMediaEndpoint, body []byte) *NativeVideoState {
	if endpoint != GrokMediaEndpointVideosGenerations && endpoint != GrokMediaEndpointVideosEdits && endpoint != GrokMediaEndpointVideosExtensions && endpoint != GrokMediaEndpointVideoStatus && endpoint != SeedanceEndpointCreate && endpoint != SeedanceEndpointStatus {
		return nil
	}
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil
	}
	status := gjson.GetBytes(body, "status").String()
	state := &NativeVideoState{DeliveryStatus: GenerationDeliveryPending}
	if endpoint.IsSeedance() {
		switch status {
		case "queued":
			state.Status = GenerationQueued
		case "running":
			state.Status = GenerationRunning
		case "succeeded":
			state.Status = GenerationSucceeded
			if strings.TrimSpace(gjson.GetBytes(body, "content.video_url").String()) != "" {
				state.DeliveryStatus = GenerationDeliveryReady
			}
		case "failed", "cancelled", "expired":
			state.Status, state.DeliveryStatus = GenerationFailed, "failed"
		}
	} else {
		switch status {
		case "pending":
			state.Status = GenerationRunning
		case "done":
			state.Status = GenerationSucceeded
			if gjson.GetBytes(body, "video.respect_moderation").Type == gjson.False {
				state.DeliveryStatus = "failed"
			} else if strings.TrimSpace(gjson.GetBytes(body, "video.url").String()) != "" {
				state.DeliveryStatus = GenerationDeliveryReady
			}
		case "failed", "expired":
			state.Status, state.DeliveryStatus = GenerationFailed, "failed"
		}
	}
	if state.Status == "" {
		return nil
	}
	return state
}

// NativeVideoTaskID isolates providers, accounts and owners even when upstreams
// reuse a task ID. It is a console identity, not a new downstream video ID.
func NativeVideoTaskID(o NativeVideoObservation) string {
	hash := sha256.Sum256([]byte(strings.Join([]string{
		strconv.FormatInt(o.UserID, 10), strconv.FormatInt(o.APIKeyID, 10), strconv.FormatInt(o.GroupID, 10), strconv.FormatInt(o.AccountID, 10), o.Protocol, o.UpstreamID,
	}, "\x00")))
	return "native_video_" + hex.EncodeToString(hash[:])
}

func (o NativeVideoObservation) Validate() error {
	if o.UserID <= 0 || o.APIKeyID <= 0 || o.GroupID <= 0 || o.AccountID <= 0 || o.UpstreamID == "" || len(o.UpstreamID) > 512 || strings.ContainsRune(o.UpstreamID, 0) || o.ObservedAt.IsZero() {
		return errors.New("invalid native video observation identity")
	}
	if o.Protocol != "xai_video" && o.Protocol != "ark_video" {
		return errors.New("unsupported native video protocol")
	}
	if o.Status != GenerationQueued && o.Status != GenerationRunning && o.Status != GenerationSucceeded && o.Status != GenerationFailed {
		return errors.New("invalid native video observation status")
	}
	if o.DeliveryStatus != GenerationDeliveryPending && o.DeliveryStatus != GenerationDeliveryReady && o.DeliveryStatus != "failed" {
		return errors.New("invalid native video delivery status")
	}
	if o.DeliveryStatus == GenerationDeliveryReady && o.Status != GenerationSucceeded {
		return errors.New("native video delivery requires completion")
	}
	if o.Create && (o.Model == "" || len(o.Model) > 100 || o.CreatedAt.IsZero() || o.CreatedAt.After(o.ObservedAt) || (o.Platform != PlatformGrok && o.Platform != PlatformOpenAI)) {
		return errors.New("invalid native video creation facts")
	}
	if o.Create && ((o.Protocol == "xai_video" && o.Platform != PlatformGrok) || (o.Protocol == "ark_video" && o.Platform != PlatformOpenAI)) {
		return errors.New("native video protocol does not match the account platform")
	}
	if o.Create && o.Action != "text_to_video" && o.Action != "image_to_video" && o.Action != "edit" && o.Action != "extend" && o.Action != "create" {
		return errors.New("invalid native video action")
	}
	if len(o.Fields.Prompt) > 8192 || len(o.Fields.Seconds) > 24 || len(o.Fields.Size) > 32 {
		return errors.New("native video public fields too large")
	}
	if strings.ContainsRune(o.Fields.Prompt+o.Fields.Seconds+o.Fields.Size, 0) {
		return errors.New("invalid native video public fields")
	}
	if o.PendingBilling != nil {
		if !o.Create {
			return errors.New("native video polls cannot change billing snapshots")
		}
		if err := o.PendingBilling.ValidateNativeVideoSnapshot(); err != nil {
			return err
		}
		if f := o.PendingBilling.Financial; f != nil && (f.UserID != o.UserID || f.APIKeyID != o.APIKeyID || f.GroupID != o.GroupID || f.AccountID != o.AccountID || f.Protocol != o.Protocol || f.TaskID != o.UpstreamID) {
			return errors.New("native video financial snapshot ownership mismatch")
		}
	}
	if o.PollConnectionHash != "" {
		decoded, err := hex.DecodeString(o.PollConnectionHash)
		if !o.Create || err != nil || len(decoded) != sha256.Size || o.PollConnectionHash != strings.ToLower(o.PollConnectionHash) {
			return errors.New("invalid native video poll connection identity")
		}
	}
	return nil
}

func (s *GenerationService) ObserveNativeVideo(ctx context.Context, observation NativeVideoObservation) error {
	if err := observation.Validate(); err != nil {
		return err
	}
	repo, ok := s.repo.(NativeVideoTaskRepository)
	if !ok {
		return errors.New("native video history repository unavailable")
	}
	return repo.ObserveNativeVideo(ctx, observation)
}

func (s *GenerationService) GetNativeVideoRecovery(ctx context.Context, lookup NativeVideoLookup) (*NativeVideoRecovery, error) {
	if err := lookup.Validate(); err != nil {
		return nil, err
	}
	repo, ok := s.repo.(NativeVideoRecoveryRepository)
	if !ok {
		return nil, errors.New("native video recovery repository unavailable")
	}
	return repo.GetNativeVideoRecovery(ctx, lookup)
}
