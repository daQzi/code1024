import * as THREE from 'three'
import { createSeamlessNoiseTexture } from '@/components/public/three/proceduralTextures'

export interface AmbientBackdropOptions {
  canvas: HTMLCanvasElement
  initialDark: boolean
  initialMood?: number
}

export interface AmbientBackdropController {
  setDark: (dark: boolean) => void
  setMood: (mood: number) => void
  resize: (width: number, height: number) => void
  dispose: () => void
}

/**
 * Lightweight 1-draw-call fullscreen WebGL cosmic aurora & fluted glass shader
 * tailored for the authenticated workspace behind Liquid Glass surfaces.
 */
export function createAmbientBackdropEngine(options: AmbientBackdropOptions): AmbientBackdropController {
  const { canvas } = options
  let targetDpr = Math.min(typeof window !== 'undefined' ? window.devicePixelRatio || 1 : 1, 1.0)

  const renderer = new THREE.WebGLRenderer({
    canvas,
    antialias: false,
    alpha: false,
    depth: false,
    stencil: false,
    powerPreference: 'low-power',
  })
  renderer.outputColorSpace = THREE.SRGBColorSpace
  renderer.setPixelRatio(targetDpr)

  const initW = Math.max(1, canvas.clientWidth || (typeof window !== 'undefined' ? window.innerWidth : 1440))
  const initH = Math.max(1, canvas.clientHeight || (typeof window !== 'undefined' ? window.innerHeight : 900))
  renderer.setSize(initW, initH, false)

  const scene = new THREE.Scene()
  const camera = new THREE.OrthographicCamera(-1, 1, 1, -1, 0, 1)
  const noiseTex = createSeamlessNoiseTexture()

  let targetThemeT = options.initialDark ? 0 : 1
  let smoothedThemeT = targetThemeT
  let targetMood = THREE.MathUtils.clamp(options.initialMood ?? -0.25, -0.5, 0.5)
  let smoothedMood = targetMood

  let pointerX = 0
  let pointerY = 0
  let smoothedPointerX = 0
  let smoothedPointerY = 0

  const geometry = new THREE.PlaneGeometry(2, 2)
  const material = new THREE.ShaderMaterial({
    depthTest: false,
    depthWrite: false,
    uniforms: {
      uTime: { value: 0 },
      uThemeT: { value: smoothedThemeT },
      uMood: { value: smoothedMood },
      uResolution: { value: new THREE.Vector2(initW, initH) },
      uPointer: { value: new THREE.Vector2(0, 0) },
      uNoiseTex: { value: noiseTex },
    },
    vertexShader: /* glsl */ `
      varying vec2 vUv;
      void main() {
        vUv = uv;
        gl_Position = vec4(position.xy, 0.0, 1.0);
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTime;
      uniform float uThemeT;
      uniform float uMood;
      uniform vec2 uResolution;
      uniform vec2 uPointer;
      uniform sampler2D uNoiseTex;
      varying vec2 vUv;

      float starLayer(vec2 uv, float scale, float threshold, float time) {
        vec2 grid = uv * scale;
        vec2 id = floor(grid);
        vec2 gv = fract(grid) - 0.5;
        float h = fract(sin(dot(id, vec2(127.1, 311.7))) * 43758.5453);
        if (h < threshold) return 0.0;
        vec2 offset = vec2(fract(h * 17.3), fract(h * 39.7)) - 0.5;
        float dist = length(gv - offset * 0.65);
        float twinkle = 0.58 + 0.42 * sin(time * (1.2 + fract(h * 7.1) * 2.1) + h * 6.2831);
        float core = smoothstep(0.11, 0.0, dist);
        return core * twinkle * ((h - threshold) / (1.0 - threshold));
      }

      void main() {
        float aspect = max(0.5, uResolution.x / max(1.0, uResolution.y));
        vec2 centered = (vUv - 0.5) * vec2(aspect, 1.0);

        // Domain-warped seamless aurora ribbons
        vec2 drift1 = vec2(uTime * 0.015, -uTime * 0.009);
        vec3 n1 = texture2D(uNoiseTex, vUv * 1.15 + drift1).rgb;
        vec2 warp = (n1.rg - 0.5) * 0.24;
        vec3 n2 = texture2D(uNoiseTex, vUv * 2.15 + warp + vec2(-uTime * 0.02, uTime * 0.012)).rgb;
        vec3 n3 = texture2D(uNoiseTex, vUv * 3.4 + (n2.rg - 0.5) * 0.16 + vec2(uTime * 0.011, uTime * 0.017)).rgb;

        float curtainA = smoothstep(0.30, 0.80, n2.r * 0.62 + n3.g * 0.38);
        float curtainB = smoothstep(0.36, 0.84, n1.b * 0.54 + n3.r * 0.46);
        float silk = sin(vUv.x * 16.0 + n2.g * 5.2 + uTime * 0.12) * 0.5 + 0.5;

        float splitMix = clamp(vUv.x * 0.72 + (1.0 - vUv.y) * 0.28 + n1.r * 0.14 - 0.06 + uMood * 0.45, 0.0, 1.0);

        // Dark Obsidian Workspace Aurora
        vec3 darkTop = vec3(0.012, 0.020, 0.044);
        vec3 darkBottom = vec3(0.022, 0.040, 0.086);
        vec3 darkBase = mix(darkBottom, darkTop, smoothstep(0.0, 0.95, vUv.y));

        vec3 tealAurora = vec3(0.08, 0.78, 0.72);
        vec3 indigoAurora = vec3(0.20, 0.38, 0.82);
        vec3 champagneAurora = vec3(0.92, 0.68, 0.32);
        vec3 amberSilk = vec3(0.82, 0.52, 0.25);

        vec3 leftTint = mix(tealAurora, indigoAurora, n2.b * 0.62);
        vec3 rightTint = mix(champagneAurora, amberSilk, n3.b * 0.58);
        vec3 auroraTint = mix(leftTint, rightTint, splitMix);

        float bandEnvelope = smoothstep(0.05, 0.48, vUv.y) * (1.0 - smoothstep(0.68, 1.04, vUv.y));
        float auroraEnergy = (curtainA * 0.68 + curtainB * 0.44) * (0.45 + 0.55 * bandEnvelope) * (0.64 + 0.36 * silk);

        vec2 pointerGlowDelta = centered - uPointer * vec2(aspect * 0.35, 0.32);
        float pointerHalo = exp(-dot(pointerGlowDelta, pointerGlowDelta) * 2.4);

        // Subtle micro-stars tuned low so console charts/tables stay crystal clear
        float stars =
          starLayer(centered + vec2(uTime * 0.002, 0.0), 14.0, 0.945, uTime) * 0.26 +
          starLayer(centered - vec2(0.0, uTime * 0.0015), 24.0, 0.962, uTime + 2.1) * 0.20;

        float dither = (n3.r - 0.5) * 0.01;
        vec3 darkCol =
          darkBase +
          auroraTint * auroraEnergy * 0.54 +
          mix(vec3(0.12, 0.72, 0.72), vec3(0.90, 0.66, 0.32), splitMix) * pointerHalo * 0.12 +
          vec3(stars) +
          vec3(dither);

        // Light Mode: 31-deg Fluted Glass + Marble Swirl + Soft Pointer Tint
        float fluteAngle = 0.541;
        float ca = cos(fluteAngle);
        float sa = sin(fluteAngle);
        vec2 rotP = vec2(ca * centered.x - sa * centered.y, sa * centered.x + ca * centered.y);
        float fluteWave = sin(rotP.x * 22.0 * 3.14159);
        vec2 fluteOffset = vec2(ca, sa) * fluteWave * 0.022;

        vec2 lightUvR = vUv * 1.2 + fluteOffset * 1.1 + vec2(uTime * 0.01, -uTime * 0.008);
        vec2 lightUvB = vUv * 1.2 + fluteOffset * 0.9 + vec2(uTime * 0.01, -uTime * 0.008);
        float mR = texture2D(uNoiseTex, lightUvR).r;
        float mG = texture2D(uNoiseTex, (lightUvR + lightUvB) * 0.5).g;
        float mB = texture2D(uNoiseTex, lightUvB).b;
        vec3 marble = mix(vec3(0.84, 0.89, 0.96), vec3(0.95, 0.97, 0.995), vec3(mR, mG, mB));
        vec3 lightChroma = mix(vec3(0.32, 0.82, 0.78), vec3(0.62, 0.74, 0.96), splitMix);
        float ridge = pow(max(0.0, fluteWave), 4.0) * 0.045;
        vec3 lightCol = mix(marble, lightChroma, clamp(auroraEnergy * 0.22 + pointerHalo * 0.14, 0.0, 0.30)) + vec3(ridge) + vec3(dither * 0.5);

        gl_FragColor = vec4(mix(darkCol, lightCol, uThemeT), 1.0);
      }
    `,
  })

  const quad = new THREE.Mesh(geometry, material)
  scene.add(quad)

  let rafId: number | null = null
  let disposed = false
  let elapsed = 0
  let lastTimestamp = typeof performance !== 'undefined' ? performance.now() : 0
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

    // Keep background strictly lightweight on lower-end GPUs
    if (rawDt < 0.15) {
      emaDt = emaDt * 0.92 + rawDt * 0.08
      if (emaDt > 0.024 && targetDpr > 0.75 && now - lastDprAdjustAt > 2000) {
        targetDpr = 0.75
        renderer.setPixelRatio(targetDpr)
        lastDprAdjustAt = now
      }
    }

    elapsed += dt
    const k = Math.min(1, dt * 4.5)
    smoothedThemeT = THREE.MathUtils.lerp(smoothedThemeT, targetThemeT, k)
    smoothedMood = THREE.MathUtils.lerp(smoothedMood, targetMood, Math.min(1, dt * 2.5))
    smoothedPointerX = THREE.MathUtils.lerp(smoothedPointerX, pointerX, k)
    smoothedPointerY = THREE.MathUtils.lerp(smoothedPointerY, pointerY, k)

    material.uniforms.uTime.value = elapsed
    material.uniforms.uThemeT.value = smoothedThemeT
    material.uniforms.uMood.value = smoothedMood
    material.uniforms.uPointer.value.set(smoothedPointerX, smoothedPointerY)

    renderer.render(scene, camera)
  }

  rafId = window.requestAnimationFrame(renderFrame)

  return {
    setDark(dark: boolean) {
      targetThemeT = dark ? 0 : 1
    },
    setMood(mood: number) {
      targetMood = THREE.MathUtils.clamp(mood, -0.5, 0.5)
    },
    resize(width: number, height: number) {
      if (disposed) return
      const w = Math.max(1, width)
      const h = Math.max(1, height)
      material.uniforms.uResolution.value.set(w, h)
      renderer.setSize(w, h, false)
    },
    dispose() {
      if (disposed) return
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
      geometry.dispose()
      material.dispose()
      noiseTex.dispose()
      renderer.dispose()
    },
  }
}
