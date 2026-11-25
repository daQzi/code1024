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
 * Single-draw-call fullscreen WebGL cosmic nebula, orbital astrolabe rings,
 * perspective astral grid pulse & 31-deg fluted light shader for the console shell.
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
        float twinkle = 0.55 + 0.45 * sin(time * (1.1 + fract(h * 7.1) * 2.2) + h * 6.2831);
        float core = smoothstep(0.11, 0.0, dist);
        return core * twinkle * ((h - threshold) / (1.0 - threshold));
      }

      float orbitalRingSet(vec2 coord, vec2 center, float radius, float time, float dir, out float nodeGlow) {
        vec2 d = coord - center;
        float r = length(d);
        float theta = atan(d.y, d.x);

        float outerRing = exp(-abs(r - radius) * 190.0) * 0.55;
        float runeBand = exp(-abs(r - (radius - 0.048)) * 155.0);
        float ticks = smoothstep(0.12, 0.72, sin(theta * 24.0 + time * 0.25 * dir));
        float dashedArc = smoothstep(-0.1, 0.35, sin(theta * 3.0 - time * 0.18 * dir));
        float innerRing = exp(-abs(r - (radius * 0.64)) * 210.0) * 0.36;

        // Two glowing celestial node beads orbiting along outer/mid tracks
        vec2 bead1Pos = center + vec2(cos(time * 0.34 * dir), sin(time * 0.34 * dir)) * radius;
        vec2 bead2Pos = center + vec2(cos(-time * 0.24 * dir + 2.1), sin(-time * 0.24 * dir + 2.1)) * (radius * 0.64);
        nodeGlow =
          exp(-length(coord - bead1Pos) * 58.0) * 0.9 +
          exp(-length(coord - bead2Pos) * 70.0) * 0.65;

        return outerRing + runeBand * ticks * dashedArc * 0.65 + innerRing * (0.45 + 0.55 * dashedArc);
      }

      void main() {
        float aspect = max(0.5, uResolution.x / max(1.0, uResolution.y));
        vec2 aspectCoord = vUv * vec2(aspect, 1.0);
        vec2 centered = (vUv - 0.5) * vec2(aspect, 1.0);

        // Soft swirl warp around dual cosmic centers
        vec2 swirlVec = centered + vec2(0.12, -0.06);
        float swirlDist = length(swirlVec);
        float swirlAngle = 0.28 * exp(-swirlDist * 1.45) * sin(uTime * 0.16 + swirlDist * 3.2);
        float cs = cos(swirlAngle);
        float sn = sin(swirlAngle);
        vec2 warpedCentered = vec2(cs * centered.x - sn * centered.y, sn * centered.x + cs * centered.y);

        vec2 baseUv = warpedCentered * 0.62 + 0.5;
        vec2 drift1 = vec2(uTime * 0.016, -uTime * 0.010);
        vec3 n1 = texture2D(uNoiseTex, baseUv * 1.25 + drift1).rgb;
        vec2 warp = (n1.rg - 0.5) * 0.28;
        vec3 n2 = texture2D(uNoiseTex, baseUv * 2.25 + warp + vec2(-uTime * 0.022, uTime * 0.014)).rgb;
        vec3 n3 = texture2D(uNoiseTex, baseUv * 3.6 + (n2.rg - 0.5) * 0.18 + vec2(uTime * 0.012, uTime * 0.019)).rgb;

        float curtainA = smoothstep(0.28, 0.80, n2.r * 0.60 + n3.g * 0.40);
        float curtainB = smoothstep(0.34, 0.84, n1.b * 0.52 + n3.r * 0.48);
        float silk = sin(vUv.x * 18.0 + n2.g * 5.6 + uTime * 0.14) * 0.5 + 0.5;

        float splitMix = clamp(vUv.x * 0.68 + (1.0 - vUv.y) * 0.32 + n1.r * 0.15 - 0.06 + uMood * 0.42, 0.0, 1.0);

        // 2A Palette: Deep Obsidian + Indigo-Violet Nebula + AICode Teal + Champagne Gold
        vec3 darkTop = vec3(0.010, 0.016, 0.038);
        vec3 darkBottom = vec3(0.018, 0.030, 0.072);
        vec3 darkBase = mix(darkBottom, darkTop, smoothstep(0.0, 0.95, vUv.y));

        vec3 indigoViolet = vec3(0.24, 0.16, 0.56);
        vec3 cosmicBlue = vec3(0.16, 0.34, 0.78);
        vec3 tealAurora = vec3(0.10, 0.86, 0.78);
        vec3 champagneAurora = vec3(0.94, 0.71, 0.34);

        float nebulaMask = smoothstep(0.26, 0.78, n1.g * 0.65 + n2.b * 0.35);
        vec3 nebulaLayer = mix(indigoViolet, cosmicBlue, n2.r) * nebulaMask * 0.42;

        vec3 leftTint = mix(tealAurora, cosmicBlue, n2.b * 0.52);
        vec3 rightTint = mix(champagneAurora, indigoViolet, n3.b * 0.42);
        vec3 auroraTint = mix(leftTint, rightTint, splitMix);

        float bandEnvelope = smoothstep(0.04, 0.48, vUv.y) * (1.0 - smoothstep(0.72, 1.04, vUv.y));
        float auroraEnergy = (curtainA * 0.72 + curtainB * 0.48) * (0.48 + 0.52 * bandEnvelope) * (0.62 + 0.38 * silk);

        // Soft peripheral readability vignette so center tables/charts remain sharp
        float radialCenter = length(centered / vec2(aspect * 0.5, 0.5));
        float edgeFactor = smoothstep(0.22, 1.05, radialCenter);
        float centerAtten = mix(0.68, 1.0, edgeFactor);

        // Rotating peripheral Celestial Orbital Rings (upper-right + lower-left with pointer parallax)
        vec2 pParallax = uPointer * 0.025;
        float nodeGlow1 = 0.0;
        float ring1 = orbitalRingSet(aspectCoord, vec2(aspect * 0.84, 0.78) - pParallax, 0.36, uTime, 1.0, nodeGlow1);
        float nodeGlow2 = 0.0;
        float ring2 = orbitalRingSet(aspectCoord, vec2(aspect * 0.14, 0.18) + pParallax * 0.8, 0.29, uTime * 0.85, -1.0, nodeGlow2);

        vec3 ringCol1 = mix(champagneAurora, tealAurora, 0.35) * (ring1 * 0.30 + nodeGlow1 * 0.48);
        vec3 ringCol2 = mix(tealAurora, indigoViolet, 0.25) * (ring2 * 0.28 + nodeGlow2 * 0.44);

        // Lower Horizon Perspective Astral Grid + 6s periodic energy wave
        float horizonMask = 1.0 - smoothstep(0.0, 0.35, vUv.y);
        float perspY = max(0.06, vUv.y + 0.06);
        float gridX = abs(fract((vUv.x - 0.5) * aspect / perspY * 1.1) - 0.5);
        float gridZ = abs(fract(0.45 / perspY - uTime * 0.12) - 0.5);
        float lineX = smoothstep(0.032, 0.0, gridX);
        float lineZ = smoothstep(0.036, 0.0, gridZ);
        float gridPattern = max(lineX, lineZ) * horizonMask;
        float pulsePos = fract(uTime * 0.16);
        float pulseWave = exp(-pow((vUv.y - pulsePos * 0.36) * 16.0, 2.0)) * horizonMask;
        vec3 horizonGlow = mix(tealAurora, champagneAurora, vUv.x) * (gridPattern * 0.10 + gridPattern * pulseWave * 0.22 + pulseWave * 0.04);

        // Multi-depth starfield + pointer glow
        vec2 pointerGlowDelta = centered - uPointer * vec2(aspect * 0.35, 0.32);
        float pointerHalo = exp(-dot(pointerGlowDelta, pointerGlowDelta) * 2.3);
        float stars =
          starLayer(centered + vec2(uTime * 0.0022, 0.0), 14.0, 0.940, uTime) * 0.32 +
          starLayer(centered - vec2(0.0, uTime * 0.0016), 25.0, 0.958, uTime + 2.1) * 0.24;

        float dither = (n3.r - 0.5) * 0.01;
        vec3 darkCol =
          darkBase +
          (nebulaLayer + auroraTint * auroraEnergy * 0.56) * centerAtten +
          mix(tealAurora, champagneAurora, splitMix) * pointerHalo * 0.13 +
          ringCol1 +
          ringCol2 +
          horizonGlow +
          vec3(stars) +
          vec3(dither);

        // Light Mode: 31-deg Fluted Glass + Marble Swirl + faint astrolabe watermarks
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
        vec3 lightCol =
          mix(marble, lightChroma, clamp(auroraEnergy * 0.22 + pointerHalo * 0.14, 0.0, 0.30)) +
          vec3(ridge) +
          vec3(0.18, 0.58, 0.58) * (ring1 + ring2) * 0.08 +
          vec3(dither * 0.5);

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
