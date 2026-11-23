<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600" data-plugin-models>
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div><h4 class="text-sm font-medium">{{ t('admin.plugins.js.modelsTitle') }}</h4><p class="mt-1 text-xs text-gray-500">{{ t('admin.plugins.js.modelsHint') }}</p></div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" data-models-query @click="query">{{ t(loading ? 'common.loading' : 'admin.plugins.js.modelsQuery') }}</button>
    </div>
    <p v-if="error" role="alert" class="text-xs text-red-600">{{ error }}</p>
    <div v-if="result" class="space-y-2" role="status">
      <p class="text-xs text-gray-500">{{ t('admin.plugins.js.modelsCount', { count: result.models.length }) }} · {{ new Date(result.fetched_at).toLocaleString() }}</p>
      <p v-if="!result.models.length" class="text-xs text-gray-500" data-models-empty>{{ t('admin.plugins.js.modelsEmpty') }}</p>
      <textarea v-else :value="result.models.join('\n')" class="input max-h-64 font-mono text-xs" rows="6" readonly :aria-label="t('admin.plugins.js.modelsTitle')" data-models-result />
    </div>
  </section>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { queryNewAPIPluginModels, type NewAPIPluginModels } from '@/api/admin/newapiPlugins'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

const props = defineProps<{ accountId: number; versionId: number }>()
const { t } = useI18n()
const stepUp = useStepUp()
const loading = ref(false)
const result = ref<NewAPIPluginModels>()
const error = ref('')
let request: AbortController | undefined
let sequence = 0
function reset() { sequence++; request?.abort(); result.value = undefined; error.value = ''; loading.value = false }
async function query() {
  reset()
  const current = sequence
  const accountID = props.accountId
  const versionID = props.versionId
  const controller = new AbortController()
  request = controller
  loading.value = true
  try {
    const models = await stepUp.run(() => queryNewAPIPluginModels(accountID, controller.signal))
    if (controller.signal.aborted || current !== sequence) return
    if (models.account_id !== accountID || models.plugin_version_id !== versionID) { error.value = t('admin.plugins.js.balanceChanged'); return }
    result.value = models
  } catch (e) {
    if (controller.signal.aborted || current !== sequence || isStepUpCancelled(e)) return
    error.value = (e as { response?: { data?: { message?: string } } }).response?.data?.message ?? t('admin.plugins.js.modelsFailed')
  } finally { if (current === sequence) loading.value = false }
}
watch(() => [props.accountId, props.versionId], reset)
onBeforeUnmount(reset)
</script>
