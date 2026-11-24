import { mount, type VueWrapper } from '@vue/test-utils'
import { reactive } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ShellRail from '../ShellRail.vue'

const appState = reactive({
  sidebarCollapsed: true,
  mobileOpen: false,
  sidebarScrollTop: 0,
  siteName: 'Example',
  siteLogo: '',
  siteVersion: '',
  publicSettingsLoaded: true,
  toggleSidebar: vi.fn(),
  setMobileOpen: vi.fn((open: boolean) => {
    appState.mobileOpen = open
  }),
})

vi.mock('vue-router', () => ({ useRoute: () => ({ path: '/dashboard' }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/stores', () => ({ useAppStore: () => appState }))
vi.mock('../useShellNav', () => ({
  useShellNav: () => ({
    sections: [
      {
        key: 'main',
        title: 'Main',
        items: [
          { path: '/dashboard', label: 'Dashboard', icon: 'grid' },
          {
            path: '/admin/group',
            label: 'System',
            icon: 'cog',
            expandOnly: true,
            children: [{ path: '/admin/settings', label: 'Settings', icon: null }],
          },
        ],
      },
    ],
    homePath: '/dashboard',
    isActive: (path: string) => path === '/dashboard',
    isGroupActive: () => false,
    isGroupExpanded: () => false,
    handleGroupClick: vi.fn(),
    handleMenuItemClick: vi.fn(),
  }),
}))
vi.mock('@/components/common/VersionBadge.vue', () => ({ default: { template: '<span />' } }))

describe('ShellRail interactions', () => {
  let wrapper: VueWrapper | undefined

  afterEach(() => {
    wrapper?.unmount()
    document.body.innerHTML = ''
    appState.sidebarCollapsed = true
    appState.mobileOpen = false
    vi.clearAllMocks()
  })

  it('shows floating Liquid Glass tooltip on hover when collapsed', async () => {
    wrapper = mount(ShellRail, {
      attachTo: document.body,
      global: {
        stubs: {
          RouterLink: { template: '<a v-bind="$attrs"><slot /></a>' },
          Icon: true,
          Transition: { inheritAttrs: false, template: '<slot />' },
        },
      },
    })

    const item = wrapper.get('a[aria-label="Dashboard"]')
    await item.trigger('mouseenter')
    expect(document.body.querySelector('[role="tooltip"]')?.textContent?.trim()).toBe('Dashboard')

    await item.trigger('mouseleave')
    expect(document.body.querySelector('[role="tooltip"]')).toBeNull()
  })

  it('closes mobile sidebar drawer on Escape keydown', async () => {
    appState.sidebarCollapsed = false
    appState.mobileOpen = true
    wrapper = mount(ShellRail, {
      attachTo: document.body,
      global: { stubs: { RouterLink: { template: '<a v-bind="$attrs"><slot /></a>' }, Icon: true } },
    })

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(appState.setMobileOpen).toHaveBeenCalledWith(false)
  })
})
