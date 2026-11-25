import { computed, readonly, ref } from 'vue'

export type AmbientFxMode = 'full' | 'ambient' | 'off'

export const AMBIENT_BACKDROP_STORAGE_KEY = 'shell_ambient_backdrop'

function parseStoredMode(raw: string | null): AmbientFxMode {
  if (raw === '0' || raw === 'off') return 'off'
  if (raw === 'ambient') return 'ambient'
  return 'full'
}

function readStoredMode(): AmbientFxMode {
  if (typeof localStorage === 'undefined') return 'full'
  try {
    return parseStoredMode(localStorage.getItem(AMBIENT_BACKDROP_STORAGE_KEY))
  } catch {
    return 'full'
  }
}

const fxMode = ref<AmbientFxMode>(readStoredMode())
let listenerAttached = false

function handleStorage(event: StorageEvent) {
  if (event.key === AMBIENT_BACKDROP_STORAGE_KEY || event.key === null) {
    fxMode.value = readStoredMode()
  }
}

function ensureStorageSync() {
  if (listenerAttached || typeof window === 'undefined') return
  window.addEventListener('storage', handleStorage)
  listenerAttached = true
}

/**
 * Sets the visual FX tier across backdrop WebGL/particles and the starfighter cursor.
 */
export function setFxMode(mode: AmbientFxMode): void {
  fxMode.value = mode
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(AMBIENT_BACKDROP_STORAGE_KEY, mode)
  } catch {
    // Ignore storage quota or privacy-mode restrictions.
  }
}

/**
 * Cycles through Full -> Ambient -> Off -> Full.
 */
export function cycleFxMode(): AmbientFxMode {
  const next: AmbientFxMode =
    fxMode.value === 'full' ? 'ambient' : fxMode.value === 'ambient' ? 'off' : 'full'
  setFxMode(next)
  return next
}

/**
 * Backwards-compatible boolean setter mapping true -> full, false -> off.
 */
export function setAmbientBackdrop(enabled: boolean): void {
  setFxMode(enabled ? 'full' : 'off')
}

/**
 * Backwards-compatible boolean toggle used by older callers/tests.
 */
export function toggleAmbientBackdrop(): boolean {
  const next = fxMode.value === 'off'
  setAmbientBackdrop(next)
  return next
}

/**
 * Reactive 3-mode cosmic FX preference synced across components and browser tabs.
 */
export function useAmbientBackdrop() {
  ensureStorageSync()
  fxMode.value = readStoredMode()
  const ambientEnabled = computed(() => fxMode.value !== 'off')
  const cursorFxEnabled = computed(() => fxMode.value === 'full')

  return {
    fxMode: readonly(fxMode),
    ambientEnabled,
    cursorFxEnabled,
    setFxMode,
    cycleFxMode,
    setAmbientBackdrop,
    toggleAmbientBackdrop,
  }
}
