<template>
  <section>
    <div class="space-y-5">
      <header class="flex flex-wrap items-start justify-between gap-4 border-b border-gray-200 pb-5 dark:border-dark-700">
        <div><h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.plugins.center.title') }}</h2><p class="mt-1 max-w-3xl text-sm text-gray-500">{{ t('admin.plugins.center.description') }}</p></div>
        <div class="flex flex-wrap gap-2">
          <slot name="header-actions" />
          <label class="btn btn-primary cursor-pointer">{{ t('admin.plugins.js.upload') }}<input type="file" accept=".js,text/javascript" class="hidden" :disabled="busy || !editable" @change="upload" /></label>
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="load">{{ t('common.refresh') }}</button>
        </div>
      </header>
      <nav class="flex flex-wrap gap-2" :aria-label="t('admin.plugins.center.title')">
        <button v-for="value in tabs" :key="value" type="button" class="btn btn-sm" :class="tab === value ? 'btn-primary' : 'btn-secondary'" :aria-pressed="tab === value" @click="tab = value">{{ t(`admin.plugins.center.${value}`) }}</button>
      </nav>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <p v-if="message" role="status" class="text-sm text-emerald-700 dark:text-emerald-400">{{ message }}</p>
      <slot v-if="tab === 'cleanup'" name="cleanup" />
      <slot v-else-if="tab === 'developer' || tab === 'review'" name="registry" :mode="tab === 'review' ? 'review' : 'mine'" />
      <template v-else>
        <div v-if="tab === 'market'" class="flex flex-wrap gap-2"><button v-for="value in marketSources" :key="value" type="button" class="btn btn-secondary btn-sm" :aria-pressed="marketSource === value" @click="marketSource = value">{{ t(`admin.plugins.center.sources.${value}`) }}</button></div>
        <slot v-if="tab === 'market' && marketSource === 'community'" name="community-market" :reload="load" />
        <slot v-else-if="tab === 'market' && marketSource === 'external'" name="external-market" :plugins="catalog?.plugins ?? []" :reload="load" />
        <div v-else class="space-y-4">
        <div class="flex flex-wrap gap-3">
          <input v-model.trim="search" type="search" class="input min-w-48 flex-1" :placeholder="t('admin.plugins.center.search')" :aria-label="t('admin.plugins.center.search')" />
          <select v-model="kind" class="input w-auto" :aria-label="t('admin.plugins.center.kind')"><option v-for="value in ['all', 'task', 'provider', 'transform']" :key="value" :value="value">{{ t(`admin.plugins.center.kinds.${value}`) }}</option></select>
          <select v-model="source" class="input w-auto" :aria-label="t('admin.plugins.center.source')"><option v-for="value in ['all', 'system', 'uploaded', 'community', 'external']" :key="value" :value="value">{{ t(`admin.plugins.center.sources.${value}`) }}</option></select>
          <button type="button" class="btn btn-secondary btn-sm" :aria-pressed="view === 'table'" @click="view = view === 'cards' ? 'table' : 'cards'">{{ t(`admin.plugins.center.${view === 'cards' ? 'table' : 'cards'}`) }}</button>
        </div>
        <label class="flex items-center gap-2 text-xs text-gray-500"><input v-model="allowOverride" type="checkbox" class="rounded" />{{ t('admin.plugins.center.allowOverride') }}</label>
        <p v-if="tab === 'market'" class="text-sm text-gray-500">{{ t('admin.plugins.center.factoryHint') }}</p>
        <p v-if="loading && !entries.length" role="status" class="py-10 text-center text-sm text-gray-500">{{ t('common.loading') }}</p>
        <p v-else-if="!filtered.length" class="rounded-xl border border-dashed border-gray-300 py-12 text-center text-sm text-gray-500 dark:border-dark-600">{{ t('admin.plugins.center.empty') }}</p>
        <div v-else :class="view === 'cards' ? 'grid gap-4 lg:grid-cols-2 xl:grid-cols-3' : 'divide-y divide-gray-200 rounded-xl border border-gray-200 dark:divide-dark-700 dark:border-dark-700'">
          <article v-for="entry in filtered" :key="`${entry.runtime}:${entry.key}`" :class="view === 'cards' ? 'card flex flex-col border border-gray-200 p-5 dark:border-dark-700' : 'flex flex-wrap items-center gap-4 p-4'">
            <div class="min-w-0 flex-1"><div class="flex flex-wrap items-center gap-2"><h3 class="break-words text-base font-semibold text-gray-900 dark:text-white">{{ entry.name }}</h3><span class="text-xs text-gray-500">v{{ entry.version }}</span></div><p class="mt-1 break-all text-xs text-gray-500">{{ entry.key }}</p><p class="mt-3 line-clamp-2 text-sm leading-6 text-gray-500">{{ entry.description }}</p></div>
            <div class="my-4 flex flex-wrap gap-1.5 text-xs"><span class="rounded bg-gray-100 px-2 py-1 dark:bg-dark-700">{{ t(`admin.plugins.center.sources.${entry.source}`) }}</span><span v-for="cap in entry.kinds" :key="cap" class="rounded border border-gray-200 px-2 py-1 dark:border-dark-600">{{ t(`admin.plugins.center.kinds.${cap}`) }}</span></div>
            <div class="mt-auto flex flex-wrap items-center justify-between gap-2"><span class="text-xs" :class="entry.ready ? 'text-emerald-700 dark:text-emerald-400' : 'text-amber-700 dark:text-amber-400'">{{ t(entry.ready ? entry.kinds.length === 1 && entry.kinds[0] === 'transform' ? 'admin.plugins.instanceControlled' : entry.enabled ? 'admin.plugins.enabled' : 'admin.plugins.disabled' : 'admin.plugins.center.unsupported') }}</span><button type="button" class="btn btn-secondary btn-sm" @click="showEntry(entry)">{{ t('admin.plugins.center.details') }}</button></div>
          </article>
        </div>
        <p class="text-xs leading-5 text-gray-500">{{ t('admin.plugins.js.versionNote') }}</p>
        </div>
      </template>
      <p class="border-t border-gray-100 pt-4 text-xs text-gray-500 dark:border-dark-700">{{ t('admin.plugins.center.attribution') }} <a href="https://github.com/QuantumNous/new-api" target="_blank" rel="noopener noreferrer" class="text-primary-600">New API</a> · {{ catalog?.baseline?.slice(0, 12) }}</p>
    </div>
    <component :is="dialog" :show="!!selected" :title="selected?.meta.name ?? ''" width="wide" @close="closeDetails">
      <div v-if="selected" class="space-y-5">
        <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
        <p class="break-all text-sm text-gray-500">{{ selected.key }} · v{{ selected.version }}</p>
        <p class="whitespace-pre-wrap text-sm leading-6">{{ localized(selected.meta.description) }}</p>
        <dl class="grid gap-4 text-sm sm:grid-cols-2"><div><dt class="text-gray-500">{{ t('admin.plugins.center.author') }}</dt><dd>{{ selected.meta.author?.name || '—' }}</dd></div><div><dt class="text-gray-500">{{ t('admin.plugins.center.source') }}</dt><dd class="break-all">{{ 'origin' in selected ? selected.origin : t('admin.plugins.center.sources.system') }}</dd></div><div class="sm:col-span-2"><dt class="text-gray-500">SHA256</dt><dd class="break-all text-xs">{{ selected.sha256 }}</dd></div></dl>
        <div><h4 class="text-sm font-medium">{{ t('admin.plugins.center.models') }}</h4><p class="mt-2 break-words text-xs leading-6 text-gray-500">{{ selected.meta.models?.join(', ') || '—' }}</p></div>
        <section><h4 class="text-sm font-medium">{{ t('admin.plugins.center.compatibility') }}</h4><ul class="mt-2 list-inside list-disc space-y-1 text-xs text-gray-500"><li v-for="issue in [...(selected.compatibility?.blockers ?? []), ...(selected.compatibility?.warnings ?? [])]" :key="issue">{{ issue }}</li><li v-if="selected.compatibility?.runnable">{{ t('admin.plugins.center.runnable') }}: {{ selected.compatibility.kinds.join(', ') }}</li></ul></section>
        <div class="flex flex-wrap gap-2"><button v-if="'id' in selected && (selected.capabilities.task || selected.capabilities.provider)" type="button" class="btn btn-primary btn-sm" :disabled="busy || !editable || !selected.compatibility?.runnable && !selected.enabled" @click="activate(selected)">{{ t(selected.enabled ? 'admin.plugins.disable' : 'admin.plugins.enable') }}</button><button v-if="factoryForSelected" type="button" class="btn btn-secondary btn-sm" :disabled="busy || !editable" @click="restore(selected.key)">{{ t('admin.plugins.center.restore') }}</button><slot name="detail-actions" :plugin="selected" /></div>
        <section v-if="history.length"><h4 class="text-sm font-medium">{{ t('admin.plugins.js.versions') }}</h4><div v-for="version in history" :key="version.id" class="mt-2 flex flex-wrap items-center justify-between gap-2 rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900"><span>v{{ version.version }} · {{ version.sha256.slice(0, 12) }} {{ version.active ? t('admin.plugins.center.active') : '' }}</span><button v-if="!version.active" type="button" class="btn btn-secondary btn-sm" :disabled="busy || !editable || !version.compatibility?.runnable" @click="activate(version, true)">{{ t('admin.plugins.js.useVersion') }}</button></div></section>
        <p class="text-xs leading-5 text-gray-500">{{ t('admin.plugins.js.versionNote') }}</p>
        <slot name="source" :plugin="selected" :versions="history" />
      </div>
    </component>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, toRaw, type Component } from 'vue'
import type { LocalizedText, NewAPIPluginCatalog, NewAPIPluginVersion, PluginFactory, PluginCenterClient } from './types'

const props = withDefaults(defineProps<{
  client: PluginCenterClient
  t: (key: string) => string
  locale: string
  dialogComponent: Component
  editable?: boolean
  runMutation: (action: () => Promise<unknown>) => Promise<unknown>
  isCancelled?: (error: unknown) => boolean
}>(), { editable: false, isCancelled: () => false })
const t = (key: string) => props.t(key)
const dialog = computed(() => toRaw(props.dialogComponent))
const slots = defineSlots<{
  'header-actions'?: () => unknown
  cleanup?: () => unknown
  registry?: (props: { mode: 'review' | 'mine' }) => unknown
  'community-market'?: (props: { reload: () => Promise<void> }) => unknown
  'external-market'?: (props: { plugins: NewAPIPluginVersion[]; reload: () => Promise<void> }) => unknown
  'detail-actions'?: (props: { plugin: NewAPIPluginVersion | PluginFactory }) => unknown
  source?: (props: { plugin: NewAPIPluginVersion | PluginFactory; versions: NewAPIPluginVersion[] }) => unknown
}>()
const tabs = computed(() => [
  'installed', 'market', ...(slots.registry ? ['developer', 'review'] : []), ...(slots.cleanup ? ['cleanup'] : [])
] as ('installed' | 'market' | 'developer' | 'review' | 'cleanup')[])
const marketSources = computed(() => [
  'system', ...(slots['community-market'] ? ['community'] : []), ...(slots['external-market'] ? ['external'] : [])
] as ('system' | 'community' | 'external')[])

interface Entry { runtime: string; key: string; name: string; version: string; description: string; source: string; kinds: string[]; ready: boolean; enabled: boolean; js?: NewAPIPluginVersion | PluginFactory }
const catalog = ref<NewAPIPluginCatalog>()
const tab = ref<'installed' | 'market' | 'developer' | 'review' | 'cleanup'>('installed')
const marketSource = ref<'system' | 'community' | 'external'>('system')
const search = ref('')
const kind = ref('all')
const source = ref('all')
const view = ref<'cards' | 'table'>('cards')
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const message = ref('')
const allowOverride = ref(false)
const selected = ref<NewAPIPluginVersion | PluginFactory>()
const history = ref<NewAPIPluginVersion[]>([])
const controller = new AbortController()
let detailSequence = 0
let detailRequest: AbortController | undefined
const localized = (value?: LocalizedText) => typeof value === 'string' ? value : value?.[props.locale] ?? value?.zh ?? value?.en ?? ''
const errorText = (e: unknown) => (e as { response?: { data?: { message?: string } }; message?: string }).response?.data?.message ?? (e as Error).message
const factories = computed(() => new Map((catalog.value?.factories ?? []).map(f => [f.key, f])))
const factoryForSelected = computed(() => selected.value && factories.value.get(selected.value.key))
function jsEntry(plugin: NewAPIPluginVersion | PluginFactory): Entry {
  return { runtime: 'js', key: plugin.key, name: plugin.meta.name, version: plugin.version, description: localized(plugin.meta.description), source: factories.value.get(plugin.key)?.sha256 === plugin.sha256 ? 'system' : 'origin' in plugin && plugin.origin.startsWith('community:') ? 'community' : 'origin' in plugin && plugin.origin.startsWith('market:') ? 'external' : 'uploaded', kinds: ['task', 'provider', 'transform'].filter(kind => plugin.capabilities[kind as 'task' | 'provider' | 'transform']), ready: plugin.compatibility?.runnable ?? false, enabled: 'enabled' in plugin && plugin.enabled, js: plugin }
}
const entries = computed<Entry[]>(() => (tab.value === 'market' ? catalog.value?.factories ?? [] : catalog.value?.plugins ?? []).map(jsEntry))
const filtered = computed(() => entries.value.filter(entry => (kind.value === 'all' || entry.kinds.includes(kind.value)) && (source.value === 'all' || entry.source === source.value) && `${entry.name} ${entry.key} ${entry.description} ${entry.js?.meta.models?.join(' ') ?? ''}`.toLowerCase().includes(search.value.toLowerCase())))
async function load() {
  if (controller.signal.aborted) return
  loading.value = true
  try { const js = await props.client.list(controller.signal); if (!controller.signal.aborted) { catalog.value = js; error.value = '' } }
  catch (e) { if (!controller.signal.aborted) error.value = errorText(e) }
  finally { if (!controller.signal.aborted) loading.value = false }
}
async function showEntry(entry: Entry) {
  error.value = ''
  selected.value = entry.js
  await refreshHistory(entry.key)
}
function closeDetails() {
  detailSequence++
  detailRequest?.abort()
  selected.value = undefined
  history.value = []
}
async function refreshHistory(key: string) {
  if (controller.signal.aborted || selected.value?.key !== key) return
  const sequence = ++detailSequence
  detailRequest?.abort()
  const request = new AbortController()
  detailRequest = request
  history.value = []
  try { const result = await props.client.versions(key, request.signal); if (!request.signal.aborted && sequence === detailSequence && selected.value?.key === key) history.value = result }
  catch (e) { if (!request.signal.aborted && sequence === detailSequence) error.value = errorText(e) }
}
async function mutate(action: () => Promise<unknown>) {
  if (controller.signal.aborted || busy.value || !props.editable) return
  busy.value = true; error.value = ''; message.value = ''
  try { await props.runMutation(action); if (controller.signal.aborted) return; await load(); if (controller.signal.aborted) return; message.value = t('admin.plugins.center.saved'); if (selected.value) { const key = selected.value.key; selected.value = catalog.value?.plugins.find(p => p.key === key) ?? selected.value; await refreshHistory(key) } }
  catch (e) { if (!props.isCancelled(e)) error.value = errorText(e) }
  finally { busy.value = false }
}
async function upload(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]; input.value = ''
  if (!file) return
  if (file.size > 1048576) { error.value = t('admin.plugins.js.tooLarge'); return }
  try {
    const content = await file.text()
    await mutate(() => props.client.install({ source: content, allow_builtin_override: allowOverride.value }))
  } catch (e) { if (!controller.signal.aborted) error.value = errorText(e) }
}
async function activate(plugin: NewAPIPluginVersion, enabled = !plugin.enabled) { await mutate(() => props.client.activate(plugin.id, enabled)) }
async function restore(key: string) { await mutate(() => props.client.restore(key)) }
defineExpose({ reload: load })
onMounted(load)
onBeforeUnmount(() => { detailSequence++; detailRequest?.abort(); controller.abort() })
</script>
