import { mount, type VueWrapper } from '@vue/test-utils'
import { computed, nextTick, ref } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import CommandPalette from '../CommandPalette.vue'

const pushMock = vi.fn().mockResolvedValue(undefined)
const toggleThemeMock = vi.fn()
const toggleSidebarMock = vi.fn()
const handleMenuItemClickMock = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: pushMock }),
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: ref('zh') }),
}))
vi.mock('@/i18n', () => ({
  availableLocales: [
    { code: 'zh', name: '中文', flag: '🇨🇳' },
    { code: 'en', name: 'English', flag: '🇺🇸' },
  ],
  setLocale: vi.fn().mockResolvedValue(undefined),
}))
vi.mock('@/stores', () => ({
  useAppStore: () => ({ sidebarCollapsed: false, toggleSidebar: toggleSidebarMock }),
  useAuthStore: () => ({ isSimpleMode: false, user: { role: 'user' } }),
  useOnboardingStore: () => ({ replay: vi.fn() }),
}))
vi.mock('@/composables/useTheme', () => ({
  useTheme: () => ({ isDark: ref(false), toggleTheme: toggleThemeMock }),
}))
vi.mock('../useShellNav', () => ({
  useShellNav: () => ({
    sections: computed(() => [
      {
        key: 'core',
        title: 'Core',
        items: [
          { path: '/dashboard', label: 'Dashboard', icon: 'grid' },
          { path: '/keys', label: 'API Keys', icon: 'key' },
        ],
      },
    ]),
    handleMenuItemClick: handleMenuItemClickMock,
  }),
}))

describe('CommandPalette', () => {
  let wrapper: VueWrapper | undefined

  afterEach(() => {
    wrapper?.unmount()
    document.body.innerHTML = ''
    document.body.classList.remove('modal-open')
    vi.clearAllMocks()
  })

  it('toggles via Cmd/Ctrl+K and closes on Escape', async () => {
    wrapper = mount(CommandPalette, {
      attachTo: document.body,
      props: { open: false },
      global: { stubs: { Icon: true } },
    })

    const openEvent = new KeyboardEvent('keydown', { key: 'k', metaKey: true, cancelable: true })
    document.dispatchEvent(openEvent)
    expect(openEvent.defaultPrevented).toBe(true)
    expect(wrapper.emitted('update:open')?.[0]).toEqual([true])

    await wrapper.setProps({ open: true })
    await nextTick()
    expect(document.body.querySelector('input.cmd-input')).not.toBeNull()

    const escEvent = new KeyboardEvent('keydown', { key: 'Escape', cancelable: true })
    document.dispatchEvent(escEvent)
    expect(wrapper.emitted('update:open')?.[1]).toEqual([false])
  })

  it('filters pages and navigates on Enter, or runs quick actions', async () => {
    wrapper = mount(CommandPalette, {
      attachTo: document.body,
      props: { open: true },
      global: { stubs: { Icon: true } },
    })
    await nextTick()

    const input = document.body.querySelector<HTMLInputElement>('input.cmd-input')!
    input.value = 'keys'
    input.dispatchEvent(new Event('input'))
    await nextTick()

    const options = document.body.querySelectorAll('[role="option"]')
    expect(options).toHaveLength(1)
    expect(options[0]?.textContent).toContain('API Keys')

    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', cancelable: true }))
    await nextTick()
    expect(pushMock).toHaveBeenCalledWith('/keys')
    expect(handleMenuItemClickMock).toHaveBeenCalledWith('/keys')

    await wrapper.setProps({ open: false })
    await wrapper.setProps({ open: true })
    await nextTick()

    const input2 = document.body.querySelector<HTMLInputElement>('input.cmd-input')!
    input2.value = 'theme'
    input2.dispatchEvent(new Event('input'))
    await nextTick()
    input2.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', cancelable: true }))
    await nextTick()
    expect(toggleThemeMock).toHaveBeenCalledTimes(1)
  })
})
