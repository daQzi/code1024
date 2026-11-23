<template>
  <RegistryPanel :select-component="Select" v-bind="props" can-write @installed="$emit('installed')" :client="client" :t="translate" :dialog-component="BaseDialog" :pagination-component="Pagination" :run-mutation="runMutation" :is-cancelled="isStepUpCancelled" />
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { PluginRegistryPanel as RegistryPanel, type PluginTranslator, type RunPluginMutation } from '../../../packages/newapi-ui/src'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp, isStepUpCancelled } from '@/composables/useStepUp'
import { dryRunPlugin, getPluginSubmission, installPluginSubmission, listPluginSubmissions, listPluginTemplates, savePluginSubmission, transitionPluginSubmission, type RegistryMode } from '@/api/pluginRegistry'
const props = withDefaults(defineProps<{ mode: RegistryMode; canInstall?: boolean }>(), { canInstall: false })
defineEmits<{ installed: [] }>()
const { t } = useI18n()
const translate: PluginTranslator = (key, values) => values ? t(key, values) : t(key)
const stepUp = useStepUp()
const runMutation: RunPluginMutation = action => stepUp.run(action)
const client = { dryRunPlugin, getPluginSubmission, installPluginSubmission, listPluginSubmissions, listPluginTemplates, savePluginSubmission, transitionPluginSubmission }
</script>
