import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import StarfighterCursorFX from '../StarfighterCursorFX.vue'
import { setFxMode } from '@/composables/useAmbientBackdrop'

describe('StarfighterCursorFX', () => {
  let finePointerMatches = true
  let reducedMotionMatches = false

  beforeEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove('has-starfighter-cursor')
    finePointerMatches = true
    reducedMotionMatches = false
    setFxMode('full')

    vi.spyOn(window, 'matchMedia').mockImplementation((query: string) => ({
      matches: query.includes('prefers-reduced-motion')
        ? reducedMotionMatches
        : query.includes('pointer: fine')
          ? finePointerMatches
          : false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }) as unknown as MediaQueryList)
  })

  afterEach(() => {
    document.body.innerHTML = ''
    document.documentElement.classList.remove('has-starfighter-cursor')
    localStorage.clear()
    vi.restoreAllMocks()
  })

  it('activates starfighter cursor class on mouse movement, yields on text inputs, and cleans up on mode switch/unmount', async () => {
    const wrapper = mount(StarfighterCursorFX, { attachTo: document.body })
    await wrapper.vm.$nextTick()

    expect(document.body.querySelector('[data-testid="starfighter-cursor"]')).not.toBeNull()
    expect(document.body.querySelector('[data-testid="starfighter-fx-canvas"]')).not.toBeNull()

    const btn = document.createElement('button')
    document.body.appendChild(btn)
    const moveOverBtn = new Event('pointermove', { bubbles: true })
    Object.defineProperties(moveOverBtn, {
      pointerType: { value: 'mouse' },
      clientX: { value: 180 },
      clientY: { value: 240 },
    })
    btn.dispatchEvent(moveOverBtn)
    expect(document.documentElement.classList.contains('has-starfighter-cursor')).toBe(true)

    const input = document.createElement('input')
    document.body.appendChild(input)
    const moveOverInput = new Event('pointermove', { bubbles: true })
    Object.defineProperties(moveOverInput, {
      pointerType: { value: 'mouse' },
      clientX: { value: 220 },
      clientY: { value: 260 },
    })
    input.dispatchEvent(moveOverInput)
    expect(document.documentElement.classList.contains('has-starfighter-cursor')).toBe(false)

    btn.dispatchEvent(moveOverBtn)
    expect(document.documentElement.classList.contains('has-starfighter-cursor')).toBe(true)

    setFxMode('ambient')
    await wrapper.vm.$nextTick()
    expect(document.body.querySelector('[data-testid="starfighter-cursor"]')).toBeNull()
    expect(document.documentElement.classList.contains('has-starfighter-cursor')).toBe(false)

    setFxMode('full')
    await wrapper.vm.$nextTick()
    expect(document.body.querySelector('[data-testid="starfighter-cursor"]')).not.toBeNull()

    wrapper.unmount()
    expect(document.documentElement.classList.contains('has-starfighter-cursor')).toBe(false)
  })
})
