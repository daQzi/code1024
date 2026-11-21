<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
    <label class="input-label" :for="`${uid}-plugin`">{{ t('admin.plugins.js.accountTitle') }}</label>
    <select :id="`${uid}-plugin`" v-model="revision" class="input">
      <option :value="0">{{ t('admin.plugins.js.native') }}</option>
      <option v-if="revision && !plugins.some(p => p.id === revision)" :value="revision">{{ t('admin.plugins.js.pinned', { id: revision }) }}</option>
      <option v-for="plugin in plugins.filter(p => p.capabilities.task)" :key="plugin.id" :value="plugin.id" :disabled="!plugin.enabled">{{ plugin.meta.name }} · v{{ plugin.version }}{{ plugin.enabled ? '' : ` (${t('admin.plugins.disabled')})` }}</option>
    </select>
    <p class="input-hint">{{ t('admin.plugins.js.accountHint') }}</p>
    <p v-if="error" role="alert" class="text-xs text-red-600">{{ error }}</p>
    <template v-if="revision">
      <label class="block"><span class="input-label">{{ t('admin.plugins.js.upstreamKind') }}</span><select v-model="upstreamKind" class="input"><option value="vendor">{{ t('admin.plugins.js.vendorUpstream') }}</option><option value="new_api">{{ t('admin.plugins.js.newAPIUpstream') }}</option></select></label>
      <p v-if="saved && !replaceConfig" class="text-xs text-gray-500">{{ t('admin.plugins.js.configSaved') }}</p>
      <button v-if="saved && !replaceConfig" type="button" class="btn btn-secondary btn-sm" @click="replaceConfig = true">{{ t('admin.plugins.js.replaceConfig') }}</button>
      <div v-else class="space-y-3">
        <div v-for="(field, name) in fields" :key="name">
          <label class="input-label" :for="`${uid}-${name}`">{{ localized(field.title) || name }}</label>
          <select v-if="field.enum" :id="`${uid}-${name}`" v-model="values[name]" class="input"><option v-for="(value, index) in field.enum" :key="String(value)" :value="value">{{ localized(field.enumTitles?.[index]) || value }}</option></select>
          <input v-else-if="field.type === 'boolean'" :id="`${uid}-${name}`" v-model="values[name]" type="checkbox" class="rounded" />
          <input v-else-if="field.type === 'number' || field.type === 'integer'" :id="`${uid}-${name}`" v-model.number="values[name]" type="number" class="input" :min="field.minimum" :max="field.maximum" :step="field.type === 'integer' ? 1 : 'any'" />
          <textarea v-else-if="field.type === 'object' || field.type === 'array' || ['textarea', 'keywords'].includes(field.format ?? '')" :id="`${uid}-${name}`" :value="String(values[name] ?? '')" class="input font-mono text-xs" rows="4" @input="values[name] = ($event.target as HTMLTextAreaElement).value" />
          <input v-else :id="`${uid}-${name}`" v-model="values[name]" :type="field.format === 'password' ? 'password' : 'text'" autocomplete="off" class="input" />
          <p v-if="field.description" class="input-hint">{{ localized(field.description) }}</p>
        </div>
      </div>
      <NewAPIVideoPricingField ref="pricingField" :value="credentials?.newapi_video_pricing" />
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { listNewAPIPlugins, type LocalizedText, type NewAPIPluginVersion } from '@/api/admin/newapiPlugins'
import NewAPIVideoPricingField from './NewAPIVideoPricingField.vue'
const props = defineProps<{ credentials?: Record<string, unknown> }>()
const { t, locale } = useI18n()
const uid = useId()
const plugins = ref<NewAPIPluginVersion[]>([])
const revision = ref(0)
const upstreamKind = ref('vendor')
const values = ref<Record<string, unknown>>({})
const saved = ref(false)
const replaceConfig = ref(false)
const error = ref('')
const pricingField = ref<InstanceType<typeof NewAPIVideoPricingField>>()
const controller = new AbortController()
const selected = computed(() => plugins.value.find(p => p.id === revision.value))
const fields = computed(() => selected.value?.meta.channelConfig?.properties ?? {})
const localized = (text?: LocalizedText) => typeof text === 'string' ? text : text?.[locale.value] ?? text?.zh ?? text?.en ?? ''
watch(() => props.credentials, credentials => { revision.value = Number(credentials?.newapi_plugin_version_id) || 0; upstreamKind.value = credentials?.newapi_upstream_kind === 'new_api' ? 'new_api' : 'vendor'; saved.value = revision.value > 0; replaceConfig.value = false }, { immediate: true })
watch(revision, (value, previous) => { if (value !== previous) { values.value = {}; replaceConfig.value = value !== Number(props.credentials?.newapi_plugin_version_id) } })
function apply(credentials: Record<string, unknown>) {
  const next = { ...credentials }
  delete next.newapi_plugin_config
  if (!revision.value) { next.newapi_plugin_version_id = null; delete next.newapi_video_pricing; return next }
  next.newapi_plugin_version_id = revision.value
  next.newapi_upstream_kind = upstreamKind.value
  next.newapi_video_pricing = pricingField.value?.rules() ?? {}
  if (!saved.value || replaceConfig.value) {
    const config: Record<string, unknown> = {}
    for (const [name, field] of Object.entries(fields.value)) {
      let value = values.value[name]
      if (value === undefined || value === '') continue
      if (field.type === 'object' || field.type === 'array') {
        try { value = JSON.parse(String(value)) } catch { throw new Error(t('admin.plugins.js.invalidJSON', { name })) }
      }
      config[name] = value
    }
    next.newapi_plugin_config = config
  }
  return next
}
defineExpose({ apply })
onMounted(async () => { try { plugins.value = (await listNewAPIPlugins(controller.signal)).plugins } catch { if (!controller.signal.aborted) error.value = t('admin.plugins.js.loadFailed') } })
onBeforeUnmount(() => controller.abort())
</script>
