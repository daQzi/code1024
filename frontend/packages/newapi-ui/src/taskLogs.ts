export interface TaskLog {
  id: string
  user_id?: number
  group_id: number
  group_name: string
  account_id?: number
  upstream_id?: string
  model: string
  platform: string
  protocol: string
  action: string
  plugin_key: string
  plugin_version: string
  source?: 'plugin' | 'native'
  monitor_status?: 'active' | 'complete' | 'review'
  monitor_error_code?: string
  status: string
  delivery_status: string
  billing_status: string
  progress: number
  hold_amount: string
  actual_amount: string | null
  error_code?: string
  error_message?: string
  created_at: string
  updated_at: string
  finished_at: string | null
}

export interface TaskLogDetail extends TaskLog {
  fields: { seconds?: string; size?: string; prompt?: string }
  events: { type: string; created_at: string }[]
  native_actions?: NativeTaskAction[]
}

export type NativeTaskAction = 'resume_poll' | 'retry_settlement'

export interface TaskLogPage { items: TaskLog[]; total: number; page: number; page_size: number; pages: number }
export interface TaskLogFilter {
  page: number
  page_size: number
  task_id?: string
  model?: string
  plugin?: string
  status?: string
  billing_status?: string
  review?: boolean
  group_id?: number
  account_id?: number
  start?: string
  end?: string
  scope?: string
}

// Transport is scoped to the authenticated host identity. Admin is a display
// flag only; the server must authorize every list, detail, download and action.
export interface TaskLogClient {
  listTaskLogs(params: TaskLogFilter, signal?: AbortSignal): Promise<TaskLogPage>
  getTaskLog(id: string, signal?: AbortSignal): Promise<TaskLogDetail>
  downloadTaskContent?(id: string, signal?: AbortSignal): Promise<Blob>
  // Optional legacy host action; the standalone console is read-only by default.
  maintainNativeTask?(id: string, action: NativeTaskAction, reason: string, signal?: AbortSignal): Promise<unknown>
}
