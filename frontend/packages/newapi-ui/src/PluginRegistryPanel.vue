<template>
  <section class="space-y-4">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div><h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t(`pluginRegistry.titles.${mode}`) }}</h3><p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t(`pluginRegistry.hints.${mode}`) }}</p></div>
      <div class="flex items-center gap-2"><HostSelect v-if="mode === 'review'" v-model="status" :select-component="selectComponent" class="min-w-32" :label="t('common.status')"
        :options="['submitted', 'published', 'rejected', 'withdrawn'].map(value => ({ value, label: t(`pluginRegistry.statuses.${value}`) }))" /><button v-if="mode === 'mine' && canWrite" type="button" class="btn btn-primary btn-sm" :disabled="!data?.namespace" @click="createDraft">{{ t('pluginRegistry.create') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="load">{{ t('common.refresh') }}</button></div>
    </header>
    <p v-if="error" role="alert" class="rounded-xl border border-red-200 bg-red-50/90 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">{{ error }}</p>
    <p v-if="message" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50/90 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">{{ message }}</p>
    <div v-if="mode === 'mine'" class="max-w-xs"><span class="input-label">{{ t('pluginRegistry.playground.template') }}</span><HostSelect v-model="templateKind" :select-component="selectComponent" :label="t('pluginRegistry.playground.template')" :disabled="loading"
        :options="templateOptions" /></div>
    <div class="card overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full text-left text-sm"><thead class="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-400"><tr><th class="px-4 py-3">{{ t('pluginRegistry.plugin') }}</th><th class="px-4 py-3">{{ t('common.status') }}</th><th class="px-4 py-3">{{ t('pluginRegistry.author') }}</th><th class="px-4 py-3">{{ t('pluginRegistry.updated') }}</th><th class="px-4 py-3">{{ t('common.actions') }}</th></tr></thead><tbody class="divide-y divide-gray-100 dark:divide-dark-700"><tr v-for="item in data?.items ?? []" :key="item.id" class="transition-colors hover:bg-gray-50/70 dark:hover:bg-dark-800/40"><td class="px-4 py-4"><p class="font-medium text-gray-900 dark:text-white">{{ item.meta.name }}</p><p class="mt-1 font-mono text-xs text-gray-500 dark:text-dark-400">{{ item.key }} · v{{ item.version }}</p></td><td class="px-4 py-4 text-xs"><span class="badge" :class="item.status === 'published' ? 'badge-success' : item.status === 'rejected' ? 'badge-danger' : item.status === 'submitted' ? 'badge-warning' : 'badge-gray'">{{ t(`pluginRegistry.statuses.${item.status}`) }}</span></td><td class="px-4 py-4 text-xs">#{{ item.author_id }}</td><td class="whitespace-nowrap px-4 py-4 text-xs text-gray-500 dark:text-dark-400">{{ new Date(item.updated_at).toLocaleString() }}</td><td class="px-4 py-4"><button type="button" class="btn btn-secondary btn-sm" @click="open(item.id)">{{ t('pluginRegistry.open') }}</button></td></tr><tr v-if="!data?.items.length"><td colspan="5" class="px-4 py-10 text-center text-gray-500 dark:text-dark-400">{{ t(loading ? 'common.loading' : 'pluginRegistry.empty') }}</td></tr></tbody></table>
      </div>
    </div>
    <component :is="pagination" v-if="data && data.total > 0" :page="page" :page-size="20" :total="data.total" @update:page="page = $event" />
    <component :is="dialog" :show="editorOpen" :title="t('pluginRegistry.editor')" width="wide" @close="close">
      <div class="space-y-4">
        <p v-if="detailError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ detailError }}</p>
        <p v-if="message" role="status" class="text-sm text-emerald-700 dark:text-emerald-400">{{ message }}</p>
        <p v-if="detailLoading" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
        <template v-else>
          <p class="break-words text-xs leading-6 text-gray-500">{{ t('pluginRegistry.namespaceHint', { namespace: data?.namespace }) }}</p>
          <dl v-if="detail" class="grid gap-3 text-xs sm:grid-cols-2"><div><dt class="text-gray-500">{{ t('pluginRegistry.plugin') }}</dt><dd>{{ detail.key }} v{{ detail.version }}</dd></div><div><dt class="text-gray-500">{{ t('common.status') }}</dt><dd>{{ t(`pluginRegistry.statuses.${detail.status}`) }} · {{ t('pluginRegistry.revision') }} {{ detail.revision }}</dd></div><div class="sm:col-span-2"><dt class="text-gray-500">SHA256</dt><dd class="break-all">{{ detail.sha256 }}</dd></div></dl>
          <p v-if="detail?.review_reason" class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-900">{{ t('pluginRegistry.reviewReason') }}: {{ detail.review_reason }}</p>
          <label class="block"><span class="input-label">plugin.js</span><textarea v-model="code" class="input min-h-72 font-mono text-xs leading-5" rows="16" :readonly="!editable" spellcheck="false" /></label>
          <label v-if="editable" class="btn btn-secondary btn-sm cursor-pointer">{{ t('pluginRegistry.upload') }}<input type="file" accept=".js,text/javascript" class="hidden" :disabled="busy" @change="upload" /></label>
          <label class="block"><span class="input-label">{{ t('pluginRegistry.readme') }}</span><textarea v-model="readme" class="input text-sm" rows="4" :readonly="!editable" maxlength="65536" /></label>
          <section v-if="mode === 'mine'" class="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-700" data-playground>
            <h4 class="text-sm font-semibold">{{ t('pluginRegistry.playground.title') }}</h4>
            <p class="text-xs leading-6 text-gray-500">{{ t('pluginRegistry.playground.hint') }}</p>
            <label class="block"><span class="input-label">{{ t('pluginRegistry.playground.fixture') }}</span><textarea v-model="fixtureText" class="input font-mono text-xs" rows="7" spellcheck="false" maxlength="70000" :disabled="dryRunning" /></label>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || dryRunning || !code" @click="dryRun">{{ t(dryRunning ? 'common.loading' : 'pluginRegistry.playground.run') }}</button>
            <p v-if="fixtureError" role="alert" class="text-sm text-red-600">{{ fixtureError }}</p>
            <div v-if="fixtureResult" class="space-y-2"><p class="break-all text-xs text-gray-500">{{ fixtureResult.duration_ms }} ms · SHA256 {{ fixtureResult.sha256 }}</p><pre class="max-h-80 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">{{ JSON.stringify(fixtureResult.result, null, 2) }}</pre><p class="text-xs text-gray-500">{{ t('pluginRegistry.playground.resultHint') }}</p></div>
          </section>
          <p v-if="detail" class="text-xs leading-6 text-gray-500">{{ [...(detail.compatibility?.blockers ?? []), ...(detail.compatibility?.warnings ?? [])].join('; ') }}</p>
          <div v-if="mode === 'review' || detail?.status === 'submitted' || detail?.status === 'published'" class="space-y-1"><label class="input-label" for="plugin-review-reason">{{ t('pluginRegistry.reviewReason') }}</label><input id="plugin-review-reason" v-model.trim="reason" class="input" maxlength="2000" /></div>
          <div class="flex flex-wrap gap-2">
            <button v-if="editable" type="button" class="btn btn-primary btn-sm" :disabled="busy" @click="save">{{ t('pluginRegistry.validateSave') }}</button>
            <button v-if="detail && editable" type="button" class="btn btn-secondary btn-sm" :disabled="busy || dirty" @click="transition('submit')">{{ t('pluginRegistry.submit') }}</button>
            <button v-if="canWrite && mode === 'review' && detail?.status === 'submitted'" type="button" class="btn btn-primary btn-sm" :disabled="busy" @click="transition('publish')">{{ t('pluginRegistry.publish') }}</button>
            <button v-if="canWrite && mode === 'review' && detail?.status === 'submitted'" type="button" class="btn btn-secondary btn-sm" :disabled="busy || reason.length < 4" @click="transition('reject')">{{ t('pluginRegistry.reject') }}</button>
            <button v-if="canWrite && mode !== 'market' && detail && ['submitted','published'].includes(detail.status)" type="button" class="btn btn-secondary btn-sm" :disabled="busy || reason.length < 4" @click="transition('withdraw')">{{ t('pluginRegistry.withdraw') }}</button>
            <button v-if="canWrite && canInstall && detail?.status === 'published'" type="button" class="btn btn-primary btn-sm" :disabled="busy" @click="install">{{ t('pluginRegistry.install') }}</button>
          </div>
          <p class="text-xs leading-6 text-gray-500">{{ t('pluginRegistry.immutableHint') }}</p>
        </template>
      </div>
    </component>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, toRaw, watch, type Component } from 'vue'
import HostSelect from './HostSelect.vue'
import type { PluginDeveloperTemplate, PluginDryRunResult, PluginFixture, PluginSubmission, PluginSubmissionPage, RegistryMode, PluginRegistryClient, PluginTranslator, RunPluginMutation } from './types'

const props = withDefaults(defineProps<{ selectComponent?: Component; mode: RegistryMode; canInstall?: boolean; canWrite?: boolean; client: PluginRegistryClient; t: PluginTranslator; dialogComponent: Component; paginationComponent: Component; runMutation: RunPluginMutation; isCancelled?: (error: unknown) => boolean }>(), { canInstall: false, canWrite: false, isCancelled: () => false })
const dialog = computed(() => toRaw(props.dialogComponent))
const pagination = computed(() => toRaw(props.paginationComponent))
const emit = defineEmits<{ installed: [] }>()
const t: PluginTranslator = (key, values) => props.t(key, values)
const data = ref<PluginSubmissionPage>()
const page = ref(1)
const status = ref('submitted')
const loading = ref(false)
const error = ref('')
const message = ref('')
const editorOpen = ref(false)
const detail = ref<PluginSubmission>()
const detailError = ref('')
const detailLoading = ref(false)
const code = ref('')
const readme = ref('')
const reason = ref('')
const busy = ref(false)
const templateKind = ref<PluginDeveloperTemplate['kind']>('ingress')
const templateOptions = computed(() => (['ingress', 'task', 'provider'] as const).map(value => ({
  value, label: t(`pluginRegistry.playground.kinds.${value}`)
})))
const templates = ref<PluginDeveloperTemplate[]>([])
const fixtureText = ref('')
const fixtureError = ref('')
const fixtureResult = ref<PluginDryRunResult>()
const dryRunning = ref(false)
const editable = computed(() => props.canWrite && props.mode === 'mine' && (!detail.value || ['draft', 'rejected'].includes(detail.value.status)))
const dirty = computed(() => !!detail.value && (code.value !== detail.value.source || readme.value !== detail.value.readme))
let listController: AbortController | undefined
let detailController: AbortController | undefined
let fixtureController: AbortController | undefined
let scopeEpoch = 0
let editorEpoch = 0
const errorText = (e: unknown) => (e as { response?: { data?: { message?: string } }; message?: string }).response?.data?.message ?? (e as Error).message
async function load() {
  listController?.abort()
  const controller = listController = new AbortController()
  loading.value = true
  try {
    const [result, examples] = await Promise.all([props.client.listPluginSubmissions(props.mode, page.value, status.value, controller.signal), props.mode === 'mine' ? props.client.listPluginTemplates(controller.signal) : Promise.resolve([])])
    if (!controller.signal.aborted) { data.value = result; templates.value = examples; error.value = '' }
  }
  catch (e) { if (!controller.signal.aborted) error.value = errorText(e) }
  finally { if (!controller.signal.aborted) loading.value = false }
}
async function open(id: number) {
  editorEpoch++
  resetFixture()
  detailController?.abort()
  const controller = detailController = new AbortController()
  detail.value = undefined; editorOpen.value = true; detailLoading.value = true; detailError.value = ''; reason.value = ''
  try {
    const result = await props.client.getPluginSubmission(id, props.mode === 'review', controller.signal)
    if (!controller.signal.aborted) {
      detail.value = result; code.value = result.source ?? ''; readme.value = result.readme
      const kind = result.capabilities?.task ? 'task' : result.capabilities?.provider ? 'provider' : 'ingress'
      fixtureText.value = JSON.stringify(templates.value.find(item => item.kind === kind)?.fixture ?? { hook: 'ingress', args: [{ stage: 'ingress', config: {} }, { body: {} }] }, null, 2)
    }
  }
  catch (e) { if (!controller.signal.aborted) detailError.value = errorText(e) }
  finally { if (!controller.signal.aborted) detailLoading.value = false }
}
function close(force = false) { if (busy.value && !force) return; editorEpoch++; detailController?.abort(); fixtureController?.abort(); editorOpen.value = false; detail.value = undefined; detailLoading.value = false; resetFixture() }
function createDraft() {
  if (!props.canWrite) return
  editorEpoch++
  const template = templates.value.find(item => item.kind === templateKind.value)
  if (!template) { error.value = t('pluginRegistry.playground.templateUnavailable'); return }
  detail.value = undefined; detailError.value = ''; readme.value = ''; reason.value = ''; editorOpen.value = true
  code.value = template.source
  resetFixture()
  fixtureText.value = JSON.stringify(template.fixture, null, 2)
}
function resetFixture() { fixtureController?.abort(); dryRunning.value = false; fixtureError.value = ''; fixtureResult.value = undefined }
async function dryRun() {
  if (dryRunning.value || props.mode !== 'mine') return
  fixtureError.value = ''; fixtureResult.value = undefined
  let fixture: PluginFixture
  try {
    fixture = JSON.parse(fixtureText.value)
    if (!fixture || typeof fixture.hook !== 'string' || !Array.isArray(fixture.args)) throw new Error()
  } catch { fixtureError.value = t('pluginRegistry.playground.invalidFixture'); return }
  const controller = fixtureController = new AbortController()
  dryRunning.value = true
  try { const result = await props.client.dryRunPlugin({ source: code.value, hook: fixture.hook, args: fixture.args }, controller.signal); if (!controller.signal.aborted) fixtureResult.value = result }
  catch (e) { if (!controller.signal.aborted) fixtureError.value = errorText(e) }
  finally { if (!controller.signal.aborted) dryRunning.value = false }
}
async function upload(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]; input.value = ''
  if (!file || !editable.value || busy.value) return
  if (file.size > 1048576) { detailError.value = t('pluginRegistry.tooLarge'); return }
  const epoch = editorEpoch
  try {
    const source = await file.text()
    if (epoch === editorEpoch && editorOpen.value && editable.value) code.value = source
  } catch (e) { if (epoch === editorEpoch && editorOpen.value) detailError.value = errorText(e) }
}
async function save() {
  if (!editable.value || busy.value) return
  const epoch = scopeEpoch
  busy.value = true; detailError.value = ''
  try { const saved = await props.client.savePluginSubmission({ id: detail.value?.id, revision: detail.value?.revision, source: code.value, readme: readme.value }); if (epoch !== scopeEpoch) return; detail.value = saved; message.value = t('pluginRegistry.saved'); await load() }
  catch (e) { if (epoch === scopeEpoch) detailError.value = errorText(e) }
  finally { if (epoch === scopeEpoch) busy.value = false }
}
async function transition(action: string) {
  if (!props.canWrite || busy.value || !detail.value) return
  const item = detail.value
  const epoch = scopeEpoch
  const admin = props.mode === 'review'
  busy.value = true; detailError.value = ''
  try {
    const perform = () => { if (epoch !== scopeEpoch) throw new Error(t('common.tryAgain')); return props.client.transitionPluginSubmission(item.id, item.revision, action, reason.value, admin) }
    if (admin) await props.runMutation(perform); else await perform()
    if (epoch !== scopeEpoch) return
    await open(item.id); await load()
  } catch (e) { if (epoch === scopeEpoch && !props.isCancelled(e)) detailError.value = errorText(e) }
  finally { if (epoch === scopeEpoch) busy.value = false }
}
async function install() {
  if (!props.canWrite || !props.canInstall || busy.value || !detail.value) return
  const item = detail.value
  const epoch = scopeEpoch
  busy.value = true; detailError.value = ''
  try { await props.runMutation(() => { if (epoch !== scopeEpoch) throw new Error(t('common.tryAgain')); return props.client.installPluginSubmission(item.id, item.sha256) }); if (epoch !== scopeEpoch) return; message.value = t('pluginRegistry.installed'); emit('installed') }
  catch (e) { if (epoch === scopeEpoch && !props.isCancelled(e)) detailError.value = errorText(e) }
  finally { if (epoch === scopeEpoch) busy.value = false }
}
watch([() => props.mode, status], () => { page.value = 1 })
watch([code, fixtureText], resetFixture)
watch([() => props.mode, status, page], () => { scopeEpoch++; busy.value = false; close(true); data.value = undefined; void load() }, { immediate: true })
onBeforeUnmount(() => { scopeEpoch++; editorEpoch++; listController?.abort(); detailController?.abort(); fixtureController?.abort() })
</script>
