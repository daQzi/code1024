<template>
    <div class="task-log-console table-page-layout flex h-full min-h-[32rem] flex-col gap-4">
      <div class="flex shrink-0 flex-wrap items-center justify-between gap-3">
        <div class="flex gap-2">
          <button v-for="option in tabs" :key="option" type="button" class="btn btn-sm" :class="tab === option ? 'btn-primary' : 'btn-secondary'" :aria-pressed="tab === option" @click="tab = option">{{ t(`taskLogs.tabs.${option}`) }}</button>
        </div>
        <div class="flex items-center gap-3 text-sm">
          <label class="flex items-center gap-2 text-gray-500 dark:text-dark-400"><input v-model="autoRefresh" type="checkbox" class="rounded" />{{ t('taskLogs.autoRefresh') }}</label>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="load()">{{ t('common.refresh') }}</button>
        </div>
      </div>
      <slot v-if="admin && tab === 'review'" name="review" />
      <template v-else>
        <form class="card shrink-0 space-y-3 p-4" @submit.prevent="applyFilters">
          <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <label><span class="input-label">{{ t('taskLogs.taskId') }}</span><input v-model.trim="filters.task_id" class="input" maxlength="512" :placeholder="t('taskLogs.taskIdHint')" /></label>
            <label><span class="input-label">{{ t('taskLogs.model') }}</span><input v-model.trim="filters.model" class="input" maxlength="256" /></label>
            <div class="min-w-0"><span class="input-label">{{ t('common.status') }}</span><HostSelect v-model="filters.status" :select-component="selectComponent" :label="t('common.status')" :options="statusOptions" /></div>
            <div class="flex flex-wrap items-end gap-2">
              <label class="flex w-full items-center gap-2 text-sm text-gray-500 dark:text-dark-400"><input v-model="filters.review" type="checkbox" class="rounded" />{{ t('taskLogs.reviewOnly') }}</label>
              <button type="submit" class="btn btn-primary btn-sm" :disabled="loading">{{ t('common.search') }}</button>
              <button type="button" class="btn btn-secondary btn-sm" @click="reset">{{ t('common.reset') }}</button>
              <button type="button" class="btn btn-ghost btn-sm" :aria-expanded="showAdvancedFilters" @click="showAdvancedFilters = !showAdvancedFilters">
                {{ t('common.filter') }}<span v-if="advancedFilterCount > 0" class="badge badge-primary ml-1">{{ advancedFilterCount }}</span>
              </button>
            </div>
          </div>
          <div v-show="showAdvancedFilters" class="grid gap-3 border-t border-gray-100 pt-3 dark:border-dark-700 sm:grid-cols-2 lg:grid-cols-4">
            <label><span class="input-label">{{ t('taskLogs.plugin') }}</span><input v-model.trim="filters.plugin" class="input" maxlength="256" /></label>
            <div class="min-w-0"><span class="input-label">{{ t('taskLogs.billing') }}</span><HostSelect v-model="filters.billing_status" :select-component="selectComponent" :label="t('taskLogs.billing')" :options="billingOptions" /></div>
            <label><span class="input-label">{{ t('taskLogs.start') }}</span><input v-model="start" type="datetime-local" class="input" /></label>
            <label><span class="input-label">{{ t('taskLogs.end') }}</span><input v-model="end" type="datetime-local" class="input" /></label>
          </div>
        </form>
        <p class="shrink-0 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('taskLogs.coverage') }}</p>
        <p v-if="error" role="alert" class="shrink-0 rounded-xl border border-red-200 bg-red-50/90 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">{{ error }}</p>
        <div class="card flex min-h-0 flex-1 flex-col overflow-hidden" :aria-busy="loading">
          <div class="flex min-h-0 flex-1 flex-col overflow-auto">
            <table class="w-full shrink-0 text-left text-sm">
              <thead class="sticky top-0 z-10 border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-400"><tr>
                <th class="px-4 py-3">{{ t('taskLogs.created') }}</th><th class="px-4 py-3">{{ t('taskLogs.model') }} / {{ t('taskLogs.taskId') }}</th><th class="px-4 py-3">{{ t('taskLogs.plugin') }}</th><th class="px-4 py-3">{{ t('taskLogs.group') }}</th><th v-if="admin" class="px-4 py-3">{{ t('taskLogs.userAccount') }}</th><th class="px-4 py-3">{{ t('common.status') }}</th><th class="px-4 py-3">{{ t('taskLogs.duration') }}</th><th class="px-4 py-3">{{ t('taskLogs.billing') }}</th><th class="px-4 py-3">{{ t('common.actions') }}</th>
              </tr></thead>
              <tbody v-if="data?.items.length" class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="task in data?.items ?? []" :key="task.id" class="align-top transition-colors hover:bg-gray-50/70 dark:hover:bg-dark-800/40">
                  <td class="whitespace-nowrap px-4 py-4 text-xs text-gray-500 dark:text-dark-400">{{ formatDate(task.created_at) }}</td>
                  <td class="max-w-64 px-4 py-4">
                    <p class="break-words font-medium text-gray-900 dark:text-white">{{ task.model }}</p>
                    <div class="mt-1 flex items-start gap-1.5">
                      <code class="block break-all text-xs text-gray-500 dark:text-dark-400">{{ task.id }}</code>
                      <button
                        type="button"
                        class="shrink-0 rounded p-0.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-700 dark:hover:text-gray-200"
                        :title="copiedTaskId === task.id ? t('common.copied') : t('common.copy')"
                        :aria-label="copiedTaskId === task.id ? t('common.copied') : t('common.copy')"
                        @click="copyTaskId(task.id)"
                      >
                        <svg v-if="copiedTaskId === task.id" class="h-3.5 w-3.5 text-emerald-500" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" /></svg>
                        <svg v-else class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" /></svg>
                      </button>
                    </div>
                  </td>
                  <td class="px-4 py-4 text-xs"><p>{{ task.plugin_key }}</p><span class="text-gray-500 dark:text-dark-400">{{ task.source === 'native' ? t('taskLogs.nativeSource') : `v${task.plugin_version}` }}</span></td>
                  <td class="px-4 py-4 text-xs">{{ task.group_name || `#${task.group_id}` }}</td>
                  <td v-if="admin" class="px-4 py-4 text-xs">#{{ task.user_id }} / #{{ task.account_id }}</td>
                  <td class="min-w-36 px-4 py-4 text-xs">
                    <span class="badge" :class="statusClass(task.status)">{{ statusLabel(task.status) }}</span>
                    <p class="mt-1.5 text-gray-500 dark:text-dark-400">{{ deliveryLabel(task.delivery_status) }}</p>
                    <div v-if="active(task)" class="mt-1.5 space-y-1">
                      <p class="tabular-nums text-gray-500 dark:text-dark-400">{{ task.progress > 0 ? `${task.progress}%` : t('taskLogs.waitingProgress') }}</p>
                      <div v-if="task.progress > 0" class="h-1.5 w-24 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
                        <div class="h-full rounded-full bg-primary-500 transition-all duration-300" :style="{ width: `${Math.min(100, Math.max(0, task.progress))}%` }" />
                      </div>
                    </div>
                    <p v-if="task.monitor_status" class="mt-1" :class="task.monitor_status === 'review' ? 'text-amber-700 dark:text-amber-400' : 'text-gray-500 dark:text-dark-400'">{{ t(`taskLogs.monitorStatuses.${task.monitor_status}`) }}</p>
                  </td>
                  <td class="whitespace-nowrap px-4 py-4 text-xs tabular-nums">{{ duration(task) }}</td>
                  <td class="whitespace-nowrap px-4 py-4 text-xs"><p>{{ billingLabel(task.billing_status) }}</p><span class="tabular-nums text-gray-500 dark:text-dark-400">{{ task.source === 'native' && task.actual_amount == null ? '—' : `$${task.actual_amount ?? task.hold_amount}` }}</span></td>
                  <td class="px-4 py-4"><button type="button" class="btn btn-secondary btn-sm" @click="openDetail(task.id)">{{ t('taskLogs.details') }}</button></td>
                </tr>
              </tbody>
            </table>
            <div v-if="!data?.items.length" data-testid="task-logs-empty" class="flex flex-1 flex-col items-center justify-center px-4 py-16 text-center text-gray-500 dark:text-dark-400">
              <svg class="mb-3 h-10 w-10 text-gray-400 dark:text-dark-500" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                <path stroke-linecap="round" stroke-linejoin="round" d="M2.25 13.5h3.86a2.25 2.25 0 012.012 1.244l.256.512a2.25 2.25 0 002.013 1.244h3.218a2.25 2.25 0 002.013-1.244l.256-.512a2.25 2.25 0 012.013-1.244h3.859m-19.5.338V18a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18v-4.162c0-.224-.034-.447-.1-.661L19.24 5.338a2.25 2.25 0 00-2.15-1.588H6.911a2.25 2.25 0 00-2.15 1.588L2.35 13.177a2.25 2.25 0 00-.1.661z" />
              </svg>
              <p class="text-sm font-medium">{{ loading ? t('common.loading') : t('taskLogs.empty') }}</p>
            </div>
          </div>
          <component :is="paginationComponent" v-if="data && data.total > 0" class="shrink-0" :page="page" :page-size="pageSize" :total="data.total" @update:page="page = Number($event)" @update:page-size="pageSize = Number($event); page = 1" />
        </div>
      </template>
    </div>
    <component :is="dialogComponent" :show="!!detailId" :title="t('taskLogs.details')" width="wide" @close="closeDetail">
      <p v-if="detailError" role="alert" class="rounded-xl border border-red-200 bg-red-50/90 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">{{ detailError }}</p>
      <p v-if="detailLoading && !detail" role="status" class="text-sm text-gray-500 dark:text-dark-400">{{ t('common.loading') }}</p>
      <div v-if="detail" class="space-y-6">
        <header class="space-y-2">
          <h4 class="text-base font-semibold text-gray-900 dark:text-white">{{ detail.model }}</h4>
          <div class="flex items-center gap-2">
            <code class="block break-all rounded bg-gray-100 px-2 py-0.5 text-xs text-gray-700 dark:bg-dark-800 dark:text-dark-300">{{ detail.id }}</code>
            <button type="button" class="btn btn-ghost btn-sm" @click="copyTaskId(detail.id)">{{ copiedTaskId === detail.id ? t('common.copied') : t('common.copy') }}</button>
          </div>
          <div class="flex items-center gap-2"><span class="badge" :class="statusClass(detail.status)">{{ statusLabel(detail.status) }}</span><span class="text-xs tabular-nums text-gray-500 dark:text-dark-400">{{ duration(detail) }}</span></div>
        </header>
        <dl class="grid gap-4 text-sm sm:grid-cols-2">
          <div><dt class="text-gray-500 dark:text-dark-400">{{ t('taskLogs.plugin') }}</dt><dd class="mt-1">{{ detail.plugin_key }} · {{ detail.source === 'native' ? t('taskLogs.nativeSource') : `v${detail.plugin_version}` }}</dd></div>
          <div><dt class="text-gray-500 dark:text-dark-400">{{ t('taskLogs.group') }}</dt><dd class="mt-1">{{ detail.group_name }}</dd></div>
          <div><dt class="text-gray-500 dark:text-dark-400">{{ t('taskLogs.protocol') }}</dt><dd class="mt-1">{{ detail.protocol }} / {{ detail.action }}</dd></div>
          <div><dt class="text-gray-500 dark:text-dark-400">{{ t('taskLogs.created') }}</dt><dd class="mt-1">{{ formatDate(detail.created_at) }}</dd></div>
          <div><dt class="text-gray-500 dark:text-dark-400">{{ t('taskLogs.held') }}</dt><dd class="mt-1 tabular-nums">{{ detail.source === 'native' ? '—' : `$${detail.hold_amount}` }}</dd></div>
          <div><dt class="text-gray-500 dark:text-dark-400">{{ t('taskLogs.settled') }}</dt><dd class="mt-1 tabular-nums">{{ detail.actual_amount == null ? '—' : `$${detail.actual_amount}` }} · {{ billingLabel(detail.billing_status) }}</dd></div>
          <div v-if="admin && detail.upstream_id" class="sm:col-span-2"><dt class="text-gray-500 dark:text-dark-400">{{ t('taskLogs.upstreamId') }}</dt><dd class="mt-1 break-all font-mono text-xs">{{ detail.upstream_id }}</dd></div>
          <div v-if="detail.fields.prompt" class="sm:col-span-2"><dt class="text-gray-500 dark:text-dark-400">{{ t('taskLogs.prompt') }}</dt><dd class="mt-1 whitespace-pre-wrap break-words">{{ detail.fields.prompt }}</dd></div>
          <div v-if="detail.fields.seconds || detail.fields.size"><dt class="text-gray-500 dark:text-dark-400">{{ t('taskLogs.parameters') }}</dt><dd class="mt-1">{{ detail.fields.seconds || '—' }} s / {{ detail.fields.size || '—' }}</dd></div>
          <div v-if="detail.error_code || detail.error_message" class="sm:col-span-2"><dt class="text-gray-500 dark:text-dark-400">{{ t(detail.status === 'succeeded' && detail.billing_status === 'review' ? 'taskLogs.billingIssue' : 'taskLogs.failure') }}</dt><dd class="mt-1 break-words text-red-600 dark:text-red-400">{{ detail.error_code }} {{ detail.error_message }}</dd></div>
          <div v-if="detail.monitor_status" class="sm:col-span-2"><dt class="text-gray-500 dark:text-dark-400">{{ t('taskLogs.monitoring') }}</dt><dd class="mt-1" :class="detail.monitor_status === 'review' ? 'text-amber-700 dark:text-amber-400' : ''">{{ t(`taskLogs.monitorStatuses.${detail.monitor_status}`) }}<span v-if="detail.monitor_status === 'review'"> · {{ monitorError(detail.monitor_error_code) }}</span></dd></div>
        </dl>
        <p v-if="detail.source === 'native'" class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('taskLogs.nativeHint') }}</p>
        <form v-if="canMaintainNative && detail.native_actions?.length" class="space-y-3 rounded-lg border border-amber-200 p-4 dark:border-amber-900" data-native-maintenance @submit.prevent="maintainNative">
          <p class="text-sm">{{ t('taskLogs.maintenance.hint') }}</p>
          <div><span class="input-label">{{ t('taskLogs.maintenance.action') }}</span><HostSelect v-model="nativeAction" :select-component="selectComponent" :label="t('taskLogs.maintenance.action')" :disabled="maintaining"
              :options="detail.native_actions.map(value => ({ value, label: t(`taskLogs.maintenance.${value}`) }))" /></div>
          <label><span class="input-label">{{ t('taskLogs.maintenance.reason') }}</span><textarea v-model="nativeReason" class="input" rows="2" maxlength="2000" :disabled="maintaining" required /></label>
          <button type="submit" class="btn btn-primary btn-sm" :disabled="maintaining || nativeReason.trim().length < 4 || !detail.native_actions.includes(nativeAction)">{{ t('taskLogs.maintenance.confirm') }}</button>
        </form>
        <div v-if="client.downloadTaskContent && detail.source !== 'native' && detail.status === 'succeeded' && detail.delivery_status === 'ready'" class="flex items-center gap-3"><button type="button" class="btn btn-primary btn-sm" :disabled="downloading" @click="download">{{ downloading ? t('common.loading') : t('taskLogs.download') }}</button><p class="text-xs text-gray-500">{{ t('taskLogs.downloadHint') }}</p></div>
        <section><h4 class="text-sm font-semibold">{{ t('taskLogs.timeline') }}</h4><ol class="mt-3 space-y-3 border-l border-gray-200 pl-4 dark:border-dark-700"><li v-for="(event, index) in detail.events" :key="index" class="text-sm"><p>{{ eventLabel(event.type) }}</p><time class="text-xs text-gray-500">{{ formatDate(event.created_at) }}</time></li></ol></section>
      </div>
    </component>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, useSlots, watch, type Component } from 'vue'
import HostSelect from './HostSelect.vue'
import type { PluginTranslator, RunPluginMutation } from './types'
import type { NativeTaskAction, TaskLog, TaskLogClient, TaskLogDetail, TaskLogFilter, TaskLogPage } from './taskLogs'

const props = withDefaults(defineProps<{
  client: TaskLogClient
  t: PluginTranslator
  locale: string
  dialogComponent: Component
  selectComponent?: Component
  paginationComponent: Component
  admin?: boolean
  editable?: boolean
  runMutation?: RunPluginMutation
  cancelMutation?: () => void
  isCancelled?: (error: unknown) => boolean
}>(), { admin: false, editable: false, isCancelled: () => false })
const slots = useSlots()
const t: PluginTranslator = (key, values) => props.t(key, values)
const admin = computed(() => props.admin)
const tabs = computed(() => admin.value ? (slots.review ? ['all', 'mine', 'review'] : ['all', 'mine']) : ['mine'])
const tab = ref(admin.value ? 'all' : 'mine')
const canMaintainNative = computed(() => props.admin && props.editable && !!props.runMutation && !!props.client.maintainNativeTask)
const statuses = ['queued', 'submitting', 'submission_uncertain', 'running', 'succeeded', 'failed']
const billingStatuses = ['held', 'pending', 'settled', 'review', 'unconfirmed', 'not_charged']
const statusOptions = computed(() => [
  { value: '', label: t('common.all') },
  ...statuses.map(value => ({ value, label: statusLabel(value) }))
])
const billingOptions = computed(() => [
  { value: '', label: t('common.all') },
  ...billingStatuses.map(value => ({ value, label: billingLabel(value) }))
])
const filters = reactive({ task_id: '', model: '', plugin: '', status: '', billing_status: '', review: false })
const start = ref('')
const end = ref('')
const applied = ref<Partial<TaskLogFilter>>({})
const page = ref(1)
const pageSize = ref(20)
const data = ref<TaskLogPage>()
const loading = ref(false)
const error = ref('')
const autoRefresh = ref(true)
const now = ref(Date.now())
const detailId = ref('')
const detail = ref<TaskLogDetail>()
const detailLoading = ref(false)
const detailError = ref('')
const downloading = ref(false)
const maintaining = ref(false)
const nativeAction = ref<NativeTaskAction>('resume_poll')
const nativeReason = ref('')
const showAdvancedFilters = ref(false)
const copiedTaskId = ref('')
let copiedTimer: ReturnType<typeof setTimeout> | undefined
const advancedFilterCount = computed(() => [filters.plugin, filters.billing_status, start.value, end.value].filter(Boolean).length)
let listController: AbortController | undefined
let detailController: AbortController | undefined
let downloadController: AbortController | undefined
let maintenanceController: AbortController | undefined
let timer: ReturnType<typeof setInterval> | undefined
const errorText = () => t('taskLogs.loadFailed')
const active = (task: TaskLog) => task.status !== 'succeeded' && task.status !== 'failed'
function statusLabel(value: string) { return statuses.includes(value) ? t(`taskLogs.statuses.${value}`) : value }
function billingLabel(value: string) { return billingStatuses.includes(value) ? t(`taskLogs.billingStatuses.${value}`) : value }
function deliveryLabel(value: string) { return ['pending', 'ready', 'failed'].includes(value) ? t(`taskLogs.deliveryStatuses.${value}`) : value }
function eventLabel(value: string) { return ['funds_held', 'funds_settled', 'administrator_reconciliation', 'native_submitted', 'native_running', 'native_succeeded', 'native_failed', 'native_monitor_review', 'native_billing_settled', 'native_billing_logged', 'native_billing_ignored', 'native_billing_review', 'native_resume_poll', 'native_retry_settlement'].includes(value) ? t(`taskLogs.events.${value}`) : t('taskLogs.events.updated') }
function monitorError(code?: string) {
  const known = ['timeout', 'account_unavailable', 'connection_changed', 'auth_failed', 'capacity', 'upstream_error', 'response_limit', 'unrecognized', 'usage_pending']
  const key = code?.replace(/^native_monitor_/, '')
  return key && known.includes(key) ? t(`taskLogs.monitorErrors.${key}`) : t('taskLogs.monitorErrors.upstream_error')
}
function statusClass(value: string) {
  if (value === 'succeeded') return 'badge-success'
  if (value === 'failed' || value === 'submission_uncertain') return 'badge-danger'
  if (value === 'running' || value === 'submitting') return 'badge-primary'
  return 'badge-gray'
}
async function copyTaskId(id: string) {
  try {
    await navigator.clipboard.writeText(id)
    copiedTaskId.value = id
    clearTimeout(copiedTimer)
    copiedTimer = setTimeout(() => { if (copiedTaskId.value === id) copiedTaskId.value = '' }, 1800)
  } catch { /* Task ID remains selectable when clipboard access is unavailable. */ }
}
function formatDate(value: string) { return new Date(value).toLocaleString(props.locale) }
function duration(task: TaskLog) {
  const seconds = Math.max(0, ((task.finished_at ? Date.parse(task.finished_at) : now.value) - Date.parse(task.created_at)) / 1000)
  return seconds < 60 ? `${seconds.toFixed(1)} s` : `${Math.floor(seconds / 60)} m ${Math.floor(seconds % 60)} s`
}
async function load(background = false) {
  if (background && loading.value) return
  listController?.abort()
  if (tab.value === 'review') { loading.value = false; return }
  const controller = listController = new AbortController()
  loading.value = true
  try {
    const result = await props.client.listTaskLogs({ ...applied.value, page: page.value, page_size: pageSize.value, scope: tab.value }, controller.signal)
    if (!controller.signal.aborted) { data.value = result; error.value = '' }
  } catch { if (!controller.signal.aborted) error.value = errorText() }
  finally { if (!controller.signal.aborted) loading.value = false }
}
function applyFilters() {
  if (start.value && end.value && Date.parse(end.value) <= Date.parse(start.value)) { error.value = t('taskLogs.invalidRange'); return }
  applied.value = { ...filters, start: start.value ? new Date(start.value).toISOString() : undefined, end: end.value ? new Date(end.value).toISOString() : undefined }
  if (page.value === 1) void load(); else page.value = 1
}
function reset() { Object.assign(filters, { task_id: '', model: '', plugin: '', status: '', billing_status: '', review: false }); start.value = ''; end.value = ''; applyFilters() }
async function openDetail(id: string, background = false) {
  if (background && detailLoading.value) return
  detailController?.abort()
  const controller = detailController = new AbortController()
  if (detailId.value !== id) {
    downloadController?.abort(); maintenanceController?.abort(); props.cancelMutation?.()
    detail.value = undefined; nativeReason.value = ''; nativeAction.value = 'resume_poll'
    downloading.value = false; maintaining.value = false
  }
  detailId.value = id; detailLoading.value = true; detailError.value = ''
  try {
    const result = await props.client.getTaskLog(id, controller.signal)
    if (!controller.signal.aborted) {
      detail.value = result
      if (result.native_actions?.length && !result.native_actions.includes(nativeAction.value)) nativeAction.value = result.native_actions[0]!
    }
  } catch { if (!controller.signal.aborted) detailError.value = errorText() }
  finally { if (!controller.signal.aborted) detailLoading.value = false }
}
function closeDetail() { detailController?.abort(); downloadController?.abort(); maintenanceController?.abort(); props.cancelMutation?.(); detailId.value = ''; detail.value = undefined; detailLoading.value = false; downloading.value = false }
async function maintainNative() {
  if (!canMaintainNative.value || !props.runMutation || !props.client.maintainNativeTask || !detail.value || maintaining.value || nativeReason.value.trim().length < 4 || !detail.value.native_actions?.includes(nativeAction.value)) return
  const id = detail.value.id
  const action = nativeAction.value
  const reason = nativeReason.value.trim()
  const controller = maintenanceController = new AbortController()
  maintaining.value = true
  detailError.value = ''
  try {
    const client = props.client
    await props.runMutation(() => {
      if (controller.signal.aborted || detailId.value !== id || !canMaintainNative.value) throw new DOMException('Aborted', 'AbortError')
      return client.maintainNativeTask!(id, action, reason, controller.signal)
    })
    if (controller.signal.aborted) return
    nativeReason.value = ''
    await Promise.all([load(true), openDetail(id)])
  } catch (e) { if (!controller.signal.aborted && !props.isCancelled(e)) detailError.value = errorText() }
  finally { if (maintenanceController === controller) maintaining.value = false }
}
async function download() {
  if (!props.client.downloadTaskContent || !detail.value || downloading.value) return
  const id = detail.value.id
  const controller = downloadController = new AbortController()
  downloading.value = true
  try {
    const blob = await props.client.downloadTaskContent(id, controller.signal)
    if (controller.signal.aborted) return
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a'); link.href = url; link.download = `${id}.mp4`; link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch { if (!controller.signal.aborted) detailError.value = errorText() }
  finally { if (downloadController === controller) downloading.value = false }
}
watch(admin, value => { tab.value = value ? 'all' : 'mine' })
watch(tab, () => { page.value = 1 })
watch([page, pageSize, tab, admin, () => props.client], () => { data.value = undefined; error.value = ''; closeDetail(); void load() }, { immediate: true })
function visibilityChanged() {
  if (document.hidden) {
    listController?.abort(); detailController?.abort(); loading.value = false; detailLoading.value = false
  } else if (autoRefresh.value) {
    now.value = Date.now(); void load(true)
    if (detailId.value) void openDetail(detailId.value, true)
  }
}
onMounted(() => {
  document.addEventListener('visibilitychange', visibilityChanged)
  timer = setInterval(() => {
    if (document.hidden) return
    now.value = Date.now()
    if (!autoRefresh.value) return
    void load(true)
    if (detail.value && (active(detail.value) || detail.value.billing_status !== 'settled')) void openDetail(detail.value.id, true)
  }, 5000)
})
onBeforeUnmount(() => { document.removeEventListener('visibilitychange', visibilityChanged); clearInterval(timer); clearTimeout(copiedTimer); listController?.abort(); detailController?.abort(); downloadController?.abort(); maintenanceController?.abort(); props.cancelMutation?.() })
</script>

<style scoped>
@media (max-width: 1023px) {
  .task-log-console {
    height: auto;
    min-height: 0;
  }
}
</style>
