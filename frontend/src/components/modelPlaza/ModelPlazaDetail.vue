<template>
  <div class="space-y-6">
    <RouterLink :to="backLocation" class="inline-flex rounded text-sm font-medium text-primary-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:text-primary-400">{{ t('modelPlaza.catalog.back') }}</RouterLink>
    <div v-if="loading" role="status" class="py-16 text-center text-sm text-gray-500 dark:text-dark-400">{{ t('modelPlaza.loading') }}</div>
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
      <section v-if="tab === 'overview'" class="space-y-5">
        <p class="max-w-3xl text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.capabilityNote') }}</p>
        <article v-for="group in response?.groups ?? []" :key="group.id" class="rounded-xl border border-gray-200 bg-white/80 p-5 dark:border-dark-700 dark:bg-dark-800/75">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ group.name }}</h2>
          <p class="mt-2 text-xs text-gray-500 dark:text-dark-400">{{ t(group.models[0]?.capabilities?.source === 'host_protocol' ? 'modelPlaza.catalog.protocolSource' : 'modelPlaza.catalog.catalogSource') }}</p>
          <dl class="mt-5 grid gap-x-8 gap-y-5 text-sm sm:grid-cols-2 lg:grid-cols-3">
            <div><dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.protocol') }}</dt><dd class="mt-2 text-gray-900 dark:text-white">{{ group.models[0]?.protocols?.map(protocolLabel).join(' · ') || unknown }}</dd></div>
            <div><dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.inputModalities') }}</dt><dd class="mt-2 text-gray-900 dark:text-white">{{ modalityText(group.models[0]?.capabilities?.input_modalities) }}</dd></div>
            <div><dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.outputModalities') }}</dt><dd class="mt-2 text-gray-900 dark:text-white">{{ modalityText(group.models[0]?.capabilities?.output_modalities) }}</dd></div>
            <div><dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.contextWindow') }}</dt><dd class="mt-2 tabular-nums text-gray-900 dark:text-white">{{ tokenLimit(group.models[0]?.capabilities?.context_window) }}</dd></div>
            <div><dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.maxOutputTokens') }}</dt><dd class="mt-2 tabular-nums text-gray-900 dark:text-white">{{ tokenLimit(group.models[0]?.capabilities?.max_output_tokens) }}</dd></div>
            <div><dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.reasoning') }}</dt><dd class="mt-2 text-gray-900 dark:text-white">{{ reasoningLabel(group.models[0]?.capabilities?.reasoning) }}</dd></div>
          </dl>
        </article>
      </section>
      <section v-else-if="tab === 'pricing'" class="space-y-4">
        <p class="text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.priceNote') }}</p>
        <PlazaGroupSection v-for="group in response?.groups ?? []" :key="group.id" :group="group" />
      </section>
      <section v-else-if="tab === 'api'" class="space-y-5">
        <p class="max-w-3xl text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('modelPlaza.catalog.apiNote') }}</p>
        <dl class="grid gap-4 rounded-xl border border-gray-200 bg-white/80 p-5 text-sm dark:border-dark-700 dark:bg-dark-800/75 sm:grid-cols-2">
          <div>
            <dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.baseUrl') }}</dt>
            <dd class="mt-2 flex flex-wrap items-center gap-2">
              <span class="break-all font-mono text-gray-900 dark:text-white">{{ baseUrl }}</span>
              <button
                type="button"
                class="rounded-md border border-gray-200 px-2 py-0.5 text-xs font-medium text-primary-600 transition-colors hover:bg-gray-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:border-dark-600 dark:text-primary-400 dark:hover:bg-dark-700"
                @click="copySnippet('baseUrl', baseUrl)"
              >
                {{ copiedKey === 'baseUrl' ? t('common.copied') : t('common.copy') }}
              </button>
            </dd>
          </div>
          <div><dt class="text-gray-500 dark:text-dark-400">{{ t('modelPlaza.table.model') }}</dt><dd class="mt-2 break-all font-mono text-gray-900 dark:text-white">{{ model.name }}</dd></div>
        </dl>
        <p class="max-w-3xl text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.protocolNote') }}</p>
        <template v-for="example in textExamples" :key="example.protocol">
          <div class="space-y-2">
            <div class="flex items-center justify-between gap-2">
              <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ protocolLabel(example.protocol) }}</h2>
              <button
                type="button"
                class="rounded-md border border-gray-200 px-2.5 py-1 text-xs font-medium text-gray-700 transition-colors hover:bg-gray-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:border-dark-600 dark:text-dark-200 dark:hover:bg-dark-700"
                @click="copySnippet(example.protocol, example.code)"
              >
                {{ copiedKey === example.protocol ? t('common.copied') : t('common.copy') }}
              </button>
            </div>
            <pre class="overflow-x-auto rounded-xl bg-gray-950 p-5 text-xs leading-6 text-gray-100"><code>{{ example.code }}</code></pre>
          </div>
        </template>
        <template v-if="model.protocols?.includes('openai_video')">
          <div class="space-y-2">
            <div class="flex items-center justify-between gap-2">
              <h2 class="text-sm font-semibold text-gray-900 dark:text-white">OpenAI Videos</h2>
              <button
                type="button"
                class="rounded-md border border-gray-200 px-2.5 py-1 text-xs font-medium text-gray-700 transition-colors hover:bg-gray-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:border-dark-600 dark:text-dark-200 dark:hover:bg-dark-700"
                @click="copySnippet('openai_video', videoExample)"
              >
                {{ copiedKey === 'openai_video' ? t('common.copied') : t('common.copy') }}
              </button>
            </div>
            <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.videoFlow') }}</p>
            <pre class="overflow-x-auto rounded-xl bg-gray-950 p-5 text-xs leading-6 text-gray-100"><code>{{ videoExample }}</code></pre>
            <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelPlaza.catalog.videoReference') }}</p>
          </div>
        </template>
      </section>
      <ModelPlazaPerformance v-else-if="model.id" :model-id="model.id" :groups="response?.groups ?? []" />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import type { ModelPlazaResponse } from '@/api/modelPlaza'
import { platformLabel, platformBadgeLightClass } from '@/utils/platformColors'
import PlazaGroupSection from './PlazaGroupSection.vue'
import ModelPlazaPerformance from './ModelPlazaPerformance.vue'

const props = defineProps<{ response: ModelPlazaResponse | null; loading: boolean; error?: boolean }>()
const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const copied = ref(false)
const copiedKey = ref<string | null>(null)
let copiedResetTimer: ReturnType<typeof setTimeout> | null = null
const model = computed(() => props.response?.groups[0]?.models[0])
const tabs = ['overview', 'pricing', 'api', 'performance'] as const
type Tab = typeof tabs[number]
const tab = computed<Tab>(() => tabs.includes(route.query.tab as Tab) ? route.query.tab as Tab : 'overview')
const unknown = computed(() => t('modelPlaza.catalog.capabilityUnknown'))
function protocolLabel(protocol: string) {
  return ({ openai_video: 'OpenAI Videos', openai_chat: 'OpenAI Chat Completions', openai_responses: 'OpenAI Responses', openai_embeddings: 'OpenAI Embeddings' } as Record<string, string>)[protocol] ?? protocol
}
function modalityText(values?: string[]) { return values?.length ? values.map(value => t(`modelPlaza.catalog.modalities.${value}`)).join(' · ') : unknown.value }
function tokenLimit(value?: number) { return value && value > 0 ? `${value.toLocaleString(locale.value)} Token` : unknown.value }
function reasoningLabel(value?: boolean) { return value == null ? unknown.value : t(value ? 'modelPlaza.catalog.declaredSupported' : 'modelPlaza.catalog.declaredUnsupported') }
const backLocation = computed(() => ({ name: 'ModelPlaza', query: { ...(route.query.embedded === '1' ? { embedded: '1' } : {}), ...(route.query.group_id ? { group_id: route.query.group_id } : {}) } }))
const baseUrl = `${window.location.origin}/v1`
function shellJSON(value: unknown) { return JSON.stringify(value, null, 2).replace(/'/g, "'\\''") }
const textExamples = computed(() => {
  const protocols = new Set(props.response?.groups.flatMap(group => group.models[0]?.protocols ?? []) ?? [])
  return [
    { protocol: 'openai_chat', path: 'chat/completions', body: { model: model.value?.name, messages: [{ role: 'user', content: 'Hello' }] } },
    { protocol: 'openai_responses', path: 'responses', body: { model: model.value?.name, input: 'Hello' } },
    { protocol: 'openai_embeddings', path: 'embeddings', body: { model: model.value?.name, input: 'Hello' } }
  ].filter(example => protocols.has(example.protocol)).map(example => ({ protocol: example.protocol, code: `curl '${baseUrl}/${example.path}' \\\n  -H 'Authorization: Bearer YOUR_API_KEY' \\\n  -H 'Content-Type: application/json' \\\n  -d '${shellJSON(example.body)}'` }))
})
const videoExample = computed(() => `curl '${baseUrl}/videos' \\\n  -H 'Authorization: Bearer YOUR_API_KEY' \\\n  -H 'Content-Type: application/json' \\\n  -H 'Idempotency-Key: your-unique-request-id' \\\n  -d '${shellJSON({ model: model.value?.name, prompt: 'A quiet ocean at sunrise', seconds: '8', size: '1280x720' })}'\n\n# ${t('modelPlaza.catalog.pollTask')}\ncurl '${baseUrl}/videos/VIDEO_ID' \\\n  -H 'Authorization: Bearer YOUR_API_KEY'\n\n# ${t('modelPlaza.catalog.downloadVideo')}\ncurl '${baseUrl}/videos/VIDEO_ID/content' \\\n  -H 'Authorization: Bearer YOUR_API_KEY' --output video.mp4`)
function setTab(value: Tab) { void router.replace({ query: { ...route.query, tab: value } }) }
async function copyModel() {
  copied.value = false
  try {
    await navigator.clipboard.writeText(model.value?.name ?? '')
    copied.value = true
  } catch { /* The model ID remains selectable when clipboard access is denied. */ }
}
async function copySnippet(key: string, value: string) {
  try {
    await navigator.clipboard.writeText(value)
    copiedKey.value = key
    if (copiedResetTimer) clearTimeout(copiedResetTimer)
    copiedResetTimer = setTimeout(() => {
      copiedKey.value = null
    }, 2000)
  } catch { /* Code examples remain selectable when clipboard access is denied. */ }
}
onBeforeUnmount(() => {
  if (copiedResetTimer) clearTimeout(copiedResetTimer)
})
</script>
