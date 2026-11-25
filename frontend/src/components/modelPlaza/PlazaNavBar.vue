<template>
  <header
    class="sticky top-0 z-30 border-b border-[var(--glass-line)] bg-[var(--glass-thick)] backdrop-blur-xl"
  >
    <div class="mx-auto flex max-w-7xl items-center justify-between gap-4 px-4 py-3.5 sm:px-6">
      <RouterLink
        to="/"
        class="flex min-w-0 items-center gap-3 rounded-xl transition-opacity hover:opacity-85 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500"
        :aria-label="siteName"
      >
        <template v-if="settings">
          <span
            class="flex h-9 w-9 flex-shrink-0 items-center justify-center overflow-hidden rounded-xl bg-white shadow-sm ring-1 ring-gray-200 dark:bg-dark-800 dark:ring-dark-700"
          >
            <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
          </span>
          <span class="truncate text-base font-semibold text-gray-950 dark:text-white">
            {{ siteName }}
          </span>
        </template>
        <template v-else>
          <span class="h-9 w-9 flex-shrink-0 animate-pulse rounded-xl bg-gray-200 dark:bg-dark-700" aria-hidden="true"></span>
          <span class="h-5 w-28 animate-pulse rounded bg-gray-200 dark:bg-dark-700" aria-hidden="true"></span>
        </template>
      </RouterLink>

      <div class="flex flex-shrink-0 items-center gap-2">
        <PublicLocaleSwitcher />
        <button
          type="button"
          class="glass-button plaza-nav-icon flex-shrink-0"
          :aria-label="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          @click="toggleTheme"
        >
          <Icon :name="isDark ? 'sun' : 'moon'" size="md" aria-hidden="true" />
        </button>
        <RouterLink
          v-if="isAuthenticated"
          :to="backTarget"
          class="glass-button glass-button--primary flex-shrink-0"
        >
          {{ t('modelPlaza.nav.backToDashboard') }}
        </RouterLink>
        <RouterLink
          v-else
          :to="{ path: '/home', query: { auth: 'login', redirect: '/model-plaza' } }"
          class="glass-button glass-button--primary flex-shrink-0"
        >
          {{ t('modelPlaza.nav.login') }}
        </RouterLink>
      </div>
    </div>
  </header>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import PublicLocaleSwitcher from '@/components/public/PublicLocaleSwitcher.vue'
import { useTheme } from '@/composables/useTheme'
import { sanitizeUrl } from '@/utils/url'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const { isDark, toggleTheme } = useTheme()
const appStore = useAppStore()
const authStore = useAuthStore()

const settings = computed(() => appStore.cachedPublicSettings)
const siteName = computed(() => settings.value?.site_name || 'Sub2API')
const siteLogo = computed(() =>
  sanitizeUrl(settings.value?.site_logo || '', { allowRelative: true, allowDataUrl: true })
)
const isAuthenticated = computed(() => authStore.isAuthenticated)
const backTarget = computed(() => (authStore.isAdmin ? '/admin/dashboard' : '/dashboard'))
</script>

<style scoped>
.plaza-nav-icon {
  width: 2.75rem;
  min-height: 2.75rem;
  padding: 0;
}
</style>
