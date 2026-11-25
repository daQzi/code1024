<template>
  <Teleport to="body">
    <canvas
      v-if="isEnabled"
      ref="canvasRef"
      class="sf-fx-canvas"
      data-testid="starfighter-fx-canvas"
      aria-hidden="true"
    />
    <div
      v-if="isEnabled"
      ref="cursorRef"
      class="sf-cursor"
      data-testid="starfighter-cursor"
      aria-hidden="true"
    >
      <svg
        class="sf-jet-svg"
        viewBox="-30 -18 32 36"
        width="32"
        height="36"
        fill="none"
        xmlns="http://www.w3.org/2000/svg"
      >
        <defs>
          <linearGradient id="sf-wing-grad" x1="-26" y1="0" x2="0" y2="0" gradientUnits="userSpaceOnUse">
            <stop offset="0%" stop-color="#0b1324" />
            <stop offset="55%" stop-color="#172947" />
            <stop offset="100%" stop-color="#38e8d5" />
          </linearGradient>
          <linearGradient id="sf-edge-grad" x1="-24" y1="-12" x2="0" y2="0" gradientUnits="userSpaceOnUse">
            <stop offset="0%" stop-color="#f0c27b" stop-opacity="0.9" />
            <stop offset="60%" stop-color="#38e8d5" stop-opacity="0.95" />
            <stop offset="100%" stop-color="#ffffff" stop-opacity="1" />
          </linearGradient>
          <linearGradient id="sf-flame-grad" x1="-29" y1="0" x2="-17" y2="0" gradientUnits="userSpaceOnUse">
            <stop offset="0%" stop-color="#38e8d5" stop-opacity="0" />
            <stop offset="55%" stop-color="#38e8d5" stop-opacity="0.85" />
            <stop offset="100%" stop-color="#f0c27b" stop-opacity="1" />
          </linearGradient>
        </defs>

        <!-- Target lock ring when hovering interactive buttons/links -->
        <circle class="sf-lock-ring" cx="0" cy="0" r="8.5" stroke="#38e8d5" stroke-width="0.9" stroke-dasharray="3 2.5" />

        <!-- Dynamic ion thruster flame behind tail (-18 -> -29) -->
        <path
          ref="flameRef"
          class="sf-jet-flame"
          d="M -18 -2.8 L -29 0 L -18 2.8 Z"
          fill="url(#sf-flame-grad)"
        />

        <!-- Swept delta hull with nose tip strictly at (0, 0) -->
        <path
          d="M 0 0 L -21 -11.5 L -16.5 -3 L -22.5 0 L -16.5 3 L -21 11.5 Z"
          fill="url(#sf-wing-grad)"
          stroke="url(#sf-edge-grad)"
          stroke-width="1.25"
          stroke-linejoin="round"
        />

        <!-- Inner cockpit / energy spine -->
        <path
          d="M -2.5 0 L -13.5 -3.6 L -15.5 0 L -13.5 3.6 Z"
          fill="#f0c27b"
          fill-opacity="0.85"
        />
        <line x1="-1.5" y1="0" x2="-15" y2="0" stroke="#e2f8ff" stroke-width="0.9" stroke-opacity="0.9" />

        <!-- Precision laser tip at (0, 0) -->
        <circle cx="0" cy="0" r="1.4" fill="#ffffff" />
      </svg>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useAmbientBackdrop } from '@/composables/useAmbientBackdrop'

interface TrailNode {
  x: number
  y: number
  life: number
  maxLife: number
  speed: number
}

interface Spark {
  x: number
  y: number
  vx: number
  vy: number
  life: number
  maxLife: number
  size: number
  gold: boolean
}

interface Shockwave {
  x: number
  y: number
  life: number
  maxLife: number
  maxRadius: number
}

const IDLE_REST_ANGLE = -Math.PI * 0.75 // Points up-left when resting
const MAX_TRAIL = 18
const MAX_SPARKS = 52
const MAX_WAVES = 8

const { cursorFxEnabled } = useAmbientBackdrop()

const finePointer = ref(false)
const reducedMotion = ref(false)
const canvasRef = ref<HTMLCanvasElement | null>(null)
const cursorRef = ref<HTMLDivElement | null>(null)
const flameRef = ref<SVGPathElement | null>(null)

const isEnabled = computed(
  () => cursorFxEnabled.value && finePointer.value && !reducedMotion.value,
)

let pointerMedia: MediaQueryList | undefined
let motionMedia: MediaQueryList | undefined
let rafId: number | null = null
let lastFrameTime = 0
let posX = -100
let posY = -100
let lastMoveX = -100
let lastMoveY = -100
let currentAngle = IDLE_REST_ANGLE
let targetAngle = IDLE_REST_ANGLE
let instSpeed = 0
let smoothedSpeed = 0
let lastMoveAt = 0
let cursorVisible = false
let overTextInput = false
let overInteractive = false
let ctx: CanvasRenderingContext2D | null = null
let dpr = 1
let viewW = 0
let viewH = 0
let listenersAttached = false

const trails: TrailNode[] = []
const sparks: Spark[] = []
const waves: Shockwave[] = []

function shortestAngleDiff(from: number, to: number): number {
  let diff = (to - from + Math.PI) % (Math.PI * 2)
  if (diff < 0) diff += Math.PI * 2
  return diff - Math.PI
}

function syncRootCursorClass() {
  if (typeof document === 'undefined') return
  const active = isEnabled.value && cursorVisible && !overTextInput
  document.documentElement.classList.toggle('has-starfighter-cursor', active)
  const el = cursorRef.value
  if (el) {
    el.classList.toggle('is-visible', active)
    el.classList.toggle('is-interactive', overInteractive)
  }
}

function resizeCanvas() {
  const canvas = canvasRef.value
  if (!canvas || typeof window === 'undefined') return
  viewW = window.innerWidth || 1440
  viewH = window.innerHeight || 900
  dpr = Math.min(window.devicePixelRatio || 1, 1.5)
  canvas.width = Math.max(1, Math.round(viewW * dpr))
  canvas.height = Math.max(1, Math.round(viewH * dpr))
  ctx = canvas.getContext('2d')
}

function clearEffects() {
  trails.length = 0
  sparks.length = 0
  waves.length = 0
  if (ctx && canvasRef.value) {
    ctx.setTransform(1, 0, 0, 1, 0, 0)
    ctx.clearRect(0, 0, canvasRef.value.width, canvasRef.value.height)
  }
}

function stopLoop() {
  if (rafId !== null && typeof window !== 'undefined') {
    window.cancelAnimationFrame(rafId)
    rafId = null
  }
}

function ensureLoop() {
  if (!isEnabled.value || rafId !== null || typeof window === 'undefined') return
  lastFrameTime = performance.now()
  rafId = window.requestAnimationFrame(tick)
}

function spawnSpark(x: number, y: number, vx: number, vy: number, maxLife: number, size: number, gold: boolean) {
  if (sparks.length >= MAX_SPARKS) {
    sparks.shift()
  }
  sparks.push({ x, y, vx, vy, life: 0, maxLife, size, gold })
}

function handlePointerMove(event: PointerEvent) {
  if (!isEnabled.value || event.pointerType === 'touch') return

  const targetEl = event.target instanceof Element ? event.target : null
  overTextInput = Boolean(
    targetEl?.closest('input, textarea, select, [contenteditable="true"], [contenteditable=""], iframe'),
  )
  overInteractive = Boolean(
    !overTextInput &&
      targetEl?.closest('a, button, [role="button"], [role="tab"], [role="menuitem"], summary'),
  )

  const x = event.clientX
  const y = event.clientY
  const prevX = cursorVisible ? lastMoveX : x
  const prevY = cursorVisible ? lastMoveY : y
  const dx = x - prevX
  const dy = y - prevY
  const dist = Math.hypot(dx, dy)

  posX = x
  posY = y
  lastMoveX = x
  lastMoveY = y
  cursorVisible = true
  lastMoveAt = performance.now()

  if (dist > 1.4 && !overTextInput) {
    targetAngle = Math.atan2(dy, dx)
    instSpeed = Math.min(85, dist)

    const tailX = x - Math.cos(currentAngle) * 18
    const tailY = y - Math.sin(currentAngle) * 18
    trails.push({ x: tailX, y: tailY, life: 0, maxLife: 0.36, speed: dist })
    if (trails.length > MAX_TRAIL) trails.shift()

    if (dist > 3.2) {
      const count = dist > 20 ? 2 : 1
      for (let i = 0; i < count; i++) {
        const spread = (Math.random() - 0.5) * 0.9
        const backAngle = currentAngle + Math.PI + spread
        const spd = 22 + Math.random() * 44
        spawnSpark(
          tailX + (Math.random() - 0.5) * 4,
          tailY + (Math.random() - 0.5) * 4,
          Math.cos(backAngle) * spd,
          Math.sin(backAngle) * spd,
          0.28 + Math.random() * 0.22,
          1.1 + Math.random() * 1.4,
          Math.random() > 0.62,
        )
      }
    }
  }

  syncRootCursorClass()
  ensureLoop()
}

function handlePointerDown(event: PointerEvent) {
  if (!isEnabled.value || event.pointerType === 'touch' || event.button !== 0 || overTextInput) return
  const x = event.clientX
  const y = event.clientY
  posX = x
  posY = y
  cursorVisible = true
  lastMoveAt = performance.now()

  if (waves.length >= MAX_WAVES) waves.shift()
  waves.push({ x, y, life: 0, maxLife: 0.42, maxRadius: 42 })

  for (let i = 0; i < 10; i++) {
    const theta = (Math.PI * 2 * i) / 10 + (Math.random() - 0.5) * 0.25
    const spd = 56 + Math.random() * 68
    spawnSpark(
      x,
      y,
      Math.cos(theta) * spd,
      Math.sin(theta) * spd,
      0.34 + Math.random() * 0.18,
      1.3 + Math.random() * 1.3,
      i % 2 === 1,
    )
  }

  syncRootCursorClass()
  ensureLoop()
}

function handlePointerLeave() {
  cursorVisible = false
  overInteractive = false
  syncRootCursorClass()
}

function handleVisibilityChange() {
  if (document.hidden) {
    stopLoop()
    clearEffects()
  }
}

function tick(now: number) {
  rafId = null
  if (!isEnabled.value || (typeof document !== 'undefined' && document.hidden)) return

  const dt = Math.min(0.05, Math.max(0.001, (now - lastFrameTime) * 0.001))
  lastFrameTime = now

  const timeSinceMove = now - lastMoveAt
  if (timeSinceMove > 850) {
    targetAngle = IDLE_REST_ANGLE
    instSpeed = 0
  } else {
    instSpeed *= Math.max(0, 1 - dt * 10)
  }

  smoothedSpeed += (instSpeed - smoothedSpeed) * Math.min(1, dt * 14)
  const angleDelta = shortestAngleDiff(currentAngle, targetAngle)
  currentAngle += angleDelta * Math.min(1, dt * (timeSinceMove > 850 ? 6.5 : 16))

  const cursorEl = cursorRef.value
  if (cursorEl && cursorVisible && !overTextInput) {
    cursorEl.style.transform = `translate3d(${posX.toFixed(1)}px, ${posY.toFixed(1)}px, 0) rotate(${currentAngle.toFixed(4)}rad)`
  }

  if (flameRef.value) {
    const flameScale = 0.72 + Math.min(1.45, smoothedSpeed * 0.038)
    flameRef.value.style.transform = `scaleX(${flameScale.toFixed(2)})`
  }

  // Update & draw particles/contrails/waves on 2D canvas
  if (ctx) {
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, viewW, viewH)

    // 1. Contrail ribbon
    for (let i = trails.length - 1; i >= 0; i--) {
      trails[i].life += dt
      if (trails[i].life >= trails[i].maxLife) {
        trails.splice(i, 1)
      }
    }

    if (trails.length >= 2) {
      ctx.lineCap = 'round'
      ctx.lineJoin = 'round'
      for (let i = 1; i < trails.length; i++) {
        const p0 = trails[i - 1]
        const p1 = trails[i]
        const t = i / trails.length
        const fade = Math.max(0, 1 - p1.life / p1.maxLife)
        const alpha = fade * t * 0.68
        if (alpha <= 0.01) continue
        ctx.beginPath()
        ctx.moveTo(p0.x, p0.y)
        ctx.lineTo(p1.x, p1.y)
        ctx.lineWidth = 0.8 + t * 2.2
        ctx.strokeStyle =
          i > trails.length - 4
            ? `rgba(240, 194, 123, ${alpha.toFixed(3)})`
            : `rgba(56, 232, 213, ${alpha.toFixed(3)})`
        ctx.stroke()
      }
    }

    // 2. Exhaust & impact sparks
    for (let i = sparks.length - 1; i >= 0; i--) {
      const s = sparks[i]
      s.life += dt
      if (s.life >= s.maxLife) {
        sparks.splice(i, 1)
        continue
      }
      s.vx *= Math.max(0, 1 - dt * 3.5)
      s.vy *= Math.max(0, 1 - dt * 3.5)
      s.x += s.vx * dt
      s.y += s.vy * dt
      const alpha = (1 - s.life / s.maxLife) * 0.9
      ctx.beginPath()
      ctx.arc(s.x, s.y, s.size * (1 - (s.life / s.maxLife) * 0.4), 0, Math.PI * 2)
      ctx.fillStyle = s.gold
        ? `rgba(240, 194, 123, ${alpha.toFixed(3)})`
        : `rgba(56, 232, 213, ${alpha.toFixed(3)})`
      ctx.fill()
    }

    // 3. Click quantum shockwaves
    for (let i = waves.length - 1; i >= 0; i--) {
      const w = waves[i]
      w.life += dt
      if (w.life >= w.maxLife) {
        waves.splice(i, 1)
        continue
      }
      const p = w.life / w.maxLife
      const eased = 1 - Math.pow(1 - p, 3)
      const r = 5 + eased * (w.maxRadius - 5)
      const alpha = (1 - p) * 0.72
      ctx.beginPath()
      ctx.arc(w.x, w.y, r, 0, Math.PI * 2)
      ctx.lineWidth = 1.6 * (1 - p * 0.6)
      ctx.strokeStyle = `rgba(56, 232, 213, ${alpha.toFixed(3)})`
      ctx.stroke()

      if (p < 0.72) {
        const innerAlpha = (1 - p / 0.72) * 0.55
        ctx.beginPath()
        ctx.arc(w.x, w.y, r * 0.58, 0, Math.PI * 2)
        ctx.lineWidth = 1
        ctx.strokeStyle = `rgba(240, 194, 123, ${innerAlpha.toFixed(3)})`
        ctx.stroke()
      }
    }
  }

  const stillAnimating =
    trails.length > 0 ||
    sparks.length > 0 ||
    waves.length > 0 ||
    Math.abs(angleDelta) > 0.008 ||
    smoothedSpeed > 0.25 ||
    timeSinceMove < 960

  if (stillAnimating) {
    rafId = window.requestAnimationFrame(tick)
  }
}

function attachListeners() {
  if (listenersAttached || typeof window === 'undefined') return
  window.addEventListener('pointermove', handlePointerMove, { passive: true })
  window.addEventListener('pointerdown', handlePointerDown, { passive: true })
  window.addEventListener('resize', resizeCanvas, { passive: true })
  window.addEventListener('blur', handlePointerLeave)
  document.addEventListener('mouseleave', handlePointerLeave)
  document.addEventListener('visibilitychange', handleVisibilityChange)
  listenersAttached = true
}

function detachListeners() {
  if (!listenersAttached || typeof window === 'undefined') return
  window.removeEventListener('pointermove', handlePointerMove)
  window.removeEventListener('pointerdown', handlePointerDown)
  window.removeEventListener('resize', resizeCanvas)
  window.removeEventListener('blur', handlePointerLeave)
  document.removeEventListener('mouseleave', handlePointerLeave)
  document.removeEventListener('visibilitychange', handleVisibilityChange)
  listenersAttached = false
}

function syncCapabilities() {
  finePointer.value = pointerMedia?.matches ?? false
  reducedMotion.value = motionMedia?.matches ?? false
}

watch(isEnabled, async (enabled) => {
  if (enabled) {
    await nextTick()
    resizeCanvas()
    attachListeners()
    syncRootCursorClass()
  } else {
    cursorVisible = false
    overTextInput = false
    overInteractive = false
    stopLoop()
    clearEffects()
    detachListeners()
    syncRootCursorClass()
  }
})

onMounted(async () => {
  if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
    pointerMedia = window.matchMedia('(hover: hover) and (pointer: fine)')
    motionMedia = window.matchMedia('(prefers-reduced-motion: reduce)')
    pointerMedia.addEventListener?.('change', syncCapabilities)
    motionMedia.addEventListener?.('change', syncCapabilities)
  }
  syncCapabilities()
  if (isEnabled.value) {
    await nextTick()
    resizeCanvas()
    attachListeners()
  }
})

onBeforeUnmount(() => {
  stopLoop()
  clearEffects()
  detachListeners()
  pointerMedia?.removeEventListener?.('change', syncCapabilities)
  motionMedia?.removeEventListener?.('change', syncCapabilities)
  if (typeof document !== 'undefined') {
    document.documentElement.classList.remove('has-starfighter-cursor')
  }
})
</script>

<style scoped>
.sf-fx-canvas {
  position: fixed;
  inset: 0;
  z-index: 99998;
  width: 100%;
  height: 100%;
  pointer-events: none;
}

.sf-cursor {
  position: fixed;
  top: -18px;
  left: -30px;
  width: 32px;
  height: 36px;
  z-index: 99999;
  pointer-events: none;
  transform-origin: 30px 18px;
  opacity: 0;
  will-change: transform, opacity;
  transition: opacity 150ms ease;
  filter: drop-shadow(0 0 8px rgb(56 232 213 / 0.55));
}

.sf-cursor.is-visible {
  opacity: 1;
}

.sf-jet-svg {
  display: block;
  overflow: visible;
}

.sf-jet-flame {
  transform-origin: -18px 0px;
}

.sf-lock-ring {
  opacity: 0;
  transform-origin: 0px 0px;
  transition: opacity 160ms ease, transform 220ms ease;
}

.sf-cursor.is-interactive .sf-lock-ring {
  opacity: 0.92;
  animation: sf-lock-spin 3.2s linear infinite;
}

@keyframes sf-lock-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>

<style>
html.has-starfighter-cursor,
html.has-starfighter-cursor body,
html.has-starfighter-cursor a,
html.has-starfighter-cursor button,
html.has-starfighter-cursor [role='button'],
html.has-starfighter-cursor [role='tab'],
html.has-starfighter-cursor [role='menuitem'],
html.has-starfighter-cursor label {
  cursor: none !important;
}
</style>
