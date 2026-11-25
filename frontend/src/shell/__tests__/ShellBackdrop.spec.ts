import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ShellBackdrop from '../ShellBackdrop.vue'
import { AMBIENT_BACKDROP_STORAGE_KEY, cycleFxMode, setFxMode } from '@/composables/useAmbientBackdrop'

describe('ShellBackdrop', () => {
  beforeEach(() => {
    localStorage.clear()
    setFxMode('full')
    vi.spyOn(window, 'matchMedia').mockImplementation((query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as MediaQueryList)
  })

  afterEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
  })

  it('falls back cleanly to CSS aurora layers when WebGL is unavailable and mounts without router', () => {
    const wrapper = mount(ShellBackdrop)
    const root = wrapper.get('.shell-bg')

    expect(root.attributes('data-ambient-mode')).toBe('css')
    expect(root.attributes('data-fx-mode')).toBe('full')
    expect(wrapper.find('canvas').exists()).toBe(false)
    expect(wrapper.find('.shell-bg__blob--a').exists()).toBe(true)
    expect(wrapper.find('.shell-bg__grid').exists()).toBe(true)
  })

  it('cycles through full -> ambient -> off -> full and persists fxMode in localStorage', () => {
    expect(cycleFxMode()).toBe('ambient')
    expect(localStorage.getItem(AMBIENT_BACKDROP_STORAGE_KEY)).toBe('ambient')
    expect(cycleFxMode()).toBe('off')
    expect(localStorage.getItem(AMBIENT_BACKDROP_STORAGE_KEY)).toBe('off')
    expect(cycleFxMode()).toBe('full')
    expect(localStorage.getItem(AMBIENT_BACKDROP_STORAGE_KEY)).toBe('full')
  })
})
