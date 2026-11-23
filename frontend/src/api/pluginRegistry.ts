import { apiClient } from './client'
import type { NewAPIPluginVersion } from './admin/newapiPlugins'
import type { PluginFixture, PluginDeveloperTemplate, PluginDryRunResult, PluginSubmission, PluginSubmissionPage, RegistryMode } from '../../packages/newapi-ui/src/types'
export type { PluginFixture, PluginDeveloperTemplate, PluginDryRunResult, PluginSubmission, PluginSubmissionPage, RegistryMode } from '../../packages/newapi-ui/src/types'
export async function listPluginSubmissions(mode: RegistryMode, page = 1, status = 'submitted', signal?: AbortSignal) {
  const path = mode === 'review' ? '/admin/plugin-registry' : mode === 'market' ? '/plugin-registry/market' : '/plugin-registry'
  return (await apiClient.get<PluginSubmissionPage>(path, { params: { page, page_size: 20, status }, signal })).data
}
export async function getPluginSubmission(id: number, admin: boolean, signal?: AbortSignal) {
  return (await apiClient.get<PluginSubmission>(`${admin ? '/admin' : ''}/plugin-registry/${id}`, { signal })).data
}
export async function savePluginSubmission(input: { id?: number; revision?: number; source: string; readme: string }) {
  return (await apiClient.post<PluginSubmission>('/plugin-registry', input)).data
}
export async function transitionPluginSubmission(id: number, revision: number, action: string, reason: string, admin: boolean) {
  await apiClient.post(`${admin ? '/admin' : ''}/plugin-registry/${id}/transition`, { revision, action, reason })
}
export async function installPluginSubmission(id: number, sha256: string) {
  return (await apiClient.post<NewAPIPluginVersion>(`/admin/plugin-registry/${id}/install`, { sha256 })).data
}
export async function listPluginTemplates(signal?: AbortSignal) {
  return (await apiClient.get<PluginDeveloperTemplate[]>('/plugin-registry/templates', { signal })).data
}
export async function dryRunPlugin(input: PluginFixture & { source: string }, signal?: AbortSignal) {
  return (await apiClient.post<PluginDryRunResult>('/plugin-registry/dry-run', input, { signal })).data
}
