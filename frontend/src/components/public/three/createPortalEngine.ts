import * as THREE from 'three'
import { buildAiCodeWorld } from './buildAiCodeWorld'
import { buildOpticalCore } from './buildOpticalCore'
import { buildQimuStudioWorld } from './buildQimuStudioWorld'
import { createSoftGlowTexture } from './proceduralTextures'
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
  const width = Math.max(1, canvas.clientWidth || window.innerWidth)
  const height = Math.max(1, canvas.clientHeight || window.innerHeight)

  const isMobileViewport = width < 768
  const highTier = !isMobileViewport

  // Single-pass hardware MSAA renderer for crisp reflections & locked 60 FPS
  const renderer = new THREE.WebGLRenderer({
    canvas,
    antialias: true,
    powerPreference: 'high-performance',
    alpha: false,
  })
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, highTier ? 1.5 : 1.25))
  renderer.setSize(width, height, false)
  renderer.toneMapping = THREE.ACESFilmicToneMapping
  renderer.toneMappingExposure = options.initialDark ? 1.12 : 1.02
  renderer.outputColorSpace = THREE.SRGBColorSpace

  const scene = new THREE.Scene()
  const camera = new THREE.PerspectiveCamera(40, width / height, 0.1, 140)
  camera.position.set(0, 2.55, 13.6)

  // Monumental 4-act spatial cinematography spline
  const cameraPosCurve = new THREE.CatmullRomCurve3(
    [
      new THREE.Vector3(0, 2.55, 13.6), // Act 1: Mechanical Aperture Hall
      new THREE.Vector3(-9.2, 2.75, 2.8), // Act 2: AICode Compute Vault
      new THREE.Vector3(0, 3.6, 3.2), // Mid Hall Architectural Sweep
      new THREE.Vector3(9.2, 2.7, 3.6), // Act 3: Qimu Cinema & Art Studio
      new THREE.Vector3(0, 7.2, 21.8), // Act 4: Grand Dual-Wing Panorama
    ],
    false,
    'catmullrom',
    0.45,
  )

  const cameraLookCurve = new THREE.CatmullRomCurve3(
    [
      new THREE.Vector3(0, 3.25, -3.5), // Act 1 LookAt
      new THREE.Vector3(-14.5, 2.65, -6.0), // Act 2 LookAt
      new THREE.Vector3(3.5, 2.8, -5.0), // Transition LookAt
      new THREE.Vector3(14.5, 2.5, -5.8), // Act 3 LookAt
      new THREE.Vector3(0, 2.9, -5.5), // Act 4 LookAt
    ],
    false,
    'catmullrom',
    0.45,
  )

  // Architectural concrete hall, studio lighting dome, basalt floor & bounce pools
  const studioEnv = createStudioEnvironment(scene, renderer, highTier)

  // Hero 3D worlds
  const opticalCore = buildOpticalCore(highTier)
  scene.add(opticalCore.group)

  const aiCodeWorld = buildAiCodeWorld(highTier)
  scene.add(aiCodeWorld.group)

  const qimuWorld = buildQimuStudioWorld(renderer, highTier)
  scene.add(qimuWorld.group)

  // Restrained architectural hall atmosphere motes (240 desktop / 100 mobile)
  const dustCount = highTier ? 240 : 100
  const dustPositions = new Float32Array(dustCount * 3)
  const dustColors = new Float32Array(dustCount * 3)
  const tealCol = new THREE.Color(0x5eead4)
  const amberCol = new THREE.Color(0xfbbf24)
  const silverCol = new THREE.Color(0xcbd5e1)
  const tmpCol = new THREE.Color()

  for (let i = 0; i < dustCount; i++) {
    const rx = ((Math.sin(i * 78.233) * 43758.5453) % 1 + 1) % 1
    const ry = ((Math.sin(i * 19.137) * 28411.172) % 1 + 1) % 1
    const rz = ((Math.sin(i * 45.913) * 19283.391) % 1 + 1) % 1
    const x = (rx - 0.5) * 44
    const y = 0.4 + ry * 9.6
    const z = -26 + rz * 38
    dustPositions[i * 3] = x
    dustPositions[i * 3 + 1] = y
    dustPositions[i * 3 + 2] = z

    if (x < -5) tmpCol.copy(tealCol)
    else if (x > 5) tmpCol.copy(amberCol)
    else tmpCol.copy(silverCol)
    dustColors[i * 3] = tmpCol.r
    dustColors[i * 3 + 1] = tmpCol.g
    dustColors[i * 3 + 2] = tmpCol.b
  }

  const dustGeo = new THREE.BufferGeometry()
  dustGeo.setAttribute('position', new THREE.BufferAttribute(dustPositions, 3))
  dustGeo.setAttribute('color', new THREE.BufferAttribute(dustColors, 3))
  const dustTex = createSoftGlowTexture()
  const dustMat = new THREE.PointsMaterial({
    map: dustTex,
    size: 0.16,
    vertexColors: true,
    transparent: true,
    opacity: 0.38,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  })
  const dustPoints = new THREE.Points(dustGeo, dustMat)
  scene.add(dustPoints)

  // Mutable reactive engine state
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
    renderer.toneMappingExposure = THREE.MathUtils.lerp(1.12, 1.02, currentThemeT)
    dustMat.opacity = THREE.MathUtils.lerp(0.38, 0.14, currentThemeT)

    const knotT = mapProgressToKnotT(smoothedProgress)
    cameraPosCurve.getPoint(knotT, camPosVec)
    cameraLookCurve.getPoint(knotT, camLookVec)

    const heroWeight = Math.max(0, 1 - smoothedProgress / 0.22)
    camPosVec.x += hoverBias * 1.45 * heroWeight + pointerX * (reducedMotion ? 0 : 0.45)
    camPosVec.y += pointerY * (reducedMotion ? 0 : 0.24)
    camLookVec.x += hoverBias * 2.4 * heroWeight + pointerX * (reducedMotion ? 0 : 0.3)
    camLookVec.y += pointerY * (reducedMotion ? 0 : 0.16)

    const targetFov = 40 - Math.abs(hoverBias) * 2.0 * heroWeight
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

    dustPoints.rotation.y = elapsed * 0.01

    // Single-pass hardware-antialiased render
    renderer.render(scene, camera)
  }

  const loop = (now: number) => {
    rafId = 0
    if (disposed || paused) return
    const rawDt = (now - lastNow) / 1000
    lastNow = now
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
      const w = Math.max(1, nextW)
      const h = Math.max(1, nextH)
      camera.aspect = w / h
      camera.updateProjectionMatrix()
      renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, w < 768 ? 1.25 : 1.5))
      renderer.setSize(w, h, false)
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
      dustGeo.dispose()
      dustMat.dispose()
      dustTex.dispose()
      renderer.dispose()
    },
  }
}
