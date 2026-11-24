import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, RouterLinkStub } from '@vue/test-utils'

import HomeView from '../HomeView.vue'

const { appStore, authStore, copyToClipboard, translate } = vi.hoisted(() => ({
  copyToClipboard: vi.fn(),
  translate: vi.fn((key: string) => key),
  appStore: {
    cachedPublicSettings: {} as Record<string, unknown>,
    siteName: 'Fallback site',
    siteLogo: '',
    docUrl: '',
    publicSettingsLoaded: true,
    fetchPublicSettings: vi.fn(),
  },
  authStore: {
    isAuthenticated: false,
    isAdmin: false,
    user: null as { email?: string } | null,
    checkAuth: vi.fn(),
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => appStore,
  useAuthStore: () => authStore,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard }),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: translate }),
  }
})

enableAutoUnmount(afterEach)

function mountHome(settings: Record<string, unknown> = {}) {
  appStore.cachedPublicSettings = {
    site_name: 'Test site',
    site_subtitle: 'Test subtitle',
    ...settings,
  }

  return mount(HomeView, {
    global: {
      stubs: {
        RouterLink: RouterLinkStub,
        LocaleSwitcher: { template: '<div data-testid="locale-switcher" />' },
        PublicLocaleSwitcher: { template: '<div data-testid="public-locale-switcher" />' },
        PublicBackdrop: { template: '<div />' },
        SpatialPortalCanvas: { template: '<div data-testid="spatial-canvas" />' },
        Icon: { template: '<span data-testid="icon" />' },
      },
    },
  })
}

function compactDestination(wrapper: ReturnType<typeof mountHome>) {
  return wrapper.get('[data-testid="compact-home"]').findComponent(RouterLinkStub).props('to')
}

function modelPlazaDestination(wrapper: ReturnType<typeof mountHome>) {
  return wrapper
    .findAllComponents(RouterLinkStub)
    .find((link) => link.props('to') === '/model-plaza')
    ?.props('to')
}

describe('HomeView compact mode', () => {
  beforeEach(() => {
    authStore.isAuthenticated = false
    authStore.isAdmin = false
    authStore.user = null
    authStore.checkAuth.mockClear()
    appStore.fetchPublicSettings.mockClear()
    appStore.publicSettingsLoaded = true
    translate.mockReset().mockImplementation((key: string) => key)
    copyToClipboard.mockReset().mockResolvedValue(true)
    localStorage.clear()
    vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: false } as MediaQueryList)
  })

  it('renders custom HTML ahead of compact mode', () => {
    const wrapper = mountHome({
      compact_home_enabled: true,
      home_content: '<section id="custom-home">Custom home</section>',
    })

    expect(wrapper.get('#custom-home').text()).toBe('Custom home')
    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
  })

  it('renders custom URL content ahead of compact mode', () => {
    const wrapper = mountHome({
      compact_home_enabled: true,
      home_content: ' https://example.com/home ',
    })

    expect(wrapper.get('iframe').attributes('src')).toBe('https://example.com/home')
    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
  })

  it('treats whitespace-only custom content as empty and selects compact mode', () => {
    const wrapper = mountHome({ compact_home_enabled: true, home_content: ' \n\t ' })

    expect(wrapper.get('[data-testid="compact-home"]').text()).toContain('Test site')
  })

  it.each([undefined, false])('selects the default home when compact mode is %s', (enabled) => {
    const settings = enabled === undefined ? {} : { compact_home_enabled: enabled }
    const wrapper = mountHome(settings)

    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
    expect(wrapper.find('main#home-main').exists()).toBe(true)
  })

  it('links unauthenticated visitors to login', () => {
    expect(compactDestination(mountHome({ compact_home_enabled: true }))).toBe('/login')
  })

  it('links authenticated users to their dashboard', () => {
    authStore.isAuthenticated = true

    expect(compactDestination(mountHome({ compact_home_enabled: true }))).toBe('/dashboard')
  })

  it('links administrators to the admin dashboard', () => {
    authStore.isAuthenticated = true
    authStore.isAdmin = true

    const wrapper = mountHome({ compact_home_enabled: true })
    expect(compactDestination(wrapper)).toBe('/admin/dashboard')
    expect(authStore.checkAuth).toHaveBeenCalledOnce()
    expect(appStore.fetchPublicSettings).not.toHaveBeenCalled()
  })

  it('shows the model plaza link to anonymous visitors when public access is enabled', () => {
    const wrapper = mountHome({
      compact_home_enabled: true,
      model_plaza_enabled: true,
      model_plaza_require_auth: false,
    })

    expect(modelPlazaDestination(wrapper)).toBe('/model-plaza')
  })

  it('hides the model plaza link from anonymous visitors when sign-in is required', () => {
    const wrapper = mountHome({
      compact_home_enabled: true,
      model_plaza_enabled: true,
      model_plaza_require_auth: true,
    })

    expect(modelPlazaDestination(wrapper)).toBeUndefined()
  })

  it('shows the model plaza link to authenticated visitors when sign-in is required', () => {
    authStore.isAuthenticated = true

    const wrapper = mountHome({
      compact_home_enabled: true,
      model_plaza_enabled: true,
      model_plaza_require_auth: true,
    })

    expect(modelPlazaDestination(wrapper)).toBe('/model-plaza')
  })

  it('shows the model plaza link in the default home guide', () => {
    const wrapper = mountHome({
      model_plaza_enabled: true,
      model_plaza_require_auth: false,
    })

    expect(modelPlazaDestination(wrapper)).toBe('/model-plaza')
  })

  it.each([
    [false, false, '/login'],
    [true, false, '/dashboard'],
    [true, true, '/admin/dashboard'],
  ])('routes the AICode portal to the correct console for authenticated=%s admin=%s', async (authenticated, admin, destination) => {
    authStore.isAuthenticated = authenticated
    authStore.isAdmin = admin
    const wrapper = mountHome()
    await flushPromises()
    const aicodePortal = wrapper.get('[data-testid="portal-aicode"]')

    expect(aicodePortal.findComponent(RouterLinkStub).props('to')).toBe(destination)
    expect(wrapper.get('[data-testid="portal-qimu"] a').attributes('href')).toBe('https://qimu.code1024.cc/')
    wrapper.unmount()
  })

  it('uses admin-configured canvas link for the Qimu portal when present', async () => {
    const wrapper = mountHome({
      custom_topbar_links: [
        {
          id: 'canvas',
          label: '启幕影业画布',
          url: 'https://qimu.example.com/studio',
          icon: 'palette',
          visibility: 'all',
          sort_order: 0,
        },
      ],
    })
    await flushPromises()

    const qimuAnchor = wrapper.get('[data-testid="portal-qimu"] a')
    expect(qimuAnchor.attributes('href')).toBe('https://qimu.example.com/studio')
    expect(qimuAnchor.text()).toContain('启幕影业画布')
  })

  it('defaults to Dark Cinema Studio on system theme and toggles cleanly to light and dark', async () => {
    const wrapper = mountHome()
    await flushPromises()

    const shell = wrapper.get('.rl-theme-shell')
    expect(shell.classes()).toContain('dark')

    const toggle = wrapper.get('[data-testid="theme-toggle"]')
    await toggle.trigger('click')
    expect(localStorage.getItem('theme')).toBe('light')
    expect(shell.classes()).not.toContain('dark')

    await toggle.trigger('click')
    expect(localStorage.getItem('theme')).toBe('dark')
    expect(shell.classes()).toContain('dark')
  })

  it('hides the model plaza link when the feature is disabled', () => {
    const wrapper = mountHome({
      compact_home_enabled: true,
      model_plaza_enabled: false,
      model_plaza_require_auth: false,
    })

    expect(modelPlazaDestination(wrapper)).toBeUndefined()
  })

  it('retains the protected model destination on the default landing when sign-in is required', async () => {
    const wrapper = mountHome({ model_plaza_enabled: true, model_plaza_require_auth: true })
    await flushPromises()
    const links = wrapper.findAllComponents(RouterLinkStub)
    expect(links.some(link => link.props('to') === '/login?redirect=%2Fmodel-plaza')).toBe(true)
    expect(wrapper.text()).toContain('glass.home.modelLoginAction')
  })

  it.each([
    ['https://api.example.com', 'https://api.example.com'],
    ['https://api.example.com/v1/', 'https://api.example.com/v1'],
    ['', window.location.origin],
  ])('normalizes and copies the configured API root %s', async (configured, expected) => {
    const wrapper = mountHome({ api_base_url: configured })
    await flushPromises()
    document.body.appendChild(wrapper.element)

    const button = wrapper.get<HTMLButtonElement>('[data-testid="portal-aicode"] button[aria-label="referenceLanding.portal.copyApi"]')
    button.element.focus()
    await button.trigger('click')
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith(expected, 'glass.home.addressCopied')
    expect(wrapper.get('[role="status"]').text()).toBe('glass.home.addressCopied')
    expect(button.attributes('aria-label')).toBe('glass.home.copied')
    expect(document.activeElement).toBe(button.element)
  })

  it('announces a clipboard failure without claiming a successful copy', async () => {
    copyToClipboard.mockResolvedValue(false)
    const wrapper = mountHome()
    await flushPromises()

    const button = wrapper.get('[data-testid="portal-aicode"] button[aria-label="referenceLanding.portal.copyApi"]')
    await button.trigger('click')
    await flushPromises()

    expect(wrapper.get('[role="status"]').text()).toBe('glass.home.copyFailed')
    expect(button.attributes('aria-label')).toBe('referenceLanding.portal.copyApi')
    expect(wrapper.find('button[aria-label="glass.home.copied"]').exists()).toBe(false)
  })

  it('closes the mobile navigation with Escape or outside click and retains a return-to-top target', async () => {
    const wrapper = mountHome()
    await flushPromises()
    document.body.appendChild(wrapper.element)

    const trigger = wrapper.get<HTMLButtonElement>('[aria-controls="home-mobile-menu"]')
    await trigger.trigger('click')
    expect(trigger.attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('#home-mobile-menu').findAll('a').length).toBeGreaterThanOrEqual(5)

    await wrapper.get('header').trigger('keydown', { key: 'Escape' })
    expect(trigger.attributes('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(trigger.element)

    await trigger.trigger('click')
    expect(wrapper.find('#home-mobile-menu').exists()).toBe(true)
    const outside = document.createElement('div')
    document.body.appendChild(outside)
    outside.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await flushPromises()
    expect(wrapper.find('#home-mobile-menu').exists()).toBe(false)
    outside.remove()

    const backTop = wrapper.get('footer a[href="#home-main"]')
    expect(wrapper.get(backTop.attributes('href')).element.tagName).toBe('MAIN')
    wrapper.unmount()
  })

  it('keeps contact links concise and limited to the supplied support email', async () => {
    const wrapper = mountHome({ contact_info: 'support@example.test' })
    await flushPromises()
    const contacts = wrapper.findAll('a[href^="mailto:"]')
    expect(contacts.length).toBeGreaterThan(0)
    expect(contacts.every(link => link.attributes('href') === 'mailto:support@example.test')).toBe(true)
    expect(wrapper.find('a[href*="qq.com"], a[href^="tencent:"]').exists()).toBe(false)
  })

  it('shows the model plaza link directly in the top navigation when enabled', async () => {
    const wrapper = mountHome({ model_plaza_enabled: true, model_plaza_require_auth: false })
    await flushPromises()
    const navLink = wrapper.get('[data-testid="nav-model-plaza"]')
    expect(navLink.findComponent(RouterLinkStub).exists() || navLink.element.tagName === 'A').toBe(true)
    expect(wrapper.findAllComponents(RouterLinkStub).some(link => link.attributes('data-testid') === 'nav-model-plaza' && link.props('to') === '/model-plaza')).toBe(true)
  })
})
