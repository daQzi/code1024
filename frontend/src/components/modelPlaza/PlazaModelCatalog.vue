<template>
  <div v-if="view === 'cards'" class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
    <article v-for="entry in pageModels" :key="entry.key" class="flex min-w-0 flex-col rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
      <div class="flex-1 space-y-4 p-5">
        <div class="flex items-center justify-between gap-3">
          <span class="rounded-md px-2 py-1 text-xs font-medium" :class="platformBadgeLightClass(entry.model.platform)">{{ platformLabel(entry.model.platform) }}</span>
          <span class="text-xs text-gray-500 dark:text-dark-400">{{ modeLabel(entry.model) }}</span>
        </div>
        <h2 class="break-words text-base font-semibold text-gray-900 dark:text-white">
          <RouterLink v-if="entry.model.id" :to="detailLocation(entry.model.id)" class="rounded focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 hover:text-primary-600 dark:hover:text-primary-400">{{ entry.model.name }}</RouterLink>
          <span v-else>{{ entry.model.name }}</span>
        </h2>
        <div class="flex flex-wrap gap-1.5">
          <span v-for="group in entry.groups" :key="group.id" class="rounded-md bg-gray-100 px-2 py-1 text-xs text-gray-600 dark:bg-dark-700 dark:text-dark-300">{{ group.name }}</span>
        </div>
        <p class="font-mono text-sm tabular-nums text-gray-800 dark:text-dark-200">{{ priceSummary(entry) }}</p>
        <div class="border-t border-gray-100 pt-3 dark:border-dark-700" :title="t('modelPlaza.catalog.summaryScope')">
          <ModelHealthBar :recent="metrics[entry.key]?.recent" :end="metricsEnd" class="mb-3" />
          <dl v-if="metrics[entry.key]?.requests" class="grid grid-cols-2 gap-3 text-xs">
            <div><dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.successRate') }}</dt><dd class="mt-1 font-medium tabular-nums text-gray-800 dark:text-dark-200">{{ successRate(metrics[entry.key]!) }}</dd></div>
            <div><dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.average') }}</dt><dd class="mt-1 font-medium tabular-nums text-gray-800 dark:text-dark-200">{{ duration(metrics[entry.key]!.average_ms) }}</dd></div>
            <div v-if="metrics[entry.key]!.first_token_ms != null"><dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.firstToken') }}</dt><dd class="mt-1 tabular-nums">{{ duration(metrics[entry.key]!.first_token_ms) }}</dd></div>
            <div v-if="metrics[entry.key]!.tokens_per_second != null"><dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.throughput') }}</dt><dd class="mt-1 tabular-nums">{{ metrics[entry.key]!.tokens_per_second?.toFixed(1) }} Token/s</dd></div>
          </dl>
          <p v-else class="text-xs text-gray-500 dark:text-dark-400">{{ metricsStatus }}</p>
          <p v-if="metricsFailed && metrics[entry.key]?.requests" class="mt-2 text-xs text-amber-700 dark:text-amber-400">{{ t('modelPlaza.catalog.metricsFailed') }}</p>
        </div>
        <div v-if="entry.model.protocols?.length" class="flex flex-wrap gap-1.5"><span v-for="protocol in entry.model.protocols" :key="protocol" class="rounded border border-gray-200 px-2 py-0.5 text-xs text-gray-500 dark:border-dark-600">{{ protocol }}</span></div>
        <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.groupPrices') }}</p>
      </div>
      <footer class="flex items-center justify-between gap-3 border-t border-gray-100 px-5 py-3 dark:border-dark-700">
        <span class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.groupCount', { count: entry.groups.length }) }}</span>
        <RouterLink v-if="entry.model.id" :to="detailLocation(entry.model.id)" class="rounded text-sm font-medium text-primary-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:text-primary-400">{{ t('modelPlaza.catalog.details') }}</RouterLink>
      </footer>
    </article>
  </div>
  <div v-else class="overflow-x-auto rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
    <table class="w-full text-left text-sm">
      <thead class="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-dark-700 dark:bg-dark-900"><tr><th class="px-5 py-3">{{ t('modelPlaza.filters.modelLabel') }}</th><th class="px-4 py-3">{{ t('modelPlaza.filters.platformLabel') }}</th><th class="px-4 py-3">{{ t('modelPlaza.catalog.kind') }}</th><th class="px-4 py-3">{{ t('modelPlaza.catalog.startingPrice') }}</th><th class="px-4 py-3" :title="t('modelPlaza.catalog.summaryScope')">{{ t('modelPlaza.catalog.performance24h') }}</th><th class="px-4 py-3">{{ t('modelPlaza.catalog.protocol') }}</th><th class="px-4 py-3">{{ t('modelPlaza.filters.groupLabel') }}</th></tr></thead>
      <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
        <tr v-for="entry in pageModels" :key="entry.key" class="align-top">
          <td class="max-w-xs break-words px-5 py-4 font-medium"><RouterLink v-if="entry.model.id" :to="detailLocation(entry.model.id)" class="rounded text-primary-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:text-primary-400">{{ entry.model.name }}</RouterLink><span v-else>{{ entry.model.name }}</span></td>
          <td class="whitespace-nowrap px-4 py-4">{{ platformLabel(entry.model.platform) }}</td>
          <td class="whitespace-nowrap px-4 py-4">{{ modeLabel(entry.model) }}</td>
          <td class="whitespace-nowrap px-4 py-4 tabular-nums">{{ priceSummary(entry) }}</td>
          <td class="whitespace-nowrap px-4 py-4 text-xs tabular-nums"><template v-if="metrics[entry.key]?.requests">{{ successRate(metrics[entry.key]!) }} · {{ duration(metrics[entry.key]!.average_ms) }}</template><span v-else class="text-gray-500">{{ metricsStatus }}</span></td>
          <td class="px-4 py-4 text-xs text-gray-500">{{ entry.model.protocols?.join(', ') || t('modelPlaza.catalog.protocolUnspecified') }}</td>
          <td class="min-w-40 px-4 py-4 text-xs text-gray-500">{{ entry.groups.map(group => group.name).join(', ') }}</td>
        </tr>
      </tbody>
    </table>
  </div>
  <nav v-if="models.length > pageSize" class="mt-4 flex flex-wrap items-center justify-between gap-3 text-sm" :aria-label="t('modelPlaza.catalog.pagination')">
    <span class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.pageSummary', { from: (page - 1) * pageSize + 1, to: Math.min(page * pageSize, models.length), total: models.length }) }}</span>
    <div class="flex gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="page === 1" @click="page--">{{ t('modelPlaza.catalog.previousPage') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="page * pageSize >= models.length" @click="page++">{{ t('modelPlaza.catalog.nextPage') }}</button></div>
  </nav>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { getModelPlazaPerformanceSummaries, type ModelPlazaGroup, type PlazaModel, type PlazaMetricsPoint } from '@/api/modelPlaza'
import { platformLabel, platformBadgeLightClass } from '@/utils/platformColors'
import { useModelPerformanceUpdates } from '@/composables/useModelPerformanceUpdates'
import ModelHealthBar from './ModelHealthBar.vue'

const props = withDefaults(defineProps<{ groups: ModelPlazaGroup[]; view?: 'cards' | 'table'; groupId?: number }>(), { view: 'cards' })
const { t } = useI18n()
const route = useRoute()
const page = ref(1)
const pageSize = 24
const metrics = ref<Record<string, PlazaMetricsPoint>>({})
const metricsLoading = ref(false)
const metricsFailed = ref(false)
const metricsEnd = ref<string>()
const models = computed(() => {
  const entries = new Map<string, { key: string; model: PlazaModel; groups: ModelPlazaGroup[] }>()
  for (const group of props.groups) {
    for (const model of group.models) {
      const key = model.id ?? JSON.stringify([model.platform, model.name])
      const entry = entries.get(key)
      if (entry) entry.groups.push(group)
      else entries.set(key, { key, model, groups: [group] })
    }
  }
  return [...entries.values()].sort((a, b) => a.model.name.localeCompare(b.model.name) || a.model.platform.localeCompare(b.model.platform))
})
watch(models, () => { page.value = 1 })
const pageModels = computed(() => models.value.slice((page.value - 1) * pageSize, page.value * pageSize))
const metricIDs = computed(() => pageModels.value.map(entry => entry.model.id).filter((id): id is string => !!id))
let controller: AbortController | undefined
async function refresh(background = false) {
  if (background && metricsLoading.value) return
  controller?.abort()
  const request = controller = new AbortController()
  const ids = metricIDs.value
  if (!background) { metrics.value = {}; metricsFailed.value = false }
  metricsLoading.value = ids.length > 0
  if (!ids.length) return
  try {
    const result = await getModelPlazaPerformanceSummaries(ids, request.signal, props.groupId)
    if (!request.signal.aborted) { metrics.value = result.models; metricsEnd.value = result.end; metricsFailed.value = false }
  } catch { if (!request.signal.aborted) metricsFailed.value = true }
  finally { if (!request.signal.aborted) metricsLoading.value = false }
}
watch([() => metricIDs.value.join(','), () => props.groupId], () => { void refresh() }, { immediate: true })
useModelPerformanceUpdates(() => ({ ids: metricIDs.value, groupId: props.groupId }), scopeChanged => { void refresh(!scopeChanged) })
onBeforeUnmount(() => controller?.abort())
const metricsStatus = computed(() => t(metricsLoading.value ? 'common.loading' : metricsFailed.value ? 'modelPlaza.catalog.metricsFailed' : 'modelPlaza.catalog.noMetrics'))
function duration(value: number | null) { return value == null ? '—' : `${(value / 1000).toFixed(2)} s` }
function successRate(point: PlazaMetricsPoint) { return `${(100 * point.successes / point.requests).toFixed(1)}%` }
function detailLocation(id: string) {
  return { name: 'ModelPlazaDetail', params: { modelId: id }, query: { ...(route.query.embedded === '1' ? { embedded: '1' } : {}), ...(props.groupId ? { group_id: String(props.groupId) } : {}) } }
}
function modeLabel(model: PlazaModel) {
  if (model.protocols?.includes('openai_video')) return t('modelPlaza.catalog.modes.video')
  const mode = model.pricing?.billing_mode ?? 'token'
  return t(`modelPlaza.catalog.modes.${mode}`)
}
function priceSummary(entry: { model: PlazaModel; groups: ModelPlazaGroup[] }) {
  const options: { value: number; unit: string }[] = []
  for (const group of entry.groups) {
    const model = group.models.find(model => model.id === entry.model.id && model.name === entry.model.name) ?? entry.model
    const mode = model.protocols?.includes('openai_video') ? 'video' : model.pricing?.billing_mode
    const rate = mode === 'video' && group.video_rate_independent ? group.video_rate_multiplier : mode === 'image' && group.image_rate_independent ? group.image_rate_multiplier : group.user_rate_multiplier ?? group.rate_multiplier
    for (const price of model.generation_pricing ?? []) {
      if (price.unit === 'upstream_cost') return t('modelPlaza.catalog.dynamicPrice')
      if (price.unit === 'expression') return t('modelPlaza.catalog.expressionPrice')
      // A service fee/minimum applies per task and cannot be presented as a
      // marginal unit price. Defer those variants to the complete price table.
      if (Number(price.service_fee) || Number(price.minimum)) return t('modelPlaza.catalog.configuredFees')
      options.push({ value: Number(price.unit_price) * rate, unit: price.unit })
    }
    if (!model.generation_pricing?.length) {
      const pricing = model.pricing
      if (pricing?.billing_mode === 'per_request' || pricing?.billing_mode === 'video' || pricing?.billing_mode === 'image') {
        if (pricing.per_request_price != null) options.push({ value: pricing.per_request_price * rate, unit: 'request' })
      } else if (pricing?.input_price != null) options.push({ value: pricing.input_price * rate * 1e6, unit: 'million' })
    }
  }
  if (!options.length) return t('modelPlaza.detail.noPricing')
  if (new Set(options.map(option => option.unit)).size > 1) return t('modelPlaza.catalog.multipleUnits')
  const minimum = Math.min(...options.map(option => option.value))
  return t('modelPlaza.catalog.priceFrom', { price: minimum.toLocaleString(undefined, { maximumFractionDigits: 8 }), unit: t(`modelPlaza.catalog.units.${options[0]!.unit}`) })
}
</script>
