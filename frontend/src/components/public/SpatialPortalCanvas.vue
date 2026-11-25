<template>
  <div
    ref="stageRef"
    class="sp-stage"
    :class="[
      dark ? 'is-dark' : 'is-light',
      hoverSide ? `is-hover-${hoverSide}` : '',
      { 'is-fallback': fallbackActive, 'is-ready': engineReady },
    ]"
    aria-hidden="true"
  >
    <div class="sp-fallback" />
    <canvas v-if="!fallbackActive" ref="canvasRef" class="sp-canvas" />
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { PortalEngineController, PortalHoverSide } from './three/createPortalEngine'

const props = defineProps<{
  progress: number
  hoverSide: PortalHoverSide
  dark: boolean
  reducedMotion: boolean
}>()

const emit = defineEmits<{
  ready: []
  fallback: []
}>()

const stageRef = ref<HTMLElement | null>(null)
const canvasRef = ref<HTMLCanvasElement | null>(null)
const fallbackActive = ref(false)
const engineReady = ref(false)

let controller: PortalEngineController | null = null
let resizeObserver: ResizeObserver | null = null
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

watch(
  () => props.progress,
  (val) => {
    controller?.setProgress(val)
  },
)

watch(
  () => props.hoverSide,
  (val) => {
    controller?.setHoverSide(val)
  },
)

watch(
  () => props.dark,
  (val) => {
    controller?.setDark(val)
  },
)

watch(
  () => props.reducedMotion,
  (val) => {
    controller?.setReducedMotion(val)
  },
)

onMounted(async () => {
  const canvas = canvasRef.value
  if (!canvas || !probeWebGL()) {
    fallbackActive.value = true
    emit('fallback')
    return
  }

  try {
    const { createPortalEngine } = await import('./three/createPortalEngine')
    if (unmounted) return

    controller = createPortalEngine({
      canvas,
      initialDark: props.dark,
      reducedMotion: props.reducedMotion,
    })
    controller.setProgress(props.progress)
    controller.setHoverSide(props.hoverSide)

    engineReady.value = true
    emit('ready')

    const syncSize = () => {
      const target = stageRef.value
      if (!target || !controller) return
      controller.resize(target.clientWidth || window.innerWidth, target.clientHeight || window.innerHeight)
    }

    if (typeof ResizeObserver !== 'undefined' && stageRef.value) {
      resizeObserver = new ResizeObserver(syncSize)
      resizeObserver.observe(stageRef.value)
    } else {
      window.addEventListener('resize', syncSize, { passive: true })
    }
  } catch {
    fallbackActive.value = true
    emit('fallback')
  }
})

onBeforeUnmount(() => {
  unmounted = true
  resizeObserver?.disconnect()
  resizeObserver = null
  controller?.dispose()
  controller = null
})
</script>

<style scoped>
.sp-stage {
  position: fixed;
  inset: 0;
  z-index: 0;
  overflow: hidden;
  contain: layout paint size;
  pointer-events: none;
  background: #03050a;
  transition: background-color 480ms cubic-bezier(0.16, 1, 0.3, 1);
}

.sp-stage.is-light {
  background: #edf1f7;
}

.sp-canvas {
  position: relative;
  z-index: 1;
  display: block;
  width: 100%;
  height: 100%;
  opacity: 0;
  transition: opacity 520ms cubic-bezier(0.16, 1, 0.3, 1);
}

.sp-stage.is-ready .sp-canvas {
  opacity: 1;
}

.sp-fallback {
  position: absolute;
  inset: 0;
  z-index: 0;
  opacity: 1;
  transition: opacity 520ms ease;
  background:
    radial-gradient(65% 58% at 22% 48%, rgb(143 181 168 / 0.16) 0%, transparent 62%),
    radial-gradient(62% 56% at 78% 48%, rgb(240 194 123 / 0.14) 0%, transparent 60%),
    radial-gradient(48% 42% at 50% 45%, rgb(148 163 184 / 0.14) 0%, transparent 66%),
    linear-gradient(170deg, #03050a 0%, #070c18 55%, #03050a 100%);
}

.sp-stage.is-light .sp-fallback {
  background:
    radial-gradient(68% 60% at 20% 45%, rgb(143 181 168 / 0.24) 0%, transparent 62%),
    radial-gradient(65% 58% at 80% 45%, rgb(240 194 123 / 0.22) 0%, transparent 60%),
    radial-gradient(50% 45% at 50% 40%, rgb(147 197 253 / 0.32) 0%, transparent 66%),
    linear-gradient(170deg, #edf1f7 0%, #dce4f1 100%);
}

.sp-stage.is-hover-aicode .sp-fallback {
  filter: saturate(1.15);
}

.sp-stage.is-ready .sp-fallback {
  opacity: 0;
}
</style>
