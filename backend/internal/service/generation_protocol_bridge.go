package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	pluginTask "github.com/Wei-Shaw/sub2api/newapi-engine/task"
	"github.com/Wei-Shaw/sub2api/newapi-engine/taskprotocol"
)

const maxInlineImageBytes = 16 << 20

type generationResponsesModeKey struct{}

// WithGenerationResponsesMode pins the requested openai_responses mode
// ("sync", "stream", or "background") on the context so scheduler selection
// only considers accounts whose bound plugin declares that mode.
func WithGenerationResponsesMode(ctx context.Context, mode string) context.Context {
	return context.WithValue(ctx, generationResponsesModeKey{}, strings.TrimSpace(mode))
}

func generationResponsesModeFromContext(ctx context.Context) string {
	mode, _ := ctx.Value(generationResponsesModeKey{}).(string)
	return mode
}

// PluginResponsesSupportInfo describes the pinned openai_responses capabilities
// of a task plugin version.
type PluginResponsesSupportInfo struct {
	Supports        []string
	HasRenderFinal  bool
	HasRenderEvents bool
}

// PluginTaskResponsesInfo inspects the pinned plugin version for a task to
// determine supported Responses modes and render hooks.
func (s *GenerationService) PluginTaskResponsesInfo(ctx context.Context, versionID int64) (PluginResponsesSupportInfo, error) {
	if s == nil || s.plugins == nil || s.plugins.repo == nil || versionID <= 0 {
		return PluginResponsesSupportInfo{}, errors.New("plugin service unavailable")
	}
	version, err := s.plugins.repo.Get(ctx, versionID)
	if err != nil || version == nil {
		return PluginResponsesSupportInfo{}, errors.New("plugin version unavailable")
	}
	binding := pluginResponsesTaskBinding(version.Meta, version.Capabilities)
	if len(binding.Models) == 0 && len(binding.Supports) == 0 {
		return PluginResponsesSupportInfo{}, errors.New("plugin does not declare openai_responses protocol")
	}
	return PluginResponsesSupportInfo{
		Supports:        binding.Supports,
		HasRenderFinal:  slices.Contains(binding.Supports, "sync") || slices.Contains(binding.Supports, "background"),
		HasRenderEvents: slices.Contains(binding.Supports, "stream"),
	}, nil
}

// HasNewAPIResponsesTaskModel reports whether any schedulable account in the
// group/platform binds a Task plugin declaring openai_responses for model, and
// returns the union of supported modes across matching accounts.
func (s *GenerationService) HasNewAPIResponsesTaskModel(ctx context.Context, groupID *int64, platform, model string) (bool, []string) {
	if s == nil || s.accounts == nil || strings.TrimSpace(model) == "" {
		return false, nil
	}
	targetPlatform := NormalizeOpenAICompatiblePlatform(platform)
	accounts, err := s.listSchedulablePlatformAccounts(ctx, groupID, targetPlatform)
	if err != nil {
		return false, nil
	}
	matched := false
	var supports []string
	for i := range accounts {
		a := &accounts[i]
		if !a.HasNewAPIPlugin() || !a.SupportsNewAPIResponsesTaskModel(model) {
			continue
		}
		matched = true
		for _, mode := range a.NewAPIResponsesTaskBinding().Supports {
			if !slices.Contains(supports, mode) {
				supports = append(supports, mode)
			}
		}
	}
	return matched, supports
}

// HasNewAPIImageTaskModel reports whether any schedulable account in the
// group/platform binds a Task plugin declaring openai_image for model.
func (s *GenerationService) HasNewAPIImageTaskModel(ctx context.Context, groupID *int64, platform, model string) bool {
	if s == nil || s.accounts == nil || strings.TrimSpace(model) == "" {
		return false
	}
	targetPlatform := NormalizeOpenAICompatiblePlatform(platform)
	accounts, err := s.listSchedulablePlatformAccounts(ctx, groupID, targetPlatform)
	if err != nil {
		return false
	}
	for i := range accounts {
		a := &accounts[i]
		if a.HasNewAPIPlugin() && a.SupportsNewAPIImageTaskModel(model) {
			return true
		}
	}
	return false
}

// FindNewAPINativeRoute matches a declared meta.routes entry for pluginKey
// among schedulable accounts in the group/platform.
func (s *GenerationService) FindNewAPINativeRoute(ctx context.Context, groupID *int64, platform, pluginKey, method, subpath string) (int64, jsplugin.Route, map[string]string, bool) {
	if s == nil || s.accounts == nil || !jsplugin.ValidPluginKey(pluginKey) {
		return 0, jsplugin.Route{}, nil, false
	}
	targetPlatform := NormalizeOpenAICompatiblePlatform(platform)
	accounts, err := s.listSchedulablePlatformAccounts(ctx, groupID, targetPlatform)
	if err != nil {
		return 0, jsplugin.Route{}, nil, false
	}
	for i := range accounts {
		a := &accounts[i]
		if !a.HasNewAPIPlugin() {
			continue
		}
		if route, params, ok := a.MatchNewAPINativeRoute(pluginKey, method, subpath); ok {
			return a.NewAPIPluginVersionID(), route, params, true
		}
	}
	return 0, jsplugin.Route{}, nil, false
}

func (s *GenerationService) listSchedulablePlatformAccounts(ctx context.Context, groupID *int64, platform string) ([]Account, error) {
	if groupID != nil {
		return s.accounts.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platform)
	}
	return s.accounts.ListSchedulableByPlatform(ctx, platform)
}

// RendererArtifacts builds the ctx.artifacts map keyed by artifact key, using
// host-owned /v1/tasks/:id/artifacts/:key/content URLs so upstream URLs are
// never exposed.
func (s *GenerationService) RendererArtifacts(ctx context.Context, task *GenerationTask) (map[string]any, error) {
	result := map[string]any{}
	if s == nil || task == nil || task.Status != GenerationSucceeded {
		return result, nil
	}
	artifacts, err := s.ListTaskArtifacts(ctx, task)
	if err != nil {
		return nil, err
	}
	for _, artifact := range artifacts {
		result[artifact.Key] = map[string]any{
			"key":      artifact.Key,
			"type":     artifact.Type,
			"url":      "/v1/tasks/" + url.PathEscape(task.ID) + "/artifacts/" + url.PathEscape(artifact.Key) + "/content",
			"mimeType": artifact.MimeType,
		}
	}
	return result, nil
}

func (s *GenerationService) taskAccount(ctx context.Context, task *GenerationTask) (*Account, error) {
	if s == nil || s.accounts == nil || task == nil {
		return nil, errors.New("task account repository unavailable")
	}
	account, err := s.accounts.GetByID(ctx, task.AccountID)
	if err != nil || account == nil {
		return nil, errors.New("task account unavailable")
	}
	return account, nil
}

// RenderResponsesFinal invokes protocols.openai_responses.renderFinal for a
// terminal durable task with host-owned artifacts.
func (s *GenerationService) RenderResponsesFinal(ctx context.Context, task *GenerationTask) (json.RawMessage, error) {
	account, err := s.taskAccount(ctx, task)
	if err != nil {
		return nil, err
	}
	artifacts, err := s.RendererArtifacts(ctx, task)
	if err != nil {
		return nil, err
	}
	renderCtx := map[string]any{
		"protocol":      "openai_responses",
		"operation":     "retrieve",
		"model":         task.Model,
		"upstreamModel": task.UpstreamModel,
		"artifacts":     artifacts,
	}
	return s.plugins.CallTaskRenderHook(ctx, account, task, "protocols", []string{"openai_responses", "renderFinal"}, renderCtx, generationTaskPublicRenderView(task))
}

// RenderResponsesEvents invokes protocols.openai_responses.renderEvents for one
// tick (or stream-only synthesis) and validates the result with taskprotocol.
func (s *GenerationService) RenderResponsesEvents(ctx context.Context, task *GenerationTask, prevState taskprotocol.ProtocolState) (taskprotocol.ProtocolEventResult, error) {
	account, err := s.taskAccount(ctx, task)
	if err != nil {
		return taskprotocol.ProtocolEventResult{}, err
	}
	artifacts, err := s.RendererArtifacts(ctx, task)
	if err != nil {
		return taskprotocol.ProtocolEventResult{}, err
	}
	renderCtx := map[string]any{
		"protocol":      "openai_responses",
		"operation":     "stream",
		"model":         task.Model,
		"upstreamModel": task.UpstreamModel,
		"artifacts":     artifacts,
	}
	var stateArg any
	if prevState.Present && !prevState.Null && len(prevState.Value) > 0 {
		stateArg = json.RawMessage(prevState.Value)
	}
	raw, err := s.plugins.CallTaskRenderHook(ctx, account, task, "protocols", []string{"openai_responses", "renderEvents"}, renderCtx, generationTaskPublicRenderView(task), stateArg)
	if err != nil {
		return taskprotocol.ProtocolEventResult{}, err
	}
	return taskprotocol.DecodePluginProtocolEventResult(raw, taskprotocol.DefaultPluginProtocolLimits())
}

// RenderTaskImage invokes protocols.openai_image.render for a terminal durable
// task with host-owned artifacts.
func (s *GenerationService) RenderTaskImage(ctx context.Context, task *GenerationTask) (json.RawMessage, error) {
	account, err := s.taskAccount(ctx, task)
	if err != nil {
		return nil, err
	}
	artifacts, err := s.RendererArtifacts(ctx, task)
	if err != nil {
		return nil, err
	}
	renderCtx := map[string]any{
		"protocol":      "openai_image",
		"operation":     "retrieve",
		"model":         task.Model,
		"upstreamModel": task.UpstreamModel,
		"artifacts":     artifacts,
	}
	return s.plugins.CallTaskRenderHook(ctx, account, task, "protocols", []string{"openai_image", "render"}, renderCtx, generationTaskPublicRenderView(task))
}

// InlineTaskImageBase64 populates b64_json for items that only carry url when
// response_format="b64_json". Host artifact URLs are opened directly through
// TaskArtifactContent; external URLs use outbound-validated public-host HTTP.
func (s *GenerationService) InlineTaskImageBase64(ctx context.Context, task *GenerationTask, response map[string]any) {
	if s == nil || task == nil || response == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	remaining := int64(maxInlineImageBytes)
	encode := func(r io.Reader) (string, error) {
		data, err := io.ReadAll(io.LimitReader(r, remaining+1))
		if err != nil || len(data) == 0 || int64(len(data)) > remaining {
			return "", errors.New("inline image content exceeds limit or is unreadable")
		}
		remaining -= int64(len(data))
		return base64.StdEncoding.EncodeToString(data), nil
	}
	prefix := "/v1/tasks/" + url.PathEscape(task.ID) + "/artifacts/"
	taskprotocol.InlineImageBase64(response, func(rawURL string) (string, error) {
		if remaining == 0 || ctx.Err() != nil {
			return "", errors.New("inline image budget exhausted")
		}
		if strings.HasPrefix(rawURL, prefix) && strings.HasSuffix(rawURL, "/content") {
			escapedKey := strings.TrimSuffix(strings.TrimPrefix(rawURL, prefix), "/content")
			artifactKey, err := url.PathUnescape(escapedKey)
			if err != nil || !pluginTask.ValidArtifactKey(artifactKey) {
				return "", errors.New("invalid artifact key")
			}
			resp, release, err := s.TaskArtifactContent(ctx, task, http.MethodGet, artifactKey, nil)
			if err != nil {
				return "", err
			}
			defer func() {
				_ = resp.Body.Close()
				if release != nil {
					release()
				}
			}()
			return encode(resp.Body)
		}
		if s.gateway == nil || s.gateway.httpUpstream == nil {
			return "", errors.New("outbound HTTP unavailable")
		}
		if _, err := s.gateway.validateOutboundURL(rawURL); err != nil {
			return "", err
		}
		fetchCtx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamPublicHostsOnly(ctx)), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", err
		}
		resp, err := s.gateway.httpUpstream.Do(req, "", 0, 0)
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("fetch image returned HTTP %d", resp.StatusCode)
		}
		return encode(resp.Body)
	})
}

// NativeRouteDecodeResult captures the decoded intent of a meta.routes submit
// or dynamic route call.
type NativeRouteDecodeResult struct {
	Kind          string          `json:"kind"`
	Model         string          `json:"model"`
	Action        string          `json:"action"`
	RequestBody   json.RawMessage `json:"requestBody"`
	TaskIDs       []string        `json:"taskIds"`
	OriginTaskIDs json.RawMessage `json:"originTaskIds"`
}

// Plugins exposes the underlying New API plugin service for gateway handlers
// that need request-processing admission on native task routes.
func (s *GenerationService) Plugins() *NewAPIPluginService {
	if s == nil {
		return nil
	}
	return s.plugins
}

// DecodeNativeRoute runs a plugin's native.<route.Decode> hook offline to
// classify submit vs query intents and discover declared models or task IDs.
func (s *GenerationService) DecodeNativeRoute(ctx context.Context, versionID int64, route jsplugin.Route, params map[string]string, subpath string, body json.RawMessage) (*NativeRouteDecodeResult, error) {
	return s.decodeNativeRoute(ctx, versionID, true, route, params, subpath, body)
}

// DecodeNativeRoutePinned runs native.<route.Decode> against a historical task's
// pinned plugin version for read-only batch query compatibility after unbinding.
func (s *GenerationService) DecodeNativeRoutePinned(ctx context.Context, versionID int64, route jsplugin.Route, params map[string]string, subpath string, body json.RawMessage) (*NativeRouteDecodeResult, error) {
	return s.decodeNativeRoute(ctx, versionID, false, route, params, subpath, body)
}

func (s *GenerationService) decodeNativeRoute(ctx context.Context, versionID int64, requireEnabled bool, route jsplugin.Route, params map[string]string, subpath string, body json.RawMessage) (*NativeRouteDecodeResult, error) {
	if s == nil || s.plugins == nil || route.Decode == "" {
		return nil, errors.New("native route decode is unavailable")
	}
	if params == nil {
		params = map[string]string{}
	}
	var wireBody map[string]any
	if len(bytes.TrimSpace(body)) == 0 && !HasGenerationMultipartFile(ctx) {
		wireBody = map[string]any{"kind": "empty"}
	} else {
		cleanedBody, files := generationContextDecodeFiles(ctx, body)
		var bodyErr error
		wireBody, bodyErr = generationFileDecodeBody(cleanedBody, files)
		if bodyErr != nil {
			return nil, bodyErr
		}
	}
	arg := map[string]any{
		"operation": route.Type,
		"method":    route.Method,
		"path":      subpath,
		"params":    params,
		"body":      wireBody,
	}
	var raw json.RawMessage
	var err error
	if requireEnabled {
		raw, err = s.plugins.CallVersionHook(ctx, versionID, "native", []string{route.Decode}, arg)
	} else {
		raw, err = s.plugins.CallPinnedVersionHook(ctx, versionID, "native", []string{route.Decode}, arg)
	}
	if err != nil {
		return nil, err
	}
	var decoded NativeRouteDecodeResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, errors.New("invalid native route decode result")
	}
	return &decoded, nil
}

// RenderNativeTask invokes native.<route.Render> for a single task and
// sanitizes any {__status, __headers, __body} reply wrapper. Uses the task's
// pinned plugin version directly so historical tasks stay readable even after
// an account unbinds or disables the plugin.
func (s *GenerationService) RenderNativeTask(ctx context.Context, task *GenerationTask, route jsplugin.Route, params map[string]string, operation string) (taskprotocol.NativeReply, error) {
	if route.Render == "" {
		return taskprotocol.NativeReply{Status: http.StatusOK, Body: task.PublicTaskReadView(), HasBody: true}, nil
	}
	artifacts, err := s.RendererArtifacts(ctx, task)
	if err != nil {
		return taskprotocol.NativeReply{}, err
	}
	if params == nil {
		params = map[string]string{}
	}
	renderCtx := map[string]any{
		"operation":     operation,
		"method":        route.Method,
		"path":          route.Path,
		"params":        params,
		"model":         task.Model,
		"upstreamModel": task.UpstreamModel,
		"artifacts":     artifacts,
	}
	raw, err := s.plugins.CallPinnedVersionHook(ctx, task.PluginVersionID, "native", []string{route.Render}, renderCtx, generationTaskPublicRenderView(task))
	if err != nil {
		return taskprotocol.NativeReply{}, err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return taskprotocol.NativeReply{}, errors.New("native render output is not valid JSON")
	}
	return taskprotocol.ParseNativeReply(http.StatusOK, decoded)
}

// RenderNativeTaskList invokes native.<route.Render> for a dynamic batch query
// over caller-owned tasks.
func (s *GenerationService) RenderNativeTaskList(ctx context.Context, versionID int64, tasks []*GenerationTask, route jsplugin.Route, params map[string]string) (taskprotocol.NativeReply, error) {
	views := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		views = append(views, generationTaskPublicRenderView(task))
	}
	if route.Render == "" {
		return taskprotocol.NativeReply{Status: http.StatusOK, Body: views, HasBody: true}, nil
	}
	if params == nil {
		params = map[string]string{}
	}
	renderCtx := map[string]any{
		"operation": "query",
		"method":    route.Method,
		"path":      route.Path,
		"params":    params,
	}
	raw, err := s.plugins.CallPinnedVersionHook(ctx, versionID, "native", []string{route.Render}, renderCtx, views)
	if err != nil {
		return taskprotocol.NativeReply{}, err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return taskprotocol.NativeReply{}, errors.New("native list render output is not valid JSON")
	}
	return taskprotocol.ParseNativeReply(http.StatusOK, decoded)
}

// GetPinnedPluginTaskVersion verifies task ownership and returns the plugin
// version metadata so native routes can enforce plugin-key isolation.
func (s *GenerationService) GetPluginVersionMeta(ctx context.Context, versionID int64) (*NewAPIPluginVersion, error) {
	if s == nil || s.plugins == nil || s.plugins.repo == nil || versionID <= 0 {
		return nil, ErrGenerationTaskNotFound
	}
	return s.plugins.repo.Get(ctx, versionID)
}

func generationTaskTerminal(status string) bool {
	return status == GenerationSucceeded || status == GenerationFailed || status == GenerationUncertain
}

func generationTaskSnapshotKey(t *GenerationTask) string {
	if t == nil {
		return ""
	}
	return fmt.Sprintf("%s|%d|%s|%s|%d", t.Status, t.Progress, t.DeliveryStatus, t.ErrorCode, len(t.Data))
}

// AwaitTask only observes committed state. The leased worker owns submission
// and settlement, including when another process is serving the HTTP request.
func (s *GenerationService) AwaitTask(ctx context.Context, task *GenerationTask, heartbeat time.Duration, onTick func(current *GenerationTask, isHeartbeat bool) error) (*GenerationTask, error) {
	if s == nil || task == nil {
		return nil, errors.New("task is unavailable")
	}
	current := task
	lastKey := ""
	emit := func(t *GenerationTask) error {
		if onTick == nil {
			return nil
		}
		key := generationTaskSnapshotKey(t)
		if key == lastKey {
			return nil
		}
		lastKey = key
		return onTick(t, false)
	}
	if err := emit(current); err != nil {
		return current, err
	}
	if generationTaskTerminal(current.Status) {
		return current, nil
	}
	backoff := 50 * time.Millisecond
	lastEmit := time.Now()
	for {
		if ctx.Err() != nil {
			return current, ctx.Err()
		}
		refreshed, err := s.repo.GetOwned(ctx, current.ID, current.UserID, current.APIKeyID)
		if err != nil {
			return current, err
		}
		if refreshed == nil {
			return current, ErrGenerationTaskNotFound
		}
		current = refreshed
		prevKey := lastKey
		if err := emit(current); err != nil {
			return current, err
		}
		if lastKey != prevKey {
			lastEmit = time.Now()
			backoff = 50 * time.Millisecond
		} else if heartbeat > 0 && onTick != nil && time.Since(lastEmit) >= heartbeat {
			if err := onTick(current, true); err != nil {
				return current, err
			}
			lastEmit = time.Now()
		}
		if generationTaskTerminal(current.Status) {
			return current, nil
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return current, ctx.Err()
		case <-timer.C:
		}
		if backoff < 500*time.Millisecond {
			backoff *= 2
			if backoff > 500*time.Millisecond {
				backoff = 500 * time.Millisecond
			}
		}
	}
}
