<template>
  <div class="space-y-4">
    <p class="text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('admin.plugins.detail.sandboxHint') }}</p>
    <div class="space-y-1 text-sm"><span>{{ t('admin.plugins.detail.hook') }}</span><HostSelect v-model="hook" :select-component="selectComponent" :options="hooks.map(value => ({ value, label: value }))" :label="t('admin.plugins.detail.hook')" :disabled="loading" /></div>
    <label class="block space-y-1 text-sm">{{ t('admin.plugins.detail.arguments') }}<textarea v-model="args" class="input font-mono text-xs" rows="10" spellcheck="false" :disabled="loading" /></label>
    <button type="button" class="btn btn-primary" :disabled="loading || !hook || !client.dryRun" @click="run">{{ t(loading ? 'common.loading' : 'admin.plugins.detail.run') }}</button>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <section v-if="output" class="space-y-2"><div class="flex items-center justify-between"><h4 class="text-sm font-medium">{{ t('admin.plugins.detail.result') }} <span class="text-gray-500">{{ duration }} ms</span></h4><PluginCopyButton :value="output" :label="t('admin.plugins.detail.copy')" :failed-label="t('admin.plugins.detail.copyFailed')" /></div><pre class="max-h-96 overflow-auto rounded-lg bg-gray-50 p-4 font-mono text-xs dark:bg-dark-900">{{ output }}</pre></section>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch, type Component } from 'vue'
import HostSelect from './HostSelect.vue'
import PluginCopyButton from './PluginCopyButton.vue'
import type { NewAPIPluginVersion, PluginCenterClient, PluginTranslator } from './types'
const props = defineProps<{ plugin: NewAPIPluginVersion; client: PluginCenterClient; t: PluginTranslator; selectComponent?: Component }>()
const offlineHooks = new Set(['ingress', 'request', 'outbound', 'egress', 'reply', 'report', 'buildSubmitRequest', 'parseSubmitResponse', 'parseSubmitEvent', 'parseSubmitEventDelta', 'buildQueryRequest', 'parseTaskResult', 'buildBatchQueryRequest', 'parseBatchResult', 'buildQuoteRequest', 'parseQuoteResponse', 'quotePrice', 'buildCancelRequest', 'parseCancelResponse', 'extractUsage', 'extractUsageOnComplete', 'listArtifacts', 'buildContentRequest', 'buildBalanceRequest', 'parseBalance', 'convertRequest', 'prepareRequest', 'convertResponse', 'convertStreamEvent', 'signRequest', 'credentialRejected'])
const hooks = computed(() => (props.plugin.capabilities.hooks ?? []).filter(value => offlineHooks.has(value)))
const hook = ref('')
const args = ref('[{}]')
const output = ref('')
const duration = ref(0)
const error = ref('')
const loading = ref(false)
let request: AbortController | undefined
watch(() => props.plugin.id, () => {
  request?.abort()
  hook.value = hooks.value[0] ?? ''
  args.value = '[{}]'
  output.value = ''
  error.value = ''
  loading.value = false
}, { immediate: true })
async function run() {
  if (!props.client.dryRun || loading.value) return
  error.value = ''
  output.value = ''
  let parsed: unknown
  try { parsed = JSON.parse(args.value) } catch { error.value = props.t('admin.plugins.detail.invalidArgs'); return }
  if (!Array.isArray(parsed) || parsed.length > 4 || new TextEncoder().encode(args.value).length > 65536) { error.value = props.t('admin.plugins.detail.invalidArgs'); return }
  const active = new AbortController()
  request = active
  loading.value = true
  try {
    const result = await props.client.dryRun(props.plugin.id, { hook: hook.value, args: parsed }, active.signal)
    if (active.signal.aborted) return
    output.value = JSON.stringify(result.result, null, 2)
    duration.value = result.duration_ms
  } catch (caught) {
    if (!active.signal.aborted) error.value = (caught as { response?: { data?: { message?: string } } }).response?.data?.message ?? props.t('admin.plugins.detail.sandboxFailed')
  } finally {
    if (!active.signal.aborted) loading.value = false
  }
}
onBeforeUnmount(() => request?.abort())
</script>
