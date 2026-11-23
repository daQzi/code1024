<template>
  <MarketplacePanel :select-component="Select" v-bind="props" editable :marketplace="marketplace" :source-client="sourceClient" @installed="$emit('installed')" :client="client" :t="translate" :locale="locale" :dialog-component="BaseDialog" :pagination-component="Pagination" :run-mutation="runMutation" :is-cancelled="isStepUpCancelled" />
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { PluginMarketplacePanel as MarketplacePanel, type PluginSourceClient, type PluginTranslator, type RunPluginMutation } from '../../../packages/newapi-ui/src'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp, isStepUpCancelled } from '@/composables/useStepUp'
import { installNewAPIPlugin, listPluginMarketplaceSources, savePluginMarketplaceSources, type NewAPIPluginVersion } from '@/api/admin/newapiPlugins'
import { fetchMarketplaceDocument, fetchMarketplaceIndex, fetchMarketplacePinIndex, fetchMarketplaceSource, findMarketplacePluginByCode } from '@/utils/pluginMarketplace'
import { getNewAPIPluginSource, getPluginFactorySource } from '@/api/admin/newapiPlugins'
const sourceClient: PluginSourceClient = {
  getNewAPIPluginSource: (id, signal) => getNewAPIPluginSource(id, signal),
  getPluginFactorySource: (key, signal) => getPluginFactorySource(key, signal)
}
const marketplace = { fetchMarketplaceDocument, fetchMarketplaceIndex, fetchMarketplacePinIndex, fetchMarketplaceSource, findMarketplacePluginByCode }
const props = defineProps<{ installedPlugins: NewAPIPluginVersion[] }>()
defineEmits<{ installed: [] }>()
const { t, locale } = useI18n()
const translate: PluginTranslator = (key, values) => values ? t(key, values) : t(key)
const stepUp = useStepUp()
const runMutation: RunPluginMutation = action => stepUp.run(action)
const client = { installNewAPIPlugin, listPluginMarketplaceSources, savePluginMarketplaceSources }
</script>
