<template>
  <div class="space-y-6">
    <RouterLink :to="backLocation" class="inline-flex rounded text-sm font-medium text-primary-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:text-primary-400">{{ t('modelPlaza.catalog.back') }}</RouterLink>
    <div v-if="loading" role="status" class="py-16 text-center text-sm text-gray-500">{{ t('modelPlaza.loading') }}</div>
    <div v-else-if="error || !model" role="alert" class="rounded-xl border border-gray-200 bg-white p-8 text-sm text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-300">{{ t('modelPlaza.catalog.unavailable') }}</div>
    <template v-else>
      <header class="space-y-3 border-b border-gray-200 pb-6 dark:border-dark-700">
        <span class="inline-block rounded-md px-2 py-1 text-xs font-medium" :class="platformBadgeLightClass(model.platform)">{{ platformLabel(model.platform) }}</span>
        <h1 class="break-words text-2xl font-semibold tracking-tight text-gray-900 dark:text-white sm:text-3xl">{{ model.name }}</h1>
        <div class="flex flex-wrap items-center gap-3">
          <code class="break-all rounded bg-gray-100 px-2 py-1 text-xs text-gray-700 dark:bg-dark-800 dark:text-dark-200">{{ model.name }}</code>
          <button type="button" class="rounded text-xs font-medium text-primary-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:text-primary-400" @click="copyModel">{{ copied ? t('modelPlaza.catalog.copied') : t('modelPlaza.catalog.copyId') }}</button>
        </div>
      </header>
      <nav class="flex gap-6 overflow-x-auto border-b border-gray-200 dark:border-dark-700" :aria-label="t('modelPlaza.catalog.details')">
        <button v-for="item in tabs" :key="item" type="button" :aria-current="tab === item ? 'page' : undefined" class="whitespace-nowrap border-b-2 px-1 pb-3 text-sm font-medium focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500" :class="tab === item ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-900 dark:text-dark-400 dark:hover:text-white'" @click="setTab(item)">{{ t(`modelPlaza.catalog.tabs.${item}`) }}</button>
      </nav>
      <section v-if="tab === 'pricing'" class="space-y-4">
        <p class="text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.priceNote') }}</p>
        <PlazaGroupSection v-for="group in response?.groups ?? []" :key="group.id" :group="group" />
      </section>
      <section v-else-if="tab === 'api'" class="space-y-5">
        <p class="max-w-3xl text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('modelPlaza.catalog.apiNote') }}</p>
        <dl class="grid gap-4 rounded-xl border border-gray-200 p-5 text-sm dark:border-dark-700 sm:grid-cols-2">
          <div><dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.baseUrl') }}</dt><dd class="mt-2 break-all font-mono text-gray-900 dark:text-white">{{ baseUrl }}</dd></div>
          <div><dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.table.model') }}</dt><dd class="mt-2 break-all font-mono text-gray-900 dark:text-white">{{ model.name }}</dd></div>
        </dl>
        <p class="max-w-3xl text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.protocolNote') }}</p>
        <template v-if="model.protocols?.includes('openai_video')">
          <h2 class="text-sm font-semibold">OpenAI Videos</h2>
          <p class="text-sm text-gray-500">{{ t('modelPlaza.catalog.videoFlow') }}</p>
          <pre class="overflow-x-auto rounded-xl bg-gray-950 p-5 text-xs leading-6 text-gray-100"><code>{{ videoExample }}</code></pre>
          <p class="text-xs text-gray-500">{{ t('modelPlaza.catalog.videoReference') }}</p>
        </template>
      </section>
      <ModelPlazaPerformance v-else-if="model.id" :model-id="model.id" :groups="response?.groups ?? []" />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import type { ModelPlazaResponse } from '@/api/modelPlaza'
import { platformLabel, platformBadgeLightClass } from '@/utils/platformColors'
import PlazaGroupSection from './PlazaGroupSection.vue'
import ModelPlazaPerformance from './ModelPlazaPerformance.vue'

const props = defineProps<{ response: ModelPlazaResponse | null; loading: boolean; error?: boolean }>()
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const copied = ref(false)
const model = computed(() => props.response?.groups[0]?.models[0])
const tabs = ['pricing', 'api', 'performance'] as const
type Tab = typeof tabs[number]
const tab = computed<Tab>(() => tabs.includes(route.query.tab as Tab) ? route.query.tab as Tab : 'pricing')
const backLocation = computed(() => ({ name: 'ModelPlaza', query: route.query.embedded === '1' ? { embedded: '1' } : {} }))
const baseUrl = `${window.location.origin}/v1`
function shellJSON(value: unknown) { return JSON.stringify(value, null, 2).replace(/'/g, "'\\''") }
const videoExample = computed(() => `curl '${baseUrl}/videos' \\\n  -H 'Authorization: Bearer YOUR_API_KEY' \\\n  -H 'Content-Type: application/json' \\\n  -H 'Idempotency-Key: your-unique-request-id' \\\n  -d '${shellJSON({ model: model.value?.name, prompt: 'A quiet ocean at sunrise', seconds: '8', size: '1280x720' })}'\n\n# ${t('modelPlaza.catalog.pollTask')}\ncurl '${baseUrl}/videos/VIDEO_ID' \\\n  -H 'Authorization: Bearer YOUR_API_KEY'\n\n# ${t('modelPlaza.catalog.downloadVideo')}\ncurl '${baseUrl}/videos/VIDEO_ID/content' \\\n  -H 'Authorization: Bearer YOUR_API_KEY' --output video.mp4`)
function setTab(value: Tab) { void router.replace({ query: { ...route.query, tab: value } }) }
async function copyModel() {
  copied.value = false
  try {
    await navigator.clipboard.writeText(model.value?.name ?? '')
    copied.value = true
  } catch { /* The model ID remains selectable when clipboard access is denied. */ }
}
</script>
