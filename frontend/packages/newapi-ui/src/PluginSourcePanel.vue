<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
    <button type="button" class="btn btn-secondary btn-sm" :aria-expanded="opened" data-source-toggle @click="opened = !opened">{{ t(comparisonOnly ? 'pluginRegistry.external.compareInstalled' : 'admin.plugins.source.title') }}</button>
    <template v-if="opened">
      <p class="text-xs leading-5 text-gray-500">{{ t('admin.plugins.source.hint') }}</p>
      <div v-if="options.length" class="space-y-1 text-sm">{{ t('admin.plugins.source.compare') }}<HostSelect v-model="compareID" :select-component="selectComponent" :label="t('admin.plugins.source.compare')"
        :options="compareOptions" data-source-compare /></div>
      <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <template v-if="current && !loading && !error">
        <p class="break-all font-mono text-xs text-gray-500">v{{ current.version }} · SHA256 {{ current.sha256 }}</p>
        <template v-if="compared">
          <p class="text-xs text-gray-500">v{{ compared.version }} → v{{ current.version }}</p>
          <p v-if="current.source === compared.source" role="status" class="text-sm text-gray-500" data-source-unchanged>{{ t('admin.plugins.source.unchanged') }}</p>
          <p v-else-if="!lines" role="status" class="text-sm text-gray-500" data-source-diff-limit>{{ t('admin.plugins.source.limit') }}</p>
          <div v-else class="max-h-96 overflow-auto rounded-lg border border-gray-200 font-mono text-xs dark:border-dark-700" :aria-label="t('admin.plugins.source.diff')" data-source-diff>
            <div v-for="(line,index) in lines" :key="index" class="flex min-w-max whitespace-pre px-2 leading-5" :class="line.kind === 'added' ? 'bg-emerald-50 text-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-200' : line.kind === 'removed' ? 'bg-red-50 text-red-800 dark:bg-red-950/40 dark:text-red-200' : ''" :data-diff-kind="line.kind"><span class="w-12 shrink-0 select-none text-right text-gray-500">{{ line.before ?? '' }}</span><span class="mr-3 w-12 shrink-0 select-none text-right text-gray-500">{{ line.after ?? '' }}</span><span>{{ line.kind === 'added' ? '+' : line.kind === 'removed' ? '-' : ' ' }} {{ line.text || ' ' }}</span></div>
          </div>
        </template>
        <label v-if="!comparisonOnly" class="block space-y-1 text-xs text-gray-500">{{ t('admin.plugins.source.raw') }}<textarea :value="current.source" readonly spellcheck="false" rows="14" class="input font-mono text-xs" :aria-label="t('admin.plugins.source.raw')" data-source-content /></label>
      </template>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch, type Component } from 'vue'
import HostSelect from './HostSelect.vue'
import type { NewAPIPluginVersion, PluginSource, PluginSourceClient, PluginTranslator } from './types'
import { sourceSHA256 } from './marketplace'
import { diffPluginSource } from './sourceDiff'

type SourceIdentity = Pick<PluginSource, 'key' | 'version' | 'sha256'> & { id?: number }
const props = defineProps<{ selectComponent?: Component; client: PluginSourceClient; t: PluginTranslator; plugin: SourceIdentity; versions: NewAPIPluginVersion[]; source?: string; comparisonOnly?: boolean }>()
const t: PluginTranslator = (key, values) => props.t(key, values)
const opened = ref(false)
const compareID = ref('')
const current = ref<PluginSource>()
const compared = ref<PluginSource>()
const loading = ref(false)
const error = ref('')
const options = computed(() => props.versions.filter(item => item.key === props.plugin.key && item.id !== props.plugin.id))
const compareOptions = computed(() => [
  { value: '', label: t(props.comparisonOnly ? 'pluginRegistry.external.currentInstalled' : 'admin.plugins.source.raw') },
  ...options.value.map(item => ({ value: String(item.id), label: `v${item.version} · #${item.id} · ${item.sha256.slice(0, 12)}` }))
])
const lines = computed(() => compared.value && current.value ? diffPluginSource(compared.value.source, current.value.source) : null)
let active: AbortController | undefined
let sequence = 0

async function readSource(expected: SourceIdentity, signal: AbortSignal, candidate?: string) {
  const result = candidate !== undefined ? { ...expected, source: candidate } : expected.id !== undefined ? await props.client.getNewAPIPluginSource(expected.id, signal) : await props.client.getPluginFactorySource(expected.key, signal)
  if (signal.aborted) throw new Error('cancelled')
  if (expected.id !== undefined && result.id !== expected.id || result.key !== expected.key || result.version !== expected.version || result.sha256 !== expected.sha256 || typeof result.source !== 'string' || result.source.length > 1048576 || new TextEncoder().encode(result.source).length > 1048576 || await sourceSHA256(result.source) !== expected.sha256) throw new Error('invalid source artifact')
  return result
}

watch([opened, compareID, () => props.plugin, () => props.source, () => props.versions], async () => {
  const epoch = ++sequence
  active?.abort()
  current.value = undefined
  compared.value = undefined
  error.value = ''
  loading.value = false
  if (!opened.value) return
  const request = new AbortController()
  active = request
  loading.value = true
  try {
    const target = options.value.find(item => String(item.id) === compareID.value) ?? (props.comparisonOnly ? options.value[0] : undefined)
    const [selected, comparison] = await Promise.all([readSource(props.plugin, request.signal, props.source), target ? readSource(target, request.signal) : Promise.resolve(undefined)])
    if (request.signal.aborted || epoch !== sequence) return
    current.value = selected
    compared.value = comparison
  } catch {
    if (!request.signal.aborted && epoch === sequence) { request.abort(); error.value = t('admin.plugins.source.loadFailed') }
  } finally {
    if (epoch === sequence) loading.value = false
  }
})
onBeforeUnmount(() => { sequence++; active?.abort() })
</script>
