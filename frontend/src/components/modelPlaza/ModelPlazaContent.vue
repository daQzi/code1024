<template>
  <div class="space-y-3.5">
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
      <!-- 近期模型状态监控面板（单行紧凑态：时间窗口切换 + 颜色阈值图例 + 悬浮说明） -->
      <section
        class="relative z-20 rounded-2xl border border-gray-200/80 bg-white/80 px-4 py-2.5 shadow-card backdrop-blur-md dark:border-dark-700/70 dark:bg-dark-800/60"
        data-testid="plaza-status-banner"
      >
        <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
          <div class="flex items-center gap-1.5">
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('modelPlaza.catalog.recentStatusTitle') }}
            </h2>
            <div class="group/status-hint relative inline-flex items-center">
              <button
                type="button"
                class="inline-flex h-5 w-5 items-center justify-center rounded-full text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:text-dark-400 dark:hover:bg-dark-700 dark:hover:text-gray-200"
                :aria-label="t('modelPlaza.catalog.recentStatusDesc')"
                :title="t('modelPlaza.catalog.recentStatusDesc')"
              >
                <Icon name="infoCircle" size="xs" class="h-3.5 w-3.5" aria-hidden="true" />
              </button>
              <div
                role="tooltip"
                class="pointer-events-none invisible absolute left-0 top-full z-30 mt-2 w-72 rounded-xl border border-gray-200/90 bg-white/95 p-3 text-xs leading-5 text-gray-600 opacity-0 shadow-lg backdrop-blur-md transition-opacity duration-150 group-hover/status-hint:visible group-hover/status-hint:opacity-100 group-focus-within/status-hint:visible group-focus-within/status-hint:opacity-100 dark:border-dark-600 dark:bg-dark-800/95 dark:text-dark-300 sm:w-80"
              >
                {{ t('modelPlaza.catalog.recentStatusDesc') }}
              </div>
            </div>
          </div>

          <div
            class="inline-flex flex-wrap items-center gap-0.5 rounded-xl border border-gray-200/80 bg-gray-100/70 p-0.5 dark:border-dark-700 dark:bg-dark-900/50"
            role="group"
            :aria-label="t('modelPlaza.catalog.recentStatusTitle')"
            data-testid="plaza-window-selector"
          >
            <button
              v-for="win in WINDOW_OPTIONS"
              :key="win"
              type="button"
              class="min-w-[40px] rounded-lg px-2.5 py-1 text-xs font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500"
              :class="selectedWindow === win
                ? 'bg-white text-emerald-700 shadow-sm ring-1 ring-emerald-500/30 dark:bg-dark-800 dark:text-emerald-300 dark:ring-emerald-400/30'
                : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'"
              :aria-pressed="selectedWindow === win"
              :data-testid="`plaza-window-${win}`"
              @click="selectedWindow = win"
            >
              {{ t(`modelPlaza.catalog.windows.${win}`) }}
            </button>
          </div>

          <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-gray-500 dark:text-dark-400 xl:ml-auto">
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
            <span
              v-if="formattedUpdatedAt"
              class="border-l border-gray-200 pl-3 tabular-nums text-gray-400 dark:border-dark-700 dark:text-dark-400"
              data-testid="plaza-updated-at"
            >
              {{ t('modelPlaza.catalog.updatedAt', { time: formattedUpdatedAt }) }}
            </span>
          </div>
        </div>
      </section>

      <!-- 统一筛选与搜索卡片:顶部工具栏(搜索 + 计费类型 + 声明协议 + 统计/视图) + 平台/倍率/分组筛选 -->
      <section
        class="rounded-2xl border border-gray-200/80 bg-white/80 shadow-card backdrop-blur-md dark:border-dark-700/70 dark:bg-dark-800/60"
        data-testid="plaza-filter-panel"
      >
        <div class="plaza-toolbar flex flex-wrap items-center gap-2 border-b border-gray-200/70 px-4 py-2.5 dark:border-dark-700/70">
          <div class="relative min-w-[200px] flex-1 sm:max-w-sm lg:max-w-md">
            <Icon
              name="search"
              size="sm"
              class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-500"
            />
            <input
              v-model="searchQuery"
              type="text"
              :aria-label="t('modelPlaza.filters.searchPlaceholder')"
              :placeholder="t('modelPlaza.filters.searchPlaceholder')"
              class="plaza-search-input input h-9 w-full rounded-xl py-0 pl-9 pr-8 text-sm"
            />
            <button
              v-if="searchQuery"
              type="button"
              class="absolute right-2.5 top-1/2 -translate-y-1/2 text-gray-400 transition-colors hover:text-gray-600 dark:text-dark-500 dark:hover:text-gray-300"
              :aria-label="t('common.clear')"
              @click="searchQuery = ''"
            >
              <Icon name="x" size="xs" class="h-3.5 w-3.5" />
            </button>
          </div>

          <div class="plaza-compact-select w-full sm:w-auto sm:min-w-[148px]">
            <Select v-model="selectedKind" :options="kindOptions" :aria-label="t('modelPlaza.catalog.kind')">
              <template #selected>
                <span class="inline-flex items-center gap-1.5 truncate">
                  <span class="text-gray-400 dark:text-dark-400">{{ t('modelPlaza.catalog.kind') }}</span>
                  <span :class="selectedKind !== 'all' ? 'font-medium text-primary-700 dark:text-primary-300' : 'text-gray-800 dark:text-gray-100'">
                    {{ selectedKindLabel }}
                  </span>
                </span>
              </template>
            </Select>
          </div>

          <div class="plaza-compact-select w-full sm:w-auto sm:min-w-[164px]">
            <Select v-model="selectedProtocol" :options="protocolOptions" :aria-label="t('modelPlaza.catalog.protocol')">
              <template #selected>
                <span class="inline-flex items-center gap-1.5 truncate">
                  <span class="text-gray-400 dark:text-dark-400">{{ t('modelPlaza.catalog.protocol') }}</span>
                  <span :class="selectedProtocol !== 'all' ? 'font-medium text-primary-700 dark:text-primary-300' : 'text-gray-800 dark:text-gray-100'">
                    {{ selectedProtocolLabel }}
                  </span>
                </span>
              </template>
            </Select>
          </div>

          <div class="ml-auto flex items-center gap-2.5">
            <p role="status" class="whitespace-nowrap text-xs tabular-nums text-gray-500 dark:text-dark-400">
              {{ t('modelPlaza.catalog.modelCount', { count: modelCount }) }}
            </p>
            <div
              class="inline-flex h-9 items-center gap-0.5 rounded-xl border border-gray-200/80 bg-gray-100/70 p-0.5 dark:border-dark-700 dark:bg-dark-900/50"
              :aria-label="t('modelPlaza.catalog.view')"
            >
              <button
                v-for="option in ['cards', 'table'] as const"
                :key="option"
                type="button"
                class="h-full rounded-[9px] px-2.5 text-xs font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500"
                :class="view === option ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-800 dark:text-primary-300' : 'text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-gray-200'"
                :aria-pressed="view === option"
                @click="view = option"
              >
                {{ t(`modelPlaza.catalog.${option}`) }}
              </button>
            </div>
          </div>
        </div>

        <div class="px-4 py-2.5">
          <PlazaFilterBar
            :platforms="platforms"
            :groups="groupOptions"
            :rates="rates"
            :platform="selectedPlatform"
            :group-id="selectedGroupId"
            :rate="selectedRate"
            @update:platform="selectedPlatform = $event"
            @update:group-id="selectedGroupId = $event"
            @update:rate="selectedRate = $event"
          />
        </div>
      </section>

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
const selectedKindLabel = computed(() =>
  kindOptions.value.find((o) => o.value === selectedKind.value)?.label ?? t('modelPlaza.filters.all')
)
const selectedProtocolLabel = computed(() =>
  protocolOptions.value.find((o) => o.value === selectedProtocol.value)?.label ?? t('modelPlaza.filters.all')
)
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

.plaza-toolbar .plaza-search-input {
  height: 2.25rem;
  padding-top: 0;
  padding-bottom: 0;
  border-radius: 0.75rem;
}

.plaza-toolbar .plaza-compact-select :deep(.select-trigger) {
  height: 2.25rem;
  padding: 0 0.75rem;
  border-radius: 0.75rem;
  font-size: 0.8125rem;
  line-height: 1.25rem;
}

.plaza-toolbar .plaza-compact-select :deep(.select-icon svg) {
  width: 1rem;
  height: 1rem;
}
</style>
