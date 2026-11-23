import { apiClient } from '../client'
import type { ProviderToolPriceSettings } from '../../../packages/newapi-ui/src/toolPrices'

export async function getProviderToolPrices(signal?: AbortSignal) {
  return (await apiClient.get<ProviderToolPriceSettings>('/admin/newapi-plugins/provider-tool-prices', { signal })).data
}
export async function saveProviderToolPrices(input: ProviderToolPriceSettings, signal?: AbortSignal) {
  return (await apiClient.put<ProviderToolPriceSettings>('/admin/newapi-plugins/provider-tool-prices', input, { signal })).data
}

import type { LocalizedText, NewAPIPluginCatalog, NewAPIPluginVersion, PluginMarketplaceSource, PluginMarketplacePin, PluginSource, PluginCredentialRevocation } from '../../../packages/newapi-ui/src/types'
export type { LocalizedText, PluginConfigField, NewAPIPluginVersion, PluginFactory, NewAPIPluginCatalog, PluginMarketplaceSource, PluginMarketplacePin, PluginSource, PluginCredentialRevocation } from '../../../packages/newapi-ui/src/types'

export async function listNewAPIPlugins(signal?: AbortSignal) {
  return (await apiClient.get<NewAPIPluginCatalog>('/admin/newapi-plugins', { signal })).data
}
export interface PluginLoginField { name: string; label: LocalizedText; type: string; required: boolean; placeholder?: LocalizedText; description?: LocalizedText }
export interface PluginCredentialView {
  account_id: number; plugin_version_id: number; revision: number; status: string
  credential?: { plugin: string; label: string; account_id: string; expires_at: number; needs_login: boolean }
  login?: { flow: string; type: string; url?: string; hint?: LocalizedText; user_code?: string; verification_uri?: string; verification_uri_complete?: string; interval?: number; expires_at: number; fields?: PluginLoginField[] }
  balance?: { amount: number | null; currency: string; unlimited: boolean }
  notify?: LocalizedText; last_run_at?: string; last_error?: string
}
export interface PluginLoginInput { flow?: string; callback_url?: string; fields?: Record<string, string>; poll?: boolean }
export async function getPluginCredential(id: number, signal?: AbortSignal) {
  return (await apiClient.get<PluginCredentialView>(`/admin/newapi-plugins/accounts/${id}/credential`, { signal })).data
}
export async function managePluginCredential(id: number, action: string, revision: number, input: PluginLoginInput, signal?: AbortSignal) {
  return (await apiClient.post<PluginCredentialView>(`/admin/newapi-plugins/accounts/${id}/credential`, { action, revision, input }, { signal })).data
}
export async function listPluginMarketplaceSources(signal?: AbortSignal) {
  return (await apiClient.get<PluginMarketplaceSource[]>('/admin/newapi-plugins/marketplace/sources', { signal })).data
}
export async function savePluginMarketplaceSources(sources: PluginMarketplaceSource[]) {
  return (await apiClient.put<PluginMarketplaceSource[]>('/admin/newapi-plugins/marketplace/sources', { sources })).data
}
export async function installNewAPIPlugin(input: { source?: string; builtin?: string; allow_builtin_override?: boolean; marketplace?: PluginMarketplacePin }) {
  return (await apiClient.post<NewAPIPluginVersion>('/admin/newapi-plugins', input)).data
}
export async function restorePluginFactory(key: string) {
  return (await apiClient.post<NewAPIPluginVersion>(`/admin/newapi-plugins/${encodeURIComponent(key)}/restore`)).data
}
export async function listNewAPIPluginVersions(key: string, signal?: AbortSignal) {
  return (await apiClient.get<NewAPIPluginVersion[]>(`/admin/newapi-plugins/${encodeURIComponent(key)}/versions`, { signal })).data
}
export async function getNewAPIPluginVersion(id: number, signal?: AbortSignal) {
  return (await apiClient.get<NewAPIPluginVersion>(`/admin/newapi-plugins/versions/${id}/info`, { signal })).data
}
export async function getNewAPIPluginSource(id: number, signal?: AbortSignal) {
  return (await apiClient.get<PluginSource>(`/admin/newapi-plugins/versions/${id}/source`, { signal })).data
}
export async function getPluginFactorySource(key: string, signal?: AbortSignal) {
  return (await apiClient.get<PluginSource>(`/admin/newapi-plugins/factories/${encodeURIComponent(key)}/source`, { signal })).data
}
export async function activateNewAPIPlugin(id: number, enabled: boolean) {
  await apiClient.put(`/admin/newapi-plugins/${id}/activation`, { enabled })
}

export interface NewAPIPluginBalance {
  account_id: number
  plugin_version_id: number
  plugin_key: string
  plugin_version: string
  fetched_at: string
  balance: {
    amount: number | null
    currency: string
    unlimited: boolean
    windows: { name: LocalizedText; used: number; limit: number; unit?: string; resetAt?: number }[]
  }
}
export async function queryNewAPIPluginBalance(accountID: number, signal?: AbortSignal) {
  return (await apiClient.post<NewAPIPluginBalance>(`/admin/newapi-plugins/accounts/${accountID}/balance`, {}, { signal })).data
}

export interface NewAPIPluginModels { account_id: number; plugin_version_id: number; models: string[]; fetched_at: string }
export async function queryNewAPIPluginModels(accountID: number, signal?: AbortSignal) {
  return (await apiClient.post<NewAPIPluginModels>(`/admin/newapi-plugins/accounts/${accountID}/models`, {}, { signal })).data
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

export async function listPluginCredentialRevocations(signal?: AbortSignal) {
  return (await apiClient.get<PluginCredentialRevocation[]>('/admin/newapi-plugins/credential-cleanup', { signal })).data
}
export async function retryPluginCredentialRevocation(id: number, signal?: AbortSignal) {
  return (await apiClient.post(`/admin/newapi-plugins/credential-cleanup/${id}/retry`, {}, { signal })).data
}
