import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import HeroGatewayPrism from '../HeroGatewayPrism.vue'
import UnifiedCoreTheater from '../UnifiedCoreTheater.vue'
import messages from '@/i18n/locales/zh/referenceLanding'

const i18n = createI18n({ legacy: false, locale: 'zh', messages: { zh: { ...messages, glass: { nav: { docs: '开发文档' } } } } })
let reducedMotion: MediaQueryList
let desktop: MediaQueryList
let observer: { observe: ReturnType<typeof vi.fn>; disconnect: ReturnType<typeof vi.fn> }

function render(component: typeof HeroGatewayPrism | typeof UnifiedCoreTheater) {
  return mount(component, {
    props: {
      siteName: 'code1024',
      canvasUrl: 'https://qimu.code1024.cc/',
      canvasLabel: '影策画布',
      docUrl: 'https://docs.code1024.cc/',
    },
    global: { plugins: [i18n] },
  })
}

describe('gateway prism experience', () => {
  beforeEach(() => {
    reducedMotion = { matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() } as unknown as MediaQueryList
    desktop = { matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() } as unknown as MediaQueryList
    observer = { observe: vi.fn(), disconnect: vi.fn() }
    vi.spyOn(window, 'matchMedia').mockImplementation(query => query.includes('prefers-reduced-motion') ? reducedMotion : desktop)
    vi.stubGlobal('IntersectionObserver', vi.fn(() => observer))
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('changes hero modes and retains the configured canvas entry', async () => {
    const wrapper = render(HeroGatewayPrism)
    const modes = wrapper.findAll('.hgp-mode-btn')
    expect(modes).toHaveLength(3)
    await modes[2].trigger('click')
    expect(wrapper.get('.hgp-stage').classes()).toContain('hgp-stage--cinema')
    expect(modes[2].attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('.hgp-canvas-link').attributes('href')).toBe('https://qimu.code1024.cc/')
    wrapper.unmount()
  })

  it('uses lightweight scene tabs on mobile and supports keyboard navigation', async () => {
    const wrapper = render(UnifiedCoreTheater)
    expect(wrapper.get('.uct').classes()).toContain('uct--static')
    const tabs = wrapper.findAll<HTMLButtonElement>('.rl-demo-tab')
    expect(tabs).toHaveLength(3)
    await tabs[1].trigger('click')
    expect(wrapper.get('#studio-panel-cinema').isVisible()).toBe(true)
    expect(wrapper.get('.uct-canvas-action').attributes('href')).toBe('https://qimu.code1024.cc/')
    tabs[1].element.focus()
    await tabs[1].trigger('keydown', { key: 'End' })
    expect(wrapper.get('#studio-panel-gateway').isVisible()).toBe(true)
    expect(wrapper.get('.uct-doc-action').attributes('href')).toBe('https://docs.code1024.cc/')
    wrapper.unmount()
    expect(observer.disconnect).toHaveBeenCalledOnce()
    expect(desktop.removeEventListener).toHaveBeenCalled()
    expect(reducedMotion.removeEventListener).toHaveBeenCalled()
  })

  it('uses static scenes when reduced motion is requested and cleans up scroll listeners', async () => {
    Object.defineProperty(reducedMotion, 'matches', { value: true })
    Object.defineProperty(desktop, 'matches', { value: true })
    const addScroll = vi.spyOn(window, 'addEventListener')
    const removeScroll = vi.spyOn(window, 'removeEventListener')
    const wrapper = render(UnifiedCoreTheater)
    expect(wrapper.get('.uct').classes()).toContain('uct--static')
    expect(addScroll).not.toHaveBeenCalledWith('scroll', expect.any(Function), expect.anything())
    await wrapper.get('#studio-tab-cinema').trigger('click')
    expect(wrapper.get('#studio-panel-cinema').isVisible()).toBe(true)
    wrapper.unmount()
    expect(removeScroll).toHaveBeenCalledWith('scroll', expect.any(Function))
  })
})
