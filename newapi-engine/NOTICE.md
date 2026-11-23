# New API compatibility runtime

Upstream: <https://github.com/QuantumNous/new-api>

Revision: `48f9df96ea7c68eae04a0f3687869f232f48929f`

Runtime and contract sources now live in the independent `newapi-engine/`
module. Host integrations and compatibility documentation remain under
`backend/internal/newapi/`. Directory extraction does not change the pinned
baseline, bundled script bytes, or applicable licenses.

The `jsplugin` implementation and its upstream regression tests, `contract`
channel settings, `contracttypes` endpoint definitions, `builtin` scripts,
and the `requestpatch` implementation and regression tests are derived from
this revision. They are licensed under **AGPL-3.0-or-later**;
see `LICENSE`. The surrounding repository's LGPL notice does not replace
these terms. Preserve the applicable notices and source-offer obligations
when distributing or operating a combined application.

Local adaptations replace New API host imports with Sub2API bridges for JSON,
logging and immutable limits. No New API database, account, balance or server
implementation is imported. `contract` contains the upstream RelayKit route
contract; downstream protocol conversions must continue through shared codecs.

Provider preset conversions use the upstream standalone module directly:
`github.com/QuantumNous/new-api/relaykit` at
`v0.3.8-0.20261120084210-48f9df96ea7c`, the exact baseline revision above.
`protocolconv` is a local bounded host bridge, not a reimplementation of its
protocol mappings. It unwraps the public Responses stream envelope, uses the
shared canonical usage merge, and requires real terminal evidence before the
upstream finalizer; an interrupted generation is not a successful response.

`requestpatch` uses the same local JSON/limits bridges. Its `ApplyWithLimit`
entry point lets a host call impose a smaller body limit without changing
global settings. JSON Patch copy operations check the prospective size before
cloning; mutations and final encoding also enforce that per-call limit. The
original `Apply` entry point retains the pinned contract's default limit.
The request-processing host enables effects by stage: JSON patches at ingress
and outbound, reasoning controls at request, business header patches at
outbound, inline text replacement at ingress/outbound, and image re-encoding
at outbound. Bodies retain the host's 1 MiB limit and routing, billing and
credential protections. Other effects and protocols are not enabled merely
by importing the patch library.

The local engine checks cancellation before and after admission, so a request
already cancelled cannot randomly win a ready semaphore and execute a hook.

The included scripts establish a pinned compatibility baseline. A successful
compile or fixture is not evidence that a live upstream, its credentials,
billing, storage, or every protocol is supported by the Sub2API host.

`modelmodifier` follows `relay/helper/model_modifier.go` and
`setting/reasoning/suffix.go` at the pinned revision, under AGPL-3.0-or-later.
It uses the same RelayKit parser and reasoning intent. Local additions bound
model/body complexity and use an atomically replaced immutable exemption policy,
with bounded database refresh and administrator CAS edits; the host
reconciles explicit request-plugin controls before conversion and billing.

`mediahost` follows `relay/requestplugin/{media,attachments,plan,extract,pdf}.go` at the
same pinned revision under AGPL-3.0-or-later. Image format/resize/animation
handling is adapted from that source. Local additions provide scoped parent
process media callbacks, bounded text and streamed fetch expansion, atomic
effect application, explicit image/document-memory admission, request-scoped
extraction caching, cancellation-aware PDF/DOCX readers and sanitized audit UI.
The PDF reader uses the same pinned `github.com/ledongthuc/pdf` revision as
the baseline. These codecs and their runner regressions now live in the
independent engine's `mediahost` package. A trusted construction-time admission
callback connects them to Sub2API's shared request-memory budget; bounded
process-wide image/document admission remains in the engine. The host bridge
does not copy parsing rules. This does not implement the multipart attachment
host or grant arbitrary file access.

`internal/pkg/relaymetrics` follows this baseline's
`pkg/perf_metrics/outcome.go` classification semantics under AGPL-3.0-or-later.
It observes bounded HTTP/Responses WebSocket terminal evidence. The local
after-write observer, per-connection bounded slots and PostgreSQL recorder do
not import New API's accounting, account-health or unbounded hot-bucket code.

The `sub2api-dreamina` and `sub2api-official5` builtins are code1024 adapters,
written against the public channel API documentation on 2026-11-21. They are
not scripts from the pinned New API revision and use AGPL-3.0-or-later as well.
The `sub2api-openai-video` builtin is a local generic OpenAI Videos adapter
under the same license. Its Task hook and video wire contracts follow the
pinned `sora` builtin, with explicit account model mapping, configurable
multipart/JSON submission, and base-path normalization. It does not alter
the original Sora script or imply support for every vendor video protocol.
The `sub2api-openai-relay` builtin is a code1024 native OpenAI Provider example
under the same license, not part of the pinned upstream scripts. The local
Provider host follows the advancedcustom route selection, authentication and
base-path joining contract while retaining Sub2API scheduling and accounting.
The Provider conversion host also follows the baseline's typed prepare/convert
contracts, billing-field protection and SSE framing. This is a local host
integration, not the upstream server implementation. Its supported subset and
limits are listed in HOST_COMPATIBILITY.md and docs/customization/PROVIDER_PLUGINS.md
(the latter relative to the repository root).
The local static-key lifecycle follows the pinned service/provider_plugin.go
and relay/channel/providerplugin contracts for checking, exchanged-token cache
identity, conditional invalidation and one rejection-driven renewal. It uses
Sub2API account/proxy persistence and the existing bounded subprocess HTTP host;
it does not import the upstream account database or OAuth lifecycle.
The bounded JSON follow-up host follows `relay/channel/providerplugin/chain.go`
at the same fixed revision: per-step auth/signing, explicit state, 32 follow-ups,
first-step-only preparation and one renewal of the rejected current step. The
local integration adds shared memory admission and a cancellable two-minute
handshake budget, removed before the final SSE stream. It does not add the
upstream server's file, WebSocket or wallet implementation.
The local JSON stream collector also follows the body/chunks contract in the
pinned `relay/channel/providerplugin/stream.go`: convertStreamEvent supplies
the complete client JSON, or JSON chunks until clean EOF. Sub2API adds a
1 MiB cumulative limit, its existing two-minute chain budget and shared memory
admission. It does not add vendor-specific delta assembly or binary delivery.
It refuses automatic credential replay after a mutating streamed request has
already emitted vendor events, preventing an ambiguous paid generation retry.

`provider.ToolUsage` follows the per-hook delta accumulation, explicit-zero
override and 10,000-per-key ceiling in the pinned
`relay/channel/providerplugin/adaptor.go` and `relay/common/tool_usage.go`,
under AGPL-3.0-or-later. `provider.PriceBook` follows the longest-model-prefix
lookup in `setting/operation_setting/tools.go`, but deliberately does not seed
vendor prices or mix vendor list currencies. Operator prices are explicit USD.
`provider.ProtocolTools` follows the actual-call and reserved-name semantics in
`relay/channel/openai/relay-openai.go`, `relay_responses.go` and the common tool
counter; bounded identity storage and Responses snapshot deduplication are local
adaptations. The Sub2API settings CAS/cache, admin UI and single-ledger surcharge
are local host integrations, not the upstream server's settings or wallet.
The Task runner's ordered-body bridge follows the same pinned
`relay/channel/task/jsplugin/adaptor.go` and `docs/plugin-api/v1.md`:
decoder input and requestBody are opt-in ordered JSON, request descriptor
bodies use JSON.stringify only with json-order@1, and string bodies are wire
text. Local migration 278 keeps immutable decoder text alongside JSONB so
background reloads preserve order; pre-migration order cannot be recovered.
The bridge also keeps state/data as JSON values across the local RPC boundary.
Corresponding source for this distribution is available at
<https://github.com/daQzi/code1024> (use the matching release tag).

`task/embedded_content.go` follows the pinned `controller/video_proxy.go`
operation/contentPath/Base64 contract. The Sub2API host retains the input under
its shared memory admission and fixed download slots; standard-library base64
decoders validate/count without allocating the decoded movie. This does not
enable the pinned server's other native routes, media tools or redirect trust.

The frontend `utils/pluginMarketplace.ts` follows the pinned
`web/src/features/task-plugins/lib/marketplace.ts` index v1 contract under
AGPL-3.0-or-later. The Vue UI, browser limits and Sub2API settings/install
bridges are local integrations. They do not import the upstream account or
marketplace backend and do not establish trust in downloaded source.

The line comparison in frontend `utils/pluginSourceDiff.ts` is adapted from
the pinned `web/src/features/task-plugins/components/source-diff.tsx` under
AGPL-3.0-or-later. Its bounded flat table and line numbers, the Vue source
viewer and the administrator artifact APIs are local integrations. Viewing
source never evaluates uploaded scripts or changes an account's pinned version.

The bounded Task report result projection in `../service/task_report_record.go`
is adapted from the same revision, `relay/requestplugin/task_report.go`, under
AGPL-3.0-or-later. Its queue, persistence and transport are Sub2API host code.

The HTTP response capture, report record and response/event projection in
`../service/request_plugin_report.go` follow the pinned
`relay/requestplugin/report.go` under AGPL-3.0-or-later. The bounded executor,
account placement, billing completion rendezvous and overload handling are
local host integrations. They do not import New API's account or billing store.

The Provider Transform host in `../service/request_plugin_provider.go` follows
the pinned `relay/requestplugin/plan.go` and `relay/requestplugin/relay.go`
stage order and request summary behavior under AGPL-3.0-or-later. Scope, audit,
resource admission and subprocess bridges are local integrations.

`paramoverride/override.go`, its header bridge and pure DSL regression tests are
adapted under AGPL-3.0-or-later from the pinned `relay/common/override.go`,
`relay/common/request_plugin.go`, `relay/common/relay_info.go`,
`relay/common/override_test.go` and `relay/requestplugin/param_override.go`.
The worker registration, bounds and allocation guards are local host code;
server RelayInfo, account storage and raw body operation audit are omitted.
The collection helper dependency is pinned to the reference's samber/lo v1.53.0.

`newapi-engine/replyfilter/reply.go` is adapted under AGPL-3.0-or-later from the pinned
`relay/requestplugin/reply.go`: UTF-16 masks/hold, protocol text fields and
cross-event full-text rewriting. The local host provides bounded subprocess
execution, Provider account placement and aggregate audit. Public error text
is fixed, incomplete frames are not flushed unchecked, and streaming stops do
not fabricate a successful terminator. Private Realtime helpers do not expose
a WebSocket host entry point; see HOST_COMPATIBILITY.md for actual coverage.
The independent reply filter uses net/http and a structural response-writer
interface; its local bounded adapter adds no framework dependency. The host
keeps a type/function bridge at backend/internal/newapi/replyfilter.
The native HTTP wrapper in backend/internal/service/request_plugin_native_reply.go and
global/group/account placement are Sub2API integrations. They wrap the native
gateway's actual Chat/Responses output without replacing protocol conversion,
and preserve measured usage and the handler's accounting context on a stop.

The manual plugin account balance host in
`../service/newapi_plugin_balance.go` follows the pinned
`relay/channel/providerplugin/management.go` management contract under
AGPL-3.0-or-later. Its saved-account admission, concurrency, HTTP constraints,
metadata endpoint and administration UI are Sub2API integrations.

The model-list management host in `../service/newapi_plugin_models.go` follows
the same pinned `providerplugin/management.go` under AGPL-3.0-or-later.
The ID-only projection, bounds, saved-account query and UI are local code.

The native request wrapper in `../service/request_plugin_native_request.go` and
its actual Chat/Responses outbound integration are local host code. Scoped
request-stage order follows the pinned `relay/requestplugin/plan.go`; native
protocol conversion, credentials, accounting and account headers remain owned
by Sub2API. Unsupported native protocols are rejected before HTTP submission
when a matching attempt-stage rule requires inspection.

The mapped channel scope and early/late ingress bookkeeping in
`../service/request_plugin_scope.go` follow the pinned
`relay/requestplugin/plan.go` matching and ingressRan semantics. Mapping to
Sub2API account identities and Task admission before its existing pricing
and ledger transaction are local integrations.

The Provider credential lifecycle host in `../service/newapi_provider_lifecycle.go`
and `../service/plugin_credential_maintenance.go` follows the pinned
`service/provider_plugin.go`, `provider_plugin_login.go` and
`provider_plugin_scheduled.go` contracts under AGPL-3.0-or-later. The
account-bound encrypted state, SQL leases, administrator session binding and
Vue forms are Sub2API integrations. Optional RawJSON `LoginStep.Session` and
`ScheduledResult.Credential` fields omit absent values over local typed RPC
while preserving explicit JSON null, as required by the original contract.

`taskprice` adapts the pinned `pkg/billingexpr/compile.go`, `run.go` and
`task_usage_test.go` Task USD semantics under AGPL-3.0-or-later. It uses
`github.com/expr-lang/expr` v1.17.8 (MIT), with a deliberately smaller scalar
environment and strict AST, cache and memory limits. It is not the full
New API billingexpr runtime. Financial persistence, immutable usage snapshots
and account/user reservation settlement remain Sub2API host integrations.
`taskprice/schema.go` adapts the same revision's
`setting/billing_setting/tiered_billing.go` Task schema smoke vectors, with a
64-vector budget applied only to referenced facts. Account model/profile
selection and unchanged legacy-rule handling are host integrations. Sampling
is not exhaustive validation and does not replace real-fact or hold checks.
`taskprice/clock.go` follows that revision's `pkg/billingexpr/run.go` time
probe names and invalid-zone UTC fallback. Admission-time and UTC-offset
snapshots, bounded calendar checks and restart-stable settlement are host
extensions; time probes never consult the settlement worker's wall clock.

`service/generation_credentials.go` follows the same pinned
`relay/channel/task/jsplugin/credential.go` managed-grant context, headers,
query and signing contract under AGPL-3.0-or-later. Sub2API adds immutable
non-secret task login identities, an anonymous-login identity, transactional
admission checks and parent-login isolation through its existing account
credential store. No tokens enter task snapshots or public task logs.

`../service/generation_jwt.go` follows the same pinned Task `auth.go`
RS256/Google service-account contract under AGPL-3.0-or-later. It reuses local
service-account parsing, bounded token-cache admission and account proxy HTTP.
Sub2API pins the public Google token endpoint, disables redirects, bounds token
responses, and never passes the service-account key to ordinary Task hooks.

The Sora remix host follows `relay/relay_task.go` (`ResolveOriginTask`) at the
same pinned revision. It retains the original Sora script and adds Sub2API
ownership, immutable connection/login identity, replay and funding checks.

`provider/vendor_tools.go` follows the same revision's Anthropic
`relay/channel/claude/relay-claude.go` and xAI `relay/channel/xai/web_search.go`
under AGPL-3.0-or-later. Local integration preserves cumulative vendor counts
before preset conversion and overlays explicit plugin facts last. Strict
integer/range/ambiguity validation and authoritative present-zero handling
are host additions; no vendor prices or currency defaults are copied.

`provider/gemini_tools.go` adapts `geminiGroundingCounter` from the pinned
`relay/channel/gemini/relay-gemini.go` under AGPL-3.0-or-later. Query evidence
is bounded and hashed in memory; both baseline metrics need explicit host
prices. Synthesized OpenAI search items are excluded from duplicate billing.
These counts are observed queries, not guaranteed vendor invoice quantities.

The same vendor observer also follows the pinned `relay/channel/ali/web_search.go`
and `relay/channel/openai/web_search.go` for DashScope and Azure report namespaces
and billing keys. Conflicting search namespaces, changes within a request and
invalid numeric reports are rejected rather than charging multiple aliases.
