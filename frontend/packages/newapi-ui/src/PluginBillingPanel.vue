<template>
  <div class="flex flex-col gap-6">
    <section v-for="(group, index) in groups" :key="index" class="space-y-3">
      <PluginModelList :models="group.models" :limit="6" expandable :more-label="t('admin.plugins.detail.moreModels')" :collapse-label="t('admin.plugins.detail.collapse')" />
      <div class="overflow-x-auto rounded-xl border border-gray-200 dark:border-dark-700">
        <table class="w-full text-left text-xs"><thead class="bg-gray-50 text-gray-500 dark:bg-dark-900"><tr><th v-for="column in ['field', 'type', 'unit', 'enum', 'description']" :key="column" class="px-3 py-3 font-medium">{{ t(`admin.plugins.detail.${column}`) }}</th></tr></thead>
          <tbody class="divide-y divide-gray-200 dark:divide-dark-700"><tr v-for="(field, name) in group.schema" :key="name"><td class="px-3 py-3 font-mono">{{ name }}</td><td class="whitespace-nowrap px-3 py-3">{{ t(`admin.plugins.detail.${field.enum?.length ? 'string' : field.type || 'number'}`) }}</td><td class="whitespace-nowrap px-3 py-3">{{ localized(field.unitLabel) || (field.unit ? t(`admin.plugins.detail.units.${field.unit}`) : '—') }}</td><td class="max-w-64 px-3 py-3 font-mono">{{ field.enum?.map(value => localized(field.enumLabels?.[value]) ? `${value} → ${localized(field.enumLabels?.[value])}` : value).join(', ') || '—' }}</td><td class="min-w-40 px-3 py-3 leading-5">{{ localized(field.description) || '—' }}</td></tr></tbody>
        </table>
      </div>
      <details v-if="group.examples?.length" class="text-xs text-gray-500"><summary class="cursor-pointer">{{ t('admin.plugins.detail.examples') }}</summary><div v-for="example in group.examples" :key="example.label" class="mt-2"><p>{{ example.label }}</p><pre class="overflow-auto rounded-lg bg-gray-50 p-3 dark:bg-dark-900">{{ JSON.stringify(example.facts, null, 2) }}</pre></div></details>
    </section>
    <p v-if="!groups.length" class="text-sm text-gray-500">{{ t('admin.plugins.detail.noUsage') }}</p>
    <section v-if="client.pricing" class="order-first space-y-3 border-b border-gray-200 pb-5 dark:border-dark-700" data-plugin-pricing>
      <div class="flex flex-wrap items-center justify-between gap-2"><h4 class="text-sm font-semibold">{{ t('admin.plugins.detail.accountPricing') }}</h4><slot name="pricing-actions" /></div>
      <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('admin.plugins.detail.pricingHint') }}</p>
      <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
      <div v-else-if="error" class="flex items-center gap-2"><p role="alert" class="text-sm text-red-600">{{ error }}</p><button type="button" class="btn btn-secondary btn-sm" @click="load">{{ t('common.refresh') }}</button></div>
      <p v-else-if="!accounts.length" class="rounded-lg bg-gray-50 p-4 text-sm text-gray-500 dark:bg-dark-900">{{ t('admin.plugins.detail.noAccounts') }}</p>
      <div v-for="account in accounts" :key="account.account_id" class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
        <div class="flex flex-wrap items-center gap-2"><h5 class="text-sm font-medium">{{ account.account_name }} <span class="text-gray-500">#{{ account.account_id }}</span></h5><span class="badge badge-gray">v{{ account.version }} · #{{ account.version_id }}</span><span class="badge" :class="account.status === 'active' && account.schedulable ? 'badge-success' : 'badge-gray'">{{ t(account.status === 'active' && account.schedulable ? 'admin.plugins.enabled' : 'admin.plugins.disabled') }}</span></div>
        <p v-if="!Object.keys(account.pricing_rules ?? {}).length" class="text-xs text-gray-500">{{ t('admin.plugins.detail.groupPricing') }}</p>
        <div v-for="(rule, model) in account.pricing_rules" :key="model" class="space-y-1 border-t border-gray-100 pt-3 text-xs leading-6 dark:border-dark-700">
          <div class="flex flex-wrap items-center gap-2"><code class="font-medium">{{ model }}</code><span class="badge badge-gray">{{ t(`admin.plugins.js.${rule.mode === 'expression' ? 'expressionPrice' : rule.mode === 'cost_plus' ? 'costPlus' : 'fixedPrice'}`) }}</span></div>
          <pre v-if="rule.mode === 'expression'" class="overflow-auto whitespace-pre-wrap break-all font-mono">{{ rule.expression }}</pre>
          <p v-else-if="rule.mode === 'cost_plus'" class="font-mono"><template v-if="rule.source_unit === 'upstream_cost'">{{ unit(rule.source_unit) }} ({{ rule.source_currency }})</template><template v-else>{{ rule.source_unit_price ?? '—' }} {{ rule.source_currency }} / {{ unit(rule.source_unit) }}</template> × {{ rule.exchange_rate ?? '1' }} × {{ rule.markup ?? '1' }}</p>
          <p v-else class="font-mono">${{ rule.unit_price ?? '—' }} / {{ unit(rule.unit) }}</p>
          <p class="text-gray-500">{{ t('admin.plugins.js.serviceFee') }} {{ rule.service_fee ?? '0' }} · {{ t('admin.plugins.js.minimumCharge') }} {{ rule.minimum ?? '0' }} · {{ t('admin.plugins.js.maximumHold') }} {{ rule.maximum ?? '—' }}</p>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import PluginModelList from './PluginModelList.vue'
import { localizedPluginText, pluginUsageGroups } from './pluginDisplay'
import type { NewAPIPluginVersion, PluginAccountPricingSummary, PluginCenterClient, PluginTranslator } from './types'
const props = defineProps<{ plugin: Pick<NewAPIPluginVersion, 'key' | 'meta'>; client: PluginCenterClient; t: PluginTranslator; locale: string }>()
const groups = computed(() => pluginUsageGroups(props.plugin.meta))
const localized = (value: Parameters<typeof localizedPluginText>[0]) => localizedPluginText(value, props.locale)
const unit = (value?: string) => value ? props.t(`admin.plugins.js.units.${value}`) : '—'
const accounts = ref<PluginAccountPricingSummary[]>([])
const loading = ref(false)
const error = ref('')
let request: AbortController | undefined
async function load() {
  request?.abort()
  const active = new AbortController()
  request = active
  accounts.value = []
  error.value = ''
  loading.value = true
  try {
    const result = await props.client.pricing?.(props.plugin.key, active.signal)
    if (!active.signal.aborted) accounts.value = result ?? []
  } catch {
    if (!active.signal.aborted) error.value = props.t('admin.plugins.detail.pricingFailed')
  } finally {
    if (!active.signal.aborted) loading.value = false
  }
}
watch([() => props.plugin.key, () => props.client], load, { immediate: true })
onBeforeUnmount(() => request?.abort())
</script>
