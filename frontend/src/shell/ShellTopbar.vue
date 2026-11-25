<template>
  <header class="topbar">
    <!-- ---------------------------------------------------------- 左 -->
    <div class="topbar__lead">
      <button
        type="button"
        class="topbar__icon-btn topbar__icon-btn--menu"
        :aria-label="t('common.toggleMenu')"
        @click="toggleMobileSidebar"
      >
        <Icon name="menu" size="md" />
      </button>

      <div class="topbar__titles">
        <h1 class="topbar__title">{{ pageTitle }}</h1>
        <p v-if="pageDescription" class="topbar__desc">{{ pageDescription }}</p>
      </div>
    </div>

    <!-- ---------------------------------------------------------- 右 -->
    <div class="topbar__trail">
      <button
        type="button"
        class="topbar__cmd"
        :aria-label="t('commandPalette.triggerLabel')"
        :title="t('commandPalette.triggerLabel')"
        @click="emit('open-command-palette')"
      >
        <Icon name="search" size="sm" aria-hidden="true" />
        <span class="topbar__cmd-text">{{ t('common.search') }}</span>
        <kbd class="topbar__cmd-kbd" aria-hidden="true">{{ shortcutHint }}</kbd>
      </button>

      <AnnouncementBell v-if="user" />

      <!-- 文档 / 模型广场 / 自定义入口：宽屏平铺，窄屏收进“更多”以免只剩一堆裸图标 -->
      <template v-if="!linksCollapsed">
        <a
          v-if="docUrl"
          :href="docUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="topbar__link"
          :aria-label="t('nav.docs')"
          :title="t('nav.docs')"
        >
          <Icon name="book" size="sm" aria-hidden="true" />
          <span class="topbar__link-text">{{ t('nav.docs') }}</span>
        </a>

        <RouterLink
          v-if="user && modelPlazaEnabled"
          :to="{ path: '/model-plaza', query: { embedded: '1' } }"
          class="topbar__link"
          :title="t('nav.modelPlaza')"
        >
          <Icon name="grid" size="sm" aria-hidden="true" />
          <span class="topbar__link-text">{{ t('nav.modelPlaza') }}</span>
        </RouterLink>

        <a
          v-for="link in visibleTopbarLinks"
          :key="link.id"
          :href="link.url"
          target="_blank"
          rel="noopener noreferrer"
          class="topbar__link"
          :title="link.label"
          :aria-label="link.label"
          data-testid="topbar-custom-link"
        >
          <span
            v-if="link.iconSvg"
            class="topbar__link-svg"
            aria-hidden="true"
            v-html="link.iconSvg"
          />
          <Icon v-else :name="link.iconName" size="sm" aria-hidden="true" />
          <span class="topbar__link-text">{{ link.label }}</span>
        </a>
      </template>

      <div v-else-if="topbarLinks.length" ref="linksMenuRef" class="topbar__links-more">
        <button
          type="button"
          class="topbar__link topbar__link--icon"
          :aria-label="t('common.moreLinks')"
          :title="t('common.moreLinks')"
          :aria-expanded="linksMenuOpen"
          aria-haspopup="menu"
          data-testid="topbar-more-links"
          @click="linksMenuOpen = !linksMenuOpen"
        >
          <Icon name="externalLink" size="sm" aria-hidden="true" />
        </button>

        <Transition name="topbar-pop">
          <div v-if="linksMenuOpen" class="topbar__menu topbar__links-menu" role="menu">
            <template v-for="link in topbarLinks" :key="link.key">
              <RouterLink
                v-if="link.to"
                :to="link.to"
                class="topbar__menu-item"
                role="menuitem"
                @click="linksMenuOpen = false"
              >
                <Icon :name="asIconName(link.iconName)" size="sm" aria-hidden="true" />
                {{ link.label }}
              </RouterLink>
              <a
                v-else
                :href="link.url"
                target="_blank"
                rel="noopener noreferrer"
                class="topbar__menu-item"
                role="menuitem"
              >
                <Icon :name="asIconName(link.iconName)" size="sm" aria-hidden="true" />
                {{ link.label }}
              </a>
            </template>
          </div>
        </Transition>
      </div>

      <LocaleSwitcher />

      <SubscriptionProgressMini v-if="user && subscriptionFeatureEnabled" />

      <!-- 余额胶囊：点击/回车展开明细，触屏与键盘都能用 -->
      <div v-if="user" class="topbar__balance">
        <button
          type="button"
          class="topbar__balance-trigger"
          :aria-expanded="balanceOpen"
          :aria-label="`${balanceAvailableText} ${formatHeaderMoney(availableBalance)}`"
          @click="balanceOpen = !balanceOpen"
        >
          <span class="topbar__balance-dot" aria-hidden="true" />
          <span class="topbar__balance-value ui-num">{{ formatHeaderMoney(availableBalance) }}</span>
          <span v-if="frozenBalance > 0" class="topbar__balance-frozen">
            {{ balanceFrozenLabel }}
          </span>
        </button>

        <div class="topbar__balance-pop" :class="{ 'is-open': balanceOpen }">
          <div class="topbar__pop-row">
            <span>{{ balanceAvailableText }}</span>
            <b class="ui-num">{{ formatHeaderMoney(availableBalance) }}</b>
          </div>
          <div class="topbar__pop-row">
            <span>{{ balanceFrozenText }}</span>
            <b class="ui-num topbar__pop-warn">{{ formatHeaderMoney(frozenBalance) }}</b>
          </div>
          <div class="topbar__pop-row topbar__pop-row--total">
            <span>{{ balanceTotalText }}</span>
            <b class="ui-num">{{ formatHeaderMoney(totalBalance) }}</b>
          </div>
        </div>
      </div>

      <!-- 用户菜单 -->
      <div v-if="user" ref="dropdownRef" class="topbar__user">
        <button
          type="button"
          class="topbar__user-btn"
          :aria-label="t('common.userMenu')"
          @click="toggleDropdown"
        >
          <span class="topbar__avatar">
            <img v-if="avatarUrl" :src="avatarUrl" :alt="displayName" />
            <span v-else>{{ userInitials }}</span>
          </span>
          <span class="topbar__user-meta">
            <span class="topbar__user-name">{{ displayName }}</span>
            <span class="topbar__user-role">{{ t('admin.users.roles.' + user.role) }}</span>
          </span>
          <span class="topbar__user-chevron" :class="{ 'is-open': dropdownOpen }">
            <Icon name="chevronDown" size="sm" />
          </span>
        </button>

        <Transition name="topbar-pop">
          <div v-if="dropdownOpen" class="topbar__menu">
            <div class="topbar__menu-head">
              <span class="topbar__menu-name">{{ displayName }}</span>
              <span class="topbar__menu-mail">{{ user.email }}</span>
              <div class="topbar__menu-balance">
                <span class="ui-text-faint">{{ t('common.balance') }}</span>
                <span class="ui-num topbar__menu-balance-v">
                  {{ formatHeaderMoney(availableBalance) }}
                </span>
              </div>
            </div>

            <div class="topbar__menu-group">
              <RouterLink to="/profile" class="topbar__menu-item" @click="closeDropdown">
                <Icon name="user" size="sm" />
                {{ t('nav.profile') }}
              </RouterLink>
              <RouterLink to="/keys" class="topbar__menu-item" @click="closeDropdown">
                <Icon name="key" size="sm" />
                {{ t('nav.apiKeys') }}
              </RouterLink>
              <a
                v-if="authStore.isAdmin"
                href="https://github.com/daQzi/code1024"
                target="_blank"
                rel="noopener noreferrer"
                class="topbar__menu-item"
                @click="closeDropdown"
              >
                <Icon name="terminal" size="sm" />
                {{ t('nav.github') }}
              </a>
            </div>

            <div v-if="contactInfo" class="topbar__menu-contact">
              <Icon name="chatBubble" size="sm" />
              <span>{{ t('common.contactSupport') }}</span>
              <b>{{ contactInfo }}</b>
            </div>

            <div v-if="showOnboardingButton" class="topbar__menu-group">
              <button type="button" class="topbar__menu-item" @click="handleReplayGuide">
                <Icon name="lightbulb" size="sm" />
                {{ $t('onboarding.restartTour') }}
              </button>
            </div>

            <div class="topbar__menu-group">
              <button
                type="button"
                class="topbar__menu-item topbar__menu-item--danger"
                @click="handleLogout"
              >
                <Icon name="login" size="sm" />
                {{ t('nav.logout') }}
              </button>
            </div>
          </div>
        </Transition>
      </div>
    </div>
  </header>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useMediaQuery } from '@vueuse/core'
import { useAppStore, useAuthStore, useOnboardingStore } from '@/stores'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import SubscriptionProgressMini from '@/components/common/SubscriptionProgressMini.vue'
import AnnouncementBell from '@/components/common/AnnouncementBell.vue'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeUrl } from '@/utils/url'
import { resolveVisibleTopbarLinks } from '@/utils/topbarLinks'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { resolveRouteMetaKeys } from '@/router/title'
import { resolveSiteBillingMode } from '@/utils/siteBillingMode'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()
const emit = defineEmits<{
  (e: 'open-command-palette'): void
}>()
const appStore = useAppStore()

const shortcutHint = computed(() => {
  if (typeof navigator === 'undefined') return '⌘K'
  return /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent) ? '⌘K' : 'Ctrl K'
})
const authStore = useAuthStore()
const adminSettingsStore = useAdminSettingsStore()
const onboardingStore = useOnboardingStore()

const user = computed(() => authStore.user)
const dropdownOpen = ref(false)
const dropdownRef = ref<HTMLElement | null>(null)
const linksMenuOpen = ref(false)
const linksMenuRef = ref<HTMLElement | null>(null)
const balanceOpen = ref(false)

/**
 * 1280 以下顶栏会挤到「一排裸图标」，此时把文档 / 模型广场 / 自定义入口
 * 收进一个「更多」菜单，只保留搜索、公告、语言、余额、头像。
 */
const linksCollapsed = useMediaQuery('(max-width: 1279px)')

const contactInfo = computed(() => appStore.contactInfo)
const docUrl = computed(() => sanitizeUrl(appStore.docUrl))
const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))
const visibleTopbarLinks = computed(() =>
  resolveVisibleTopbarLinks({
    publicLinks: appStore.cachedPublicSettings?.custom_topbar_links,
    adminLinks: adminSettingsStore.customTopbarLinks,
    adminLoaded: adminSettingsStore.loaded,
    isAdmin: authStore.isAdmin,
    isAuthenticated: Boolean(user.value),
  })
)
const avatarUrl = computed(() => user.value?.avatar_url?.trim() || '')

type TopbarEntry = {
  key: string
  label: string
  iconName: string
  to?: string
  url?: string
}

/** Icon.vue 的 name 是字面量联合类型，而入口数据是字符串，这里受控放宽。 */
function asIconName(name: string) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return (name || 'externalLink') as any
}

/** 折叠进「更多」菜单的全部入口，顺序与平铺时一致。 */
const topbarLinks = computed<TopbarEntry[]>(() => {
  const entries: TopbarEntry[] = []
  if (docUrl.value) {
    entries.push({ key: 'docs', label: t('nav.docs'), iconName: 'book', url: docUrl.value })
  }
  if (user.value && modelPlazaEnabled.value) {
    entries.push({
      key: 'plaza',
      label: t('nav.modelPlaza'),
      iconName: 'grid',
      to: '/model-plaza',
    })
  }
  for (const link of visibleTopbarLinks.value) {
    entries.push({
      key: `custom:${link.id}`,
      label: link.label,
      iconName: link.iconName,
      url: link.url,
    })
  }
  return entries
})

const availableBalance = computed(() => Number(user.value?.balance || 0))
const frozenBalance = computed(() => Number(user.value?.frozen_balance || 0))
const totalBalance = computed(() => availableBalance.value + frozenBalance.value)

const balanceAvailableText = computed(() =>
  t('common.availableBalance') === 'common.availableBalance'
    ? '可用余额'
    : t('common.availableBalance')
)
const balanceFrozenText = computed(() =>
  t('common.frozenBalance') === 'common.frozenBalance' ? '冻结金额' : t('common.frozenBalance')
)
const balanceTotalText = computed(() =>
  t('common.totalBalance') === 'common.totalBalance' ? '总余额' : t('common.totalBalance')
)
const balanceFrozenLabel = computed(
  () => `${balanceFrozenText.value} ${formatHeaderMoney(frozenBalance.value)}`
)

const showOnboardingButton = computed(
  () => !authStore.isSimpleMode && user.value?.role === 'admin'
)

const userInitials = computed(() => {
  if (!user.value) return ''
  if (user.value.username) return user.value.username.substring(0, 2).toUpperCase()
  if (user.value.email) {
    return user.value.email.split('@')[0].substring(0, 2).toUpperCase()
  }
  return ''
})

const displayName = computed(() => {
  if (!user.value) return ''
  return user.value.username || user.value.email?.split('@')[0] || ''
})

const subscriptionFeatureEnabled = computed(() =>
  isFeatureFlagEnabled(FeatureFlags.subscription)
)

const routeMetaKeys = computed(() =>
  resolveRouteMetaKeys(route, {
    billingMode: resolveSiteBillingMode(appStore.cachedPublicSettings),
  })
)

const pageTitle = computed(() => {
  if (route.name === 'CustomPage') {
    const id = route.params.id as string
    const publicItems = appStore.cachedPublicSettings?.custom_menu_items ?? []
    const menuItem =
      publicItems.find((item) => item.id === id) ??
      (authStore.isAdmin
        ? adminSettingsStore.customMenuItems.find((item) => item.id === id)
        : undefined)
    if (menuItem?.label) return menuItem.label
  }
  const titleKey = routeMetaKeys.value.titleKey
  if (titleKey) return t(titleKey)
  return (route.meta.title as string) || ''
})

const pageDescription = computed(() => {
  const descKey = routeMetaKeys.value.descriptionKey
  if (descKey) return t(descKey)
  return (route.meta.description as string) || ''
})

function toggleMobileSidebar() {
  appStore.toggleMobileSidebar()
}

function toggleDropdown() {
  dropdownOpen.value = !dropdownOpen.value
  linksMenuOpen.value = false
  balanceOpen.value = false
}

function closeDropdown() {
  dropdownOpen.value = false
}

async function handleLogout() {
  closeDropdown()
  try {
    await authStore.logout()
  } catch (error) {
    console.error('Logout error:', error)
  }
  await router.push('/home')
}

function handleReplayGuide() {
  closeDropdown()
  onboardingStore.replay()
}

function formatHeaderMoney(value: number) {
  if (!Number.isFinite(value)) return '$0.00'
  return `$${value.toFixed(2)}`
}

function handleClickOutside(event: MouseEvent) {
  const target = event.target as Node | null
  if (dropdownRef.value && target && !dropdownRef.value.contains(target)) {
    closeDropdown()
  }
  if (linksMenuRef.value && target && !linksMenuRef.value.contains(target)) {
    linksMenuOpen.value = false
  }
  if (balanceOpen.value && target instanceof Element && !target.closest('.topbar__balance')) {
    balanceOpen.value = false
  }
}

function handleGlobalKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape' || event.defaultPrevented || event.isComposing) return
  if (linksMenuOpen.value) linksMenuOpen.value = false
  if (balanceOpen.value) balanceOpen.value = false
}

// 断点切换后残留的浮层会错位，直接收起
watch(linksCollapsed, (collapsed) => {
  if (!collapsed) linksMenuOpen.value = false
})

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  document.addEventListener('keydown', handleGlobalKeydown)
})
onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
  document.removeEventListener('keydown', handleGlobalKeydown)
})
</script>

<style scoped>
.topbar {
  position: sticky;
  top: 0;
  z-index: 40;
  display: flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--ui-s-3);
  min-height: var(--ui-topbar-h);
  padding: var(--ui-s-2) var(--ui-s-4);
  border-radius: var(--ui-r-xl);
  isolation: isolate;
  font-family: var(--ui-font);

  background-color: rgb(var(--ui-glass-rgb) / calc(var(--ui-glass-alpha) + 0.16));
  -webkit-backdrop-filter: blur(30px) saturate(var(--ui-glass-sat))
    brightness(var(--ui-glass-bright));
  backdrop-filter: blur(30px) saturate(var(--ui-glass-sat)) brightness(var(--ui-glass-bright));
  box-shadow:
    inset 0 1px 1.5px var(--ui-edge-inner),
    inset 0 -1px 1px var(--ui-edge-shade),
    var(--ui-shadow-2);
}

.topbar::before {
  content: '';
  position: absolute;
  inset: 0;
  z-index: -1;
  border-radius: inherit;
  padding: 1px;
  background: linear-gradient(
    150deg,
    var(--ui-edge-strong) 0%,
    var(--ui-edge) 30%,
    transparent 58%,
    var(--ui-edge-shade) 100%
  );
  -webkit-mask:
    linear-gradient(#000 0 0) content-box,
    linear-gradient(#000 0 0);
  mask:
    linear-gradient(#000 0 0) content-box,
    linear-gradient(#000 0 0);
  -webkit-mask-composite: xor;
  mask-composite: exclude;
  pointer-events: none;
}

/* ---------------------------------------------------------------- 左区 */
.topbar__lead {
  display: flex;
  align-items: center;
  gap: var(--ui-s-3);
  min-width: 0;
}

.topbar__titles {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.topbar__title {
  margin: 0;
  font-size: var(--ui-fs-lg);
  font-weight: 660;
  letter-spacing: -0.02em;
  line-height: 1.2;
  color: var(--ui-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.topbar__desc {
  margin: 0;
  font-size: var(--ui-fs-2xs);
  line-height: 1.35;
  color: var(--ui-text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.topbar__icon-btn {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border: 0;
  border-radius: var(--ui-r-md);
  background: transparent;
  color: var(--ui-text-muted);
  cursor: pointer;
  transition:
    color var(--ui-dur-fast) var(--ui-ease-out),
    background-color var(--ui-dur-fast) var(--ui-ease-out);
}
.topbar__icon-btn:hover {
  color: var(--ui-text);
  background-color: var(--ui-line);
}

.topbar__icon-btn--menu {
  display: none;
}

/* ---------------------------------------------------------------- 右区 */
.topbar__trail {
  display: flex;
  align-items: center;
  gap: var(--ui-s-1);
  min-width: 0;
  flex-shrink: 0;
}

.topbar__link {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 10px;
  border-radius: var(--ui-r-md);
  color: var(--ui-text-muted);
  font-size: var(--ui-fs-sm);
  font-weight: 540;
  text-decoration: none;
  white-space: nowrap;
  transition:
    color var(--ui-dur-fast) var(--ui-ease-out),
    background-color var(--ui-dur-fast) var(--ui-ease-out);
}
.topbar__link:hover {
  color: var(--ui-text);
  background-color: var(--ui-line);
}
.topbar__link-svg {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  flex-shrink: 0;
}
.topbar__link-svg :deep(svg) {
  width: 100%;
  height: 100%;
  display: block;
}
.topbar__link-text {
  max-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* “更多入口”折叠按钮与浮层 */
.topbar__links-more {
  position: relative;
  flex-shrink: 0;
}
.topbar__link--icon {
  width: 32px;
  padding: 0;
  justify-content: center;
}
.topbar__links-menu {
  width: 208px;
  max-height: min(60vh, 420px);
}

/*
 * “更多入口”按钮在窄屏被挤到工具栏中部，父容器只有 32px 宽，
 * 冒泡层 right:0 会整块往左溢出到屏幕外。这里改为贴视口右上角定位。
 */
@media (max-width: 1279px) {
  /* 用双类选择器提权：.topbar__menu 定义在后面，同权重会覆盖这里的 fixed */
  .topbar__menu.topbar__links-menu {
    position: fixed;
    top: calc(var(--ui-topbar-h) + var(--ui-s-6) + var(--ui-s-2));
    right: var(--ui-s-3);
    left: auto;
    width: min(260px, calc(100vw - 1.5rem));
  }
}

.topbar__cmd {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 32px;
  padding: 0 8px 0 10px;
  border: 0;
  border-radius: var(--ui-r-md);
  background-color: rgb(var(--ui-glass-soft-rgb) / calc(var(--ui-glass-soft-alpha) + 0.16));
  box-shadow: inset 0 0 0 1px var(--ui-line);
  color: var(--ui-text-muted);
  font-family: var(--ui-font);
  font-size: var(--ui-fs-xs);
  font-weight: 540;
  cursor: pointer;
  white-space: nowrap;
  transition:
    color var(--ui-dur-fast) var(--ui-ease-out),
    background-color var(--ui-dur-fast) var(--ui-ease-out),
    box-shadow var(--ui-dur-fast) var(--ui-ease-out);
}

.topbar__cmd:hover {
  color: var(--ui-text);
  background-color: rgb(var(--ui-glass-strong-rgb) / calc(var(--ui-glass-strong-alpha) + 0.08));
  box-shadow: inset 0 0 0 1px var(--ui-accent-ring);
}

.topbar__cmd-kbd {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 20px;
  padding: 0 5px;
  border-radius: 6px;
  font-family: var(--ui-font-mono);
  font-size: 10px;
  font-weight: 600;
  color: var(--ui-text-faint);
  background-color: var(--ui-line-soft);
  box-shadow: inset 0 0 0 1px var(--ui-line);
}

@media (max-width: 767px) {
  .topbar__cmd {
    width: 32px;
    justify-content: center;
    padding: 0;
  }
  .topbar__cmd-text,
  .topbar__cmd-kbd {
    display: none;
  }
}

/* 余额胶囊 */
.topbar__balance {
  position: relative;
  display: inline-flex;
  height: 32px;
}

.topbar__balance-trigger {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 32px;
  padding: 0 12px;
  border: 0;
  border-radius: var(--ui-r-pill);
  background-color: var(--ui-accent-soft);
  box-shadow:
    inset 0 1px 1px var(--ui-edge-inner),
    0 0 0 1px rgb(63 99 90 / 0.16);
  font-family: var(--ui-font);
  cursor: pointer;
  transition: box-shadow var(--ui-dur-fast) var(--ui-ease-out);
}
.topbar__balance-trigger:hover {
  box-shadow:
    inset 0 1px 1px var(--ui-edge-inner),
    0 0 0 1px var(--ui-accent-ring);
}
.topbar__balance-trigger:focus-visible {
  outline: none;
  box-shadow:
    inset 0 1px 1px var(--ui-edge-inner),
    0 0 0 3px var(--ui-accent-ring);
}

.topbar__balance-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background-image: var(--ui-arc);
  box-shadow: 0 0 8px 0 rgb(34 211 238 / 0.8);
  flex-shrink: 0;
}

.topbar__balance-value {
  font-size: var(--ui-fs-sm);
  font-weight: 680;
  letter-spacing: -0.01em;
  color: var(--ui-accent);
}

.topbar__balance-frozen {
  font-size: 10px;
  font-weight: 600;
  padding: 1px 5px;
  border-radius: var(--ui-r-pill);
  color: var(--ui-warning);
  background-color: var(--ui-warning-soft);
}

.topbar__balance-pop {
  position: absolute;
  right: 0;
  top: calc(100% + 8px);
  z-index: 5;
  width: 208px;
  padding: var(--ui-s-3);
  border-radius: var(--ui-r-lg);
  background-color: rgb(var(--ui-glass-strong-rgb) / calc(var(--ui-glass-strong-alpha) + 0.42));
  -webkit-backdrop-filter: blur(26px) saturate(180%);
  backdrop-filter: blur(26px) saturate(180%);
  box-shadow:
    inset 0 1px 1px var(--ui-edge-strong),
    0 0 0 1px var(--ui-line),
    var(--ui-shadow-3);
  opacity: 0;
  visibility: hidden;
  transform: translateY(-4px);
  transition:
    opacity var(--ui-dur-base) var(--ui-ease-out),
    transform var(--ui-dur-base) var(--ui-ease-out),
    visibility var(--ui-dur-base);
  pointer-events: none;
}

.topbar__balance-pop.is-open,
.topbar__balance:hover .topbar__balance-pop {
  opacity: 1;
  visibility: visible;
  transform: translateY(0);
}

.topbar__pop-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--ui-s-3);
  padding: 4px 0;
  font-size: var(--ui-fs-xs);
  color: var(--ui-text-muted);
}
.topbar__pop-row b {
  font-weight: 640;
  color: var(--ui-text);
}
.topbar__pop-warn {
  color: var(--ui-warning) !important;
}
.topbar__pop-row--total {
  margin-top: 4px;
  padding-top: 8px;
  border-top: 1px solid var(--ui-line);
}

/* 用户 */
.topbar__user {
  position: relative;
}

.topbar__user-btn {
  display: inline-flex;
  align-items: center;
  gap: var(--ui-s-2);
  height: 38px;
  padding: 0 8px 0 4px;
  border: 0;
  border-radius: var(--ui-r-pill);
  background: transparent;
  cursor: pointer;
  transition: background-color var(--ui-dur-fast) var(--ui-ease-out);
}
.topbar__user-btn:hover {
  background-color: var(--ui-line);
}

.topbar__avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  flex-shrink: 0;
  overflow: hidden;
  border-radius: var(--ui-r-md);
  background-image: var(--ui-arc);
  color: #fff;
  font-size: var(--ui-fs-xs);
  font-weight: 680;
  box-shadow:
    inset 0 1px 1px rgb(255 255 255 / 0.4),
    0 4px 12px -8px rgb(63 99 90 / 0.8);
}
.topbar__avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.topbar__user-meta {
  display: none;
  flex-direction: column;
  align-items: flex-start;
  gap: 0;
  min-width: 0;
  text-align: left;
}

.topbar__user-name {
  font-size: var(--ui-fs-xs);
  font-weight: 620;
  color: var(--ui-text);
  line-height: 1.25;
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.topbar__user-role {
  font-size: 10px;
  line-height: 1.25;
  color: var(--ui-text-faint);
}

.topbar__user-chevron {
  display: none;
  color: var(--ui-text-faint);
  transition: transform var(--ui-dur-base) var(--ui-ease-out);
}
.topbar__user-chevron.is-open {
  transform: rotate(180deg);
}

/* 下拉菜单 */
.topbar__menu {
  position: absolute;
  right: 0;
  top: calc(100% + 8px);
  z-index: 6;
  width: 244px;
  max-width: calc(100vw - 2rem);
  max-height: calc(100dvh - var(--ui-topbar-h) - 3rem);
  overflow-y: auto;
  overflow-wrap: anywhere;
  padding: 6px;
  border-radius: var(--ui-r-lg);
  background-color: rgb(var(--ui-glass-strong-rgb) / calc(var(--ui-glass-strong-alpha) + 0.44));
  -webkit-backdrop-filter: blur(30px) saturate(180%);
  backdrop-filter: blur(30px) saturate(180%);
  box-shadow:
    inset 0 1px 1px var(--ui-edge-strong),
    0 0 0 1px var(--ui-line),
    var(--ui-shadow-4);
  transform-origin: top right;
}

.topbar__menu-head {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: 10px 10px 8px;
  border-bottom: 1px solid var(--ui-line);
  margin-bottom: 4px;
}

.topbar__menu-name {
  font-size: var(--ui-fs-sm);
  font-weight: 640;
  color: var(--ui-text);
}

.topbar__menu-mail {
  font-size: var(--ui-fs-2xs);
  color: var(--ui-text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
}

.topbar__menu-balance {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 6px;
  font-size: var(--ui-fs-2xs);
}

.topbar__menu-balance-v {
  font-weight: 680;
  color: var(--ui-accent);
}

.topbar__menu-group {
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding: 3px 0;
}
.topbar__menu-group + .topbar__menu-group {
  border-top: 1px solid var(--ui-line);
}

.topbar__menu-item {
  display: flex;
  align-items: center;
  gap: var(--ui-s-2);
  width: 100%;
  min-height: 32px;
  padding: 0 10px;
  border: 0;
  border-radius: var(--ui-r-sm);
  background: transparent;
  color: var(--ui-text-soft);
  font-family: var(--ui-font);
  font-size: var(--ui-fs-xs);
  font-weight: 540;
  text-align: left;
  text-decoration: none;
  cursor: pointer;
  transition:
    color var(--ui-dur-fast) var(--ui-ease-out),
    background-color var(--ui-dur-fast) var(--ui-ease-out);
}
.topbar__menu-item:hover {
  color: var(--ui-text);
  background-color: var(--ui-line);
}

.topbar__menu-item--danger {
  color: var(--ui-danger);
}
.topbar__menu-item--danger:hover {
  color: var(--ui-danger);
  background-color: var(--ui-danger-soft);
}

.topbar__menu-contact {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  padding: 8px 10px;
  border-top: 1px solid var(--ui-line);
  font-size: 10px;
  color: var(--ui-text-faint);
}
.topbar__menu-contact b {
  color: var(--ui-text-soft);
  font-weight: 600;
}

/* 过渡 */
.topbar-pop-enter-active {
  transition:
    opacity var(--ui-dur-base) var(--ui-ease-out),
    transform var(--ui-dur-base) var(--ui-ease-spring);
}
.topbar-pop-leave-active {
  transition:
    opacity var(--ui-dur-fast) var(--ui-ease-in-out),
    transform var(--ui-dur-fast) var(--ui-ease-in-out);
}
.topbar-pop-enter-from,
.topbar-pop-leave-to {
  opacity: 0;
  transform: scale(0.95) translateY(-6px);
}

/* ---------------------------------------------------------------- 响应式 */
@media (max-width: 1023px) {
  .topbar__icon-btn--menu {
    display: inline-flex;
  }
}

@media (min-width: 1180px) {
  .topbar__user-meta {
    display: flex;
  }
  .topbar__user-chevron {
    display: inline-flex;
  }
}

@media (max-width: 860px) {
  .topbar__balance-trigger {
    padding: 0 9px;
  }
}

@media (max-width: 640px) {
  .topbar {
    padding: var(--ui-s-2) var(--ui-s-3);
  }
  .topbar__desc {
    display: none;
  }
  .topbar__balance-frozen {
    display: none;
  }
  .topbar__lead {
    gap: var(--ui-s-2);
    flex: 1 1 100%;
  }
  .topbar__trail {
    flex: 1 1 auto;
    justify-content: flex-end;
    flex-wrap: wrap;
  }
  .topbar {
    flex-wrap: wrap;
    gap: var(--ui-s-2);
  }
}

@media (prefers-reduced-motion: reduce) {
  .topbar-pop-enter-active,
  .topbar-pop-leave-active,
  .topbar__balance-pop {
    transition: none;
  }
}
</style>
