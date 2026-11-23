<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex flex-wrap items-start justify-between gap-4 border-b border-gray-200 pb-5 dark:border-dark-700">
        <div><h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('requestPlugins.title') }}</h2><p class="mt-1 max-w-3xl text-sm text-gray-500">{{ t('requestPlugins.description') }}</p></div>
        <div class="flex gap-2"><RouterLink to="/admin/plugins" class="btn btn-secondary">{{ t('nav.plugins') }}</RouterLink><button class="btn btn-primary" :disabled="busy || loading || !versions.length" @click="create">{{ t('requestPlugins.create') }}</button></div>
      </header>
      <p class="rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm leading-6 text-amber-900 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-200">{{ t('requestPlugins.coverage') }}</p>
      <ModelModifierSettings />
      <nav class="flex gap-2"><button v-for="value in ['instances','runs'] as const" :key="value" class="btn btn-sm" :class="tab === value ? 'btn-primary' : 'btn-secondary'" :aria-pressed="tab === value" @click="tab = value; value === 'runs' && loadRuns()">{{ t(`requestPlugins.${value}`) }}</button><button class="btn btn-secondary btn-sm" :disabled="loading || busy" @click="tab === 'runs' ? loadRuns() : load()">{{ t('common.refresh') }}</button></nav>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="replyAuditFailures" role="alert" class="rounded-lg border border-red-200 p-3 text-sm text-red-700 dark:border-red-900 dark:text-red-400" data-reply-audit-failures>{{ t('requestPlugins.replyAuditFailures', { count: replyAuditFailures }) }}</p>
      <p v-if="reportStats" class="text-xs leading-5 text-gray-500" data-report-stats>{{ t('requestPlugins.reportStats', reportStats) }}</p>
      <p v-if="message" role="status" class="text-sm text-emerald-700 dark:text-emerald-400">{{ message }}</p>
      <template v-if="tab === 'instances'">
        <p v-if="!loading && !instances.length" class="rounded-xl border border-dashed border-gray-300 p-10 text-center text-sm text-gray-500 dark:border-dark-600">{{ t('requestPlugins.empty') }}</p>
        <article v-for="item in instances" :key="item.id" class="card flex flex-wrap items-center gap-5 border border-gray-200 p-5 dark:border-dark-700" data-instance>
          <div class="min-w-0 flex-1"><h3 class="font-medium text-gray-900 dark:text-white">{{ item.name }} <span class="ml-2 text-xs" :class="item.enabled ? 'text-emerald-700 dark:text-emerald-400' : 'text-gray-500'">{{ t(item.enabled ? 'admin.plugins.enabled' : 'admin.plugins.disabled') }}</span></h3><p class="mt-1 break-all text-xs text-gray-500">{{ item.plugin_key }} · v{{ item.plugin_version }} · {{ t('requestPlugins.order') }} {{ item.position }}</p><p class="mt-2 text-sm text-gray-500">{{ t(`requestPlugins.scopes.${item.scope}`) }}<span v-if="item.scope === 'group'">: {{ item.group_ids.map(groupName).join(', ') }}</span><span v-if="item.scope === 'account'">: {{ (item.account_ids ?? []).map(id => `#${id}`).join(', ') }}</span> · {{ item.models.join(', ') || t('requestPlugins.allModels') }} · {{ item.stages.join(' → ') }} · {{ item.formats?.join(', ') || t('requestPlugins.allFormats') }}</p></div>
          <div class="flex gap-2"><button class="btn btn-secondary btn-sm" :disabled="busy" @click="edit(item)">{{ t('common.edit') }}</button><button class="btn btn-secondary btn-sm" :disabled="busy" @click="toggle(item)">{{ t(item.enabled ? 'admin.plugins.disable' : 'admin.plugins.enable') }}</button></div>
        </article>
      </template>
      <template v-else>
        <form class="flex gap-2" @submit.prevent="page = 1; loadRuns()"><input v-model.trim="requestID" class="input max-w-lg" :placeholder="t('requestPlugins.requestID')" :aria-label="t('requestPlugins.requestID')" /><button class="btn btn-secondary" :disabled="loading">{{ t('common.search') }}</button></form>
        <p class="text-xs text-gray-500">{{ t('requestPlugins.auditHint') }}</p>
        <article v-for="run in runs" :key="run.id" class="card space-y-3 border border-gray-200 p-4 dark:border-dark-700">
          <div class="flex flex-wrap justify-between gap-2 text-xs text-gray-500"><span>{{ new Date(run.created_at).toLocaleString() }} · {{ run.model || '—' }} · {{ run.format }} <span v-if="run.dry_run">· {{ t('requestPlugins.dryRun') }}</span></span><code class="break-all">{{ run.request_id }}</code></div>
          <div v-for="(record,index) in run.records" :key="index" class="flex flex-wrap items-center gap-x-5 gap-y-1 text-sm"><span>#{{ record.instance_id }} · vID {{ record.version_id }} · {{ record.stage }}</span><span :class="record.action === 'reject' || record.action === 'error' ? 'text-red-600' : 'text-gray-600 dark:text-gray-300'">{{ t(`requestPlugins.actions.${record.action}`) }}</span><span class="text-xs text-gray-500">{{ record.duration_ms }} ms · {{ t('requestPlugins.effectCount') }} {{ record.effects }} · {{ t('requestPlugins.findingCount') }} {{ record.findings }} <span v-if="record.account_id"> · #{{ record.account_id }} · {{ t('requestPlugins.attempt') }} {{ record.attempt ?? 0 }}</span> <code>{{ record.error_class }}</code></span><RequestPluginMediaRecords :record="record" /></div>
        </article>
        <p v-if="!loading && !runs.length" class="py-8 text-center text-sm text-gray-500">{{ t('requestPlugins.noRuns') }}</p>
        <div class="flex items-center justify-end gap-3 text-sm"><button class="btn btn-secondary btn-sm" :disabled="loading || page === 1" @click="page--; loadRuns()">{{ t('requestPlugins.previous') }}</button><span>{{ page }} / {{ Math.max(1,Math.ceil(total / 20)) }}</span><button class="btn btn-secondary btn-sm" :disabled="loading || page * 20 >= total" @click="page++; loadRuns()">{{ t('requestPlugins.next') }}</button></div>
      </template>
    </div>
    <BaseDialog :show="!!draft" :title="t('requestPlugins.editor')" width="wide" @close="closeEditor">
      <form v-if="draft" class="space-y-4" @submit.prevent="save">
        <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="space-y-1 text-sm">{{ t('requestPlugins.name') }}<input v-model="draft.name" class="input" maxlength="128" required :disabled="busy" /></label>
          <label class="space-y-1 text-sm">{{ t('requestPlugins.version') }}<select v-model="draft.plugin_version_id" class="input" :disabled="busy || !!draft.id" @change="resetConfig"><option v-for="version in versions" :key="version.id" :value="version.id">{{ version.meta.name }} · v{{ version.version }} · #{{ version.id }}</option></select></label>
          <label class="space-y-1 text-sm">{{ t('requestPlugins.scope') }}<select v-model="draft.scope" class="input" :disabled="busy" data-scope @change="changeScope"><option value="global">{{ t('requestPlugins.scopes.global') }}</option><option value="group">{{ t('requestPlugins.scopes.group') }}</option><option value="account" :disabled="!selectedVersion?.capabilities.hooks?.some(stage => ['ingress','request','outbound','egress','reply','report'].includes(stage))">{{ t('requestPlugins.scopes.account') }}</option></select></label>
          <label class="space-y-1 text-sm">{{ t('requestPlugins.order') }}<input v-model.number="draft.position" type="number" min="0" max="10000" required class="input" :disabled="busy" /></label>
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="space-y-1 text-sm">{{ t('requestPlugins.stages') }}<select :value="draft.stages.join(',')" class="input" :disabled="busy" data-stage-mode @change="changeStages(($event.target as HTMLSelectElement).value)"><option v-for="choice in stageChoices" :key="choice" :value="choice">{{ choice.split(',').join(' → ') }}</option></select></label>
          <label class="space-y-1 text-sm">{{ t('requestPlugins.formatScope') }}<select :value="draft.formats?.join(',') || ''" class="input" :disabled="busy" data-format-scope @change="changeFormats(($event.target as HTMLSelectElement).value)"><option v-if="!explicitFormats" value="">{{ t('requestPlugins.allFormats') }}</option><option v-if="explicitFormats" value="openai,openai_responses">{{ t('requestPlugins.providerFormats') }}</option><option v-for="format in supportedFormats" :key="format" :value="format">{{ format }}</option></select></label>
        </div>
        <p v-if="selectedVersion?.meta.scope?.channelTypes?.length" class="text-xs leading-5 text-gray-500" data-channel-scope>{{ t('requestPlugins.channelScopeHint', { types: selectedVersion.meta.scope.channelTypes.join(', ') }) }}</p>
        <fieldset v-if="draft.scope === 'group'" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700"><legend class="px-1 text-sm">{{ t('requestPlugins.groups') }}</legend><div class="flex max-h-40 flex-wrap gap-3 overflow-auto"><label v-for="group in groups" :key="group.id" class="flex items-center gap-1 text-sm"><input v-model="draft.group_ids" :value="group.id" type="checkbox" :disabled="busy" />{{ group.name }} (#{{ group.id }})</label></div></fieldset>
        <fieldset v-if="draft.scope === 'account'" class="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-700" data-accounts><legend class="px-1 text-sm">{{ t('requestPlugins.accounts') }}</legend><p class="text-xs leading-5 text-gray-500">{{ t('requestPlugins.accountHint') }}</p><Select :model-value="null" :options="accountOptions" searchable remote :loading="accountLoading" :disabled="busy || (draft.account_ids?.length ?? 0) >= 100" :aria-label="t('requestPlugins.accounts')" :placeholder="t('requestPlugins.searchAccounts')" @search="searchAccounts" @update:model-value="addAccount" /><div class="flex flex-wrap gap-2"><button v-for="id in draft.account_ids" :key="id" type="button" class="btn btn-secondary btn-sm" :disabled="busy" :aria-label="`${t('common.remove')} #${id}`" @click="removeAccount(id)">{{ accountNames[id] ?? `#${id}` }} ×</button></div></fieldset>
        <label class="block space-y-1 text-sm">{{ t('requestPlugins.models') }}<textarea v-model="modelText" class="input font-mono text-xs" rows="2" :disabled="busy" :placeholder="t('requestPlugins.allModels')" /></label>
        <label class="block space-y-1 text-sm">{{ t('requestPlugins.config') }}<textarea v-model="configText" class="input font-mono text-xs" rows="9" :disabled="busy" spellcheck="false" required /></label>
        <p class="text-xs leading-5 text-gray-500">{{ t('requestPlugins.versionHint') }}</p>
        <label class="flex items-center gap-2 text-sm"><input v-model="draft.enabled" type="checkbox" :disabled="busy" />{{ t('requestPlugins.enabled') }}</label>
        <label v-if="draft.stages.some(stage => stage === 'ingress' || stage === 'egress')" class="block space-y-2 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm dark:border-amber-900 dark:bg-amber-950/40" data-stage-network><span class="flex items-center gap-2"><input v-model="draft.network_enabled" type="checkbox" :disabled="busy" />{{ t('requestPlugins.stageNetwork') }}</span><span class="block text-xs leading-5 text-amber-900 dark:text-amber-200">{{ t('requestPlugins.stageNetworkHint') }}</span></label>
        <label v-if="draft.stages.includes('reply')" class="block space-y-2 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm dark:border-amber-900 dark:bg-amber-950/40" data-reply-network><span class="flex items-center gap-2"><input v-model="draft.reply_network_enabled" type="checkbox" :disabled="busy" />{{ t('requestPlugins.replyNetwork') }}</span><span class="block text-xs leading-5 text-amber-900 dark:text-amber-200">{{ t('requestPlugins.replyNetworkHint') }}</span></label>
        <p v-if="draft.stages.includes('report')" class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-200">{{ t('requestPlugins.reportHint') }}</p>
        <details v-if="draft.stages.some(stage => stage !== 'report')" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700" open><summary class="cursor-pointer text-sm font-medium">{{ t('requestPlugins.dryRun') }}</summary><div class="mt-3 space-y-3"><p class="text-xs text-gray-500">{{ t('requestPlugins.dryRunHint') }}</p><select v-model="sampleStage" class="input w-auto" :disabled="busy" :aria-label="t('requestPlugins.stages')" @change="taskStages && (sampleFormat = 'task')"><option v-for="stage in draft.stages.filter(stage => stage !== 'report')" :key="stage">{{ stage }}</option></select><select v-model="sampleFormat" class="input w-auto" :aria-label="t('requestPlugins.format')" :disabled="busy || taskStages"><option v-for="format in supportedFormats" :key="format">{{ format }}</option></select><textarea v-model="sampleText" class="input font-mono text-xs" rows="6" :disabled="busy" :aria-label="t('requestPlugins.sample')" spellcheck="false" /><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="dryRun">{{ t('requestPlugins.run') }}</button><div v-if="dryResult" class="space-y-2" data-dry-result><p class="text-sm" :class="dryResult.rejected || dryResult.error_class ? 'text-red-600' : 'text-emerald-700 dark:text-emerald-400'">{{ dryResult.rejected ? t('requestPlugins.actions.reject') : dryResult.error_class ? t('requestPlugins.actions.error') : t('requestPlugins.actions.continue') }} {{ dryResult.error_class }}</p><pre class="max-h-80 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">{{ JSON.stringify(dryResult.body,null,2) }}</pre><RequestPluginMediaRecords v-for="(record,index) in dryResult.records" :key="index" :record="record" /><template v-if="dryResult.headers && Object.keys(dryResult.headers).length"><p class="text-xs text-gray-500">{{ t('requestPlugins.businessHeaders') }}</p><pre class="max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900" data-dry-headers>{{ JSON.stringify(dryResult.headers,null,2) }}</pre></template></div></div></details>
        <div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="busy" @click="closeEditor">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="busy || !draft.name.trim() || draft.scope === 'group' && !draft.group_ids.length || draft.scope === 'account' && !draft.account_ids?.length">{{ t('common.save') }}</button></div>
      </form>
    </BaseDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import RequestPluginMediaRecords from '@/components/plugins/RequestPluginMediaRecords.vue'
import ModelModifierSettings from '@/components/plugins/ModelModifierSettings.vue'
import { useStepUp, isStepUpCancelled } from '@/composables/useStepUp'
import { adminAPI } from '@/api/admin'
import { listNewAPIPlugins, listNewAPIPluginVersions, type NewAPIPluginVersion, type PluginConfigField } from '@/api/admin/newapiPlugins'
import { dryRunRequestPlugin, getRequestPluginConfig, listRequestPluginRuns, getRequestProcessingStatus, saveRequestPlugin, type RequestPluginInstance, type RequestPluginOutcome, type RequestPluginRun } from '@/api/admin/requestPlugins'
import type { AdminGroup } from '@/types'
import { platformUsesGateway } from '@/constants/platformCatalog'

const { t } = useI18n()
const stepUp = useStepUp()
const controller = new AbortController()
const instances = ref<RequestPluginInstance[]>([])
const versions = ref<NewAPIPluginVersion[]>([])
const groups = ref<AdminGroup[]>([])
const runs = ref<RequestPluginRun[]>([])
const tab = ref<'instances' | 'runs'>('instances')
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const message = ref('')
const replyAuditFailures = ref(0)
const reportStats = ref<Awaited<ReturnType<typeof getRequestProcessingStatus>>['report_stats']>()
const draft = ref<RequestPluginInstance>()
const configText = ref('{}')
const modelText = ref('')
const sampleFormat = ref('openai')
const sampleStage = ref('ingress')
const sampleText = ref(JSON.stringify({ model: 'gpt-4o-mini', messages: [{ role: 'user', content: 'Contact test@example.com' }] }, null, 2))
const dryResult = ref<RequestPluginOutcome>()
const requestID = ref('')
const page = ref(1)
const total = ref(0)
let epoch = 0
let runsSequence = 0
const accountOptions = ref<SelectOption[]>([])
const accountNames = ref<Record<number, string>>({})
const accountLoading = ref(false)
let accountSearch: AbortController | undefined
const reportOnly = computed(() => draft.value?.stages.length === 1 && draft.value.stages[0] === 'report')
const nativeProtocolFormats = computed(() => !!draft.value?.formats?.length && draft.value.formats.every(format => ['claude', 'gemini'].includes(format)))
const nativeClaudeFormat = computed(() => draft.value?.formats?.join(',') === 'claude')
const httpStages = computed(() => draft.value?.scope === 'account' && !reportOnly.value || draft.value?.stages.some(stage => ['request', 'outbound', 'reply'].includes(stage)))
const taskStages = computed(() => !httpStages.value && draft.value?.stages.includes('egress') && draft.value.formats?.join(',') === 'task')
const channelScoped = computed(() => !!selectedVersion.value?.meta.scope?.channelTypes?.length)
const explicitFormats = computed(() => channelScoped.value || httpStages.value || draft.value?.stages.some(stage => stage === 'egress' || stage === 'report'))
const supportedFormats = computed(() => httpStages.value ? ['openai', 'openai_responses', 'claude', 'gemini'] : reportOnly.value ? ['openai', 'openai_responses', 'claude', 'gemini', ...(draft.value?.scope === 'account' ? [] : ['task'])] : explicitFormats.value ? ['openai', 'openai_responses', 'claude', 'gemini', 'task'] : ['openai','openai_responses','claude','gemini','task'])
const selectedVersion = computed(() => versions.value.find(v => v.id === draft.value?.plugin_version_id))
const stageChoices = computed(() => {
  const hooks = selectedVersion.value?.capabilities.hooks ?? []
  const stages = ['ingress', 'request', 'outbound', 'egress', 'reply', 'report']
  const choices: string[] = []
  for (let mask = 1; mask < (1 << stages.length); mask++) {
    const selected = stages.filter((stage, index) => (mask & (1 << index)) && hooks.includes(stage))
    if (selected.length && !choices.includes(selected.join(','))) choices.push(selected.join(','))
  }
  return choices
})
function changeStages(value: string) {
  if (!draft.value) return
  draft.value.stages = value.split(',')
  if (!draft.value.stages.some(stage => stage === 'ingress' || stage === 'egress')) draft.value.network_enabled = false
  if (!draft.value.stages.includes('reply')) draft.value.reply_network_enabled = false
  if (httpStages.value && (!draft.value.formats?.length || !draft.value.formats.every(format => supportedFormats.value.includes(format)))) draft.value.formats = ['openai']
  else if (explicitFormats.value && (!draft.value.formats?.length || !draft.value.formats.every(format => supportedFormats.value.includes(format)))) draft.value.formats = ['task']
  sampleStage.value = draft.value.stages[0]!
  if (draft.value.formats?.length === 1) sampleFormat.value = draft.value.formats[0]!
  dryResult.value = undefined
  if (draft.value.scope === 'account') void searchAccounts('')
}
function changeFormats(value: string) {
  if (!draft.value) return
  draft.value.formats = value ? value.split(',') : []
  if (draft.value.formats.length && !draft.value.formats.includes(sampleFormat.value)) sampleFormat.value = draft.value.formats[0]!
  dryResult.value = undefined
  if (draft.value.scope === 'account') void searchAccounts('')
}
function changeScope() {
  if (!draft.value) return
  if (draft.value.scope === 'account') {
    draft.value.group_ids = []
    draft.value.formats = ['openai']
  } else {
    draft.value.account_ids = []
    accountNames.value = {}
    accountSearch?.abort()
    draft.value.formats = []
  }
  changeStages(stageChoices.value.includes(draft.value.stages.join(',')) ? draft.value.stages.join(',') : stageChoices.value[0] ?? 'ingress')
}
async function searchAccounts(keyword: string) {
  accountSearch?.abort()
  const search = new AbortController()
  accountSearch = search
  const currentEpoch = epoch
  accountLoading.value = true
  try {
    const result = await adminAPI.accounts.list(1, 20, { search: keyword }, { signal: search.signal })
    if (search.signal.aborted || controller.signal.aborted || epoch !== currentEpoch || draft.value?.scope !== 'account') return
    accountOptions.value = result.items.filter(account => platformUsesGateway(account.platform, 'openai') && draft.value?.formats?.every(format => ['openai', 'openai_responses'].includes(format)) || nativeClaudeFormat.value && account.platform === 'anthropic' || nativeProtocolFormats.value && ['gemini', 'antigravity'].includes(account.platform) || reportOnly.value && nativeProtocolFormats.value && account.platform === 'anthropic').map(account => ({ value: account.id, label: `${account.name} (#${account.id})` }))
  } catch (e) { if (!search.signal.aborted && !controller.signal.aborted && epoch === currentEpoch) error.value = errorText(e) }
  finally { if (accountSearch === search) accountLoading.value = false }
}
function addAccount(value: string | number | boolean | null) {
  if (!draft.value || typeof value !== 'number' || !Number.isSafeInteger(value) || value <= 0 || (draft.value.account_ids?.length ?? 0) >= 100) return
  draft.value.account_ids ??= []
  if (draft.value.account_ids.includes(value)) return
  draft.value.account_ids.push(value)
  accountNames.value[value] = accountOptions.value.find(option => option.value === value)?.label ?? `#${value}`
}
function removeAccount(id: number) {
  if (!draft.value) return
  draft.value.account_ids = draft.value.account_ids?.filter(value => value !== id)
  delete accountNames.value[id]
}
const groupName = (id: number) => groups.value.find(g => g.id === id)?.name ?? `#${id}`
const errorText = (e: unknown) => (e as { response?: { data?: { message?: string } } }).response?.data?.message ?? (e as Error).message

async function load() {
  loading.value = true
  try {
    const [status, catalog, availableGroups] = await Promise.all([getRequestProcessingStatus(controller.signal), listNewAPIPlugins(controller.signal), adminAPI.groups.getAllIncludingInactive()])
    if (controller.signal.aborted) return
    instances.value = status.items
    replyAuditFailures.value = status.reply_audit_failures ?? 0
    reportStats.value = status.report_stats
    versions.value = catalog.plugins.filter(p => p.capabilities.transform && p.capabilities.hooks?.some(h => ['ingress','request','outbound','egress','reply','report'].includes(h)))
    groups.value = availableGroups
    error.value = ''
  } catch (e) { if (!controller.signal.aborted) error.value = errorText(e) }
  finally { if (!controller.signal.aborted) loading.value = false }
}
function schemaDefaults(field?: PluginConfigField): unknown {
  if (field?.default !== undefined) return field.default
  if (field?.type !== 'object') return undefined
  return Object.fromEntries(Object.entries(field.properties ?? {}).map(([key,value]) => [key,schemaDefaults(value)]).filter(([,value]) => value !== undefined))
}
function resetConfig() {
  if (draft.value && !stageChoices.value.length && selectedVersion.value?.capabilities.hooks?.some(stage => stage === 'request' || stage === 'outbound' || stage === 'reply')) { draft.value.scope = 'account'; changeScope() }
  if (draft.value && !stageChoices.value.includes(draft.value.stages.join(','))) changeStages(stageChoices.value[0] ?? 'ingress')
  if (draft.value && channelScoped.value && (!draft.value.formats?.length || !draft.value.formats.every(format => supportedFormats.value.includes(format)))) changeFormats('openai')
  const config = JSON.parse(JSON.stringify(schemaDefaults(selectedVersion.value?.meta.configSchema) ?? {})) as Record<string, unknown>
  if (selectedVersion.value?.key === 'request-redact' && !draft.value?.stages.includes('egress')) config.finalCheck = 'off'
  configText.value = JSON.stringify(config, null, 2)
  dryResult.value = undefined
}
function create() {
  accountSearch?.abort(); accountOptions.value = []; accountNames.value = {}
  epoch++
  sampleStage.value = 'ingress'; sampleFormat.value = 'openai'
  error.value = ''; message.value = ''; modelText.value = ''
  draft.value = { id: 0, name: '', plugin_version_id: versions.value[0]!.id, plugin_key: '', plugin_version: '', enabled: false, network_enabled: false, reply_network_enabled: false, scope: 'group', group_ids: [], account_ids: [], models: [], position: 0, stages: ['ingress'], formats: [], revision: 0, updated_at: '' }
  resetConfig()
}
function closeEditor() { if (!busy.value) { epoch++; accountSearch?.abort(); accountOptions.value = []; accountNames.value = {}; draft.value = undefined; dryResult.value = undefined } }
async function mutation(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true; error.value = ''; message.value = ''
  const currentEpoch = epoch
  try { await stepUp.run(async () => { if (controller.signal.aborted || currentEpoch !== epoch) return; await action() }) }
  catch (e) { if (!controller.signal.aborted && !isStepUpCancelled(e)) error.value = errorText(e) }
  finally { if (!controller.signal.aborted) busy.value = false }
}
async function edit(item: RequestPluginInstance) {
  epoch++; accountSearch?.abort(); accountOptions.value = []; accountNames.value = {}
  await mutation(async () => {
    const [config, history] = await Promise.all([getRequestPluginConfig(item.id, controller.signal), listNewAPIPluginVersions(item.plugin_key)])
    if (controller.signal.aborted) return
    for (const version of history) if (!versions.value.some(v => v.id === version.id)) versions.value.push(version)
    draft.value = { ...item, group_ids: [...item.group_ids], models: [...item.models], stages: [...item.stages], formats: [...(item.formats ?? [])], account_ids: [...(item.account_ids ?? [])] }
    sampleStage.value = item.stages[0] ?? 'ingress'
    if (item.formats?.length === 1) sampleFormat.value = item.formats[0]!
    configText.value = JSON.stringify(config, null, 2); modelText.value = item.models.join('\n'); dryResult.value = undefined
  })
}
function jsonObject(text: string): Record<string, unknown> {
  const value: unknown = JSON.parse(text)
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(t('requestPlugins.invalidJSON'))
  return value as Record<string, unknown>
}
async function save() {
  if (!draft.value) return
  let config: Record<string, unknown>
  try { config = jsonObject(configText.value) } catch { error.value = t('requestPlugins.invalidJSON'); return }
  const input = { ...draft.value, config, group_ids: draft.value.scope === 'group' ? [...draft.value.group_ids] : [], account_ids: draft.value.scope === 'account' ? [...(draft.value.account_ids ?? [])] : [], models: modelText.value.split('\n').map(v => v.trim()).filter(Boolean) }
  await mutation(async () => { await saveRequestPlugin(input, controller.signal); if (controller.signal.aborted) return; accountSearch?.abort(); draft.value = undefined; await load(); message.value = t('requestPlugins.saved') })
}
async function toggle(item: RequestPluginInstance) {
  const input = { ...item, enabled: !item.enabled }
  await mutation(async () => { await saveRequestPlugin(input, controller.signal); await load(); if (!controller.signal.aborted) message.value = t('requestPlugins.saved') })
}
async function dryRun() {
  if (!draft.value) return
  let config: Record<string, unknown>, body: Record<string, unknown>
  try { config = jsonObject(configText.value); body = jsonObject(sampleText.value) } catch { error.value = t('requestPlugins.invalidJSON'); return }
  const stage = draft.value.scope === 'account' && sampleStage.value === 'ingress' ? 'late_ingress' : sampleStage.value
  const input = { plugin_version_id: draft.value.plugin_version_id, config, body, format: sampleFormat.value, stage }
  await mutation(async () => { const result = await dryRunRequestPlugin(input, controller.signal); if (!controller.signal.aborted) dryResult.value = result })
}
async function loadRuns() {
  const sequence = ++runsSequence
  loading.value = true
  try { const result = await listRequestPluginRuns(page.value, requestID.value, controller.signal); if (!controller.signal.aborted && sequence === runsSequence) { runs.value = result.items; total.value = result.total; error.value = '' } }
  catch (e) { if (!controller.signal.aborted && sequence === runsSequence) error.value = errorText(e) }
  finally { if (!controller.signal.aborted && sequence === runsSequence) loading.value = false }
}
onMounted(load)
onBeforeUnmount(() => { epoch++; accountSearch?.abort(); controller.abort() })
</script>
