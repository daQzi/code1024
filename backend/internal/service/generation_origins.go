package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
)

var ErrGenerationOrigin = infraerrors.New(400, "INVALID_ORIGIN_TASK", "The origin tasks are unavailable, belong to a different connection, or changed during admission.").WithCause(generation.ErrOrigin)
var ErrGenerationDecode = infraerrors.New(400, "INVALID_TASK_INTENT", "The task request cannot be decoded consistently by its available plugins.")

type GenerationOriginTask = generation.OriginTask

func ValidateGenerationOrigins(origins []GenerationOriginTask) error {
	if err := generation.ValidateOrigins(origins); err != nil {
		return ErrGenerationOrigin
	}
	return nil
}

// This projection deliberately excludes API keys, account configuration and
// grants. Historical versions must participate even after their active version
// or model alias changes, so an old parent cannot silently become a new task.
type GenerationVideoDecodeCandidate struct {
	VersionID     int64
	Mapping       map[string]any
	UpstreamModel string
}

func (c GenerationVideoDecodeCandidate) supportsModels(models []string, model, upstream string) bool {
	return slices.ContainsFunc(models, func(pattern string) bool {
		if pattern == "*" {
			// Generic drivers cannot claim unrelated models during pre-routing,
			// including historical candidates with no explicit account mapping.
			target, explicit := c.Mapping[model].(string)
			return explicit && pluginVideoModelMatches(pattern, model) && pluginVideoModelMatches(pattern, target)
		}
		return pluginVideoModelMatches(pattern, model) || pluginVideoModelMatches(pattern, upstream)
	})
}

type GenerationVideoRoutingRepository interface {
	ListVideoDecodeCandidates(context.Context, int64, int64, int64, string, string) ([]GenerationVideoDecodeCandidate, error)
	GetVideoOrigin(context.Context, string, int64, int64, int64) (*GenerationTask, error)
}

type generationVideoPlanKey struct{}
type generationVideoCandidatesKey struct{}
type generationVideoCandidates struct {
	model, protocol string
	candidates      []GenerationVideoDecodeCandidate
}
type generationVideoDecoderIdentity struct {
	version int64
	model   string
}
type generationVideoPlan struct {
	model, protocol string
	allowed         map[generationVideoDecoderIdentity]bool
	origins         []GenerationOriginTask
	accountID       int64
	versionID       int64
	connection      GenerationConnection
	configEncrypted string
	credential      GenerationCredentialBinding
}

func generationAccountConnection(account *Account) GenerationConnection {
	kind := account.GetCredential("newapi_upstream_kind")
	if kind == "" {
		kind = "vendor"
	}
	return GenerationConnection{BaseURL: account.GetCredential("base_url"), Kind: kind}
}

func GenerationVideoRoutingPrepared(ctx context.Context) bool {
	plan, _ := ctx.Value(generationVideoPlanKey{}).(*generationVideoPlan)
	return plan != nil
}

// Candidate discovery precedes body normalization, so a plugin that does not
// claim this model/operation cannot change an existing native endpoint's limits.
func (s *GenerationService) VideoPluginRoutingNeeded(ctx context.Context, key *APIKey, platform, model, protocol string) (context.Context, bool, error) {
	repo, ok := s.repo.(GenerationVideoRoutingRepository)
	if !ok {
		return ctx, false, nil
	}
	if key == nil || key.Group == nil || s.plugins == nil {
		return nil, false, ErrGenerationDecode
	}
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, 8<<20); err != nil {
		return nil, false, err
	}
	candidates, err := repo.ListVideoDecodeCandidates(ctx, key.UserID, key.ID, key.Group.ID, NormalizeOpenAICompatiblePlatform(platform), model)
	if err != nil {
		return nil, false, err
	}
	filtered := make([]GenerationVideoDecodeCandidate, 0, len(candidates))
	plugin := GenerationTaskSubmitPlugin(ctx)
	for _, candidate := range candidates {
		if match, ok := generationRouteMatchFromContext(ctx); ok && candidate.VersionID != match.VersionID {
			continue
		}
		version, err := s.plugins.repo.Get(ctx, candidate.VersionID)
		if err != nil || version == nil {
			return nil, false, ErrGenerationDecode
		}
		if plugin != "" && (version.Meta.Key != plugin || !version.Enabled) {
			continue
		}
		account := &Account{Credentials: map[string]any{NewAPIPluginRevisionKey: candidate.VersionID, "model_mapping": candidate.Mapping}}
		upstream := candidate.UpstreamModel
		if upstream == "" {
			upstream = account.GetMappedModel(model)
		}
		if !version.Capabilities.Task || !candidate.supportsModels(taskProtocolModels(version, protocol), model, upstream) {
			continue
		}
		if isNativeVideoGenerationProtocol(protocol) {
			route, found := pluginNativeVideoRoute(version.Meta, protocol)
			if !found || !nativeVideoRouteModel(route.Models, model, upstream) {
				continue
			}
		}
		filtered = append(filtered, candidate)
	}
	ctx = context.WithValue(ctx, generationVideoCandidatesKey{}, generationVideoCandidates{model: model, protocol: protocol, candidates: filtered})
	return ctx, len(filtered) > 0, nil
}

func generationVideoRoutingAllows(ctx context.Context, account *Account, model string) bool {
	plan, _ := ctx.Value(generationVideoPlanKey{}).(*generationVideoPlan)
	if plan == nil {
		return true
	}
	if account == nil || plan.model != model || !plan.allowed[generationVideoDecoderIdentity{account.NewAPIPluginVersionID(), account.GetMappedModel(model)}] {
		return false
	}
	return plan.accountID == 0 || account.ID == plan.accountID && account.NewAPIPluginVersionID() == plan.versionID && generationAccountConnection(account) == plan.connection && account.GetCredential(newAPIPluginConfigEncryptedKey) == plan.configEncrypted
}

func validateGenerationVideoPlan(ctx context.Context, account *Account, model, protocol string) error {
	plan, _ := ctx.Value(generationVideoPlanKey{}).(*generationVideoPlan)
	if plan != nil && (plan.protocol != protocol || !generationVideoRoutingAllows(ctx, account, model)) {
		return ErrGenerationOrigin
	}
	return nil
}

func generationSameOriginIDs(left, right []string) bool {
	a, b := slices.Clone(left), slices.Clone(right)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func validateGenerationVideoIntent(ctx context.Context, account *Account, model, protocol string, intent *generationVideoIntent) error {
	if err := validateGenerationVideoPlan(ctx, account, model, protocol); err != nil {
		return err
	}
	plan, _ := ctx.Value(generationVideoPlanKey{}).(*generationVideoPlan)
	var expected []string
	if plan != nil {
		for _, origin := range plan.origins {
			expected = append(expected, origin.TaskID)
		}
	}
	// Account-scoped late ingress may edit a prompt, but cannot switch parents
	// after the scheduler has already acquired the original account's slot.
	if !generationSameOriginIDs(expected, intent.origins) {
		return ErrGenerationOrigin
	}
	return nil
}

// PrepareVideoRouting is called after replay and before scheduler admission.
// It recognizes only explicit decoder intent, never guesses IDs from JSON.
func (s *GenerationService) PrepareVideoRouting(ctx context.Context, key *APIKey, platform, model string, body json.RawMessage, protocol string) (context.Context, error) {
	repo, ok := s.repo.(GenerationVideoRoutingRepository)
	if !ok { // Older embedded hosts retain their explicit no-origin behavior.
		return ctx, nil
	}
	if key == nil || key.Group == nil || s.plugins == nil {
		return nil, ErrGenerationDecode
	}
	pinned, cached := ctx.Value(generationVideoCandidatesKey{}).(generationVideoCandidates)
	if !cached || pinned.model != model || pinned.protocol != protocol {
		var err error
		ctx, _, err = s.VideoPluginRoutingNeeded(ctx, key, platform, model, protocol)
		if err != nil {
			return nil, err
		}
		pinned, _ = ctx.Value(generationVideoCandidatesKey{}).(generationVideoCandidates)
	}
	candidates := pinned.candidates
	if len(candidates) == 0 {
		return ctx, nil
	}
	// Include source decoding, bounded parent data, runner JSON copies and file
	// metadata in the same admission budget as the client request body.
	if err := pkghttputil.ReserveRequestBodyMemory(ctx, 24<<20); err != nil {
		return nil, err
	}
	decodeBody, files, err := s.previewVideoDecodeFiles(ctx, key, body, protocol)
	if err != nil {
		return nil, err
	}
	decodeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	decodeCtx = context.WithValue(decodeCtx, generationDecodeFilesKey{}, files)
	plan := &generationVideoPlan{model: model, protocol: protocol, allowed: make(map[generationVideoDecoderIdentity]bool)}
	seen := make(map[generationVideoDecoderIdentity]bool)
	var originIDs []string
	accepted := false
	recognized := false
	plugin := GenerationTaskSubmitPlugin(ctx)
	for _, candidate := range candidates {
		if match, ok := generationRouteMatchFromContext(ctx); ok && candidate.VersionID != match.VersionID {
			continue
		}
		account := &Account{Credentials: map[string]any{NewAPIPluginRevisionKey: candidate.VersionID, "model_mapping": candidate.Mapping}}
		upstream := candidate.UpstreamModel
		if upstream == "" {
			upstream = account.GetMappedModel(model)
		}
		identity := generationVideoDecoderIdentity{candidate.VersionID, upstream}
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if len(seen) > 32 {
			return nil, ErrGenerationCapacity
		}
		version, err := s.plugins.repo.Get(decodeCtx, candidate.VersionID)
		if err != nil || version == nil {
			return nil, ErrGenerationDecode
		}
		if plugin != "" && version.Meta.Key != plugin {
			continue
		}
		if !version.Capabilities.Task || !candidate.supportsModels(taskProtocolModels(version, protocol), model, upstream) {
			continue
		}
		if isNativeVideoGenerationProtocol(protocol) {
			route, found := pluginNativeVideoRoute(version.Meta, protocol)
			if !found || !nativeVideoRouteModel(route.Models, model, upstream) {
				continue
			}
		}
		recognized = true
		if !version.Enabled {
			continue // Disabling a plugin also forbids executing its decoder.
		}
		intent, err := s.plugins.decodeVideoIntent(decodeCtx, candidate.VersionID, model, upstream, decodeBody, protocol)
		if err != nil {
			if decodeCtx.Err() != nil {
				return nil, ErrGenerationDecode
			}
			continue
		}
		if accepted && !generationSameOriginIDs(originIDs, intent.origins) {
			return nil, ErrGenerationDecode
		}
		accepted = true
		originIDs = intent.origins
		plan.allowed[identity] = true
	}
	if !accepted {
		if recognized {
			return nil, ErrGenerationDecode
		}
		return ctx, nil // Preserve unclaimed vendor-native endpoints.
	}
	for _, id := range originIDs {
		parent, err := repo.GetVideoOrigin(ctx, id, key.UserID, key.ID, key.Group.ID)
		if err != nil || parent == nil || parent.GroupID != key.Group.ID || parent.UpstreamID == "" || parent.Platform != NormalizeOpenAICompatiblePlatform(platform) {
			return nil, ErrGenerationOrigin
		}
		if plan.accountID == 0 {
			plan.accountID, plan.versionID = parent.AccountID, parent.PluginVersionID
			plan.connection, plan.configEncrypted = parent.Connection, parent.ConfigEncrypted
			plan.credential = parent.CredentialBinding
		} else if parent.AccountID != plan.accountID || parent.PluginVersionID != plan.versionID || parent.Connection != plan.connection || parent.ConfigEncrypted != plan.configEncrypted || !SameGenerationCredential(parent.CredentialBinding, plan.credential) {
			return nil, ErrGenerationOrigin
		}
		view := generationTaskView(parent)
		plan.origins = append(plan.origins, GenerationOriginTask{TaskID: parent.ID, UpstreamTaskID: parent.UpstreamID, Action: parent.Action, Status: generationViewStatus(view), Data: bytes.Clone(parent.Data)})
	}
	if err := ValidateGenerationOrigins(plan.origins); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, generationVideoPlanKey{}, plan), nil
}

// Preview only metadata. Multipart spools and stored files are not copied or
// persisted while probing protocol candidates, and ownership still applies.
func (s *GenerationService) previewVideoDecodeFiles(ctx context.Context, key *APIKey, body json.RawMessage, protocol string) (json.RawMessage, []GenerationFileSnapshot, error) {
	if upload, _ := ctx.Value(generationMultipartUploadKey{}).(*generationMultipartUpload); upload != nil && len(upload.files) > 0 {
		if protocol != GenerationProtocolOpenAI && protocol != GenerationProtocolGenericTask && protocol != GenerationProtocolOpenAIImage && !strings.HasPrefix(protocol, GenerationProtocolNativePrefix) {
			return nil, nil, ErrGenerationDecode
		}
		files := make([]GenerationFileSnapshot, 0, len(upload.files))
		counts := make(map[string]int)
		for _, input := range upload.files {
			ref := generationFileRef(input.field, counts[input.field])
			counts[input.field]++
			files = append(files, GenerationFileSnapshot{File: pluginTask.File{Ref: ref, Field: input.field, Filename: input.filename, MimeType: input.kind, Size: input.size}})
		}
		return body, files, nil
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(body, &input) != nil || input == nil {
		return nil, nil, ErrGenerationDecode
	}
	if input["file_references"] != nil {
		return s.prepareVideoFiles(ctx, key, body, protocol) // Lookup only; never uploads this branch.
	}
	if multipart, _ := ctx.Value(generationMultipartInputKey{}).(bool); multipart && protocol == GenerationProtocolOpenAI {
		var reference string
		if json.Unmarshal(input["input_reference"], &reference) == nil && strings.HasPrefix(reference, "data:") {
			header, encoded, found := strings.Cut(strings.TrimPrefix(reference, "data:"), ",")
			kind, b64 := strings.CutSuffix(header, ";base64")
			if !found || !b64 || kind != "image/png" && kind != "image/jpeg" && kind != "image/webp" {
				return nil, nil, errors.New("invalid multipart image")
			}
			delete(input, "input_reference")
			cleaned, err := json.Marshal(input)
			return cleaned, []GenerationFileSnapshot{{File: pluginTask.File{Ref: "request_file:input_reference", Field: "input_reference", Filename: "input_reference", MimeType: kind, Size: int64(base64.StdEncoding.DecodedLen(len(encoded)))}}}, err
		}
	}
	return body, nil, nil
}

func generationViewStatus(view map[string]any) string {
	status, _ := view["status"].(string)
	return status
}
