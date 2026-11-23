<template>
  <AppLayout>
    <div class="space-y-6">
      <div v-if="loading" role="status" aria-busy="true" class="space-y-6">
        <div class="grid gap-4 [grid-template-columns:repeat(auto-fit,minmax(min(100%,14rem),1fr))]">
          <div v-for="cardIndex in 4" :key="`user-stat-skeleton-${cardIndex}`" class="card p-4">
            <div class="flex items-center gap-3">
              <UiSkeleton :width="36" :height="36" radius="var(--ui-r-md)" />
              <div class="flex-1 space-y-2">
                <UiSkeleton width="55%" :height="12" />
                <UiSkeleton width="75%" :height="22" />
                <UiSkeleton width="45%" :height="12" />
              </div>
            </div>
          </div>
        </div>
        <div class="card space-y-4 p-5">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <UiSkeleton width="180px" :height="18" />
            <div class="flex items-center gap-2">
              <UiSkeleton width="120px" :height="32" radius="var(--ui-r-md)" />
              <UiSkeleton width="88px" :height="32" radius="var(--ui-r-md)" />
            </div>
          </div>
          <UiSkeleton width="100%" :height="220" radius="var(--ui-r-md)" />
        </div>
        <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
          <div class="card space-y-3 p-5 lg:col-span-2">
            <UiSkeleton width="160px" :height="18" />
            <UiSkeleton v-for="rowIndex in 4" :key="`user-row-skeleton-${rowIndex}`" width="100%" :height="40" radius="var(--ui-r-md)" />
          </div>
          <div class="card space-y-3 p-5 lg:col-span-1">
            <UiSkeleton width="120px" :height="18" />
            <UiSkeleton v-for="actionIndex in 3" :key="`user-action-skeleton-${actionIndex}`" width="100%" :height="44" radius="var(--ui-r-md)" />
          </div>
        </div>
      </div>
      <template v-else-if="stats">
        <UserDashboardStats :stats="stats" :balance="user?.balance || 0" :is-simple="authStore.isSimpleMode" :platform-quotas="platformQuotas" />
        <UserDashboardCharts v-model:startDate="startDate" v-model:endDate="endDate" v-model:granularity="granularity" :loading="loadingCharts" :trend="trendData" :models="modelStats" @dateRangeChange="loadCharts" @granularityChange="loadCharts" @refresh="refreshAll" />
        <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
          <div class="lg:col-span-2"><UserDashboardRecentUsage :data="recentUsage" :loading="loadingUsage" /></div>
          <div class="lg:col-span-1"><UserDashboardQuickActions /></div>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'; import { useAuthStore } from '@/stores/auth'; import { usageAPI, type UserDashboardStats as UserStatsType } from '@/api/usage'
import AppLayout from '@/components/layout/AppLayout.vue'; import UiSkeleton from '@/ui/UiSkeleton.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'; import UserDashboardCharts from '@/components/user/dashboard/UserDashboardCharts.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'; import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
import type { UsageLog, TrendDataPoint, ModelStat, PlatformQuotaItem } from '@/types'
import { getMyPlatformQuotas } from '@/api/user'
import { formatDateLocalInput } from '@/utils/format'

const authStore = useAuthStore(); const user = computed(() => authStore.user)
const stats = ref<UserStatsType | null>(null); const loading = ref(false); const loadingUsage = ref(false); const loadingCharts = ref(false)
const trendData = ref<TrendDataPoint[]>([]); const modelStats = ref<ModelStat[]>([]); const recentUsage = ref<UsageLog[]>([])
const platformQuotas = ref<PlatformQuotaItem[] | null>(null)

const startDate = ref(formatDateLocalInput(new Date(Date.now() - 6 * 86400000))); const endDate = ref(formatDateLocalInput(new Date())); const granularity = ref('day')

const loadStats = async () => { loading.value = true; try { await authStore.refreshUser(); stats.value = await usageAPI.getDashboardStats() } catch (error) { console.error('Failed to load dashboard stats:', error) } finally { loading.value = false } }
const loadCharts = async () => { loadingCharts.value = true; try { const res = await Promise.all([usageAPI.getDashboardTrend({ start_date: startDate.value, end_date: endDate.value, granularity: granularity.value as any }), usageAPI.getDashboardModels({ start_date: startDate.value, end_date: endDate.value })]); trendData.value = res[0].trend || []; modelStats.value = res[1].models || [] } catch (error) { console.error('Failed to load charts:', error) } finally { loadingCharts.value = false } }
const loadRecent = async () => { loadingUsage.value = true; try { const res = await usageAPI.getByDateRange(startDate.value, endDate.value); recentUsage.value = res.items.slice(0, 5) } catch (error) { console.error('Failed to load recent usage:', error) } finally { loadingUsage.value = false } }
const loadPlatformQuotas = async () => { try { const data = await getMyPlatformQuotas(); platformQuotas.value = data.platform_quotas ?? [] } catch (error) { console.warn('Failed to load platform quotas:', error); platformQuotas.value = [] } }
const refreshAll = () => { loadStats(); loadCharts(); loadRecent(); loadPlatformQuotas() }

onMounted(() => { refreshAll() })
</script>
