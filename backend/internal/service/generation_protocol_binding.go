package service

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
	"github.com/Wei-Shaw/sub2api/newapi-engine/taskprotocol"
)

const (
	newAPIPluginKeyKey               = "newapi_plugin_key"
	newAPIPluginGenericTaskModelsKey = "newapi_plugin_generic_task_models"
	newAPIPluginResponsesTaskKey     = "newapi_plugin_responses_task"
	newAPIPluginImageTaskKey         = "newapi_plugin_image_task"
	newAPIPluginNativeRoutesKey      = "newapi_plugin_native_routes"
)

const (
	OpenAIEndpointCapabilityPluginGenericTask   OpenAIEndpointCapability = "plugin_generic_task"
	OpenAIEndpointCapabilityPluginResponsesTask OpenAIEndpointCapability = "plugin_responses_task"
	OpenAIEndpointCapabilityPluginImageTask     OpenAIEndpointCapability = "plugin_image_task"
	OpenAIEndpointCapabilityPluginNativeRoutes  OpenAIEndpointCapability = "plugin_native_routes"
)

// NewAPIResponsesTaskBinding stores the pinned openai_responses Task protocol
// declaration on an account credential snapshot for scheduler filtering.
type NewAPIResponsesTaskBinding struct {
	Models   []string `json:"models,omitempty"`
	Supports []string `json:"supports,omitempty"`
}

// NewAPINativeRoutesBinding stores the pinned meta.routes declaration on an
// account credential snapshot for scheduler filtering and route matching.
type NewAPINativeRoutesBinding struct {
	Key    string           `json:"key,omitempty"`
	Models []string         `json:"models,omitempty"`
	Routes []jsplugin.Route `json:"routes,omitempty"`
}

func pluginResponsesTaskBinding(meta jsplugin.Meta, descriptor runner.Descriptor) NewAPIResponsesTaskBinding {
	if !descriptor.Task {
		return NewAPIResponsesTaskBinding{}
	}
	for _, protocol := range meta.Protocols {
		if protocol.Name != "openai_responses" {
			continue
		}
		models := slices.Clone(protocol.Models)
		if len(models) == 0 {
			models = slices.Clone(meta.Models)
		}
		supports := slices.Clone(protocol.Supports)
		if len(supports) == 0 {
			supports = []string{"sync"}
		}
		return NewAPIResponsesTaskBinding{Models: models, Supports: supports}
	}
	return NewAPIResponsesTaskBinding{}
}

func pluginImageTaskModels(meta jsplugin.Meta, descriptor runner.Descriptor) []string {
	if !descriptor.Task {
		return nil
	}
	for _, protocol := range meta.Protocols {
		if protocol.Name != "openai_image" {
			continue
		}
		if len(protocol.Models) > 0 {
			return slices.Clone(protocol.Models)
		}
		return slices.Clone(meta.Models)
	}
	return nil
}

func pluginNativeRoutesBinding(meta jsplugin.Meta, descriptor runner.Descriptor) NewAPINativeRoutesBinding {
	if !descriptor.Task || len(meta.Routes) == 0 {
		return NewAPINativeRoutesBinding{}
	}
	return NewAPINativeRoutesBinding{
		Key:    meta.Key,
		Models: slices.Clone(meta.Models),
		Routes: slices.Clone(meta.Routes),
	}
}

func pluginGenericTaskModels(meta jsplugin.Meta, descriptor runner.Descriptor) []string {
	if !descriptor.Task {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	add := func(list []string) {
		for _, item := range list {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	add(meta.Models)
	for _, protocol := range meta.Protocols {
		add(protocol.Models)
	}
	for _, route := range meta.Routes {
		add(route.Models)
	}
	return out
}

func pluginHasAnyTaskRoute(meta jsplugin.Meta, descriptor runner.Descriptor) bool {
	if !descriptor.Task {
		return false
	}
	return len(pluginVideoModels(meta)) > 0 ||
		sunoTaskManifest(meta, descriptor) ||
		len(pluginResponsesTaskBinding(meta, descriptor).Models) > 0 ||
		len(pluginImageTaskModels(meta, descriptor)) > 0 ||
		len(meta.Routes) > 0 ||
		len(meta.Models) > 0
}

func (a *Account) NewAPIPluginKey() string {
	if a == nil {
		return ""
	}
	return strings.TrimSpace(a.GetCredential(newAPIPluginKeyKey))
}

func (a *Account) NewAPIResponsesTaskBinding() NewAPIResponsesTaskBinding {
	var binding NewAPIResponsesTaskBinding
	if a == nil || !a.HasNewAPIPlugin() || a.Type != AccountTypeAPIKey {
		return binding
	}
	raw, _ := json.Marshal(a.Credentials[newAPIPluginResponsesTaskKey])
	_ = json.Unmarshal(raw, &binding)
	return binding
}

func (a *Account) NewAPINativeRoutesBinding() NewAPINativeRoutesBinding {
	var binding NewAPINativeRoutesBinding
	if a == nil || !a.HasNewAPIPlugin() || a.Type != AccountTypeAPIKey {
		return binding
	}
	raw, _ := json.Marshal(a.Credentials[newAPIPluginNativeRoutesKey])
	_ = json.Unmarshal(raw, &binding)
	return binding
}

func (a *Account) supportsDeclaredModelList(models []string, model string) bool {
	if strings.Contains(model, "*") || strings.TrimSpace(model) == "" || !a.HasNewAPIPlugin() || !a.isModelMappingSupported(model) {
		return false
	}
	if len(models) == 0 {
		return false
	}
	mapped := a.GetMappedModel(model)
	return slices.ContainsFunc(models, func(candidate string) bool {
		if candidate == "*" {
			_, explicit := a.GetModelMapping()[model]
			return explicit && pluginVideoModelMatches(candidate, mapped)
		}
		return pluginVideoModelMatches(candidate, mapped) || pluginVideoModelMatches(candidate, model)
	})
}

func (a *Account) SupportsNewAPIGenericTaskModel(model string) bool {
	return a.supportsDeclaredTaskModel(newAPIPluginGenericTaskModelsKey, model)
}

func (a *Account) SupportsNewAPIResponsesTaskModel(model string) bool {
	return a.supportsDeclaredModelList(a.NewAPIResponsesTaskBinding().Models, model)
}

func (a *Account) SupportsNewAPIResponsesTaskMode(mode string) bool {
	return slices.Contains(a.NewAPIResponsesTaskBinding().Supports, mode)
}

func (a *Account) SupportsNewAPIImageTaskModel(model string) bool {
	return a.supportsDeclaredTaskModel(newAPIPluginImageTaskKey, model)
}

func (a *Account) SupportsNewAPINativeRouteModel(pluginKey, model string) bool {
	binding := a.NewAPINativeRoutesBinding()
	if len(binding.Routes) == 0 {
		return false
	}
	if pluginKey != "" && binding.Key != "" && binding.Key != pluginKey {
		return false
	}
	if model == "" {
		return true
	}
	if a.SupportsNewAPIGenericTaskModel(model) {
		return true
	}
	return a.supportsDeclaredModelList(binding.Models, model)
}

func (a *Account) MatchNewAPINativeRoute(pluginKey, method, subpath string) (jsplugin.Route, map[string]string, bool) {
	binding := a.NewAPINativeRoutesBinding()
	if len(binding.Routes) == 0 {
		return jsplugin.Route{}, nil, false
	}
	if pluginKey != "" && binding.Key != "" && binding.Key != pluginKey {
		return jsplugin.Route{}, nil, false
	}
	return taskprotocol.MatchNativeRoute(binding.Routes, method, subpath)
}

// GenerationTaskCapabilityForProtocol maps a durable task protocol to the
// scheduler endpoint capability used during account selection.
func GenerationTaskCapabilityForProtocol(protocol string) OpenAIEndpointCapability {
	switch {
	case protocol == GenerationProtocolSunoTask:
		return OpenAIEndpointCapability("plugin_suno_task")
	case protocol == GenerationProtocolGenericTask:
		return OpenAIEndpointCapabilityPluginGenericTask
	case protocol == GenerationProtocolOpenAIResponses:
		return OpenAIEndpointCapabilityPluginResponsesTask
	case protocol == GenerationProtocolOpenAIImage:
		return OpenAIEndpointCapabilityPluginImageTask
	case strings.HasPrefix(protocol, GenerationProtocolNativePrefix):
		return OpenAIEndpointCapabilityPluginNativeRoutes
	default:
		return OpenAIEndpointCapability("plugin_task")
	}
}

func mergeDistinctModels(lists ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, list := range lists {
		for _, item := range list {
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}
