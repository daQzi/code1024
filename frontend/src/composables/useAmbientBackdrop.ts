import { readonly, ref } from 'vue'

export const AMBIENT_BACKDROP_STORAGE_KEY = 'shell_ambient_backdrop'

const ambientEnabled = ref<boolean>(readStoredPreference())
let listenerAttached = false

function readStoredPreference(): boolean {
  if (typeof localStorage === 'undefined') return true
  try {
    const raw = localStorage.getItem(AMBIENT_BACKDROP_STORAGE_KEY)
    return raw !== '0'
  } catch {
    return true
  }
}

function handleStorage(event: StorageEvent) {
  if (event.key === AMBIENT_BACKDROP_STORAGE_KEY || event.key === null) {
    ambientEnabled.value = readStoredPreference()
  }
}

function ensureStorageSync() {
  if (listenerAttached || typeof window === 'undefined') return
  window.addEventListener('storage', handleStorage)
  listenerAttached = true
}

/**
 * Controls whether logged-in console views render the lightweight WebGL cosmic aurora layer.
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
 * Toggles the console dynamic aurora background preference.
 */
export function toggleAmbientBackdrop(): boolean {
  const next = !ambientEnabled.value
  setAmbientBackdrop(next)
  return next
}

/**
 * Reactive ambient backdrop preference synced across components and tabs.
 */
export function useAmbientBackdrop() {
  ensureStorageSync()
  ambientEnabled.value = readStoredPreference()
  return {
    ambientEnabled: readonly(ambientEnabled),
    setAmbientBackdrop,
    toggleAmbientBackdrop,
  }
}
