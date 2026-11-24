<template>
  <Teleport to="body">
    <Transition name="cmd-palette">
      <div
        v-if="open"
        class="cmd-scrim"
        :style="{ zIndex: layer }"
        role="dialog"
        aria-modal="true"
        :aria-label="t('commandPalette.triggerLabel')"
        @click.self="close"
      >
        <div ref="panelRef" class="cmd-panel" @keydown="handlePanelKeydown">
          <div class="cmd-input-row">
            <Icon name="search" size="md" class="cmd-input-icon" aria-hidden="true" />
            <input
              ref="inputRef"
              v-model="query"
              type="text"
              class="cmd-input"
              autocomplete="off"
              spellcheck="false"
              :placeholder="t('commandPalette.searchPlaceholder')"
              :aria-activedescendant="activeItem ? `cmd-opt-${activeItem.id}` : undefined"
              role="combobox"
              aria-expanded="true"
              aria-controls="cmd-listbox"
              @keydown.down.prevent="moveActive(1)"
              @keydown.up.prevent="moveActive(-1)"
              @keydown.home.prevent="setActive(0)"
              @keydown.end.prevent="setActive(flatResults.length - 1)"
              @keydown.enter="handleEnter"
            />
            <button
              v-if="query"
              type="button"
              class="cmd-clear-btn"
              :aria-label="t('common.clear')"
              @click="clearQuery"
            >
              <Icon name="x" size="sm" aria-hidden="true" />
            </button>
            <kbd class="cmd-kbd">ESC</kbd>
          </div>

          <div id="cmd-listbox" ref="listRef" class="cmd-list ui-scroll" role="listbox">
            <template v-if="groupedResults.length">
              <div v-for="group in groupedResults" :key="group.key" class="cmd-group">
                <p class="cmd-group-title">{{ group.label }}</p>
                <button
                  v-for="item in group.items"
                  :id="`cmd-opt-${item.id}`"
                  :key="item.id"
                  type="button"
                  role="option"
                  :aria-selected="activeItem?.id === item.id"
                  class="cmd-item"
                  :class="{ 'is-active': activeItem?.id === item.id }"
                  @mouseenter="setActiveById(item.id)"
                  @click="runItem(item)"
                >
                  <span class="cmd-item-icon">
                    <Icon :name="asIconName(item.icon)" size="sm" aria-hidden="true" />
                  </span>
                  <span class="cmd-item-main">
                    <span class="cmd-item-label">{{ item.label }}</span>
                    <span v-if="item.subtitle" class="cmd-item-sub">{{ item.subtitle }}</span>
                  </span>
                  <span v-if="item.hint" class="cmd-item-hint">{{ item.hint }}</span>
                </button>
              </div>
            </template>

            <div v-else class="cmd-empty">
              <p class="cmd-empty-title">{{ t('commandPalette.emptyTitle') }}</p>
              <p class="cmd-empty-hint">{{ t('commandPalette.emptyHint') }}</p>
            </div>
          </div>

          <footer class="cmd-foot">
            <span class="cmd-foot-hint">
              <kbd class="cmd-kbd">↑</kbd>
              <kbd class="cmd-kbd">↓</kbd>
              {{ t('commandPalette.hintNavigate') }}
            </span>
            <span class="cmd-foot-hint">
              <kbd class="cmd-kbd">↵</kbd>
              {{ t('commandPalette.hintConfirm') }}
            </span>
            <span class="cmd-foot-hint">
              <kbd class="cmd-kbd">Esc</kbd>
              {{ t('commandPalette.hintClose') }}
            </span>
          </footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore, useAuthStore, useOnboardingStore } from '@/stores'
import { useTheme } from '@/composables/useTheme'
import { useDialogLayer } from '@/composables/useDialogLayer'
import { availableLocales, setLocale } from '@/i18n'
import { useShellNav } from './useShellNav'

interface PaletteEntry {
  id: string
  group: 'pages' | 'actions'
  label: string
  subtitle?: string
  hint?: string
  icon: string
  keywords: string[]
  run: () => void | Promise<void>
}

const props = defineProps<{
  open: boolean
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
}>()

const router = useRouter()
const { t, locale } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const onboardingStore = useOnboardingStore()
const { isDark, toggleTheme } = useTheme()
const { sections, handleMenuItemClick } = useShellNav()
const { layer, isTopmost } = useDialogLayer(() => props.open, () => 90)

const panelRef = ref<HTMLElement | null>(null)
const inputRef = ref<HTMLInputElement | null>(null)
const listRef = ref<HTMLElement | null>(null)
const query = ref('')
const activeIndex = ref(0)
let previousActiveElement: HTMLElement | null = null

function asIconName(name: string) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return (name || 'grid') as any
}

const pageEntries = computed<PaletteEntry[]>(() => {
  const entries: PaletteEntry[] = []
  const seen = new Set<string>()

  for (const section of sections.value) {
    for (const item of section.items) {
      if (item.children?.length) {
        if (!item.expandOnly && !seen.has(item.path)) {
          seen.add(item.path)
          entries.push({
            id: `page:${item.path}`,
            group: 'pages',
            label: item.label,
            subtitle: section.title,
            hint: item.path,
            icon: item.icon ?? 'grid',
            keywords: [item.label, section.title ?? '', item.path],
            run: async () => {
              handleMenuItemClick(item.path)
              await router.push(item.path)
            },
          })
        }
        for (const child of item.children) {
          if (seen.has(child.path)) continue
          seen.add(child.path)
          const crumbs = [section.title, item.label].filter(Boolean).join(' / ')
          entries.push({
            id: `page:${child.path}`,
            group: 'pages',
            label: child.label,
            subtitle: crumbs || undefined,
            hint: child.path,
            icon: item.icon ?? 'grid',
            keywords: [child.label, item.label, section.title ?? '', child.path],
            run: async () => {
              handleMenuItemClick(child.path)
              await router.push(child.path)
            },
          })
        }
      } else if (!seen.has(item.path)) {
        seen.add(item.path)
        entries.push({
          id: `page:${item.path}`,
          group: 'pages',
          label: item.label,
          subtitle: section.title,
          hint: item.path,
          icon: item.icon ?? 'grid',
          keywords: [item.label, section.title ?? '', item.path],
          run: async () => {
            handleMenuItemClick(item.path)
            await router.push(item.path)
          },
        })
      }
    }
  }

  return entries
})

const actionEntries = computed<PaletteEntry[]>(() => {
  const items: PaletteEntry[] = [
    {
      id: 'action:theme',
      group: 'actions',
      label: isDark.value ? t('commandPalette.switchToLight') : t('commandPalette.switchToDark'),
      icon: isDark.value ? 'sun' : 'moon',
      keywords: ['theme', 'dark', 'light', 'mode', '主题', '浅色', '深色', '暗黑'],
      run: () => {
        toggleTheme()
      },
    },
    {
      id: 'action:sidebar',
      group: 'actions',
      label: appStore.sidebarCollapsed
        ? t('commandPalette.expandSidebar')
        : t('commandPalette.collapseSidebar'),
      icon: appStore.sidebarCollapsed ? 'chevronRight' : 'chevronLeft',
      keywords: ['sidebar', 'rail', 'collapse', 'expand', '侧边栏', '折叠', '展开'],
      run: () => {
        appStore.toggleSidebar()
      },
    },
  ]

  for (const loc of availableLocales) {
    if (loc.code === locale.value) continue
    items.push({
      id: `action:locale:${loc.code}`,
      group: 'actions',
      label: t('commandPalette.switchLocale', { name: loc.name }),
      hint: loc.code.toUpperCase(),
      icon: 'globe',
      keywords: ['language', 'locale', 'i18n', '语言', loc.code, loc.name],
      run: async () => {
        await setLocale(loc.code)
        window.location.reload()
      },
    })
  }

  if (!authStore.isSimpleMode && authStore.user?.role === 'admin') {
    items.push({
      id: 'action:tour',
      group: 'actions',
      label: t('commandPalette.replayTour'),
      icon: 'lightbulb',
      keywords: ['tour', 'onboarding', 'guide', '引导', '教程'],
      run: () => {
        onboardingStore.replay()
      },
    })
  }

  return items
})

const flatResults = computed<PaletteEntry[]>(() => {
  const terms = query.value
    .trim()
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)

  const all = [...pageEntries.value, ...actionEntries.value]
  if (!terms.length) return all

  return all.filter((entry) => {
    const haystack = [entry.label, entry.subtitle ?? '', entry.hint ?? '', ...entry.keywords]
      .join(' ')
      .toLowerCase()
    return terms.every((term) => haystack.includes(term))
  })
})

const groupedResults = computed(() => {
  const pages = flatResults.value.filter((item) => item.group === 'pages')
  const actions = flatResults.value.filter((item) => item.group === 'actions')
  const groups: Array<{ key: string; label: string; items: PaletteEntry[] }> = []
  if (pages.length) {
    groups.push({ key: 'pages', label: t('commandPalette.pagesGroup'), items: pages })
  }
  if (actions.length) {
    groups.push({ key: 'actions', label: t('commandPalette.actionsGroup'), items: actions })
  }
  return groups
})

const activeItem = computed(() => flatResults.value[activeIndex.value] ?? null)

function close() {
  emit('update:open', false)
}

function clearQuery() {
  query.value = ''
  inputRef.value?.focus()
}

function setActive(index: number) {
  const total = flatResults.value.length
  if (!total) {
    activeIndex.value = 0
    return
  }
  activeIndex.value = ((index % total) + total) % total
  scrollActiveIntoView()
}

function moveActive(delta: number) {
  setActive(activeIndex.value + delta)
}

function setActiveById(id: string) {
  const idx = flatResults.value.findIndex((item) => item.id === id)
  if (idx !== -1) activeIndex.value = idx
}

function scrollActiveIntoView() {
  void nextTick(() => {
    const item = activeItem.value
    if (!item || !listRef.value) return
    const el = document.getElementById(`cmd-opt-${item.id}`)
    el?.scrollIntoView?.({ block: 'nearest' })
  })
}

async function runItem(item: PaletteEntry) {
  close()
  await item.run()
}

function handleEnter(event: KeyboardEvent) {
  if (event.isComposing) return
  const item = activeItem.value
  if (!item) return
  event.preventDefault()
  void runItem(item)
}

function handlePanelKeydown(event: KeyboardEvent) {
  if (event.key !== 'Tab' || !panelRef.value) return
  const focusable = Array.from(
    panelRef.value.querySelectorAll<HTMLElement>('input, button:not([disabled])')
  )
  if (!focusable.length) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  const active = document.activeElement as HTMLElement | null
  if (event.shiftKey) {
    if (!active || active === first) {
      event.preventDefault()
      last.focus()
    }
  } else if (!active || active === last) {
    event.preventDefault()
    first.focus()
  }
}

function handleGlobalKeydown(event: KeyboardEvent) {
  if (event.isComposing) return

  if ((event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'k') {
    if (event.defaultPrevented) return
    event.preventDefault()
    emit('update:open', !props.open)
    return
  }

  if (props.open && isTopmost.value && event.key === 'Escape' && !event.defaultPrevented) {
    event.preventDefault()
    close()
  }
}

watch(query, () => {
  activeIndex.value = 0
})

watch(
  () => props.open,
  async (isOpen) => {
    if (isOpen) {
      previousActiveElement = document.activeElement as HTMLElement | null
      query.value = ''
      activeIndex.value = 0
      await nextTick()
      inputRef.value?.focus()
    } else if (previousActiveElement && typeof previousActiveElement.focus === 'function') {
      previousActiveElement.focus()
      previousActiveElement = null
    }
  }
)

onMounted(() => {
  document.addEventListener('keydown', handleGlobalKeydown)
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', handleGlobalKeydown)
})
</script>

<style scoped>
.cmd-scrim {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: min(14vh, 112px) var(--ui-s-4) var(--ui-s-6);
  background-color: rgb(2 6 14 / 0.44);
  -webkit-backdrop-filter: blur(6px);
  backdrop-filter: blur(6px);
}

.cmd-panel {
  position: relative;
  display: flex;
  flex-direction: column;
  width: 100%;
  max-width: 560px;
  max-height: min(68vh, 520px);
  overflow: hidden;
  border-radius: var(--ui-r-xl);
  font-family: var(--ui-font);
  color: var(--ui-text);
  background-color: rgb(var(--ui-glass-strong-rgb) / calc(var(--ui-glass-strong-alpha) + 0.18));
  -webkit-backdrop-filter: blur(36px) saturate(var(--ui-glass-sat)) brightness(var(--ui-glass-bright));
  backdrop-filter: blur(36px) saturate(var(--ui-glass-sat)) brightness(var(--ui-glass-bright));
  box-shadow:
    inset 0 1px 1.5px var(--ui-edge-strong),
    0 0 0 1px var(--ui-hairline),
    var(--ui-shadow-4);
}

.cmd-input-row {
  display: flex;
  align-items: center;
  gap: var(--ui-s-2);
  padding: var(--ui-s-3) var(--ui-s-4);
  border-bottom: 1px solid var(--ui-line);
}

.cmd-input-icon {
  color: var(--ui-text-faint);
  flex-shrink: 0;
}

.cmd-input {
  flex: 1 1 auto;
  min-width: 0;
  border: 0;
  background: transparent;
  color: var(--ui-text);
  font-size: var(--ui-fs-md);
  line-height: 1.5;
  outline: none;
}

.cmd-input::placeholder {
  color: var(--ui-text-faint);
}

.cmd-clear-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: 0;
  border-radius: var(--ui-r-xs);
  background: transparent;
  color: var(--ui-text-faint);
  cursor: pointer;
}

.cmd-clear-btn:hover {
  color: var(--ui-text);
  background-color: var(--ui-line);
}

.cmd-list {
  flex: 1 1 auto;
  overflow-y: auto;
  padding: var(--ui-s-2);
}

.cmd-group + .cmd-group {
  margin-top: var(--ui-s-2);
  padding-top: var(--ui-s-2);
  border-top: 1px solid var(--ui-line-soft);
}

.cmd-group-title {
  margin: 0 0 4px;
  padding: 2px var(--ui-s-2);
  font-size: 11px;
  font-weight: 660;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--ui-text-faint);
}

.cmd-item {
  display: flex;
  align-items: center;
  gap: var(--ui-s-3);
  width: 100%;
  padding: 9px var(--ui-s-3);
  border: 0;
  border-radius: var(--ui-r-md);
  background: transparent;
  color: var(--ui-text-soft);
  font-family: var(--ui-font);
  font-size: var(--ui-fs-sm);
  text-align: left;
  cursor: pointer;
  transition:
    background-color var(--ui-dur-fast) var(--ui-ease-out),
    color var(--ui-dur-fast) var(--ui-ease-out);
}

.cmd-item.is-active {
  color: var(--ui-text);
  background-color: var(--ui-accent-soft);
  box-shadow: inset 0 0 0 1px var(--ui-accent-ring);
}

.cmd-item-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--ui-r-sm);
  background-color: var(--ui-line-soft);
  color: var(--ui-text-muted);
  flex-shrink: 0;
}

.cmd-item.is-active .cmd-item-icon {
  color: var(--ui-accent);
  background-color: rgb(var(--ui-glass-strong-rgb) / 0.55);
}

.cmd-item-main {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1 1 auto;
  gap: 1px;
}

.cmd-item-label {
  font-weight: 560;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cmd-item-sub {
  font-size: var(--ui-fs-2xs);
  color: var(--ui-text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cmd-item-hint {
  font-family: var(--ui-font-mono);
  font-size: 11px;
  color: var(--ui-text-faint);
  flex-shrink: 0;
}

.cmd-empty {
  padding: var(--ui-s-8) var(--ui-s-4);
  text-align: center;
}

.cmd-empty-title {
  margin: 0;
  font-size: var(--ui-fs-sm);
  font-weight: 600;
  color: var(--ui-text-soft);
}

.cmd-empty-hint {
  margin: 6px 0 0;
  font-size: var(--ui-fs-xs);
  color: var(--ui-text-faint);
}

.cmd-foot {
  display: flex;
  align-items: center;
  gap: var(--ui-s-4);
  padding: var(--ui-s-2) var(--ui-s-4);
  border-top: 1px solid var(--ui-line);
  font-size: 11px;
  color: var(--ui-text-faint);
}

.cmd-foot-hint {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.cmd-kbd {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 20px;
  height: 20px;
  padding: 0 5px;
  border-radius: 6px;
  font-family: var(--ui-font-mono);
  font-size: 10px;
  font-weight: 600;
  color: var(--ui-text-muted);
  background-color: var(--ui-line-soft);
  box-shadow: inset 0 0 0 1px var(--ui-line);
}

.cmd-palette-enter-active {
  transition: opacity 180ms var(--ui-ease-out);
}

.cmd-palette-leave-active {
  transition: opacity 140ms ease-in;
}

.cmd-palette-enter-from,
.cmd-palette-leave-to {
  opacity: 0;
}

.cmd-palette-enter-active .cmd-panel {
  transition: transform 200ms var(--ui-ease-out), opacity 180ms var(--ui-ease-out);
}

.cmd-palette-leave-active .cmd-panel {
  transition: transform 140ms ease-in, opacity 140ms ease-in;
}

.cmd-palette-enter-from .cmd-panel,
.cmd-palette-leave-to .cmd-panel {
  opacity: 0;
  transform: translate3d(0, -8px, 0) scale(0.97);
}

@media (prefers-reduced-motion: reduce) {
  .cmd-palette-enter-active,
  .cmd-palette-leave-active,
  .cmd-palette-enter-active .cmd-panel,
  .cmd-palette-leave-active .cmd-panel {
    transition-duration: 1ms;
  }
  .cmd-palette-enter-from .cmd-panel,
  .cmd-palette-leave-to .cmd-panel {
    transform: none;
  }
}
</style>
