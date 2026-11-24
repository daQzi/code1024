<template>
  <div class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-700">
    <div class="flex items-center justify-between gap-2">
      <h4 class="text-sm font-semibold">{{ t('admin.plugins.js.pricingTitle') }}</h4>
      <button type="button" class="btn btn-secondary btn-sm" @click="add">{{ t('admin.plugins.js.addPrice') }}</button>
    </div>
    <p class="input-hint">{{ t('admin.plugins.js.pricingHint') }}</p>
    <datalist :id="`${uid}-models`"><option v-for="model in concreteModels" :key="model" :value="model" /></datalist>
    <div v-for="(row, index) in rows" :key="row.id" class="space-y-3 rounded-lg bg-gray-50 p-3 dark:bg-dark-900">
      <div class="grid gap-3 sm:grid-cols-2">
        <label><span class="input-label">{{ t('admin.plugins.js.priceModel') }}</span><input v-model="row.model" class="input" placeholder="*" :list="`${uid}-models`" /></label>
        <div class="min-w-0">
          <span class="input-label">{{ t('admin.plugins.js.priceMode') }}</span>
          <Select v-model="row.mode" :options="modeOptions" :aria-label="t('admin.plugins.js.priceMode')" @change="normalizeMode(row)" />
        </div>
        <div v-if="row.mode !== 'expression'" class="min-w-0">
          <span class="input-label">{{ t('admin.plugins.js.priceUnit') }}</span>
          <Select v-model="row.unit" :options="row.mode === 'cost_plus' ? costUnits : fixedUnits" :aria-label="t('admin.plugins.js.priceUnit')" />
        </div>
        <label v-if="row.mode !== 'expression' && row.unit !== 'upstream_cost'"><span class="input-label">{{ t(row.mode === 'fixed' ? 'admin.plugins.js.salePrice' : 'admin.plugins.js.sourcePrice') }}</span><input v-model="row.price" type="number" min="0" step="any" class="input" /></label>
      </div>
      <label v-if="row.mode === 'expression'" class="block">
        <span class="input-label">{{ t('admin.plugins.js.expressionBody') }}</span>
        <textarea v-model="row.expression" class="input font-mono text-xs" rows="3" maxlength="4096" spellcheck="false" />
        <span class="input-hint">{{ t('admin.plugins.js.expressionHint') }}</span>
      </label>
      <details v-if="usageGroups(row.model).length" class="rounded-lg border border-gray-200 p-3 text-xs dark:border-dark-700" data-usage-reference>
        <summary class="cursor-pointer font-medium">{{ t('admin.plugins.detail.availableUsage') }}</summary>
        <div v-for="(group, groupIndex) in usageGroups(row.model)" :key="groupIndex" class="mt-3 space-y-2">
          <p class="break-words font-mono text-gray-500">{{ group.models.join(', ') || '*' }}</p>
          <div v-for="(field, name) in group.schema" :key="name" class="flex flex-wrap items-start gap-2 leading-5">
            <button v-if="row.mode === 'expression'" type="button" class="rounded-md bg-primary-50 px-2 py-0.5 font-mono text-primary-700 dark:bg-dark-700 dark:text-primary-300" :title="t('admin.plugins.detail.insertUsage')" @click="insertUsage(row, String(name))">{{ `u(${JSON.stringify(name)})` }}</button>
            <code v-else>{{ name }}</code>
            <span class="text-gray-500">{{ localized(field.description) || fieldTypeLabel(field) }}{{ fieldUnitLabel(field) ? ` · ${fieldUnitLabel(field)}` : '' }}{{ field.enum?.length ? ` · ${field.enum.join(' / ')}` : '' }}</span>
          </div>
        </div>
      </details>
      <div class="grid gap-3 sm:grid-cols-3">
        <label><span class="input-label">{{ t('admin.plugins.js.serviceFee') }}</span><input v-model="row.serviceFee" type="number" min="0" step="any" class="input" /></label>
        <label><span class="input-label">{{ t('admin.plugins.js.minimumCharge') }}</span><input v-model="row.minimum" type="number" min="0" step="any" class="input" /></label>
        <label><span class="input-label">{{ t('admin.plugins.js.maximumHold') }}</span><input v-model="row.maximum" type="number" min="0" step="any" class="input" /></label>
      </div>
      <div v-if="row.mode === 'cost_plus'" class="grid gap-3 sm:grid-cols-3">
        <label><span class="input-label">{{ t('admin.plugins.js.sourceCurrency') }}</span><input v-model="row.currency" maxlength="3" class="input uppercase" /></label>
        <label><span class="input-label">{{ t('admin.plugins.js.exchangeRate') }}</span><input v-model="row.exchangeRate" type="number" min="0" step="any" class="input" /></label>
        <label><span class="input-label">{{ t('admin.plugins.js.markup') }}</span><input v-model="row.markup" type="number" min="0" step="any" class="input" /></label>
      </div>
      <button type="button" class="text-xs text-red-600" @click="rows.splice(index, 1)">{{ t('common.remove') }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import {
  localizedPluginText,
  pluginUsageFieldTypeLabel,
  pluginUsageGroupsForModel,
  pluginUsageUnitLabel,
  type NewAPIPluginVersion,
  type UsageFieldSchema
} from '../../../packages/newapi-ui/src'
const props = defineProps<{ value?: unknown; pluginMeta?: NewAPIPluginVersion['meta'] }>()
const { t, locale } = useI18n()
const uid = useId()
const localized = (value: Parameters<typeof localizedPluginText>[0]) => localizedPluginText(value, locale?.value ?? 'en')
const translate = (key: string) => t(key)
const concreteModels = computed(() => (props.pluginMeta?.models ?? []).filter(model => !model.includes('*')))
const fieldTypeLabel = (field: UsageFieldSchema) => pluginUsageFieldTypeLabel(field, translate)
const fieldUnitLabel = (field: UsageFieldSchema) => pluginUsageUnitLabel(field, translate, locale?.value ?? 'en')
function usageGroups(model: string) {
  if (!props.pluginMeta) return []
  return pluginUsageGroupsForModel(props.pluginMeta, model)
}
function insertUsage(row: Row, name: string) {
  const field = `u(${JSON.stringify(name)})`
  row.expression = row.expression.trim() ? `${row.expression} + ${field}` : field
}
const modeOptions = computed(() => [
  { value: 'fixed', label: t('admin.plugins.js.fixedPrice') },
  { value: 'cost_plus', label: t('admin.plugins.js.costPlus') },
  { value: 'expression', label: t('admin.plugins.js.expressionPrice') }
])
const fixedUnits = computed(() => ['request', 'second', 'token', 'credit'].map(value => ({ value, label: t(`admin.plugins.js.units.${value}`) })))
const costUnits = computed(() => [...fixedUnits.value, { value: 'upstream_cost', label: t('admin.plugins.js.units.upstream_cost') }])
type Rule = Record<string, unknown>
interface Row { id: number; model: string; mode: string; expression: string; unit: string; price: string; currency: string; exchangeRate: string; markup: string; serviceFee: string; minimum: string; maximum: string; original: Rule }
const rows = ref<Row[]>([])
let sequence = 0
watch(() => props.value, value => {
  rows.value = Object.entries((value ?? {}) as Record<string, Rule>).map(([model, rule]) => ({
    id: ++sequence, model, mode: String(rule.mode ?? 'fixed'), expression: String(rule.expression ?? ''), unit: String(rule.mode === 'cost_plus' ? rule.source_unit : rule.unit ?? 'second'), price: String(rule.unit_price ?? rule.source_unit_price ?? ''), currency: String(rule.source_currency ?? 'USD'), exchangeRate: String(rule.exchange_rate ?? '1'), markup: String(rule.markup ?? '1.2'), serviceFee: String(rule.service_fee ?? '0'), minimum: String(rule.minimum ?? '0'), maximum: String(rule.maximum ?? ''), original: { ...rule }
  }))
}, { immediate: true })
function add() { rows.value.push({ id: ++sequence, model: '*', mode: 'fixed', expression: '', unit: 'second', price: '', currency: 'USD', exchangeRate: '1', markup: '1.2', serviceFee: '0', minimum: '0', maximum: '', original: {} }) }
function normalizeMode(row: Row) { if (row.mode !== 'cost_plus' && row.unit === 'upstream_cost') row.unit = 'second' }
function rules(): Record<string, Rule> {
  const result: Record<string, Rule> = Object.create(null)
  for (const row of rows.value) {
    const model = row.model.trim()
    const finalBill = row.mode === 'cost_plus' && row.unit === 'upstream_cost'
    const expression = row.mode === 'expression'
    if (!['fixed', 'cost_plus', 'expression'].includes(row.mode) || !model || model in result || (!finalBill && !expression && (row.price === '' || !Number.isFinite(Number(row.price)) || Number(row.price) < 0)) || ((finalBill || expression) && !(Number(row.maximum) > 0))) throw new Error(t('admin.plugins.js.invalidPrice'))
    if (expression && (!row.expression.trim() || new TextEncoder().encode(row.expression).length > 4096)) throw new Error(t('admin.plugins.js.invalidExpressionPrice'))
    if (row.mode === 'cost_plus' && (!/^[A-Z]{3}$/.test(row.currency.toUpperCase()) || !(Number(row.exchangeRate) > 0) || !Number.isFinite(Number(row.markup)) || Number(row.markup) < 0)) throw new Error(t('admin.plugins.js.invalidPrice'))
    if (model.length > 100 || [row.serviceFee, row.minimum, ...(row.maximum ? [row.maximum] : [])].some(value => value === '' || !Number.isFinite(Number(value)) || Number(value) < 0) || (row.maximum && Number(row.maximum) < Number(row.minimum))) throw new Error(t('admin.plugins.js.invalidPrice'))
    const original = { ...row.original }
    for (const key of ['expression', 'unit', 'unit_price', 'source_unit', 'source_unit_price', 'source_currency', 'exchange_rate', 'markup']) delete original[key]
    if (expression) result[model] = { ...original, mode: 'expression', expression: row.expression }
    else if (row.mode === 'fixed') result[model] = { ...original, mode: 'fixed', unit: row.unit, unit_price: row.price }
    else result[model] = { ...original, mode: 'cost_plus', source_unit: row.unit, source_unit_price: row.price, source_currency: row.currency.toUpperCase(), exchange_rate: row.exchangeRate, markup: row.markup }
    result[model]!.service_fee = row.serviceFee
    result[model]!.minimum = row.minimum
    if (finalBill) delete result[model]!.source_unit_price
    if (row.maximum) result[model]!.maximum = row.maximum
    else delete result[model]!.maximum
  }
  return result
}
defineExpose({ rules })
</script>
