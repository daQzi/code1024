<template>
  <div class="health-bar relative" @mouseleave="activeIndex = null">
    <div class="flex h-5 items-center gap-0.5" role="img" :aria-label="t('modelPlaza.catalog.hourlyHealth')">
      <span
        v-for="(bucket, index) in buckets"
        :key="bucket.time"
        tabindex="0"
        class="health-bar__bucket min-w-0 flex-1 h-full rounded-sm cursor-pointer outline-none"
        :class="[
          color(bucket.point),
          {
            'is-active': activeIndex === index,
            'is-dimmed': activeIndex !== null && activeIndex !== index
          }
        ]"
        :aria-label="label(bucket)"
        @mouseenter="activeIndex = index"
        @focus="activeIndex = index"
        @blur="handleBlur(index)"
      />
    </div>

    <Transition name="health-tip">
      <div
        v-if="activeBucket"
        role="tooltip"
        class="health-bar__tip"
        :class="tipAlignClass"
        :style="tipPositionStyle"
      >
        <i class="health-bar__tip-dot" :class="color(activeBucket.point)" aria-hidden="true" />
        <strong class="health-bar__tip-time">{{ formatTime(activeBucket.time) }}</strong>
        <i class="health-bar__tip-sep" aria-hidden="true">·</i>
        <b class="health-bar__tip-stat">{{ formatStat(activeBucket) }}</b>
      </div>
    </Transition>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PlazaHealthBucket } from '@/api/modelPlaza'

const props = defineProps<{
  recent?: PlazaHealthBucket[]
  start?: string
  end?: string
  bucketSeconds?: number
}>()
const { t, locale } = useI18n()
const activeIndex = ref<number | null>(null)

const stepMs = computed(() => Math.max(60000, (props.bucketSeconds ?? 3600) * 1000))

const buckets = computed(() => {
  const step = stepMs.value
  const parsedEnd = props.end ? Date.parse(props.end) : Date.now()
  const endAligned = Math.floor((Number.isFinite(parsedEnd) ? parsedEnd : Date.now()) / step) * step

  let count = 24
  if (props.start) {
    const parsedStart = Date.parse(props.start)
    if (Number.isFinite(parsedStart)) {
      const startAligned = Math.floor(parsedStart / step) * step
      count = Math.min(24, Math.max(12, Math.round((endAligned - startAligned) / step)))
    }
  }

  const samples = new Map(
    (props.recent ?? []).map((point) => [Math.floor(Date.parse(point.bucket) / step) * step, point]),
  )
  return Array.from({ length: count }, (_, index) => {
    const time = endAligned - (count - 1 - index) * step
    return { time, point: samples.get(time) }
  })
})

const activeBucket = computed(() => (activeIndex.value == null ? null : buckets.value[activeIndex.value] ?? null))

const tipAlignClass = computed(() => {
  if (activeIndex.value == null) return ''
  const total = buckets.value.length
  const edge = Math.min(4, Math.floor(total * 0.2))
  if (activeIndex.value <= edge) return 'health-bar__tip--left'
  if (activeIndex.value >= total - 1 - edge) return 'health-bar__tip--right'
  return 'health-bar__tip--center'
})

const tipPositionStyle = computed(() => {
  if (activeIndex.value == null) return {}
  const total = buckets.value.length
  const edge = Math.min(4, Math.floor(total * 0.2))
  if (activeIndex.value <= edge) return { left: '0px' }
  if (activeIndex.value >= total - 1 - edge) return { right: '0px' }
  return { left: `${((activeIndex.value + 0.5) / total) * 100}%` }
})

function handleBlur(index: number) {
  if (activeIndex.value === index) activeIndex.value = null
}

function color(point?: PlazaHealthBucket) {
  if (!point?.requests) return 'bg-gray-200 dark:bg-dark-600'
  const rate = point.successes / point.requests
  if (rate >= 0.95) return 'bg-emerald-500 dark:bg-emerald-500'
  if (rate >= 0.8) return 'bg-amber-400'
  return 'bg-red-500'
}

function formatTime(timestamp: number) {
  return new Date(timestamp).toLocaleString(locale.value, {
    hour: '2-digit',
    minute: '2-digit',
    month: '2-digit',
    day: '2-digit',
  })
}

function formatStat(bucket: { time: number; point?: PlazaHealthBucket }) {
  if (!bucket.point?.requests) return t('modelPlaza.catalog.noMetrics')
  return `${bucket.point.successes}/${bucket.point.requests} (${(bucket.point.successes * 100 / bucket.point.requests).toFixed(1)}%)`
}

function label(bucket: { time: number; point?: PlazaHealthBucket }) {
  const time = formatTime(bucket.time)
  if (!bucket.point?.requests) return `${time} · ${t('modelPlaza.catalog.noMetrics')}`
  return `${time} · ${bucket.point.successes}/${bucket.point.requests} · ${(bucket.point.successes * 100 / bucket.point.requests).toFixed(1)}%`
}
</script>

<style scoped>
.health-bar__bucket {
  transition:
    transform var(--ui-dur-fast, 120ms) var(--ui-ease-out, cubic-bezier(0.16, 1, 0.3, 1)),
    opacity var(--ui-dur-fast, 120ms) var(--ui-ease-out, cubic-bezier(0.16, 1, 0.3, 1)),
    filter var(--ui-dur-fast, 120ms) var(--ui-ease-out, cubic-bezier(0.16, 1, 0.3, 1));
}

.health-bar__bucket.is-active,
.health-bar__bucket:focus-visible {
  transform: scaleY(1.22);
  filter: brightness(1.08);
  box-shadow: 0 0 0 1px rgb(255 255 255 / 0.6);
}

.health-bar__bucket.is-dimmed {
  opacity: 0.48;
}

.health-bar__tip {
  position: absolute;
  bottom: calc(100% + 8px);
  z-index: 25;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: min(100%, 260px);
  padding: 5px 10px;
  border-radius: var(--ui-r-md, 12px);
  font-family: var(--ui-font, system-ui, sans-serif);
  font-size: 11px;
  line-height: 1.3;
  white-space: nowrap;
  color: var(--ui-text, #0b1220);
  background-color: rgb(var(--ui-glass-strong-rgb, 255 255 255) / calc(var(--ui-glass-strong-alpha, 0.74) + 0.2));
  -webkit-backdrop-filter: blur(20px) saturate(180%);
  backdrop-filter: blur(20px) saturate(180%);
  box-shadow:
    inset 0 1px 1px var(--ui-edge-strong, rgb(255 255 255 / 0.85)),
    0 0 0 1px var(--ui-hairline, rgb(15 23 42 / 0.12)),
    var(--ui-shadow-3, 0 14px 34px -12px rgb(15 23 42 / 0.2));
  pointer-events: none;
}

.health-bar__tip--center {
  transform: translateX(-50%);
}

.health-bar__tip-dot {
  width: 7px;
  height: 7px;
  border-radius: 999px;
  flex-shrink: 0;
}

.health-bar__tip-time {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.health-bar__tip-sep {
  font-style: normal;
  color: var(--ui-text-faint, #6b7d95);
}

.health-bar__tip-stat {
  font-weight: 560;
  font-variant-numeric: tabular-nums;
  color: var(--ui-text-muted, #4a5b73);
}

.health-tip-enter-active,
.health-tip-leave-active {
  transition:
    opacity 120ms var(--ui-ease-out, cubic-bezier(0.16, 1, 0.3, 1)),
    transform 120ms var(--ui-ease-out, cubic-bezier(0.16, 1, 0.3, 1));
}

.health-tip-enter-from,
.health-tip-leave-to {
  opacity: 0;
}

.health-tip-enter-from.health-bar__tip--center,
.health-tip-leave-to.health-bar__tip--center {
  transform: translate(-50%, 3px);
}

.health-tip-enter-from.health-bar__tip--left,
.health-tip-leave-to.health-bar__tip--left,
.health-tip-enter-from.health-bar__tip--right,
.health-tip-leave-to.health-bar__tip--right {
  transform: translateY(3px);
}

@media (prefers-reduced-motion: reduce) {
  .health-bar__bucket,
  .health-tip-enter-active,
  .health-tip-leave-active {
    transition: none;
  }
  .health-bar__bucket.is-active,
  .health-bar__bucket:focus-visible {
    transform: none;
  }
}
</style>
