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

const STAR_COUNT = 960

function createRingShaderMaterial(
  colorA: number,
  colorB: number,
  ticks: number,
  speed: number,
  opacityBase: number,
): THREE.ShaderMaterial {
  return new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
    side: THREE.DoubleSide,
    uniforms: {
      uTime: { value: 0 },
      uThemeT: { value: 0 },
      uColorA: { value: new THREE.Color(colorA) },
      uColorB: { value: new THREE.Color(colorB) },
      uTicks: { value: ticks },
      uSpeed: { value: speed },
      uOpacity: { value: opacityBase },
    },
    vertexShader: /* glsl */ `
      varying vec2 vUv;
      void main() {
        vUv = uv;
        gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTime;
      uniform float uThemeT;
      uniform vec3 uColorA;
      uniform vec3 uColorB;
      uniform float uTicks;
      uniform float uSpeed;
      uniform float uOpacity;
      varying vec2 vUv;

      void main() {
        float angle = vUv.x * 6.2831853;
        float tubeProfile = sin(vUv.y * 3.1415926);
        float dashWave = smoothstep(-0.15, 0.45, sin(angle * 3.0 + uTime * uSpeed));
        float runeTicks = smoothstep(0.2, 0.85, sin(angle * uTicks - uTime * uSpeed * 1.4));
        float pulse = 0.5 + 0.5 * sin(angle * 2.0 - uTime * uSpeed * 1.8);
        float orbHead = pow(max(0.0, cos(angle - uTime * uSpeed * 1.6)), 24.0);

        float alpha = (0.28 + 0.42 * dashWave + 0.30 * runeTicks) * pow(max(0.0, tubeProfile), 0.75);
        alpha = clamp(alpha + orbHead * 0.95, 0.0, 1.0) * uOpacity * (1.0 - uThemeT * 0.92);
        vec3 col = mix(uColorA, uColorB, pulse) + vec3(0.45, 0.85, 1.0) * orbHead * 0.85;
        gl_FragColor = vec4(col, alpha);
      }
    `,
  })
}

function createRibbonMaterial(
  colorA: number,
  colorB: number,
  colorC: number,
  speed: number,
  phase: number,
): THREE.ShaderMaterial {
  return new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
    side: THREE.DoubleSide,
    uniforms: {
      uTime: { value: 0 },
      uThemeT: { value: 0 },
      uColorA: { value: new THREE.Color(colorA) },
      uColorB: { value: new THREE.Color(colorB) },
      uColorC: { value: new THREE.Color(colorC) },
      uSpeed: { value: speed },
      uPhase: { value: phase },
    },
    vertexShader: /* glsl */ `
      uniform float uTime;
      uniform float uSpeed;
      uniform float uPhase;
      varying vec2 vUv;
      void main() {
        vUv = uv;
        vec3 pos = position;
        float t = uTime * uSpeed + uPhase;
        pos.y += sin(pos.x * 0.34 + t) * 0.85 + cos(pos.x * 0.19 - t * 0.7) * 0.45;
        pos.z += cos(pos.x * 0.28 + t * 0.85) * 0.95;
        gl_Position = projectionMatrix * modelViewMatrix * vec4(pos, 1.0);
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTime;
      uniform float uThemeT;
      uniform vec3 uColorA;
      uniform vec3 uColorB;
      uniform vec3 uColorC;
      uniform float uSpeed;
      uniform float uPhase;
      varying vec2 vUv;

      void main() {
        float xFade = smoothstep(0.0, 0.16, vUv.x) * (1.0 - smoothstep(0.84, 1.0, vUv.x));
        float yDist = abs(vUv.y - 0.5) * 2.0;
        float core = exp(-yDist * yDist * 7.5);
        float ribbon = exp(-yDist * yDist * 2.1);

        float flow1 = 0.5 + 0.5 * sin(vUv.x * 16.0 - uTime * (0.9 + uSpeed) + uPhase);
        float flow2 = 0.5 + 0.5 * sin(vUv.x * 9.0 + vUv.y * 5.0 + uTime * 0.65);
        float shimmer = mix(0.55, 1.0, flow1 * 0.65 + flow2 * 0.35);

        vec3 tint = mix(uColorA, uColorB, smoothstep(0.15, 0.85, vUv.x));
        tint = mix(tint, uColorC, flow2 * 0.45);
        float alpha = (ribbon * 0.16 + core * 0.24) * xFade * shimmer * (1.0 - uThemeT * 0.85);
        gl_FragColor = vec4(tint, alpha);
      }
    `,
  })
}

/**
 * Multi-layer 3D WebGL cosmic backdrop engine for the authenticated console:
 * renders a volumetric nebula/aurora sky shader, rotating 3D astrolabe halos,
 * undulating plasma energy ribbons, and a depth-streaming 3D starfield.
 */
export function createAmbientBackdropEngine(options: AmbientBackdropOptions): AmbientBackdropController {
  const { canvas } = options
  let targetDpr = Math.min(typeof window !== 'undefined' ? window.devicePixelRatio || 1 : 1, 1.0)

  const renderer = new THREE.WebGLRenderer({
    canvas,
    antialias: false,
    alpha: false,
    depth: true,
    stencil: false,
    powerPreference: 'low-power',
  })
  renderer.outputColorSpace = THREE.SRGBColorSpace
  renderer.autoClear = false
  renderer.setPixelRatio(targetDpr)

  const initW = Math.max(1, canvas.clientWidth || (typeof window !== 'undefined' ? window.innerWidth : 1440))
  const initH = Math.max(1, canvas.clientHeight || (typeof window !== 'undefined' ? window.innerHeight : 900))
  renderer.setSize(initW, initH, false)

  // Pass 1: Volumetric Sky Dome / Aurora Quad
  const skyScene = new THREE.Scene()
  const skyCamera = new THREE.OrthographicCamera(-1, 1, 1, -1, 0, 1)
  const noiseTex = createSeamlessNoiseTexture()

  let targetThemeT = options.initialDark ? 0 : 1
  let smoothedThemeT = targetThemeT
  let targetMood = THREE.MathUtils.clamp(options.initialMood ?? -0.25, -0.5, 0.5)
  let smoothedMood = targetMood

  let pointerX = 0
  let pointerY = 0
  let smoothedPointerX = 0
  let smoothedPointerY = 0

  const skyGeometry = new THREE.PlaneGeometry(2, 2)
  const skyMaterial = new THREE.ShaderMaterial({
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

      void main() {
        float aspect = max(0.5, uResolution.x / max(1.0, uResolution.y));
        vec2 centered = (vUv - 0.5) * vec2(aspect, 1.0);

        // Cosmic swirl domain warp
        vec2 swirlVec = centered + vec2(0.08, -0.05) - uPointer * 0.04;
        float swirlDist = length(swirlVec);
        float swirlAngle = 0.34 * exp(-swirlDist * 1.25) * sin(uTime * 0.18 + swirlDist * 3.4);
        float cs = cos(swirlAngle);
        float sn = sin(swirlAngle);
        vec2 warpedCentered = vec2(cs * centered.x - sn * centered.y, sn * centered.x + cs * centered.y);

        vec2 baseUv = warpedCentered * 0.58 + 0.5;
        vec2 drift1 = vec2(uTime * 0.018, -uTime * 0.011);
        vec3 n1 = texture2D(uNoiseTex, baseUv * 1.2 + drift1).rgb;
        vec2 warp = (n1.rg - 0.5) * 0.32;
        vec3 n2 = texture2D(uNoiseTex, baseUv * 2.15 + warp + vec2(-uTime * 0.025, uTime * 0.016)).rgb;
        vec3 n3 = texture2D(uNoiseTex, baseUv * 3.4 + (n2.rg - 0.5) * 0.22 + vec2(uTime * 0.014, uTime * 0.021)).rgb;

        float curtainA = smoothstep(0.24, 0.78, n2.r * 0.58 + n3.g * 0.42);
        float curtainB = smoothstep(0.28, 0.82, n1.b * 0.50 + n3.r * 0.50);
        float silk = sin(vUv.x * 16.0 + n2.g * 6.2 + uTime * 0.18) * 0.5 + 0.5;

        float splitMix = clamp(vUv.x * 0.64 + (1.0 - vUv.y) * 0.36 + n1.r * 0.18 - 0.08 + uMood * 0.44, 0.0, 1.0);

        // Deep Obsidian Cosmic Base + Vibrant Multi-Chromatic Nebula
        vec3 darkTop = vec3(0.016, 0.031, 0.090);
        vec3 darkBottom = vec3(0.039, 0.071, 0.188);
        vec3 darkBase = mix(darkBottom, darkTop, smoothstep(0.0, 0.92, vUv.y));

        vec3 royalViolet = vec3(0.427, 0.157, 0.851);
        vec3 sapphire = vec3(0.145, 0.388, 0.922);
        vec3 mysticMagenta = vec3(0.576, 0.200, 0.918);
        vec3 tealAurora = vec3(0.133, 0.949, 0.875);
        vec3 celestialViolet = vec3(0.655, 0.545, 0.980);
        vec3 champagneAurora = vec3(0.984, 0.749, 0.141);

        float nebulaMask = smoothstep(0.22, 0.76, n1.g * 0.62 + n2.b * 0.38);
        vec3 nebulaBlend = mix(royalViolet, sapphire, n2.r);
        nebulaBlend = mix(nebulaBlend, mysticMagenta, n3.b * 0.45);
        vec3 nebulaLayer = nebulaBlend * nebulaMask * 0.58;

        vec3 leftTint = mix(tealAurora, celestialViolet, n2.b * 0.48);
        vec3 rightTint = mix(champagneAurora, celestialViolet, n3.b * 0.55);
        vec3 auroraTint = mix(leftTint, rightTint, splitMix);

        float bandEnvelope = smoothstep(0.02, 0.46, vUv.y) * (1.0 - smoothstep(0.76, 1.06, vUv.y));
        float auroraEnergy = (curtainA * 0.78 + curtainB * 0.55) * (0.52 + 0.48 * bandEnvelope) * (0.64 + 0.36 * silk);

        // Diagonal ethereal light shafts / dimensional rays
        float rayCoord = centered.x * 0.82 + centered.y * 0.56 - uTime * 0.045;
        float shaftA = pow(0.5 + 0.5 * sin(rayCoord * 7.5 + n2.g * 2.6), 4.0);
        float shaftB = pow(0.5 + 0.5 * sin((centered.x * 0.6 - centered.y * 0.75 + uTime * 0.03) * 5.2 + n1.r * 2.1), 5.0);
        vec3 rayGlow = mix(tealAurora, celestialViolet, vUv.x) * (shaftA * 0.14 + shaftB * 0.11) * (0.4 + 0.6 * n2.r);

        // Gentle center readability balance so tables stay sharp without crushing glow
        float radialCenter = length(centered / vec2(aspect * 0.5, 0.5));
        float edgeFactor = smoothstep(0.18, 1.0, radialCenter);
        float centerAtten = mix(0.84, 1.0, edgeFactor);

        // Lower perspective astral horizon grid + luminous sweep
        float horizonMask = 1.0 - smoothstep(0.0, 0.34, vUv.y);
        float perspY = max(0.06, vUv.y + 0.06);
        float gridX = abs(fract((vUv.x - 0.5) * aspect / perspY * 1.15) - 0.5);
        float gridZ = abs(fract(0.45 / perspY - uTime * 0.14) - 0.5);
        float lineX = smoothstep(0.030, 0.0, gridX);
        float lineZ = smoothstep(0.034, 0.0, gridZ);
        float gridPattern = max(lineX, lineZ) * horizonMask;
        float pulsePos = fract(uTime * 0.18);
        float pulseWave = exp(-pow((vUv.y - pulsePos * 0.34) * 15.0, 2.0)) * horizonMask;
        vec3 horizonGlow = mix(tealAurora, champagneAurora, vUv.x) * (gridPattern * 0.14 + gridPattern * pulseWave * 0.28 + pulseWave * 0.06);

        // Interactive soft pointer halo
        vec2 pointerGlowDelta = centered - uPointer * vec2(aspect * 0.36, 0.34);
        float pointerHalo = exp(-dot(pointerGlowDelta, pointerGlowDelta) * 2.0);
        float dither = (n3.r - 0.5) * 0.01;

        vec3 darkCol =
          darkBase +
          (nebulaLayer + auroraTint * auroraEnergy * 0.76 + rayGlow) * centerAtten +
          mix(tealAurora, champagneAurora, splitMix) * pointerHalo * 0.16 +
          horizonGlow +
          vec3(dither);

        // Light Mode: 31-deg Fluted Glass + Marble Swirl + Iridescent Aurora
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
          mix(marble, lightChroma, clamp(auroraEnergy * 0.24 + pointerHalo * 0.14, 0.0, 0.32)) +
          vec3(ridge) +
          vec3(dither * 0.5);

        gl_FragColor = vec4(mix(darkCol, lightCol, uThemeT), 1.0);
      }
    `,
  })
  skyScene.add(new THREE.Mesh(skyGeometry, skyMaterial))

  // Pass 2: Real 3D Cosmic Astrolabe Rings, Flowing Plasma Ribbons & Depth-Flying Starfield
  const spaceScene = new THREE.Scene()
  const spaceCamera = new THREE.PerspectiveCamera(50, initW / initH, 0.1, 100)
  spaceCamera.position.set(0, 0, 8)

  const disposables: Array<{ dispose: () => void }> = [skyGeometry, skyMaterial, noiseTex]
  const themeAwareMaterials: THREE.ShaderMaterial[] = [skyMaterial]

  // A. Primary 3D Astrolabe Cluster (Upper-Right Deep Space)
  const astrolabePrimary = new THREE.Group()
  astrolabePrimary.position.set(3.8, 1.4, -8.5)
  astrolabePrimary.rotation.set(-0.48, -0.35, 0.25)
  spaceScene.add(astrolabePrimary)

  const ringSpecsPrimary = [
    { radius: 3.15, tube: 0.028, colorA: 0x22f2df, colorB: 0xfbbf24, ticks: 36, speed: 0.28, opacity: 0.56 },
    { radius: 2.38, tube: 0.022, colorA: 0xa78bfa, colorB: 0x22f2df, ticks: 24, speed: -0.36, opacity: 0.48 },
    { radius: 1.65, tube: 0.017, colorA: 0xfbbf24, colorB: 0xa78bfa, ticks: 18, speed: 0.44, opacity: 0.44 },
  ]
  const primaryRings = ringSpecsPrimary.map((spec) => {
    const geom = new THREE.TorusGeometry(spec.radius, spec.tube, 12, 112)
    const mat = createRingShaderMaterial(spec.colorA, spec.colorB, spec.ticks, spec.speed, spec.opacity)
    disposables.push(geom, mat)
    themeAwareMaterials.push(mat)
    const mesh = new THREE.Mesh(geom, mat)
    astrolabePrimary.add(mesh)
    return { mesh, speed: spec.speed }
  })

  const sectorDiscGeom = new THREE.RingGeometry(1.18, 2.52, 72)
  const sectorDiscMat = new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
    side: THREE.DoubleSide,
    uniforms: {
      uTime: { value: 0 },
      uThemeT: { value: 0 },
    },
    vertexShader: /* glsl */ `
      varying vec3 vPos;
      void main() {
        vPos = position;
        gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTime;
      uniform float uThemeT;
      varying vec3 vPos;
      void main() {
        float r = length(vPos.xy);
        float angle = atan(vPos.y, vPos.x);
        float rings = exp(-abs(r - 1.85) * 24.0) * 0.45;
        float arc = smoothstep(0.1, 0.85, sin(angle * 4.0 - uTime * 0.35));
        float radialFade = smoothstep(1.18, 1.55, r) * (1.0 - smoothstep(2.15, 2.52, r));
        vec3 col = mix(vec3(0.13, 0.95, 0.87), vec3(0.65, 0.54, 0.98), 0.5 + 0.5 * cos(angle));
        float alpha = (rings + arc * radialFade * 0.14) * (1.0 - uThemeT * 0.92);
        gl_FragColor = vec4(col, alpha);
      }
    `,
  })
  disposables.push(sectorDiscGeom, sectorDiscMat)
  themeAwareMaterials.push(sectorDiscMat)
  astrolabePrimary.add(new THREE.Mesh(sectorDiscGeom, sectorDiscMat))

  // B. Secondary Counter-Rotating Astrolabe (Lower-Left Deep Space)
  const astrolabeSecondary = new THREE.Group()
  astrolabeSecondary.position.set(-4.6, -2.1, -10.5)
  astrolabeSecondary.rotation.set(0.55, 0.42, -0.2)
  spaceScene.add(astrolabeSecondary)

  const ringSpecsSecondary = [
    { radius: 2.42, tube: 0.022, colorA: 0x22f2df, colorB: 0xa78bfa, ticks: 28, speed: -0.32, opacity: 0.46 },
    { radius: 1.66, tube: 0.016, colorA: 0xfbbf24, colorB: 0x22f2df, ticks: 16, speed: 0.42, opacity: 0.40 },
  ]
  const secondaryRings = ringSpecsSecondary.map((spec) => {
    const geom = new THREE.TorusGeometry(spec.radius, spec.tube, 10, 96)
    const mat = createRingShaderMaterial(spec.colorA, spec.colorB, spec.ticks, spec.speed, spec.opacity)
    disposables.push(geom, mat)
    themeAwareMaterials.push(mat)
    const mesh = new THREE.Mesh(geom, mat)
    astrolabeSecondary.add(mesh)
    return { mesh, speed: spec.speed }
  })

  // C. Two Translucent 3D Flowing Energy Ribbons
  const ribbonGeom1 = new THREE.PlaneGeometry(26, 3.8, 72, 12)
  const ribbonMat1 = createRibbonMaterial(0x22f2df, 0xa78bfa, 0xfbbf24, 0.55, 0.0)
  const ribbonMesh1 = new THREE.Mesh(ribbonGeom1, ribbonMat1)
  ribbonMesh1.position.set(0.4, 0.8, -9.8)
  ribbonMesh1.rotation.set(-0.28, 0.08, -0.22)
  spaceScene.add(ribbonMesh1)
  disposables.push(ribbonGeom1, ribbonMat1)
  themeAwareMaterials.push(ribbonMat1)

  const ribbonGeom2 = new THREE.PlaneGeometry(24, 3.2, 64, 10)
  const ribbonMat2 = createRibbonMaterial(0xa78bfa, 0xfbbf24, 0x22f2df, 0.42, 2.4)
  const ribbonMesh2 = new THREE.Mesh(ribbonGeom2, ribbonMat2)
  ribbonMesh2.position.set(-0.8, -1.5, -12.6)
  ribbonMesh2.rotation.set(0.24, -0.1, 0.18)
  spaceScene.add(ribbonMesh2)
  disposables.push(ribbonGeom2, ribbonMat2)
  themeAwareMaterials.push(ribbonMat2)

  // D. GPU-Animated 3D Depth-Flying Starfield
  const starPositions = new Float32Array(STAR_COUNT * 3)
  const starColors = new Float32Array(STAR_COUNT * 3)
  const starSeeds = new Float32Array(STAR_COUNT * 3)

  const palette = [
    new THREE.Color(0xf8fafc), // ice white
    new THREE.Color(0x38e8d5), // cyan
    new THREE.Color(0xc4b5fd), // lavender
    new THREE.Color(0xfcd34d), // champagne gold
  ]

  for (let i = 0; i < STAR_COUNT; i++) {
    const i3 = i * 3
    starPositions[i3] = (Math.random() - 0.5) * 32
    starPositions[i3 + 1] = (Math.random() - 0.5) * 20
    starPositions[i3 + 2] = -26 + Math.random() * 24

    const tint = palette[i % 10 === 0 ? 3 : i % 5 === 0 ? 1 : i % 4 === 0 ? 2 : 0]!
    starColors[i3] = tint.r
    starColors[i3 + 1] = tint.g
    starColors[i3 + 2] = tint.b

    starSeeds[i3] = Math.random() // phase / speed variation
    starSeeds[i3 + 1] = 0.7 + Math.random() * 1.4 // base size scale
    starSeeds[i3 + 2] = Math.random() > 0.88 ? 1.0 : 0.0 // ~12% hero 4-point flare stars
  }

  const starGeometry = new THREE.BufferGeometry()
  starGeometry.setAttribute('position', new THREE.BufferAttribute(starPositions, 3))
  starGeometry.setAttribute('aColor', new THREE.BufferAttribute(starColors, 3))
  starGeometry.setAttribute('aSeed', new THREE.BufferAttribute(starSeeds, 3))

  const starMaterial = new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
    uniforms: {
      uTime: { value: 0 },
      uThemeT: { value: 0 },
      uPixelRatio: { value: targetDpr },
    },
    vertexShader: /* glsl */ `
      attribute vec3 aColor;
      attribute vec3 aSeed;
      uniform float uTime;
      uniform float uPixelRatio;
      varying vec3 vColor;
      varying float vAlpha;
      varying float vHero;

      void main() {
        vec3 pos = position;
        float speed = 0.55 + aSeed.x * 1.25;
        pos.z = mod(pos.z + uTime * speed + 26.0, 24.0) - 26.0;

        vec4 mvPosition = modelViewMatrix * vec4(pos, 1.0);
        gl_Position = projectionMatrix * mvPosition;

        float nearFade = smoothstep(-1.5, -5.0, pos.z);
        float farFade = smoothstep(-26.0, -21.0, pos.z);
        float twinkle = 0.62 + 0.38 * sin(uTime * (1.4 + aSeed.x * 2.6) + aSeed.x * 6.2831);

        vColor = aColor;
        vHero = aSeed.z;
        vAlpha = nearFade * farFade * twinkle;

        float heroBoost = mix(1.0, 2.2, vHero);
        gl_PointSize = clamp((24.0 * aSeed.y * heroBoost * uPixelRatio) / max(1.0, -mvPosition.z), 1.4, 24.0);
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uThemeT;
      varying vec3 vColor;
      varying float vAlpha;
      varying float vHero;

      void main() {
        vec2 uv = gl_PointCoord - 0.5;
        float dist = length(uv);
        if (dist > 0.5) discard;

        float softCore = exp(-dist * dist * 22.0);
        float innerPin = exp(-dist * dist * 68.0);
        float spikeX = exp(-abs(uv.y) * 34.0) * exp(-abs(uv.x) * 6.5);
        float spikeY = exp(-abs(uv.x) * 34.0) * exp(-abs(uv.y) * 6.5);
        float spikes = (spikeX + spikeY) * vHero * 0.72;

        float intensity = clamp(softCore * 0.65 + innerPin * 0.85 + spikes, 0.0, 1.0);
        float alpha = intensity * vAlpha * (1.0 - uThemeT * 0.92);
        vec3 col = mix(vColor, vec3(1.0), innerPin * 0.55);
        gl_FragColor = vec4(col, alpha);
      }
    `,
  })
  disposables.push(starGeometry, starMaterial)
  themeAwareMaterials.push(starMaterial)
  spaceScene.add(new THREE.Points(starGeometry, starMaterial))

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
        starMaterial.uniforms.uPixelRatio!.value = targetDpr
        lastDprAdjustAt = now
      }
    }

    elapsed += dt
    const k = Math.min(1, dt * 4.5)
    smoothedThemeT = THREE.MathUtils.lerp(smoothedThemeT, targetThemeT, k)
    smoothedMood = THREE.MathUtils.lerp(smoothedMood, targetMood, Math.min(1, dt * 2.5))
    smoothedPointerX = THREE.MathUtils.lerp(smoothedPointerX, pointerX, k)
    smoothedPointerY = THREE.MathUtils.lerp(smoothedPointerY, pointerY, k)

    skyMaterial.uniforms.uMood!.value = smoothedMood
    skyMaterial.uniforms.uPointer!.value.set(smoothedPointerX, smoothedPointerY)

    for (const mat of themeAwareMaterials) {
      mat.uniforms.uTime!.value = elapsed
      mat.uniforms.uThemeT!.value = smoothedThemeT
    }

    // Subtle 3D orbital counter-rotation + perspective parallax
    astrolabePrimary.rotation.z = 0.25 + Math.sin(elapsed * 0.14) * 0.08
    astrolabePrimary.rotation.y = -0.35 + smoothedPointerX * 0.10
    astrolabePrimary.rotation.x = -0.48 - smoothedPointerY * 0.08
    for (const item of primaryRings) {
      item.mesh.rotation.z = elapsed * item.speed * 0.22
    }

    astrolabeSecondary.rotation.z = -0.20 + Math.cos(elapsed * 0.12) * 0.07
    astrolabeSecondary.rotation.y = 0.42 + smoothedPointerX * 0.08
    for (const item of secondaryRings) {
      item.mesh.rotation.z = elapsed * item.speed * 0.25
    }

    spaceCamera.position.x = smoothedPointerX * 0.55
    spaceCamera.position.y = smoothedPointerY * 0.35
    spaceCamera.lookAt(0, 0, -9)

    renderer.clear()
    renderer.render(skyScene, skyCamera)
    renderer.clearDepth()
    renderer.render(spaceScene, spaceCamera)
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
      skyMaterial.uniforms.uResolution!.value.set(w, h)
      spaceCamera.aspect = w / h
      spaceCamera.updateProjectionMatrix()
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
      for (const item of disposables) {
        item.dispose()
      }
      renderer.dispose()
    },
  }
}
