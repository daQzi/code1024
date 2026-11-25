import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { enableAutoUnmount, flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'

import SpatialPortalCanvas from '../SpatialPortalCanvas.vue'
import ReferenceLanding from '../ReferenceLanding.vue'
import messages from '@/i18n/locales/zh/referenceLanding'

const { appStore, authStore, copyToClipboard } = vi.hoisted(() => ({
  copyToClipboard: vi.fn().mockResolvedValue(true),
  appStore: {
    cachedPublicSettings: {
      site_name: 'Code1024',
      model_plaza_enabled: true,
      model_plaza_require_auth: false,
    } as Record<string, unknown>,
    siteName: 'Code1024',
    siteLogo: '',
    docUrl: 'https://doc.code1024.cc/',
    contactInfo: '',
    apiBaseUrl: 'https://api.code1024.cc',
    publicSettingsLoaded: true,
    fetchPublicSettings: vi.fn(),
  },
  authStore: {
    isAuthenticated: false,
    isAdmin: false,
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

vi.mock('@/api/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/auth')>()
  return {
    ...actual,
    getPublicSettings: vi.fn().mockResolvedValue({
      registration_enabled: true,
      backend_mode_enabled: false,
      email_verify_enabled: false,
      promo_code_enabled: false,
      invitation_code_enabled: false,
      affiliate_enabled: false,
      turnstile_enabled: false,
      linuxdo_oauth_enabled: false,
      github_oauth_enabled: false,
      google_oauth_enabled: false,
      oidc_oauth_enabled: false,
      password_reset_enabled: false,
      passkey_enabled: false,
    }),
  }
})

const i18n = createI18n({
  legacy: false,
  locale: 'zh',
  messages: {
    zh: {
      ...messages,
      nav: { modelPlaza: '模型广场' },
      common: { close: '关闭', optional: '可选' },
      auth: {
        v2: { loginTitle: '欢迎回来' },
        signInToAccount: '登录您的账户以继续',
        signUpToStart: '注册并开始使用 {siteName}',
        signIn: '登录',
        signingIn: '登录中...',
        signUp: '注册',
        createAccount: '创建账户',
        processing: '处理中...',
        continue: '继续',
        emailLabel: '邮箱',
        emailPlaceholder: "you{'@'}example.com",
        passwordLabel: '密码',
        passwordPlaceholder: '输入密码',
        createPasswordPlaceholder: '至少 6 位密码',
        passwordHint: '密码长度至少为 6 个字符',
        confirmPassword: '确认密码',
        confirmPasswordPlaceholder: '再次输入密码',
        dontHaveAccount: '还没有账户？',
        alreadyHaveAccount: '已有账户？',
        forgotPassword: '忘记密码？',
        oauthOrContinue: '或使用以下方式继续',
      },
      glass: {
        nav: {
          skip: '跳到主要内容',
          navigation: '主导航',
          docs: '接入文档',
          light: '切换到浅色外观',
          dark: '切换到深色外观',
          login: '登录',
          console: '控制台',
          openMenu: '打开导航菜单',
          closeMenu: '关闭导航菜单',
          language: '语言',
          backTop: '回到顶部',
        },
        auth: {
          showPassword: '显示密码',
          hidePassword: '隐藏密码',
        },
        home: {
          copied: '已复制',
          addressCopied: '连接地址已复制',
          copyFailed: '复制失败',
          modelsAction: '浏览模型',
          modelLoginAction: '登录查看模型',
          footerExplore: '探索',
        },
      },
    },
  },
})

enableAutoUnmount(afterEach)

describe('SpatialPortalCanvas & ReferenceLanding', () => {
  let reducedMotionMatches = false

  beforeEach(() => {
    reducedMotionMatches = false
    localStorage.clear()
    document.documentElement.classList.remove('dark')
    delete document.documentElement.dataset.theme

    vi.spyOn(window, 'matchMedia').mockImplementation((query: string) => ({
      matches: query.includes('prefers-reduced-motion') ? reducedMotionMatches : false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
    }) as unknown as MediaQueryList)

    vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('gracefully enters CSS fallback mode in SpatialPortalCanvas when WebGL is unavailable', async () => {
    const wrapper = mount(SpatialPortalCanvas, {
      props: {
        progress: 0,
        hoverSide: null,
        dark: true,
        reducedMotion: false,
      },
    })

    await flushPromises()

    expect(wrapper.emitted('fallback')).toBeTruthy()
    expect(wrapper.get('.sp-stage').classes()).toContain('is-fallback')
    expect(wrapper.find('canvas').exists()).toBe(false)
    expect(wrapper.find('.sp-fallback').exists()).toBe(true)
  })

  it('switches acts via chapter navigation and updates aria-current state', async () => {
    const CanvasStub = defineComponent({
      name: 'SpatialPortalCanvas',
      props: ['progress', 'hoverSide', 'dark', 'reducedMotion'],
      template: '<div data-testid="canvas-stub" :data-progress="progress" :data-hover="hoverSide" :data-dark="dark" :data-reduced="reducedMotion" />',
    })

    const wrapper = mount(ReferenceLanding, {
      global: {
        plugins: [i18n],
        stubs: {
          RouterLink: RouterLinkStub,
          SpatialPortalCanvas: CanvasStub,
          PublicLocaleSwitcher: { template: '<div />' },
          Icon: { template: '<span />' },
        },
      },
    })

    await flushPromises()

    // Click topbar chapter link for Qimu (Act 3)
    const qimuNav = wrapper.get('.rl-nav-links a[href="#act-qimu"]')
    await qimuNav.trigger('click')
    await flushPromises()

    expect(qimuNav.attributes('aria-current')).toBe('location')
    expect(wrapper.get('.sp-act--qimu').classes()).toContain('is-active')
    expect(window.scrollTo).toHaveBeenCalled()

    // Click right rail button for AICode (Act 2)
    const railDots = wrapper.findAll('.sp-rail-dot')
    expect(railDots).toHaveLength(4)
    await railDots[1].trigger('click')
    await flushPromises()

    expect(railDots[1].attributes('aria-current')).toBe('step')
    expect(wrapper.get('.sp-act--aicode').classes()).toContain('is-active')
  })

  it('updates hoverSide and unified Act 1 capability switcher modes', async () => {
    const CanvasStub = defineComponent({
      name: 'SpatialPortalCanvas',
      props: ['progress', 'hoverSide', 'dark', 'reducedMotion'],
      template: '<div data-testid="canvas-stub" :data-hover="hoverSide ?? \'\'" />',
    })

    const wrapper = mount(ReferenceLanding, {
      global: {
        plugins: [i18n],
        stubs: {
          RouterLink: RouterLinkStub,
          SpatialPortalCanvas: CanvasStub,
          PublicLocaleSwitcher: { template: '<div />' },
          Icon: { template: '<span />' },
        },
      },
    })

    await flushPromises()

    const canvas = wrapper.get('[data-testid="canvas-stub"]')
    const modeAiCode = wrapper.get('[data-testid="mode-aicode"]')
    const modeQimu = wrapper.get('[data-testid="mode-qimu"]')
    const modeCore = wrapper.get('[data-testid="mode-core"]')

    await modeAiCode.trigger('click')
    expect(modeAiCode.attributes('aria-selected')).toBe('true')
    expect(canvas.attributes('data-hover')).toBe('aicode')
    expect(wrapper.get('.sp-mode-panel').text()).toContain('AICode')

    await modeQimu.trigger('click')
    expect(modeQimu.attributes('aria-selected')).toBe('true')
    expect(canvas.attributes('data-hover')).toBe('qimu')
    expect(wrapper.get('.sp-mode-panel').text()).toContain('启幕工坊')

    await modeCore.trigger('click')
    expect(modeCore.attributes('aria-selected')).toBe('true')
    expect(canvas.attributes('data-hover')).toBe('')

    const aicodeDock = wrapper.get('[data-testid="portal-aicode"]')
    const qimuDock = wrapper.get('[data-testid="portal-qimu"]')

    await aicodeDock.trigger('pointerenter')
    expect(canvas.attributes('data-hover')).toBe('aicode')

    await aicodeDock.trigger('pointerleave')
    expect(canvas.attributes('data-hover')).toBe('')

    await qimuDock.trigger('pointerenter')
    expect(canvas.attributes('data-hover')).toBe('qimu')
  })

  it('honors prefers-reduced-motion and avoids smooth scrolling', async () => {
    reducedMotionMatches = true
    const CanvasStub = defineComponent({
      name: 'SpatialPortalCanvas',
      props: ['progress', 'hoverSide', 'dark', 'reducedMotion'],
      template: '<div data-testid="canvas-stub" :data-reduced="String(reducedMotion)" />',
    })

    const wrapper = mount(ReferenceLanding, {
      global: {
        plugins: [i18n],
        stubs: {
          RouterLink: RouterLinkStub,
          SpatialPortalCanvas: CanvasStub,
          PublicLocaleSwitcher: { template: '<div />' },
          Icon: { template: '<span />' },
        },
      },
    })

    await flushPromises()

    expect(wrapper.get('.reference-landing').classes()).toContain('is-reduced-motion')
    expect(wrapper.get('[data-testid="canvas-stub"]').attributes('data-reduced')).toBe('true')

    await wrapper.get('.rl-nav-links a[href="#act-finale"]').trigger('click')
    expect(window.scrollTo).toHaveBeenCalledWith(expect.objectContaining({ behavior: 'auto' }))
  })

  it('renders crisp AICode naming without relay suffix', async () => {
    const wrapper = mount(ReferenceLanding, {
      global: {
        plugins: [i18n],
        stubs: {
          RouterLink: RouterLinkStub,
          SpatialPortalCanvas: { template: '<div />' },
          PublicLocaleSwitcher: { template: '<div />' },
          Icon: { template: '<span />' },
        },
      },
    })

    await flushPromises()

    const aicodePortal = wrapper.get('[data-testid="portal-aicode"]')
    expect(aicodePortal.text()).toContain('AICode')
    expect(aicodePortal.text()).toContain('进入 AICode')
    expect(aicodePortal.text()).not.toContain('中转站')
  })

  it('locks homepage dark mode and opens in-page login/register modal without navigating away', async () => {
    const wrapper = mount(ReferenceLanding, {
      global: {
        plugins: [i18n],
        stubs: {
          RouterLink: RouterLinkStub,
          SpatialPortalCanvas: { template: '<div />' },
          PublicLocaleSwitcher: { template: '<div />' },
          Icon: { template: '<span />' },
        },
      },
    })

    await flushPromises()

    expect(wrapper.get('.rl-theme-shell').classes()).toContain('dark')
    expect(wrapper.find('[data-testid="theme-toggle"]').exists()).toBe(false)

    // Click topbar login CTA -> opens in-page auth modal (login mode)
    await wrapper.get('.rl-nav-login').trigger('click')

    await vi.waitFor(() => {
      expect(document.body.querySelector('[data-testid="auth-portal-modal"]')).not.toBeNull()
    })
    expect(document.body.querySelector('[data-testid="auth-login-submit"]')).not.toBeNull()

    // Switch tab inside modal to register mode
    const regTab = document.body.querySelector<HTMLButtonElement>('[data-testid="auth-tab-register"]')
    expect(regTab).not.toBeNull()
    regTab?.click()
    await flushPromises()
    expect(document.body.querySelector('[data-testid="auth-register-submit"]')).not.toBeNull()

    // Dismiss via close button
    document.body.querySelector<HTMLButtonElement>('[data-testid="auth-modal-close"]')?.click()
    await flushPromises()
    expect(document.body.querySelector('[data-testid="auth-portal-modal"]')).toBeNull()

    // Click Act 1 unified primary CTA -> opens modal in-page again
    await wrapper.get('[data-testid="hero-primary-cta"]').trigger('click')
    await vi.waitFor(() => {
      expect(document.body.querySelector('[data-testid="auth-portal-modal"]')).not.toBeNull()
    })
  })
})
