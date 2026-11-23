import { buildApiUrl } from '@/api/url'

export interface ModelPerformanceWatchOptions {
  ids: string[]
  groupId: number
  signal: AbortSignal
  token?: string | null
  onRefresh: () => void
  onScopeChanged: () => void
}

// A notification has no metrics or credentials. Bounds apply to accumulated
// partial lines as well as complete events; an endless short-line frame cannot
// grow memory. The subscription is short-lived and owns/cancels its reader.
export async function watchModelPerformance(options: ModelPerformanceWatchOptions): Promise<void> {
  const ids = [...new Set(options.ids)]
  if (!ids.length || ids.length > 24 || !ids.every(id => /^mdl_[a-f0-9]{32}$/.test(id)) || !Number.isSafeInteger(options.groupId) || options.groupId <= 0) throw new Error('Invalid performance subscription scope')
  const query = new URLSearchParams({ ids: ids.join(','), group_id: String(options.groupId) })
  const controller = new AbortController()
  const abort = () => controller.abort()
  options.signal.addEventListener('abort', abort, { once: true })
  if (options.signal.aborted) abort()
  const timer = setTimeout(abort, 28000)
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
  try {
    const response = await fetch(`${buildApiUrl('/model-plaza/performance/updates')}?${query}`, {
      signal: controller.signal,
      credentials: 'include', cache: 'no-store', redirect: 'error', referrerPolicy: 'no-referrer',
      headers: { Accept: 'text/event-stream', ...(options.token ? { Authorization: `Bearer ${options.token}` } : {}) }
    })
    if (!response.ok || !response.headers.get('Content-Type')?.startsWith('text/event-stream') || !response.body) throw new Error('Performance subscription unavailable')
    reader = response.body.getReader()
    const decoder = new TextDecoder('utf-8', { fatal: true })
    let pending = ''
    let event = ''
    let eventBytes = 0
    let totalBytes = 0
    let frames = 0
    for (;;) {
      const { value, done } = await reader.read()
      if (done) break
      totalBytes += value.byteLength
      if (totalBytes > 16384 || value.byteLength > 4096) throw new Error('Performance subscription exceeded its limit')
      pending += decoder.decode(value, { stream: true })
      let newline: number
      while ((newline = pending.indexOf('\n')) !== -1) {
        const line = pending.slice(0, newline).replace(/\r$/, '')
        pending = pending.slice(newline + 1)
        eventBytes += line.length + 1
        if (eventBytes > 1024) throw new Error('Performance event exceeded its limit')
        if (!line) {
          if (++frames > 64) throw new Error('Too many performance events')
          if (event === 'refresh') options.onRefresh()
          if (event === 'scope_changed') { options.onScopeChanged(); return }
          event = ''; eventBytes = 0
        } else if (line.startsWith('event:')) event = line.slice(6).trim()
      }
      if (pending.length > 1024) throw new Error('Performance event line exceeded its limit')
    }
    decoder.decode()
  } finally {
    clearTimeout(timer)
    options.signal.removeEventListener('abort', abort)
    controller.abort()
    if (reader) { await reader.cancel().catch(() => undefined); reader.releaseLock() }
  }
}
