<template>
  <div
    class="shell-bg"
    :data-ambient-mode="shouldMountWebGL ? 'webgl' : 'css'"
    aria-hidden="true"
  >
    <div class="shell-bg__blob shell-bg__blob--a" />
    <div class="shell-bg__blob shell-bg__blob--b" />
    <div class="shell-bg__blob shell-bg__blob--c" />
    <div class="shell-bg__blob shell-bg__blob--d" />
    <canvas
      v-if="shouldMountWebGL"
      ref="canvasRef"
      class="shell-bg__canvas"
      :class="{ 'is-ready': engineReady }"
    />
    <canvas
      v-if="shouldMountWebGL"
      ref="particlesCanvasRef"
      class="shell-bg__particles"
      :class="{ 'is-ready': engineReady }"
    />
    <div class="shell-bg__grid" />
    <div class="shell-bg__grain" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useTheme } from '@/composables/useTheme'
import { useAmbientBackdrop } from '@/composables/useAmbientBackdrop'
import type { AmbientBackdropController } from './createAmbientBackdropEngine'
import type { AmbientParticlesController } from './createAmbientParticlesEngine'

const props = withDefaults(
  defineProps<{
    routePath?: string
  }>(),
  {
    routePath: '/',
  },
)

const { isDark } = useTheme()
const { ambientEnabled } = useAmbientBackdrop()

const canvasRef = ref<HTMLCanvasElement | null>(null)
const particlesCanvasRef = ref<HTMLCanvasElement | null>(null)
const engineReady = ref(false)
const webglSupported = ref(false)
const reducedMotion = ref(false)
const isNarrowViewport = ref(false)

let controller: AmbientBackdropController | null = null
let particlesController: AmbientParticlesController | null = null
let resizeObserver: ResizeObserver | null = null
let motionMedia: MediaQueryList | undefined
let viewportMedia: MediaQueryList | undefined
let mountToken = 0
let unmounted = false

function probeWebGL(): boolean {
  if (typeof document === 'undefined') return false
  try {
    const probe = document.createElement('canvas')
    return Boolean(probe.getContext('webgl2') || probe.getContext('webgl'))
  } catch {
    return false
  }
}

function resolveMoodForPath(path: string): number {
  if (/^\/(subscriptions|payment|redeem|orders)/.test(path)) return 0.32
  if (/^\/model-plaza/.test(path)) return 0
  if (/^\/(dashboard|keys|usage|admin)/.test(path)) return -0.32
  return -0.2
}

const shouldMountWebGL = computed(
  () => ambientEnabled.value && !reducedMotion.value && !isNarrowViewport.value && webglSupported.value,
)

function disposeEngine() {
  mountToken++
  engineReady.value = false
  resizeObserver?.disconnect()
  resizeObserver = null
  controller?.dispose()
  controller = null
  particlesController?.dispose()
  particlesController = null
}

async function startEngine() {
  const token = ++mountToken
  await nextTick()
  if (unmounted || token !== mountToken || !shouldMountWebGL.value) return
  const canvas = canvasRef.value
  const particlesCanvas = particlesCanvasRef.value
  if (!canvas) return

  try {
    const [{ createAmbientBackdropEngine }, { createAmbientParticlesEngine }] = await Promise.all([
      import('./createAmbientBackdropEngine'),
      import('./createAmbientParticlesEngine'),
    ])
    if (unmounted || token !== mountToken || !shouldMountWebGL.value) return

    controller = createAmbientBackdropEngine({
      canvas,
      initialDark: isDark.value,
      initialMood: resolveMoodForPath(props.routePath),
    })

    if (particlesCanvas) {
      particlesController = createAmbientParticlesEngine({
        canvas: particlesCanvas,
        initialDark: isDark.value,
      })
    }

    engineReady.value = true

    const syncSize = () => {
      const w = canvas.parentElement?.clientWidth || window.innerWidth || 1440
      const h = canvas.parentElement?.clientHeight || window.innerHeight || 900
      controller?.resize(w, h)
      particlesController?.resize(w, h)
    }

    if (typeof ResizeObserver !== 'undefined' && canvas.parentElement) {
      resizeObserver = new ResizeObserver(syncSize)
      resizeObserver.observe(canvas.parentElement)
    }
  } catch {
    engineReady.value = false
    webglSupported.value = false
    disposeEngine()
  }
}

watch(shouldMountWebGL, (active) => {
  if (active) {
    void startEngine()
  } else {
    disposeEngine()
  }
})

watch(isDark, (dark) => {
  controller?.setDark(dark)
  particlesController?.setDark(dark)
})

watch(
  () => props.routePath,
  (path) => {
    controller?.setMood(resolveMoodForPath(path))
  },
)

function syncMediaPreferences() {
  reducedMotion.value = motionMedia?.matches ?? false
  isNarrowViewport.value = viewportMedia?.matches ?? (typeof window !== 'undefined' && window.innerWidth < 768)
}

onMounted(() => {
  webglSupported.value = probeWebGL()
  if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
    motionMedia = window.matchMedia('(prefers-reduced-motion: reduce)')
    viewportMedia = window.matchMedia('(max-width: 767px)')
    motionMedia.addEventListener?.('change', syncMediaPreferences)
    viewportMedia.addEventListener?.('change', syncMediaPreferences)
  }
  syncMediaPreferences()
  if (shouldMountWebGL.value) {
    void startEngine()
  }
})

onBeforeUnmount(() => {
  unmounted = true
  motionMedia?.removeEventListener?.('change', syncMediaPreferences)
  viewportMedia?.removeEventListener?.('change', syncMediaPreferences)
  disposeEngine()
})
</script>

<style scoped>
.shell-bg {
  position: fixed;
  inset: 0;
  z-index: 0;
  overflow: hidden;
  pointer-events: none;
  contain: strict;
  background:
    radial-gradient(120% 80% at 12% 0%, var(--ui-canvas-tint-1) 0%, transparent 46%),
    radial-gradient(110% 70% at 92% 8%, var(--ui-canvas-tint-2) 0%, transparent 44%),
    radial-gradient(120% 90% at 50% 104%, var(--ui-canvas-tint-3) 0%, transparent 52%),
    linear-gradient(168deg, var(--ui-canvas) 0%, var(--ui-canvas-deep) 100%);
  opacity: 1;
}

.shell-bg__canvas,
.shell-bg__particles {
  position: absolute;
  inset: 0;
  display: block;
  width: 100%;
  height: 100%;
  opacity: 0;
  transition: opacity 520ms cubic-bezier(0.16, 1, 0.3, 1);
}

.shell-bg__canvas {
  z-index: 1;
}

.shell-bg__particles {
  z-index: 2;
}

.shell-bg__canvas.is-ready,
.shell-bg__particles.is-ready {
  opacity: 1;
}

.shell-bg__blob {
  position: absolute;
  border-radius: 50%;
  filter: blur(48px);
  opacity: var(--ui-blob-opacity, 0.74);
  will-change: transform;
}

.shell-bg__blob--a {
  width: 46vw;
  height: 46vw;
  top: -14vw;
  left: -8vw;
  background: radial-gradient(circle, var(--ui-canvas-tint-1) 0%, transparent 68%);
  animation: shell-drift-a 34s var(--ui-ease-in-out) infinite alternate;
}

.shell-bg__blob--b {
  width: 40vw;
  height: 40vw;
  top: 4vw;
  right: -10vw;
  background: radial-gradient(circle, var(--ui-canvas-tint-2) 0%, transparent 68%);
  animation: shell-drift-b 42s var(--ui-ease-in-out) infinite alternate;
}

.shell-bg__blob--c {
  width: 52vw;
  height: 52vw;
  bottom: -22vw;
  left: 18vw;
  background: radial-gradient(circle, var(--ui-canvas-tint-3) 0%, transparent 66%);
  animation: shell-drift-c 48s var(--ui-ease-in-out) infinite alternate;
}

.shell-bg__blob--d {
  width: 30vw;
  height: 30vw;
  bottom: -8vw;
  right: 4vw;
  background: radial-gradient(circle, var(--ui-canvas-tint-4) 0%, transparent 70%);
  opacity: calc(var(--ui-blob-opacity, 0.74) * 0.6);
  animation: shell-drift-d 38s var(--ui-ease-in-out) infinite alternate;
}

/* 极细网格，只在左上角淡出，给玻璃提供可折射的细节 */
.shell-bg__grid {
  position: absolute;
  inset: 0;
  z-index: 3;
  background-image:
    linear-gradient(to right, var(--ui-line-soft) 1px, transparent 1px),
    linear-gradient(to bottom, var(--ui-line-soft) 1px, transparent 1px);
  background-size: 56px 56px;
  -webkit-mask-image: radial-gradient(120% 90% at 30% 0%, #000 0%, transparent 72%);
  mask-image: radial-gradient(120% 90% at 30% 0%, #000 0%, transparent 72%);
  opacity: 0.48;
}

/* 颗粒：让大面积渐变不出现色带 */
.shell-bg__grain {
  position: absolute;
  inset: 0;
  z-index: 4;
  opacity: 0.14;
  mix-blend-mode: overlay;
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='160' height='160'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.82' numOctaves='3' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='160' height='160' filter='url(%23n)' opacity='0.55'/%3E%3C/svg%3E");
}

@keyframes shell-drift-a {
  from {
    transform: translate3d(0, 0, 0) scale(1);
  }
  to {
    transform: translate3d(6vw, 5vh, 0) scale(1.12);
  }
}
@keyframes shell-drift-b {
  from {
    transform: translate3d(0, 0, 0) scale(1.06);
  }
  to {
    transform: translate3d(-7vw, 7vh, 0) scale(0.94);
  }
}
@keyframes shell-drift-c {
  from {
    transform: translate3d(0, 0, 0) scale(1);
  }
  to {
    transform: translate3d(-5vw, -6vh, 0) scale(1.1);
  }
}
@keyframes shell-drift-d {
  from {
    transform: translate3d(0, 0, 0) scale(1.1);
  }
  to {
    transform: translate3d(4vw, -5vh, 0) scale(0.96);
  }
}

@media (prefers-reduced-motion: reduce) {
  .shell-bg__blob {
    animation: none;
  }
}

@media (max-width: 767px) {
  .shell-bg__blob {
    animation: none;
  }
}
</style>
