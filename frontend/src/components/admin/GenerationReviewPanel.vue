<template>
  <section class="card space-y-4 p-5">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.plugins.js.review.title') }}</h3>
        <p class="mt-1 max-w-3xl text-sm text-gray-500">{{ t('admin.plugins.js.review.hint') }}</p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="load">{{ t('common.refresh') }}</button>
    </header>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <p v-if="message" role="status" class="text-sm text-emerald-600 dark:text-emerald-400">{{ message }}</p>
    <p v-if="loading" class="py-4 text-sm text-gray-500">{{ t('common.loading') }}</p>
    <p v-else-if="!tasks.length" class="py-4 text-sm text-gray-500">{{ t('admin.plugins.js.review.empty') }}</p>
    <div v-else class="divide-y divide-gray-200 dark:divide-dark-700">
      <article v-for="task in tasks" :key="task.id" class="space-y-3 py-4">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0 space-y-1">
            <strong class="block break-words text-sm text-gray-900 dark:text-white">{{ task.model }}</strong>
            <code class="block break-all text-xs text-gray-500">{{ task.id }}</code>
            <p class="text-xs text-gray-500">{{ t('admin.plugins.js.review.identities', { user: task.user_id, account: task.account_id }) }}</p>
          </div>
          <div class="space-y-1 text-right text-xs text-gray-500">
            <p class="font-medium text-gray-900 dark:text-white">{{ t('admin.plugins.js.review.held', { amount: task.hold_amount }) }}</p>
            <p>{{ task.status }} / {{ task.billing_status }}</p>
            <p>{{ task.error_code }}</p>
          </div>
        </div>
        <button v-if="selected?.id !== task.id" type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="select(task)">{{ t('admin.plugins.js.review.open') }}</button>
        <form v-else class="space-y-3 rounded-lg bg-gray-50 p-4 dark:bg-dark-900" @submit.prevent="submit">
          <label class="block"><span class="input-label">{{ t('admin.plugins.js.review.action') }}</span>
            <select v-model="action" class="input" :disabled="busy">
              <option v-if="!terminal(task)" value="resume_poll">{{ t('admin.plugins.js.review.resume') }}</option>
              <option value="refund">{{ t('admin.plugins.js.review.refund') }}</option>
              <option v-if="terminal(task)" value="settle">{{ t('admin.plugins.js.review.settle') }}</option>
            </select>
          </label>
          <label v-if="action === 'resume_poll'" class="block"><span class="input-label">{{ t('admin.plugins.js.review.upstreamId') }}</span><input v-model.trim="upstreamId" class="input" required maxlength="512" :disabled="busy" /></label>
          <label v-if="action === 'settle'" class="block"><span class="input-label">{{ t('admin.plugins.js.review.amount') }}</span><input v-model="amount" class="input" type="number" min="0" :max="task.hold_amount" step="0.00000001" required :disabled="busy" /></label>
          <label class="block"><span class="input-label">{{ t('admin.plugins.js.review.reason') }}</span><textarea v-model.trim="reason" class="input" rows="3" required minlength="4" maxlength="2000" :disabled="busy" /></label>
          <p class="text-xs leading-5 text-gray-500">{{ t('admin.plugins.js.review.safety') }}</p>
          <div class="flex gap-2"><button type="submit" class="btn btn-primary btn-sm" :disabled="busy || !valid">{{ t('admin.plugins.js.review.confirm') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="selected = undefined">{{ t('common.cancel') }}</button></div>
        </form>
      </article>
    </div>
  </section>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { listGenerationReviews, reconcileGeneration, type GenerationReviewAction, type GenerationReviewTask } from '@/api/admin/newapiPlugins'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'

const { t } = useI18n()
const stepUp = useStepUp()
const tasks = ref<GenerationReviewTask[]>([])
const selected = ref<GenerationReviewTask>()
const action = ref<GenerationReviewAction>('resume_poll')
const upstreamId = ref('')
const amount = ref('')
const reason = ref('')
const busy = ref(false)
const loading = ref(true)
const error = ref('')
const message = ref('')
const controller = new AbortController()
const terminal = (task: GenerationReviewTask) => task.status === 'succeeded' || task.status === 'failed'
const valid = computed(() => reason.value.length >= 4 && (action.value !== 'resume_poll' || !!upstreamId.value) && (action.value !== 'settle' || (!!amount.value && Number(amount.value) >= 0 && Number(amount.value) <= Number(selected.value?.hold_amount))))
const errorText = (e: unknown) => (e as { response?: { data?: { message?: string } }; message?: string }).response?.data?.message ?? (e as Error).message
function select(task: GenerationReviewTask) { selected.value = task; action.value = terminal(task) ? 'settle' : 'resume_poll'; upstreamId.value = task.upstream_id; amount.value = task.hold_amount; reason.value = ''; message.value = '' }
async function load() {
  loading.value = true
  try { tasks.value = await listGenerationReviews(controller.signal); error.value = '' } catch (e) { if (!controller.signal.aborted) error.value = errorText(e) } finally { loading.value = false }
}
async function submit() {
  if (!selected.value || !valid.value || busy.value) return
  busy.value = true; error.value = ''; message.value = ''
  try {
    const taskId = selected.value.id
    const resolution = { action: action.value, reason: reason.value, ...(action.value === 'resume_poll' ? { upstream_id: upstreamId.value } : {}), ...(action.value === 'settle' ? { amount: String(amount.value) } : {}) }
    await stepUp.run(() => reconcileGeneration(taskId, resolution))
    selected.value = undefined; message.value = t('admin.plugins.js.review.saved'); await load()
  } catch (e) { if (!isStepUpCancelled(e)) error.value = errorText(e) } finally { busy.value = false }
}
onMounted(load)
onBeforeUnmount(() => controller.abort())
</script>
