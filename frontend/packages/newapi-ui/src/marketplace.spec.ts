import { afterEach, describe, expect, it, vi } from 'vitest'
import { webcrypto } from 'node:crypto'
import { communityMarketplaceURL, fetchMarketplaceDocument, fetchMarketplaceIndex, fetchMarketplacePinIndex, fetchMarketplaceSource, fetchMarketplaceText, findMarketplacePluginByCode, marketplaceSourceKind, parseMarketplaceIndex, requireMarketplaceVersion, resolveCommunityPlugin, resolveMarketplaceFile, sourceSHA256 } from './marketplace'

const address = 'https://plugins.example.test/api/index.json'
const source = '\uFEFFexport const meta = {name: "海"};'
const fixture = async () => ({ indexVersion: 1, name: 'Example', plugins: [{ key: 'test-video', name: 'Video', latest: '1.0.0', versions: [{ version: '1.0.0', path: '../video.js', sha256: await sourceSHA256(source) }] }], revoked: [] as { sha256: string }[] })
function useFetch(handler: (url: string, init: RequestInit) => Response | Promise<Response>) {
  vi.stubGlobal('fetch', vi.fn((url: string, init: RequestInit) => handler(url, init)))
}
vi.stubGlobal('crypto', webcrypto)
afterEach(() => { vi.unstubAllGlobals(); vi.stubGlobal('crypto', webcrypto); vi.useRealTimers() })

describe('New API marketplace contract and bounded browser transport', () => {
  it('finds eight-digit codes in official indexes before other configured sources', async () => {
    const raw = await fixture()
    const payload = { ...raw, plugins: [{ ...raw.plugins[0], code: '12345678' }] }
    useFetch((url, init) => {
      expect(url).toBe('https://www.newapi.ai/api/v1/plugins/index.json')
      expect(init.credentials).toBe('omit')
      return new Response(JSON.stringify(payload))
    })
    const found = await findMarketplacePluginByCode(' 12345678 ', [{ name: 'Custom', index_url: address }, { name: 'Official', index_url: 'https://www.newapi.ai/api/v1/plugins/index.json' }], new AbortController().signal)
    expect(found?.source.name).toBe('Official')
    expect(found?.index.plugins[0]?.key).toBe('test-video')
    expect(found?.code).toBeUndefined()
    expect(fetch).toHaveBeenCalledTimes(1)
  })
  it('resolves unlisted community codes and retains current takedowns when merging', async () => {
    const raw = await fixture()
    const hash = raw.plugins[0]!.versions[0]!.sha256
    useFetch((url, init) => {
      expect(init).toMatchObject({ credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer' })
      if (url === communityMarketplaceURL) return new Response(JSON.stringify({ indexVersion: 1, plugins: [], revoked: [{ sha256: hash }] }))
      expect(url).toBe('https://www.newapi.ai/api/v1/community/resolve?code=12345678')
      return new Response(JSON.stringify({ ...raw, plugins: [{ ...raw.plugins[0], code: '12345678' }] }))
    })
    const sources = [{ name: 'Community', index_url: communityMarketplaceURL }]
    const found = await findMarketplacePluginByCode('12345678', sources, new AbortController().signal)
    expect(found?.code).toBe('12345678')
    expect(found?.index.plugins).toHaveLength(1)
    const fresh = await fetchMarketplacePinIndex(communityMarketplaceURL, found?.code, new AbortController().signal)
    expect(() => requireMarketplaceVersion(fresh, { index_url: communityMarketplaceURL, key: 'test-video', version: '1.0.0', sha256: hash })).toThrow('revoked')
    expect(fetch).toHaveBeenCalledTimes(4)
  })
  it('never resolves disabled sources or an arbitrary URL and rejects a mismatched lookup response', async () => {
    const raw = await fixture()
    useFetch(() => new Response(JSON.stringify(raw)))
    expect(await findMarketplacePluginByCode('12345678', [], new AbortController().signal)).toBeNull()
    expect(fetch).not.toHaveBeenCalled()
    for (const code of ['https://evil.test/a', '1234&key=secret', '1234567', '１２３４５６７８']) {
      await expect(resolveCommunityPlugin(code, new AbortController().signal)).rejects.toThrow('code')
    }
    expect(fetch).not.toHaveBeenCalled()
    await expect(resolveCommunityPlugin('12345678', new AbortController().signal)).rejects.toThrow('index')
    useFetch(() => new Response(null, { status: 404 }))
    expect(await resolveCommunityPlugin('12345678', new AbortController().signal)).toBeNull()
    useFetch(url => new Response(JSON.stringify(url === communityMarketplaceURL ? { ...raw, plugins: [] } : { ...raw, plugins: [{ ...raw.plugins[0], code: '12345678' }] })))
    await expect(fetchMarketplacePinIndex(address, '12345678', new AbortController().signal)).rejects.toThrow('url')
  })
  it('rechecks resolved listings and stops code lookup when canceled', async () => {
    useFetch(url => url === communityMarketplaceURL ? new Response(JSON.stringify({ indexVersion: 1, plugins: [] })) : new Response(null, { status: 404 }))
    await expect(fetchMarketplacePinIndex(communityMarketplaceURL, '12345678', new AbortController().signal)).rejects.toThrow('changed')
    vi.mocked(fetch).mockClear()
    const controller = new AbortController()
    controller.abort(new Error('stop lookup'))
    await expect(findMarketplacePluginByCode('12345678', [{ name: 'Example', index_url: address }], controller.signal)).rejects.toThrow('stop lookup')
    expect(fetch).not.toHaveBeenCalled()
  })
  it('resolves same-origin files and labels sources from addresses', () => {
    expect(resolveMarketplaceFile(address, '../video.js')).toBe('https://plugins.example.test/video.js')
    for (const path of ['https://other.test/a.js', '//other.test/a.js', 'https://user@plugins.example.test/a', '/a#b', '/a?token=b', 'data:text/javascript,a', '']) expect(() => resolveMarketplaceFile(address, path)).toThrow()
    expect(marketplaceSourceKind('https://www.newapi.ai/api/v1/plugins/index.json')).toBe('official')
    expect(marketplaceSourceKind('https://www.newapi.ai/api/v1/community/index.json')).toBe('community')
    expect(marketplaceSourceKind(address)).toBe('third_party')
  })
  it('filters unknown kinds, preserves missing hashes as blocked versions and rejects ambiguous indexes', async () => {
    const raw = await fixture()
    raw.plugins[0]!.versions.push({ version: '2.0.0', path: 'missing.js', sha256: '' })
    const parsed = parseMarketplaceIndex(raw)
    expect(parsed.plugins[0]!.versions[1]!.sha256).toBeUndefined()
    expect(() => requireMarketplaceVersion(parsed, { index_url: address, key: 'test-video', version: '2.0.0', sha256: '' })).toThrow('changed')
    expect(() => parseMarketplaceIndex({ ...raw, plugins: [raw.plugins[0], raw.plugins[0]] })).toThrow('index')
    expect(() => parseMarketplaceIndex({ ...raw, indexVersion: 2 })).toThrow('index')
    const version = raw.plugins[0]!.versions[0]!
    const pin = { index_url: address, key: 'test-video', version: version.version, sha256: version.sha256 }
    raw.revoked = [{ sha256: version.sha256.toUpperCase() }]
    expect(() => requireMarketplaceVersion(parseMarketplaceIndex(raw), pin)).toThrow('revoked')
    raw.revoked = []; version.sha256 = 'b'.repeat(64)
    expect(() => requireMarketplaceVersion(parseMarketplaceIndex(raw), pin)).toThrow('changed')
  })
  it('verifies actual UTF-8 source including BOM and uses credential-free requests', async () => {
    const raw = await fixture()
    useFetch((url, init) => {
      expect(init).toMatchObject({ credentials: 'omit', redirect: 'error', cache: 'no-store', referrerPolicy: 'no-referrer' })
      return new Response(url.endsWith('index.json') ? JSON.stringify(raw) : source)
    })
    const index = await fetchMarketplaceIndex(address, new AbortController().signal)
    expect(await fetchMarketplaceSource(address, index.plugins[0]!.versions[0]!, new AbortController().signal)).toBe(source)
    useFetch(() => new Response('tampered'))
    await expect(fetchMarketplaceSource(address, index.plugins[0]!.versions[0]!, new AbortController().signal)).rejects.toThrow('hashMismatch')
  })
  it('enforces byte caps for unknown and declared lengths and cancels the stream', async () => {
    const cancel = vi.fn()
    useFetch(() => new Response(new ReadableStream({ start(controller) { controller.enqueue(new Uint8Array(9)) }, cancel }), { headers: {} }))
    await expect(fetchMarketplaceText(address, 8, new AbortController().signal)).rejects.toThrow('large')
    expect(cancel).toHaveBeenCalledTimes(1)
    useFetch(() => new Response('small', { headers: { 'content-length': '100' } }))
    await expect(fetchMarketplaceText(address, 8, new AbortController().signal)).rejects.toThrow('large')
    useFetch(() => new Response(new Uint8Array([0xff])))
    await expect(fetchMarketplaceText(address, 8, new AbortController().signal)).rejects.toThrow()
  })
  it('covers body reads with the deadline and caller cancellation', async () => {
    vi.useFakeTimers()
    useFetch((_url, init) => new Response(new ReadableStream({ start(controller) { init.signal!.addEventListener('abort', () => controller.error(init.signal!.reason)) } })))
    const timed = expect(fetchMarketplaceText(address, 8, new AbortController().signal)).rejects.toThrow('timeout')
    await vi.advanceTimersByTimeAsync(20001); await timed
    const controller = new AbortController()
    const pending = expect(fetchMarketplaceText(address, 8, controller.signal)).rejects.toThrow('cancel-fixture')
    await Promise.resolve(); controller.abort(new Error('cancel-fixture')); await pending
    expect(vi.getTimerCount()).toBe(0)
  })
  it('renders localized documents as text and checks their digest', async () => {
    const content = '<script>untrusted()</script> 中文'
    const hash = await sourceSHA256(content)
    useFetch(url => { expect(url).toBe('https://plugins.example.test/api/readme.zh.md'); return new Response(content) })
    expect(await fetchMarketplaceDocument(address, { path: 'readme.md', translations: { 'zh-CN': { path: 'readme.zh.md', sha256: hash } } }, 'zh', new AbortController().signal)).toBe(content)
  })
})
