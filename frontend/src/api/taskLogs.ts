import { apiClient } from './client'

import type { NativeTaskAction, TaskLogDetail, TaskLogFilter, TaskLogPage } from '../../packages/newapi-ui/src/taskLogs'
export type { NativeTaskAction, TaskLog, TaskLogDetail, TaskLogFilter, TaskLogPage } from '../../packages/newapi-ui/src/taskLogs'

function base(admin: boolean) { return admin ? '/admin/task-logs' : '/task-logs' }
export async function listTaskLogs(admin: boolean, params: TaskLogFilter, signal?: AbortSignal) {
  return (await apiClient.get<TaskLogPage>(base(admin), { params, signal })).data
}
export async function getTaskLog(admin: boolean, id: string, signal?: AbortSignal) {
  return (await apiClient.get<TaskLogDetail>(`${base(admin)}/${encodeURIComponent(id)}`, { signal })).data
}
export async function downloadTaskContent(admin: boolean, id: string, signal?: AbortSignal) {
  return (await apiClient.get<Blob>(`${base(admin)}/${encodeURIComponent(id)}/content`, { responseType: 'blob', timeout: 300000, signal })).data
}
export async function maintainNativeTask(id: string, action: NativeTaskAction, reason: string, signal?: AbortSignal) {
  return (await apiClient.put<{ updated: boolean }>(`/admin/task-logs/${encodeURIComponent(id)}/native-maintenance`, { action, reason }, { signal })).data
}
