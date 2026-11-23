<template>
  <section class="space-y-4" data-credential-cleanup>
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div><h3 class="font-medium">{{ t('admin.plugins.cleanup.title') }}</h3><p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500">{{ t('admin.plugins.cleanup.hint') }}</p></div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="load">{{ t('common.refresh') }}</button>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="message" role="status" class="text-sm text-emerald-700">{{ message }}</p>
    <p v-if="busy && !jobs.length" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
    <p v-else-if="!jobs.length" class="text-sm text-gray-500">{{ t('admin.plugins.cleanup.empty') }}</p>
    <ol class="divide-y divide-gray-200 rounded-lg border border-gray-200 dark:divide-dark-700 dark:border-dark-700" v-else>
      <li v-for="job in jobs" :key="job.id" class="flex flex-wrap items-center justify-between gap-3 p-4">
        <div class="min-w-0 space-y-1 text-sm">
          <p>{{ t('admin.plugins.cleanup.account', { id: job.account_id }) }} · {{ t('admin.plugins.cleanup.version', { id: job.plugin_version_id }) }}</p>
          <p :class="job.status === 'attention' ? 'text-red-600' : 'text-gray-500'">{{ t(`admin.plugins.cleanup.statuses.${job.status}`) }} · {{ t('admin.plugins.cleanup.attempts', { count: job.attempts }) }}</p>
          <p class="text-xs text-gray-500">{{ new Date(job.created_at).toLocaleString(locale) }}<span v-if="job.last_error"> · {{ job.last_error }}</span></p>
        </div>
        <button v-if="['pending', 'attention'].includes(job.status)" type="button" class="btn btn-secondary btn-sm" :disabled="busy || !editable" data-cleanup-retry @click="retry(job.id)">{{ t('admin.plugins.cleanup.retry') }}</button>
      </li>
    </ol>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import type { PluginCredentialRevocation, PluginCleanupClient, PluginTranslator, RunPluginMutation } from './types'

const props = withDefaults(defineProps<{ client: PluginCleanupClient; t: PluginTranslator; locale: string; runMutation: RunPluginMutation; isCancelled?: (error: unknown) => boolean; editable?: boolean }>(), { editable: false, isCancelled: () => false })
const t: PluginTranslator = (key, values) => props.t(key, values)
const jobs = ref<PluginCredentialRevocation[]>([])
const busy = ref(false)
const error = ref('')
const message = ref('')
const request = new AbortController()
async function refresh() {
  const result = await props.client.listPluginCredentialRevocations(request.signal)
  if (!request.signal.aborted) jobs.value = result
}
async function operation(action?: () => Promise<unknown>) {
  if (busy.value || request.signal.aborted || action && !props.editable) return
  busy.value = true; error.value = ''; message.value = ''
  try {
    if (action) await props.runMutation(action)
    if (request.signal.aborted) return
    await refresh()
    if (action && !request.signal.aborted) message.value = t('admin.plugins.cleanup.queued')
  } catch (err) {
    if (!request.signal.aborted && !props.isCancelled(err)) error.value = t('admin.plugins.cleanup.failed')
  } finally { if (!request.signal.aborted) busy.value = false }
}
const load = () => operation()
const retry = (id: number) => operation(() => props.client.retryPluginCredentialRevocation(id, request.signal))
onMounted(load)
onBeforeUnmount(() => request.abort())
</script>
