import * as THREE from 'three'
import { createStudioEnvironment } from './studioEnvironment'
import { buildOpticalCore } from './buildOpticalCore'
import { buildAiCodeWorld } from './buildAiCodeWorld'
import { buildQimuStudioWorld } from './buildQimuStudioWorld'

export type PortalHoverSide = 'aicode' | 'qimu' | null

export interface PortalEngineOptions {
  canvas: HTMLCanvasElement
  initialDark: boolean
  reducedMotion: boolean
}

export interface PortalEngineController {
  setProgress: (progress: number) => void
  setHoverSide: (side: PortalHoverSide) => void
  setDark: (dark: boolean) => void
  setReducedMotion: (reduced: boolean) => void
  resize: (width: number, height: number) => void
  dispose: () => void
}

// Scroll progress milestones aligned to ReferenceLanding 4-Act stops
const PROGRESS_STOPS: ReadonlyArray<readonly [number, number]> = [
  [0, 0],
  [0.35, 0.25],
  [0.53, 0.5],
  [0.72, 0.75],
  [1.0, 1.0],
]

function mapProgressToCurveT(progress: number): number {
  const p = THREE.MathUtils.clamp(progress, 0, 1)
  for (let i = 1; i < PROGRESS_STOPS.length; i++) {
    const [p0, t0] = PROGRESS_STOPS[i - 1]
    const [p1, t1] = PROGRESS_STOPS[i]
    if (p <= p1) {
      const span = Math.max(0.0001, p1 - p0)
      return THREE.MathUtils.lerp(t0, t1, (p - p0) / span)
    }
  }
  return 1
}

export function createPortalEngine(options: PortalEngineOptions): PortalEngineController {
  const { canvas } = options

  const isMobileViewport = typeof window !== 'undefined' ? window.innerWidth < 768 : false
  const cores = typeof navigator !== 'undefined' ? navigator.hardwareConcurrency || 4 : 4
  const highTier = !isMobileViewport && cores >= 4

  const maxDprCap = isMobileViewport ? 1.0 : 1.25
  let targetDpr = Math.min(typeof window !== 'undefined' ? window.devicePixelRatio || 1 : 1, maxDprCap)

  const renderer = new THREE.WebGLRenderer({
    canvas,
    antialias: true,
    alpha: false,
    depth: true,
    stencil: false,
    powerPreference: 'high-performance',
  })
  renderer.outputColorSpace = THREE.SRGBColorSpace
  renderer.toneMapping = THREE.ACESFilmicToneMapping
  renderer.toneMappingExposure = 1.04
  renderer.setPixelRatio(targetDpr)

  const initW = Math.max(1, canvas.clientWidth || (typeof window !== 'undefined' ? window.innerWidth : 1440))
  const initH = Math.max(1, canvas.clientHeight || (typeof window !== 'undefined' ? window.innerHeight : 900))
  renderer.setSize(initW, initH, false)

  const scene = new THREE.Scene()
  const aspect0 = initW / initH
  const camera = new THREE.PerspectiveCamera(aspect0 < 1 ? 52 : 46, aspect0, 0.25, 280)

  // Symmetric Act 1..Act 4 camera & target spline aligned to x = +-11.2 worlds
  const camPosCurve = new THREE.CatmullRomCurve3(
    [
      new THREE.Vector3(0, 3.3, 14.8), // Act 1: Panoramic split portal
      new THREE.Vector3(-5.2, 3.2, 4.6), // Act 2: AICode world closeup (world at -11.2, 0, -7.2)
      new THREE.Vector3(0, 4.0, 3.8), // Mid transition past central optical core
      new THREE.Vector3(4.9, 3.25, 6.2), // Act 3: Qimu studio closeup (world at +11.2, 0, -7.2)
      new THREE.Vector3(0, 7.8, 22.8), // Act 4: Elevated grand panoramic overview
    ],
    false,
    'centripetal',
  )

  const camLookCurve = new THREE.CatmullRomCurve3(
    [
      new THREE.Vector3(0, 3.8, -7.8),
      new THREE.Vector3(-11.2, 3.1, -6.5),
      new THREE.Vector3(2.4, 3.5, -7.6),
      new THREE.Vector3(11.2, 3.05, -6.4),
      new THREE.Vector3(0, 3.5, -7.4),
    ],
    false,
    'centripetal',
  )

  const env = createStudioEnvironment(scene, renderer, highTier)
  const opticalCore = buildOpticalCore(highTier)
  const aiCodeWorld = buildAiCodeWorld(highTier)
  const qimuWorld = buildQimuStudioWorld(renderer, highTier)

  scene.add(opticalCore.group, aiCodeWorld.group, qimuWorld.group)

  let targetProgress = 0
  let smoothedProgress = 0
  let targetThemeT = options.initialDark ? 0 : 1
  let smoothedThemeT = targetThemeT
  let hoverSide: PortalHoverSide = null
  let smoothedHoverBias = 0
  let reducedMotion = options.reducedMotion

  let pointerX = 0
  let pointerY = 0
  let smoothedPointerX = 0
  let smoothedPointerY = 0

  env.applyTheme(smoothedThemeT)

  const basePos = new THREE.Vector3()
  const baseLook = new THREE.Vector3()
  const finalPos = new THREE.Vector3()
  const finalLook = new THREE.Vector3()

  let rafId: number | null = null
  let disposed = false
  let lastTimestamp = typeof performance !== 'undefined' ? performance.now() : 0
  let elapsed = 0
  let emaDt = 1 / 60
  let lastDprAdjustAt = 0

  const handlePointerMove = (e: PointerEvent) => {
    const w = typeof window !== 'undefined' ? window.innerWidth || 1440 : 1440
    const h = typeof window !== 'undefined' ? window.innerHeight || 900 : 900
    pointerX = THREE.MathUtils.clamp((e.clientX / w) * 2 - 1, -1, 1)
    pointerY = THREE.MathUtils.clamp(-((e.clientY / h) * 2 - 1), -1, 1)
  }

  const handleVisibilityChange = () => {
    if (typeof document !== 'undefined' && !document.hidden) {
      lastTimestamp = performance.now()
    }
  }

  if (typeof window !== 'undefined') {
    window.addEventListener('pointermove', handlePointerMove, { passive: true })
  }
  if (typeof document !== 'undefined') {
    document.addEventListener('visibilitychange', handleVisibilityChange)
  }

  const renderFrame = (now: number) => {
    if (disposed) return
    rafId = window.requestAnimationFrame(renderFrame)

    if (typeof document !== 'undefined' && document.hidden) {
      lastTimestamp = now
      return
    }

    const rawDt = Math.max(0.001, (now - lastTimestamp) * 0.001)
    lastTimestamp = now
    const dt = Math.min(rawDt, 0.05)

    // Adaptive resolution governor: keep locked 60-120 FPS without frame-churn
    if (rawDt < 0.15) {
      emaDt = emaDt * 0.92 + rawDt * 0.08
      if (emaDt > 0.02 && targetDpr > 0.85 && now - lastDprAdjustAt > 2000) {
        targetDpr = Math.max(0.85, Number((targetDpr - 0.15).toFixed(2)))
        renderer.setPixelRatio(targetDpr)
        lastDprAdjustAt = now
      }
    }

    if (!reducedMotion) {
      elapsed += dt
    }

    const lerpRate = reducedMotion ? 1 : Math.min(1, dt * 6.5)
    smoothedProgress = THREE.MathUtils.lerp(smoothedProgress, targetProgress, lerpRate)
    smoothedThemeT = THREE.MathUtils.lerp(smoothedThemeT, targetThemeT, Math.min(1, dt * 5.2))

    const targetHoverBias = hoverSide === 'aicode' ? -1 : hoverSide === 'qimu' ? 1 : 0
    smoothedHoverBias = THREE.MathUtils.lerp(smoothedHoverBias, targetHoverBias, Math.min(1, dt * 4.8))

    smoothedPointerX = THREE.MathUtils.lerp(smoothedPointerX, pointerX, Math.min(1, dt * 5.5))
    smoothedPointerY = THREE.MathUtils.lerp(smoothedPointerY, pointerY, Math.min(1, dt * 5.5))

    const curveT = mapProgressToCurveT(smoothedProgress)
    camPosCurve.getPoint(curveT, basePos)
    camLookCurve.getPoint(curveT, baseLook)

    // Hover focus pull & gentle spatial parallax
    const parallaxScale = reducedMotion ? 0 : 1
    const idleX = Math.sin(elapsed * 0.42) * 0.14 * parallaxScale
    const idleY = Math.cos(elapsed * 0.56) * 0.08 * parallaxScale

    finalPos.copy(basePos)
    finalPos.x += smoothedHoverBias * 1.45 + smoothedPointerX * 0.48 * parallaxScale + idleX
    finalPos.y += smoothedPointerY * 0.24 * parallaxScale + idleY
    finalPos.z -= Math.abs(smoothedHoverBias) * 1.15

    finalLook.copy(baseLook)
    finalLook.x += smoothedHoverBias * 2.2 + smoothedPointerX * 0.35 * parallaxScale
    finalLook.y += smoothedPointerY * 0.18 * parallaxScale

    camera.position.copy(finalPos)
    camera.lookAt(finalLook)

    // Keep AI/Qimu focus weights normalized for subtle stage reactivity
    const aiFocusWeight = THREE.MathUtils.clamp(
      (smoothedHoverBias < 0 ? -smoothedHoverBias * 0.65 : 0) +
        (1.0 - Math.min(1, Math.abs(smoothedProgress - 0.35) / 0.28)),
      0,
      1,
    )
    const qimuFocusWeight = THREE.MathUtils.clamp(
      (smoothedHoverBias > 0 ? smoothedHoverBias * 0.65 : 0) +
        (1.0 - Math.min(1, Math.abs(smoothedProgress - 0.72) / 0.28)),
      0,
      1,
    )

    // Gate off-act 6Hz texture canvas updates when camera zooms exclusively into the other side
    const aiActive = smoothedProgress < 0.68 || smoothedProgress > 0.88
    const qimuActive = smoothedProgress > 0.4 || smoothedProgress < 0.18

    env.applyTheme(smoothedThemeT)
    env.update?.(elapsed, smoothedHoverBias)
    opticalCore.update(elapsed, dt, smoothedHoverBias, smoothedThemeT)
    aiCodeWorld.update(elapsed, dt, aiFocusWeight, smoothedThemeT, aiActive)
    qimuWorld.update(elapsed, dt, smoothedPointerX, smoothedPointerY, qimuFocusWeight, smoothedThemeT, qimuActive)

    renderer.toneMappingExposure = THREE.MathUtils.lerp(1.06, 1.0, smoothedThemeT)
    renderer.render(scene, camera)
  }

  rafId = window.requestAnimationFrame(renderFrame)

  return {
    setProgress(progress: number) {
      targetProgress = THREE.MathUtils.clamp(progress, 0, 1)
    },
    setHoverSide(side: PortalHoverSide) {
      hoverSide = side
    },
    setDark(dark: boolean) {
      targetThemeT = dark ? 0 : 1
    },
    setReducedMotion(value: boolean) {
      reducedMotion = value
    },
    resize(width: number, height: number) {
      const safeW = Math.max(1, width)
      const safeH = Math.max(1, height)
      const aspect = safeW / safeH
      camera.aspect = aspect
      camera.fov = aspect < 1 ? 52 : 46
      camera.updateProjectionMatrix()
      renderer.setSize(safeW, safeH, false)
    },
    dispose() {
      disposed = true
      if (rafId !== null) {
        window.cancelAnimationFrame(rafId)
        rafId = null
      }
      if (typeof window !== 'undefined') {
        window.removeEventListener('pointermove', handlePointerMove)
      }
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', handleVisibilityChange)
      }
      opticalCore.dispose()
      aiCodeWorld.dispose()
      qimuWorld.dispose()
      env.dispose()
      renderer.dispose()
    },
  }
}
