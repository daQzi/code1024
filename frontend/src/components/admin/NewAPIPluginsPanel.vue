<template>
  <section class="card space-y-5 p-5">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.plugins.js.title') }}</h3>
        <p class="mt-1 max-w-3xl text-sm text-gray-500">{{ t('admin.plugins.js.description') }}</p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="load">{{ t('common.refresh') }}</button>
    </header>
    <div class="flex flex-wrap items-end gap-3">
      <label class="min-w-48 flex-1"><span class="input-label">{{ t('admin.plugins.js.builtin') }}</span>
        <select v-model="builtin" class="input"><option value="">{{ t('admin.plugins.js.choose') }}</option><option v-for="key in catalog?.builtins ?? []" :key="key" :value="key">{{ key }}</option></select>
      </label>
      <button type="button" class="btn btn-primary" :disabled="busy || !builtin" @click="install({ builtin })">{{ t('admin.plugins.js.installBuiltin') }}</button>
      <label class="btn btn-secondary cursor-pointer">{{ t('admin.plugins.js.upload') }}<input type="file" accept=".js,text/javascript" class="hidden" :disabled="busy" @change="upload" /></label>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <p v-if="message" role="status" class="text-sm text-emerald-600 dark:text-emerald-400">{{ message }}</p>
    <p v-if="!catalog" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
    <div v-else class="divide-y divide-gray-200 dark:divide-dark-700">
      <p v-if="!catalog.plugins.length" class="py-6 text-sm text-gray-500">{{ t('admin.plugins.js.empty') }}</p>
      <article v-for="plugin in catalog.plugins" :key="plugin.key" class="space-y-3 py-4">
        <div class="flex flex-wrap items-center gap-3">
          <strong class="text-sm text-gray-900 dark:text-white">{{ plugin.meta.name }}</strong>
          <code class="text-xs text-gray-500">{{ plugin.key }} · v{{ plugin.version }}</code>
          <span class="rounded bg-gray-100 px-2 py-1 text-xs dark:bg-dark-700">{{ t(plugin.enabled ? 'admin.plugins.enabled' : 'admin.plugins.disabled') }}</span>
          <div class="ml-auto flex gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="versions(plugin)">{{ t('admin.plugins.js.versions') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="activate(plugin.id, !plugin.enabled)">{{ t(plugin.enabled ? 'admin.plugins.disable' : 'admin.plugins.enable') }}</button>
          </div>
        </div>
        <div class="flex flex-wrap gap-2 text-xs text-gray-500">
          <span v-if="plugin.capabilities.task">Task</span><span v-if="plugin.capabilities.provider">Provider</span><span v-if="plugin.capabilities.transform">Transform</span>
          <span>{{ t('admin.plugins.js.modelCount', { count: plugin.meta.models?.length ?? 0 }) }}</span>
          <span v-for="cap in plugin.capabilities.required_capabilities" :key="cap">{{ cap }}</span>
        </div>
        <div v-if="historyKey === plugin.key" class="space-y-2 rounded-lg bg-gray-50 p-3 dark:bg-dark-900">
          <div v-for="revision in history" :key="revision.id" class="flex flex-wrap items-center justify-between gap-2 text-xs">
            <span class="font-mono">v{{ revision.version }} · {{ revision.sha256.slice(0, 12) }}</span>
            <button v-if="!revision.active" type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="activate(revision.id, true)">{{ t('admin.plugins.js.useVersion') }}</button>
          </div>
        </div>
      </article>
    </div>
    <p class="text-xs leading-5 text-gray-500">{{ t('admin.plugins.js.versionNote') }}</p>
  </section>
  <GenerationReviewPanel />
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import GenerationReviewPanel from './GenerationReviewPanel.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { activateNewAPIPlugin, installNewAPIPlugin, listNewAPIPlugins, listNewAPIPluginVersions, type NewAPIPluginCatalog, type NewAPIPluginVersion } from '@/api/admin/newapiPlugins'
const { t } = useI18n()
const stepUp = useStepUp()
const catalog = ref<NewAPIPluginCatalog>()
const builtin = ref('')
const busy = ref(false)
const error = ref('')
const message = ref('')
const historyKey = ref('')
const history = ref<NewAPIPluginVersion[]>([])
function errorText(e: unknown) { return (e as { response?: { data?: { message?: string } }; message?: string }).response?.data?.message ?? (e as Error).message }
async function load() { try { catalog.value = await listNewAPIPlugins(); error.value = '' } catch (e) { error.value = errorText(e) } }
async function install(input: { source?: string; builtin?: string }) {
  busy.value = true; error.value = ''; message.value = ''
  try { const version = await stepUp.run(() => installNewAPIPlugin(input)); message.value = t('admin.plugins.js.installed', { version: version.version }); await load(); await versions(version) } catch (e) { if (!isStepUpCancelled(e)) error.value = errorText(e) } finally { busy.value = false }
}
async function upload(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]; input.value = ''
  if (!file) return
  if (file.size > 1048576) { error.value = t('admin.plugins.js.tooLarge'); return }
  await install({ source: await file.text() })
}
async function activate(id: number, enabled: boolean) {
  busy.value = true; error.value = ''; message.value = ''
  try { await stepUp.run(() => activateNewAPIPlugin(id, enabled)); await load(); historyKey.value = '' } catch (e) { if (!isStepUpCancelled(e)) error.value = errorText(e) } finally { busy.value = false }
}
async function versions(plugin: NewAPIPluginVersion) { try { history.value = await listNewAPIPluginVersions(plugin.key); historyKey.value = plugin.key } catch (e) { error.value = errorText(e) } }
onMounted(load)
</script>
