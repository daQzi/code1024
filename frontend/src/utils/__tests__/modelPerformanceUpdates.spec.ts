import { afterEach, describe, expect, it, vi } from 'vitest'
import { watchModelPerformance } from '../modelPerformanceUpdates'

const id = `mdl_${'a'.repeat(32)}`
function streamResponse(parts: string[]) {
  return new Response(new ReadableStream({ start(controller) { for (const text of parts) controller.enqueue(new TextEncoder().encode(text)); controller.close() } }), { headers: { 'Content-Type': 'text/event-stream' } })
}
function options() { return { ids: [id], groupId: 7, signal: new AbortController().signal, onRefresh: vi.fn(), onScopeChanged: vi.fn() } }

describe('performance notifications', () => {
  afterEach(() => { vi.unstubAllGlobals() })
  it('sends credentials in a header and handles chunked notifications only after a complete frame', async () => {
    const fetcher = vi.fn().mockResolvedValue(streamResponse(['event: re', 'fresh\ndata: {}\n', '\n: keepalive\n\nevent: scope_changed\ndata: {}\n\n']))
    vi.stubGlobal('fetch', fetcher)
    const args = { ...options(), token: 'fixture-secret' }
    await watchModelPerformance(args)
    expect(args.onRefresh).toHaveBeenCalledOnce()
    expect(args.onScopeChanged).toHaveBeenCalledOnce()
    const [url, request] = fetcher.mock.calls[0]!
    expect(url).toContain('group_id=7'); expect(url).not.toContain('fixture-secret')
    expect(request).toMatchObject({ redirect: 'error', credentials: 'include', headers: { Authorization: 'Bearer fixture-secret' } })
  })
  it('bounds accumulated short lines, never dispatches a partial frame, and releases its reader', async () => {
    const cancel = vi.fn()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(new ReadableStream({
      start(controller) { controller.enqueue(new TextEncoder().encode(`event: refresh\n${':x\n'.repeat(400)}`)) }, cancel
    }), { headers: { 'Content-Type': 'text/event-stream' } })))
    const args = options()
    await expect(watchModelPerformance(args)).rejects.toThrow('limit')
    expect(args.onRefresh).not.toHaveBeenCalled()
    expect(cancel).toHaveBeenCalledOnce()
  })
  it('rejects unauthorized responses and invalid scopes without falling back to anonymous', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response('', { status: 401 }))
    vi.stubGlobal('fetch', fetcher)
    await expect(watchModelPerformance({ ...options(), token: 'expired' })).rejects.toThrow('unavailable')
    expect(fetcher).toHaveBeenCalledOnce()
    await expect(watchModelPerformance({ ...options(), ids: Array.from({ length: 25 }, (_, i) => `mdl_${i.toString(16).padStart(32, '0')}`) })).rejects.toThrow('scope')
    expect(fetcher).toHaveBeenCalledOnce()
  })
  it('cancels a blocked read when the page aborts', async () => {
    const cancel = vi.fn()
    const parent = new AbortController()
    vi.stubGlobal('fetch', vi.fn().mockImplementation((_url, request) => {
      const stream = new ReadableStream({ start(controller) { request.signal.addEventListener('abort', () => controller.error(new DOMException('Aborted', 'AbortError'))) }, cancel })
      return Promise.resolve(new Response(stream, { headers: { 'Content-Type': 'text/event-stream' } }))
    }))
    const result = watchModelPerformance({ ...options(), signal: parent.signal })
    await Promise.resolve(); parent.abort()
    await expect(result).rejects.toThrow('Aborted')
  })
})
