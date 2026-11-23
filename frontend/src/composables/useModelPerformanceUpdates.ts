import { onBeforeUnmount, onMounted, watch } from 'vue'
import { watchModelPerformance } from '@/utils/modelPerformanceUpdates'
import { useVisibleInterval } from '@/composables/useVisibleInterval'

// Ordinary authenticated requests remain authoritative and keep their 15s
// fallback. SSE only invalidates that data; changing scope cancels the old
// connection and hidden tabs retain no live subscription.
export function useModelPerformanceUpdates(scope: () => { ids: string[]; groupId?: number }, refresh: (scopeChanged?: boolean) => void) {
  let mounted = false
  let controller: AbortController | undefined
  let reconnect: ReturnType<typeof setTimeout> | undefined
  let failures = 0
  const stop = () => { clearTimeout(reconnect); reconnect = undefined; controller?.abort(); controller = undefined }
  const start = () => {
    stop()
    if (!mounted || document.hidden) return
    const current = scope()
    if (!current.groupId || !current.ids.length || current.ids.length > 24) return
    const request = controller = new AbortController()
    const token = localStorage.getItem('auth_token')
    let revoked = false
    void watchModelPerformance({ ...current, groupId: current.groupId, signal: request.signal, token,
      onRefresh: () => { if (!request.signal.aborted && localStorage.getItem('auth_token') === token) refresh() },
      onScopeChanged: () => { if (!request.signal.aborted) { revoked = true; refresh(true) } }
    }).then(() => { if (!request.signal.aborted) failures = 0 }, () => { if (!request.signal.aborted) failures++ }).finally(() => {
      if (request.signal.aborted || !mounted || document.hidden) return
      // Every reconnect passes OptionalJWT again. Auth failures cannot silently
      // reconnect as anonymous; the ordinary client owns token refresh.
      reconnect = setTimeout(start, revoked ? 15000 : failures ? Math.min(60000, 15000 * failures) : 1000)
    })
  }
  const visibility = () => { if (document.hidden) stop(); else { refresh(); start() } }
  watch(() => JSON.stringify(scope()), () => { if (mounted) start() })
  onMounted(() => { mounted = true; start(); document.addEventListener('visibilitychange', visibility) })
  onBeforeUnmount(() => { mounted = false; stop(); document.removeEventListener('visibilitychange', visibility) })
  useVisibleInterval(() => { refresh() })
}
