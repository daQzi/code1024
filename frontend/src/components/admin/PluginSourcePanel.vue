<template>
  <SourcePanel :select-component="Select" v-bind="props" :client="client" :t="translate" />
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { PluginSourcePanel as SourcePanel, type NewAPIPluginVersion, type PluginSource, type PluginSourceClient, type PluginTranslator } from '../../../packages/newapi-ui/src'
import { getNewAPIPluginSource, getPluginFactorySource } from '@/api/admin/newapiPlugins'
const props = defineProps<{ plugin: Pick<PluginSource, 'key' | 'version' | 'sha256'> & { id?: number }; versions: NewAPIPluginVersion[]; source?: string; comparisonOnly?: boolean }>()
const { t } = useI18n()
const translate: PluginTranslator = (key, values) => values ? t(key, values) : t(key)
const client: PluginSourceClient = {
  getNewAPIPluginSource: (id, signal) => getNewAPIPluginSource(id, signal),
  getPluginFactorySource: (key, signal) => getPluginFactorySource(key, signal)
}
</script>
