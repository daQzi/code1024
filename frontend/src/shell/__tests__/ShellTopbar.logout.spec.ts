import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import ShellTopbar from '../ShellTopbar.vue'

const pushMock = vi.fn().mockResolvedValue(undefined)
const logoutMock = vi.fn().mockResolvedValue(undefined)

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: pushMock }),
  useRoute: () => ({ path: '/dashboard', meta: {} }),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    contactInfo: '',
    docUrl: '',
    cachedPublicSettings: {},
    toggleMobileSidebar: vi.fn(),
  }),
  useAuthStore: () => ({
    user: { id: 1, email: 'demo@example.com', username: 'demo', role: 'user', balance: 0, frozen_balance: 0 },
    isAdmin: false,
    isSimpleMode: false,
    logout: logoutMock,
  }),
  useOnboardingStore: () => ({ replay: vi.fn() }),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    cachedPublicSettings: {},
  }),
}))

vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => ({ loaded: false, customTopbarLinks: [] }),
}))

vi.mock('@/components/common/LocaleSwitcher.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/components/common/SubscriptionProgressMini.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/components/common/AnnouncementBell.vue', () => ({ default: { template: '<div />' } }))

describe('ShellTopbar logout flow', () => {
  it('redirects to homepage /home instead of standalone /login on logout', async () => {
    const wrapper = mount(ShellTopbar, {
      global: {
        stubs: { RouterLink: { template: '<a><slot /></a>' }, Icon: true },
      },
    })

    await wrapper.get('.topbar__user-btn').trigger('click')
    const logoutBtn = wrapper.get('.topbar__menu-item--danger')
    await logoutBtn.trigger('click')
    await flushPromises()

    expect(logoutMock).toHaveBeenCalledOnce()
    expect(pushMock).toHaveBeenCalledWith('/home')
  })
})
