<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600" data-plugin-credential>
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div><h4 class="text-sm font-medium">{{ t('admin.plugins.credential.title') }}</h4><p class="mt-1 text-xs text-gray-500">{{ t('admin.plugins.credential.hint') }}</p></div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="load">{{ t('common.refresh') }}</button>
    </div>
    <p v-if="error" role="alert" class="text-xs text-red-600">{{ error }}</p>
    <template v-if="view">
      <p class="text-sm" role="status">{{ t(`admin.plugins.credential.statuses.${view.status}`) }}</p>
      <p v-if="view.credential" class="break-all text-xs text-gray-500">{{ view.credential.label || view.credential.account_id }}<span v-if="view.credential.expires_at"> · {{ date(view.credential.expires_at) }}</span></p>
      <p v-if="view.notify" class="whitespace-pre-wrap break-words text-xs text-amber-700">{{ localized(view.notify) }}</p>
      <p v-if="view.balance" class="text-xs text-gray-500" data-maintenance-balance>{{ t('admin.plugins.credential.upstreamBalance') }}: {{ view.balance.unlimited ? t('admin.plugins.js.balanceUnlimited') : view.balance.amount === null ? t('admin.plugins.js.balanceUnknown') : `${view.balance.amount} ${view.balance.currency}` }}</p>
      <p v-if="view.last_error" class="text-xs text-red-600">{{ t('admin.plugins.credential.maintenanceFailed') }}</p>
      <div class="flex flex-wrap gap-2">
        <button v-if="hooks.includes('login')" type="button" class="btn btn-secondary btn-sm" :disabled="busy" data-credential-login @click="act('login')">{{ t('admin.plugins.credential.login') }}</button>
        <button v-if="hooks.includes('refresh') && view.credential" type="button" class="btn btn-secondary btn-sm" :disabled="busy" data-credential-refresh @click="act('refresh')">{{ t('admin.plugins.credential.renew') }}</button>
        <button v-if="hooks.includes('scheduled')" type="button" class="btn btn-secondary btn-sm" :disabled="busy" data-credential-scheduled @click="act('scheduled')">{{ t('admin.plugins.credential.maintain') }}</button>
        <button v-if="view.credential" type="button" class="btn btn-secondary btn-sm text-red-600" :disabled="busy" data-credential-revoke @click="act('revoke')">{{ t('admin.plugins.credential.revoke') }}</button>
      </div>
      <form v-if="view.login" class="space-y-3 border-t border-gray-100 pt-3 dark:border-dark-700" @submit.prevent="act('continue')">
        <p v-if="view.login.hint" class="text-xs text-gray-500">{{ localized(view.login.hint) }}</p>
        <p v-if="view.login.user_code" class="font-mono text-lg">{{ view.login.user_code }}</p>
        <a v-if="loginURL" :href="loginURL" target="_blank" rel="noopener noreferrer" class="inline-block text-sm text-primary-600">{{ t('admin.plugins.credential.openAuthorization') }}</a>
        <label v-if="['paste', 'redirect'].includes(view.login.type)" class="block"><span class="input-label">{{ t('admin.plugins.credential.callbackURL') }}</span><input v-model="callbackURL" type="text" class="input font-mono text-xs" autocomplete="off" :disabled="busy" required /></label>
        <label v-for="field in view.login.fields ?? []" :key="field.name" class="block"><span class="input-label">{{ localized(field.label) }}</span><input v-model="fields[field.name]" :type="field.type === 'password' ? 'password' : 'text'" :required="field.required" :placeholder="localized(field.placeholder)" :disabled="busy" autocomplete="off" class="input" /><span v-if="field.description" class="input-hint">{{ localized(field.description) }}</span></label>
        <p class="text-xs text-gray-500">{{ t('admin.plugins.credential.expires') }} {{ date(view.login.expires_at) }}<span v-if="polling"> · {{ t('admin.plugins.credential.pollHint', { seconds: view.login.interval || 5 }) }}</span></p>
        <button type="submit" class="btn btn-primary btn-sm" :disabled="busy" data-credential-continue>{{ t(busy ? 'common.loading' : polling ? 'admin.plugins.credential.checkLogin' : 'common.submit') }}</button>
      </form>
    </template>
  </section>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getPluginCredential, managePluginCredential, type LocalizedText, type PluginCredentialView } from '@/api/admin/newapiPlugins'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

const props = defineProps<{ accountId: number; versionId: number; hooks: string[] }>()
const { t, locale } = useI18n()
const stepUp = useStepUp()
const view = ref<PluginCredentialView>()
const error = ref('')
const busy = ref(false)
const callbackURL = ref('')
const fields = ref<Record<string, string>>({})
let request: AbortController | undefined
let generation = 0
const localized = (value?: LocalizedText) => typeof value === 'string' ? value : value?.[locale.value] ?? value?.zh ?? value?.en ?? ''
const date = (seconds: number) => new Date(seconds * 1000).toLocaleString(locale.value)
const polling = computed(() => ['device', 'pending'].includes(view.value?.login?.type ?? ''))
const loginURL = computed(() => {
  const login = view.value?.login
  const value = login?.url || login?.verification_uri_complete || login?.verification_uri
  try { const url = new URL(value || ''); return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : undefined } catch { return undefined }
})
function reset() { generation++; request?.abort(); view.value = undefined; callbackURL.value = ''; fields.value = {}; error.value = ''; busy.value = false }
async function operation(action?: string) {
  if (busy.value) return
  request?.abort()
  const controller = request = new AbortController()
  const current = generation
  const account = props.accountId
  busy.value = true; error.value = ''
  const input = action === 'continue' ? { flow: view.value?.login?.flow, ...(polling.value ? { poll: true } : view.value?.login?.type === 'fields' ? { fields: { ...fields.value } } : { callback_url: callbackURL.value }) } : {}
  const revision = view.value?.revision ?? 0
  try {
    const result = action ? await stepUp.run(() => managePluginCredential(account, action, revision, input, controller.signal)) : await getPluginCredential(account, controller.signal)
    if (controller.signal.aborted || current !== generation) return
    if (result.account_id !== account || result.plugin_version_id !== props.versionId) throw new Error(t('admin.plugins.credential.changed'))
    view.value = result
  } catch (e) {
    if (!controller.signal.aborted && current === generation && !isStepUpCancelled(e)) error.value = t('admin.plugins.credential.operationFailed')
  } finally {
    if (!controller.signal.aborted && current === generation) { busy.value = false; callbackURL.value = ''; fields.value = {} }
  }
}
const load = () => operation()
const act = (action: string) => operation(action)
watch(() => [props.accountId, props.versionId], () => { reset(); void load() }, { immediate: true })
onBeforeUnmount(reset)
</script>
