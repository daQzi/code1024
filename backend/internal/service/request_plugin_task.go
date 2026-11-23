package service

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/newapi/mediahost"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	contract "github.com/Wei-Shaw/sub2api/newapi-engine/contract"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
)

const MaxTaskRequestPluginSnapshot = 256 << 10
const MaxTaskRequestPluginEncrypted = generation.MaxRequestPolicyEncrypted

type requestPluginPlanKey struct{}
type requestPluginPlanContext struct{ plan *RequestPluginPlan }

// The wrapper also pins an empty plan: a policy added after ingress must not
// start applying to the same accepted request while it is being admitted.
func WithRequestPluginPlan(ctx context.Context, plan *RequestPluginPlan) context.Context {
	return context.WithValue(ctx, requestPluginPlanKey{}, requestPluginPlanContext{plan})
}

func (p *RequestPluginPlan) HasStage(stage string) bool {
	return p != nil && slices.ContainsFunc(p.items, func(i RequestPluginInstance) bool { return slices.Contains(i.Stages, stage) })
}

type taskRequestPluginSnapshot struct {
	Call  RequestPluginCall       `json:"call"`
	Items []taskRequestPluginItem `json:"items"`
}

type taskRequestPluginItem struct {
	ID             int64           `json:"id"`
	Name           string          `json:"name"`
	VersionID      int64           `json:"version_id"`
	SHA256         string          `json:"sha256"`
	Config         json.RawMessage `json:"config"`
	NetworkEnabled bool            `json:"network_enabled,omitempty"`
}

func (s *NewAPIPluginService) snapshotTaskRequestPlugins(ctx context.Context, key *APIKey, model string) (string, error) {
	pinned, found := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	if !found {
		return "", nil // Internal callers without a gateway policy plan.
	}
	plan := pinned.plan
	if !plan.HasStage("egress") {
		return "", nil
	}
	if plan.call.UserID != key.UserID || plan.call.TokenID != key.ID || plan.call.GroupID != key.Group.ID || plan.call.Model != model || plan.call.Format != "task" {
		return "", errors.New("task policy identity does not match admission")
	}
	snapshot := taskRequestPluginSnapshot{Call: plan.call}
	snapshot.Call.ModelCandidates = nil // Only admission matching needs these.
	encodedBudget := 0
	for _, instance := range plan.items {
		if !slices.Contains(instance.Stages, "egress") || !requestPluginMatchesChannelType(instance, 61) {
			continue
		}
		encodedBudget += len(instance.Config) + len(instance.Name) + 256
		if encodedBudget > MaxTaskRequestPluginSnapshot {
			return "", errors.New("task policy snapshot exceeds 256 KiB")
		}
		digest := sha256.Sum256([]byte(instance.Version.Source))
		snapshot.Items = append(snapshot.Items, taskRequestPluginItem{ID: instance.ID, Name: instance.Name, VersionID: instance.PluginVersionID, SHA256: hex.EncodeToString(digest[:]), Config: instance.Config, NetworkEnabled: instance.NetworkEnabled})
	}
	if len(snapshot.Items) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) > MaxTaskRequestPluginSnapshot || s.encryptor == nil {
		return "", errors.New("task policy snapshot is unavailable or exceeds 256 KiB")
	}
	sealed, err := s.encryptor.Encrypt(string(raw))
	if err != nil || len(sealed) > MaxTaskRequestPluginEncrypted {
		return "", errors.New("task policy snapshot encryption failed")
	}
	return sealed, nil
}

// A channel-scoped Task ingress is selected only after the actual account is
// known, before decoding, estimating usage or freezing funds. Accepted
// idempotent requests are replayed before this method is reached.
func (s *NewAPIPluginService) taskRequestPluginLateIngress(ctx context.Context, key *APIKey, account *Account, model string, body json.RawMessage) (json.RawMessage, error) {
	pinned, _ := ctx.Value(requestPluginPlanKey{}).(requestPluginPlanContext)
	plan := pinned.plan
	if !plan.hasLateIngressFor(account.ID, 61) {
		return body, nil
	}
	if key == nil || key.Group == nil || plan.call.UserID != key.UserID || plan.call.TokenID != key.ID || plan.call.GroupID != key.Group.ID || plan.call.Format != "task" || plan.call.Model != model {
		return nil, &RequestPluginFailure{Class: "identity"}
	}
	ctx = context.WithValue(ctx, requestPluginProviderBodyKey{}, []byte(body))
	form, _ := ctx.Value(generationMultipartBodyKey{}).(bool)
	out, err := s.relayRequestPluginStages(ctx, account, plan.call.Path, account.GetMappedModel(model), body, contract.AdvancedCustomRoute{}, requestPluginStageRun{
		provider: true, stages: []string{"ingress"}, format: "task", unpriced: true, videoForm: form,
	})
	if err != nil {
		return nil, err
	}
	if len(plan.records)+len(out.Records) > 128 {
		return nil, &RequestPluginFailure{Class: "limit"}
	}
	plan.records = append(plan.records, out.Records...)
	return out.Body, nil
}

// Task egress runs on the JS-built upstream JSON, after the hold and before
// submit HTTP. It is read-only. Multipart is explicitly unchecked, matching
// the fixed New API baseline. No Task request/outbound/reply is synthesized.
func (s *NewAPIPluginService) taskRequestPluginEgress(ctx context.Context, task *GenerationTask, body json.RawMessage, multipart bool) error {
	return s.taskRequestPluginEgressWithParts(ctx, task, body, multipart, nil)
}

// taskRequestPluginEgressDescriptor inspects JSON bodies and multipart task
// descriptors (including text fields and persisted fileRef attachments) in the
// read-only egress stage. Raw opaque text descriptors remain explicitly
// unchecked.
func (s *NewAPIPluginService) taskRequestPluginEgressDescriptor(ctx context.Context, task *GenerationTask, descriptor pluginTask.Request) error {
	if descriptor.BodyType == "multipart" && len(descriptor.Parts) > 0 {
		return s.taskRequestPluginEgressWithParts(ctx, task, nil, false, descriptor.Parts)
	}
	return s.taskRequestPluginEgress(ctx, task, descriptor.Body, descriptor.OpaqueBody())
}

func (s *NewAPIPluginService) taskRequestPluginEgressWithParts(ctx context.Context, task *GenerationTask, body json.RawMessage, multipart bool, parts []pluginTask.Part) error {
	if task.RequestPluginsEncrypted == "" {
		return nil // An older task must not acquire newly configured policy.
	}
	if len(task.RequestPluginsEncrypted) > MaxTaskRequestPluginEncrypted {
		return &RequestPluginFailure{Class: "snapshot"}
	}
	raw, err := s.decryptRequestPluginConfig(task.RequestPluginsEncrypted)
	var snapshot taskRequestPluginSnapshot
	if err != nil || len(raw) > MaxTaskRequestPluginSnapshot || json.Unmarshal(raw, &snapshot) != nil || len(snapshot.Items) == 0 || len(snapshot.Items) > 64 || snapshot.Call.UserID != task.UserID || snapshot.Call.TokenID != task.APIKeyID || snapshot.Call.GroupID != task.GroupID || snapshot.Call.Model != task.Model || snapshot.Call.Format != "task" {
		return &RequestPluginFailure{Class: "snapshot"}
	}
	if snapshot.Call.RequestID == "" {
		snapshot.Call.RequestID = task.ID
	}
	out, failure := s.runTaskRequestPluginEgress(ctx, task, snapshot, body, multipart, parts)
	// Audit must succeed before any submit, even after a rule rejects.
	auditCtx, auditCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	err = s.RecordRequestPluginRun(auditCtx, snapshot.Call, out, false)
	auditCancel()
	if err != nil {
		return &RequestPluginFailure{Class: "audit"}
	}
	s.appendTaskReportRecords(task, out.Records)
	if err = ctx.Err(); err != nil {
		return &RequestPluginFailure{Class: "canceled"}
	}
	return failure
}

func projectTaskMultipartEgress(task *GenerationTask, session *newAPITaskSession, parts []pluginTask.Part) (json.RawMessage, []mediahost.MultipartFile, error) {
	fields := make(map[string]any, len(parts))
	files := make([]mediahost.MultipartFile, 0, len(parts))
	for _, part := range parts {
		if part.FileRef == "" {
			var val any
			if len(part.Value) > 0 {
				if err := json.Unmarshal(part.Value, &val); err != nil {
					val = string(part.Value)
				}
			} else {
				val = ""
			}
			if existing, exists := fields[part.Name]; exists {
				if list, ok := existing.([]any); ok {
					fields[part.Name] = append(list, val)
				} else {
					fields[part.Name] = []any{existing, val}
				}
			} else {
				fields[part.Name] = val
			}
			continue
		}
		var matched *GenerationFileSnapshot
		for i := range task.Files {
			if task.Files[i].Ref == part.FileRef {
				matched = &task.Files[i]
				break
			}
		}
		if matched == nil {
			return nil, nil, ErrGenerationFileNotFound
		}
		var digest [32]byte
		if rawDigest, err := hex.DecodeString(matched.SHA256); err == nil && len(rawDigest) == len(digest) {
			copy(digest[:], rawDigest)
		}
		ref := part.FileRef
		files = append(files, mediahost.MultipartFile{
			Field:    part.Name,
			Filename: cmp.Or(part.Filename, matched.Filename),
			MIME:     matched.MimeType,
			Size:     int(matched.Size),
			SHA256:   digest,
			Open: func(openCtx context.Context) (io.ReadCloser, error) {
				_, reader, err := session.openFile(openCtx, ref)
				return reader, err
			},
		})
	}
	projected, err := json.Marshal(fields)
	if err != nil || len(projected) > MaxRequestPluginBody {
		return nil, nil, errors.New("projected multipart task body exceeds limit")
	}
	return projected, files, nil
}

func (s *NewAPIPluginService) runTaskRequestPluginEgress(ctx context.Context, task *GenerationTask, snapshot taskRequestPluginSnapshot, body json.RawMessage, multipart bool, parts []pluginTask.Part) (RequestPluginOutcome, error) {
	mediaText := &mediahost.TextCache{}
	parentCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out := RequestPluginOutcome{Body: body, Records: []RequestPluginRecord{}}
	var failure error
	for _, item := range snapshot.Items {
		started := time.Now()
		record := RequestPluginRecord{InstanceID: item.ID, VersionID: item.VersionID, Stage: "egress", Action: "continue"}
		if multipart {
			record.Action = "unchecked"
			out.Records = append(out.Records, record)
			continue
		}
		version, runErr := s.repo.Get(ctx, item.VersionID)
		trustedVersion := false
		if runErr == nil {
			digest := sha256.Sum256([]byte(version.Source))
			trustedVersion = hex.EncodeToString(digest[:]) == item.SHA256 && slices.Contains(version.Capabilities.Hooks, "egress")
			if !trustedVersion || len(parts) == 0 && (len(body) > MaxRequestPluginBody || len(body) > 0 && !json.Valid(body)) {
				runErr = errors.New("invalid task egress snapshot or JSON body")
			}
		}
		var result jsplugin.TransformResult
		var media *mediahost.Source
		if runErr == nil {
			hookCtx, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
			// The JSON wire codec allows at most 1 MiB after file expansion.
			// Reserve its parse/base64/projection copies before storage reads.
			if err := pkghttputil.ReserveRequestBodyMemory(hookCtx, 16<<20); err != nil {
				runErr, record.ErrorClass = err, "capacity"
			} else if len(parts) > 0 {
				session := &newAPITaskSession{service: s, task: task}
				var projected json.RawMessage
				var mpFiles []mediahost.MultipartFile
				projected, mpFiles, runErr = projectTaskMultipartEgress(task, session, parts)
				if runErr == nil {
					media, runErr = mediahost.NewWithMultipartFiles(projected, mediaText, mpFiles)
				}
			} else if len(body) != 0 {
				session := &newAPITaskSession{service: s, task: task}
				media, runErr = mediahost.NewWithFileReferences(hookCtx, body, mediaText, session.openFile)
			}
			if runErr == nil {
				runErr = s.pool.With(hookCtx, version.Source, func(client *runner.Client) error {
					request := jsplugin.TransformRequest{Body: body}
					if media != nil {
						request.Body, request.Media = media.View(), media.Projection()
					}
					values, err := s.callRequestPluginTransform(hookCtx, client, item.NetworkEnabled, jsplugin.TransformContext{
						Stage: "egress", Format: "task", Path: snapshot.Call.Path, Model: task.Model, UpstreamModel: task.UpstreamModel,
						Group: snapshot.Call.Group, Channel: &jsplugin.TransformChannel{ID: int(task.AccountID), Type: 61}, Attempt: 0,
						User: jsplugin.TransformUser{ID: int(task.UserID)}, Token: &jsplugin.TransformToken{ID: int(task.APIKeyID), Name: snapshot.Call.TokenName},
						Instance: jsplugin.TransformInstance{ID: strconv.FormatInt(item.ID, 10), Name: item.Name}, Config: item.Config, Media: media,
					}, request)
					if errors.Is(err, pkghttputil.ErrRequestBodyMemoryBudget) || errors.Is(err, mediahost.ErrMemoryCapacity) {
						record.ErrorClass = "capacity"
					}
					if err != nil || len(values) != 1 {
						return errors.New("task egress execution failed")
					}
					return json.Unmarshal(values[0], &result)
				})
			}
			release()
		}
		if media != nil {
			record.Attachments, record.MoreAttachments = media.Records()
		}
		record.Findings = len(result.Findings)
		if runErr == nil && result.Action == "reject" {
			record.Action, out.Rejected = "reject", true
			failure = &RequestPluginFailure{Rejected: true, Class: "rejected"}
		} else if runErr == nil && len(result.Effects) > 0 {
			var effects []json.RawMessage
			if json.Unmarshal(result.Effects, &effects) != nil || len(effects) > 0 {
				runErr = errors.New("egress effects are forbidden")
				record.ErrorClass = "invalid_effect"
			}
		}
		if runErr != nil {
			record.Action = "error"
			if record.ErrorClass == "" {
				record.ErrorClass = "execution"
			}
			if trustedVersion && version.Meta.Optional && parentCtx.Err() == nil {
				record.Action = "skipped"
			} else {
				failure = &RequestPluginFailure{Class: record.ErrorClass}
			}
		}
		if parentCtx.Err() != nil {
			failure = &RequestPluginFailure{Class: "canceled"}
		}
		record.DurationMS = time.Since(started).Milliseconds()
		out.Records = append(out.Records, record)
		if failure != nil {
			break
		}
	}
	return out, failure
}
