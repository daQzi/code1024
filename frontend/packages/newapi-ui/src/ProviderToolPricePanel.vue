<template>
  <details class="rounded-xl border border-gray-200 p-4 dark:border-dark-700" data-provider-tool-prices @toggle="onToggle">
    <summary class="cursor-pointer text-sm font-medium text-gray-900 dark:text-white">{{ t('providerToolPrices.title') }}</summary>
    <form class="mt-3 space-y-3" @submit.prevent="save">
      <p class="max-w-3xl text-xs leading-5 text-gray-500">{{ t('providerToolPrices.hint') }}</p>
      <p class="max-w-3xl text-xs leading-5 text-gray-500">{{ t('providerToolPrices.fallback') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="saved" role="status" class="text-sm text-emerald-700 dark:text-emerald-400">{{ t('providerToolPrices.saved') }}</p>
      <label class="block text-sm">
        {{ t('providerToolPrices.prices') }}
        <textarea v-model="draft" class="input mt-1 font-mono text-xs" rows="8" :disabled="busy || !settings || !editable" :aria-label="t('providerToolPrices.prices')" spellcheck="false" />
      </label>
      <div class="flex gap-2">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="load">{{ t('common.refresh') }}</button>
        <button v-if="editable && runMutation" class="btn btn-primary btn-sm" :disabled="busy || !settings">{{ t('common.save') }}</button>
      </div>
    </form>
  </details>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { parseProviderToolPrices, type ProviderToolPriceClient, type ProviderToolPriceSettings } from './toolPrices'
import type { PluginTranslator, RunPluginMutation } from './types'

const props = withDefaults(defineProps<{
  client: ProviderToolPriceClient
  t: PluginTranslator
  editable?: boolean
  runMutation?: RunPluginMutation
  cancelMutation?: () => void
  isCancelled?: (error: unknown) => boolean
}>(), { editable: false, isCancelled: () => false })
const t: PluginTranslator = (key, values) => props.t(key, values)
const settings = ref<ProviderToolPriceSettings>()
const draft = ref('')
const busy = ref(false)
const error = ref('')
const saved = ref(false)
let controller: AbortController | undefined
let sequence = 0
let disposed = false

function reset() {
  sequence++
  controller?.abort()
  props.cancelMutation?.()
  settings.value = undefined
  draft.value = ''; busy.value = false; error.value = ''; saved.value = false
}
watch(() => props.client, reset)
watch(() => props.editable, () => {
  sequence++; controller?.abort(); props.cancelMutation?.(); busy.value = false
})
onBeforeUnmount(() => { disposed = true; reset() })
function accept(value: ProviderToolPriceSettings) {
  settings.value = { prices: { ...value.prices }, revision: value.revision }
  draft.value = JSON.stringify(value.prices, null, 2)
}
function current(id: number, client: ProviderToolPriceClient, signal: AbortSignal) {
  return !disposed && id === sequence && client === props.client && !signal.aborted
}
function onToggle(event: Event) {
  if ((event.target as HTMLDetailsElement).open && !settings.value && !busy.value) void load()
}
async function load() {
  if (busy.value) return
  const id = ++sequence, client = props.client
  controller?.abort(); controller = new AbortController()
  const signal = controller.signal
  busy.value = true; error.value = ''; saved.value = false
  try {
    const value = await client.read(signal)
    if (current(id, client, signal)) accept(value)
  } catch {
    if (current(id, client, signal)) error.value = t('providerToolPrices.loadFailed')
  } finally {
    if (current(id, client, signal)) busy.value = false
  }
}
async function save() {
  if (busy.value || !settings.value || !props.editable || !props.runMutation) return
  let prices: Record<string, number>
  try { prices = parseProviderToolPrices(draft.value) } catch {
    error.value = t('providerToolPrices.invalid'); return
  }
  const input: ProviderToolPriceSettings = { prices, revision: settings.value.revision }
  const id = ++sequence, client = props.client
  controller?.abort(); controller = new AbortController()
  const signal = controller.signal
  busy.value = true; error.value = ''; saved.value = false
  try {
    await props.runMutation(async () => {
      if (!current(id, client, signal) || !props.editable) return
      const value = await client.save(input, signal)
      if (current(id, client, signal)) { accept(value); saved.value = true }
    })
  } catch (value) {
    if (current(id, client, signal) && !props.isCancelled(value)) {
      error.value = t((value as { response?: { status?: number } })?.response?.status === 409 ? 'providerToolPrices.conflict' : 'providerToolPrices.saveFailed')
    }
  } finally {
    if (current(id, client, signal)) busy.value = false
  }
}
</script>
