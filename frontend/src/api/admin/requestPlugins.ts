import { apiClient } from '../client'

export interface RequestPluginInstance {
  id: number
  name: string
  plugin_version_id: number
  plugin_key: string
  plugin_version: string
  enabled: boolean
  network_enabled?: boolean
  reply_network_enabled?: boolean
  scope: 'global' | 'group' | 'account'
  group_ids: number[]
  account_ids?: number[]
  models: string[]
  position: number
  formats?: string[]
  stages: string[]
  revision: number
  updated_at: string
  config?: Record<string, unknown>
}
export interface RequestPluginRecord {
  instance_id: number
  version_id: number
  stage: string
  action: 'started' | 'continue' | 'reject' | 'error' | 'skipped' | 'unchecked'
  error_class?: string
  duration_ms: number
  effects: number
  findings: number
  account_id?: number
  attempt?: number
  attachments?: {
    kind: 'image' | 'audio' | 'video' | 'document' | 'other'
    mime?: string
    status: 'checked' | 'truncated' | 'unchecked'
    reason?: string
    rewritten?: boolean
  }[]
  more_attachments?: number
  media?: {
    index: number
    from: { mime: string; bytes: number }
    to: { mime: string; bytes: number }
    lossless: boolean
  }[]
}
export interface RequestPluginRun {
  id: number
  request_id: string
  user_id: number
  group_id: number
  model: string
  format: string
  dry_run: boolean
  records: RequestPluginRecord[]
  created_at: string
}
export interface RequestPluginOutcome {
  body: unknown
  headers?: Record<string, string>
  records: RequestPluginRecord[]
  rejected: boolean
  error_class?: string
}

export async function listRequestPlugins(signal?: AbortSignal) {
  return (await getRequestProcessingStatus(signal)).items
}
export async function getRequestProcessingStatus(signal?: AbortSignal) {
  return (await apiClient.get<{ items: RequestPluginInstance[]; reply_audit_failures: number; report_stats?: { accepted: number; completed: number; dropped: number; failed: number } }>('/admin/request-processing', { signal })).data
}
export async function saveRequestPlugin(input: Partial<RequestPluginInstance>, signal?: AbortSignal) {
  return (await apiClient.post<RequestPluginInstance>('/admin/request-processing', input, { signal })).data
}
export async function getRequestPluginConfig(id: number, signal?: AbortSignal) {
  return (await apiClient.post<{ config: Record<string, unknown> }>(`/admin/request-processing/${id}/config`, {}, { signal })).data.config
}
export async function dryRunRequestPlugin(input: { plugin_version_id: number; config: Record<string, unknown>; body: unknown; format: string; stage?: string }, signal?: AbortSignal) {
  return (await apiClient.post<RequestPluginOutcome>('/admin/request-processing/dry-run', input, { signal })).data
}
export async function listRequestPluginRuns(page: number, request_id: string, signal?: AbortSignal) {
  return (await apiClient.get<{ items: RequestPluginRun[]; total: number }>('/admin/request-processing/runs', { params: { page, page_size: 20, request_id }, signal })).data
}

export interface ModelModifierSettings {
  exemptions: string[]
  use_startup: boolean
  revision: string
  startup_exemptions?: string[]
}
export async function getModelModifierSettings(signal?: AbortSignal) {
  return (await apiClient.get<ModelModifierSettings>('/admin/request-processing/model-modifiers', { signal })).data
}
export async function saveModelModifierSettings(input: ModelModifierSettings, signal?: AbortSignal) {
  return (await apiClient.put<ModelModifierSettings>('/admin/request-processing/model-modifiers', input, { signal })).data
}
