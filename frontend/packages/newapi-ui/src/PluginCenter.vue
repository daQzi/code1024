<template>
  <section class="space-y-5">
    <header v-if="showHeader" class="border-b border-gray-200 pb-5 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.plugins.center.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('admin.plugins.center.description') }}</p>
    </header>
    <div class="flex flex-wrap items-center justify-between gap-3">
      <nav class="flex flex-wrap gap-2" :aria-label="t('admin.plugins.center.title')">
        <button v-for="value in tabs" :key="value" type="button" class="btn btn-sm" :class="tab === value ? 'btn-primary' : 'btn-secondary'" :aria-pressed="tab === value" @click="tab = value">{{ t(`admin.plugins.center.${value}`) }}</button>
      </nav>
      <div class="flex flex-wrap items-center gap-2">
        <slot name="header-actions" />
        <button type="button" class="btn btn-primary btn-sm" :disabled="busy || !editable" @click="openUpload()">{{ t('admin.plugins.detail.upload') }}</button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="load">{{ t('common.refresh') }}</button>
      </div>
    </div>
    <p v-if="error" role="alert" class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">{{ error }}</p>
    <p v-if="message" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">{{ message }}</p>
    <slot v-if="tab === 'cleanup'" name="cleanup" />
    <slot v-else-if="tab === 'developer' || tab === 'review'" name="registry" :mode="tab === 'review' ? 'review' : 'mine'" />
    <template v-else>
      <div v-if="tab === 'market'" class="flex flex-wrap gap-2">
        <button v-for="value in marketSources" :key="value" type="button" class="btn btn-sm" :class="marketSource === value ? 'btn-primary' : 'btn-secondary'" :aria-pressed="marketSource === value" @click="marketSource = value">{{ t(`admin.plugins.center.sources.${value}`) }}</button>
      </div>
      <slot v-if="tab === 'market' && marketSource === 'community'" name="community-market" :reload="load" />
      <slot v-else-if="tab === 'market' && marketSource === 'external'" name="external-market" :plugins="catalog?.plugins ?? []" :reload="load" />
      <div v-else class="space-y-4">
        <div class="flex flex-wrap items-center gap-3">
          <input v-model.trim="search" type="search" class="input min-w-48 flex-1" :placeholder="t('admin.plugins.center.search')" :aria-label="t('admin.plugins.center.search')" />
          <div class="flex flex-wrap gap-1 rounded-xl bg-gray-100 p-1 dark:bg-dark-900" :aria-label="t('admin.plugins.center.kind')">
            <button v-for="value in kinds" :key="value" type="button" class="rounded-lg px-3 py-1.5 text-xs font-medium" :class="kind === value ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500 hover:text-gray-900 dark:text-dark-400'" :aria-pressed="kind === value" @click="kind = value">{{ t(`admin.plugins.center.kinds.${value}`) }} <span class="ml-1 opacity-70">{{ value === 'all' ? entries.length : entries.filter(entry => entry.kinds.includes(value)).length }}</span></button>
          </div>
          <HostSelect v-model="source" :select-component="selectComponent" class="w-full sm:w-36" :label="t('admin.plugins.center.source')" :options="['all', 'system', 'overridden', 'uploaded', 'community', 'external'].map(value => ({ value, label: t(`admin.plugins.center.sources.${value}`) }))" />
          <div class="flex rounded-lg border border-gray-200 p-1 dark:border-dark-700">
            <button v-for="value in ['cards', 'table'] as const" :key="value" type="button" class="rounded-md p-1.5" :class="view === value ? 'bg-primary-50 text-primary-600 dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500'" :aria-label="t(`admin.plugins.center.${value}`)" :aria-pressed="view === value" @click="view = value">
              <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><template v-if="value === 'cards'"><rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><rect x="14" y="14" width="7" height="7" rx="1" /></template><template v-else><rect x="3" y="3" width="18" height="18" rx="2" /><path d="M3 9h18M3 15h18M9 3v18" /></template></svg>
            </button>
          </div>
        </div>
        <p v-if="tab === 'market'" class="text-sm text-gray-500 dark:text-dark-400">{{ t('admin.plugins.center.factoryHint') }}</p>
        <p v-if="loading && !entries.length" role="status" class="py-10 text-center text-sm text-gray-500">{{ t('common.loading') }}</p>
        <p v-else-if="!filtered.length" class="rounded-xl border border-dashed border-gray-300 py-12 text-center text-sm text-gray-500 dark:border-dark-600">{{ t('admin.plugins.center.empty') }}</p>
        <div v-else-if="view === 'cards'" class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          <article v-for="entry in filtered" :key="entry.key" class="flex min-w-0 flex-col rounded-2xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800">
            <div class="flex items-start gap-3">
              <PluginIcon :icon="entry.plugin.meta.icon" :name="entry.name" :icon-component="iconComponent" />
              <button type="button" class="min-w-0 flex-1 text-left focus-visible:ring-2 focus-visible:ring-primary-500" :aria-label="`${t('admin.plugins.center.details')} ${entry.name}`" data-plugin-details @click="showEntry(entry)">
                <h3 class="break-words text-base font-semibold text-gray-900 dark:text-white">{{ entry.name }}</h3><p class="mt-1 break-all font-mono text-xs text-gray-500 dark:text-dark-400">{{ entry.key }}</p>
              </button>
              <details class="relative shrink-0" @keydown.esc="closeMenu">
                <summary class="cursor-pointer list-none rounded-lg px-2 py-1 text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-700" :aria-label="t('admin.plugins.detail.actions')">•••</summary>
                <div class="absolute right-0 top-9 z-20 w-56 rounded-xl border border-gray-200 bg-white p-1 shadow-lg dark:border-dark-600 dark:bg-dark-800" @click="closeMenu">
                  <button type="button" class="menu-action" @click="showEntry(entry)">{{ t('admin.plugins.center.details') }}</button>
                  <button type="button" class="menu-action" :disabled="!editable || busy" @click="openUpload(entry.key)">{{ t('admin.plugins.detail.uploadVersion') }}</button>
                  <button v-if="client.remove" type="button" class="menu-action text-red-600 dark:text-red-400" :disabled="!editable || busy || !canDelete(entry.plugin)" @click="requestDelete(entry.plugin)">{{ t('admin.plugins.detail.deleteVersion') }}</button>
                </div>
              </details>
            </div>
            <div class="mt-4 flex flex-wrap gap-1.5"><span class="badge badge-gray">{{ t(`admin.plugins.center.sources.${entry.source}`) }}</span><span v-for="capability in entry.kinds" :key="capability" class="badge badge-primary">{{ t(`admin.plugins.center.kinds.${capability}`) }}</span><span class="badge badge-gray font-mono">v{{ entry.plugin.version }}</span></div>
            <p class="mt-3 line-clamp-2 min-h-12 text-sm leading-6 text-gray-500 dark:text-dark-400">{{ entry.description }}</p>
            <div class="mb-5 mt-3 space-y-2"><p class="text-xs font-medium text-gray-500">{{ t(entry.plugin.capabilities.task || entry.plugin.capabilities.provider ? 'admin.plugins.center.models' : 'admin.plugins.detail.hooks') }}</p><PluginModelList :models="entry.plugin.capabilities.task || entry.plugin.capabilities.provider ? entry.plugin.meta.models ?? [] : entry.plugin.capabilities.hooks ?? []" /></div>
            <div class="mt-auto flex items-center justify-between gap-3 border-t border-gray-200 pt-4 dark:border-dark-700">
              <span class="text-xs" :class="entry.ready ? 'text-gray-500 dark:text-dark-400' : 'text-amber-600 dark:text-amber-400'">{{ status(entry) }}</span>
              <button v-if="'id' in entry.plugin && (entry.plugin.capabilities.task || entry.plugin.capabilities.provider)" type="button" role="switch" :aria-checked="entry.plugin.enabled" :aria-label="`${t(entry.plugin.enabled ? 'admin.plugins.disable' : 'admin.plugins.enable')} ${entry.name}`" class="relative h-6 w-10 shrink-0 rounded-full transition-colors disabled:cursor-not-allowed disabled:opacity-40" :class="entry.plugin.enabled ? 'bg-primary-600' : 'bg-gray-300 dark:bg-dark-600'" :disabled="!editable || busy || !entry.ready && !entry.plugin.enabled" @click="activate(entry.plugin)"><span class="absolute top-0.5 h-5 w-5 rounded-full bg-white shadow-sm transition-all" :class="entry.plugin.enabled ? 'left-[18px]' : 'left-0.5'" /></button>
              <button v-else-if="!('id' in entry.plugin)" type="button" class="btn btn-secondary btn-sm" :disabled="!editable || busy" @click="restore(entry.key)">{{ t('admin.plugins.js.installBuiltin') }}</button>
            </div>
          </article>
        </div>
        <div v-else class="overflow-x-auto rounded-xl border border-gray-200 dark:border-dark-700">
          <table class="w-full text-left text-sm"><thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-900"><tr><th v-for="column in ['plugin', 'version', 'source', 'kind', 'models', 'status', 'actions']" :key="column" class="whitespace-nowrap px-4 py-3 font-medium">{{ t(`admin.plugins.detail.${column}`) }}</th></tr></thead>
            <tbody class="divide-y divide-gray-200 dark:divide-dark-700"><tr v-for="entry in filtered" :key="entry.key">
              <td class="px-4 py-3"><div class="flex items-center gap-3"><PluginIcon :icon="entry.plugin.meta.icon" :name="entry.name" :icon-component="iconComponent" /><button type="button" class="text-left" data-plugin-details @click="showEntry(entry)"><span class="font-medium">{{ entry.name }}</span><code class="mt-1 block text-xs text-gray-500">{{ entry.key }}</code></button></div></td>
              <td class="whitespace-nowrap px-4 py-3 font-mono text-xs">v{{ entry.plugin.version }}</td><td class="whitespace-nowrap px-4 py-3 text-xs">{{ t(`admin.plugins.center.sources.${entry.source}`) }}</td><td class="px-4 py-3"><div class="flex flex-wrap gap-1"><span v-for="capability in entry.kinds" :key="capability" class="badge badge-primary">{{ t(`admin.plugins.center.kinds.${capability}`) }}</span></div></td><td class="px-4 py-3 font-mono text-xs">{{ entry.plugin.meta.models?.length ?? 0 }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-xs"><button v-if="'id' in entry.plugin && (entry.plugin.capabilities.task || entry.plugin.capabilities.provider)" type="button" role="switch" :aria-checked="entry.plugin.enabled" class="btn btn-secondary btn-sm" :disabled="!editable || busy || !entry.ready && !entry.plugin.enabled" @click="activate(entry.plugin)">{{ status(entry) }}</button><span v-else>{{ status(entry) }}</span></td><td class="px-4 py-3"><button type="button" class="btn btn-secondary btn-sm" @click="showEntry(entry)">{{ t('admin.plugins.center.details') }}</button></td>
            </tr></tbody>
          </table>
        </div>
        <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('admin.plugins.js.versionNote') }}</p>
      </div>
    </template>
    <p class="border-t border-gray-100 pt-4 text-xs text-gray-500 dark:border-dark-700">{{ t('admin.plugins.center.attribution') }} <a href="https://github.com/QuantumNous/new-api" target="_blank" rel="noopener noreferrer" class="text-primary-600 hover:underline dark:text-primary-400">New API</a> · {{ catalog?.baseline?.slice(0, 12) }}</p>
    <component :is="dialog" :show="!!selected" :title="selected ? localized(selected.meta.name_i18n) || selected.meta.name : ''" width="extra-wide" placement="right" @close="closeDetails">
      <template v-if="selected">
        <p v-if="error" role="alert" class="mb-4 text-sm text-red-600">{{ error }}</p>
        <PluginDetailPanel :key="selected.key" :plugin="selected" :factory="factories.get(selected.key)" :history="history" :history-loading="historyLoading" :client="client" :t="t" :locale="locale" :busy="busy" :editable="editable" :select-component="selectComponent" :icon-component="iconComponent" :has-source="!!slots.source" @activate="activate" @restore="restore" @remove="requestDelete" @upload="openUpload">
          <template #detail-actions><slot name="detail-actions" :plugin="selected" /></template><template #pricing-actions><slot name="pricing-actions" :plugin="selected" /></template><template #source="{ mode }"><slot name="source" :plugin="selected" :versions="history" :mode="mode" /></template>
        </PluginDetailPanel>
      </template>
    </component>
    <component :is="dialog" :show="uploadOpened" :title="t(uploadKey ? 'admin.plugins.detail.uploadVersion' : 'admin.plugins.detail.upload')" width="wide" @close="closeUpload">
      <form class="space-y-4" @submit.prevent="installUpload">
        <p v-if="uploadKey" class="text-sm text-gray-500">{{ t('admin.plugins.detail.targetPlugin') }} <code>{{ uploadKey }}</code></p>
        <label class="flex cursor-pointer flex-col items-center gap-2 rounded-xl border-2 border-dashed border-gray-300 p-6 text-center text-sm text-gray-500 dark:border-dark-600" @dragover.prevent @drop.prevent="dropFile">{{ fileName || t('admin.plugins.detail.chooseFile') }}<input type="file" accept=".js,text/javascript" :disabled="busy" @change="pickFile" /></label>
        <label class="block space-y-1 text-sm">{{ t('admin.plugins.detail.pasteSource') }}<textarea v-model="uploadSource" rows="10" spellcheck="false" class="input font-mono text-xs" :disabled="busy" /></label>
        <label class="flex items-start gap-2 text-xs leading-5 text-gray-500"><input v-model="allowOverride" type="checkbox" class="mt-1 rounded" :disabled="busy" />{{ t('admin.plugins.center.allowOverride') }}</label>
        <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
        <div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="busy" @click="closeUpload">{{ t('common.cancel') }}</button><button type="submit" class="btn btn-primary" :disabled="busy || !uploadSource.trim()">{{ t(busy ? 'common.loading' : 'admin.plugins.detail.install') }}</button></div>
      </form>
    </component>
    <component :is="dialog" :show="!!deleteTarget" :title="t('admin.plugins.detail.deleteVersion')" width="narrow" @close="closeDelete">
      <div v-if="deleteTarget" class="space-y-4"><p class="text-sm"><code>{{ deleteTarget.key }} · v{{ deleteTarget.version }}</code></p><p class="text-sm leading-6 text-gray-500">{{ t('admin.plugins.detail.deleteHint') }}</p><p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p><div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="busy" @click="closeDelete">{{ t('common.cancel') }}</button><button type="button" class="btn bg-red-600 text-white hover:bg-red-700" :disabled="busy" data-confirm-delete @click="removeVersion">{{ t('admin.plugins.detail.confirmDelete') }}</button></div></div>
    </component>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, toRaw, type Component } from 'vue'
import HostSelect from './HostSelect.vue'
import PluginDetailPanel from './PluginDetailPanel.vue'
import PluginIcon from './PluginIcon.vue'
import PluginModelList from './PluginModelList.vue'
import { localizedPluginText } from './pluginDisplay'
import type { NewAPIPluginCatalog, NewAPIPluginVersion, PluginFactory, PluginCenterClient, PluginTranslator } from './types'

const props = withDefaults(defineProps<{
  client: PluginCenterClient; t: PluginTranslator; locale: string; dialogComponent: Component
  selectComponent?: Component; iconComponent?: Component; editable?: boolean; showHeader?: boolean
  runMutation: (action: () => Promise<unknown>) => Promise<unknown>
  cancelMutation?: () => void; isCancelled?: (error: unknown) => boolean
}>(), { editable: false, showHeader: true, isCancelled: () => false })
const t: PluginTranslator = (key, values) => props.t(key, values)
const dialog = computed(() => toRaw(props.dialogComponent))
const slots = defineSlots<{
  'header-actions'?: () => unknown; cleanup?: () => unknown
  registry?: (props: { mode: 'review' | 'mine' }) => unknown
  'community-market'?: (props: { reload: () => Promise<void> }) => unknown
  'external-market'?: (props: { plugins: NewAPIPluginVersion[]; reload: () => Promise<void> }) => unknown
  'detail-actions'?: (props: { plugin: NewAPIPluginVersion | PluginFactory }) => unknown
  'pricing-actions'?: (props: { plugin: NewAPIPluginVersion | PluginFactory }) => unknown
  source?: (props: { plugin: NewAPIPluginVersion | PluginFactory; versions: NewAPIPluginVersion[]; mode: 'source' | 'diff' }) => unknown
}>()
const tabs = computed(() => ['installed', 'market', ...(slots.registry ? ['developer', 'review'] : []), ...(slots.cleanup ? ['cleanup'] : [])])
const marketSources = computed(() => ['system', ...(slots['community-market'] ? ['community'] : []), ...(slots['external-market'] ? ['external'] : [])])
const kinds = ['all', 'task', 'provider', 'transform']
interface Entry { key: string; name: string; description: string; source: string; kinds: string[]; ready: boolean; plugin: NewAPIPluginVersion | PluginFactory }
const catalog = ref<NewAPIPluginCatalog>()
const tab = ref('installed')
const marketSource = ref('system')
const search = ref('')
const kind = ref('all')
const source = ref('all')
const view = ref<'cards' | 'table'>('cards')
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const message = ref('')
const selected = ref<NewAPIPluginVersion | PluginFactory>()
const history = ref<NewAPIPluginVersion[]>([])
const historyLoading = ref(false)
const uploadOpened = ref(false)
const uploadKey = ref('')
const uploadSource = ref('')
const fileName = ref('')
const allowOverride = ref(false)
const deleteTarget = ref<NewAPIPluginVersion>()
const controller = new AbortController()
let detailRequest: AbortController | undefined
const localized = (value: Parameters<typeof localizedPluginText>[0]) => localizedPluginText(value, props.locale)
const errorText = (caught: unknown) => (caught as { response?: { data?: { message?: string } }; message?: string }).response?.data?.message ?? (caught as Error).message ?? t('admin.plugins.detail.operationFailed')
const factories = computed(() => new Map((catalog.value?.factories ?? []).map(factory => [factory.key, factory])))
function entryFor(plugin: NewAPIPluginVersion | PluginFactory): Entry {
  const factory = factories.value.get(plugin.key)
  let origin = 'uploaded'
  if (factory?.sha256 === plugin.sha256 || 'origin' in plugin && plugin.origin.startsWith('builtin')) origin = 'system'
  else if ('origin' in plugin && plugin.origin.startsWith('community:')) origin = 'community'
  else if ('origin' in plugin && plugin.origin.startsWith('market:')) origin = 'external'
  else if (factory) origin = 'overridden'
  return { key: plugin.key, name: localized(plugin.meta.name_i18n) || plugin.meta.name, description: localized(plugin.meta.description), source: origin, kinds: ['task', 'provider', 'transform'].filter(value => plugin.capabilities[value as 'task' | 'provider' | 'transform']), ready: plugin.compatibility?.runnable ?? false, plugin }
}
const entries = computed(() => (tab.value === 'market' ? catalog.value?.factories ?? [] : catalog.value?.plugins ?? []).map(entryFor))
const filtered = computed(() => entries.value.filter(entry => (kind.value === 'all' || entry.kinds.includes(kind.value)) && (source.value === 'all' || entry.source === source.value) && `${entry.name} ${entry.key} ${entry.description} ${entry.plugin.meta.models?.join(' ') ?? ''}`.toLowerCase().includes(search.value.toLowerCase())))
function status(entry: Entry) {
  if (!entry.ready) return t('admin.plugins.center.unsupported')
  if (!entry.plugin.capabilities.task && !entry.plugin.capabilities.provider) return t('admin.plugins.instanceControlled')
  return t('enabled' in entry.plugin && entry.plugin.enabled ? 'admin.plugins.enabled' : 'admin.plugins.disabled')
}
function canDelete(plugin: NewAPIPluginVersion | PluginFactory): plugin is NewAPIPluginVersion {
  return 'id' in plugin && !plugin.origin.startsWith('builtin') && factories.value.get(plugin.key)?.sha256 !== plugin.sha256
}
function closeMenu(event: Event) { (event.target as HTMLElement).closest('details')?.removeAttribute('open') }
async function load() {
  if (controller.signal.aborted) return
  loading.value = true
  try {
    const result = await props.client.list(controller.signal)
    if (!controller.signal.aborted) catalog.value = result
  } catch (caught) {
    if (!controller.signal.aborted) error.value = errorText(caught)
  } finally { if (!controller.signal.aborted) loading.value = false }
}
async function showEntry(entry: Entry) {
  error.value = ''
  selected.value = entry.plugin
  await refreshHistory(entry.key)
}
function closeDetails() {
  detailRequest?.abort()
  props.cancelMutation?.()
  selected.value = undefined
  history.value = []
}
async function refreshHistory(key: string) {
  detailRequest?.abort()
  const request = new AbortController()
  detailRequest = request
  history.value = []
  historyLoading.value = true
  try {
    const result = await props.client.versions(key, request.signal)
    if (!request.signal.aborted && selected.value?.key === key) history.value = result
  } catch (caught) {
    if (!request.signal.aborted) error.value = errorText(caught)
  } finally { if (!request.signal.aborted) historyLoading.value = false }
}
async function mutate(action: () => Promise<unknown>): Promise<boolean> {
  if (controller.signal.aborted || busy.value || !props.editable) return false
  busy.value = true
  error.value = ''
  message.value = ''
  try {
    let completed = false
    await props.runMutation(async () => {
      if (controller.signal.aborted || !props.editable) return
      await action()
      completed = true
    })
    if (controller.signal.aborted || !completed) return false
    await load()
    if (!error.value) message.value = t('admin.plugins.center.saved')
    if (selected.value) {
      const updated = catalog.value?.plugins.find(plugin => plugin.key === selected.value?.key)
      if (updated) { selected.value = updated; await refreshHistory(updated.key) }
      else closeDetails()
    }
    return true
  } catch (caught) {
    if (!controller.signal.aborted && !props.isCancelled(caught)) error.value = errorText(caught)
    return false
  } finally { busy.value = false }
}
function openUpload(key = '') {
  uploadKey.value = key
  uploadSource.value = ''
  fileName.value = ''
  allowOverride.value = false
  error.value = ''
  uploadOpened.value = true
}
function closeUpload() {
  props.cancelMutation?.()
  if (!busy.value) uploadOpened.value = false
}
async function readFile(file?: File) {
  if (!file || busy.value) return
  if (file.size > 1048576) { error.value = t('admin.plugins.js.tooLarge'); return }
  try { uploadSource.value = await file.text(); fileName.value = file.name; error.value = '' }
  catch { error.value = t('admin.plugins.detail.fileFailed') }
}
async function pickFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  await readFile(file)
}
async function dropFile(event: DragEvent) { await readFile(event.dataTransfer?.files[0]) }
async function installUpload() {
  const content = uploadSource.value
  if (new TextEncoder().encode(content).length > 1048576) { error.value = t('admin.plugins.js.tooLarge'); return }
  const input = { source: content, expected_key: uploadKey.value || undefined, allow_builtin_override: allowOverride.value }
  if (await mutate(() => props.client.install(input))) uploadOpened.value = false
}
function requestDelete(plugin: NewAPIPluginVersion | PluginFactory) {
  if (!canDelete(plugin) || !props.client.remove || !props.editable) return
  error.value = ''
  deleteTarget.value = plugin
}
function closeDelete() {
  props.cancelMutation?.()
  if (!busy.value) deleteTarget.value = undefined
}
async function removeVersion() {
  const target = deleteTarget.value
  if (!target || !props.client.remove) return
  if (await mutate(() => props.client.remove!(target.id))) deleteTarget.value = undefined
}
async function activate(plugin: NewAPIPluginVersion, enabled = !plugin.enabled) { await mutate(() => props.client.activate(plugin.id, enabled)) }
async function restore(key: string) { await mutate(() => props.client.restore(key)) }
defineExpose({ reload: load })
onMounted(load)
onBeforeUnmount(() => { detailRequest?.abort(); controller.abort(); props.cancelMutation?.() })
</script>

<style scoped>
.menu-action { display: block; width: 100%; border-radius: 0.5rem; padding: 0.625rem 0.75rem; text-align: left; font-size: 0.75rem; }
.menu-action:hover { background: rgb(128 128 128 / 0.1); }
.menu-action:disabled { opacity: 0.4; cursor: not-allowed; }
summary::-webkit-details-marker { display: none; }
</style>
