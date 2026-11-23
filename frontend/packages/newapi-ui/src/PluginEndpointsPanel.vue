<template>
  <section class="space-y-4 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
    <h4 class="text-sm font-semibold">{{ t('admin.plugins.detail.endpoints') }}</h4>
    <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('admin.plugins.detail.endpointHint') }}</p>
    <div v-for="claim in meta.protocols ?? []" :key="claim.name" class="rounded-lg bg-gray-50 p-3 dark:bg-dark-900">
      <div class="mb-2 flex flex-wrap items-center gap-2"><code class="text-xs text-gray-500">{{ claim.name }}</code><details v-if="claim.models?.length" class="text-xs"><summary class="cursor-pointer">{{ t('admin.plugins.detail.modelScope') }}</summary><PluginModelList class="mt-2" :models="claim.models" :limit="claim.models.length" /></details></div>
      <ul class="divide-y divide-gray-200 dark:divide-dark-700">
        <li v-for="endpoint in protocols[claim.name] ?? []" :key="endpoint.path" class="flex items-start gap-3 py-2">
          <span class="badge badge-gray mt-1 font-mono">{{ endpoint.method }}</span>
          <div class="min-w-0 flex-1"><code class="break-all text-xs">{{ endpoint.path }}</code><div v-if="endpoint.method === 'POST'" class="mt-1 flex flex-wrap gap-1"><span v-for="mode in claim.supports" :key="mode" class="badge badge-primary font-mono">{{ mode }}</span></div></div>
          <PluginCopyButton :value="endpoint.path" :label="t('admin.plugins.detail.copy')" :failed-label="t('admin.plugins.detail.copyFailed')" />
        </li>
      </ul>
    </div>
    <div v-if="meta.routes?.length" class="rounded-lg bg-gray-50 p-3 dark:bg-dark-900">
      <div class="mb-2 flex flex-wrap items-center justify-between gap-2"><h5 class="text-xs font-medium">{{ t('admin.plugins.detail.customRoutes') }}</h5><button type="button" class="btn btn-secondary btn-sm" :aria-pressed="hostPaths" @click="hostPaths = !hostPaths">{{ t(hostPaths ? 'admin.plugins.detail.vendorPath' : 'admin.plugins.detail.hostPath') }}</button></div>
      <ul class="divide-y divide-gray-200 dark:divide-dark-700">
        <li v-for="route in meta.routes" :key="route.method + route.path" class="flex items-start gap-3 py-2">
          <span class="badge badge-gray mt-1 font-mono">{{ route.method }}</span>
          <div class="min-w-0 flex-1"><code class="break-all text-xs">{{ path(route.path) }}</code><p class="mt-1 text-xs text-gray-500">{{ route.type }}</p><details v-if="route.models?.length" class="mt-1 text-xs"><summary class="cursor-pointer">{{ t('admin.plugins.detail.modelScope') }}</summary><PluginModelList class="mt-2" :models="route.models" :limit="route.models.length" /></details></div>
          <PluginCopyButton :value="path(route.path)" :label="t('admin.plugins.detail.copy')" :failed-label="t('admin.plugins.detail.copyFailed')" />
        </li>
      </ul>
    </div>
    <div v-if="meta.relay?.length" class="space-y-2"><h5 class="text-xs font-medium">{{ t('admin.plugins.detail.relayRoutes') }}</h5><div v-for="route in meta.relay" :key="route.incoming_path" class="flex items-center gap-3 rounded-lg bg-gray-50 p-3 dark:bg-dark-900"><code class="min-w-0 flex-1 break-all text-xs">{{ route.incoming_path }}</code><PluginCopyButton :value="route.incoming_path" :label="t('admin.plugins.detail.copy')" :failed-label="t('admin.plugins.detail.copyFailed')" /></div></div>
    <p v-if="!meta.protocols?.length && !meta.routes?.length && !meta.relay?.length" class="text-sm text-gray-500">{{ t('admin.plugins.detail.undeclared') }}</p>
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import PluginCopyButton from './PluginCopyButton.vue'
import PluginModelList from './PluginModelList.vue'
import type { NewAPIPluginVersion, PluginTranslator } from './types'
const props = defineProps<{ meta: NewAPIPluginVersion['meta']; pluginKey: string; t: PluginTranslator }>()
const hostPaths = ref(false)
const path = (declared: string) => hostPaths.value ? `/v1/tasks/${props.pluginKey}/native${declared}` : declared
const protocols: Record<string, { method: string; path: string }[]> = {
  openai_responses: [{ method: 'POST', path: '/v1/responses' }, { method: 'GET', path: '/v1/responses/{response_id}' }],
  openai_video: [{ method: 'POST', path: '/v1/videos' }, { method: 'GET', path: '/v1/videos/{task_id}' }, { method: 'GET', path: '/v1/videos/{task_id}/content' }],
  openai_image: [{ method: 'POST', path: '/v1/images/generations' }, { method: 'POST', path: '/v1/images/edits' }]
}
</script>
