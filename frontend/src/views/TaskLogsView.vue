<template>
  <AppLayout>
    <TaskLogConsole
      :client="client" :admin="admin" :t="translate" :locale="locale"
      :dialog-component="BaseDialog" :pagination-component="Pagination"
      :editable="admin" :run-mutation="runMutation" :cancel-mutation="stepUp.onCancel"
      :is-cancelled="isStepUpCancelled"
    >
      <template v-if="admin" #review><GenerationReviewPanel /></template>
    </TaskLogConsole>
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { TaskLogConsole, type TaskLogClient, type PluginTranslator, type RunPluginMutation } from '../../packages/newapi-ui/src'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import GenerationReviewPanel from '@/components/admin/GenerationReviewPanel.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { downloadTaskContent, getTaskLog, listTaskLogs, maintainNativeTask } from '@/api/taskLogs'

const { t, locale } = useI18n()
const route = useRoute()
const admin = computed(() => route.meta.requiresAdmin === true)
const translate: PluginTranslator = (key, values) => values ? t(key, values) : t(key)
const stepUp = useStepUp()
const runMutation: RunPluginMutation = action => stepUp.run(action)
// Replacing the scoped client on role changes aborts pending reads/actions in
// the console. Authorization remains in the host API and server middleware.
const client = computed<TaskLogClient>(() => {
  const privileged = admin.value
  return {
    listTaskLogs: (filter, signal) => listTaskLogs(privileged, filter, signal),
    getTaskLog: (id, signal) => getTaskLog(privileged, id, signal),
    downloadTaskContent: (id, signal) => downloadTaskContent(privileged, id, signal),
    ...(privileged ? { maintainNativeTask } : {})
  }
})
</script>
