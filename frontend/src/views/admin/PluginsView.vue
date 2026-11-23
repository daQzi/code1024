<template>
  <AppLayout>
    <PluginCenter :client="client" :t="translate" :locale="locale" :dialog-component="BaseDialog" editable :show-header="false" :run-mutation="runMutation" :is-cancelled="isStepUpCancelled">
      <template #header-actions>
        <RouterLink to="/admin/request-processing" class="btn btn-secondary btn-sm">{{ t('requestPlugins.title') }}</RouterLink>
        <RouterLink to="/admin/native-plugins" class="btn btn-secondary btn-sm">{{ t('admin.plugins.center.nativeManager') }}</RouterLink>
      </template>
      <template #cleanup><PluginCredentialCleanupPanel /></template>
      <template #registry="{ mode }"><PluginRegistryPanel :key="mode" :mode="mode" /></template>
      <template #community-market="{ reload }"><PluginRegistryPanel mode="market" can-install @installed="reload" /></template>
      <template #external-market="{ plugins, reload }"><PluginMarketplacePanel :installed-plugins="plugins" @installed="reload" /></template>
      <template #detail-actions="{ plugin }">
        <RouterLink v-if="plugin.capabilities.transform" to="/admin/request-processing" class="btn btn-secondary btn-sm">{{ t('requestPlugins.title') }}</RouterLink>
        <RouterLink to="/admin/accounts" class="btn btn-secondary btn-sm">{{ t('admin.plugins.center.accounts') }}</RouterLink>
      </template>
      <template #source="{ plugin, versions }"><PluginSourcePanel :key="plugin.key + plugin.sha256" :plugin="plugin" :versions="versions" /></template>
    </PluginCenter>
    <div class="mt-5"><ProviderToolPricePanel :client="toolPriceClient" :t="translate" editable :run-mutation="runMutation" :cancel-mutation="stepUp.onCancel" :is-cancelled="isStepUpCancelled" /></div>
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>

<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { PluginCenter, ProviderToolPricePanel, type PluginCenterClient, type ProviderToolPriceClient, type RunPluginMutation } from '../../../packages/newapi-ui/src'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PluginCredentialCleanupPanel from '@/components/admin/PluginCredentialCleanupPanel.vue'
import PluginSourcePanel from '@/components/admin/PluginSourcePanel.vue'
import PluginRegistryPanel from '@/components/plugins/PluginRegistryPanel.vue'
import PluginMarketplacePanel from '@/components/plugins/PluginMarketplacePanel.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp, isStepUpCancelled } from '@/composables/useStepUp'
import { activateNewAPIPlugin, installNewAPIPlugin, listNewAPIPlugins, listNewAPIPluginVersions, restorePluginFactory, getProviderToolPrices, saveProviderToolPrices } from '@/api/admin/newapiPlugins'

const { t, locale } = useI18n()
const translate = (key: string) => t(key)
const stepUp = useStepUp()
const runMutation: RunPluginMutation = action => stepUp.run(action)
const client: PluginCenterClient = { list: listNewAPIPlugins, versions: listNewAPIPluginVersions, install: installNewAPIPlugin, activate: activateNewAPIPlugin, restore: restorePluginFactory }
const toolPriceClient: ProviderToolPriceClient = { read: getProviderToolPrices, save: saveProviderToolPrices }
</script>
