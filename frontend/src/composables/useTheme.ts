import { readonly, ref } from 'vue'

export type ThemePreference = 'light' | 'dark' | 'system'

type ViewTransitionLike = {
  ready?: Promise<unknown>
  finished?: Promise<unknown>
}

type DocumentWithViewTransition = Document & {
  startViewTransition?: (callback: () => void) => ViewTransitionLike
}

const preference = ref<ThemePreference>('dark')
const isDark = ref(true)
let media: MediaQueryList | undefined
let initialized = false

function storedPreference(): ThemePreference {
  try {
    const saved = localStorage.getItem('theme')
    return saved === 'dark' || saved === 'light' || saved === 'system' ? saved : 'dark'
  } catch {
    return preference.value
  }
}

function resolveIsDark(): boolean {
  return preference.value === 'dark' || (preference.value === 'system' && !!media?.matches)
}

function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-reduced-motion: reduce)').matches
    : false
}

function applyTheme() {
  const dark = resolveIsDark()
  isDark.value = dark
  // Both selector forms are used by the application. Updating only the class
  // leaves the old data-theme selector active until the next page load.
  document.documentElement.classList.toggle('dark', dark)
  document.documentElement.dataset.theme = dark ? 'dark' : 'light'
}

function applyThemeWithTransition() {
  const doc = document as DocumentWithViewTransition
  const canTransition =
    typeof doc.startViewTransition === 'function' &&
    document.visibilityState !== 'hidden' &&
    !prefersReducedMotion()

  if (!canTransition) {
    applyTheme()
    return
  }

  try {
    const transition = doc.startViewTransition!(() => {
      applyTheme()
    })
    transition?.ready?.catch(() => {})
    transition?.finished?.catch(() => {})
  } catch {
    applyTheme()
  }
}

function syncFromStorage() {
  preference.value = storedPreference()
  applyTheme()
}

function onStorage(event: StorageEvent) {
  if (event.key === 'theme' || event.key === null) syncFromStorage()
}

function stopThemeSync() {
  if (!initialized) return
  if (typeof media?.removeEventListener === 'function') media.removeEventListener('change', syncFromStorage)
  else media?.removeListener?.(syncFromStorage)
  window.removeEventListener('storage', onStorage)
  initialized = false
  media = undefined
}

// Start once for the application lifetime. Calling again when another layout
// mounts also picks up preferences written by a legacy public-page switcher.
export function initializeTheme(): () => void {
  if (!initialized) {
    media = window.matchMedia('(prefers-color-scheme: dark)')
    if (typeof media.addEventListener === 'function') media.addEventListener('change', syncFromStorage)
    else media.addListener?.(syncFromStorage)
    window.addEventListener('storage', onStorage)
    initialized = true
  }
  syncFromStorage()
  return stopThemeSync
}

export function setTheme(next: ThemePreference) {
  const wasInitialized = initialized
  if (!initialized) initializeTheme()
  preference.value = next
  const shouldTransition = wasInitialized && resolveIsDark() !== isDark.value
  if (shouldTransition) {
    applyThemeWithTransition()
  } else {
    applyTheme()
  }
  try {
    localStorage.setItem('theme', next)
  } catch {
    // Storage restrictions must not prevent the current page changing theme.
  }
}

export function useTheme() {
  initializeTheme()
  return {
    isDark: readonly(isDark),
    preference: readonly(preference),
    setTheme,
    toggleTheme: () => setTheme(isDark.value ? 'light' : 'dark'),
  }
}
