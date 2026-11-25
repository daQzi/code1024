import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { initializeTheme, setTheme, useTheme } from '../useTheme'

describe('theme synchronization', () => {
  let stop: (() => void) | undefined
  let onSystemChange: (() => void) | undefined
  const media = {
    matches: false,
    addEventListener: vi.fn((_event: string, listener: () => void) => { onSystemChange = listener }),
    removeEventListener: vi.fn(),
  }

  function expectTheme(dark: boolean) {
    expect(document.documentElement.classList.contains('dark')).toBe(dark)
    expect(document.documentElement.dataset.theme).toBe(dark ? 'dark' : 'light')
    expect(document.documentElement.matches('.dark, [data-theme="dark"]')).toBe(dark)
  }

  beforeEach(() => {
    media.matches = false
    onSystemChange = undefined
    vi.clearAllMocks()
    localStorage.removeItem('theme')
    document.documentElement.classList.remove('dark')
    delete document.documentElement.dataset.theme
    vi.spyOn(window, 'matchMedia').mockReturnValue(media as unknown as MediaQueryList)
  })

  afterEach(() => {
    stop?.()
    vi.restoreAllMocks()
    localStorage.removeItem('theme')
    document.documentElement.classList.remove('dark')
    delete document.documentElement.dataset.theme
  })

  it('repairs stale selector state at startup and keeps all consumers reactive', () => {
    localStorage.setItem('theme', 'light')
    document.documentElement.classList.add('dark')
    document.documentElement.dataset.theme = 'dark'
    stop = initializeTheme()
    const first = useTheme()
    const second = useTheme()
    expectTheme(false)
    first.toggleTheme()
    expectTheme(true)
    expect(second.isDark.value).toBe(true)
    expect(localStorage.getItem('theme')).toBe('dark')
    second.toggleTheme()
    expectTheme(false)
    expect(first.isDark.value).toBe(false)
    expect(localStorage.getItem('theme')).toBe('light')
    expect(media.addEventListener).toHaveBeenCalledTimes(1)
  })

  it('defaults to dark when unset and follows system changes only in explicit system mode', () => {
    media.matches = false
    stop = initializeTheme()
    expectTheme(true)
    setTheme('system')
    expect(localStorage.getItem('theme')).toBe('system')
    expectTheme(false)
    media.matches = true
    onSystemChange?.()
    expectTheme(true)
    setTheme('light')
    media.matches = true
    onSystemChange?.()
    expectTheme(false)
    setTheme('system')
    expect(localStorage.getItem('theme')).toBe('system')
    expectTheme(true)
    media.matches = false
    onSystemChange?.()
    expectTheme(false)
  })

  it('accepts a stored system mode and synchronizes changes from another tab', () => {
    media.matches = true
    localStorage.setItem('theme', 'system')
    stop = initializeTheme()
    expectTheme(true)
    localStorage.setItem('theme', 'light')
    window.dispatchEvent(new StorageEvent('storage', { key: 'theme', newValue: 'light' }))
    expectTheme(false)
    localStorage.removeItem('theme')
    window.dispatchEvent(new StorageEvent('storage', { key: null }))
    expectTheme(true)
  })

  it('adopts changes from a legacy public-page switcher on the next layout mount', () => {
    stop = initializeTheme()
    localStorage.setItem('theme', 'dark')
    const nextLayout = useTheme()
    expect(nextLayout.isDark.value).toBe(true)
    expectTheme(true)
    localStorage.setItem('theme', 'light')
    media.matches = true
    onSystemChange?.()
    expectTheme(false)
  })

  it('applies the current selection even when persisting it is blocked', () => {
    stop = initializeTheme()
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => { throw new Error('Storage blocked') })
    expect(() => setTheme('light')).not.toThrow()
    expectTheme(false)
  })

  it('removes application-lifetime subscriptions during teardown', () => {
    const removeStorage = vi.spyOn(window, 'removeEventListener')
    localStorage.setItem('theme', 'light')
    stop = initializeTheme()
    expectTheme(false)
    stop()
    expect(media.removeEventListener).toHaveBeenCalledWith('change', onSystemChange)
    expect(removeStorage).toHaveBeenCalledWith('storage', expect.any(Function))
    localStorage.setItem('theme', 'dark')
    window.dispatchEvent(new StorageEvent('storage', { key: 'theme', newValue: 'dark' }))
    expectTheme(false)
  })

  it('supports the legacy system-theme listener API', () => {
    const legacyMedia = { matches: false, addListener: vi.fn(), removeListener: vi.fn() }
    vi.mocked(window.matchMedia).mockReturnValue(legacyMedia as unknown as MediaQueryList)
    localStorage.setItem('theme', 'system')
    stop = initializeTheme()
    expectTheme(false)
    const change = legacyMedia.addListener.mock.calls[0][0]
    legacyMedia.matches = true
    change()
    expectTheme(true)
    stop()
    expect(legacyMedia.removeListener).toHaveBeenCalledWith(change)
  })

  it('uses document.startViewTransition on interactive theme changes when motion is allowed', () => {
    const startViewTransition = vi.fn((cb: () => void) => {
      cb()
      return { ready: Promise.resolve(), finished: Promise.resolve() }
    })
    Object.defineProperty(document, 'startViewTransition', {
      configurable: true,
      writable: true,
      value: startViewTransition
    })
    try {
      stop = initializeTheme()
      setTheme('light')
      expect(startViewTransition).toHaveBeenCalledTimes(1)
      expectTheme(false)
    } finally {
      delete (document as unknown as Record<string, unknown>).startViewTransition
    }
  })

  it('skips view transition when prefers-reduced-motion is active', () => {
    const startViewTransition = vi.fn((cb: () => void) => {
      cb()
      return { ready: Promise.resolve(), finished: Promise.resolve() }
    })
    Object.defineProperty(document, 'startViewTransition', {
      configurable: true,
      writable: true,
      value: startViewTransition
    })
    vi.mocked(window.matchMedia).mockImplementation((query: string) => ({
      ...media,
      matches: query === '(prefers-reduced-motion: reduce)'
    }) as unknown as MediaQueryList)
    try {
      stop = initializeTheme()
      setTheme('light')
      expect(startViewTransition).not.toHaveBeenCalled()
      expectTheme(false)
    } finally {
      delete (document as unknown as Record<string, unknown>).startViewTransition
    }
  })
})
