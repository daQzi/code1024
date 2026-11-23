package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/newapi-engine/builtin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/generation"
	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/tidwall/gjson"
)

const GenerationTaskSubmitRoute = "/v1/tasks/:key"
const GenerationProtocolSunoTask = generation.ProtocolSunoTask
const newAPIPluginSunoModelsKey = "newapi_plugin_suno_models"

type generationTaskSubmitKey struct{}

func WithGenerationTaskSubmit(ctx context.Context, pluginKey string) context.Context {
	return context.WithValue(ctx, generationTaskSubmitKey{}, pluginKey)
}

func GenerationTaskSubmitPlugin(ctx context.Context) string {
	key, _ := ctx.Value(generationTaskSubmitKey{}).(string)
	return key
}

func GenerationTaskSubmitProtocol(ctx context.Context) string {
	if GenerationTaskSubmitPlugin(ctx) == "sunoapi" {
		return GenerationProtocolSunoTask
	}
	return GenerationProtocolOpenAI
}

func sunoTaskManifest(meta jsplugin.Meta, descriptor runner.Descriptor) bool {
	return descriptor.Task && meta.Key == "sunoapi" && meta.Version == "1.1.0" && meta.FetchMode == "batch"
}

func sunoTaskVersion(v *NewAPIPluginVersion) bool {
	if v == nil || !sunoTaskManifest(v.Meta, v.Capabilities) {
		return false
	}
	source, err := builtin.Source("sunoapi")
	return err == nil && source == v.Source
}

func sunoTaskMetadataVersion(v NewAPIPluginVersion) bool {
	if !sunoTaskManifest(v.Meta, v.Capabilities) {
		return false
	}
	source, err := builtin.Source("sunoapi")
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(source))
	return v.SHA256 == hex.EncodeToString(digest[:])
}

func taskProtocolModels(v *NewAPIPluginVersion, protocol string) []string {
	if v == nil {
		return nil
	}
	switch {
	case protocol == GenerationProtocolSunoTask:
		if sunoTaskVersion(v) {
			return []string{"suno_music", "suno_lyrics"}
		}
		return nil
	case protocol == GenerationProtocolGenericTask:
		return pluginGenericTaskModels(v.Meta, v.Capabilities)
	case protocol == GenerationProtocolOpenAIResponses:
		return pluginResponsesTaskBinding(v.Meta, v.Capabilities).Models
	case protocol == GenerationProtocolOpenAIImage:
		return pluginImageTaskModels(v.Meta, v.Capabilities)
	case strings.HasPrefix(protocol, GenerationProtocolNativePrefix):
		if v.Meta.Key != strings.TrimPrefix(protocol, GenerationProtocolNativePrefix) {
			return nil
		}
		return pluginGenericTaskModels(v.Meta, v.Capabilities)
	default:
		return pluginVideoModels(v.Meta)
	}
}

func GenerationTaskSubmitModel(body []byte) (string, error) {
	if len(body) > MaxRequestPluginBody || !utf8.Valid(body) || !json.Valid(body) {
		return "", ErrGenerationDecode
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() || !newAPIProviderUnambiguousJSON(root, 0) {
		return "", ErrGenerationDecode
	}
	model := root.Get("model")
	if model.Type != gjson.String || model.Str == "" || len(model.Str) > 100 || strings.TrimSpace(model.Str) != model.Str {
		return "", ErrGenerationDecode
	}
	valid := true
	root.ForEach(func(field, value gjson.Result) bool {
		// Reject aliases that a decoder or vendor could interpret as a
		// different routing/operation field, and reject streaming explicitly.
		valid = (!strings.EqualFold(field.Str, "model") || field.Str == "model") &&
			(!strings.EqualFold(field.Str, "stream") || field.Str == "stream" && value.Type == gjson.False)
		return valid
	})
	if !valid {
		return "", ErrGenerationDecode
	}
	return model.Str, nil
}

func ValidGenerationTaskSubmitPlugin(key string) bool { return jsplugin.ValidPluginKey(key) }

func PrepareGenerationTaskSubmit(ctx context.Context, pluginKey string, body []byte) (context.Context, error) {
	if !ValidGenerationTaskSubmitPlugin(pluginKey) {
		return ctx, ErrGenerationDecode
	}
	scratch, release := pkghttputil.WithRequestBodyMemoryScope(ctx)
	defer release()
	if err := pkghttputil.ReserveRequestBodyMemory(scratch, int64(len(body))*12+(1<<20)); err != nil {
		return ctx, err
	}
	if _, err := GenerationTaskSubmitModel(body); err != nil {
		return ctx, err
	}
	return WithGenerationClientRequest(WithGenerationTaskSubmit(ctx, pluginKey), body)
}

// Recheck the selected revision and the public model before any hold or I/O.
func (s *GenerationService) validateTaskSubmitBinding(ctx context.Context, account *Account, model, protocol string, body []byte) error {
	key := GenerationTaskSubmitPlugin(ctx)
	if key == "" {
		return nil
	}
	clientModel, err := GenerationTaskSubmitModel(body)
	expectedProtocol := GenerationTaskSubmitProtocol(ctx)
	protocolAllowed := protocol == expectedProtocol || (key != "sunoapi" && protocol == GenerationProtocolGenericTask)
	if err != nil || clientModel != model || !protocolAllowed || !account.supportsPluginTaskModel(protocol, model) || !GenerationVideoRoutingPrepared(ctx) {
		return ErrGenerationDecode
	}
	version, err := s.plugins.repo.Get(ctx, account.NewAPIPluginVersionID())
	if err != nil {
		return err
	}
	if version == nil || !version.Enabled || !version.Capabilities.Task || version.Meta.Key != key || protocol == GenerationProtocolSunoTask && !sunoTaskVersion(version) {
		return ErrGenerationDecode
	}
	return nil
}

func (a *Account) supportsPluginTaskModel(protocol, model string) bool {
	switch {
	case protocol == GenerationProtocolSunoTask:
		mapped := a.GetMappedModel(model)
		return (mapped == "suno_music" || mapped == "suno_lyrics") && a.supportsDeclaredTaskModel(newAPIPluginSunoModelsKey, model)
	case protocol == GenerationProtocolGenericTask:
		return a.SupportsNewAPIGenericTaskModel(model)
	case protocol == GenerationProtocolOpenAIResponses:
		return a.SupportsNewAPIResponsesTaskModel(model)
	case protocol == GenerationProtocolOpenAIImage:
		return a.SupportsNewAPIImageTaskModel(model)
	case strings.HasPrefix(protocol, GenerationProtocolNativePrefix):
		return a.SupportsNewAPINativeRouteModel(strings.TrimPrefix(protocol, GenerationProtocolNativePrefix), model)
	default:
		return a.SupportsNewAPIVideoModel(model) && a.supportsNativeVideoProtocol(protocol, model)
	}
}

// Expose simple JSON music/lyrics inputs. Media, source tasks and callbacks
// are different operations and must not sneak in through vendor metadata.
func validSunoTaskBody(body []byte) bool {
	valid := true
	gjson.ParseBytes(body).ForEach(func(field, value gjson.Result) bool {
		switch field.Str {
		case "model", "stream":
		case "input", "prompt":
			valid = value.Type == gjson.String && len(value.Str) <= 8192
		case "metadata":
			valid = value.IsObject()
			value.ForEach(func(key, option gjson.Result) bool {
				switch key.Str {
				case "prompt", "gpt_description_prompt", "title", "tags":
					valid = valid && option.Type == gjson.String && len(option.Str) <= 8192
				case "mv":
					valid = valid && option.Type == gjson.String && len(option.Str) <= 128
				case "make_instrumental":
					valid = valid && (option.Type == gjson.True || option.Type == gjson.False)
				default:
					valid = false
				}
				return valid
			})
		default:
			valid = false
		}
		return valid
	})
	return valid
}

// Late rules may reject or adjust non-text settings, but cannot replace
// moderated text after the handler's audit has run.
func sunoTaskAuditTextUnchanged(before, after []byte) bool {
	if bytes.Equal(before, after) {
		return true
	}
	for _, field := range []string{"input", "prompt", "metadata.prompt", "metadata.gpt_description_prompt", "metadata.title", "metadata.tags"} {
		if gjson.GetBytes(before, field).Str != gjson.GetBytes(after, field).Str {
			return false
		}
	}
	return true
}
