import * as THREE from 'three'
import { buildAiCodeWorld } from './buildAiCodeWorld'
import { buildOpticalCore } from './buildOpticalCore'
import { buildQimuStudioWorld } from './buildQimuStudioWorld'
import { createStudioEnvironment } from './studioEnvironment'

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

const PROGRESS_STOPS: ReadonlyArray<[number, number]> = [
  [0.0, 0.0],
  [0.35, 0.25],
  [0.53, 0.5],
  [0.72, 0.75],
  [1.0, 1.0],
]

function mapProgressToKnotT(progress: number): number {
  const p = Math.max(0, Math.min(1, progress))
  for (let i = 0; i < PROGRESS_STOPS.length - 1; i++) {
    const [p0, k0] = PROGRESS_STOPS[i]
    const [p1, k1] = PROGRESS_STOPS[i + 1]
    if (p <= p1) {
      const local = (p - p0) / Math.max(1e-5, p1 - p0)
      return k0 + local * (k1 - k0)
    }
  }
  return 1
}

export function createPortalEngine(options: PortalEngineOptions): PortalEngineController {
  const { canvas } = options
  let currentWidth = Math.max(1, canvas.clientWidth || window.innerWidth)
  let currentHeight = Math.max(1, canvas.clientHeight || window.innerHeight)

  const isMobileViewport = currentWidth < 768
  const highTier = !isMobileViewport

  let currentDpr = Math.min(window.devicePixelRatio || 1, highTier ? 1.25 : 1.0)
  let dprSteppedDown = currentDpr <= 1.0
  let frameCount = 0
  let emaDt = 0.016

  // Single-pass hardware MSAA renderer with stencil disabled for peak fill-rate & 60-120fps headroom
  const renderer = new THREE.WebGLRenderer({
    canvas,
    antialias: true,
    powerPreference: 'high-performance',
    alpha: false,
    stencil: false,
    depth: true,
  })
  renderer.setPixelRatio(currentDpr)
  renderer.setSize(currentWidth, currentHeight, false)
  renderer.toneMapping = THREE.ACESFilmicToneMapping
  renderer.toneMappingExposure = options.initialDark ? 1.08 : 1.0
  renderer.outputColorSpace = THREE.SRGBColorSpace

  const scene = new THREE.Scene()
  let baseFov = currentWidth / currentHeight < 1.0 ? 50 : 44
  const camera = new THREE.PerspectiveCamera(baseFov, currentWidth / currentHeight, 0.1, 260)
  camera.position.set(0, 3.2, 16.5)

  // Macro Cosmic-Cinema Horizon spatial spline
  const cameraPosCurve = new THREE.CatmullRomCurve3(
    [
      new THREE.Vector3(0, 3.2, 16.5), // Act 1: Macro Eclipse & Mirror Ocean Panorama
      new THREE.Vector3(-8.2, 3.1, 4.6), // Act 2: AICode Spatial Smoked-Glass Gallery
      new THREE.Vector3(0, 4.2, 4.2), // Mid Horizon Sweep
      new THREE.Vector3(7.1, 3.25, 7.6), // Act 3: Qimu IMAX, Manju & Editorial Gallery
      new THREE.Vector3(0, 8.8, 25.5), // Act 4: Grand Dual-Horizon Panorama Finale
    ],
    false,
    'catmullrom',
    0.45,
  )

  const cameraLookCurve = new THREE.CatmullRomCurve3(
    [
      new THREE.Vector3(0, 3.8, -8.5), // Act 1 LookAt
      new THREE.Vector3(-14.5, 3.0, -7.8), // Act 2 LookAt
      new THREE.Vector3(2.8, 3.4, -8.5), // Mid Sweep LookAt
      new THREE.Vector3(14.5, 2.95, -7.2), // Act 3 LookAt
      new THREE.Vector3(0, 3.2, -8.5), // Act 4 LookAt
    ],
    false,
    'catmullrom',
    0.45,
  )

  const studioEnv = createStudioEnvironment(scene, renderer, highTier)
  const opticalCore = buildOpticalCore(highTier)
  scene.add(opticalCore.group)

  const aiCodeWorld = buildAiCodeWorld(highTier)
  scene.add(aiCodeWorld.group)

  const qimuWorld = buildQimuStudioWorld(renderer, highTier)
  scene.add(qimuWorld.group)

  let targetProgress = 0
  let smoothedProgress = 0
  let hoverSide: PortalHoverSide = null
  let hoverBias = 0
  let targetThemeT = options.initialDark ? 0 : 1
  let currentThemeT = targetThemeT
  let pointerTargetX = 0
  let pointerTargetY = 0
  let pointerX = 0
  let pointerY = 0
  let reducedMotion = options.reducedMotion
  let disposed = false
  let paused = typeof document !== 'undefined' && document.hidden
  let rafId = 0

  let elapsed = 0
  let lastNow = performance.now()

  const camPosVec = new THREE.Vector3()
  const camLookVec = new THREE.Vector3()

  const onPointerMove = (e: PointerEvent) => {
    if (e.pointerType === 'touch' || reducedMotion) return
    const w = window.innerWidth || 1
    const h = window.innerHeight || 1
    pointerTargetX = (e.clientX / w - 0.5) * 2
    pointerTargetY = -(e.clientY / h - 0.5) * 2
  }

  const onPointerLeave = () => {
    pointerTargetX = 0
    pointerTargetY = 0
  }

  const onVisibilityChange = () => {
    paused = document.hidden
    if (!paused && !disposed) {
      lastNow = performance.now()
      scheduleFrame()
    }
  }

  window.addEventListener('pointermove', onPointerMove, { passive: true })
  window.addEventListener('pointerleave', onPointerLeave, { passive: true })
  document.addEventListener('visibilitychange', onVisibilityChange)

  const renderFrame = (dt: number) => {
    const smoothRate = reducedMotion ? 1 : Math.min(1, dt * 6.5)

    smoothedProgress += (targetProgress - smoothedProgress) * (reducedMotion ? 1 : Math.min(1, dt * 7.5))
    const targetBias = hoverSide === 'aicode' ? -1 : hoverSide === 'qimu' ? 1 : 0
    hoverBias += (targetBias - hoverBias) * smoothRate
    currentThemeT += (targetThemeT - currentThemeT) * smoothRate

    pointerX += (pointerTargetX - pointerX) * (reducedMotion ? 0 : Math.min(1, dt * 5.0))
    pointerY += (pointerTargetY - pointerY) * (reducedMotion ? 0 : Math.min(1, dt * 5.0))

    studioEnv.applyTheme(currentThemeT)
    studioEnv.update?.(elapsed, hoverBias)
    renderer.toneMappingExposure = THREE.MathUtils.lerp(1.08, 1.0, currentThemeT)

    const knotT = mapProgressToKnotT(smoothedProgress)
    cameraPosCurve.getPoint(knotT, camPosVec)
    cameraLookCurve.getPoint(knotT, camLookVec)

    const heroWeight = Math.max(0, 1 - smoothedProgress / 0.22)
    camPosVec.x += hoverBias * 1.45 * heroWeight + pointerX * (reducedMotion ? 0 : 0.42)
    camPosVec.y += pointerY * (reducedMotion ? 0 : 0.22)
    camLookVec.x += hoverBias * 2.4 * heroWeight + pointerX * (reducedMotion ? 0 : 0.28)
    camLookVec.y += pointerY * (reducedMotion ? 0 : 0.14)

    const targetFov = baseFov - Math.abs(hoverBias) * 1.8 * heroWeight
    if (Math.abs(camera.fov - targetFov) > 0.02) {
      camera.fov += (targetFov - camera.fov) * smoothRate
      camera.updateProjectionMatrix()
    }

    camera.position.copy(camPosVec)
    camera.lookAt(camLookVec)

    const aicodeFocus = Math.max(0, 1 - Math.abs(smoothedProgress - 0.35) / 0.25)
    const qimuFocus = Math.max(0, 1 - Math.abs(smoothedProgress - 0.72) / 0.25)
    const isAct2Near = smoothedProgress > 0.12 && smoothedProgress < 0.62
    const isAct3Near = smoothedProgress > 0.45 && smoothedProgress < 0.96

    opticalCore.update(elapsed, dt, hoverBias, currentThemeT)
    aiCodeWorld.update(elapsed, dt, aicodeFocus, currentThemeT, isAct2Near)
    qimuWorld.update(elapsed, dt, pointerX, pointerY, qimuFocus, currentThemeT, isAct3Near)

    renderer.render(scene, camera)
  }

  const loop = (now: number) => {
    rafId = 0
    if (disposed || paused) return
    const rawDt = (now - lastNow) / 1000
    lastNow = now

    // Adaptive DPR governor during warmup frames 15..60
    frameCount++
    if (!dprSteppedDown && frameCount >= 5 && rawDt > 0 && rawDt < 0.25) {
      emaDt = emaDt * 0.85 + rawDt * 0.15
      if (frameCount >= 15 && frameCount <= 60 && emaDt > 0.02 && currentDpr > 1.0) {
        currentDpr = 1.0
        dprSteppedDown = true
        renderer.setPixelRatio(1.0)
        renderer.setSize(currentWidth, currentHeight, false)
      }
    }

    const dt = Math.min(0.05, Math.max(0.001, rawDt))
    if (!reducedMotion) {
      elapsed += dt
    }
    renderFrame(dt)
    if (!reducedMotion) {
      scheduleFrame()
    }
  }

  function scheduleFrame() {
    if (rafId || disposed || paused) return
    rafId = window.requestAnimationFrame(loop)
  }

  renderFrame(0.016)
  if (!reducedMotion) {
    scheduleFrame()
  }

  return {
    setProgress(progress: number) {
      targetProgress = Math.max(0, Math.min(1, progress))
      if (reducedMotion && !disposed) {
        renderFrame(0.016)
      }
    },
    setHoverSide(side: PortalHoverSide) {
      hoverSide = side
      if (reducedMotion && !disposed) {
        renderFrame(0.016)
      }
    },
    setDark(dark: boolean) {
      targetThemeT = dark ? 0 : 1
      if (reducedMotion && !disposed) {
        renderFrame(0.016)
      }
    },
    setReducedMotion(reduced: boolean) {
      reducedMotion = reduced
      if (reduced) {
        if (rafId) {
          window.cancelAnimationFrame(rafId)
          rafId = 0
        }
        renderFrame(0.016)
      } else {
        lastNow = performance.now()
        scheduleFrame()
      }
    },
    resize(nextW: number, nextH: number) {
      currentWidth = Math.max(1, nextW)
      currentHeight = Math.max(1, nextH)
      const aspect = currentWidth / currentHeight
      baseFov = aspect < 1.0 ? 50 : 44
      camera.aspect = aspect
      camera.updateProjectionMatrix()
      if (!dprSteppedDown) {
        currentDpr = Math.min(window.devicePixelRatio || 1, currentWidth < 768 ? 1.0 : 1.25)
        renderer.setPixelRatio(currentDpr)
      }
      renderer.setSize(currentWidth, currentHeight, false)
      if (reducedMotion && !disposed) {
        renderFrame(0.016)
      }
    },
    dispose() {
      if (disposed) return
      disposed = true
      if (rafId) {
        window.cancelAnimationFrame(rafId)
        rafId = 0
      }
      window.removeEventListener('pointermove', onPointerMove)
      window.removeEventListener('pointerleave', onPointerLeave)
      document.removeEventListener('visibilitychange', onVisibilityChange)

      opticalCore.dispose()
      aiCodeWorld.dispose()
      qimuWorld.dispose()
      studioEnv.dispose()
      renderer.dispose()
    },
  }
}
