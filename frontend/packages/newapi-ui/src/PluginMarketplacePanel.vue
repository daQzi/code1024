<template>
  <section class="space-y-4">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div><h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('pluginRegistry.external.title') }}</h3><p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('pluginRegistry.external.hint') }}</p></div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || sourcesLoading || !editable" @click="editSources">{{ t('pluginRegistry.external.manageSources') }}</button>
    </header>
    <div class="flex flex-wrap gap-3">
      <HostSelect v-model="sourceURL" :select-component="selectComponent" class="min-w-0 flex-1" :disabled="busy || sourcesLoading"
        :label="t('pluginRegistry.external.source')" :options="sourceOptions" />
      <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || loading || !sourceURL" @click="loadIndex">{{ t('common.refresh') }}</button>
    </div>
    <p class="break-all font-mono text-xs text-gray-500 dark:text-dark-400">{{ sourceURL }}</p>
    <form class="flex flex-wrap gap-3" @submit.prevent="lookupCode">
      <input v-model.trim="pluginCode" inputmode="numeric" maxlength="8" class="input min-w-48 flex-1" :disabled="busy || lookupLoading" :placeholder="t('pluginRegistry.external.codeHint')" :aria-label="t('pluginRegistry.external.code')" />
      <button type="submit" class="btn btn-secondary btn-sm" :disabled="busy || sourcesLoading || lookupLoading || !/^\d{8}$/.test(pluginCode)">{{ t(lookupLoading ? 'common.loading' : 'pluginRegistry.external.lookup') }}</button>
      <button v-if="lookup" type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="clearLookup">{{ t('pluginRegistry.external.clearLookup') }}</button>
    </form>
    <p v-if="lookup" class="break-all text-xs text-gray-500 dark:text-dark-400">{{ t('pluginRegistry.external.lookupSource') }} {{ lookup.source.name }} · {{ lookup.source.index_url }}</p>
    <p v-if="error" role="alert" class="rounded-xl border border-red-200 bg-red-50/90 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">{{ error }}</p>
    <p v-if="message" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50/90 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">{{ message }}</p>
    <div class="flex flex-wrap gap-3"><input v-model.trim="search" type="search" class="input min-w-48 flex-1" :placeholder="t('pluginRegistry.external.search')" :aria-label="t('pluginRegistry.external.search')" /><HostSelect v-model="kind" :select-component="selectComponent" class="w-full sm:w-36" :label="t('admin.plugins.center.kind')"
        :options="['all', 'task', 'provider', 'transform'].map(value => ({ value, label: t(`admin.plugins.center.kinds.${value}`) }))" /></div>
    <p v-if="loading || sourcesLoading" role="status" class="py-10 text-center text-sm text-gray-500 dark:text-dark-400">{{ t('common.loading') }}</p>
    <p v-else-if="!filtered.length" class="rounded-xl border border-dashed border-gray-300 py-12 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-dark-400">{{ t('pluginRegistry.empty') }}</p>
    <div v-else class="grid gap-4 lg:grid-cols-2 xl:grid-cols-3">
      <article v-for="plugin in visible" :key="plugin.key" class="card card-hover flex min-w-0 flex-col border border-gray-200 p-5 dark:border-dark-700">
        <h4 class="break-words text-base font-semibold text-gray-900 dark:text-white">{{ plugin.name }} <span class="text-xs font-normal text-gray-500 dark:text-dark-400">v{{ plugin.latest }}</span></h4>
        <p class="mt-1 break-all font-mono text-xs text-gray-500 dark:text-dark-400">{{ plugin.key }}<span v-if="plugin.author"> · {{ plugin.author }}</span><span v-if="plugin.code"> · {{ plugin.code }}</span></p>
        <p class="mt-3 line-clamp-3 text-sm leading-6 text-gray-500 dark:text-dark-400">{{ localized(plugin.description) }}</p>
        <div class="my-4 flex flex-wrap gap-1.5 text-xs"><span v-for="cap in [...new Set(plugin.versions.map(v => v.kind))]" :key="cap" class="badge badge-primary">{{ t(`admin.plugins.center.kinds.${cap}`) }}</span><span v-if="plugin.recommended" class="badge badge-success">{{ t('pluginRegistry.external.recommended') }}</span></div>
        <div class="mt-auto flex flex-wrap items-center justify-between gap-2"><span class="text-xs text-gray-500 dark:text-dark-400">{{ installState(plugin) }}</span><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="open(plugin)">{{ t('pluginRegistry.open') }}</button></div>
      </article>
    </div>
    <component :is="pagination" v-if="filtered.length" :page="page" :page-size="24" :total="filtered.length" @update:page="page = $event" />
    <component :is="dialog" :show="!!selected" :title="selected?.name ?? ''" width="wide" @close="close">
      <div v-if="selected" class="space-y-4">
        <p class="break-all text-xs text-gray-500">{{ selected.key }} · {{ reviewSourceURL }}</p>
        <HostSelect v-model="versionName" :select-component="selectComponent" :disabled="busy" :label="t('pluginRegistry.external.version')"
          :options="selected.versions.map(version => ({ value: version.version, label: `v${version.version} · ${version.kind}` }))" />
        <dl class="grid min-w-0 gap-3 text-xs sm:grid-cols-2"><div class="sm:col-span-2"><dt class="text-gray-500">SHA256</dt><dd class="break-all">{{ entry?.sha256 || t('pluginRegistry.external.errors.hashMissing') }}</dd></div><div><dt class="text-gray-500">{{ t('pluginRegistry.external.hosts') }}</dt><dd class="break-all">{{ entry?.allowedHosts.join(', ') || '—' }}</dd></div><div><dt class="text-gray-500">{{ t('pluginRegistry.external.baseURL') }}</dt><dd class="break-all">{{ entry?.baseUrl || '—' }}</dd></div></dl>
        <p v-if="blocker" role="alert" class="text-sm text-red-600">{{ blocker }}</p>
        <p v-if="detailError" role="alert" class="text-sm text-red-600">{{ detailError }}</p>
        <p v-if="detailLoading" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
        <label class="block"><span class="input-label">plugin.js</span><textarea :value="code" readonly spellcheck="false" rows="14" class="input font-mono text-xs leading-5" /></label>
        <PluginSourcePanel :select-component="selectComponent" :client="sourceClient" :t="t" v-if="code && comparisonIdentity && installedPlugins.some(item => item.key === comparisonIdentity?.key)" :key="`${comparisonIdentity.key}:${comparisonIdentity.version}:${comparisonIdentity.sha256}`" :plugin="comparisonIdentity" :source="code" :versions="installedPlugins" comparison-only />
        <section v-if="readme || changelog || documentError" class="space-y-3"><p v-if="documentError" role="alert" class="text-xs text-amber-700 dark:text-amber-400">{{ documentError }}</p><div v-if="readme"><h4 class="text-sm font-medium">{{ t('pluginRegistry.external.readme') }}</h4><pre class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">{{ readme }}</pre></div><div v-if="changelog"><h4 class="text-sm font-medium">{{ t('pluginRegistry.external.changelog') }}</h4><pre class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">{{ changelog }}</pre></div></section>
        <p class="text-xs leading-6 text-gray-500">{{ t('pluginRegistry.external.compatibilityHint') }}</p>
        <label class="flex items-center gap-2 text-xs text-gray-500"><input v-model="allowOverride" type="checkbox" :disabled="busy" class="rounded" />{{ t('admin.plugins.center.allowOverride') }}</label>
        <button type="button" class="btn btn-primary btn-sm" :disabled="busy || !editable || detailLoading || !code || !!blocker" @click="install">{{ t(busy ? 'common.loading' : 'pluginRegistry.install') }}</button>
        <div v-if="installed" role="status" class="space-y-2"><p class="text-sm text-emerald-700 dark:text-emerald-400">{{ t('pluginRegistry.installed') }}</p><p class="text-xs leading-6 text-gray-500">{{ [...installed.compatibility.blockers, ...installed.compatibility.warnings].join('; ') }}</p></div>
      </div>
    </component>
    <component :is="dialog" :show="sourcesOpen" :title="t('pluginRegistry.external.manageSources')" width="wide" @close="!busy && (sourcesOpen = false)">
      <div class="space-y-4">
        <p class="text-xs leading-6 text-gray-500">{{ t('pluginRegistry.external.sourcesHint') }}</p>
        <p v-if="sourceError" role="alert" class="text-sm text-red-600">{{ sourceError }}</p>
        <div v-for="(source, position) in drafts" :key="position" class="flex flex-wrap gap-2"><input v-model.trim="source.name" class="input w-40" :disabled="busy" maxlength="120" :aria-label="t('pluginRegistry.external.sourceName')" /><input v-model.trim="source.index_url" class="input min-w-48 flex-1" :disabled="busy" maxlength="2048" :aria-label="t('pluginRegistry.external.indexURL')" /><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="drafts.splice(position, 1)">{{ t('common.delete') }}</button></div>
        <div class="flex gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="busy || drafts.length >= 16" @click="drafts.push({name: '', index_url: ''})">{{ t('common.add') }}</button><button type="button" class="btn btn-primary btn-sm" :disabled="busy" @click="saveSources">{{ t('common.save') }}</button></div>
      </div>
    </component>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, toRaw, watch, type Component } from 'vue'
import HostSelect from './HostSelect.vue'
import PluginSourcePanel from './PluginSourcePanel.vue'
import type { LocalizedText, NewAPIPluginVersion, PluginMarketplaceSource, PluginMarketplaceClient, PluginSourceClient, PluginTranslator, RunPluginMutation } from './types'
import { marketplaceSourceKind, marketplaceURL, requireMarketplaceVersion, type MarketplaceIndex, type MarketplaceLookup, type MarketplacePlugin, type MarketplaceReadClient } from './marketplace'

const props = withDefaults(defineProps<{ selectComponent?: Component; installedPlugins: NewAPIPluginVersion[]; client: PluginMarketplaceClient; marketplace: MarketplaceReadClient; sourceClient: PluginSourceClient; t: PluginTranslator; locale: string; dialogComponent: Component; paginationComponent: Component; runMutation: RunPluginMutation; isCancelled?: (error: unknown) => boolean; editable?: boolean }>(), { editable: false, isCancelled: () => false })
const dialog = computed(() => toRaw(props.dialogComponent))
const pagination = computed(() => toRaw(props.paginationComponent))
const emit = defineEmits<{ installed: [] }>()
const t: PluginTranslator = (key, values) => props.t(key, values)
const sources = ref<PluginMarketplaceSource[]>([])
const sourceURL = ref('')
const sourceOptions = computed(() => sources.value.map(source => ({
  value: source.index_url,
  label: `${source.name} · ${t(`pluginRegistry.external.origins.${marketplaceSourceKind(source.index_url)}`)}`
})))
const index = ref<MarketplaceIndex>()
const lookup = ref<MarketplaceLookup>()
const pluginCode = ref('')
const lookupLoading = ref(false)
const reviewSourceURL = ref('')
const reviewLookupCode = ref<string>()
const reviewIndex = ref<MarketplaceIndex>()
const loading = ref(false)
const sourcesLoading = ref(true)
const error = ref('')
const message = ref('')
const search = ref('')
const kind = ref('all')
const page = ref(1)
const selected = ref<MarketplacePlugin>()
const versionName = ref('')
const entry = computed(() => selected.value?.versions.find(v => v.version === versionName.value))
const comparisonIdentity = computed(() => {
  const plugin = selected.value
  const version = entry.value
  return plugin && version?.sha256 ? { key: plugin.key, version: version.version, sha256: version.sha256 } : undefined
})
const code = ref('')
const readme = ref('')
const changelog = ref('')
const documentError = ref('')
const detailError = ref('')
const detailLoading = ref(false)
const allowOverride = ref(false)
const busy = ref(false)
const installed = ref<NewAPIPluginVersion>()
const sourcesOpen = ref(false)
const drafts = ref<PluginMarketplaceSource[]>([])
const sourceError = ref('')
const lifetime = new AbortController()
let listController: AbortController | undefined
let detailController: AbortController | undefined
let installController: AbortController | undefined
let lookupController: AbortController | undefined
const localized = (value?: LocalizedText) => typeof value === 'string' ? value : value?.[props.locale] ?? value?.[props.locale.startsWith('zh') ? 'zh-CN' : 'en'] ?? value?.zh ?? value?.en ?? ''
const errorText = (error: unknown) => { const message = (error as { response?: { data?: { message?: string } }; message?: string })?.response?.data?.message ?? (error as Error)?.message; return message?.startsWith('pluginRegistry.external.') ? t(message) : message || t('common.tryAgain') }
const filtered = computed(() => ((lookup.value?.index ?? index.value)?.plugins ?? []).filter(plugin => (kind.value === 'all' || plugin.versions.some(v => v.kind === kind.value)) && `${plugin.name} ${plugin.key} ${plugin.code ?? ''} ${plugin.models.join(' ')} ${localized(plugin.description)}`.toLowerCase().includes(search.value.toLowerCase())))
const visible = computed(() => filtered.value.slice((page.value - 1) * 24, page.value * 24))
const blocker = computed(() => {
  if (!entry.value) return ''
  if (!entry.value.sha256) return t('pluginRegistry.external.errors.hashMissing')
  if (entry.value.minApiVersion > 1) return t('pluginRegistry.external.errors.apiVersion')
  const revoked = reviewIndex.value?.revoked.find(item => item.sha256 === entry.value?.sha256)
  return revoked ? `${t('pluginRegistry.external.errors.revoked')} ${localized(revoked.reason)}` : ''
})
function installState(plugin: MarketplacePlugin) {
  const active = props.installedPlugins.find(item => item.key === plugin.key)
  if (!active) return t('pluginRegistry.external.notInstalled')
  const latest = plugin.versions.find(v => v.version === plugin.latest)
  return t(active.sha256 === latest?.sha256 ? 'pluginRegistry.external.current' : 'pluginRegistry.external.installedVersion', { version: active.version })
}
function close() { if (busy.value) return; detailController?.abort(); selected.value = undefined; installed.value = undefined }
async function loadSources() {
  try { const result = await props.client.listPluginMarketplaceSources(lifetime.signal); if (!lifetime.signal.aborted) { sources.value = result; sourceURL.value = result[0]?.index_url ?? '' } }
  catch (e) { if (!lifetime.signal.aborted) error.value = errorText(e) }
  finally { if (!lifetime.signal.aborted) sourcesLoading.value = false }
}
async function loadIndex() {
  lookupController?.abort(); lookup.value = undefined; lookupLoading.value = false
  close(); listController?.abort()
  const controller = listController = new AbortController()
  index.value = undefined; page.value = 1; error.value = ''; message.value = ''
  if (!sourceURL.value) { loading.value = false; return }
  loading.value = true
  try { const result = await props.marketplace.fetchMarketplaceIndex(sourceURL.value, controller.signal); if (!controller.signal.aborted) index.value = result }
  catch (e) { if (!controller.signal.aborted) error.value = errorText(e) }
  finally { if (!controller.signal.aborted) loading.value = false }
}
async function lookupCode() {
  if (busy.value || lookupLoading.value || !/^\d{8}$/.test(pluginCode.value)) return
  close(); lookupController?.abort()
  const controller = lookupController = new AbortController()
  lookupLoading.value = true; error.value = ''; message.value = ''
  try {
    const found = await props.marketplace.findMarketplacePluginByCode(pluginCode.value, sources.value, controller.signal)
    if (controller.signal.aborted) return
    lookup.value = found ?? undefined
    search.value = ''; kind.value = 'all'; page.value = 1
    if (!found) message.value = t('pluginRegistry.external.notFound')
  } catch (e) { if (!controller.signal.aborted) error.value = errorText(e) }
  finally { if (!controller.signal.aborted) lookupLoading.value = false }
}
function clearLookup() { close(); lookupController?.abort(); lookup.value = undefined; lookupLoading.value = false; message.value = '' }
function open(plugin: MarketplacePlugin) {
  reviewSourceURL.value = lookup.value?.source.index_url ?? sourceURL.value
  reviewLookupCode.value = lookup.value?.code
  reviewIndex.value = lookup.value?.index ?? index.value
  selected.value = plugin; versionName.value = plugin.latest; allowOverride.value = false
}
async function loadDetail() {
  detailController?.abort()
  const controller = detailController = new AbortController()
  code.value = ''; readme.value = ''; changelog.value = ''; detailError.value = ''; documentError.value = ''; installed.value = undefined; detailLoading.value = false
  const version = entry.value
  const url = reviewSourceURL.value
  if (!version || blocker.value) return
  detailLoading.value = true
  try {
    const source = await props.marketplace.fetchMarketplaceSource(url, version, controller.signal)
    if (!controller.signal.aborted) code.value = source
  } catch (e) { if (!controller.signal.aborted) detailError.value = errorText(e) }
  finally { if (!controller.signal.aborted) detailLoading.value = false }
  for (const [key, doc] of [['readme', version.readme], ['changelog', version.changelog]] as const) {
    if (!doc || controller.signal.aborted) continue
    try { const content = await props.marketplace.fetchMarketplaceDocument(url, doc, props.locale, controller.signal); if (!controller.signal.aborted) { if (key === 'readme') readme.value = content; else changelog.value = content } }
    catch (e) { if (!controller.signal.aborted) documentError.value = errorText(e) }
  }
}
async function install() {
  const plugin = selected.value
  const version = entry.value
  if (!props.editable || busy.value || !plugin || !version?.sha256 || !code.value || blocker.value) return
  const pin = { index_url: reviewSourceURL.value, key: plugin.key, version: version.version, sha256: version.sha256 }
  const lookupCode = reviewLookupCode.value
  const override = allowOverride.value
  const controller = installController = new AbortController()
  busy.value = true; detailError.value = ''; installed.value = undefined
  try {
    const result = await props.runMutation(async () => {
      controller.signal.throwIfAborted()
      const current = lookupCode ? await props.marketplace.fetchMarketplacePinIndex(pin.index_url, lookupCode, controller.signal) : await props.marketplace.fetchMarketplaceIndex(pin.index_url, controller.signal)
      const version = requireMarketplaceVersion(current, pin)
      const source = await props.marketplace.fetchMarketplaceSource(pin.index_url, version, controller.signal)
      controller.signal.throwIfAborted()
      return props.client.installNewAPIPlugin({ source, marketplace: pin, allow_builtin_override: override })
    })
    if (!controller.signal.aborted) { installed.value = result; emit('installed') }
  } catch (e) { if (!controller.signal.aborted && !props.isCancelled(e)) detailError.value = errorText(e) }
  finally { if (!controller.signal.aborted) busy.value = false }
}
function editSources() { drafts.value = sources.value.map(item => ({ ...item })); sourceError.value = ''; sourcesOpen.value = true }
async function saveSources() {
  if (!props.editable || busy.value) return
  sourceError.value = ''
  try { for (const source of drafts.value) marketplaceURL(source.index_url) }
  catch (e) { sourceError.value = errorText(e); return }
  const input = drafts.value.map(item => ({ ...item }))
  busy.value = true
  try {
    const result = await props.runMutation(() => { lifetime.signal.throwIfAborted(); return props.client.savePluginMarketplaceSources(input) })
    if (lifetime.signal.aborted) return
    sources.value = result; sourcesOpen.value = false
    const previous = sourceURL.value
    sourceURL.value = result.some(item => item.index_url === previous) ? previous : result[0]?.index_url ?? ''
    if (previous === sourceURL.value) await loadIndex()
  } catch (e) { if (!lifetime.signal.aborted && !props.isCancelled(e)) sourceError.value = errorText(e) }
  finally { if (!lifetime.signal.aborted) busy.value = false }
}
watch(sourceURL, loadIndex)
watch([search, kind], () => { page.value = 1 })
watch([selected, versionName], () => { if (selected.value) void loadDetail() })
onMounted(loadSources)
onBeforeUnmount(() => { lifetime.abort(); listController?.abort(); detailController?.abort(); installController?.abort(); lookupController?.abort() })
</script>
