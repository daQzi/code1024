<template>
  <details class="rounded-xl border border-gray-200 p-4 dark:border-dark-700" data-model-modifier-settings @toggle="onToggle">
    <summary class="cursor-pointer text-sm font-medium text-gray-900 dark:text-white">{{ t('requestPlugins.modifiersTitle') }}</summary>
    <form class="mt-3 space-y-3" @submit.prevent="save">
      <p class="max-w-3xl text-xs leading-5 text-gray-500">{{ t('requestPlugins.modifiersHint') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="saved" role="status" class="text-sm text-emerald-700 dark:text-emerald-400">{{ t('requestPlugins.modifiersSaved') }}</p>
      <label class="flex items-center gap-2 text-sm"><input v-model="useStartup" type="checkbox" :disabled="busy || !settings" />{{ t('requestPlugins.modifiersStartup') }}</label>
      <label class="block text-sm">
        {{ t('requestPlugins.modifiersEntries') }}
        <textarea v-model="displayEntries" class="input mt-1 font-mono text-xs" rows="5" :disabled="busy || !settings || useStartup" :aria-label="t('requestPlugins.modifiersEntries')" spellcheck="false" />
      </label>
      <div class="flex gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="load">{{ t('common.refresh') }}</button><button class="btn btn-primary btn-sm" :disabled="busy || !settings">{{ t('common.save') }}</button></div>
    </form>
  </details>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { getModelModifierSettings, saveModelModifierSettings, type ModelModifierSettings } from '@/api/admin/requestPlugins'

const { t } = useI18n()
const stepUp = useStepUp()
const controller = new AbortController()
const settings = ref<ModelModifierSettings>()
const useStartup = ref(true)
const entries = ref('')
const busy = ref(false)
const error = ref('')
const saved = ref(false)
const displayEntries = computed({
  get: () => useStartup.value ? (settings.value?.startup_exemptions ?? []).join('\n') : entries.value,
  set: (value: string) => { if (!useStartup.value) entries.value = value },
})

function errorText(error: unknown) {
  const value = error as { response?: { data?: { message?: string } }; message?: string }
  return value?.response?.data?.message ?? value?.message ?? t('common.error')
}
function accept(value: ModelModifierSettings) {
  settings.value = value
  useStartup.value = value.use_startup
  entries.value = value.exemptions.join('\n')
}
function onToggle(event: Event) {
  if ((event.target as HTMLDetailsElement).open && !settings.value && !busy.value) void load()
}
async function load() {
  if (busy.value) return
  busy.value = true; error.value = ''; saved.value = false
  try {
    const result = await getModelModifierSettings(controller.signal)
    if (!controller.signal.aborted) accept(result)
  } catch (errorValue) {
    if (!controller.signal.aborted) error.value = errorText(errorValue)
  } finally { if (!controller.signal.aborted) busy.value = false }
}
async function save() {
  if (!settings.value || busy.value) return
  const exemptions = useStartup.value ? [] : entries.value.split(/\r?\n/).map(value => value.trim()).filter(Boolean)
  if (exemptions.length > 128 || exemptions.some(value => new TextEncoder().encode(value).length > 512)) {
    error.value = t('requestPlugins.modifiersLimit')
    return
  }
  const input: ModelModifierSettings = { exemptions, use_startup: useStartup.value, revision: settings.value.revision }
  busy.value = true; error.value = ''; saved.value = false
  try {
    await stepUp.run(async () => {
      if (controller.signal.aborted) return
      const result = await saveModelModifierSettings(input, controller.signal)
      if (!controller.signal.aborted) { accept(result); saved.value = true }
    })
  } catch (errorValue) {
    if (!controller.signal.aborted && !isStepUpCancelled(errorValue)) error.value = errorText(errorValue)
  } finally { if (!controller.signal.aborted) busy.value = false }
}
onBeforeUnmount(() => controller.abort())
</script>
