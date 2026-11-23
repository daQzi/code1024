package service

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/tidwall/gjson"
)

const (
	GenerationProtocolOpenAI          = generation.ProtocolOpenAI
	GenerationProtocolGenericTask     = generation.ProtocolGenericTask
	GenerationProtocolOpenAIResponses = generation.ProtocolOpenAIResponses
	GenerationProtocolOpenAIImage     = generation.ProtocolOpenAIImage
	GenerationProtocolNativePrefix    = generation.ProtocolNativePrefix
	GenerationProtocolXAI             = generation.ProtocolXAI
	GenerationProtocolXAIEdit         = generation.ProtocolXAIEdit
	GenerationProtocolXAIExtend       = generation.ProtocolXAIExtend
	GenerationProtocolArk             = generation.ProtocolArk
	newAPIPluginNativeVideosKey       = "newapi_plugin_native_videos"
)

// IsVideoGenerationProtocol reports whether protocol represents a video task
// that uses video pricing fallbacks, video group rate multipliers, and the
// /v1/videos listing/retrieval endpoints.
func IsVideoGenerationProtocol(protocol string) bool {
	switch protocol {
	case "", GenerationProtocolOpenAI, GenerationProtocolXAI, GenerationProtocolXAIEdit, GenerationProtocolXAIExtend, GenerationProtocolArk:
		return true
	default:
		return false
	}
}

func isNativeVideoGenerationProtocol(protocol string) bool {
	switch protocol {
	case GenerationProtocolXAI, GenerationProtocolXAIEdit, GenerationProtocolXAIExtend, GenerationProtocolArk:
		return true
	default:
		return false
	}
}

type generationVideoRoute struct{ Path string }

func pluginNativeVideoRoute(meta jsplugin.Meta, protocol string) (jsplugin.Route, bool) {
	ingress, ok := generationVideoIngress(protocol)
	if !ok || protocol == GenerationProtocolOpenAI {
		return jsplugin.Route{}, false
	}
	for _, route := range meta.Routes {
		if route.Path == ingress.Path && route.Method == http.MethodPost && route.Type == jsplugin.RouteTypeSubmit && route.Decode != "" && route.Render != "" && (route.RetainResult == nil || *route.RetainResult) {
			return route, true
		}
	}
	return jsplugin.Route{}, false
}

func nativeVideoRouteModel(models []string, model, upstream string) bool {
	if len(models) == 0 {
		return true
	}
	for _, candidate := range models {
		if pluginVideoModelMatches(candidate, model) || pluginVideoModelMatches(candidate, upstream) {
			return true
		}
	}
	return false
}

func pluginNativeVideoBinding(meta jsplugin.Meta) map[string][]string {
	routes := make(map[string][]string)
	for _, protocol := range []string{GenerationProtocolXAI, GenerationProtocolXAIEdit, GenerationProtocolXAIExtend, GenerationProtocolArk} {
		if route, ok := pluginNativeVideoRoute(meta, protocol); ok {
			routes[protocol] = append([]string{}, route.Models...)
		}
	}
	return routes
}

// Only existing, authenticated video endpoints are exposed. A plugin's routes
// never register new public paths or claim the image and text handlers.
func generationVideoIngress(protocol string) (generationVideoRoute, bool) {
	switch protocol {
	case GenerationProtocolOpenAI:
		return generationVideoRoute{Path: "/v1/videos"}, true
	case GenerationProtocolSunoTask:
		return generationVideoRoute{Path: "/v1/tasks/sunoapi"}, true
	case GenerationProtocolGenericTask:
		return generationVideoRoute{Path: "/v1/tasks"}, true
	case GenerationProtocolOpenAIResponses:
		return generationVideoRoute{Path: "/v1/responses"}, true
	case GenerationProtocolOpenAIImage:
		return generationVideoRoute{Path: "/v1/images/generations"}, true
	case GenerationProtocolXAI:
		return generationVideoRoute{Path: "/v1/videos/generations"}, true
	case GenerationProtocolXAIEdit:
		return generationVideoRoute{Path: "/v1/videos/edits"}, true
	case GenerationProtocolXAIExtend:
		return generationVideoRoute{Path: "/v1/videos/extensions"}, true
	case GenerationProtocolArk:
		return generationVideoRoute{Path: "/api/v3/contents/generations/tasks"}, true
	}
	if strings.HasPrefix(protocol, GenerationProtocolNativePrefix) && len(protocol) > len(GenerationProtocolNativePrefix) {
		return generationVideoRoute{Path: "/v1/tasks/" + strings.TrimPrefix(protocol, GenerationProtocolNativePrefix) + "/native"}, true
	}
	return generationVideoRoute{}, false
}

type generationIngressPathKey struct{}

func WithGenerationIngressPath(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, generationIngressPathKey{}, path)
}

func generationIngressPath(ctx context.Context, fallback string) string {
	if path, _ := ctx.Value(generationIngressPathKey{}).(string); strings.TrimSpace(path) != "" {
		return path
	}
	return fallback
}

type generationRouteMatch struct {
	Route     jsplugin.Route
	Params    map[string]string
	Path      string
	VersionID int64
}

type generationRouteMatchKey struct{}

func WithGenerationRouteMatch(ctx context.Context, route jsplugin.Route, params map[string]string, path string, versionID int64) context.Context {
	return context.WithValue(ctx, generationRouteMatchKey{}, generationRouteMatch{
		Route:     route,
		Params:    params,
		Path:      path,
		VersionID: versionID,
	})
}

func generationRouteMatchFromContext(ctx context.Context) (generationRouteMatch, bool) {
	match, ok := ctx.Value(generationRouteMatchKey{}).(generationRouteMatch)
	return match, ok
}

// GenerationVideoCreateProtocol recognizes only gateway-owned endpoint aliases.
func GenerationVideoCreateProtocol(path string) (string, bool) {
	if _, remix := GenerationVideoRemixOrigin(path); remix {
		return GenerationProtocolOpenAI, true
	}
	videoPath := strings.TrimPrefix(path, "/v1")
	switch videoPath {
	case "/videos":
		return GenerationProtocolOpenAI, true
	case "/videos/generations":
		return GenerationProtocolXAI, true
	case "/videos/edits":
		return GenerationProtocolXAIEdit, true
	case "/videos/extensions":
		return GenerationProtocolXAIExtend, true
	}
	for _, prefix := range []string{"/api/v3", "/v3", "/v1", ""} {
		if path == prefix+"/contents/generations/tasks" {
			return GenerationProtocolArk, true
		}
	}
	return "", false
}

type generationProtocolKey struct{}

func withGenerationProtocol(ctx context.Context, protocol string) context.Context {
	return context.WithValue(ctx, generationProtocolKey{}, protocol)
}

// The same idempotency key on different wire operations must not turn an edit
// into a generation, even when their JSON happens to be identical.
func generationProtocolFingerprint(ctx context.Context, fingerprint string) string {
	if plugin := GenerationTaskSubmitPlugin(ctx); plugin != "" {
		fingerprint = HashUsageRequestPayload([]byte("task_submit\x00" + plugin + "\x00" + fingerprint))
	}
	if remix, ok := ctx.Value(generationRemixKey{}).(generationRemix); ok {
		fingerprint = HashUsageRequestPayload([]byte("openai_video_remix\x00" + remix.origin + "\x00" + fingerprint))
	}
	protocol, _ := ctx.Value(generationProtocolKey{}).(string)
	if protocol == "" || protocol == GenerationProtocolOpenAI {
		return fingerprint // Preserve every already accepted canonical request.
	}
	if protocol == GenerationProtocolOpenAIImage || strings.HasPrefix(protocol, GenerationProtocolNativePrefix) {
		if match, ok := generationRouteMatchFromContext(ctx); ok && match.Route.Method != "" && !strings.EqualFold(match.Route.Method, http.MethodPost) {
			fingerprint = HashUsageRequestPayload([]byte(strings.ToUpper(match.Route.Method) + "\x00" + fingerprint))
		}
		fingerprint = HashUsageRequestPayload([]byte(generationIngressPath(ctx, "") + "\x00" + fingerprint))
	}
	return HashUsageRequestPayload([]byte(protocol + "\x00" + fingerprint))
}

func (s *GenerationService) ReplayVideo(ctx context.Context, user, key int64, idempotency string, body json.RawMessage, protocol string) (*GenerationTask, error) {
	return s.Replay(withGenerationProtocol(ctx, protocol), user, key, idempotency, body)
}

// ReplayBoundTask also checks the original group before returning any saved
// task. An API key can be reassigned to a different group after admission.
func (s *GenerationService) ReplayBoundTask(ctx context.Context, key *APIKey, idempotency string, body json.RawMessage, protocol string) (*GenerationTask, error) {
	if key == nil || key.GroupID == nil {
		return nil, ErrGenerationTaskNotFound
	}
	existing, err := s.ReplayVideo(ctx, key.UserID, key.ID, idempotency, body, protocol)
	if err == nil && existing != nil && existing.GroupID != *key.GroupID {
		return nil, ErrGenerationIdempotencyConflict
	}
	return existing, err
}

func (a *Account) supportsNativeVideoProtocol(protocol, model string) bool {
	if protocol == "" || protocol == GenerationProtocolOpenAI {
		return true
	}
	var routes map[string][]string
	raw, _ := json.Marshal(a.Credentials[newAPIPluginNativeVideosKey])
	if json.Unmarshal(raw, &routes) != nil {
		return false
	}
	models, ok := routes[protocol]
	if !ok {
		return false
	}
	return nativeVideoRouteModel(models, model, a.GetMappedModel(model))
}

// WithNativeVideoPluginRouting narrows scheduler candidates to the account's
// validated manifest snapshot, before priority/weight or concurrency selection.
func WithNativeVideoPluginRouting(ctx context.Context, protocol string) context.Context {
	return withGenerationProtocol(WithNewAPIPluginRouting(ctx), protocol)
}

func generationNativePublicFields(body, decoded json.RawMessage, protocol string) GenerationVideoFields {
	fields := generationVideoFields(body, decoded)
	if protocol == GenerationProtocolOpenAI {
		return fields
	}
	if fields.Seconds == "" {
		if duration := gjson.GetBytes(body, "duration"); duration.Type == gjson.Number && duration.Num > 0 && len(duration.Raw) <= 24 {
			fields.Seconds = duration.Raw
		}
	}
	if seconds, err := strconv.ParseFloat(fields.Seconds, 64); err != nil || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		fields.Seconds = ""
	}
	if protocol == GenerationProtocolArk {
		if info, err := ParseSeedanceRequest(body); err == nil && len(info.Prompt) <= 8192 {
			fields.Prompt = info.Prompt
		}
	}
	return fields
}
