<template>
  <div class="public-glass auth-surface" :data-input="inputMethod" @pointerdown.capture="inputMethod = 'pointer'" @keydown.capture="inputMethod = 'keyboard'">
    <PublicBackdrop />
    <a class="glass-skip" href="#auth-main">{{ t('glass.nav.skip') }}</a>
    <header class="auth-top">
      <nav class="auth-nav glass-panel" :aria-label="t('glass.nav.home')">
        <router-link to="/home" class="auth-brand"><PublicBrandLogo :src="siteLogo || '/logo.svg'" /><span>{{ siteName }}</span></router-link>
        <div class="auth-nav-actions">
          <PublicLocaleSwitcher />
          <button
            type="button"
            class="glass-button auth-theme-toggle"
            :aria-label="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon :name="isDark ? 'sun' : 'moon'" size="md" aria-hidden="true" />
          </button>
          <router-link to="/home" class="auth-back glass-button"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path d="m14 7-5 5 5 5" /></svg>{{ t('glass.auth.backHome') }}</router-link>
        </div>
      </nav>
    </header>
    <main id="auth-main" class="auth-main" tabindex="-1">
      <section class="auth-panel glass-panel">
        <div class="auth-content"><slot /></div>
        <div v-if="$slots.footer" class="auth-footer"><slot name="footer" /></div>
      </section>
    </main>
    <footer class="auth-copyright"><span>&copy; {{ currentYear }} {{ siteName }}</span><a v-if="helpUrl" :href="helpUrl">{{ t('glass.nav.help') }} <span aria-hidden="true">↗</span></a></footer>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import { useTheme } from '@/composables/useTheme'
import { sanitizeUrl } from '@/utils/url'
import Icon from '@/components/icons/Icon.vue'
import PublicBrandLogo from '@/components/common/PublicBrandLogo.vue'
import PublicBackdrop from '@/components/public/PublicBackdrop.vue'
import PublicLocaleSwitcher from '@/components/public/PublicLocaleSwitcher.vue'
import '@/styles/public-glass.css'
const { t } = useI18n()
const { isDark, toggleTheme } = useTheme()
const appStore = useAppStore()
const inputMethod = ref('keyboard')
const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const helpUrl = computed(() => {
  const contact = appStore.cachedPublicSettings?.contact_info?.trim() || appStore.contactInfo?.trim() || ''
  if (/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(contact)) return `mailto:${contact}`
  return sanitizeUrl(appStore.docUrl || '')
})
const currentYear = new Date().getFullYear()
onMounted(() => { if (!appStore.publicSettingsLoaded) void appStore.fetchPublicSettings() })
</script>

<style scoped>
.auth-theme-toggle {
  width: 2.75rem;
  min-height: 2.75rem;
  padding: 0;
  flex-shrink: 0;
}

.auth-panel {
  animation: auth-panel-enter 200ms cubic-bezier(0.16, 1, 0.3, 1);
}

@keyframes auth-panel-enter {
  from {
    opacity: 0;
    transform: translate3d(0, 8px, 0);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .auth-panel {
    animation: none;
  }
}
</style>
