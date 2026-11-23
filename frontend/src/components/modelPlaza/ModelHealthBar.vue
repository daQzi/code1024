<template>
  <div class="flex h-5 gap-0.5" role="img" :aria-label="t('modelPlaza.catalog.hourlyHealth')">
    <span v-for="bucket in buckets" :key="bucket.time" class="min-w-0 flex-1 rounded-sm" :class="color(bucket.point)" :title="label(bucket)" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PlazaHealthBucket } from '@/api/modelPlaza'

const props = defineProps<{ recent?: PlazaHealthBucket[]; end?: string }>()
const { t, locale } = useI18n()
const hour = 3600000
const buckets = computed(() => {
  const end = props.end ? Math.floor(Date.parse(props.end) / hour) * hour : Math.floor(Date.now() / hour) * hour
  const samples = new Map((props.recent ?? []).map(point => [Date.parse(point.bucket), point]))
  return Array.from({ length: 24 }, (_, index) => {
    const time = end - (23 - index) * hour
    return { time, point: samples.get(time) }
  })
})
function color(point?: PlazaHealthBucket) {
  if (!point?.requests) return 'bg-gray-200 dark:bg-dark-600'
  if (point.successes === point.requests) return 'bg-emerald-500 dark:bg-emerald-500'
  return point.successes === 0 ? 'bg-red-500' : 'bg-amber-400'
}
function label(bucket: { time: number; point?: PlazaHealthBucket }) {
  const time = new Date(bucket.time).toLocaleString(locale.value, { hour: '2-digit', minute: '2-digit', month: '2-digit', day: '2-digit' })
  if (!bucket.point?.requests) return `${time} · ${t('modelPlaza.catalog.noMetrics')}`
  return `${time} · ${bucket.point.successes}/${bucket.point.requests} · ${(bucket.point.successes * 100 / bucket.point.requests).toFixed(1)}%`
}
</script>
