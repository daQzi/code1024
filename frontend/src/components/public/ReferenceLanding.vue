<template>
  <div ref="landingRoot" class="public-glass reference-landing">
    <PublicBackdrop />
    <div ref="progressBar" class="rl-reading-progress" aria-hidden="true" />
    <a class="rl-skip" href="#home-main">{{ t('glass.nav.skip') }}</a>
    <header ref="headerRef" class="rl-header hp__top" :class="{ 'rl-header--scrolled': isScrolled }" @keydown.esc="closeMenu">
      <nav class="glass-panel rl-nav rl-container" :aria-label="t('glass.nav.navigation')">
        <router-link to="/" class="rl-brand"><PublicBrandLogo :src="siteLogo || '/logo.svg'" /><span>{{ siteName }}</span></router-link>
        <div class="rl-nav-links">
          <a
            v-for="item in navigation"
            :key="item.id"
            :href="`#${item.id}`"
            :class="{ 'is-active': activeSection === item.id }"
            :aria-current="activeSection === item.id ? 'location' : undefined"
            @click="scrollToSection(item.id, $event)"
          >{{ item.label }}</a>
          <router-link v-if="modelPlazaEnabled" :to="modelPlazaPath" class="rl-nav-plaza" data-testid="nav-model-plaza">
            <Icon name="grid" size="sm" aria-hidden="true" />
            <span>{{ t('nav.modelPlaza') }}</span>
          </router-link>
        </div>
        <div class="rl-nav-actions">
          <PublicLocaleSwitcher />
          <button class="glass-button rl-icon-button" type="button" :aria-label="t(isDark ? 'glass.nav.light' : 'glass.nav.dark')" @click="toggleTheme"><Icon :name="isDark ? 'sun' : 'moon'" size="md" aria-hidden="true" /></button>
          <router-link :to="entryPath" class="glass-button glass-button--primary rl-button rl-button--dark rl-nav-login">{{ t(isAuthenticated ? 'glass.nav.console' : 'glass.nav.login') }}</router-link>
          <button ref="menuButton" class="glass-button rl-icon-button rl-menu-toggle" type="button" :aria-expanded="menuOpen" aria-controls="home-mobile-menu" :aria-label="t(menuOpen ? 'glass.nav.closeMenu' : 'glass.nav.openMenu')" @click="menuOpen = !menuOpen"><Icon :name="menuOpen ? 'x' : 'menu'" size="md" aria-hidden="true" /></button>
        </div>
      </nav>
      <nav v-if="menuOpen" id="home-mobile-menu" class="glass-panel rl-mobile-nav rl-container" :aria-label="t('glass.nav.navigation')" @click="closeMenu">
        <a
          v-for="item in navigation"
          :key="item.id"
          :href="`#${item.id}`"
          :class="{ 'is-active': activeSection === item.id }"
          :aria-current="activeSection === item.id ? 'location' : undefined"
          @click="scrollToSection(item.id, $event)"
        >{{ item.label }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></a>
        <router-link v-if="modelPlazaEnabled" :to="modelPlazaPath" class="rl-mobile-plaza" data-testid="mobile-model-plaza">{{ t('nav.modelPlaza') }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></router-link>
        <button type="button" class="rl-mobile-theme" @click="toggleTheme">{{ t(isDark ? 'glass.nav.light' : 'glass.nav.dark') }}<Icon :name="isDark ? 'sun' : 'moon'" size="sm" aria-hidden="true" /></button>
        <div class="rl-mobile-language"><span>{{ t('glass.nav.language') }}</span><PublicLocaleSwitcher /></div>
      </nav>
    </header>

    <main id="home-main">
      <section ref="heroSection" class="rl-hero rl-container" aria-labelledby="home-title">
        <div class="rl-hero-copy">
          <h1 id="home-title">{{ t('referenceLanding.hero.title') }}<span>{{ t('referenceLanding.hero.accent') }}</span></h1>
          <p class="rl-hero-description">{{ t('referenceLanding.hero.description') }}</p>
          <div class="rl-hero-actions">
            <router-link :to="entryPath" class="glass-button glass-button--primary rl-button rl-button--dark">{{ t(isAuthenticated ? 'referenceLanding.hero.authenticated' : 'referenceLanding.hero.primary') }}<Icon name="arrowRight" size="md" aria-hidden="true" /></router-link>
            <a href="#connection" class="glass-button rl-button rl-button--outline" @click="scrollToSection('connection', $event)">{{ t('referenceLanding.hero.secondary') }}<Icon name="chevronRight" size="sm" aria-hidden="true" /></a>
          </div>
          <p v-if="registrationEnabled && !isAuthenticated" class="rl-register">{{ t('referenceLanding.hero.registerLead') }} <router-link to="/register">{{ t('referenceLanding.hero.register') }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></router-link></p>
          <div class="glass-panel rl-hero-endpoint"><div><span>{{ t('referenceLanding.hero.endpoint') }}</span><code>{{ apiBaseUrl }}</code></div><button class="glass-button rl-icon-button" :class="{ 'is-copied': copiedTarget === 'hero-address' }" type="button" :aria-label="t(copiedTarget === 'hero-address' ? 'glass.home.copied' : 'referenceLanding.hero.copy')" @click="copyValue(apiBaseUrl, 'hero-address')"><Icon :name="copiedTarget === 'hero-address' ? 'check' : 'copy'" size="md" aria-hidden="true" /></button></div>
          <p class="rl-hero-footnote">{{ t('referenceLanding.hero.footnote') }}</p>
        </div>

        <LandingStoryMarquee :site-name="siteName" />

      </section>

      <section class="rl-section rl-container rl-showcase" aria-labelledby="showcase-title">
        <div class="rl-section-heading" data-reveal>
          <span class="glass-control rl-eyebrow">{{ t('glassDemo.example') }}</span>
          <h2 id="showcase-title">{{ t('glassDemo.label') }}</h2>
          <p>{{ t('glass.home.heroDescription') }}</p>
          <div class="glass-panel rl-demo-tabs" role="group" :aria-label="t('glassDemo.example')">
            <button
              v-for="(scene, index) in demoScenes"
              :key="scene.id"
              type="button"
              class="rl-demo-tab"
              :class="{ 'is-active': activeDemoScene === index }"
              :aria-pressed="activeDemoScene === index"
              @click="selectDemoScene(index)"
            >
              <Icon :name="scene.icon" size="sm" aria-hidden="true" />
              <span>{{ scene.label }}</span>
            </button>
          </div>
        </div>
        <div class="rl-showcase-stage" data-reveal data-reveal-delay="80">
          <AiWorkflowDemo ref="workflowDemo" @scene-change="activeDemoScene = $event" />
        </div>
      </section>

      <section class="rl-section rl-container rl-challenges" aria-labelledby="challenges-title">
        <div class="rl-section-heading" data-reveal><h2 id="challenges-title">{{ t('referenceLanding.challenges.title') }} <span>{{ t('referenceLanding.challenges.accent') }}</span></h2><p>{{ t('referenceLanding.challenges.description') }}</p></div>
        <div class="rl-challenge-grid">
          <article v-for="(problem, index) in challenges" :key="problem.id" class="glass-panel rl-challenge" data-reveal :data-reveal-delay="index * 70"><span class="glass-control rl-feature-icon"><Icon :name="problem.icon" size="md" aria-hidden="true" /></span><h3>{{ problem.title }}</h3><p>{{ problem.description }}</p></article>
        </div>
      </section>

      <section id="capabilities" class="rl-section rl-container" aria-labelledby="features-title">
        <div class="rl-section-heading" data-reveal><h2 id="features-title">{{ t('referenceLanding.features.title') }} <span>{{ t('referenceLanding.features.accent') }}</span></h2><p>{{ t('referenceLanding.features.description') }}</p></div>
        <div class="rl-features" :class="{ 'rl-features--two': !modelPlazaEnabled }">
          <article v-if="modelPlazaEnabled" class="glass-panel" data-testid="model-feature" data-reveal data-reveal-delay="0"><Icon name="cube" size="xl" aria-hidden="true" /><h3>{{ t('referenceLanding.features.modelTitle') }}</h3><p>{{ t('referenceLanding.features.modelDescription') }}</p><router-link :to="modelPlazaPath" class="rl-text-link">{{ modelPlazaLinkLabel }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></router-link></article>
          <article class="glass-panel" data-testid="keys-feature" data-reveal :data-reveal-delay="modelPlazaEnabled ? 80 : 0"><Icon name="key" size="xl" aria-hidden="true" /><h3>{{ t('referenceLanding.features.keyTitle') }}</h3><p>{{ t('referenceLanding.features.keyDescription') }}</p><router-link :to="protectedPath('/keys')" class="rl-text-link">{{ t('glass.home.keysAction') }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></router-link></article>
          <article class="glass-panel" data-testid="usage-feature" data-reveal :data-reveal-delay="modelPlazaEnabled ? 160 : 80"><Icon name="chart" size="xl" aria-hidden="true" /><h3>{{ t('referenceLanding.features.usageTitle') }}</h3><p>{{ t('referenceLanding.features.usageDescription') }}</p><router-link :to="protectedPath('/usage')" class="rl-text-link">{{ t('glass.home.usageAction') }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></router-link></article>
        </div>
      </section>

      <section id="quota" class="rl-quota-section" aria-labelledby="quota-title">
        <div class="glass-panel rl-container rl-quota-layout">
          <div class="rl-quota-intro" data-reveal><h2 id="quota-title">{{ t('referenceLanding.quota.title') }}<span>{{ t('referenceLanding.quota.accent') }}</span></h2><p>{{ t('referenceLanding.quota.description') }}</p><router-link :to="protectedPath('/dashboard')" class="glass-button glass-button--primary rl-button rl-button--dark">{{ t('referenceLanding.quota.action') }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></router-link></div>
          <div class="rl-quota-rules" data-reveal>
            <article v-for="rule in quotaRules" :key="rule.id"><div class="rl-quota-rule-heading"><h3>{{ rule.title }}</h3><span>{{ rule.scope }}</span></div><p>{{ rule.description }}</p></article>
            <p class="rl-quota-note"><Icon name="infoCircle" size="md" aria-hidden="true" />{{ t('referenceLanding.quota.note') }}</p>
          </div>
        </div>
      </section>

      <section id="connection" class="rl-section rl-container rl-connection" aria-labelledby="connection-title">
        <div class="rl-connect-intro" data-reveal>
          <h2 id="connection-title">{{ t('referenceLanding.connection.title') }}<span>{{ t('referenceLanding.connection.accent') }}</span></h2>
          <p>{{ t('referenceLanding.connection.description') }}</p>
          <div class="glass-panel rl-tabs" role="tablist" :aria-label="t('glass.home.connectionTabs')" @keydown="handleTabKey($event, 'connection')"><button v-for="tab in connectionTabs" :id="`connection-tab-${tab}`" :key="tab" type="button" role="tab" :aria-selected="connectionMode === tab" :aria-controls="`connection-panel-${tab}`" :tabindex="connectionMode === tab ? 0 : -1" @click="connectionMode = tab"><Icon :name="tab === 'client' ? 'grid' : 'terminal'" size="sm" aria-hidden="true" />{{ t(`glass.home.${tab}Tab`) }}</button></div>
          <p class="rl-protocol-note">{{ t('glass.home.protocolNote') }}</p>
          <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer" class="rl-text-link">{{ t('glass.nav.docs') }}<Icon name="externalLink" size="sm" aria-hidden="true" /></a>
        </div>
        <div class="rl-connect-panels" data-reveal>
          <div v-show="connectionMode === 'client'" id="connection-panel-client" class="glass-panel rl-config-panel" role="tabpanel" aria-labelledby="connection-tab-client" tabindex="0">
            <h3>{{ t('glass.home.clientTitle') }}</h3>
            <p>{{ t('glass.home.clientDescription') }}</p>
            <dl class="rl-config-list">
              <div><dt>{{ t('glass.home.baseUrlLabel') }}<small>Base URL</small></dt><dd><div class="rl-copy-row"><code>{{ apiBaseUrl }}</code><button class="glass-button rl-icon-button" :class="{ 'is-copied': copiedTarget === 'address' }" type="button" :aria-label="t(copiedTarget === 'address' ? 'glass.home.copied' : 'glass.home.copyAddress')" @click="copyValue(apiBaseUrl, 'address')"><Icon :name="copiedTarget === 'address' ? 'check' : 'copy'" size="md" aria-hidden="true" /></button></div><p>{{ t('glass.home.baseUrlNote') }}</p></dd></div>
              <div><dt>{{ t('glass.home.keyLabel') }}<small>API Key</small></dt><dd><strong>{{ t('glass.home.keyValue') }}</strong><p>{{ t('glass.home.keyNote') }}</p><router-link :to="protectedPath('/keys')" class="rl-text-link">{{ t('glass.home.keyAction') }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></router-link></dd></div>
              <div><dt>{{ t('glass.home.modelLabel') }}<small>Model</small></dt><dd><strong>{{ t('glass.home.modelValue') }}</strong><p>{{ t('glass.home.modelNote') }}</p><router-link v-if="modelPlazaEnabled" :to="modelPlazaPath" class="rl-text-link">{{ modelPlazaLinkLabel }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></router-link><p v-else>{{ t(appStore.publicSettingsLoaded ? 'glass.home.modelUnavailable' : 'glass.home.modelLoading') }}</p></dd></div>
            </dl>
          </div>
          <div v-show="connectionMode === 'developer'" id="connection-panel-developer" class="glass-panel rl-code-panel" role="tabpanel" aria-labelledby="connection-tab-developer" tabindex="0">
            <h3>{{ t('glass.home.developerTitle') }}</h3><p>{{ t('glass.home.developerDescription') }}</p>
            <div class="rl-editor-bar"><div class="rl-code-tabs" role="tablist" :aria-label="t('glass.home.codeTabs')" @keydown="handleTabKey($event, 'code')"><button v-for="language in codeLanguages" :id="`code-tab-${language}`" :key="language" type="button" role="tab" :aria-selected="codeLanguage === language" :aria-controls="`code-panel-${language}`" :tabindex="codeLanguage === language ? 0 : -1" @click="codeLanguage = language">{{ language === 'python' ? 'Python' : 'curl · Bash' }}</button></div><button class="rl-code-copy" :class="{ 'is-copied': copiedTarget === 'code' }" type="button" @click="copyValue(codeExample, 'code')"><Icon :name="copiedTarget === 'code' ? 'check' : 'copy'" size="sm" aria-hidden="true" />{{ t(copiedTarget === 'code' ? 'glass.home.copied' : 'glass.home.copyCode') }}</button></div>
            <pre v-for="language in codeLanguages" v-show="codeLanguage === language" :id="`code-panel-${language}`" :key="language" class="rl-code" role="tabpanel" :aria-labelledby="`code-tab-${language}`" tabindex="0"><code><template v-for="(token, tokenIndex) in highlightedCodeExamples[language]" :key="tokenIndex"><span v-if="token.type" :class="`rl-tok-${token.type}`">{{ token.text }}</span><template v-else>{{ token.text }}</template></template></code></pre>
            <div class="rl-code-notes"><p>{{ t(codeLanguage === 'python' ? 'glass.home.pythonSetup' : 'glass.home.curlSetup') }}</p><details><summary>{{ t('glass.home.environmentTitle') }}<Icon name="chevronDown" size="sm" aria-hidden="true" /></summary><p>{{ t('glass.home.environmentDescription') }}</p><h4>{{ t('glass.home.shellLabel') }}</h4><pre><code>export OPENAI_API_KEY='YOUR_API_KEY'
export MODEL_ID='YOUR_MODEL_ID'</code></pre><h4>{{ t('glass.home.powershellLabel') }}</h4><pre><code>$env:OPENAI_API_KEY='YOUR_API_KEY'
$env:MODEL_ID='YOUR_MODEL_ID'</code></pre></details></div>
          </div>
        </div>
      </section>

      <section id="quick-start" class="rl-section rl-container rl-start" aria-labelledby="start-title">
        <div class="rl-section-heading" data-reveal><h2 id="start-title">{{ t('referenceLanding.start.title') }}</h2><p>{{ t('referenceLanding.start.description') }}</p></div>
        <ol class="rl-steps"><li v-for="(step, index) in steps" :key="step.id" data-reveal :data-reveal-delay="index * 70"><span class="rl-step-number" aria-hidden="true">{{ index + 1 }}</span><h3>{{ step.title }}</h3><p>{{ step.desc }}</p><div class="rl-step-links"><router-link :to="step.to" class="rl-text-link">{{ step.action }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></router-link><router-link v-if="step.id === 'account' && registrationEnabled && !isAuthenticated" to="/register" class="rl-text-link">{{ t('glass.home.steps.registerAction') }}</router-link></div></li></ol>
        <div v-if="paymentEnabled" class="rl-billing"><div><h3>{{ t('glass.home.billingTitle') }}</h3><p>{{ t('glass.home.paymentAvailable') }}</p></div><router-link :to="protectedPath('/purchase')" class="rl-text-link">{{ t('glass.home.paymentAction') }}<Icon name="arrowRight" size="sm" aria-hidden="true" /></router-link></div>
      </section>

      <section id="questions" class="rl-section rl-container rl-questions" aria-labelledby="questions-title">
        <div data-reveal><h2 id="questions-title">{{ t('referenceLanding.faq.title') }}<span>{{ t('referenceLanding.faq.accent') }}</span></h2><p>{{ t('referenceLanding.faq.description') }}</p><p v-if="supportContact">{{ supportContact }}</p></div>
        <div class="glass-panel rl-faq-list" data-reveal><details v-for="faq in faqs" :key="faq.id"><summary>{{ faq.q }}<Icon name="plus" size="md" aria-hidden="true" /></summary><p>{{ faq.a }}</p></details></div>
      </section>
    </main>

    <footer class="rl-footer"><div class="rl-container"><div class="rl-footer-top"><div><router-link to="/" class="rl-brand"><PublicBrandLogo :src="siteLogo || '/logo.svg'" /><span>{{ siteName }}</span></router-link><p>{{ t('referenceLanding.footer.note') }}</p></div><nav :aria-label="t('glass.home.footerExplore')"><a href="#connection" @click="scrollToSection('connection', $event)">{{ t('referenceLanding.nav.connection') }}</a><a href="#quota" @click="scrollToSection('quota', $event)">{{ t('referenceLanding.nav.quota') }}</a><router-link v-if="modelPlazaEnabled" :to="modelPlazaPath">{{ t('nav.modelPlaza') }}</router-link><a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer">{{ t('glass.nav.docs') }}</a></nav><div class="rl-footer-contact"><span>{{ t('referenceLanding.footer.help') }}</span><a v-if="supportEmail" :href="`mailto:${supportEmail}`">{{ supportContact }}</a><span v-else-if="supportContact">{{ supportContact }}</span></div></div><div class="rl-footer-bottom"><span>&copy; {{ currentYear }} {{ siteName }}</span><p>{{ t('referenceLanding.footer.legal') }}</p><a href="#home-main" class="rl-back-top" @click="scrollToSection('home-main', $event)">{{ t('glass.nav.backTop') }}<Icon name="arrowUp" size="sm" aria-hidden="true" /></a></div></div></footer>
    <p class="rl-sr-only" role="status" aria-live="polite" aria-atomic="true">{{ copyStatus }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore, useAuthStore } from '@/stores'
import Icon from '@/components/icons/Icon.vue'
import AiWorkflowDemo from '@/components/public/AiWorkflowDemo.vue'
import PublicBackdrop from '@/components/public/PublicBackdrop.vue'
import LandingStoryMarquee from '@/components/public/LandingStoryMarquee.vue'
import PublicBrandLogo from '@/components/common/PublicBrandLogo.vue'
import PublicLocaleSwitcher from '@/components/public/PublicLocaleSwitcher.vue'
import { useClipboard } from '@/composables/useClipboard'
import { useLandingMotion } from '@/composables/useLandingMotion'
import { useTheme } from '@/composables/useTheme'
import { sanitizeUrl } from '@/utils/url'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import '@/styles/reference-landing.css'

const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()
const supportEmail = computed(() => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(supportContact.value) ? supportContact.value : '')
const supportContact = computed(() => appStore.cachedPublicSettings?.contact_info?.trim() || appStore.contactInfo?.trim() || '')
const { copyToClipboard } = useClipboard()
const { isDark, toggleTheme } = useTheme()
const landingRoot = ref<HTMLElement | null>(null)
const headerRef = ref<HTMLElement | null>(null)
const heroSection = ref<HTMLElement | null>(null)
const progressBar = ref<HTMLElement | null>(null)
const workflowDemo = ref<InstanceType<typeof AiWorkflowDemo> | null>(null)
useLandingMotion(landingRoot)
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const isAuthenticated = computed(() => authStore.isAuthenticated)
const entryPath = computed(() => isAuthenticated.value ? authStore.isAdmin ? '/admin/dashboard' : '/dashboard' : '/login')
const registrationEnabled = computed(() => appStore.cachedPublicSettings?.registration_enabled === true && appStore.cachedPublicSettings?.backend_mode_enabled !== true)
const invitationRequired = computed(() => appStore.cachedPublicSettings?.invitation_code_enabled === true)
const paymentEnabled = computed(() => appStore.cachedPublicSettings?.payment_enabled === true && appStore.cachedPublicSettings?.payment_balance_disabled !== true && appStore.cachedPublicSettings?.backend_mode_enabled !== true)
const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))
const modelPlazaRequiresAuth = computed(() => appStore.cachedPublicSettings?.model_plaza_require_auth === true)
const modelPlazaPath = computed(() => modelPlazaRequiresAuth.value ? protectedPath('/model-plaza') : '/model-plaza')
const modelPlazaLinkLabel = computed(() => t(modelPlazaRequiresAuth.value && !isAuthenticated.value ? 'glass.home.modelLoginAction' : 'glass.home.modelsAction'))
const apiBaseUrl = computed(() => {
  const configured = sanitizeUrl(appStore.cachedPublicSettings?.api_base_url?.trim() || appStore.apiBaseUrl?.trim() || '')
  return (configured || window.location.origin).replace(/\/+$/, '')
})
const currentYear = new Date().getFullYear()
function protectedPath(path: string) { return isAuthenticated.value ? path : `/login?redirect=${encodeURIComponent(path)}` }
const navigation = computed(() => ['capabilities', 'quota', 'connection', 'questions'].map((id, index) => ({ id, label: t(`referenceLanding.nav.${['features', 'quota', 'connection', 'questions'][index]}`) })))
const activeSection = ref('')
const isScrolled = ref(false)
const activeDemoScene = ref(0)
const demoScenes = computed(() => ([
  { id: 'codex', icon: 'terminal' },
  { id: 'reasoning', icon: 'sparkles' },
  { id: 'api', icon: 'bolt' },
] as const).map(scene => ({ ...scene, label: t(`glassDemo.${scene.id}.tag`) })))
function selectDemoScene(index: number) {
  activeDemoScene.value = index
  workflowDemo.value?.selectScene?.(index)
}
function scrollToSection(id: string, event: MouseEvent) {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
  const reduced = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
  const behavior: 'auto' | 'smooth' = reduced ? 'auto' : 'smooth'
  if (id === 'home-main') {
    event.preventDefault()
    activeSection.value = ''
    window.scrollTo?.({ top: 0, behavior })
    return
  }
  const target = document.getElementById(id)
  if (!target) return
  event.preventDefault()
  activeSection.value = id
  target.scrollIntoView?.({ behavior, block: 'start' })
}
const menuOpen = ref(false)
const menuButton = ref<HTMLButtonElement | null>(null)
function closeMenu(event?: Event) { menuOpen.value = false; if (event instanceof KeyboardEvent) menuButton.value?.focus() }
function handleOutsidePointer(event: PointerEvent) {
  if (!menuOpen.value) return
  if (event.target instanceof Node && headerRef.value?.contains(event.target)) return
  menuOpen.value = false
}
watch(menuOpen, open => {
  if (open) document.addEventListener('pointerdown', handleOutsidePointer)
  else document.removeEventListener('pointerdown', handleOutsidePointer)
})
const challenges = computed(() => ([
  { id: 'setup', icon: 'link' }, { id: 'budget', icon: 'chart' },
  { id: 'projects', icon: 'key' }, { id: 'records', icon: 'search' },
] as const).map(item => ({ ...item, title: t(`referenceLanding.challenges.${item.id}Title`), description: t(`referenceLanding.challenges.${item.id}Description`) })))
const quotaRules = computed(() => ['account', 'subscription', 'key'].map(id => ({ id, title: t(`referenceLanding.quota.${id}Title`), scope: t(`referenceLanding.quota.${id}Scope`), description: t(`referenceLanding.quota.${id}Description`) })))

const connectionTabs = ['client', 'developer'] as const
const connectionMode = ref<(typeof connectionTabs)[number]>('client')
const codeLanguages = ['python', 'curl'] as const
const codeLanguage = ref<(typeof codeLanguages)[number]>('python')
async function handleTabKey(event: KeyboardEvent, group: 'connection' | 'code') {
  if (event.altKey || event.ctrlKey || event.metaKey || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  const tabs = Array.from((event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('[role="tab"]'))
  const current = tabs.indexOf(document.activeElement as HTMLButtonElement)
  const index = current >= 0 ? current : tabs.findIndex(tab => tab.getAttribute('aria-selected') === 'true')
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length
  event.preventDefault()
  if (group === 'connection') connectionMode.value = connectionTabs[next]
  else codeLanguage.value = codeLanguages[next]
  await nextTick()
  tabs[next]?.focus()
}
const codeExamples = computed(() => ({
  python: `import os
from openai import OpenAI

client = OpenAI(
    base_url=${JSON.stringify(apiBaseUrl.value)},
    api_key=os.environ["OPENAI_API_KEY"]
)

response = client.chat.completions.create(
    model=os.environ["MODEL_ID"],
    messages=[
        {"role": "user", "content": "Hello!"}
    ]
)

print(response.choices[0].message.content)`,
  curl: String.raw`curl '${apiBaseUrl.value.replace(/'/g, "'\\''")}/chat/completions' \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"$MODEL_ID\",
    \"messages\": [
      {\"role\": \"user\", \"content\": \"Hello!\"}
    ]
  }"`
}))
const codeExample = computed(() => codeExamples.value[codeLanguage.value])
interface CodeToken {
  type: string
  text: string
}
function tokenizeCode(source: string, language: (typeof codeLanguages)[number]): CodeToken[] {
  const pattern = language === 'python'
    ? /"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\b(?:import|from)\b|\b(?:OpenAI|create|print)\b|\b(?:base_url|api_key|model|messages|os|environ)\b|\b\d+\b/g
    : /'(?:[^']*)'|"(?:\\.|[^"\\])*"|\bcurl\b|(?<=\s)-[Hd]\b|\\$/gm
  const tokens: CodeToken[] = []
  let lastIndex = 0
  for (const match of source.matchAll(pattern)) {
    const matchIndex = match.index ?? 0
    if (matchIndex > lastIndex) tokens.push({ type: '', text: source.slice(lastIndex, matchIndex) })
    const value = match[0]
    let type = ''
    if (value.startsWith('"') || value.startsWith("'")) type = 'string'
    else if (value === 'import' || value === 'from' || value === 'curl') type = 'keyword'
    else if (value.startsWith('-')) type = 'flag'
    else if (value === '\\') type = 'punct'
    else if (language === 'python' && (value === 'OpenAI' || value === 'create' || value === 'print')) type = 'func'
    else if (language === 'python' && /^\d+$/.test(value)) type = 'num'
    else if (language === 'python') type = 'var'
    tokens.push({ type, text: value })
    lastIndex = matchIndex + value.length
  }
  if (lastIndex < source.length) tokens.push({ type: '', text: source.slice(lastIndex) })
  return tokens
}
const highlightedCodeExamples = computed(() => ({
  python: tokenizeCode(codeExamples.value.python, 'python'),
  curl: tokenizeCode(codeExamples.value.curl, 'curl'),
}))
const copiedTarget = ref('')
const copyStatus = ref('')
let copyTimer: ReturnType<typeof setTimeout> | undefined
let sectionObserver: IntersectionObserver | undefined
let scrollFrame = 0
let scrollProgressEnabled = false
function syncScrollState() {
  scrollFrame = 0
  const scrollY = window.scrollY || document.documentElement.scrollTop || 0
  isScrolled.value = scrollY > 18
  if (!scrollProgressEnabled || !progressBar.value) return
  const maxScroll = document.documentElement.scrollHeight - window.innerHeight
  const ratio = maxScroll > 0 ? Math.min(1, Math.max(0, scrollY / maxScroll)) : 0
  progressBar.value.style.setProperty('--rl-progress', String(ratio))
}
function onWindowScroll() {
  if (scrollFrame) return
  scrollFrame = window.requestAnimationFrame(syncScrollState)
}
onMounted(() => {
  const supportsCssTimeline = typeof CSS !== 'undefined' && typeof CSS.supports === 'function' && CSS.supports('animation-timeline', 'scroll(root block)')
  const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
  scrollProgressEnabled = !supportsCssTimeline && !reducedMotion
  syncScrollState()
  window.addEventListener('scroll', onWindowScroll, { passive: true })
  if (typeof IntersectionObserver !== 'undefined') {
    const visibleSections = new Map<string, number>()
    sectionObserver = new IntersectionObserver(entries => {
      for (const entry of entries) {
        const id = entry.target.id
        if (!id) continue
        if (entry.isIntersecting) visibleSections.set(id, entry.intersectionRatio)
        else visibleSections.delete(id)
      }
      if (visibleSections.size === 0) {
        if ((window.scrollY || 0) < 260) activeSection.value = ''
        return
      }
      const orderedIds = ['capabilities', 'quota', 'connection', 'questions']
      const firstVisible = orderedIds.find(id => visibleSections.has(id))
      if (firstVisible) activeSection.value = firstVisible
    }, { rootMargin: '-22% 0px -55% 0px', threshold: [0, 0.15, 0.4] })
    for (const id of ['capabilities', 'quota', 'connection', 'questions']) {
      const sectionEl = document.getElementById(id)
      if (sectionEl) sectionObserver.observe(sectionEl)
    }
    if (heroSection.value) sectionObserver.observe(heroSection.value)
  }
})
async function copyValue(value: string, target: 'hero-address' | 'address' | 'code') {
  copyStatus.value = ''
  const trigger = document.activeElement as HTMLElement | null
  const message = t(target === 'code' ? 'glass.home.codeCopied' : 'glass.home.addressCopied')
  const success = await copyToClipboard(value, message)
  if (trigger?.isConnected) trigger.focus({ preventScroll: true })
  copiedTarget.value = success ? target : ''
  copyStatus.value = success ? message : t('glass.home.copyFailed')
  if (copyTimer) clearTimeout(copyTimer)
  copyTimer = setTimeout(() => { copiedTarget.value = ''; copyStatus.value = '' }, 3000)
}
onBeforeUnmount(() => {
  if (copyTimer) clearTimeout(copyTimer)
  document.removeEventListener('pointerdown', handleOutsidePointer)
  window.removeEventListener('scroll', onWindowScroll)
  if (scrollFrame) window.cancelAnimationFrame(scrollFrame)
  sectionObserver?.disconnect()
})
const accountDescription = computed(() => t(!appStore.publicSettingsLoaded ? 'glass.home.steps.accountLoading' : !registrationEnabled.value ? 'glass.home.steps.accountClosed' : invitationRequired.value ? 'glass.home.steps.accountInvite' : 'glass.home.steps.accountOpen'))
const steps = computed(() => [
  { id: 'account', title: t('glass.home.steps.accountTitle'), desc: accountDescription.value, to: entryPath.value, action: t(isAuthenticated.value ? 'glass.nav.console' : 'glass.home.steps.accountAction') },
  { id: 'model', title: t('glass.home.steps.modelTitle'), desc: t('glass.home.steps.modelDescription'), to: modelPlazaEnabled.value ? modelPlazaPath.value : entryPath.value, action: modelPlazaEnabled.value ? modelPlazaLinkLabel.value : t('glass.home.steps.modelAction') },
  { id: 'key', title: t('glass.home.steps.keyTitle'), desc: t('glass.home.steps.keyDescription'), to: protectedPath('/keys'), action: t('glass.home.steps.keyAction') },
  { id: 'connect', title: t('glass.home.steps.connectTitle'), desc: t('glass.home.steps.connectDescription'), to: protectedPath('/usage'), action: t('glass.home.steps.usageAction') },
])
const faqs = computed(() => [
  ...['account', 'official', 'quota', 'tools'].map(id => ({ id, q: t(`referenceLanding.faq.${id}Q`), a: t(`referenceLanding.faq.${id}A`) })),
  { id: 'invite', q: t('glass.home.faq.inviteQ'), a: t(!appStore.publicSettingsLoaded ? 'glass.home.faq.registrationLoading' : !registrationEnabled.value ? 'glass.home.faq.registrationClosed' : invitationRequired.value ? 'glass.home.faq.inviteRequired' : 'glass.home.faq.inviteNotRequired') },
  { id: 'errors', q: t('glass.home.faq.errorsQ'), a: t('glass.home.faq.errorsA') },
])
</script>
