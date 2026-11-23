export type LocalizedText = string | Record<string, string>
export interface UsageFieldSchema {
  type?: string
  unit?: string
  unitLabel?: LocalizedText
  enum?: string[]
  enumLabels?: Record<string, LocalizedText>
  description?: LocalizedText
}
export interface UsageProfile {
  models: string[]
  schema: Record<string, UsageFieldSchema>
  examples?: { label: string; facts: Record<string, unknown> }[]
}
export interface PluginConfigField {
  type: 'string' | 'number' | 'integer' | 'boolean' | 'array' | 'object'
  title?: LocalizedText
  description?: LocalizedText
  format?: string
  default?: unknown
  enum?: (string | number)[]
  enumTitles?: LocalizedText[]
  minimum?: number
  maximum?: number
  properties?: Record<string, PluginConfigField>
  required?: string[]
}
export interface NewAPIPluginVersion {
  id: number
  key: string
  version: string
  sha256: string
  origin: string
  meta: {
    name: string
    name_i18n?: LocalizedText
    icon?: string
    sortPriority?: number
    fetchMode?: string
    channelTypes?: number[]
    allowedHosts?: string[]
    upstreams?: string[]
    routes?: { method: string; path: string; type: string; models?: string[] }[]
    usageSchema?: Record<string, UsageFieldSchema>
    usageProfiles?: UsageProfile[]
    usageExamples?: UsageProfile['examples']
    auth?: { type: string }
    optional?: boolean
    description?: LocalizedText
    models: string[]
    baseUrl?: string
    channelConfig?: PluginConfigField
    configSchema?: PluginConfigField
    protocols?: { name: string; models?: string[]; supports?: string[] }[]
    relay?: { incoming_path: string; incoming_methods?: string[]; upstream_path?: string; converter?: string }[]
    scope?: { models?: string[]; channelTypes?: number[] }
    author?: { name?: string; url?: string }
    website?: string
  }
  capabilities: { task: boolean; provider: boolean; transform: boolean; required_capabilities?: string[]; hooks?: string[] }
  compatibility: { runnable: boolean; kinds: string[]; warnings: string[]; blockers: string[] }
  enabled: boolean
  active: boolean
  created_at: string
}
export type PluginFactory = Pick<NewAPIPluginVersion, 'key' | 'version' | 'sha256' | 'meta' | 'capabilities' | 'compatibility'>
export interface NewAPIPluginCatalog { plugins: NewAPIPluginVersion[]; builtins: string[]; factories?: PluginFactory[]; baseline: string }

export interface PluginCenterInstallInput { source?: string; builtin?: string; expected_key?: string; allow_builtin_override?: boolean; marketplace?: { index_url: string; key: string; version: string; sha256: string } }

// The UI receives its transport and authorization boundary from the host.
export interface PluginCenterClient {
  list(signal?: AbortSignal): Promise<NewAPIPluginCatalog>
  versions(key: string, signal?: AbortSignal): Promise<NewAPIPluginVersion[]>
  install(input: PluginCenterInstallInput): Promise<NewAPIPluginVersion>
  activate(id: number, enabled: boolean): Promise<void>
  restore(key: string): Promise<NewAPIPluginVersion>
  remove?(id: number): Promise<PluginDeleteResult>
  dryRun?(id: number, input: PluginFixture, signal?: AbortSignal): Promise<PluginDryRunResult>
  pricing?(key: string, signal?: AbortSignal): Promise<PluginAccountPricingSummary[]>
}

export interface PluginDeleteResult {
  deleted_id: number
  key: string
  version: string
  fallback_version_id?: number
  removed_plugin: boolean
}
export interface PluginPriceRule {
  mode: string
  expression?: string
  unit?: string
  unit_price?: string
  source_currency?: string
  source_unit?: string
  source_unit_price?: string
  exchange_rate?: string
  markup?: string
  service_fee?: string
  minimum?: string
  maximum?: string
}
export interface PluginAccountPricingSummary {
  account_id: number
  account_name: string
  status: string
  schedulable: boolean
  version_id: number
  version: string
  pricing_rules: Record<string, PluginPriceRule>
}

export interface PluginFixture { source?: string; hook: string; args: unknown[] }
export interface PluginDeveloperTemplate { kind: 'ingress' | 'task' | 'provider'; source: string; fixture: PluginFixture }
export interface PluginDryRunResult { hook: string; sha256: string; duration_ms: number; offline: boolean; result: unknown; compatibility: NewAPIPluginVersion['compatibility'] }

export interface PluginSubmission extends Pick<NewAPIPluginVersion, 'key' | 'version' | 'sha256' | 'meta' | 'capabilities' | 'compatibility'> {
  id: number
  author_id: number
  source?: string
  readme: string
  status: 'draft' | 'submitted' | 'rejected' | 'published' | 'withdrawn'
  revision: number
  review_reason: string
  created_at: string
  updated_at: string
  published_at?: string
}
export interface PluginSubmissionPage { items: PluginSubmission[]; total: number; page: number; page_size: number; pages: number; namespace: string }
export type RegistryMode = 'mine' | 'review' | 'market'

export interface PluginMarketplaceSource { name: string; index_url: string }
export interface PluginMarketplacePin { index_url: string; key: string; version: string; sha256: string }

export interface PluginSource { id?: number; key: string; version: string; sha256: string; source: string }

export interface PluginCredentialRevocation {
  id: number; account_id: number; plugin_version_id: number
  status: 'pending' | 'completed' | 'protected' | 'attention'
  attempts: number; last_error: string; created_at: string; finished_at?: string
}

export type PluginTranslator = (key: string, values?: Record<string, unknown>) => string
export type RunPluginMutation = <T>(action: () => Promise<T>) => Promise<T>
export interface PluginSourceClient {
  getNewAPIPluginSource(id: number, signal?: AbortSignal): Promise<PluginSource>
  getPluginFactorySource(key: string, signal?: AbortSignal): Promise<PluginSource>
}
export interface PluginRegistryClient {
  listPluginSubmissions(mode: RegistryMode, page: number, status: string, signal?: AbortSignal): Promise<PluginSubmissionPage>
  getPluginSubmission(id: number, admin: boolean, signal?: AbortSignal): Promise<PluginSubmission>
  savePluginSubmission(input: { id?: number; revision?: number; source: string; readme: string }): Promise<PluginSubmission>
  transitionPluginSubmission(id: number, revision: number, action: string, reason: string, admin: boolean): Promise<void>
  installPluginSubmission(id: number, sha256: string): Promise<NewAPIPluginVersion>
  listPluginTemplates(signal?: AbortSignal): Promise<PluginDeveloperTemplate[]>
  dryRunPlugin(input: PluginFixture & { source: string }, signal?: AbortSignal): Promise<PluginDryRunResult>
}
export interface PluginMarketplaceClient {
  listPluginMarketplaceSources(signal?: AbortSignal): Promise<PluginMarketplaceSource[]>
  savePluginMarketplaceSources(sources: PluginMarketplaceSource[]): Promise<PluginMarketplaceSource[]>
  installNewAPIPlugin(input: PluginCenterInstallInput): Promise<NewAPIPluginVersion>
}
export interface PluginCleanupClient {
  listPluginCredentialRevocations(signal?: AbortSignal): Promise<PluginCredentialRevocation[]>
  retryPluginCredentialRevocation(id: number, signal?: AbortSignal): Promise<unknown>
}
