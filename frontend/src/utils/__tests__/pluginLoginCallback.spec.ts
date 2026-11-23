import { afterEach, describe, expect, it, vi } from 'vitest'

describe('plugin login callback bootstrap', () => {
  afterEach(() => {
    window.history.replaceState({}, '', '/')
    document.head.querySelectorAll('meta[name="referrer"]').forEach(meta => meta.remove())
    vi.resetModules()
  })

  it('removes callback secrets before startup, keeping them only for a single view', async () => {
    window.history.replaceState({}, '', '/plugin-login/callback/fixture?code=one-time-secret&state=host-state')
    const module = await import('../pluginLoginCallback')
    expect(window.location.search).toBe('')
    expect(window.location.hash).toBe('')
    expect(window.location.pathname).toBe('/plugin-login/callback/fixture')
    expect(document.head.querySelector('meta[name="referrer"]')?.getAttribute('content')).toBe('no-referrer')
    expect(module.takePluginLoginCallback()).toContain('code=one-time-secret')
    expect(module.takePluginLoginCallback()).toBe('')
    expect(localStorage.getItem('plugin-login-callback')).toBeNull()
  })

  it('leaves ordinary routes and their search parameters intact', async () => {
    window.history.replaceState({}, '', '/admin/accounts?search=fixture')
    const module = await import('../pluginLoginCallback')
    expect(window.location.search).toBe('?search=fixture')
    expect(module.takePluginLoginCallback()).toBe('')
  })

  it('clears oversized or fragment-only callback URLs without retaining them', async () => {
    window.history.replaceState({}, '', '/plugin-login/callback/fixture#access_token=private')
    const module = await import('../pluginLoginCallback')
    expect(window.location.hash).toBe('')
    expect(module.takePluginLoginCallback()).toBe('')
  })
})
