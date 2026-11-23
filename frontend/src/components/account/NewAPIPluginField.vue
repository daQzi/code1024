<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
    <label class="input-label" :for="`${uid}-plugin`">{{ t('admin.plugins.js.accountTitle') }}</label>
    <Select :id="`${uid}-plugin`" v-model="revision" :options="pluginOptions" :aria-label="t('admin.plugins.js.accountTitle')" />
    <p class="input-hint">{{ t('admin.plugins.js.accountHint') }}</p>
    <p v-if="selected?.meta.description" class="input-hint">{{ localized(selected.meta.description) }}</p>
    <p v-if="requiresExplicitVideoModels" class="input-hint">{{ t('admin.plugins.js.genericVideoHint') }}</p>
    <p v-if="error" role="alert" class="text-xs text-red-600">{{ error }}</p>
    <template v-if="revision">
      <div>
        <span class="input-label">{{ t('admin.plugins.js.upstreamKind') }}</span>
        <Select v-model="upstreamKind" :options="upstreamOptions" :aria-label="t('admin.plugins.js.upstreamKind')" />
      </div>
      <p v-if="saved && !replaceConfig" class="text-xs text-gray-500">{{ t('admin.plugins.js.configSaved') }}</p>
      <button v-if="saved && !replaceConfig" type="button" class="btn btn-secondary btn-sm" @click="replaceConfig = true">{{ t('admin.plugins.js.replaceConfig') }}</button>
      <div v-else class="space-y-3">
        <div v-for="(field, name) in fields" :key="name">
          <label class="input-label" :for="`${uid}-${name}`">{{ localized(field.title) || name }}</label>
          <Select v-if="field.enum" :id="`${uid}-${name}`" :model-value="enumValue(values[name])"
            :options="field.enum.map((value, index) => ({ value, label: localized(field.enumTitles?.[index]) || String(value) }))"
            :aria-label="localized(field.title) || name" @update:model-value="values[name] = $event" />
          <input v-else-if="field.type === 'boolean'" :id="`${uid}-${name}`" v-model="values[name]" type="checkbox" class="rounded" />
          <input v-else-if="field.type === 'number' || field.type === 'integer'" :id="`${uid}-${name}`" v-model.number="values[name]" type="number" class="input" :min="field.minimum" :max="field.maximum" :step="field.type === 'integer' ? 1 : 'any'" />
          <textarea v-else-if="field.type === 'object' || field.type === 'array' || ['textarea', 'keywords'].includes(field.format ?? '')" :id="`${uid}-${name}`" :value="String(values[name] ?? '')" class="input font-mono text-xs" rows="4" @input="values[name] = ($event.target as HTMLTextAreaElement).value" />
          <input v-else :id="`${uid}-${name}`" v-model="values[name]" :type="field.format === 'password' ? 'password' : 'text'" autocomplete="off" class="input" />
          <p v-if="field.description" class="input-hint">{{ localized(field.description) }}</p>
        </div>
      </div>
      <p v-if="selected?.compatibility?.kinds.includes('provider')" class="input-hint">{{ t('admin.plugins.js.providerHint') }}</p>
      <p v-if="requiresLogin && !accountId" class="input-hint">{{ t('admin.plugins.credential.saveFirst') }}</p>
      <label v-if="selected?.capabilities.hooks?.includes('scheduled')" class="flex items-start gap-2 text-sm"><input v-model="autoDisable" type="checkbox" class="mt-1 rounded" /><span>{{ t('admin.plugins.credential.autoDisable') }}<span class="input-hint block">{{ t('admin.plugins.credential.autoDisableHint') }}</span></span></label>
      <NewAPIPluginCredential v-if="accountId && revision === Number(credentials?.newapi_plugin_version_id) && selected?.compatibility?.kinds.some(kind => kind === 'provider' || kind === 'task') && selected.capabilities.hooks?.some(hook => ['login', 'refresh', 'revoke', 'scheduled'].includes(hook))" :account-id="accountId" :version-id="revision" :hooks="selected.capabilities.hooks" />
      <NewAPIPluginBalance v-if="accountId && revision === Number(credentials?.newapi_plugin_version_id) && selected?.capabilities.hooks?.includes('buildBalanceRequest') && selected.capabilities.hooks.includes('parseBalance')" :account-id="accountId" :version-id="revision" />
      <NewAPIPluginModels v-if="accountId && revision === Number(credentials?.newapi_plugin_version_id) && selected?.meta.relay?.some(route => route.incoming_path === '/v1/models')" :account-id="accountId" :version-id="revision" />
      <NewAPIVideoPricingField v-if="selected?.compatibility?.kinds.includes('task')" ref="pricingField" :value="credentials?.newapi_video_pricing" />
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { getNewAPIPluginVersion, listNewAPIPlugins, type LocalizedText, type NewAPIPluginVersion } from '@/api/admin/newapiPlugins'
import NewAPIVideoPricingField from './NewAPIVideoPricingField.vue'
import NewAPIPluginBalance from './NewAPIPluginBalance.vue'
import NewAPIPluginModels from './NewAPIPluginModels.vue'
import NewAPIPluginCredential from './NewAPIPluginCredential.vue'
const props = defineProps<{ credentials?: Record<string, unknown>; accountId?: number }>()
const { t, locale } = useI18n()
const uid = useId()
const plugins = ref<NewAPIPluginVersion[]>([])
const revision = ref(0)
const upstreamKind = ref('vendor')
const autoDisable = ref(false)
const values = ref<Record<string, unknown>>({})
const saved = ref(false)
const replaceConfig = ref(false)
const error = ref('')
const pricingField = ref<InstanceType<typeof NewAPIVideoPricingField>>()
const controller = new AbortController()
const catalogReady = ref(false)
let pinnedController: AbortController | undefined
const selected = computed(() => plugins.value.find(p => p.id === revision.value))
const pluginOptions = computed(() => [
  { value: 0, label: t('admin.plugins.js.native') },
  ...(revision.value && !selected.value ? [{ value: revision.value, label: t('admin.plugins.js.pinned', { id: revision.value }) }] : []),
  ...plugins.value.filter(plugin => plugin.compatibility?.kinds.some(kind => kind === 'task' || kind === 'provider')).map(plugin => ({
    value: plugin.id,
    label: `${plugin.meta.name} · v${plugin.version}${plugin.enabled ? '' : ` (${t('admin.plugins.disabled')})`}`,
    disabled: !plugin.enabled
  }))
])
const upstreamOptions = computed(() => [
  { value: 'vendor', label: t('admin.plugins.js.vendorUpstream') },
  { value: 'new_api', label: t('admin.plugins.js.newAPIUpstream') }
])
const enumValue = (value: unknown) => value === null || typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean' ? value : undefined
const requiresExplicitVideoModels = computed(() => {
  if (!selected.value?.compatibility?.kinds.includes('task')) return false
  const meta = selected.value.meta
  const video = meta.protocols?.find(protocol => protocol.name === 'openai_video')
  return (video?.models?.length ? video.models : meta.models)?.includes('*') ?? false
})
const requiresLogin = computed(() => !!selected.value?.compatibility?.kinds.some(kind => kind === 'provider' || kind === 'task') && !!selected.value?.capabilities.hooks?.includes('login'))
const fields = computed(() => selected.value?.meta.channelConfig?.properties ?? {})
const localized = (text?: LocalizedText) => typeof text === 'string' ? text : text?.[locale.value] ?? text?.zh ?? text?.en ?? ''
watch(() => props.credentials, credentials => { revision.value = Number(credentials?.newapi_plugin_version_id) || 0; upstreamKind.value = credentials?.newapi_upstream_kind === 'new_api' ? 'new_api' : 'vendor'; autoDisable.value = credentials?.newapi_plugin_auto_disable === true; saved.value = revision.value > 0; replaceConfig.value = false }, { immediate: true })
watch(revision, (value, previous) => { if (value !== previous) { values.value = {}; replaceConfig.value = value !== Number(props.credentials?.newapi_plugin_version_id); autoDisable.value = !replaceConfig.value && props.credentials?.newapi_plugin_auto_disable === true } })
function apply(credentials: Record<string, unknown>) {
  const next = { ...credentials }
  delete next.newapi_plugin_config
  if (!revision.value) { next.newapi_plugin_version_id = null; delete next.newapi_video_pricing; delete next.newapi_plugin_auto_disable; return next }
  next.newapi_plugin_version_id = revision.value
  next.newapi_upstream_kind = upstreamKind.value
  next.newapi_plugin_auto_disable = autoDisable.value
  if (pricingField.value) next.newapi_video_pricing = pricingField.value.rules()
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
defineExpose({ apply, requiresLogin })
watch([catalogReady, () => props.credentials?.newapi_plugin_version_id], async ([ready, id]) => {
  pinnedController?.abort()
  if (!ready) return
  const pinnedID = Number(id)
  if (!Number.isSafeInteger(pinnedID) || pinnedID <= 0 || plugins.value.some(plugin => plugin.id === pinnedID)) return
  const request = new AbortController()
  pinnedController = request
  try {
    const pinned = await getNewAPIPluginVersion(pinnedID, request.signal)
    if (!request.signal.aborted && Number(props.credentials?.newapi_plugin_version_id) === pinnedID && pinned.id === pinnedID) plugins.value.push(pinned)
  } catch { if (!request.signal.aborted) error.value = t('admin.plugins.js.loadFailed') }
})
onMounted(async () => {
  try {
    const catalog = await listNewAPIPlugins(controller.signal)
    if (controller.signal.aborted) return
    plugins.value = catalog.plugins
    catalogReady.value = true
  } catch { if (!controller.signal.aborted) error.value = t('admin.plugins.js.loadFailed') }
})
onBeforeUnmount(() => { controller.abort(); pinnedController?.abort() })
</script>
