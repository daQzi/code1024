import { onBeforeUnmount, onMounted } from 'vue'

// Refresh visible consoles, including immediately after returning to the tab.
// Callers own cancellation and skip an overlapping request.
export function useVisibleInterval(refresh: () => void, milliseconds = 15000) {
  let timer: ReturnType<typeof setInterval> | undefined
  const tick = () => { if (!document.hidden) refresh() }
  onMounted(() => {
    timer = setInterval(tick, milliseconds)
    document.addEventListener('visibilitychange', tick)
  })
  onBeforeUnmount(() => {
    clearInterval(timer)
    document.removeEventListener('visibilitychange', tick)
  })
}
