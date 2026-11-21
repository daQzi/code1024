import { apiClient } from '../client'

export type LocalizedText = string | Record<string, string>
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
    description?: LocalizedText
    models: string[]
    baseUrl?: string
    channelConfig?: PluginConfigField
    configSchema?: PluginConfigField
    protocols?: { name: string; models?: string[]; supports?: string[] }[]
  }
  capabilities: { task: boolean; provider: boolean; transform: boolean; required_capabilities?: string[] }
  enabled: boolean
  active: boolean
  created_at: string
}
export interface NewAPIPluginCatalog { plugins: NewAPIPluginVersion[]; builtins: string[]; baseline: string }
export async function listNewAPIPlugins(signal?: AbortSignal) {
  return (await apiClient.get<NewAPIPluginCatalog>('/admin/newapi-plugins', { signal })).data
}
export async function installNewAPIPlugin(input: { source?: string; builtin?: string }) {
  return (await apiClient.post<NewAPIPluginVersion>('/admin/newapi-plugins', input)).data
}
export async function listNewAPIPluginVersions(key: string) {
  return (await apiClient.get<NewAPIPluginVersion[]>(`/admin/newapi-plugins/${encodeURIComponent(key)}/versions`)).data
}
export async function activateNewAPIPlugin(id: number, enabled: boolean) {
  await apiClient.put(`/admin/newapi-plugins/${id}/activation`, { enabled })
}

export interface GenerationReviewTask {
  id: string
  model: string
  account_id: number
  user_id: number
  status: string
  billing_status: string
  hold_amount: string
  upstream_id: string
  error_code: string
  created_at: string
}
export type GenerationReviewAction = 'resume_poll' | 'refund' | 'settle'
export async function listGenerationReviews(signal?: AbortSignal) {
  return (await apiClient.get<GenerationReviewTask[]>('/admin/newapi-plugins/tasks/review', { signal })).data
}
export async function reconcileGeneration(id: string, input: { action: GenerationReviewAction; upstream_id?: string; amount?: string; reason: string }) {
  await apiClient.put(`/admin/newapi-plugins/tasks/${encodeURIComponent(id)}/reconcile`, input)
}
