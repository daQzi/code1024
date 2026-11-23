package service

import (
	"context"
	"slices"

	"github.com/Wei-Shaw/sub2api/newapi-engine/jsplugin"
	engineplugin "github.com/Wei-Shaw/sub2api/newapi-engine/plugin"
	"github.com/Wei-Shaw/sub2api/newapi-engine/runner"
)

const NewAPIPluginBaseline = engineplugin.Baseline

type PluginHostCompatibility = engineplugin.Compatibility
type PluginFactory = engineplugin.Factory

func PluginFactories() ([]PluginFactory, error) {
	return engineplugin.NewManager(nil, nil, AssessPluginHost).Factories()
}

func AssessPluginHost(meta jsplugin.Meta, descriptor runner.Descriptor) PluginHostCompatibility {
	c := PluginHostCompatibility{Kinds: []string{}, Warnings: []string{}, Blockers: []string{}}
	for _, capability := range descriptor.RequiredCapabilities {
		if !runner.SupportsCapability(capability) {
			c.Blockers = append(c.Blockers, "required capability unavailable: "+capability)
		}
	}
	if descriptor.Task {
		if pluginHasAnyTaskRoute(meta, descriptor) {
			c.Kinds = append(c.Kinds, "task")
			if len(pluginNativeVideoBinding(meta)) > 0 {
				c.Warnings = append(c.Warnings, "native video ingress supports xAI generation/edit/extension and Ark create/query through durable Task drivers, validated origin tasks and pinned connection/login identity; generic meta.routes run under /v1/tasks/:key/native/*subpath with host-sanitized reply headers")
			}
			if len(pluginResponsesTaskBinding(meta, descriptor).Models) > 0 {
				c.Warnings = append(c.Warnings, "openai_responses Task protocol supports sync, background, SSE streaming, and GET /v1/responses/:response_id backed by durable tasks and host-owned artifacts")
			}
			if len(pluginImageTaskModels(meta, descriptor)) > 0 {
				c.Warnings = append(c.Warnings, "openai_image Task protocol supports JSON and multipart /v1/images/generations and /v1/images/edits with host-normalized data[] and optional b64_json inlining")
			}
			if slices.Contains(descriptor.Hooks, "buildCancelRequest") {
				c.Warnings = append(c.Warnings, "upstream task cancellation invokes buildCancelRequest/parseCancelResponse and only settles as canceled_upstream on explicit confirmation")
			}
			if slices.Contains(descriptor.Hooks, "login") || slices.Contains(descriptor.Hooks, "scheduled") {
				c.Warnings = append(c.Warnings, "video Task login/refresh/revoke/scheduled share encrypted account grants, durable maintenance, admission and restart identity checks; paid submissions are never automatically replayed after credential rejection")
			}
			if meta.Auth.Type == "oauth2_jwt" {
				c.Warnings = append(c.Warnings, "video Task oauth2_jwt resolves Google service accounts through bounded host-only RS256 exchange and a connection-scoped cache; only authHeader/projectId reach task hooks; new_api upstreams use their gateway key instead")
			}
		} else {
			c.Warnings = append(c.Warnings, "task plugin declares no models, protocols, or routes")
		}
	}
	if descriptor.Provider {
		if len(pluginProviderBinding(meta, descriptor).Routes) > 0 {
			c.Kinds = append(c.Kinds, "provider")
			c.Warnings = append(c.Warnings, "Provider supports OpenAI Chat/Responses, Anthropic Messages text JSON/SSE, Embeddings JSON, Images generations/edits (JSON and multipart form/file/$file), and Audio transcriptions/translations/speech; static key check/exchange with node-local token caching, one credential-rejection renewal, route authentication, prepareRequest, signRequest, plugin JSON/SSE/NDJSON conversion and stream-to-JSON body/chunks, plus up to 32 follow-ups with pinned state and current-step renewal; pinned RelayKit supports all 12 text converters across OpenAI Chat, Responses, Anthropic Messages and Gemini (including preset+plugin); presets require bounded JSON/state and real stream terminal evidence, and an Anthropic target needs an explicit output-token limit; Provider login/refresh/revoke and timer maintenance use encrypted account-bound state, database leases and explicit administrator actions, including durable cleanup on account removal/rebinding and native disable-event maintenance; priced tool hooks and supported vendor reports (including GLM, DashScope and Anthropic hosted tools) share the original ledger; credential hooks have bounded network access")
		} else {
			c.Warnings = append(c.Warnings, "Provider has no supported OpenAI Chat/Responses/Messages/Embeddings/Images/Audio route")
		}
	}
	if slices.Contains(descriptor.Hooks, "buildBalanceRequest") && slices.Contains(descriptor.Hooks, "parseBalance") {
		c.Warnings = append(c.Warnings, "saved plugin accounts support manual upstream balance queries with static keys or persisted Provider login credentials, quota windows and bounded follow-up/signing with one current-step credential renewal; observations never update the local wallet or pricing; timer maintenance may report its own balance")
	}
	if _, ok := meta.ModelListRoute(); ok {
		c.Warnings = append(c.Warnings, "saved plugin accounts support explicit model-list discovery with static keys or persisted Provider login credentials through none/plugin management routes with signing and bounded next/renewal; returned IDs never update mappings, capabilities, pricing or the public catalog automatically")
	}
	if descriptor.Transform {
		if slices.ContainsFunc(descriptor.Hooks, func(hook string) bool {
			return slices.Contains([]string{"ingress", "request", "outbound", "egress", "reply", "report"}, hook)
		}) {
			c.Kinds = append(c.Kinds, "transform")
			c.Warnings = append(c.Warnings, "request processing supports global/group/account Chat/Responses HTTP request, late account ingress, outbound, egress, reply and report, plus Task egress/report; early global/group ingress runs once, later attempts retain pinned rules; native outbound reads the actual converted protocol body while Provider outbound precedes plugin conversion; passthrough late ingress/outbound are unchecked and egress remains read-only; unsupported native protocols fail before sending; replies check actual downstream JSON/SSE and reports await confirmed settlement; native Messages/Gemini JSON/SSE through Anthropic, Gemini and Antigravity also support global/group/account reply/report rules, with actual client-format filtering, real upstream cancellation and preserved measured usage; native Anthropic Messages also supports request/late ingress/outbound/egress for API key, OAuth/setup-token, Vertex and Bedrock with patches before signing; Gemini/Antigravity other late stages remain unavailable; meta.scope.channelTypes matches mapped actual account identities; channel-scoped ingress waits for selection and does not repeat completed early ingress; Task late ingress precedes pricing; unmapped identities do not match; inline JSON attachment metadata/text, media.text and outbound JPEG/PNG/WebP reencoding are supported with parent-only media streams and sanitized audit; bounded read-only PDF/DOCX extraction and request-wide caching are available; stored-file/multipart tools and other protocol/WebSocket hooks remain unavailable; ingress/egress networking and reply networking require separate instance grants; request/outbound stay offline per the baseline contract; reports retain their separate network contract")
		} else {
			c.Warnings = append(c.Warnings, "this transform has no supported request processing execution path")
		}
	}
	if len(c.Kinds) == 0 {
		c.Blockers = append(c.Blockers, "no supported host execution path")
	}
	c.Runnable = len(c.Blockers) == 0
	return c
}

func (s *NewAPIPluginService) RegisterFactoryVersions(ctx context.Context) error {
	return s.pluginManager().RegisterFactoryVersions(ctx)
}
func (s *NewAPIPluginService) RestoreFactory(ctx context.Context, key string, actor *int64) (*NewAPIPluginVersion, error) {
	return s.pluginManager().RestoreFactory(ctx, key, actor)
}
