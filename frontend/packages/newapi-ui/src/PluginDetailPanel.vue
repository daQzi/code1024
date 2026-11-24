<template>
  <div class="space-y-5">
    <header class="space-y-3">
      <div class="flex items-center gap-3"><PluginIcon :icon="plugin.meta.icon" :name="name" :icon-component="iconComponent" /><div class="min-w-0 flex-1"><h3 class="break-words text-lg font-semibold">{{ name }}</h3><div class="flex items-center gap-1"><code class="break-all text-xs text-gray-500">{{ plugin.key }}</code><PluginCopyButton :value="plugin.key" :label="t('admin.plugins.detail.copy')" :failed-label="t('admin.plugins.detail.copyFailed')" /></div></div><span class="badge badge-primary font-mono">v{{ plugin.version }}</span></div>
      <p class="text-sm leading-6 text-gray-500 dark:text-dark-400">{{ localized(plugin.meta.description) }}</p>
      <div class="flex flex-wrap gap-2">
        <button v-if="'id' in plugin && (plugin.capabilities.task || plugin.capabilities.provider)" type="button" class="btn btn-primary btn-sm" :disabled="busy || !editable || !plugin.compatibility?.runnable && !plugin.enabled" @click="emit('activate', plugin)">{{ t(plugin.enabled ? 'admin.plugins.disable' : 'admin.plugins.enable') }}</button>
        <button v-if="factory" type="button" class="btn btn-secondary btn-sm" :disabled="busy || !editable" @click="emit('restore', plugin.key)">{{ t('admin.plugins.center.restore') }}</button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !editable" @click="emit('upload', plugin.key)">{{ t('admin.plugins.detail.uploadVersion') }}</button>
        <button v-if="'id' in plugin && client.remove" type="button" class="btn btn-secondary btn-sm text-red-600 dark:text-red-400" :disabled="busy || !editable || !canDelete(plugin)" @click="emit('remove', plugin)">{{ t('admin.plugins.detail.deleteVersion') }}</button>
        <slot name="detail-actions" />
      </div>
    </header>
    <div role="tablist" class="sticky -top-4 z-10 flex gap-1 overflow-x-auto border-b border-gray-200 bg-white py-1 dark:border-dark-700 dark:bg-dark-800" :aria-label="t('admin.plugins.center.details')">
      <button v-for="item in tabs" :id="`${uid}-${item}`" :key="item" type="button" role="tab" :tabindex="tab === item ? 0 : -1" :aria-selected="tab === item" :aria-controls="`${uid}-content`" class="shrink-0 border-b-2 px-3 py-3 text-sm font-medium focus-visible:ring-2 focus-visible:ring-primary-500" :class="tab === item ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-900 dark:hover:text-dark-100'" @click="tab = item" @keydown="navigateTab($event, item)">{{ t(`admin.plugins.detail.${item}`) }}</button>
    </div>
    <div :id="`${uid}-content`" role="tabpanel" :aria-labelledby="`${uid}-${tab}`">
      <div v-if="tab === 'overview'" class="space-y-5">
        <section class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"><h4 class="mb-4 text-sm font-semibold">{{ t('admin.plugins.detail.metadata') }}</h4><dl class="grid gap-4 text-sm sm:grid-cols-2">
          <div><dt class="text-xs text-gray-500">{{ t('admin.plugins.detail.priority') }}</dt><dd class="mt-1">{{ plugin.meta.sortPriority ?? 0 }}</dd></div>
          <div v-if="plugin.capabilities.task"><dt class="text-xs text-gray-500">{{ t('admin.plugins.detail.fetchMode') }}</dt><dd class="mt-1">{{ plugin.meta.fetchMode || '—' }}</dd></div>
          <div><dt class="text-xs text-gray-500">{{ t('admin.plugins.center.author') }}</dt><dd class="mt-1">{{ plugin.meta.author?.name || '—' }}</dd></div>
          <div><dt class="text-xs text-gray-500">{{ t('admin.plugins.center.source') }}</dt><dd class="mt-1 break-all">{{ 'origin' in plugin ? plugin.origin : t('admin.plugins.center.sources.system') }}</dd></div>
          <div v-if="plugin.capabilities.task || plugin.capabilities.provider"><dt class="text-xs text-gray-500">{{ t('admin.plugins.detail.channelTypes') }}</dt><dd class="mt-1">{{ plugin.meta.channelTypes?.map(value => `#${value}`).join(', ') || t('admin.plugins.detail.undeclared') }}</dd></div>
          <div><dt class="text-xs text-gray-500">{{ t('admin.plugins.detail.website') }}</dt><dd class="mt-1 break-all"><a v-if="website" :href="website" target="_blank" rel="noopener noreferrer" class="text-primary-600 hover:underline dark:text-primary-400">{{ plugin.meta.website }}</a><span v-else>{{ t('admin.plugins.detail.undeclared') }}</span></dd></div>
          <div class="sm:col-span-2"><dt class="text-xs text-gray-500">{{ t('admin.plugins.detail.baseUrl') }}</dt><dd class="mt-1 flex items-center gap-2"><code class="min-w-0 flex-1 break-all text-xs">{{ plugin.meta.baseUrl || '—' }}</code><PluginCopyButton v-if="plugin.meta.baseUrl" :value="plugin.meta.baseUrl" :label="t('admin.plugins.detail.copy')" :failed-label="t('admin.plugins.detail.copyFailed')" /></dd></div>
          <div class="sm:col-span-2"><dt class="text-xs text-gray-500">SHA256</dt><dd class="mt-1 break-all font-mono text-xs">{{ plugin.sha256 }}</dd></div>
        </dl></section>
        <section v-if="plugin.capabilities.task || plugin.capabilities.provider" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"><div class="mb-3 flex items-center gap-2"><h4 class="text-sm font-semibold">{{ t('admin.plugins.center.models') }}</h4><span class="badge badge-gray">{{ plugin.meta.models?.length ?? 0 }}</span><PluginCopyButton class="ml-auto" :value="plugin.meta.models?.join('\n') ?? ''" :label="t('admin.plugins.detail.copyModels')" :failed-label="t('admin.plugins.detail.copyFailed')" /></div><PluginModelList :models="plugin.meta.models ?? []" :limit="plugin.meta.models?.length ?? 0" /></section>
        <PluginEndpointsPanel v-if="plugin.capabilities.task || plugin.capabilities.provider" :meta="plugin.meta" :plugin-key="plugin.key" :t="t" />
        <section class="space-y-2 rounded-xl border border-gray-200 p-4 dark:border-dark-700"><h4 class="text-sm font-semibold">{{ t('admin.plugins.center.compatibility') }}</h4><ul class="list-inside list-disc space-y-1 text-xs leading-6 text-gray-500 dark:text-dark-400"><li v-for="issue in [...(plugin.compatibility?.blockers ?? []), ...(plugin.compatibility?.warnings ?? [])]" :key="issue">{{ issue }}</li><li v-if="plugin.compatibility?.runnable">{{ t('admin.plugins.center.runnable') }} {{ plugin.compatibility.kinds.join(', ') }}</li></ul><PluginModelList v-if="plugin.capabilities.transform" :models="plugin.capabilities.hooks ?? []" :limit="100" /></section>
      </div>
      <PluginBillingPanel v-else-if="tab === 'billing'" :plugin="plugin" :client="client" :t="t" :locale="locale"><template #pricing-actions><slot name="pricing-actions" /></template></PluginBillingPanel>
      <section v-else-if="tab === 'history'" class="space-y-3">
        <p v-if="historyLoading" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
        <div v-for="version in history" :key="version.id" class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-gray-200 p-3 dark:border-dark-700"><div class="space-y-1 text-xs"><div class="flex items-center gap-2"><span class="font-mono font-medium">v{{ version.version }}</span><span v-if="version.active" class="badge badge-primary">{{ t('admin.plugins.center.active') }}</span></div><p class="break-all text-gray-500">{{ version.origin }} · {{ version.sha256.slice(0, 12) }}</p></div><div class="flex gap-2"><button v-if="!version.active" type="button" class="btn btn-secondary btn-sm" :disabled="busy || !editable || !version.compatibility?.runnable" @click="emit('activate', version, true)">{{ t('admin.plugins.js.useVersion') }}</button><button v-if="canDelete(version)" type="button" class="btn btn-secondary btn-sm text-red-600" :disabled="busy || !editable" @click="emit('remove', version)">{{ t('admin.plugins.detail.deleteVersion') }}</button></div></div>
        <p v-if="!historyLoading && !history.length" class="text-sm text-gray-500">{{ t('admin.plugins.detail.noHistory') }}</p>
        <p class="text-xs leading-6 text-gray-500">{{ t('admin.plugins.js.versionNote') }}</p>
      </section>
      <slot v-else-if="tab === 'source' || tab === 'diff'" name="source" :mode="tab === 'source' ? 'source' : 'diff'" />
      <PluginSandboxPanel v-else-if="tab === 'sandbox' && 'id' in plugin" :key="plugin.id" :plugin="plugin" :client="client" :t="t" :select-component="selectComponent" :editable="editable" :run-mutation="runMutation" :cancel-mutation="cancelMutation" :is-cancelled="isCancelled" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, useId, watch, type Component } from 'vue'
import PluginBillingPanel from './PluginBillingPanel.vue'
import PluginCopyButton from './PluginCopyButton.vue'
import PluginEndpointsPanel from './PluginEndpointsPanel.vue'
import PluginIcon from './PluginIcon.vue'
import PluginModelList from './PluginModelList.vue'
import PluginSandboxPanel from './PluginSandboxPanel.vue'
import { localizedPluginText } from './pluginDisplay'
import type { NewAPIPluginVersion, PluginCenterClient, PluginFactory, PluginTranslator } from './types'
const props = defineProps<{
  plugin: NewAPIPluginVersion | PluginFactory; factory?: PluginFactory; history: NewAPIPluginVersion[]; historyLoading: boolean
  client: PluginCenterClient; t: PluginTranslator; locale: string; editable: boolean; busy: boolean
  selectComponent?: Component; iconComponent?: Component; hasSource: boolean
  runMutation?: (action: () => Promise<unknown>) => Promise<unknown>
  cancelMutation?: () => void; isCancelled?: (error: unknown) => boolean
}>()
const emit = defineEmits<{ activate: [version: NewAPIPluginVersion, enabled?: boolean]; restore: [key: string]; remove: [version: NewAPIPluginVersion]; upload: [key: string] }>()
defineSlots<{
  'detail-actions'?: () => unknown
  'pricing-actions'?: () => unknown
  source?: (props: { mode: 'source' | 'diff' }) => unknown
}>()
const uid = useId()
const tab = ref('overview')
function navigateTab(event: KeyboardEvent, current: string) {
  const position = tabs.value.indexOf(current)
  const next = event.key === 'ArrowRight' ? (position + 1) % tabs.value.length : event.key === 'ArrowLeft' ? (position - 1 + tabs.value.length) % tabs.value.length : event.key === 'Home' ? 0 : event.key === 'End' ? tabs.value.length - 1 : undefined
  if (next === undefined) return
  event.preventDefault()
  tab.value = tabs.value[next]!
  const container = (event.currentTarget as HTMLElement).parentElement
  const nextButton = container?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]
  nextButton?.focus()
}
const localized = (value: Parameters<typeof localizedPluginText>[0]) => localizedPluginText(value, props.locale)
const name = computed(() => localized(props.plugin.meta.name_i18n) || props.plugin.meta.name)
const tabs = computed(() => ['overview', ...(props.plugin.capabilities.task || props.plugin.meta.usageProfiles?.length || Object.keys(props.plugin.meta.usageSchema ?? {}).length ? ['billing'] : []), ...(props.hasSource ? ['source'] : []), 'history', ...(props.hasSource ? ['diff'] : []), ...('id' in props.plugin && props.client.dryRun ? ['sandbox'] : [])])
watch(tabs, available => {
  if (!available.includes(tab.value)) tab.value = 'overview'
})
const website = computed(() => {
  try {
    const url = new URL(props.plugin.meta.website ?? '')
    if (['https:', 'http:'].includes(url.protocol)) return url.href
  } catch { return undefined }
  return undefined
})
const canDelete = (version: NewAPIPluginVersion) => !!props.client.remove && !version.origin.startsWith('builtin') && props.factory?.sha256 !== version.sha256
</script>
