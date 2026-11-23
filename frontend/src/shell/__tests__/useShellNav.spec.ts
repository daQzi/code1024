import { reactive } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useShellNav, type ShellNavItem } from '../useShellNav'

const {
  routeState,
  pushMock,
  appStoreState,
  authStoreState,
  adminSettingsState,
  onboardingState,
} = vi.hoisted(() => ({
  routeState: { path: '/dashboard' },
  pushMock: vi.fn(),
  appStoreState: {
    sidebarCollapsed: false,
    mobileOpen: false,
    cachedPublicSettings: {} as Record<string, unknown>,
    toggleSidebar: vi.fn(),
    setMobileOpen: vi.fn(),
  },
  authStoreState: {
    isAdmin: false,
    isSimpleMode: false,
  },
  adminSettingsState: {
    opsMonitoringEnabled: true,
    paymentEnabled: true,
    customMenuItems: [] as Array<{ id: string; label: string; icon_svg?: string; visibility: string; sort_order: number }>,
    fetch: vi.fn(),
  },
  onboardingState: {
    isCurrentStep: vi.fn(() => false),
    nextStep: vi.fn(),
  },
}))

vi.mock('vue-router', () => ({
  useRoute: () => reactive(routeState),
  useRouter: () => ({ push: pushMock }),
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('@/stores', () => ({
  useAppStore: () => appStoreState,
  useAuthStore: () => authStoreState,
  useAdminSettingsStore: () => adminSettingsState,
  useOnboardingStore: () => onboardingState,
}))
vi.mock('@/utils/featureFlags', () => ({
  FeatureFlags: {
    channelMonitor: 'channelMonitor',
    payment: 'payment',
    availableChannels: 'availableChannels',
    subscription: 'subscription',
    affiliate: 'affiliate',
    riskControl: 'riskControl',
    mcp: 'mcp',
    pluginManagement: 'pluginManagement',
  },
  makeSidebarFlag: () => () => true,
}))
vi.mock('@/utils/siteBillingMode', () => ({
  resolveSiteBillingMode: () => 'both',
}))
vi.mock('@/composables/useBatchImageAccess', () => ({
  useBatchImageAccess: () => ({ canUseBatchImage: { value: true } }),
}))

function flattenPaths(items: ShellNavItem[]): string[] {
  return items.flatMap((item) => [item.path, ...(item.children ? flattenPaths(item.children) : [])])
}

describe('useShellNav', () => {
  beforeEach(() => {
    routeState.path = '/dashboard'
    appStoreState.sidebarCollapsed = false
    appStoreState.mobileOpen = false
    authStoreState.isAdmin = false
    authStoreState.isSimpleMode = false
    pushMock.mockReset()
    appStoreState.toggleSidebar.mockReset()
  })

  it('exposes task logs and plugin developer routes for regular users', () => {
    const nav = useShellNav()
    const paths = nav.sections.value.flatMap((section) => flattenPaths(section.items))
    expect(paths).toContain('/task-logs')
    expect(paths).toContain('/plugin-developer')
  })

  it('exposes admin task logs and expandable plugins submenu for admins', () => {
    authStoreState.isAdmin = true
    routeState.path = '/admin/request-processing'
    const nav = useShellNav()
    const allItems = nav.sections.value.flatMap((section) => section.items)
    const paths = flattenPaths(allItems)

    expect(paths).toContain('/admin/task-logs')
    expect(paths).toContain('/admin/plugins')
    expect(paths).toContain('/admin/request-processing')
    expect(paths).toContain('/admin/native-plugins')

    const pluginsGroup = allItems.find((item) => item.path === '/admin/plugins')!
    expect(pluginsGroup.expandOnly).toBe(true)
    expect(nav.isGroupActive(pluginsGroup)).toBe(true)
    expect(nav.isGroupExpanded(pluginsGroup)).toBe(true)
  })

  it('auto-expands collapsed sidebar and opens group on collapsed group click', () => {
    authStoreState.isAdmin = true
    appStoreState.sidebarCollapsed = true
    appStoreState.mobileOpen = false
    routeState.path = '/admin/dashboard'

    const nav = useShellNav()
    const pluginsGroup = nav.sections.value
      .flatMap((section) => section.items)
      .find((item) => item.path === '/admin/plugins')!

    nav.handleGroupClick(pluginsGroup)

    expect(appStoreState.toggleSidebar).toHaveBeenCalledTimes(1)
    expect(nav.isGroupExpanded(pluginsGroup)).toBe(true)
    expect(pushMock).not.toHaveBeenCalled()
  })
})
