<template>
  <CleanupPanel :client="client" :t="translate" :locale="locale" editable :run-mutation="runMutation" :is-cancelled="isStepUpCancelled" />
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { PluginCredentialCleanupPanel as CleanupPanel, type PluginTranslator, type RunPluginMutation } from '../../../packages/newapi-ui/src'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp, isStepUpCancelled } from '@/composables/useStepUp'
import { listPluginCredentialRevocations, retryPluginCredentialRevocation } from '@/api/admin/newapiPlugins'
const { t, locale } = useI18n()
const translate: PluginTranslator = (key, values) => values ? t(key, values) : t(key)
const stepUp = useStepUp()
const runMutation: RunPluginMutation = action => stepUp.run(action)
const client = { listPluginCredentialRevocations, retryPluginCredentialRevocation }
</script>
