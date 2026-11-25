import { readonly, ref } from 'vue'

export const AMBIENT_BACKDROP_STORAGE_KEY = 'shell_ambient_backdrop'

function parseStoredEnabled(raw: string | null): boolean {
  return raw !== '0' && raw !== 'off'
}

function readStoredEnabled(): boolean {
  if (typeof localStorage === 'undefined') return true
  try {
    return parseStoredEnabled(localStorage.getItem(AMBIENT_BACKDROP_STORAGE_KEY))
  } catch {
    return true
  }
}

const ambientEnabled = ref<boolean>(readStoredEnabled())
let listenerAttached = false

function handleStorage(event: StorageEvent) {
  if (event.key === AMBIENT_BACKDROP_STORAGE_KEY || event.key === null) {
    ambientEnabled.value = readStoredEnabled()
  }
}

function ensureStorageSync() {
  if (listenerAttached || typeof window === 'undefined') return
  window.addEventListener('storage', handleStorage)
  listenerAttached = true
}

/**
 * Sets whether the dynamic 3D cosmic console backdrop is enabled.
 */
export function setAmbientBackdrop(enabled: boolean): void {
  ambientEnabled.value = enabled
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(AMBIENT_BACKDROP_STORAGE_KEY, enabled ? '1' : '0')
  } catch {
    // Ignore storage quota or privacy-mode restrictions.
  }
}

/**
 * Toggles the dynamic 3D cosmic console backdrop on or off.
 */
export function toggleAmbientBackdrop(): boolean {
  const next = !ambientEnabled.value
  setAmbientBackdrop(next)
  return next
}

/**
 * Reactive 2-state cosmic backdrop preference synced across components and browser tabs.
 */
export function useAmbientBackdrop() {
  ensureStorageSync()
  ambientEnabled.value = readStoredEnabled()

  return {
    ambientEnabled: readonly(ambientEnabled),
    setAmbientBackdrop,
    toggleAmbientBackdrop,
  }
}
