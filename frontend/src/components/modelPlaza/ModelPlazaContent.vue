<template>
  <div class="space-y-5">
    <!-- 页头(独立形态下展示标题;后台形态 AppHeader 已有页面标题) -->
    <div v-if="!embedded">
      <h1 class="text-2xl font-bold tracking-tight text-gray-900 dark:text-white sm:text-3xl">{{ t('modelPlaza.title') }}</h1>
      <p class="mt-1.5 text-sm text-gray-500 dark:text-dark-400">{{ t('modelPlaza.description') }}</p>
    </div>

    <!-- 全局价格说明(管理员配置,Markdown) -->
    <div
      v-if="descriptionHtml"
      class="plaza-description rounded-2xl border border-gray-100 bg-white px-5 py-4 text-sm shadow-card dark:border-dark-700/50 dark:bg-dark-800/50"
      v-html="descriptionHtml"
    ></div>

    <!-- 未登录提示 -->
    <p
      v-if="!isAuthenticated"
      class="flex items-center gap-1.5 text-xs text-gray-400 dark:text-dark-500"
    >
      <Icon name="infoCircle" size="xs" class="h-3.5 w-3.5" />
      {{ t('modelPlaza.anonymousHint') }}
    </p>

    <!-- 加载/错误/空 -->
    <div v-if="loading" class="flex min-h-[240px] items-center justify-center">
      <div class="h-8 w-8 animate-spin rounded-full border-2 border-primary-600/25 border-t-primary-600 dark:border-primary-400/25 dark:border-t-primary-400"></div>
    </div>
    <div
      v-else-if="error"
      class="rounded-2xl border border-red-200 bg-red-50 px-5 py-8 text-center text-sm text-red-600 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300"
    >
      {{ t('modelPlaza.loadFailed') }}
    </div>
    <template v-else>
      <!-- 近期模型状态监控面板（时间窗口切换 + 颜色阈值图例） -->
      <section
        class="rounded-2xl border border-gray-200/80 bg-white/80 px-5 py-4 shadow-card backdrop-blur-md dark:border-dark-700/70 dark:bg-dark-800/60"
        data-testid="plaza-status-banner"
      >
        <div class="flex flex-wrap items-start justify-between gap-2">
          <div>
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('modelPlaza.catalog.recentStatusTitle') }}
            </h2>
            <p class="mt-1 max-w-4xl text-xs leading-5 text-gray-500 dark:text-dark-400">
              {{ t('modelPlaza.catalog.recentStatusDesc') }}
            </p>
          </div>
          <div class="flex flex-wrap items-center gap-3 text-[11px] text-gray-500 dark:text-dark-400">
            <span class="inline-flex items-center gap-1.5">
              <i class="h-2 w-2 rounded-full bg-emerald-500" aria-hidden="true" />
              {{ t('modelPlaza.catalog.legendHealthy') }}
            </span>
            <span class="inline-flex items-center gap-1.5">
              <i class="h-2 w-2 rounded-full bg-amber-400" aria-hidden="true" />
              {{ t('modelPlaza.catalog.legendDegraded') }}
            </span>
            <span class="inline-flex items-center gap-1.5">
              <i class="h-2 w-2 rounded-full bg-red-500" aria-hidden="true" />
              {{ t('modelPlaza.catalog.legendUnhealthy') }}
            </span>
            <span class="inline-flex items-center gap-1.5">
              <i class="h-2 w-2 rounded-full bg-gray-300 dark:bg-dark-600" aria-hidden="true" />
              {{ t('modelPlaza.catalog.legendNoData') }}
            </span>
          </div>
        </div>
        <div class="mt-3 flex flex-wrap items-center gap-2">
          <div class="flex flex-wrap items-center gap-1.5" role="group" :aria-label="t('modelPlaza.catalog.recentStatusTitle')" data-testid="plaza-window-selector">
            <button
              v-for="win in WINDOW_OPTIONS"
              :key="win"
              type="button"
              class="min-w-[44px] rounded-xl border px-3 py-1.5 text-xs font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500"
              :class="selectedWindow === win
                ? 'border-emerald-500 bg-emerald-50/80 text-emerald-700 dark:border-emerald-400 dark:bg-emerald-500/15 dark:text-emerald-300'
                : 'border-gray-200 bg-white/70 text-gray-600 hover:border-gray-300 hover:text-gray-900 dark:border-dark-600 dark:bg-dark-900/40 dark:text-dark-300 dark:hover:border-dark-500 dark:hover:text-white'"
              :aria-pressed="selectedWindow === win"
              :data-testid="`plaza-window-${win}`"
              @click="selectedWindow = win"
            >
              {{ t(`modelPlaza.catalog.windows.${win}`) }}
            </button>
          </div>
          <span
            v-if="formattedUpdatedAt"
            class="ml-auto text-xs tabular-nums text-gray-400 dark:text-dark-400"
            data-testid="plaza-updated-at"
          >
            {{ t('modelPlaza.catalog.updatedAt', { time: formattedUpdatedAt }) }}
          </span>
        </div>
      </section>

      <!-- 筛选区:平台 → 分组 → 倍率 -->
      <PlazaFilterBar
        :platforms="platforms"
        :groups="groupOptions"
        :rates="rates"
        :platform="selectedPlatform"
        :group-id="selectedGroupId"
        :rate="selectedRate"
        :search="searchQuery"
        @update:platform="selectedPlatform = $event"
        @update:group-id="selectedGroupId = $event"
        @update:rate="selectedRate = $event"
        @update:search="searchQuery = $event"
      />
      <div class="flex flex-wrap items-end gap-3">
        <div class="w-full sm:w-40">
          <span class="input-label">{{ t('modelPlaza.catalog.kind') }}</span>
          <Select v-model="selectedKind" :options="kindOptions" :aria-label="t('modelPlaza.catalog.kind')" />
        </div>
        <div class="w-full sm:w-48">
          <span class="input-label">{{ t('modelPlaza.catalog.protocol') }}</span>
          <Select v-model="selectedProtocol" :options="protocolOptions" :aria-label="t('modelPlaza.catalog.protocol')" />
        </div>
        <p role="status" class="pb-2 text-xs text-gray-500">{{ t('modelPlaza.catalog.modelCount', { count: modelCount }) }}</p>
        <div class="ml-auto flex gap-1 rounded-lg border border-gray-200 p-1 dark:border-dark-700" :aria-label="t('modelPlaza.catalog.view')">
          <button v-for="option in ['cards', 'table'] as const" :key="option" type="button" class="rounded-md px-3 py-1.5 text-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500" :class="view === option ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'text-gray-500'" :aria-pressed="view === option" @click="view = option">{{ t(`modelPlaza.catalog.${option}`) }}</button>
        </div>
      </div>

      <!-- 分组分节的模型清单(默认按生效倍率升序) -->
      <div v-if="filteredGroups.length > 0" class="space-y-5">
        <PlazaModelCatalog
          :groups="filteredGroups"
          :view="view"
          :group-id="selectedGroupId === 'all' ? undefined : selectedGroupId"
          :window="selectedWindow"
          @metrics-updated="metricsUpdatedAt = $event"
        />
      </div>
      <div
        v-else
        class="rounded-2xl border border-dashed border-gray-300 px-5 py-12 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-dark-400"
      >
        {{ searchActive ? t('modelPlaza.noSearchResult') : t('modelPlaza.empty') }}
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import Icon from '@/components/icons/Icon.vue'
import Select from '@/components/common/Select.vue'
import PlazaFilterBar from './PlazaFilterBar.vue'
import PlazaModelCatalog from './PlazaModelCatalog.vue'
import type { ModelPlazaGroup, ModelPlazaResponse, PlazaMetricsWindow, PlazaModel } from '@/api/modelPlaza'
import { useAuthStore } from '@/stores/auth'

const WINDOW_OPTIONS: readonly PlazaMetricsWindow[] = ['1h', '90m', '3h', '6h', '12h', '24h']

const props = defineProps<{
  response: ModelPlazaResponse | null
  loading: boolean
  error?: boolean
  /** 后台内嵌形态(AppLayout 内):隐藏页头。 */
  embedded?: boolean
}>()

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const isAuthenticated = computed(() => authStore.isAuthenticated)

const selectedWindow = ref<PlazaMetricsWindow>('24h')
const metricsUpdatedAt = ref<string>('')
const formattedUpdatedAt = computed(() => {
  if (!metricsUpdatedAt.value) return ''
  const parsed = Date.parse(metricsUpdatedAt.value)
  if (!Number.isFinite(parsed)) return ''
  return new Date(parsed).toLocaleTimeString(locale.value, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
})

const selectedPlatform = ref<string>('all')
const selectedGroupId = ref<number | 'all'>('all')
watch([() => props.response?.groups, () => route.query.group_id], ([groups, raw]) => {
  if (!groups) return
  const requested = Number(raw)
  selectedGroupId.value = raw === 'all' ? 'all' : groups.some(group => group.id === requested) ? requested : groups[0]?.id ?? 'all'
}, { immediate: true })
watch(selectedGroupId, (id) => {
  if (String(id) !== route.query.group_id) void router.replace({ query: { ...route.query, group_id: String(id) } })
})
const selectedRate = ref<number | 'all'>('all')
const searchQuery = ref('')
const selectedKind = ref('all')
const selectedProtocol = ref('all')
const view = ref<'cards' | 'table'>('cards')
function modelKind(model: PlazaModel) { return model.protocols?.includes('openai_video') ? 'video' : model.pricing?.billing_mode ?? 'token' }
const kinds = computed(() => [...new Set((props.response?.groups ?? []).flatMap(group => group.models.map(modelKind)))].sort())
const protocols = computed(() => [...new Set((props.response?.groups ?? []).flatMap(group => group.models.flatMap(model => model.protocols ?? [])))].sort())
const kindOptions = computed(() => [
  { value: 'all', label: t('modelPlaza.filters.all') },
  ...kinds.value.map(value => ({ value, label: t(`modelPlaza.catalog.modes.${value}`) }))
])
const protocolOptions = computed(() => [
  { value: 'all', label: t('modelPlaza.filters.all') },
  ...protocols.value.map(value => ({ value, label: value }))
])
const modelCount = computed(() => new Set(filteredGroups.value.flatMap(group => group.models.map(model => model.id ?? JSON.stringify([model.platform, model.name])))).size)

const searchActive = computed(() => searchQuery.value.trim() !== '')

const descriptionHtml = computed(() => {
  const md = props.response?.description?.trim()
  if (!md) return ''
  return DOMPurify.sanitize(marked.parse(md) as string)
})

/** 生效倍率 = 用户专属倍率 ?? 分组默认倍率。 */
function effectiveRate(g: ModelPlazaGroup): number {
  return g.user_rate_multiplier ?? g.rate_multiplier
}

const platforms = computed(() =>
  [...new Set((props.response?.groups ?? []).map((g) => g.platform).filter(Boolean))].sort()
)

const groupOptions = computed(() =>
  (props.response?.groups ?? []).map((g) => ({
    id: g.id,
    name: g.name,
    platform: g.platform,
    rate: effectiveRate(g)
  }))
)

/** 全量生效倍率;当前组合下不可用的项由 FilterBar 置灰而非隐藏。 */
const rates = computed(() =>
  [...new Set((props.response?.groups ?? []).map(effectiveRate))].sort((a, b) => a - b)
)

/** 数据刷新后选中的倍率可能不复存在,重置为全部。 */
watch(rates, (list) => {
  if (selectedRate.value !== 'all' && !list.includes(selectedRate.value)) {
    selectedRate.value = 'all'
  }
})

const filteredGroups = computed(() => {
  let groups = props.response?.groups ?? []
  if (selectedPlatform.value !== 'all') {
    groups = groups.filter((g) => g.platform === selectedPlatform.value)
  }
  if (selectedGroupId.value !== 'all') {
    groups = groups.filter((g) => g.id === selectedGroupId.value)
  }
  if (selectedRate.value !== 'all') {
    groups = groups.filter((g) => effectiveRate(g) === selectedRate.value)
  }
  // 模型名搜索:分组内只留命中的模型,整组无命中则隐藏该分组。
  const q = searchQuery.value.trim().toLowerCase()
  if (q || selectedKind.value !== 'all' || selectedProtocol.value !== 'all') {
    groups = groups
      .map((g) => ({ ...g, models: g.models.filter((m) => m.name.toLowerCase().includes(q) && (selectedKind.value === 'all' || modelKind(m) === selectedKind.value) && (selectedProtocol.value === 'all' || m.protocols?.includes(selectedProtocol.value))) }))
      .filter((g) => g.models.length > 0)
  }
  // 专属倍率会改变生效值,不能只依赖后端按默认倍率的排序。
  return [...groups].sort(
    (a, b) => effectiveRate(a) - effectiveRate(b) || a.name.localeCompare(b.name)
  )
})
</script>

<style scoped>
.plaza-description {
  line-height: 1.7;
  overflow-wrap: anywhere;
}

.plaza-description :deep(h1),
.plaza-description :deep(h2),
.plaza-description :deep(h3) {
  @apply mb-2 mt-3 font-semibold text-gray-900 first:mt-0 dark:text-white;
}

.plaza-description :deep(p) {
  @apply mb-2 text-gray-700 last:mb-0 dark:text-dark-200;
}

.plaza-description :deep(a) {
  @apply text-primary-600 underline underline-offset-4 hover:text-primary-700 dark:text-primary-300;
}

.plaza-description :deep(ul) {
  @apply mb-2 list-disc pl-5;
}

.plaza-description :deep(ol) {
  @apply mb-2 list-decimal pl-5;
}

.plaza-description :deep(li) {
  @apply mb-0.5 text-gray-700 dark:text-dark-200;
}

.plaza-description :deep(code) {
  @apply rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs dark:bg-dark-800;
}

.plaza-description :deep(blockquote) {
  @apply my-2 border-l-4 border-gray-300 pl-3 text-gray-600 dark:border-dark-600 dark:text-dark-300;
}
</style>
