<template>
  <Teleport to="body">
    <div
      class="pointer-events-none fixed right-4 top-4 z-[9999] w-[min(28rem,calc(100vw-2rem))] space-y-3"
      aria-live="polite"
      aria-atomic="true"
    >
      <TransitionGroup
        enter-active-class="toast-enter-active"
        enter-from-class="toast-enter-from"
        enter-to-class="toast-enter-to"
        leave-active-class="toast-leave-active"
        leave-from-class="toast-leave-from"
        leave-to-class="toast-leave-to"
        move-class="toast-move"
      >
        <div
          v-for="toast in toasts"
          :key="toast.id"
          :role="toast.type === 'error' || toast.type === 'warning' ? 'alert' : 'status'"
          :class="[
            'toast-card pointer-events-auto min-w-0 w-full overflow-hidden rounded-2xl',
            `toast-card--${toast.type}`
          ]"
          @mouseenter="pauseToast(toast.id)"
          @mouseleave="resumeToast(toast.id)"
          @focusin="pauseToast(toast.id)"
          @focusout="handleFocusOut($event, toast.id)"
        >
          <div class="px-4 py-3.5">
            <div class="flex items-start gap-3">
              <!-- Icon -->
              <div :class="['toast-icon-badge mt-0.5 flex-shrink-0', getIconBadgeClass(toast.type)]">
                <Icon
                  :name="getToastIconName(toast.type)"
                  size="md"
                  :class="getIconColor(toast.type)"
                  aria-hidden="true"
                />
              </div>

              <!-- Content -->
              <div class="min-w-0 flex-1 [overflow-wrap:anywhere]">
                <p v-if="toast.title" class="toast-title text-sm font-semibold">
                  {{ toast.title }}
                </p>
                <p
                  :class="[
                    'text-sm leading-relaxed',
                    toast.title ? 'toast-message--sub mt-1' : 'toast-message'
                  ]"
                >
                  {{ toast.message }}
                </p>
              </div>

              <!-- Close button -->
              <button
                type="button"
                @click="removeToast(toast.id)"
                class="toast-close -m-1 flex-shrink-0 rounded-lg p-1.5 transition-colors"
                aria-label="Close notification"
              >
                <Icon name="x" size="sm" />
              </button>
            </div>
          </div>

          <!-- Progress bar -->
          <div v-if="toast.duration" class="toast-progress-track h-1">
            <div
              :class="['h-full toast-progress', getProgressBarColor(toast.type)]"
              :style="{
                animationDuration: `${toast.duration}ms`,
                animationPlayState: toast.paused ? 'paused' : 'running'
              }"
            ></div>
          </div>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'

const appStore = useAppStore()

const toasts = computed(() => appStore.toasts)

const getToastIconName = (type: string): 'checkCircle' | 'xCircle' | 'exclamationTriangle' | 'infoCircle' => {
  switch (type) {
    case 'success':
      return 'checkCircle'
    case 'error':
      return 'xCircle'
    case 'warning':
      return 'exclamationTriangle'
    case 'info':
    default:
      return 'infoCircle'
  }
}

const getIconColor = (type: string): string => {
  const colors: Record<string, string> = {
    success: 'text-green-500',
    error: 'text-red-500',
    warning: 'text-yellow-500',
    info: 'text-blue-500'
  }
  return colors[type] || colors.info
}

const getIconBadgeClass = (type: string): string => {
  const classes: Record<string, string> = {
    success: 'bg-emerald-500/12 dark:bg-emerald-400/15',
    error: 'bg-red-500/12 dark:bg-red-400/15',
    warning: 'bg-amber-500/15 dark:bg-amber-400/15',
    info: 'bg-blue-500/12 dark:bg-blue-400/15'
  }
  return classes[type] || classes.info
}

const getProgressBarColor = (type: string): string => {
  const colors: Record<string, string> = {
    success: 'bg-emerald-500',
    error: 'bg-red-500',
    warning: 'bg-amber-500',
    info: 'bg-blue-500'
  }
  return colors[type] || colors.info
}

const pauseToast = (id: string) => {
  appStore.pauseToast(id)
}

const resumeToast = (id: string) => {
  appStore.resumeToast(id)
}

const handleFocusOut = (event: FocusEvent, id: string) => {
  const container = event.currentTarget as HTMLElement | null
  const nextTarget = event.relatedTarget as Node | null
  if (container && nextTarget && container.contains(nextTarget)) return
  appStore.resumeToast(id)
}

const removeToast = (id: string) => {
  appStore.hideToast(id)
}
</script>

<style scoped>
.toast-card {
  position: relative;
  background-color: rgb(var(--ui-glass-strong-rgb, 255 255 255) / calc(var(--ui-glass-strong-alpha, 0.74) + 0.14));
  -webkit-backdrop-filter: blur(22px) saturate(180%);
  backdrop-filter: blur(22px) saturate(180%);
  box-shadow:
    inset 0 1px 1px var(--ui-edge-strong, rgb(255 255 255 / 0.85)),
    0 0 0 1px var(--ui-hairline, rgb(15 23 42 / 0.12)),
    var(--ui-shadow-3, 0 14px 34px -12px rgb(15 23 42 / 0.2));
}

.toast-card::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 3.5px;
  background-color: var(--ui-info, #2563eb);
}

.toast-card--success::before {
  background-color: var(--ui-success, #059669);
}

.toast-card--error::before {
  background-color: var(--ui-danger, #dc2626);
}

.toast-card--warning::before {
  background-color: var(--ui-warning, #d97706);
}

.toast-card--info::before {
  background-color: var(--ui-info, #2563eb);
}

.toast-icon-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 10px;
}

.toast-title,
.toast-message {
  color: var(--ui-text, #0b1220);
}

.toast-message--sub {
  color: var(--ui-text-muted, #4a5b73);
}

.toast-close {
  color: var(--ui-text-faint, #6b7d95);
}

.toast-close:hover {
  color: var(--ui-text, #0b1220);
  background-color: var(--ui-line, rgb(15 23 42 / 0.08));
}

.toast-progress-track {
  background-color: var(--ui-line-soft, rgb(15 23 42 / 0.06));
}

.toast-progress {
  width: 100%;
  transform-origin: left center;
  will-change: transform;
  animation-name: toast-progress-shrink;
  animation-timing-function: linear;
  animation-fill-mode: forwards;
}

@keyframes toast-progress-shrink {
  from {
    transform: scaleX(1);
  }
  to {
    transform: scaleX(0);
  }
}

.toast-enter-active {
  transition:
    opacity 240ms var(--ui-ease-out, cubic-bezier(0.16, 1, 0.3, 1)),
    transform 260ms var(--ui-ease-out, cubic-bezier(0.16, 1, 0.3, 1));
}

.toast-leave-active {
  transition:
    opacity 180ms ease-in,
    transform 180ms ease-in;
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translate3d(24px, 0, 0) scale(0.96);
}

.toast-enter-to,
.toast-leave-from {
  opacity: 1;
  transform: translate3d(0, 0, 0) scale(1);
}

.toast-move {
  transition: transform 220ms var(--ui-ease-out, cubic-bezier(0.16, 1, 0.3, 1));
}

@media (prefers-reduced-motion: reduce) {
  .toast-enter-active,
  .toast-leave-active,
  .toast-move {
    transition-duration: 1ms;
  }
  .toast-enter-from,
  .toast-leave-to {
    transform: none;
  }
}
</style>
