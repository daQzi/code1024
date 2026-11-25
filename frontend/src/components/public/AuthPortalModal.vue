<template>
  <Teleport to="body">
    <div v-if="open" class="rl-theme-shell dark auth-portal-layer">
      <div
        class="public-glass auth-portal-root"
        data-input="pointer"
        @click.self="handleBackdropClick"
      >
        <div
          ref="dialogRef"
          class="auth-portal-dialog"
          role="dialog"
          aria-modal="true"
          :aria-labelledby="headingId"
          tabindex="-1"
          data-testid="auth-portal-modal"
        >
          <span class="auth-portal-crown" aria-hidden="true" />

          <header class="auth-portal-header">
            <div class="auth-portal-brand">
              <PublicBrandLogo :src="siteLogo || '/logo.svg'" />
              <span>{{ displaySiteName }}</span>
            </div>
            <button
              type="button"
              class="auth-portal-close"
              data-testid="auth-modal-close"
              :aria-label="t('common.close')"
              @click="emitClose"
            >
              <Icon name="x" size="md" aria-hidden="true" />
            </button>
          </header>

          <div
            v-if="showRegisterTab"
            class="auth-portal-tabs"
            role="tablist"
            :aria-label="t('glass.nav.navigation')"
          >
            <button
              type="button"
              role="tab"
              class="auth-portal-tab"
              :class="{ 'is-active': activeMode === 'login' }"
              :aria-selected="activeMode === 'login'"
              data-testid="auth-tab-login"
              @click="switchMode('login')"
            >
              {{ t('auth.signIn') }}
            </button>
            <button
              type="button"
              role="tab"
              class="auth-portal-tab"
              :class="{ 'is-active': activeMode === 'register' }"
              :aria-selected="activeMode === 'register'"
              data-testid="auth-tab-register"
              @click="switchMode('register')"
            >
              {{ t('auth.signUp') }}
            </button>
          </div>

          <div class="auth-portal-body auth-content">
            <div class="auth-flow auth-portal-flow">
              <div class="auth-portal-intro">
                <h2 :id="headingId" class="auth-portal-title">
                  {{ activeMode === 'login' ? t('auth.v2.loginTitle') : t('auth.createAccount') }}
                </h2>
                <p class="auth-portal-subtitle">
                  {{
                    activeMode === 'login'
                      ? t('auth.signInToAccount')
                      : t('auth.signUpToStart', { siteName: displaySiteName })
                  }}
                </p>
              </div>

              <!-- LOGIN TAB -->
              <template v-if="activeMode === 'login'">
                <form
                  ref="loginFormRef"
                  class="auth-form"
                  novalidate
                  :aria-busy="isLoading"
                  @submit.prevent="handleLogin"
                >
                  <p v-if="errorMessage" class="auth-notice auth-error" role="alert">
                    {{ errorMessage }}
                  </p>

                  <div class="auth-field">
                    <label for="auth-modal-email" class="input-label">{{ t('auth.emailLabel') }}</label>
                    <input
                      id="auth-modal-email"
                      v-model="loginForm.email"
                      type="email"
                      required
                      autocomplete="email"
                      inputmode="email"
                      autocapitalize="none"
                      :spellcheck="false"
                      :disabled="loginActionDisabled"
                      class="input"
                      :class="{ 'input-error': loginErrors.email }"
                      :aria-invalid="!!loginErrors.email"
                      :aria-describedby="loginErrors.email ? 'auth-modal-email-error' : undefined"
                      :placeholder="t('auth.emailPlaceholder')"
                    />
                    <p v-if="loginErrors.email" id="auth-modal-email-error" class="auth-error">
                      {{ loginErrors.email }}
                    </p>
                  </div>

                  <div class="auth-field">
                    <div class="auth-label-row">
                      <label for="auth-modal-password" class="input-label">{{ t('auth.passwordLabel') }}</label>
                      <button
                        v-if="passwordResetEnabled && !backendModeEnabled"
                        type="button"
                        class="auth-link"
                        @click="goToRoute('/forgot-password')"
                      >
                        {{ t('auth.forgotPassword') }}
                      </button>
                    </div>
                    <div class="auth-control">
                      <input
                        id="auth-modal-password"
                        v-model="loginForm.password"
                        :type="showLoginPassword ? 'text' : 'password'"
                        required
                        autocomplete="current-password"
                        :disabled="loginActionDisabled"
                        class="input auth-input-action"
                        :class="{ 'input-error': loginErrors.password }"
                        :aria-invalid="!!loginErrors.password"
                        :aria-describedby="loginErrors.password ? 'auth-modal-password-error' : undefined"
                        :placeholder="t('auth.passwordPlaceholder')"
                      />
                      <button
                        type="button"
                        class="auth-reveal"
                        :disabled="loginActionDisabled"
                        :aria-label="t(showLoginPassword ? 'glass.auth.hidePassword' : 'glass.auth.showPassword')"
                        :aria-pressed="showLoginPassword"
                        aria-controls="auth-modal-password"
                        @click="showLoginPassword = !showLoginPassword"
                      >
                        <Icon :name="showLoginPassword ? 'eyeOff' : 'eye'" size="md" aria-hidden="true" />
                      </button>
                    </div>
                    <p v-if="loginErrors.password" id="auth-modal-password-error" class="auth-error">
                      {{ loginErrors.password }}
                    </p>
                  </div>

                  <div v-if="captchaEnabled" class="auth-captcha">
                    <TurnstileWidget
                      ref="loginTurnstileRef"
                      :turnstile-enabled="turnstileEnabled"
                      :turnstile-site-key="turnstileSiteKey"
                      :tencent-enabled="tencentCaptchaEnabled"
                      :tencent-app-id="tencentCaptchaAppId"
                      :tencent-region="tencentCaptchaRegion"
                      :aliyun-enabled="aliyunCaptchaEnabled"
                      :aliyun-scene-id="aliyunCaptchaSceneId"
                      :aliyun-prefix="aliyunCaptchaPrefix"
                      :aliyun-region="aliyunCaptchaRegion"
                      @verify="onTurnstileVerify"
                      @expire="onTurnstileExpire"
                      @error="onTurnstileError"
                    />
                  </div>
                  <p v-if="loginErrors.turnstile" class="auth-error" role="alert">
                    {{ loginErrors.turnstile }}
                  </p>

                  <LoginAgreementPrompt
                    v-if="loginAgreementEnabled"
                    :accepted="agreementAccepted"
                    :documents="loginAgreementDocuments"
                    :mode="loginAgreementMode"
                    :updated-at="loginAgreementUpdatedAt"
                    :visible="showAgreementModal"
                    @accept="acceptLoginAgreement"
                    @reject="rejectLoginAgreement('login')"
                    @open="showAgreementModal = true"
                  />

                  <button
                    type="submit"
                    :disabled="loginActionDisabled || (turnstileEnabled && !turnstileToken)"
                    :aria-busy="isLoading"
                    class="primary-btn btn btn-primary auth-portal-submit"
                    data-testid="auth-login-submit"
                  >
                    <span v-if="isLoading" class="auth-spinner" aria-hidden="true" />
                    {{ isLoading ? t('auth.signingIn') : t('auth.signIn') }}
                  </button>
                </form>

                <div v-if="showPasskeyLogin || showLoginOAuth" class="auth-alternatives">
                  <div class="auth-divider">
                    <span>{{ t('auth.oauthOrContinue') }}</span>
                  </div>
                  <button
                    v-if="showPasskeyLogin"
                    type="button"
                    class="secondary-btn auth-secondary login__passkey"
                    :disabled="loginActionDisabled"
                    :aria-busy="passkeyLoading"
                    @click="handlePasskeyLogin"
                  >
                    <span v-if="passkeyLoading" class="auth-spinner" aria-hidden="true" />
                    <Icon v-else name="key" size="md" aria-hidden="true" />
                    {{ passkeyLoading ? t('auth.passkeySigningIn') : t('auth.passkeySignIn') }}
                  </button>
                  <div class="auth-providers">
                    <EmailOAuthButtons
                      :disabled="loginActionDisabled"
                      :github-enabled="githubOAuthEnabled"
                      :google-enabled="googleOAuthEnabled"
                      :show-divider="false"
                      @start="handleLoginOAuthStart"
                    />
                    <LinuxDoOAuthSection
                      v-if="linuxdoOAuthEnabled"
                      :disabled="loginActionDisabled"
                      :show-divider="false"
                      @start="handleLoginOAuthStart"
                    />
                    <DingTalkOAuthSection
                      v-if="dingtalkOAuthEnabled"
                      :disabled="loginActionDisabled"
                      :show-divider="false"
                      @start="handleLoginOAuthStart"
                    />
                    <WechatOAuthSection
                      v-if="wechatOAuthEnabled"
                      :disabled="loginActionDisabled"
                      :show-divider="false"
                      @start="handleLoginOAuthStart"
                    />
                    <OidcOAuthSection
                      v-if="oidcOAuthEnabled"
                      :disabled="loginActionDisabled"
                      :provider-name="oidcOAuthProviderName"
                      :show-divider="false"
                      @start="handleLoginOAuthStart"
                    />
                  </div>
                </div>
              </template>

              <!-- REGISTER TAB -->
              <template v-else>
                <p
                  v-if="!registrationEnabled && settingsLoaded"
                  class="auth-notice auth-notice--warning"
                  role="status"
                >
                  {{ t('auth.registrationDisabled') }}
                </p>
                <form
                  v-else
                  ref="registerFormRef"
                  class="auth-form"
                  novalidate
                  :aria-busy="isLoading"
                  @submit.prevent="handleRegister"
                >
                  <p v-if="errorMessage" class="auth-notice auth-error" role="alert">
                    {{ errorMessage }}
                  </p>

                  <div class="auth-field">
                    <label for="auth-modal-reg-email" class="input-label">{{ t('auth.emailLabel') }}</label>
                    <input
                      id="auth-modal-reg-email"
                      v-model="registerForm.email"
                      type="email"
                      required
                      autocomplete="email"
                      inputmode="email"
                      autocapitalize="none"
                      :spellcheck="false"
                      :disabled="registrationActionDisabled"
                      class="input"
                      :class="{ 'input-error': registerErrors.email }"
                      :aria-invalid="!!registerErrors.email"
                      :aria-describedby="registerErrors.email ? 'auth-modal-reg-email-error' : undefined"
                      :placeholder="t('auth.emailPlaceholder')"
                    />
                    <p v-if="registerErrors.email" id="auth-modal-reg-email-error" class="auth-error">
                      {{ registerErrors.email }}
                    </p>
                  </div>

                  <div class="auth-field">
                    <label for="auth-modal-reg-password" class="input-label">{{ t('auth.passwordLabel') }}</label>
                    <div class="auth-control">
                      <input
                        id="auth-modal-reg-password"
                        v-model="registerForm.password"
                        :type="showRegPassword ? 'text' : 'password'"
                        required
                        autocomplete="new-password"
                        :disabled="registrationActionDisabled"
                        class="input auth-input-action"
                        :class="{ 'input-error': registerErrors.password }"
                        :aria-invalid="!!registerErrors.password"
                        :aria-describedby="registerErrors.password ? 'auth-modal-reg-password-error auth-modal-reg-hint' : 'auth-modal-reg-hint'"
                        :placeholder="t('auth.createPasswordPlaceholder')"
                      />
                      <button
                        type="button"
                        class="auth-reveal"
                        :disabled="registrationActionDisabled"
                        :aria-label="t(showRegPassword ? 'glass.auth.hidePassword' : 'glass.auth.showPassword')"
                        :aria-pressed="showRegPassword"
                        aria-controls="auth-modal-reg-password"
                        @click="showRegPassword = !showRegPassword"
                      >
                        <Icon :name="showRegPassword ? 'eyeOff' : 'eye'" size="md" aria-hidden="true" />
                      </button>
                    </div>
                    <p id="auth-modal-reg-hint" class="auth-hint">{{ t('auth.passwordHint') }}</p>
                    <p v-if="registerErrors.password" id="auth-modal-reg-password-error" class="auth-error">
                      {{ registerErrors.password }}
                    </p>
                  </div>

                  <div class="auth-field">
                    <label for="auth-modal-reg-confirm" class="input-label">{{ t('auth.confirmPassword') }}</label>
                    <div class="auth-control">
                      <input
                        id="auth-modal-reg-confirm"
                        v-model="confirmPassword"
                        :type="showRegConfirmPassword ? 'text' : 'password'"
                        required
                        autocomplete="new-password"
                        :disabled="registrationActionDisabled"
                        class="input auth-input-action"
                        :class="{ 'input-error': registerErrors.confirmPassword }"
                        :aria-invalid="!!registerErrors.confirmPassword"
                        :aria-describedby="registerErrors.confirmPassword ? 'auth-modal-reg-confirm-error' : undefined"
                        :placeholder="t('auth.confirmPasswordPlaceholder')"
                      />
                      <button
                        type="button"
                        class="auth-reveal"
                        :disabled="registrationActionDisabled"
                        :aria-label="t(showRegConfirmPassword ? 'glass.auth.hidePassword' : 'glass.auth.showPassword')"
                        :aria-pressed="showRegConfirmPassword"
                        aria-controls="auth-modal-reg-confirm"
                        @click="showRegConfirmPassword = !showRegConfirmPassword"
                      >
                        <Icon :name="showRegConfirmPassword ? 'eyeOff' : 'eye'" size="md" aria-hidden="true" />
                      </button>
                    </div>
                    <p
                      v-if="registerErrors.confirmPassword"
                      id="auth-modal-reg-confirm-error"
                      class="auth-error"
                    >
                      {{ registerErrors.confirmPassword }}
                    </p>
                  </div>

                  <div v-if="invitationCodeEnabled" class="auth-field">
                    <label for="auth-modal-invitation" class="input-label">
                      {{ t('auth.invitationCodeLabel') }}
                    </label>
                    <input
                      id="auth-modal-invitation"
                      v-model="registerForm.invitation_code"
                      type="text"
                      required
                      autocomplete="off"
                      autocapitalize="none"
                      :spellcheck="false"
                      :disabled="registrationActionDisabled"
                      class="input"
                      :class="{
                        'input-error': invitationValidation.invalid || registerErrors.invitation_code,
                      }"
                      :aria-invalid="!!(invitationValidation.invalid || registerErrors.invitation_code)"
                      aria-describedby="auth-modal-invitation-status"
                      :placeholder="t('auth.invitationCodePlaceholder')"
                      @input="handleInvitationCodeInput"
                    />
                    <div id="auth-modal-invitation-status" aria-live="polite">
                      <p v-if="invitationValidating" class="auth-hint">
                        {{ t('auth.invitationCodeValidating') }}
                      </p>
                      <p
                        v-else-if="registerErrors.invitation_code || invitationValidation.invalid"
                        class="auth-error"
                      >
                        {{ registerErrors.invitation_code || invitationValidation.message }}
                      </p>
                      <p v-else-if="invitationValidation.valid" class="auth-notice auth-notice--success">
                        {{ t('auth.invitationCodeValid') }}
                      </p>
                    </div>
                  </div>

                  <div
                    v-else-if="affiliateEnabled"
                    class="auth-field"
                    data-testid="affiliate-invitation-field"
                  >
                    <label for="auth-modal-aff" class="input-label">
                      {{ t('auth.invitationCodeLabel') }}
                      <span class="auth-hint">({{ t('common.optional') }})</span>
                    </label>
                    <input
                      id="auth-modal-aff"
                      v-model="registerForm.aff_code"
                      type="text"
                      autocomplete="off"
                      autocapitalize="none"
                      :spellcheck="false"
                      :disabled="registrationActionDisabled"
                      class="input"
                      :placeholder="t('auth.invitationCodePlaceholder')"
                    />
                  </div>

                  <div v-if="promoCodeEnabled" class="auth-field">
                    <label for="auth-modal-promo" class="input-label">
                      {{ t('auth.promoCodeLabel') }}
                      <span class="auth-hint">({{ t('common.optional') }})</span>
                    </label>
                    <input
                      id="auth-modal-promo"
                      v-model="registerForm.promo_code"
                      type="text"
                      autocomplete="off"
                      autocapitalize="none"
                      :spellcheck="false"
                      :disabled="registrationActionDisabled"
                      class="input"
                      :class="{ 'input-error': promoValidation.invalid }"
                      :aria-invalid="promoValidation.invalid"
                      aria-describedby="auth-modal-promo-status"
                      :placeholder="t('auth.promoCodePlaceholder')"
                      @input="handlePromoCodeInput"
                    />
                    <div id="auth-modal-promo-status" aria-live="polite">
                      <p v-if="promoValidating" class="auth-hint">
                        {{ t('auth.promoCodeValidating') }}
                      </p>
                      <p v-else-if="promoValidation.invalid" class="auth-error">
                        {{ promoValidation.message }}
                      </p>
                      <p v-else-if="promoValidation.valid" class="auth-notice auth-notice--success">
                        {{
                          t('auth.promoCodeValid', {
                            amount: promoValidation.bonusAmount?.toFixed(2),
                          })
                        }}
                      </p>
                    </div>
                  </div>

                  <div v-if="captchaEnabled" class="auth-captcha">
                    <TurnstileWidget
                      ref="registerTurnstileRef"
                      :turnstile-enabled="turnstileEnabled"
                      :turnstile-site-key="turnstileSiteKey"
                      :tencent-enabled="tencentCaptchaEnabled"
                      :tencent-app-id="tencentCaptchaAppId"
                      :tencent-region="tencentCaptchaRegion"
                      :aliyun-enabled="aliyunCaptchaEnabled"
                      :aliyun-scene-id="aliyunCaptchaSceneId"
                      :aliyun-prefix="aliyunCaptchaPrefix"
                      :aliyun-region="aliyunCaptchaRegion"
                      @verify="onTurnstileVerify"
                      @expire="onTurnstileExpire"
                      @error="onTurnstileError"
                    />
                  </div>
                  <p v-if="registerErrors.turnstile" class="auth-error" role="alert">
                    {{ registerErrors.turnstile }}
                  </p>

                  <LoginAgreementPrompt
                    v-if="loginAgreementEnabled"
                    :accepted="agreementAccepted"
                    :documents="loginAgreementDocuments"
                    :mode="loginAgreementMode"
                    :updated-at="loginAgreementUpdatedAt"
                    :visible="showAgreementModal"
                    @accept="acceptLoginAgreement"
                    @reject="rejectLoginAgreement('register')"
                    @open="showAgreementModal = true"
                  />

                  <button
                    type="submit"
                    :disabled="registrationActionDisabled || (turnstileEnabled && !turnstileToken)"
                    :aria-busy="isLoading"
                    class="primary-btn btn btn-primary auth-portal-submit"
                    data-testid="auth-register-submit"
                  >
                    <span v-if="isLoading" class="auth-spinner" aria-hidden="true" />
                    {{
                      isLoading
                        ? t('auth.processing')
                        : emailVerifyEnabled
                          ? t('auth.continue')
                          : t('auth.createAccount')
                    }}
                  </button>
                </form>

                <div v-if="showRegisterOAuth && registrationEnabled" class="auth-alternatives">
                  <div class="auth-divider">
                    <span>{{ t('auth.oauthOrContinue') }}</span>
                  </div>
                  <div class="auth-providers">
                    <EmailOAuthButtons
                      :disabled="registrationActionDisabled"
                      :aff-code="registerForm.aff_code"
                      :promo-code="registerForm.promo_code"
                      :github-enabled="githubOAuthEnabled"
                      :google-enabled="googleOAuthEnabled"
                      :show-divider="false"
                      @start="handleRegisterOAuthStart"
                    />
                    <LinuxDoOAuthSection
                      v-if="linuxdoOAuthEnabled"
                      :disabled="registrationActionDisabled"
                      :aff-code="registerForm.aff_code"
                      :promo-code="registerForm.promo_code"
                      :show-divider="false"
                      @start="handleRegisterOAuthStart"
                    />
                    <WechatOAuthSection
                      v-if="wechatOAuthEnabled"
                      :disabled="registrationActionDisabled"
                      :aff-code="registerForm.aff_code"
                      :show-divider="false"
                      @start="handleRegisterOAuthStart"
                    />
                    <OidcOAuthSection
                      v-if="oidcOAuthEnabled"
                      :disabled="registrationActionDisabled"
                      :provider-name="oidcOAuthProviderName"
                      :aff-code="registerForm.aff_code"
                      :show-divider="false"
                      @start="handleRegisterOAuthStart"
                    />
                  </div>
                </div>
              </template>
            </div>
          </div>

          <footer v-if="showRegisterTab" class="auth-portal-footer">
            <template v-if="activeMode === 'login'">
              <span>{{ t('auth.dontHaveAccount') }}</span>
              <button type="button" class="auth-link" @click="switchMode('register')">
                {{ t('auth.signUp') }}
              </button>
              <p v-if="invitationCodeEnabled" class="auth-footer-note">
                {{ t('accessGuide.login.invitationNote') }}
              </p>
            </template>
            <template v-else>
              <span>{{ t('auth.alreadyHaveAccount') }}</span>
              <button type="button" class="auth-link" @click="switchMode('login')">
                {{ t('auth.signIn') }}
              </button>
            </template>
          </footer>
        </div>
      </div>

      <TotpLoginModal
        v-if="show2FAModal"
        ref="totpModalRef"
        :temp-token="totpTempToken"
        :user-email-masked="totpUserEmailMasked"
        @verify="handle2FAVerify"
        @cancel="handle2FACancel"
      />
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import PublicBrandLogo from '@/components/common/PublicBrandLogo.vue'
import LinuxDoOAuthSection from '@/components/auth/LinuxDoOAuthSection.vue'
import DingTalkOAuthSection from '@/components/auth/DingTalkOAuthSection.vue'
import OidcOAuthSection from '@/components/auth/OidcOAuthSection.vue'
import WechatOAuthSection from '@/components/auth/WechatOAuthSection.vue'
import EmailOAuthButtons from '@/components/auth/EmailOAuthButtons.vue'
import LoginAgreementPrompt from '@/components/auth/LoginAgreementPrompt.vue'
import TotpLoginModal from '@/components/auth/TotpLoginModal.vue'
import Icon from '@/components/icons/Icon.vue'
import TurnstileWidget from '@/components/CaptchaChallenge.vue'
import { useAuthDialogFocus } from '@/components/auth/useAuthDialogFocus'
import { useAppStore, useAuthStore } from '@/stores'
import {
  buildOAuthLoginStartURL,
  getPublicSettings,
  isTotp2FARequired,
  isWeChatWebOAuthEnabled,
  startOAuthLogin,
  type OAuthLoginStart,
  validateInvitationCode,
  validatePromoCode,
} from '@/api/auth'
import type { ActionCaptchaRequestProof, LoginAgreementDocument, TotpLoginResponse } from '@/types'
import { buildAuthErrorMessage } from '@/utils/authError'
import { extractApiErrorCode, extractI18nErrorMessage } from '@/utils/apiError'
import {
  clearAffiliateReferralCode,
  clearAllAffiliateReferralCodes,
  loadAffiliateReferralCode,
  resolveAffiliateReferralCode,
} from '@/utils/oauthAffiliate'
import {
  formatRegistrationEmailSuffixWhitelistForMessage,
  isRegistrationEmailSuffixAllowed,
  normalizeRegistrationEmailSuffixWhitelist,
} from '@/utils/registrationEmailPolicy'
import '@/styles/public-glass.css'

export type AuthModalMode = 'login' | 'register'

const props = withDefaults(
  defineProps<{
    open: boolean
    mode: AuthModalMode
    redirectTo?: string
    siteName: string
    siteLogo?: string
  }>(),
  {
    redirectTo: '',
    siteLogo: '',
  },
)

const emit = defineEmits<{
  close: []
  'update:mode': [mode: AuthModalMode]
}>()

const { t, locale } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()

// Guard router/route access so tests mounting ReferenceLanding without Router plugin never crash.
let router: ReturnType<typeof useRouter> | undefined
let route: ReturnType<typeof useRoute> | undefined
try {
  router = useRouter()
  route = useRoute()
} catch {
  router = undefined
  route = undefined
}

const LOGIN_AGREEMENT_STORAGE_KEY = 'sub2api_login_agreement_consent'
const headingId = 'auth-portal-modal-title'

const dialogRef = ref<HTMLElement | null>(null)
const loginFormRef = ref<HTMLFormElement | null>(null)
const registerFormRef = ref<HTMLFormElement | null>(null)

function emitClose() {
  if (isLoading.value || passkeyLoading.value) return
  emit('close')
}

function handleBackdropClick() {
  emitClose()
}

useAuthDialogFocus(dialogRef, () => props.open, emitClose)

const isLoading = ref(false)
const passkeyLoading = ref(false)
const settingsLoaded = ref(false)
const errorMessage = ref('')
const showLoginPassword = ref(false)
const showRegPassword = ref(false)
const showRegConfirmPassword = ref(false)
const confirmPassword = ref('')

// Public settings state (defaults keep offline preview usable if settings request rejects)
const registrationEnabled = ref(true)
const emailVerifyEnabled = ref(false)
const promoCodeEnabled = ref(appStore.cachedPublicSettings?.promo_code_enabled === true)
const invitationCodeEnabled = ref(false)
const affiliateEnabled = ref(false)
const backendModeEnabled = ref(false)
const turnstileEnabled = ref(false)
const turnstileSiteKey = ref('')
const tencentCaptchaEnabled = ref(false)
const tencentCaptchaAppId = ref('')
const tencentCaptchaRegion = ref('cn')
const aliyunCaptchaEnabled = ref(false)
const aliyunCaptchaSceneId = ref('')
const aliyunCaptchaPrefix = ref('')
const aliyunCaptchaRegion = ref('cn')
const linuxdoOAuthEnabled = ref(false)
const dingtalkOAuthEnabled = ref(false)
const wechatOAuthEnabled = ref(false)
const oidcOAuthEnabled = ref(false)
const oidcOAuthProviderName = ref('OIDC')
const githubOAuthEnabled = ref(false)
const googleOAuthEnabled = ref(false)
const passwordResetEnabled = ref(false)
const passkeyEnabled = ref(false)
const registrationEmailSuffixWhitelist = ref<string[]>([])
const emailDomainQuotaEnabled = ref(false)
const remoteSiteName = ref('')
const loginAgreementEnabled = ref(false)
const loginAgreementMode = ref<'modal' | 'checkbox' | string>('modal')
const loginAgreementUpdatedAt = ref('')
const loginAgreementRevision = ref('')
const loginAgreementDocuments = ref<LoginAgreementDocument[]>([])
const agreementAccepted = ref(true)
const showAgreementModal = ref(false)

const displaySiteName = computed(() => remoteSiteName.value || props.siteName || 'Sub2API')
const showRegisterTab = computed(
  () => !backendModeEnabled.value && (settingsLoaded.value ? registrationEnabled.value : true),
)
const activeMode = computed<AuthModalMode>(() =>
  props.mode === 'register' && showRegisterTab.value ? 'register' : 'login',
)

const loginTurnstileRef = ref<InstanceType<typeof TurnstileWidget> | null>(null)
const registerTurnstileRef = ref<InstanceType<typeof TurnstileWidget> | null>(null)
const turnstileToken = ref('')
const tencentCaptchaRandstr = ref('')

const aliyunCaptchaReady = computed(
  () =>
    aliyunCaptchaEnabled.value &&
    Boolean(aliyunCaptchaSceneId.value) &&
    Boolean(aliyunCaptchaPrefix.value),
)
const actionCaptchaEnabled = computed(
  () =>
    (tencentCaptchaEnabled.value && Boolean(tencentCaptchaAppId.value)) || aliyunCaptchaReady.value,
)
const captchaEnabled = computed(
  () => (turnstileEnabled.value && Boolean(turnstileSiteKey.value)) || actionCaptchaEnabled.value,
)

const show2FAModal = ref(false)
const totpTempToken = ref('')
const totpUserEmailMasked = ref('')
const totpModalRef = ref<InstanceType<typeof TotpLoginModal> | null>(null)

const loginForm = reactive({
  email: '',
  password: '',
})
const loginErrors = reactive({
  email: '',
  password: '',
  turnstile: '',
})

const registerForm = reactive({
  email: '',
  password: '',
  promo_code: '',
  invitation_code: '',
  aff_code: '',
})
const registerErrors = reactive({
  email: '',
  password: '',
  confirmPassword: '',
  turnstile: '',
  invitation_code: '',
})

const promoValidating = ref(false)
const promoValidation = reactive({
  valid: false,
  invalid: false,
  bonusAmount: null as number | null,
  message: '',
})
let promoValidateTimeout: ReturnType<typeof setTimeout> | null = null

const invitationValidating = ref(false)
const invitationValidation = reactive({
  valid: false,
  invalid: false,
  message: '',
})
let invitationValidateTimeout: ReturnType<typeof setTimeout> | null = null

const agreementGateActive = computed(() => loginAgreementEnabled.value && !agreementAccepted.value)
const loginActionDisabled = computed(
  () => isLoading.value || passkeyLoading.value || !settingsLoaded.value || agreementGateActive.value,
)
const registrationActionDisabled = computed(
  () => isLoading.value || !settingsLoaded.value || agreementGateActive.value,
)

const showPasskeyLogin = computed(
  () => passkeyEnabled.value && typeof window.PublicKeyCredential !== 'undefined',
)
const showLoginOAuth = computed(
  () =>
    !backendModeEnabled.value &&
    (linuxdoOAuthEnabled.value ||
      dingtalkOAuthEnabled.value ||
      wechatOAuthEnabled.value ||
      oidcOAuthEnabled.value ||
      githubOAuthEnabled.value ||
      googleOAuthEnabled.value),
)
const showRegisterOAuth = computed(
  () =>
    linuxdoOAuthEnabled.value ||
    wechatOAuthEnabled.value ||
    oidcOAuthEnabled.value ||
    githubOAuthEnabled.value ||
    googleOAuthEnabled.value,
)

function activeTurnstile() {
  return activeMode.value === 'login' ? loginTurnstileRef.value : registerTurnstileRef.value
}

function clearFormFeedback() {
  errorMessage.value = ''
  loginErrors.email = ''
  loginErrors.password = ''
  loginErrors.turnstile = ''
  registerErrors.email = ''
  registerErrors.password = ''
  registerErrors.confirmPassword = ''
  registerErrors.turnstile = ''
  registerErrors.invitation_code = ''
}

function switchMode(nextMode: AuthModalMode) {
  if (nextMode === 'register' && !showRegisterTab.value) return
  if (nextMode === activeMode.value) return
  if (nextMode === 'register' && loginForm.email && !registerForm.email) {
    registerForm.email = loginForm.email
  } else if (nextMode === 'login' && registerForm.email && !loginForm.email) {
    loginForm.email = registerForm.email
  }
  clearFormFeedback()
  if (captchaEnabled.value) resetCaptchaProof()
  emit('update:mode', nextMode)
}

function syncAffiliateReferralCode() {
  const query = route?.query
  const code = resolveAffiliateReferralCode(query?.aff, query?.aff_code)
  if (code) {
    registerForm.aff_code = code
  }
}

async function navigateAfterAuth(target: string) {
  emit('close')
  if (router?.push) {
    await router.push(target)
  } else {
    window.location.href = target
  }
}

function resolvePostLoginTarget(): string {
  if (props.redirectTo) return props.redirectTo
  const routeRedirect = route?.query?.redirect
  if (typeof routeRedirect === 'string' && routeRedirect) return routeRedirect
  return authStore.isAdmin ? '/admin/dashboard' : '/dashboard'
}

async function goToRoute(path: string) {
  emit('close')
  if (router?.push) {
    await router.push(path)
  } else {
    window.location.href = path
  }
}

function applyPublicSettings(settings: Awaited<ReturnType<typeof getPublicSettings>>) {
  registrationEnabled.value = settings.registration_enabled === true
  emailVerifyEnabled.value = settings.email_verify_enabled === true
  promoCodeEnabled.value = settings.promo_code_enabled === true
  invitationCodeEnabled.value = settings.invitation_code_enabled === true
  affiliateEnabled.value = settings.affiliate_enabled === true
  backendModeEnabled.value = settings.backend_mode_enabled === true
  turnstileEnabled.value = settings.turnstile_enabled === true
  turnstileSiteKey.value = settings.turnstile_site_key || ''
  tencentCaptchaEnabled.value = settings.tencent_captcha_enabled === true
  tencentCaptchaAppId.value = settings.tencent_captcha_app_id || ''
  tencentCaptchaRegion.value = settings.tencent_captcha_region || 'cn'
  aliyunCaptchaEnabled.value = settings.aliyun_captcha_enabled === true
  aliyunCaptchaSceneId.value = settings.aliyun_captcha_scene_id || ''
  aliyunCaptchaPrefix.value = settings.aliyun_captcha_prefix || ''
  aliyunCaptchaRegion.value = settings.aliyun_captcha_region || 'cn'
  remoteSiteName.value = settings.site_name || ''
  linuxdoOAuthEnabled.value = settings.linuxdo_oauth_enabled === true
  dingtalkOAuthEnabled.value = settings.dingtalk_oauth_enabled ?? false
  wechatOAuthEnabled.value = isWeChatWebOAuthEnabled(settings)
  oidcOAuthEnabled.value = settings.oidc_oauth_enabled === true
  oidcOAuthProviderName.value = settings.oidc_oauth_provider_name || 'OIDC'
  githubOAuthEnabled.value = settings.github_oauth_enabled === true
  googleOAuthEnabled.value = settings.google_oauth_enabled === true
  passwordResetEnabled.value = settings.password_reset_enabled === true
  passkeyEnabled.value = settings.passkey_enabled === true
  registrationEmailSuffixWhitelist.value = normalizeRegistrationEmailSuffixWhitelist(
    settings.registration_email_suffix_whitelist || [],
  )
  emailDomainQuotaEnabled.value = settings.registration_email_domain_quota_enabled === true
  applyLoginAgreementSettings(settings)
}

let settingsFetched = false
async function loadModalSettings() {
  if (settingsFetched) return
  settingsFetched = true
  try {
    const expiredFlag = typeof sessionStorage !== 'undefined' ? sessionStorage.getItem('auth_expired') : null
    if (expiredFlag) {
      sessionStorage.removeItem('auth_expired')
      const message = t('auth.reloginRequired')
      errorMessage.value = message
      appStore.showWarning(message)
    }

    syncAffiliateReferralCode()
    const settings = await getPublicSettings()
    applyPublicSettings(settings)

    if (promoCodeEnabled.value) {
      const promoParam = route?.query?.promo
      if (typeof promoParam === 'string' && promoParam) {
        registerForm.promo_code = promoParam
        await validatePromoCodeDebounced(promoParam)
      }
    }
    syncAffiliateReferralCode()

    if (props.mode === 'register' && (!registrationEnabled.value || backendModeEnabled.value)) {
      emit('update:mode', 'login')
    }
  } catch {
    registrationEnabled.value = appStore.cachedPublicSettings?.registration_enabled ?? true
    backendModeEnabled.value = appStore.cachedPublicSettings?.backend_mode_enabled === true
    loginAgreementEnabled.value = false
    agreementAccepted.value = true
  } finally {
    settingsLoaded.value = true
  }
}

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      clearFormFeedback()
      void loadModalSettings()
    }
  },
  { immediate: true },
)

onMounted(() => {
  if (props.open) {
    void loadModalSettings()
  }
})

onUnmounted(() => {
  if (promoValidateTimeout) clearTimeout(promoValidateTimeout)
  if (invitationValidateTimeout) clearTimeout(invitationValidateTimeout)
})

function applyLoginAgreementSettings(settings: {
  login_agreement_enabled?: boolean
  login_agreement_mode?: string
  login_agreement_updated_at?: string
  login_agreement_revision?: string
  login_agreement_documents?: LoginAgreementDocument[]
}) {
  const documents = Array.isArray(settings.login_agreement_documents)
    ? settings.login_agreement_documents.filter((doc) => doc.title?.trim())
    : []
  loginAgreementDocuments.value = documents
  loginAgreementEnabled.value = settings.login_agreement_enabled === true && documents.length > 0
  loginAgreementMode.value = settings.login_agreement_mode === 'checkbox' ? 'checkbox' : 'modal'
  loginAgreementUpdatedAt.value = settings.login_agreement_updated_at || ''
  loginAgreementRevision.value =
    settings.login_agreement_revision ||
    `${loginAgreementUpdatedAt.value}:${documents.map((doc) => `${doc.id}:${doc.title}`).join('|')}`

  agreementAccepted.value =
    !loginAgreementEnabled.value || hasAcceptedLoginAgreement(loginAgreementRevision.value)
  showAgreementModal.value =
    loginAgreementEnabled.value &&
    !agreementAccepted.value &&
    loginAgreementMode.value !== 'checkbox'
}

function hasAcceptedLoginAgreement(revision: string): boolean {
  if (!revision) return false
  try {
    const raw = localStorage.getItem(LOGIN_AGREEMENT_STORAGE_KEY)
    if (!raw) return false
    const parsed = JSON.parse(raw) as { revision?: string }
    return parsed.revision === revision
  } catch {
    return false
  }
}

function acceptLoginAgreement() {
  if (loginAgreementRevision.value) {
    localStorage.setItem(
      LOGIN_AGREEMENT_STORAGE_KEY,
      JSON.stringify({
        revision: loginAgreementRevision.value,
        accepted_at: new Date().toISOString(),
      }),
    )
  }
  agreementAccepted.value = true
  showAgreementModal.value = false
}

function rejectLoginAgreement(current: AuthModalMode) {
  localStorage.removeItem(LOGIN_AGREEMENT_STORAGE_KEY)
  agreementAccepted.value = false
  showAgreementModal.value = false
  appStore.showWarning(
    t(
      current === 'login'
        ? 'legal.loginAgreementPrompt.loginRejectedWarning'
        : 'legal.loginAgreementPrompt.registerRejectedWarning',
    ),
  )
}

function onTurnstileVerify(token: string, randstr = '') {
  turnstileToken.value = token
  tencentCaptchaRandstr.value = randstr
  loginErrors.turnstile = ''
  registerErrors.turnstile = ''
}

function onTurnstileExpire() {
  turnstileToken.value = ''
  tencentCaptchaRandstr.value = ''
  const msg = t('auth.turnstileExpired')
  if (activeMode.value === 'login') loginErrors.turnstile = msg
  else registerErrors.turnstile = msg
}

function onTurnstileError() {
  turnstileToken.value = ''
  tencentCaptchaRandstr.value = ''
  const msg = t('auth.turnstileFailed')
  if (activeMode.value === 'login') loginErrors.turnstile = msg
  else registerErrors.turnstile = msg
}

function resetCaptchaProof() {
  activeTurnstile()?.reset()
  turnstileToken.value = ''
  tencentCaptchaRandstr.value = ''
  loginErrors.turnstile = ''
  registerErrors.turnstile = ''
}

async function acquireActionProof(): Promise<boolean> {
  if (!actionCaptchaEnabled.value) return true
  const proof = await activeTurnstile()?.verifyAction()
  if (!proof) return false
  turnstileToken.value = proof.token
  tencentCaptchaRandstr.value = proof.randstr
  return true
}

async function focusFirstInvalidField(form: HTMLFormElement | null) {
  await nextTick()
  form?.querySelector<HTMLInputElement>('[aria-invalid="true"]')?.focus()
}

function validateLoginForm(): boolean {
  loginErrors.email = ''
  loginErrors.password = ''
  loginErrors.turnstile = ''

  if (agreementGateActive.value) {
    appStore.showWarning(t('legal.loginAgreementPrompt.loginRequiredWarning'))
    if (loginAgreementMode.value !== 'checkbox') showAgreementModal.value = true
    return false
  }

  let valid = true
  if (!loginForm.email.trim()) {
    loginErrors.email = t('auth.emailRequired')
    valid = false
  } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(loginForm.email)) {
    loginErrors.email = t('auth.invalidEmail')
    valid = false
  }

  if (!loginForm.password) {
    loginErrors.password = t('auth.passwordRequired')
    valid = false
  } else if (loginForm.password.length < 6) {
    loginErrors.password = t('auth.passwordMinLength')
    valid = false
  }

  if (turnstileEnabled.value && !turnstileToken.value) {
    loginErrors.turnstile = t('auth.completeVerification')
    valid = false
  }

  return valid
}

async function handleLogin() {
  errorMessage.value = ''
  if (!validateLoginForm()) {
    await focusFirstInvalidField(loginFormRef.value)
    return
  }
  if (!(await acquireActionProof())) return

  isLoading.value = true
  try {
    const response = await authStore.login({
      email: loginForm.email,
      password: loginForm.password,
      turnstile_token:
        turnstileEnabled.value || aliyunCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_ticket: tencentCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_randstr: tencentCaptchaEnabled.value ? tencentCaptchaRandstr.value : undefined,
    })

    if (isTotp2FARequired(response)) {
      const totpResponse = response as TotpLoginResponse
      totpTempToken.value = totpResponse.temp_token || ''
      totpUserEmailMasked.value = totpResponse.user_email_masked || ''
      show2FAModal.value = true
      isLoading.value = false
      return
    }

    clearAllAffiliateReferralCodes()
    appStore.showSuccess(t('auth.loginSuccess'))
    await navigateAfterAuth(resolvePostLoginTarget())
  } catch (error: unknown) {
    errorMessage.value = extractI18nErrorMessage(error, t, 'auth.errors', t('auth.loginFailed'))
    appStore.showError(errorMessage.value)
  } finally {
    if (captchaEnabled.value) resetCaptchaProof()
    isLoading.value = false
  }
}

async function handlePasskeyLogin() {
  if (agreementGateActive.value) {
    appStore.showWarning(t('legal.loginAgreementPrompt.loginRequiredWarning'))
    if (loginAgreementMode.value !== 'checkbox') showAgreementModal.value = true
    return
  }

  passkeyLoading.value = true
  try {
    let proof: ActionCaptchaRequestProof | undefined
    if (actionCaptchaEnabled.value) {
      const result = await activeTurnstile()?.verifyAction()
      if (!result) return
      proof = tencentCaptchaEnabled.value
        ? { tencent_captcha_ticket: result.token, tencent_captcha_randstr: result.randstr }
        : { turnstile_token: result.token }
    }

    await authStore.loginWithPasskey(proof)
    clearAllAffiliateReferralCodes()
    appStore.showSuccess(t('auth.loginSuccess'))
    await navigateAfterAuth(resolvePostLoginTarget())
  } catch (error: unknown) {
    const fallback =
      error instanceof DOMException && error.name === 'NotAllowedError'
        ? t('auth.passkeyCancelled')
        : t('auth.passkeyFailed')
    errorMessage.value = extractI18nErrorMessage(error, t, 'auth.errors', fallback)
    appStore.showError(errorMessage.value)
  } finally {
    if (actionCaptchaEnabled.value) resetCaptchaProof()
    passkeyLoading.value = false
  }
}

async function handleOAuthStart(request: OAuthLoginStart, disabled: boolean) {
  if (disabled) return
  if (!actionCaptchaEnabled.value) {
    window.location.href = buildOAuthLoginStartURL(request)
    return
  }

  isLoading.value = true
  try {
    const proof = await activeTurnstile()?.verifyAction()
    if (!proof) return
    const result = await startOAuthLogin(
      request,
      tencentCaptchaEnabled.value
        ? { tencent_captcha_ticket: proof.token, tencent_captcha_randstr: proof.randstr }
        : { turnstile_token: proof.token },
    )
    window.location.href = result.authorize_url
  } catch (error: unknown) {
    errorMessage.value = extractI18nErrorMessage(error, t, 'auth.errors', t('auth.turnstileFailed'))
    appStore.showError(errorMessage.value)
  } finally {
    resetCaptchaProof()
    isLoading.value = false
  }
}

function handleLoginOAuthStart(request: OAuthLoginStart) {
  return handleOAuthStart(request, loginActionDisabled.value)
}

function handleRegisterOAuthStart(request: OAuthLoginStart) {
  return handleOAuthStart(request, registrationActionDisabled.value)
}

async function handle2FAVerify(code: string) {
  totpModalRef.value?.setVerifying(true)
  try {
    await authStore.login2FA(totpTempToken.value, code)
    show2FAModal.value = false
    clearAllAffiliateReferralCodes()
    appStore.showSuccess(t('auth.loginSuccess'))
    await navigateAfterAuth(resolvePostLoginTarget())
  } catch (error: unknown) {
    const err = error as { message?: string; response?: { data?: { message?: string } } }
    const message = err.response?.data?.message || err.message || t('profile.totp.loginFailed')
    totpModalRef.value?.setError(message)
    totpModalRef.value?.setVerifying(false)
  }
}

function handle2FACancel() {
  show2FAModal.value = false
  totpTempToken.value = ''
  totpUserEmailMasked.value = ''
}

function handlePromoCodeInput() {
  const code = registerForm.promo_code.trim()
  promoValidation.valid = false
  promoValidation.invalid = false
  promoValidation.bonusAmount = null
  promoValidation.message = ''

  if (!code) {
    promoValidating.value = false
    return
  }
  if (promoValidateTimeout) clearTimeout(promoValidateTimeout)
  promoValidateTimeout = setTimeout(() => {
    void validatePromoCodeDebounced(code)
  }, 500)
}

async function validatePromoCodeDebounced(code: string) {
  if (!code.trim()) return
  promoValidating.value = true
  try {
    const result = await validatePromoCode(code)
    if (result.valid) {
      promoValidation.valid = true
      promoValidation.invalid = false
      promoValidation.bonusAmount = result.bonus_amount || 0
      promoValidation.message = ''
    } else {
      promoValidation.valid = false
      promoValidation.invalid = true
      promoValidation.bonusAmount = null
      promoValidation.message = getPromoErrorMessage(result.error_code)
    }
  } catch {
    promoValidation.valid = false
    promoValidation.invalid = true
    promoValidation.message = t('auth.promoCodeInvalid')
  } finally {
    promoValidating.value = false
  }
}

function getPromoErrorMessage(errorCode?: string): string {
  switch (errorCode) {
    case 'PROMO_CODE_NOT_FOUND':
      return t('auth.promoCodeNotFound')
    case 'PROMO_CODE_EXPIRED':
      return t('auth.promoCodeExpired')
    case 'PROMO_CODE_DISABLED':
      return t('auth.promoCodeDisabled')
    case 'PROMO_CODE_MAX_USED':
      return t('auth.promoCodeMaxUsed')
    case 'PROMO_CODE_ALREADY_USED':
      return t('auth.promoCodeAlreadyUsed')
    default:
      return t('auth.promoCodeInvalid')
  }
}

function handleInvitationCodeInput() {
  const code = registerForm.invitation_code.trim()
  invitationValidation.valid = false
  invitationValidation.invalid = false
  invitationValidation.message = ''
  registerErrors.invitation_code = ''
  if (!code) return

  if (invitationValidateTimeout) clearTimeout(invitationValidateTimeout)
  invitationValidateTimeout = setTimeout(() => {
    void validateInvitationCodeDebounced(code)
  }, 500)
}

async function validateInvitationCodeDebounced(code: string) {
  invitationValidating.value = true
  try {
    const result = await validateInvitationCode(code)
    if (result.valid) {
      invitationValidation.valid = true
      invitationValidation.invalid = false
      invitationValidation.message = ''
    } else {
      invitationValidation.valid = false
      invitationValidation.invalid = true
      invitationValidation.message = t('auth.invitationCodeInvalid')
    }
  } catch {
    invitationValidation.valid = false
    invitationValidation.invalid = true
    invitationValidation.message = t('auth.invitationCodeInvalid')
  } finally {
    invitationValidating.value = false
  }
}

function buildEmailSuffixNotAllowedMessage(): string {
  const normalizedWhitelist = normalizeRegistrationEmailSuffixWhitelist(
    registrationEmailSuffixWhitelist.value,
  )
  if (normalizedWhitelist.length === 0) {
    return t('auth.emailSuffixNotAllowed')
  }
  const separator = String(locale.value || '').toLowerCase().startsWith('zh') ? '、' : ', '
  return t('auth.emailSuffixNotAllowedWithAllowed', {
    suffixes: formatRegistrationEmailSuffixWhitelistForMessage(normalizedWhitelist, {
      separator,
      more: (count) => t('auth.emailSuffixAllowedMore', { count }),
    }),
  })
}

function validateRegisterForm(): boolean {
  registerErrors.email = ''
  registerErrors.password = ''
  registerErrors.confirmPassword = ''
  registerErrors.turnstile = ''
  registerErrors.invitation_code = ''

  if (agreementGateActive.value) {
    appStore.showWarning(t('legal.loginAgreementPrompt.registerRequiredWarning'))
    if (loginAgreementMode.value !== 'checkbox') showAgreementModal.value = true
    return false
  }

  let valid = true
  if (!registerForm.email.trim()) {
    registerErrors.email = t('auth.emailRequired')
    valid = false
  } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(registerForm.email)) {
    registerErrors.email = t('auth.invalidEmail')
    valid = false
  } else if (
    !emailDomainQuotaEnabled.value &&
    !isRegistrationEmailSuffixAllowed(registerForm.email, registrationEmailSuffixWhitelist.value)
  ) {
    registerErrors.email = buildEmailSuffixNotAllowedMessage()
    valid = false
  }

  if (!registerForm.password) {
    registerErrors.password = t('auth.passwordRequired')
    valid = false
  } else if (registerForm.password.length < 6) {
    registerErrors.password = t('auth.passwordMinLength')
    valid = false
  }

  if (!confirmPassword.value) {
    registerErrors.confirmPassword = t('auth.confirmPasswordRequired')
    valid = false
  } else if (registerForm.password !== confirmPassword.value) {
    registerErrors.confirmPassword = t('auth.passwordsDoNotMatch')
    valid = false
  }

  if (invitationCodeEnabled.value && !registerForm.invitation_code.trim()) {
    registerErrors.invitation_code = t('auth.invitationCodeRequired')
    valid = false
  }

  if (turnstileEnabled.value && !turnstileToken.value) {
    registerErrors.turnstile = t('auth.completeVerification')
    valid = false
  }

  return valid
}

async function handleRegister() {
  errorMessage.value = ''
  if (!validateRegisterForm()) {
    await focusFirstInvalidField(registerFormRef.value)
    return
  }

  if (registerForm.promo_code.trim()) {
    if (promoValidating.value) {
      errorMessage.value = t('auth.promoCodeValidating')
      return
    }
    if (promoValidation.invalid) {
      errorMessage.value = t('auth.promoCodeInvalidCannotRegister')
      await focusFirstInvalidField(registerFormRef.value)
      return
    }
  }

  if (invitationCodeEnabled.value) {
    if (invitationValidating.value) {
      errorMessage.value = t('auth.invitationCodeValidating')
      return
    }
    if (invitationValidation.invalid) {
      errorMessage.value = t('auth.invitationCodeInvalidCannotRegister')
      await focusFirstInvalidField(registerFormRef.value)
      return
    }
    if (registerForm.invitation_code.trim() && !invitationValidation.valid) {
      errorMessage.value = t('auth.invitationCodeValidating')
      await validateInvitationCodeDebounced(registerForm.invitation_code.trim())
      if (!invitationValidation.valid) {
        errorMessage.value = t('auth.invitationCodeInvalidCannotRegister')
        await focusFirstInvalidField(registerFormRef.value)
        return
      }
    }
  }

  if (!(await acquireActionProof())) return

  isLoading.value = true
  try {
    const affCode = registerForm.aff_code.trim() || loadAffiliateReferralCode()
    if (affCode) registerForm.aff_code = affCode

    if (emailVerifyEnabled.value) {
      sessionStorage.setItem(
        'register_data',
        JSON.stringify({
          email: registerForm.email,
          password: registerForm.password,
          turnstile_token:
            turnstileEnabled.value || aliyunCaptchaEnabled.value ? turnstileToken.value : undefined,
          tencent_captcha_ticket: tencentCaptchaEnabled.value ? turnstileToken.value : undefined,
          tencent_captcha_randstr: tencentCaptchaEnabled.value
            ? tencentCaptchaRandstr.value
            : undefined,
          promo_code: registerForm.promo_code || undefined,
          invitation_code: registerForm.invitation_code || undefined,
          ...(affCode ? { aff_code: affCode } : {}),
        }),
      )
      await goToRoute('/email-verify')
      return
    }

    await authStore.register({
      email: registerForm.email,
      password: registerForm.password,
      turnstile_token:
        turnstileEnabled.value || aliyunCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_ticket: tencentCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_randstr: tencentCaptchaEnabled.value
        ? tencentCaptchaRandstr.value
        : undefined,
      promo_code: registerForm.promo_code || undefined,
      invitation_code: registerForm.invitation_code || undefined,
      ...(affCode ? { aff_code: affCode } : {}),
    })
    clearAffiliateReferralCode()
    appStore.showSuccess(t('auth.accountCreatedSuccess', { siteName: displaySiteName.value }))
    await navigateAfterAuth('/dashboard')
  } catch (error: unknown) {
    errorMessage.value = buildRegistrationErrorMessage(error, t('auth.registrationFailed'))
    appStore.showError(errorMessage.value)
  } finally {
    if (captchaEnabled.value) resetCaptchaProof()
    isLoading.value = false
  }
}

function buildRegistrationErrorMessage(error: unknown, fallback: string): string {
  if (extractApiErrorCode(error) === 'EMAIL_BLOCKED') {
    return t('auth.errors.EMAIL_BLOCKED')
  }
  if (extractApiErrorCode(error) === 'EMAIL_DOMAIN_REGISTRATION_LIMIT') {
    return t('auth.emailDomainRegistrationLimit')
  }
  return buildAuthErrorMessage(error, { fallback })
}
</script>

<style scoped>
.auth-portal-layer {
  position: fixed;
  inset: 0;
  z-index: 80;
}

.public-glass.auth-portal-root {
  min-height: 100%;
  height: 100%;
  background: rgb(3 5 11 / 0.68);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: max(16px, env(safe-area-inset-top, 16px)) 16px max(16px, env(safe-area-inset-bottom, 16px));
}

.auth-portal-dialog {
  position: relative;
  width: min(440px, 100%);
  max-height: min(88svh, 760px);
  display: flex;
  flex-direction: column;
  border-radius: 24px;
  background: rgb(9 14 26 / 0.88);
  border: 1px solid rgb(255 255 255 / 0.14);
  box-shadow:
    inset 0 1px 0 rgb(255 255 255 / 0.18),
    0 32px 80px -20px rgb(0 0 0 / 0.85);
  overflow: hidden;
  outline: none;
  animation: auth-portal-enter 260ms cubic-bezier(0.16, 1, 0.3, 1) both;
}

@keyframes auth-portal-enter {
  from {
    opacity: 0;
    transform: translateY(14px) scale(0.985);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

.auth-portal-crown {
  height: 2px;
  width: 100%;
  flex: none;
  background: linear-gradient(90deg, transparent 0%, #8fb5a8 38%, #c9a876 62%, transparent 100%);
  opacity: 0.9;
}

.auth-portal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 18px 22px 12px;
  flex: none;
}

.auth-portal-brand {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  font-size: 0.96rem;
  font-weight: 640;
  letter-spacing: -0.02em;
  color: #f8fafc;
}

.auth-portal-brand img {
  width: 24px;
  height: 24px;
  object-fit: contain;
  flex: none;
}

.auth-portal-close {
  width: 34px;
  height: 34px;
  border-radius: 999px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(255 255 255 / 0.14);
  background: rgb(255 255 255 / 0.05);
  color: rgb(226 232 240 / 0.78);
  cursor: pointer;
  transition: background-color 160ms ease, color 160ms ease, border-color 160ms ease;
}

.auth-portal-close:hover {
  background: rgb(255 255 255 / 0.12);
  border-color: rgb(255 255 255 / 0.28);
  color: #ffffff;
}

.auth-portal-tabs {
  margin: 0 22px 4px;
  padding: 4px;
  border-radius: 999px;
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 4px;
  background: rgb(255 255 255 / 0.05);
  border: 1px solid rgb(255 255 255 / 0.1);
  flex: none;
}

.auth-portal-tab {
  min-height: 36px;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: rgb(203 213 225 / 0.75);
  font-size: 0.85rem;
  font-weight: 600;
  cursor: pointer;
  transition: background-color 180ms ease, color 180ms ease, box-shadow 180ms ease;
}

.auth-portal-tab.is-active {
  background: #f5f7fb;
  color: #050811;
  box-shadow: 0 4px 14px -4px rgb(0 0 0 / 0.45);
}

.auth-portal-body {
  padding: 14px 24px 20px;
  overflow-y: auto;
  overscroll-behavior: contain;
}

.auth-portal-flow {
  gap: 1.25rem;
}

.auth-portal-intro {
  text-align: center;
}

.auth-portal-title {
  margin: 0;
  font-size: 1.45rem;
  font-weight: 620;
  letter-spacing: -0.03em;
  color: #f8fafc;
}

.auth-portal-subtitle {
  margin: 4px 0 0;
  font-size: 0.84rem;
  color: rgb(203 213 225 / 0.72);
}

.auth-portal-root .auth-content .input {
  min-height: 2.85rem;
  border-radius: 12px;
  background: rgb(255 255 255 / 0.05);
  border-color: rgb(255 255 255 / 0.13);
  color: #f8fafc;
  font-size: 0.94rem;
}

.auth-portal-root .auth-content .input:focus {
  border-color: #8fb5a8;
  outline-color: rgb(143 181 168 / 0.25);
}

.auth-portal-root .auth-content .primary-btn.auth-portal-submit {
  min-height: 2.9rem;
  border-radius: 12px;
  background: #f5f7fb;
  color: #050811;
  border: 1px solid transparent;
  box-shadow: none;
  font-weight: 640;
}

.auth-portal-root .auth-content .primary-btn.auth-portal-submit:hover:not(:disabled) {
  background: #ffffff;
  box-shadow: 0 0 0 1px rgb(143 181 168 / 0.55), 0 10px 24px -10px rgb(143 181 168 / 0.45);
}

.auth-portal-root .auth-link {
  background: transparent;
  border: 0;
  padding: 0;
  color: #8fb5a8;
  font-size: 0.82rem;
  font-weight: 560;
  cursor: pointer;
}

.auth-portal-root .auth-link:hover {
  text-decoration: underline;
}

.auth-portal-footer {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-wrap: wrap;
  gap: 6px;
  padding: 12px 24px 16px;
  border-top: 1px solid rgb(255 255 255 / 0.09);
  background: rgb(4 8 16 / 0.55);
  font-size: 0.83rem;
  color: rgb(203 213 225 / 0.72);
  text-align: center;
}

.auth-portal-footer .auth-footer-note {
  margin: 2px 0 0;
  flex-basis: 100%;
  font-size: 0.76rem;
}

@media (prefers-reduced-motion: reduce) {
  .auth-portal-dialog {
    animation: none;
  }
}
</style>
