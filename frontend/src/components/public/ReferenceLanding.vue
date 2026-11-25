<template>
  <div ref="landingRoot" class="rl-theme-shell dark">
    <div class="public-glass reference-landing" :class="{ 'is-reduced-motion': reducedMotion }">
      <SpatialPortalCanvas
        :progress="scrollProgress"
        :hover-side="hoverSide"
        :dark="true"
        :reduced-motion="reducedMotion"
        :aria-label="t('referenceLanding.portal.canvasLabel')"
      />

      <div class="sp-film-grain" aria-hidden="true" />
      <div class="sp-arch-guides" aria-hidden="true" />
      <div class="rl-reading-progress" :style="{ '--rl-progress': String(scrollProgress) }" aria-hidden="true" />
      <a class="rl-skip" href="#home-main">{{ t('glass.nav.skip') }}</a>

      <header ref="headerRef" class="rl-header hp__top" :class="{ 'rl-header--scrolled': isScrolled }" @keydown.esc="closeMenu">
        <nav class="rl-nav rl-container" :aria-label="t('glass.nav.navigation')">
          <router-link to="/" class="rl-brand" @click="scrollToAct('act-portal', $event)">
            <PublicBrandLogo :src="siteLogo || '/logo.svg'" />
            <span>{{ siteName }}</span>
          </router-link>

          <div class="rl-nav-links">
            <a
              v-for="act in acts"
              :key="act.id"
              :href="`#${act.id}`"
              :class="{ 'is-active': activeAct === act.key }"
              :aria-current="activeAct === act.key ? 'location' : undefined"
              @click="scrollToAct(act.id, $event)"
            >
              {{ act.label }}
            </a>
            <router-link
              v-if="modelPlazaEnabled"
              :to="modelPlazaPath"
              class="rl-nav-plaza"
              data-testid="nav-model-plaza"
              @click.capture="handleProtectedPlazaClick($event)"
            >
              <Icon name="grid" size="sm" aria-hidden="true" />
              <span>{{ t('nav.modelPlaza') }}</span>
            </router-link>
          </div>

          <div class="rl-nav-actions">
            <a
              v-if="docUrl"
              :href="docUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="glass-button rl-icon-button rl-nav-docs"
              :aria-label="t('glass.nav.docs')"
              :title="t('glass.nav.docs')"
            >
              <Icon name="book" size="md" aria-hidden="true" />
            </a>
            <PublicLocaleSwitcher />
            <router-link
              :to="entryPath"
              class="glass-button rl-button rl-nav-login"
              @click.capture="handleAuthEntryClick($event)"
            >
              {{ t(isAuthenticated ? 'glass.nav.console' : 'glass.nav.login') }}
            </router-link>
            <button
              ref="menuButton"
              class="glass-button rl-icon-button rl-menu-toggle"
              type="button"
              :aria-expanded="menuOpen"
              aria-controls="home-mobile-menu"
              :aria-label="t(menuOpen ? 'glass.nav.closeMenu' : 'glass.nav.openMenu')"
              @click="menuOpen = !menuOpen"
            >
              <Icon :name="menuOpen ? 'x' : 'menu'" size="md" aria-hidden="true" />
            </button>
          </div>
        </nav>

        <nav
          v-if="menuOpen"
          id="home-mobile-menu"
          class="glass-panel rl-mobile-nav rl-container"
          :aria-label="t('glass.nav.navigation')"
          @click="closeMenu"
        >
          <a
            v-for="act in acts"
            :key="act.id"
            :href="`#${act.id}`"
            :class="{ 'is-active': activeAct === act.key }"
            :aria-current="activeAct === act.key ? 'location' : undefined"
            @click="scrollToAct(act.id, $event)"
          >
            <span>{{ act.label }}</span>
            <Icon name="arrowRight" size="sm" aria-hidden="true" />
          </a>
          <router-link
            v-if="modelPlazaEnabled"
            :to="modelPlazaPath"
            class="rl-mobile-plaza"
            data-testid="mobile-model-plaza"
            @click.capture="handleProtectedPlazaClick($event)"
          >
            <span>{{ t('nav.modelPlaza') }}</span>
            <Icon name="arrowRight" size="sm" aria-hidden="true" />
          </router-link>
          <a :href="qimuUrl" target="_blank" rel="noopener noreferrer">
            <span>{{ qimuLabel }}</span>
            <Icon name="externalLink" size="sm" aria-hidden="true" />
          </a>
          <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer">
            <span>{{ t('glass.nav.docs') }}</span>
            <Icon name="externalLink" size="sm" aria-hidden="true" />
          </a>
          <div class="rl-mobile-language">
            <span>{{ t('glass.nav.language') }}</span>
            <PublicLocaleSwitcher />
          </div>
        </nav>
      </header>

      <!-- Vertical 4-Act Spatial Rail -->
      <aside class="sp-rail" :aria-label="t('referenceLanding.portal.railLabel')">
        <div class="sp-rail-track" aria-hidden="true">
          <span class="sp-rail-fill" :style="{ transform: `scaleY(${scrollProgress})` }" />
        </div>
        <button
          v-for="act in acts"
          :key="act.id"
          type="button"
          class="sp-rail-dot"
          :class="{ 'is-active': activeAct === act.key }"
          :aria-label="act.label"
          :aria-current="activeAct === act.key ? 'step' : undefined"
          @click="scrollToAct(act.id)"
        >
          <span class="sp-rail-tooltip">{{ act.label }}</span>
        </button>
      </aside>

      <main id="home-main" ref="trackRef" class="sp-main">
        <!-- Invisible scroll landmarks matching 3D camera curve stops -->
        <div class="sp-anchors" aria-hidden="true">
          <div id="act-portal" class="sp-anchor sp-anchor--portal" />
          <div id="act-aicode" class="sp-anchor sp-anchor--aicode" />
          <div id="act-qimu" class="sp-anchor sp-anchor--qimu" />
          <div id="act-finale" class="sp-anchor sp-anchor--finale" />
        </div>

        <div class="sp-sticky-hud">
          <!-- ACT 1: Macro Luminous Eclipse Hero (keeps upper 65% completely clear) -->
          <section
            class="sp-act sp-act--portal"
            :class="{ 'is-active': activeAct === 'portal' }"
            :aria-hidden="activeAct !== 'portal'"
            aria-labelledby="home-title"
          >
            <div class="sp-portal-lower rl-container">
              <div class="sp-hero-brand">
                <p class="rl-sr-only">{{ t('referenceLanding.portal.tagline') }}</p>
                <span class="sp-kicker">
                  <span class="sp-kicker-index">01 / 04</span>
                  <span class="sp-kicker-sep" aria-hidden="true">·</span>
                  <span class="sp-kicker-label">{{ t('referenceLanding.portal.tagline') }}</span>
                </span>
                <h1 id="home-title">{{ siteName }}</h1>
                <p class="sp-hero-subtitle">{{ t('referenceLanding.portal.heroSubtitle') }}</p>
              </div>

              <div class="sp-hero-cta-row">
                <router-link
                  :to="entryPath"
                  class="glass-button sp-cta-aicode sp-cta-primary"
                  data-testid="hero-primary-cta"
                  @click.capture="handleAuthEntryClick($event)"
                >
                  <span class="sp-roll">
                    <span class="sp-roll-main">{{ t(isAuthenticated ? 'referenceLanding.portal.consoleAction' : 'referenceLanding.portal.primaryAction') }}</span>
                    <span class="sp-roll-hover" aria-hidden="true">{{ t(isAuthenticated ? 'referenceLanding.portal.consoleAction' : 'referenceLanding.portal.primaryAction') }}</span>
                  </span>
                  <Icon name="arrowRight" size="sm" aria-hidden="true" />
                </router-link>

                <router-link
                  v-if="modelPlazaEnabled"
                  :to="modelPlazaPath"
                  class="glass-button sp-cta-secondary"
                  data-testid="hero-secondary-cta"
                  @click.capture="handleProtectedPlazaClick($event)"
                >
                  <Icon name="grid" size="sm" aria-hidden="true" />
                  <span>{{ modelPlazaLinkLabel }}</span>
                </router-link>
                <button
                  v-else
                  type="button"
                  class="glass-button sp-cta-secondary"
                  data-testid="hero-secondary-cta"
                  @click="scrollToAct('act-aicode')"
                >
                  <Icon name="sparkles" size="sm" aria-hidden="true" />
                  <span>{{ t('referenceLanding.portal.modeAiCode') }}</span>
                </button>
              </div>

              <div
                ref="modeDockRef"
                class="sp-mode-dock"
                data-testid="platform-mode-switcher"
                @pointermove="onModeDockPointerMove"
                @pointerleave="onModeDockPointerLeave"
              >
                <div
                  class="sp-mode-tabs"
                  role="tablist"
                  :aria-label="t('referenceLanding.portal.tagline')"
                  @pointerleave="resetHeroPreview"
                >
                  <button
                    type="button"
                    role="tab"
                    class="sp-mode-tab"
                    :class="{ 'is-active': heroMode === 'core' }"
                    :aria-selected="heroMode === 'core'"
                    data-testid="mode-core"
                    @click="setHeroMode('core')"
                    @pointerenter="previewHeroMode('core')"
                  >
                    <span>{{ t('referenceLanding.portal.modeCore') }}</span>
                  </button>
                  <button
                    type="button"
                    role="tab"
                    class="sp-mode-tab sp-mode-tab--aicode"
                    :class="{ 'is-active': heroMode === 'aicode' }"
                    :aria-selected="heroMode === 'aicode'"
                    data-testid="mode-aicode"
                    @click="setHeroMode('aicode')"
                    @pointerenter="previewHeroMode('aicode')"
                  >
                    <span class="sp-dot sp-dot--aicode" aria-hidden="true" />
                    <span>{{ t('referenceLanding.portal.modeAiCode') }}</span>
                  </button>
                  <button
                    type="button"
                    role="tab"
                    class="sp-mode-tab sp-mode-tab--qimu"
                    :class="{ 'is-active': heroMode === 'qimu' }"
                    :aria-selected="heroMode === 'qimu'"
                    data-testid="mode-qimu"
                    @click="setHeroMode('qimu')"
                    @pointerenter="previewHeroMode('qimu')"
                  >
                    <span class="sp-dot sp-dot--qimu" aria-hidden="true" />
                    <span>{{ t('referenceLanding.portal.modeQimu') }}</span>
                  </button>
                </div>

                <div class="sp-mode-panel" role="tabpanel">
                  <template v-if="heroMode === 'core'">
                    <span class="sp-mode-summary">{{ t('referenceLanding.portal.coreSummary') }}</span>
                    <div class="sp-mode-quick">
                      <button type="button" class="sp-mode-jump" @click="scrollToAct('act-aicode')">
                        <span class="sp-dot sp-dot--aicode" aria-hidden="true" />
                        <span>{{ t('referenceLanding.portal.aicodeTitle') }}</span>
                      </button>
                      <button type="button" class="sp-mode-jump" @click="scrollToAct('act-qimu')">
                        <span class="sp-dot sp-dot--qimu" aria-hidden="true" />
                        <span>{{ t('referenceLanding.portal.qimuTitle') }}</span>
                      </button>
                    </div>
                  </template>
                  <template v-else-if="heroMode === 'aicode'">
                    <span class="sp-mode-summary">
                      <strong>{{ t('referenceLanding.portal.aicodeTitle') }}</strong>
                      <span aria-hidden="true"> · </span>
                      {{ t('referenceLanding.portal.aicodeTag') }}
                    </span>
                    <div class="sp-mode-actions">
                      <router-link
                        :to="entryPath"
                        class="glass-button sp-mode-action"
                        @click.capture="handleAuthEntryClick($event)"
                      >
                        <span>{{ t(isAuthenticated ? 'referenceLanding.portal.consoleAction' : 'referenceLanding.portal.aicodeAction') }}</span>
                        <Icon name="arrowRight" size="sm" aria-hidden="true" />
                      </router-link>
                      <button
                        type="button"
                        class="glass-button rl-icon-button sp-copy-btn"
                        :class="{ 'is-copied': copiedTarget === 'hero-address' }"
                        :aria-label="t(copiedTarget === 'hero-address' ? 'glass.home.copied' : 'referenceLanding.portal.copyApi')"
                        :title="apiBaseUrl"
                        @click="copyValue(apiBaseUrl, 'hero-address')"
                      >
                        <Icon :name="copiedTarget === 'hero-address' ? 'check' : 'copy'" size="md" aria-hidden="true" />
                      </button>
                    </div>
                  </template>
                  <template v-else>
                    <span class="sp-mode-summary">
                      <strong>{{ t('referenceLanding.portal.qimuTitle') }}</strong>
                      <span aria-hidden="true"> · </span>
                      {{ t('referenceLanding.portal.qimuTag') }}
                    </span>
                    <div class="sp-mode-actions">
                      <a
                        :href="qimuUrl"
                        target="_blank"
                        rel="noopener noreferrer"
                        class="glass-button sp-mode-action sp-mode-action--qimu"
                      >
                        <span>{{ qimuLabel }}</span>
                        <Icon name="externalLink" size="sm" aria-hidden="true" />
                      </a>
                    </div>
                  </template>
                </div>
              </div>

              <button
                type="button"
                class="sp-scroll-cue"
                @click="scrollToAct('act-aicode')"
              >
                <span>{{ t('referenceLanding.portal.scrollHint') }}</span>
                <Icon name="chevronDown" size="sm" aria-hidden="true" />
              </button>
            </div>
          </section>

          <!-- ACT 2: AICode 3D Quantum Compute & Multi-Modal Core (35%) -->
          <section
            class="sp-act sp-act--stage sp-act--aicode"
            :class="{ 'is-active': activeAct === 'aicode' }"
            :aria-hidden="activeAct !== 'aicode'"
          >
            <div class="rl-container sp-stage-bar sp-stage-bar--left">
              <div
                class="sp-stage-dock"
                data-testid="portal-aicode"
                @pointerenter="hoverSide = 'aicode'"
                @pointerleave="resetHeroPreview"
              >
                <div class="sp-dock-head">
                  <span class="sp-dot sp-dot--aicode" aria-hidden="true" />
                  <div>
                    <h2>{{ t('referenceLanding.portal.aicodeTitle') }}</h2>
                    <span class="sp-dock-tags">{{ t('referenceLanding.portal.aicodeTag') }}</span>
                  </div>
                  <span class="sp-stage-index">02 / 04</span>
                </div>
                <div class="sp-dock-actions">
                  <router-link
                    :to="entryPath"
                    class="glass-button sp-cta-aicode"
                    @click.capture="handleAuthEntryClick($event)"
                  >
                    <span class="sp-roll">
                      <span class="sp-roll-main">{{ t(isAuthenticated ? 'referenceLanding.portal.consoleAction' : 'referenceLanding.portal.aicodeAction') }}</span>
                      <span class="sp-roll-hover" aria-hidden="true">{{ t(isAuthenticated ? 'referenceLanding.portal.consoleAction' : 'referenceLanding.portal.aicodeAction') }}</span>
                    </span>
                    <Icon name="arrowRight" size="sm" aria-hidden="true" />
                  </router-link>
                  <router-link
                    v-if="modelPlazaEnabled"
                    :to="modelPlazaPath"
                    class="glass-button"
                    @click.capture="handleProtectedPlazaClick($event)"
                  >
                    <Icon name="grid" size="sm" aria-hidden="true" />
                    <span>{{ modelPlazaLinkLabel }}</span>
                  </router-link>
                  <button
                    type="button"
                    class="glass-button rl-icon-button sp-copy-btn"
                    :class="{ 'is-copied': copiedTarget === 'stage-address' }"
                    :aria-label="t(copiedTarget === 'stage-address' ? 'glass.home.copied' : 'referenceLanding.portal.copyApi')"
                    :title="apiBaseUrl"
                    @click="copyValue(apiBaseUrl, 'stage-address')"
                  >
                    <Icon :name="copiedTarget === 'stage-address' ? 'check' : 'copy'" size="md" aria-hidden="true" />
                  </button>
                </div>
              </div>
            </div>
          </section>

          <!-- ACT 3: Qimu Cinema, Manju & Poster Studio (72%) -->
          <section
            class="sp-act sp-act--stage sp-act--qimu"
            :class="{ 'is-active': activeAct === 'qimu' }"
            :aria-hidden="activeAct !== 'qimu'"
          >
            <div class="rl-container sp-stage-bar sp-stage-bar--right">
              <div
                class="sp-stage-dock"
                data-testid="portal-qimu"
                @pointerenter="hoverSide = 'qimu'"
                @pointerleave="resetHeroPreview"
              >
                <div class="sp-dock-head">
                  <span class="sp-dot sp-dot--qimu" aria-hidden="true" />
                  <div>
                    <h2>{{ t('referenceLanding.portal.qimuTitle') }}</h2>
                    <span class="sp-dock-tags">{{ t('referenceLanding.portal.qimuTag') }}</span>
                  </div>
                  <span class="sp-stage-index">03 / 04</span>
                </div>
                <div class="sp-dock-actions">
                  <a
                    :href="qimuUrl"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="glass-button sp-cta-qimu"
                  >
                    <span class="sp-roll">
                      <span class="sp-roll-main">{{ qimuLabel }}</span>
                      <span class="sp-roll-hover" aria-hidden="true">{{ qimuLabel }}</span>
                    </span>
                    <Icon name="externalLink" size="sm" aria-hidden="true" />
                  </a>
                </div>
              </div>
            </div>
          </section>

          <!-- ACT 4: Dual-Core Panorama Finale & Minimal Footer (100%) -->
          <section
            class="sp-act sp-act--finale"
            :class="{ 'is-active': activeAct === 'finale' }"
            :aria-hidden="activeAct !== 'finale'"
          >
            <h2 class="sp-finale-title">{{ t('referenceLanding.portal.finaleTitle') }}</h2>
            <div class="sp-finale-portals">
              <router-link
                :to="entryPath"
                class="glass-button rl-button sp-cta-aicode"
                @click.capture="handleAuthEntryClick($event)"
              >
                <span class="sp-roll">
                  <span class="sp-roll-main">{{ t(isAuthenticated ? 'referenceLanding.portal.consoleAction' : 'referenceLanding.portal.primaryAction') }}</span>
                  <span class="sp-roll-hover" aria-hidden="true">{{ t(isAuthenticated ? 'referenceLanding.portal.consoleAction' : 'referenceLanding.portal.primaryAction') }}</span>
                </span>
                <Icon name="arrowRight" size="md" aria-hidden="true" />
              </router-link>
              <a
                :href="qimuUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="glass-button rl-button sp-cta-qimu"
              >
                <span class="sp-roll">
                  <span class="sp-roll-main">{{ qimuLabel }}</span>
                  <span class="sp-roll-hover" aria-hidden="true">{{ qimuLabel }}</span>
                </span>
                <Icon name="externalLink" size="md" aria-hidden="true" />
              </a>
            </div>

            <footer class="rl-footer-bar rl-container">
              <div class="rl-footer-brand">
                <PublicBrandLogo :src="siteLogo || '/logo.svg'" />
                <span>&copy; {{ currentYear }} {{ siteName }}</span>
              </div>
              <nav class="rl-footer-links" :aria-label="t('glass.home.footerExplore')">
                <router-link
                  v-if="modelPlazaEnabled"
                  :to="modelPlazaPath"
                  @click.capture="handleProtectedPlazaClick($event)"
                >
                  {{ t('nav.modelPlaza') }}
                </router-link>
                <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer">{{ t('glass.nav.docs') }}</a>
                <a
                  v-for="link in ecosystemLinks"
                  :key="link.id"
                  :href="link.url"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {{ link.label }}
                </a>
                <a v-if="supportEmail" :href="`mailto:${supportEmail}`">{{ supportContact }}</a>
                <span v-else-if="supportContact" class="rl-footer-contact-text">{{ supportContact }}</span>
              </nav>
              <a href="#home-main" class="rl-back-top" @click="scrollToAct('act-portal', $event)">
                <span>{{ t('glass.nav.backTop') }}</span>
                <Icon name="arrowUp" size="sm" aria-hidden="true" />
              </a>
            </footer>
          </section>
        </div>
      </main>

      <p class="rl-sr-only" role="status" aria-live="polite" aria-atomic="true">{{ copyStatus }}</p>

      <AuthPortalModal
        v-if="authModalOpen"
        :open="authModalOpen"
        :mode="authModalMode"
        :redirect-to="authModalRedirect"
        :site-name="siteName"
        :site-logo="siteLogo"
        @close="closeAuthModal"
        @update:mode="authModalMode = $event"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore, useAuthStore } from '@/stores'
import Icon from '@/components/icons/Icon.vue'
import PublicBrandLogo from '@/components/common/PublicBrandLogo.vue'
import PublicLocaleSwitcher from '@/components/public/PublicLocaleSwitcher.vue'
import type { PortalHoverSide } from '@/components/public/three/createPortalEngine'
import type { AuthModalMode } from '@/components/public/AuthPortalModal.vue'
import { useClipboard } from '@/composables/useClipboard'
import { sanitizeUrl } from '@/utils/url'
import { resolveVisibleTopbarLinks } from '@/utils/topbarLinks'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import '@/styles/reference-landing.css'

const SpatialPortalCanvas = defineAsyncComponent(
  () => import('@/components/public/SpatialPortalCanvas.vue'),
)
const AuthPortalModal = defineAsyncComponent(
  () => import('@/components/public/AuthPortalModal.vue'),
)

const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const authModalOpen = ref(false)
const authModalMode = ref<AuthModalMode>('login')
const authModalRedirect = ref('')

function openAuthModal(mode: AuthModalMode = 'login', redirectTo = '') {
  authModalMode.value = mode
  authModalRedirect.value = redirectTo
  authModalOpen.value = true
}

function closeAuthModal() {
  authModalOpen.value = false
}

function isPlainLeftClick(event: MouseEvent): boolean {
  return !(event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0)
}

function handleAuthEntryClick(event: MouseEvent, redirectTo = '') {
  if (isAuthenticated.value || !isPlainLeftClick(event)) return
  event.preventDefault()
  menuOpen.value = false
  openAuthModal('login', redirectTo)
}

function handleProtectedPlazaClick(event: MouseEvent) {
  if (!modelPlazaRequiresAuth.value || isAuthenticated.value || !isPlainLeftClick(event)) return
  event.preventDefault()
  menuOpen.value = false
  openAuthModal('login', '/model-plaza')
}

const landingRoot = ref<HTMLElement | null>(null)
const headerRef = ref<HTMLElement | null>(null)
const trackRef = ref<HTMLElement | null>(null)
const modeDockRef = ref<HTMLElement | null>(null)

type HeroMode = 'core' | 'aicode' | 'qimu'
const heroMode = ref<HeroMode>('core')

function setHeroMode(mode: HeroMode) {
  heroMode.value = mode
  hoverSide.value = mode === 'core' ? null : mode
}

function previewHeroMode(mode: HeroMode) {
  hoverSide.value = mode === 'core' ? null : mode
}

function resetHeroPreview() {
  hoverSide.value = heroMode.value === 'core' ? null : heroMode.value
}

function onModeDockPointerMove(event: PointerEvent) {
  const el = modeDockRef.value
  if (!el || reducedMotion.value) return
  const rect = el.getBoundingClientRect()
  if (rect.width <= 0 || rect.height <= 0) return
  const x = Math.min(100, Math.max(0, ((event.clientX - rect.left) / rect.width) * 100))
  const y = Math.min(100, Math.max(0, ((event.clientY - rect.top) / rect.height) * 100))
  el.style.setProperty('--sp-mx', `${x.toFixed(1)}%`)
  el.style.setProperty('--sp-my', `${y.toFixed(1)}%`)
}

function onModeDockPointerLeave() {
  const el = modeDockRef.value
  if (!el) return
  el.style.setProperty('--sp-mx', '50%')
  el.style.setProperty('--sp-my', '0%')
}

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Code1024')
const siteLogo = computed(() =>
  sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', {
    allowRelative: true,
    allowDataUrl: true,
  }),
)
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const supportContact = computed(() => appStore.cachedPublicSettings?.contact_info?.trim() || appStore.contactInfo?.trim() || '')
const supportEmail = computed(() =>
  /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(supportContact.value) ? supportContact.value : '',
)

const customLinks = computed(() =>
  resolveVisibleTopbarLinks({
    publicLinks: appStore.cachedPublicSettings?.custom_topbar_links,
    isAdmin: authStore.isAdmin,
    isAuthenticated: authStore.isAuthenticated,
  }),
)
const canvasLink = computed(() =>
  customLinks.value.find((link) => link.id === 'canvas' || /qimu/i.test(link.url)),
)
const qimuUrl = computed(() => canvasLink.value?.url || 'https://qimu.code1024.cc/')
const qimuLabel = computed(() => canvasLink.value?.label || t('referenceLanding.portal.qimuAction'))
const ecosystemLinks = computed(() => customLinks.value.slice(0, 5))

const isAuthenticated = computed(() => authStore.isAuthenticated)
const entryPath = computed(() =>
  isAuthenticated.value ? (authStore.isAdmin ? '/admin/dashboard' : '/dashboard') : '/login',
)
const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))
const modelPlazaRequiresAuth = computed(() => appStore.cachedPublicSettings?.model_plaza_require_auth === true)
const modelPlazaPath = computed(() =>
  modelPlazaRequiresAuth.value ? protectedPath('/model-plaza') : '/model-plaza',
)
const modelPlazaLinkLabel = computed(() =>
  t(modelPlazaRequiresAuth.value && !isAuthenticated.value ? 'glass.home.modelLoginAction' : 'glass.home.modelsAction'),
)

const apiBaseUrl = computed(() => {
  const configured = sanitizeUrl(appStore.cachedPublicSettings?.api_base_url?.trim() || appStore.apiBaseUrl?.trim() || '')
  return (configured || window.location.origin).replace(/\/+$/, '')
})
const currentYear = new Date().getFullYear()

function protectedPath(path: string) {
  return isAuthenticated.value ? path : `/login?redirect=${encodeURIComponent(path)}`
}

type ActKey = 'portal' | 'aicode' | 'qimu' | 'finale'
type ActId = 'act-portal' | 'act-aicode' | 'act-qimu' | 'act-finale'

const acts = computed<Array<{ key: ActKey; id: ActId; progress: number; label: string }>>(() => [
  { key: 'portal', id: 'act-portal', progress: 0, label: t('referenceLanding.portal.acts.overview') },
  { key: 'aicode', id: 'act-aicode', progress: 0.35, label: t('referenceLanding.portal.acts.aicode') },
  { key: 'qimu', id: 'act-qimu', progress: 0.72, label: t('referenceLanding.portal.acts.qimu') },
  { key: 'finale', id: 'act-finale', progress: 1, label: t('referenceLanding.portal.acts.panorama') },
])

const scrollProgress = ref(0)
const isScrolled = ref(false)
const hoverSide = ref<PortalHoverSide>(null)
const reducedMotion = ref(false)

const activeAct = computed<ActKey>(() => {
  const p = scrollProgress.value
  if (p < 0.18) return 'portal'
  if (p < 0.54) return 'aicode'
  if (p < 0.86) return 'qimu'
  return 'finale'
})

function scrollToAct(id: ActId, event?: MouseEvent) {
  if (event && (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0)) return
  event?.preventDefault()
  const targetAct = acts.value.find((a) => a.id === id)
  if (!targetAct) return

  const trackEl = trackRef.value
  const docMax = Math.max(0, document.documentElement.scrollHeight - window.innerHeight)
  const trackMax = trackEl ? Math.max(0, trackEl.offsetHeight - window.innerHeight) : docMax
  const maxScroll = Math.max(docMax, trackMax)
  const targetY = Math.round(targetAct.progress * maxScroll)

  scrollProgress.value = targetAct.progress
  window.scrollTo?.({
    top: targetY,
    behavior: reducedMotion.value ? 'auto' : 'smooth',
  })
}

const menuOpen = ref(false)
const menuButton = ref<HTMLButtonElement | null>(null)
function closeMenu(event?: Event) {
  menuOpen.value = false
  if (event instanceof KeyboardEvent) menuButton.value?.focus()
}
function handleOutsidePointer(event: PointerEvent) {
  if (!menuOpen.value) return
  if (event.target instanceof Node && headerRef.value?.contains(event.target)) return
  menuOpen.value = false
}
watch(menuOpen, (open) => {
  if (open) document.addEventListener('pointerdown', handleOutsidePointer)
  else document.removeEventListener('pointerdown', handleOutsidePointer)
})

const copiedTarget = ref('')
const copyStatus = ref('')
let copyTimer: ReturnType<typeof setTimeout> | undefined

async function copyValue(value: string, target: 'hero-address' | 'stage-address') {
  copyStatus.value = ''
  const trigger = document.activeElement as HTMLElement | null
  const message = t('glass.home.addressCopied')
  const success = await copyToClipboard(value, message)
  if (trigger?.isConnected) trigger.focus({ preventScroll: true })
  copiedTarget.value = success ? target : ''
  copyStatus.value = success ? message : t('glass.home.copyFailed')
  if (copyTimer) clearTimeout(copyTimer)
  copyTimer = setTimeout(() => {
    copiedTarget.value = ''
    copyStatus.value = ''
  }, 3000)
}

let scrollFrame = 0
let motionMedia: MediaQueryList | undefined

function syncScroll() {
  scrollFrame = 0
  const scrollY = window.scrollY || document.documentElement.scrollTop || 0
  isScrolled.value = scrollY > 18
  const maxScroll = document.documentElement.scrollHeight - window.innerHeight
  if (maxScroll > 0) {
    scrollProgress.value = Math.min(1, Math.max(0, scrollY / maxScroll))
  }
}

function onWindowScroll() {
  if (scrollFrame) return
  scrollFrame = window.requestAnimationFrame(syncScroll)
}

function syncMotionPreference() {
  reducedMotion.value = motionMedia?.matches ?? false
}

onMounted(() => {
  if (!isAuthenticated.value && typeof window !== 'undefined') {
    const params = new URLSearchParams(window.location.search)
    const authParam = params.get('auth')
    const rawRedirect = params.get('redirect') || ''
    const safeRedirect = rawRedirect.startsWith('/') && !rawRedirect.startsWith('//') ? rawRedirect : ''
    if (authParam === 'login' || authParam === 'register') {
      openAuthModal(authParam, safeRedirect)
    }
  }
  if (typeof window.matchMedia === 'function') {
    motionMedia = window.matchMedia('(prefers-reduced-motion: reduce)')
    syncMotionPreference()
    motionMedia.addEventListener?.('change', syncMotionPreference)
  }
  syncScroll()
  window.addEventListener('scroll', onWindowScroll, { passive: true })
  window.addEventListener('resize', onWindowScroll, { passive: true })
})

onBeforeUnmount(() => {
  if (copyTimer) clearTimeout(copyTimer)
  document.removeEventListener('pointerdown', handleOutsidePointer)
  window.removeEventListener('scroll', onWindowScroll)
  window.removeEventListener('resize', onWindowScroll)
  motionMedia?.removeEventListener?.('change', syncMotionPreference)
  if (scrollFrame) window.cancelAnimationFrame(scrollFrame)
})
</script>
