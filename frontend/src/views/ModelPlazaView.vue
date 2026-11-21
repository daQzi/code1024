<template>
  <!-- 后台内嵌形态:?embedded=1 且已登录,套完整后台布局 -->
  <AppLayout v-if="isEmbedded">
    <ModelPlazaDetail v-if="modelId" :response="data" :loading="loading" :error="loadFailed" />
    <ModelPlazaContent v-else :response="data" :loading="loading" :error="loadFailed" embedded />
  </AppLayout>

  <!-- 独立形态:自带导航条(logo/站名 + 登录/回后台) -->
  <div v-else class="min-h-screen bg-gray-50 dark:bg-dark-950">
    <PlazaNavBar />
    <main class="mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
      <ModelPlazaDetail v-if="modelId" :response="data" :loading="loading" :error="loadFailed" />
      <ModelPlazaContent v-else :response="data" :loading="loading" :error="loadFailed" />
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import PlazaNavBar from '@/components/modelPlaza/PlazaNavBar.vue'
import ModelPlazaContent from '@/components/modelPlaza/ModelPlazaContent.vue'
import ModelPlazaDetail from '@/components/modelPlaza/ModelPlazaDetail.vue'
import { getModelPlaza, getModelPlazaModel, type ModelPlazaResponse } from '@/api/modelPlaza'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()

// embedded=1 但未登录(如转发的链接)自动降级为独立形态。
const isEmbedded = computed(() => route.query.embedded === '1' && authStore.isAuthenticated)

const data = ref<ModelPlazaResponse | null>(null)
const loading = ref(true)
const loadFailed = ref(false)
const modelId = computed(() => typeof route.params.modelId === 'string' ? route.params.modelId : '')

watch([modelId, () => authStore.isAuthenticated], async ([id], _, onCleanup) => {
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  data.value = null
  loading.value = true
  loadFailed.value = false
  // 独立形态导航条需要站点名/Logo;有 __APP_CONFIG__ 注入时同步命中缓存。
  void appStore.fetchPublicSettings()
  try {
    const result = id
      ? await getModelPlazaModel(id, { signal: controller.signal })
      : await getModelPlaza({ signal: controller.signal })
    if (!controller.signal.aborted) data.value = result
  } catch {
    if (!controller.signal.aborted) loadFailed.value = true
  } finally {
    if (!controller.signal.aborted) loading.value = false
  }
}, { immediate: true })
</script>
