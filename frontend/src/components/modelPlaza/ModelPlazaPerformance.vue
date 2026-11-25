<template>
  <section class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="max-w-3xl text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.metricsScope') }}</p>
      <div class="flex flex-wrap items-center gap-1.5" role="group" :aria-label="t('modelPlaza.catalog.recentStatusTitle')">
        <button
          v-for="win in WINDOW_OPTIONS"
          :key="win"
          type="button"
          class="min-w-[44px] rounded-xl border px-2.5 py-1 text-xs font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500"
          :class="selectedWindow === win
            ? 'border-emerald-500 bg-emerald-50/80 text-emerald-700 dark:border-emerald-400 dark:bg-emerald-500/15 dark:text-emerald-300'
            : 'border-gray-200 bg-white/70 text-gray-600 hover:border-gray-300 hover:text-gray-900 dark:border-dark-600 dark:bg-dark-900/40 dark:text-dark-300 dark:hover:border-dark-500 dark:hover:text-white'"
          :aria-pressed="selectedWindow === win"
          @click="selectedWindow = win"
        >
          {{ t(`modelPlaza.catalog.windows.${win}`) }}
        </button>
      </div>
    </div>
    <div v-if="loading" role="status" class="py-12 text-center text-sm text-gray-500">{{ t('modelPlaza.loading') }}</div>
    <div v-else-if="failed" role="alert" class="rounded-xl border border-gray-200 p-6 text-sm dark:border-dark-700">
      {{ t('modelPlaza.loadFailed') }}
      <button type="button" class="ml-3 text-primary-600 dark:text-primary-400" @click="revision++">{{ t('common.tryAgain') }}</button>
    </div>
    <template v-else-if="summary && summary.requests > 0">
      <dl class="grid grid-cols-2 gap-5 border-y border-gray-200 py-5 dark:border-dark-700 lg:grid-cols-4">
        <div><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.samples') }}</dt><dd class="mt-2 text-xl font-semibold tabular-nums text-gray-900 dark:text-white">{{ summary.requests.toLocaleString() }}</dd></div>
        <div><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.successRate') }}</dt><dd class="mt-2 text-xl font-semibold tabular-nums text-gray-900 dark:text-white">{{ successRate(summary) }}</dd></div>
        <div><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.average') }}</dt><dd class="mt-2 text-xl font-semibold tabular-nums text-gray-900 dark:text-white">{{ duration(summary.average_ms) }}</dd></div>
        <div><dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.p95') }}</dt><dd class="mt-2 text-xl font-semibold tabular-nums text-gray-900 dark:text-white">{{ duration(summary.p95_ms) }}</dd></div>
      </dl>
      <dl class="flex flex-wrap gap-6 text-sm">
        <div><dt class="text-xs text-gray-500">{{ t('modelPlaza.catalog.firstToken') }}</dt><dd class="mt-1 tabular-nums">{{ duration(summary.first_token_ms) }}</dd></div>
        <div><dt class="text-xs text-gray-500">{{ t('modelPlaza.catalog.throughput') }}</dt><dd class="mt-1 tabular-nums">{{ summary.tokens_per_second == null ? '—' : `${summary.tokens_per_second.toFixed(1)} Token/s` }}</dd></div>
      </dl>
      <div v-if="series.length" class="h-64"><Line :data="chartData" :options="chartOptions" /></div>
      <div class="overflow-x-auto rounded-xl border border-gray-200 dark:border-dark-700">
        <table class="w-full text-left text-sm">
          <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-400"><tr>
            <th class="px-4 py-3">{{ t('modelPlaza.filters.groupLabel') }}</th><th class="px-4 py-3">{{ t('modelPlaza.catalog.samples') }}</th><th class="px-4 py-3">{{ t('modelPlaza.catalog.successRate') }}</th><th class="px-4 py-3">{{ t('modelPlaza.catalog.average') }}</th><th class="px-4 py-3">{{ t('modelPlaza.catalog.firstToken') }}</th>
          </tr></thead>
          <tbody class="divide-y divide-gray-100 text-gray-700 dark:divide-dark-700 dark:text-dark-200"><tr v-for="point in groupTotals" :key="point.group_id">
            <td class="px-4 py-3">{{ groupNames.get(point.group_id) }}</td><td class="px-4 py-3 tabular-nums">{{ point.requests }}</td><td class="px-4 py-3 tabular-nums">{{ successRate(point) }}</td><td class="px-4 py-3 tabular-nums">{{ duration(point.average_ms) }}</td><td class="px-4 py-3 tabular-nums">{{ duration(point.first_token_ms) }}</td>
          </tr></tbody>
        </table>
      </div>
    </template>
    <div v-else class="rounded-xl border border-dashed border-gray-300 px-6 py-12 text-center dark:border-dark-600">
      <h2 class="text-sm font-medium text-gray-900 dark:text-white">{{ t('modelPlaza.catalog.noMetrics') }}</h2>
      <p class="mx-auto mt-2 max-w-xl text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.metricsNote') }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend, type ChartOptions } from 'chart.js'
import { Line } from 'vue-chartjs'
import { getModelPlazaPerformance, type ModelPlazaGroup, type PlazaMetrics, type PlazaMetricsPoint, type PlazaMetricsWindow } from '@/api/modelPlaza'
import { useModelPerformanceUpdates } from '@/composables/useModelPerformanceUpdates'

const WINDOW_OPTIONS: readonly PlazaMetricsWindow[] = ['1h', '90m', '3h', '6h', '12h', '24h']

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend)
const props = defineProps<{ modelId: string; groups: ModelPlazaGroup[] }>()
const { t, locale } = useI18n()
const route = useRoute()
const selectedWindow = ref<PlazaMetricsWindow>('24h')
const metrics = ref<PlazaMetrics | null>(null)
const loading = ref(true)
const failed = ref(false)
const revision = ref(0)
let controller: AbortController | undefined
let refreshing = false
async function refresh(background = false) {
  if (background && refreshing) return
  controller?.abort()
  const request = controller = new AbortController()
  refreshing = true
  if (!background) { loading.value = true; failed.value = false; metrics.value = null }
  try {
    const result = selectedWindow.value !== '24h'
      ? await getModelPlazaPerformance(props.modelId, request.signal, Number(route.query.group_id) || undefined, selectedWindow.value)
      : await getModelPlazaPerformance(props.modelId, request.signal, Number(route.query.group_id) || undefined)
    if (!request.signal.aborted) { metrics.value = result; failed.value = false }
  } catch { if (!request.signal.aborted) failed.value = true }
  finally { if (!request.signal.aborted) { loading.value = false; refreshing = false } }
}
watch([() => props.modelId, revision, () => route.query.group_id, selectedWindow], () => { void refresh() }, { immediate: true })
useModelPerformanceUpdates(() => ({ ids: [props.modelId], groupId: Number(route.query.group_id) || undefined }), scopeChanged => { void refresh(!scopeChanged) })
onBeforeUnmount(() => controller?.abort())
const summary = computed(() => metrics.value?.points.find(p => p.group_id === 0 && !p.bucket))
const groupTotals = computed(() => metrics.value?.points.filter(p => p.group_id !== 0 && !p.bucket) ?? [])
const groupNames = computed(() => new Map(props.groups.map(group => [group.id, group.name])))
const series = computed(() => {
  if (!metrics.value) return []
  const stepMs = Math.max(60000, (metrics.value.bucket_seconds ?? 3600) * 1000)
  const points = new Map(metrics.value.points.filter(p => p.group_id === 0 && p.bucket).map(p => [Math.floor(Date.parse(p.bucket!) / stepMs) * stepMs, p]))
  const endAligned = Math.floor(Date.parse(metrics.value.end) / stepMs) * stepMs
  const startAligned = Math.floor(Date.parse(metrics.value.start) / stepMs) * stepMs
  const count = Math.min(24, Math.max(12, Math.round((endAligned - startAligned) / stepMs)))
  return Array.from({ length: count }, (_, index) => {
    const time = endAligned - (count - 1 - index) * stepMs
    return { bucket: new Date(time).toISOString(), average_ms: points.get(time)?.average_ms ?? null }
  })
})
const chartData = computed(() => ({
  labels: series.value.map(p => new Date(p.bucket!).toLocaleTimeString(locale.value, { hour: '2-digit', minute: '2-digit' })),
  datasets: [{ label: t('modelPlaza.catalog.averageSeconds'), data: series.value.map(p => p.average_ms == null ? null : p.average_ms / 1000), borderColor: '#6366f1', backgroundColor: '#6366f1', pointRadius: 3, borderWidth: 2, spanGaps: false }]
}))
const chartOptions: ChartOptions<'line'> = { responsive: true, maintainAspectRatio: false, animation: false, plugins: { legend: { display: true } }, scales: { y: { beginAtZero: true } } }
function duration(value: number | null) { return value == null ? '—' : `${(value / 1000).toFixed(2)} s` }
function successRate(point: PlazaMetricsPoint) { return point.requests > 0 ? `${(point.successes * 100 / point.requests).toFixed(2)}%` : '—' }
</script>
