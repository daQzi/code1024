<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600" data-plugin-balance>
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div><h4 class="text-sm font-medium">{{ t('admin.plugins.js.balanceTitle') }}</h4><p class="mt-1 text-xs text-gray-500">{{ t('admin.plugins.js.balanceHint') }}</p></div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" data-balance-query @click="query">{{ t(loading ? 'common.loading' : 'admin.plugins.js.balanceQuery') }}</button>
    </div>
    <p v-if="error" role="alert" class="text-xs text-red-600">{{ error }}</p>
    <div v-if="result" class="space-y-2" role="status">
      <p class="text-lg font-semibold" data-balance-amount>{{ result.balance.unlimited ? t('admin.plugins.js.balanceUnlimited') : result.balance.amount === null ? t('admin.plugins.js.balanceUnknown') : `${number(result.balance.amount)} ${result.balance.currency}` }}</p>
      <p class="text-xs text-gray-500">{{ t('admin.plugins.js.balanceFetched') }} {{ date(result.fetched_at) }} · {{ result.plugin_key }} v{{ result.plugin_version }}</p>
      <div v-for="(window, index) in result.balance.windows" :key="index" class="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-gray-100 pt-2 text-xs dark:border-dark-700" data-balance-window>
        <span class="font-medium">{{ localized(window.name) }}</span><span>{{ number(window.used) }} / {{ number(window.limit) }} {{ window.unit }}</span><span v-if="window.resetAt" class="text-gray-500">{{ t('admin.plugins.js.balanceResets') }} {{ date(window.resetAt * 1000) }}</span>
      </div>
    </div>
  </section>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { queryNewAPIPluginBalance, type LocalizedText, type NewAPIPluginBalance } from '@/api/admin/newapiPlugins'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

const props = defineProps<{ accountId: number; versionId: number }>()
const { t, locale } = useI18n()
const stepUp = useStepUp()
const loading = ref(false)
const result = ref<NewAPIPluginBalance>()
const error = ref('')
let request: AbortController | undefined
let sequence = 0
const number = (value: number) => new Intl.NumberFormat(locale.value, { maximumFractionDigits: 6 }).format(value)
const localized = (value: LocalizedText) => typeof value === 'string' ? value : value[locale.value] ?? value.zh ?? value.en ?? Object.values(value)[0] ?? ''
function date(value: string | number) {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString(locale.value) : '—'
}
function reset() {
  sequence++
  request?.abort()
  result.value = undefined
  error.value = ''
  loading.value = false
}
async function query() {
  reset()
  const current = sequence
  const accountID = props.accountId
  const versionID = props.versionId
  const controller = new AbortController()
  request = controller
  loading.value = true
  try {
    const balance = await stepUp.run(() => queryNewAPIPluginBalance(accountID, controller.signal))
    if (controller.signal.aborted || sequence !== current) return
    if (balance.account_id !== accountID || balance.plugin_version_id !== versionID) {
      error.value = t('admin.plugins.js.balanceChanged')
      return
    }
    result.value = balance
  } catch (e) {
    if (controller.signal.aborted || sequence !== current || isStepUpCancelled(e)) return
    error.value = (e as { response?: { data?: { message?: string } } }).response?.data?.message ?? t('admin.plugins.js.balanceFailed')
  } finally {
    if (sequence === current) loading.value = false
  }
}
watch(() => [props.accountId, props.versionId], reset)
onBeforeUnmount(reset)
</script>
