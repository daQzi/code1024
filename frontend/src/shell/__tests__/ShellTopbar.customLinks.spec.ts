import { mount, type VueWrapper } from '@vue/test-utils'
import { reactive } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { CustomTopbarLink } from '@/types'
import ShellTopbar from '../ShellTopbar.vue'

const appState = reactive<{
  docUrl: string
  contactInfo: string
  cachedPublicSettings: { custom_topbar_links?: CustomTopbarLink[]; custom_menu_items?: [] } | null
  toggleMobileSidebar: ReturnType<typeof vi.fn>
}>({
  docUrl: '',
  contactInfo: '',
  cachedPublicSettings: {
    custom_topbar_links: [],
    custom_menu_items: [],
  },
  toggleMobileSidebar: vi.fn(),
})

const authState = reactive<{
  user: { role: string; username: string; email: string; balance: number; frozen_balance: number } | null
  isAdmin: boolean
  isSimpleMode: boolean
  logout: ReturnType<typeof vi.fn>
}>({
  user: { role: 'user', username: 'alice', email: 'alice@example.com', balance: 10, frozen_balance: 0 },
  isAdmin: false,
  isSimpleMode: false,
  logout: vi.fn(),
})

const adminSettingsState = reactive<{
  loaded: boolean
  customMenuItems: []
  customTopbarLinks: CustomTopbarLink[]
}>({
  loaded: false,
  customMenuItems: [],
  customTopbarLinks: [],
})

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => ({ name: 'Dashboard', meta: { title: 'Dashboard' }, params: {} }),
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('@/stores', () => ({
  useAppStore: () => appState,
  useAuthStore: () => authState,
  useOnboardingStore: () => ({ replay: vi.fn() }),
}))
vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => adminSettingsState,
}))
vi.mock('@/utils/featureFlags', () => ({
  FeatureFlags: { modelPlaza: 'model_plaza_enabled', subscription: 'subscription_enabled' },
  isFeatureFlagEnabled: () => false,
}))
vi.mock('@/router/title', () => ({
  resolveRouteMetaKeys: () => ({ titleKey: 'nav.dashboard', descriptionKey: '' }),
}))
vi.mock('@/utils/siteBillingMode', () => ({
  resolveSiteBillingMode: () => 'recharge_and_subscription',
}))
vi.mock('@/components/common/LocaleSwitcher.vue', () => ({ default: { template: '<span />' } }))
vi.mock('@/components/common/SubscriptionProgressMini.vue', () => ({ default: { template: '<span />' } }))
vi.mock('@/components/common/AnnouncementBell.vue', () => ({ default: { template: '<span />' } }))

describe('ShellTopbar custom links', () => {
  let wrapper: VueWrapper | undefined

  afterEach(() => {
    wrapper?.unmount()
    appState.cachedPublicSettings = { custom_topbar_links: [], custom_menu_items: [] }
    authState.user = { role: 'user', username: 'alice', email: 'alice@example.com', balance: 10, frozen_balance: 0 }
    authState.isAdmin = false
    adminSettingsState.loaded = false
    adminSettingsState.customTopbarLinks = []
    vi.clearAllMocks()
  })

  function mountTopbar() {
    return mount(ShellTopbar, {
      global: {
        mocks: { $t: (key: string) => key },
        stubs: {
          RouterLink: { template: '<a v-bind="$attrs"><slot /></a>' },
          LocaleSwitcher: true,
          SubscriptionProgressMini: true,
          AnnouncementBell: true,
          Transition: { inheritAttrs: false, template: '<slot />' },
          Icon: {
            props: ['name'],
            template: '<i class="stub-icon" :data-icon="name" />',
          },
        },
      },
    })
  }

  it('renders preset icon and sanitized custom SVG links, filtering admin-only and unsafe URLs', () => {
    appState.cachedPublicSettings = {
      custom_topbar_links: [
        {
          id: 'store',
          label: '商店入口',
          url: 'https://ai.code1024.cc/',
          icon: 'shoppingBag',
          icon_svg: '',
          visibility: 'all',
          sort_order: 1,
        },
        {
          id: 'canvas',
          label: '画布入口',
          url: 'https://qimu.code1024.cc/',
          icon: 'palette',
          icon_svg: '<svg onload="alert(1)" viewBox="0 0 24 24"><path d="M1 1h22v22H1z"/></svg>',
          visibility: 'all',
          sort_order: 0,
        },
        {
          id: 'admin-only',
          label: '运维站',
          url: 'https://ops.code1024.cc/',
          icon: 'terminal',
          visibility: 'admin',
          sort_order: 2,
        },
        {
          id: 'bad-url',
          label: 'XSS',
          url: 'javascript:alert(1)',
          icon: 'bolt',
          visibility: 'all',
          sort_order: 3,
        },
      ],
      custom_menu_items: [],
    }

    wrapper = mountTopbar()
    const links = wrapper.findAll('[data-testid="topbar-custom-link"]')
    expect(links).toHaveLength(2)

    expect(links[0].attributes('href')).toBe('https://qimu.code1024.cc/')
    expect(links[0].attributes('target')).toBe('_blank')
    expect(links[0].attributes('rel')).toBe('noopener noreferrer')
    expect(links[0].text()).toContain('画布入口')
    const svgHtml = links[0].find('.topbar__link-svg').html()
    expect(svgHtml).toContain('<svg')
    expect(svgHtml).not.toContain('onload')

    expect(links[1].attributes('href')).toBe('https://ai.code1024.cc/')
    expect(links[1].text()).toContain('商店入口')
    expect(links[1].find('[data-icon="shoppingBag"]').exists()).toBe(true)
  })

  it('uses adminSettingsStore.customTopbarLinks when admin is loaded', () => {
    authState.isAdmin = true
    authState.user = { role: 'admin', username: 'root', email: 'root@example.com', balance: 0, frozen_balance: 0 }
    adminSettingsState.loaded = true
    adminSettingsState.customTopbarLinks = [
      { id: 'user-only', label: '充值站', url: 'https://pay.code1024.cc/', icon: 'wallet', visibility: 'user', sort_order: 0 },
      { id: 'admin-only', label: '运维站', url: 'https://ops.code1024.cc/', icon: 'terminal', visibility: 'admin', sort_order: 1 },
    ]

    wrapper = mountTopbar()
    const links = wrapper.findAll('[data-testid="topbar-custom-link"]')
    expect(links).toHaveLength(1)
    expect(links[0].text()).toContain('运维站')
    expect(links[0].find('[data-icon="terminal"]').exists()).toBe(true)
  })
})
