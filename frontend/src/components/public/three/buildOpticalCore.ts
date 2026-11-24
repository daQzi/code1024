import * as THREE from 'three'
import { createSoftGlowTexture } from './proceduralTextures'

export interface OpticalCoreHandle {
  group: THREE.Group
  update: (
    elapsed: number,
    dt: number,
    hoverBias: number,
    themeT: number,
    pointerX?: number,
    pointerY?: number,
    pointerSpeed?: number,
  ) => void
  dispose: () => void
}

interface OrbitNodeSeed {
  ringIndex: number
  baseAngle: number
  speed: number
  scale: number
  radialOffset: number
}

export function buildOpticalCore(highTier: boolean): OpticalCoreHandle {
  const group = new THREE.Group()
  const coreCenter = new THREE.Vector3(0, 4.35, -8.0)

  const geometries: THREE.BufferGeometry[] = []
  const materials: THREE.Material[] = []
  const textures: THREE.Texture[] = []

  const softGlowTex = createSoftGlowTexture()
  textures.push(softGlowTex)

  const corePivot = new THREE.Group()
  corePivot.position.copy(coreCenter)
  group.add(corePivot)

  // 1. Central Jewel-like Obsidian Event Horizon Pupil + Fresnel Photon Shell
  const pupilGeo = new THREE.SphereGeometry(1.58, 48, 32)
  geometries.push(pupilGeo)
  const pupilMat = new THREE.MeshStandardMaterial({
    color: 0x060b18,
    metalness: 0.9,
    roughness: 0.12,
    envMapIntensity: 1.95,
  })
  materials.push(pupilMat)
  const pupilMesh = new THREE.Mesh(pupilGeo, pupilMat)
  corePivot.add(pupilMesh)

  const coronaGeo = new THREE.SphereGeometry(1.86, 48, 32)
  geometries.push(coronaGeo)
  const coronaMat = new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    side: THREE.FrontSide,
    blending: THREE.AdditiveBlending,
    uniforms: {
      uTime: { value: 0 },
      uHoverBias: { value: 0 },
      uThemeT: { value: 0 },
    },
    vertexShader: /* glsl */ `
      varying vec3 vNormalW;
      varying vec3 vViewDir;
      varying vec3 vLocalPos;
      void main() {
        vLocalPos = position;
        vec4 wp = modelMatrix * vec4(position, 1.0);
        vNormalW = normalize(mat3(modelMatrix) * normal);
        vViewDir = normalize(cameraPosition - wp.xyz);
        gl_Position = projectionMatrix * viewMatrix * wp;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTime;
      uniform float uHoverBias;
      uniform float uThemeT;
      varying vec3 vNormalW;
      varying vec3 vViewDir;
      varying vec3 vLocalPos;

      void main() {
        float ndotv = max(dot(normalize(vNormalW), normalize(vViewDir)), 0.0);
        float rim = pow(1.0 - ndotv, 3.2);
        float swirl = sin(vLocalPos.x * 3.4 - uTime * 2.1 + sin(vLocalPos.y * 2.6 + uTime * 1.3)) * 0.5 + 0.5;
        float sideMix = clamp((vLocalPos.x * 0.35 + 0.5) + uHoverBias * 0.25, 0.0, 1.0);
        vec3 tealRim = vec3(0.18, 0.88, 0.84);
        vec3 champagneRim = vec3(0.96, 0.76, 0.42);
        vec3 rimCol = mix(tealRim, champagneRim, sideMix);
        float alpha = rim * (0.58 + 0.35 * swirl) * mix(1.0, 0.72, uThemeT);
        gl_FragColor = vec4(rimCol * 1.05, alpha);
      }
    `,
  })
  materials.push(coronaMat)
  const coronaMesh = new THREE.Mesh(coronaGeo, coronaMat)
  corePivot.add(coronaMesh)

  // 2. Silky Silver-Teal & Warm Champagne 12-Shaft Volumetric God-Ray Vortex
  const godRayGeo = new THREE.PlaneGeometry(26, 26, 1, 1)
  geometries.push(godRayGeo)
  const godRayMat = new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    fog: false,
    blending: THREE.AdditiveBlending,
    uniforms: {
      uTime: { value: 0 },
      uHoverBias: { value: 0 },
      uThemeT: { value: 0 },
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
      uniform float uHoverBias;
      uniform float uThemeT;
      varying vec2 vUv;

      void main() {
        vec2 p = (vUv - 0.5) * 2.0; // -1..1
        float r = length(p);
        float angle = atan(p.y, p.x);

        // Preserve dark crystal pupil interior (r < 0.135 stays unlit)
        float eclipseMask = smoothstep(0.115, 0.175, r);
        float edgeFade = smoothstep(1.0, 0.24, r);

        // 12 primary god-ray shafts + 7 secondary counter-rotating shafts
        float rayA = pow(max(0.0, cos(angle * 12.0 + uTime * 0.26 + sin(r * 4.5 - uTime * 0.4) * 0.35)), 4.0);
        float rayB = pow(max(0.0, cos(angle * 7.0 - uTime * 0.19 + 1.2)), 3.0);
        float ringGlow = exp(-abs(r - 0.165) * 24.0) * 0.78;
        float haloGlow = exp(-max(r - 0.14, 0.0) * 4.2) * 0.48;

        float sideSplit = smoothstep(-0.65, 0.65, p.x + uHoverBias * 0.35);
        vec3 tealCol = vec3(0.16, 0.84, 0.80);
        vec3 platinumCol = vec3(0.84, 0.89, 0.96);
        vec3 champagneCol = vec3(0.95, 0.76, 0.42);

        vec3 leftCol = mix(tealCol, platinumCol, rayB * 0.52);
        vec3 rightCol = mix(champagneCol, platinumCol, rayB * 0.48);
        vec3 rayCol = mix(leftCol, rightCol, sideSplit);

        float intensity = (haloGlow + ringGlow + (rayA * 0.52 + rayB * 0.28) * smoothstep(0.95, 0.15, r)) * eclipseMask * edgeFade;
        float themeGain = mix(0.68, 0.28, uThemeT);
        gl_FragColor = vec4(rayCol * intensity * themeGain, clamp(intensity * themeGain, 0.0, 0.92));
      }
    `,
  })
  materials.push(godRayMat)
  const godRayMesh = new THREE.Mesh(godRayGeo, godRayMat)
  godRayMesh.position.z = -0.18
  corePivot.add(godRayMesh)

  // Secondary luminous sprite glow behind pupil for rich softness
  const haloSpriteMat = new THREE.SpriteMaterial({
    map: softGlowTex,
    color: 0x38e8d5,
    transparent: true,
    opacity: 0.32,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
  })
  materials.push(haloSpriteMat)
  const haloSprite = new THREE.Sprite(haloSpriteMat)
  haloSprite.scale.set(16.5, 16.5, 1)
  haloSprite.position.z = -0.35
  corePivot.add(haloSprite)

  // 3. Five Tilted Luxury Metal Gyroscope Rings (platinum, titanium, teal, champagne, pearl)
  const ringConfigs = [
    { radius: 3.4, tube: 0.038, color: 0xe2e8f0, emissive: 0x334155, tilt: [0.42, 0.18, 0.12], speed: [0.22, 0.34, 0.08] },
    { radius: 4.3, tube: 0.032, color: 0xf0c27b, emissive: 0x92611a, tilt: [-0.36, 0.44, -0.22], speed: [-0.16, 0.28, 0.14] },
    { radius: 5.4, tube: 0.028, color: 0x94a3b8, emissive: 0x1e293b, tilt: [0.78, -0.25, 0.35], speed: [0.12, -0.22, 0.18] },
    { radius: 6.7, tube: 0.024, color: 0x38e8d5, emissive: 0x0f766e, tilt: [-0.68, -0.42, -0.18], speed: [-0.09, 0.18, -0.15] },
    { radius: 8.2, tube: 0.022, color: 0xcbd5e1, emissive: 0x475569, tilt: [0.22, 0.72, -0.48], speed: [0.06, -0.14, 0.11] },
  ] as const

  const ringMeshes: THREE.Mesh[] = []
  const ringMats: THREE.MeshStandardMaterial[] = []

  ringConfigs.forEach((cfg) => {
    const rGeo = new THREE.TorusGeometry(cfg.radius, cfg.tube, 12, highTier ? 128 : 80)
    geometries.push(rGeo)
    const rMat = new THREE.MeshStandardMaterial({
      color: cfg.color,
      emissive: cfg.emissive,
      emissiveIntensity: 0.58,
      metalness: 0.9,
      roughness: 0.16,
      transparent: true,
      opacity: 0.9,
    })
    materials.push(rMat)
    ringMats.push(rMat)
    const mesh = new THREE.Mesh(rGeo, rMat)
    mesh.rotation.set(cfg.tilt[0], cfg.tilt[1], cfg.tilt[2])
    corePivot.add(mesh)
    ringMeshes.push(mesh)
  })

  // 4. 48 Orbiting Crystalline Nodes (InstancedMesh attached across all 5 gyroscope rings)
  const nodeCount = 48
  const nodeGeo = new THREE.OctahedronGeometry(0.14, 0)
  geometries.push(nodeGeo)
  const nodeMat = new THREE.MeshStandardMaterial({
    color: 0xffffff,
    emissive: 0x38e8d5,
    emissiveIntensity: 0.82,
    metalness: 0.88,
    roughness: 0.14,
  })
  materials.push(nodeMat)
  const nodeInstanced = new THREE.InstancedMesh(nodeGeo, nodeMat, nodeCount)
  nodeInstanced.instanceMatrix.setUsage(THREE.DynamicDrawUsage)
  corePivot.add(nodeInstanced)

  const nodeSeeds: OrbitNodeSeed[] = []
  const tmpColor = new THREE.Color()
  for (let i = 0; i < nodeCount; i++) {
    const ringIndex = i % ringConfigs.length
    const isWarm = ringIndex === 1 || ringIndex === 4
    tmpColor.setHex(isWarm ? (i % 2 === 0 ? 0xf0c27b : 0xf8fafc) : i % 2 === 0 ? 0x38e8d5 : 0xe2e8f0)
    nodeInstanced.setColorAt(i, tmpColor)
    nodeSeeds.push({
      ringIndex,
      baseAngle: (i / nodeCount) * Math.PI * 2 * 2.3,
      speed: (ringIndex % 2 === 0 ? 1 : -1) * (0.28 + (i % 5) * 0.08),
      scale: 0.72 + ((i * 7) % 10) * 0.08,
      radialOffset: ((i % 3) - 1) * 0.12,
    })
  }
  if (nodeInstanced.instanceColor) nodeInstanced.instanceColor.needsUpdate = true

  // 5. 6 Full-Viewport 3D Flowing Energy-Silk Plasma Ribbons (Corner-to-Corner spanning x in [-26, 26], z in [-14, 9])
  interface RibbonSpec {
    points: THREE.Vector3[]
    radius: number
    speed: number
    phase: number
    waveAmp: number
    colLeft: THREE.Color
    colRight: THREE.Color
  }

  const ribbonSpecs: RibbonSpec[] = [
    // Upper-left high sweep down into Eclipse crown and soaring top-right
    {
      points: [
        new THREE.Vector3(-26, 12.6, -4.5),
        new THREE.Vector3(-14, 9.2, -9.5),
        new THREE.Vector3(0, 6.2, -8.2),
        new THREE.Vector3(14, 9.6, -9.8),
        new THREE.Vector3(26, 13.0, -4.0),
      ],
      radius: 0.095,
      speed: 0.34,
      phase: 0.0,
      waveAmp: 0.42,
      colLeft: new THREE.Color(0x38e8d5),
      colRight: new THREE.Color(0xf0c27b),
    },
    // Mid-left foreground sweep curving gracefully past lower-center toward mid-right
    {
      points: [
        new THREE.Vector3(-25, 2.2, 6.8),
        new THREE.Vector3(-12, 1.4, -1.2),
        new THREE.Vector3(0, 2.3, -6.8),
        new THREE.Vector3(12, 1.5, -1.0),
        new THREE.Vector3(25, 2.5, 6.5),
      ],
      radius: 0.085,
      speed: 0.42,
      phase: 1.4,
      waveAmp: 0.36,
      colLeft: new THREE.Color(0x94a3b8),
      colRight: new THREE.Color(0xf0c27b),
    },
    // High diagonal top-left foreground -> Eclipse heart -> deep far-right sky
    {
      points: [
        new THREE.Vector3(-24, 11.4, 3.2),
        new THREE.Vector3(-11, 6.8, -4.8),
        new THREE.Vector3(0, 4.4, -8.1),
        new THREE.Vector3(11.5, 5.4, -11.2),
        new THREE.Vector3(26, 9.2, -13.8),
      ],
      radius: 0.08,
      speed: 0.38,
      phase: 2.7,
      waveAmp: 0.48,
      colLeft: new THREE.Color(0xe2e8f0),
      colRight: new THREE.Color(0xf0c27b),
    },
    // Counter-diagonal deep left sky -> Eclipse heart -> right foreground
    {
      points: [
        new THREE.Vector3(-26, 9.4, -13.5),
        new THREE.Vector3(-11.5, 5.4, -10.8),
        new THREE.Vector3(0, 4.2, -7.8),
        new THREE.Vector3(11.5, 6.9, -4.2),
        new THREE.Vector3(24, 11.8, 3.4),
      ],
      radius: 0.08,
      speed: 0.36,
      phase: 3.9,
      waveAmp: 0.48,
      colLeft: new THREE.Color(0x38e8d5),
      colRight: new THREE.Color(0xe2e8f0),
    },
    // Lower sweeping galactic bridge linking AICode & Qimu worlds cleanly in front
    {
      points: [
        new THREE.Vector3(-24, 4.8, 8.2),
        new THREE.Vector3(-13, 3.8, 1.5),
        new THREE.Vector3(0, 5.2, -5.8),
        new THREE.Vector3(13, 3.9, 1.6),
        new THREE.Vector3(24, 5.0, 8.4),
      ],
      radius: 0.072,
      speed: 0.46,
      phase: 5.1,
      waveAmp: 0.34,
      colLeft: new THREE.Color(0x38e8d5),
      colRight: new THREE.Color(0xf0c27b),
    },
    // Crown cosmic halo arc hovering in upper zenith across entire viewport width
    {
      points: [
        new THREE.Vector3(-25, 13.4, -8.5),
        new THREE.Vector3(-12, 11.2, -12.8),
        new THREE.Vector3(0, 9.8, -10.2),
        new THREE.Vector3(12, 11.2, -12.8),
        new THREE.Vector3(25, 13.4, -8.5),
      ],
      radius: 0.074,
      speed: 0.29,
      phase: 0.85,
      waveAmp: 0.52,
      colLeft: new THREE.Color(0xcbd5e1),
      colRight: new THREE.Color(0xf0c27b),
    },
  ]

  const ribbonMats: THREE.ShaderMaterial[] = []
  ribbonSpecs.forEach((spec) => {
    const curve = new THREE.CatmullRomCurve3(spec.points)
    const rGeo = new THREE.TubeGeometry(curve, highTier ? 128 : 80, spec.radius, 6, false)
    geometries.push(rGeo)

    const rMat = new THREE.ShaderMaterial({
      transparent: true,
      depthWrite: false,
      fog: false,
      blending: THREE.AdditiveBlending,
      uniforms: {
        uTime: { value: 0 },
        uSpeed: { value: spec.speed },
        uPhase: { value: spec.phase },
        uWaveAmp: { value: spec.waveAmp },
        uThemeT: { value: 0 },
        uHoverBias: { value: 0 },
        uColLeft: { value: spec.colLeft },
        uColRight: { value: spec.colRight },
      },
      vertexShader: /* glsl */ `
        uniform float uTime;
        uniform float uPhase;
        uniform float uWaveAmp;
        varying vec2 vUv;
        varying float vWorldX;

        void main() {
          vUv = uv;
          vec3 pos = position;
          float s = uv.x * 6.28318 * 2.0 + uTime * 0.9 + uPhase;
          pos.y += sin(s) * uWaveAmp;
          pos.z += cos(s * 0.75 + 0.9) * (uWaveAmp * 0.75);
          vec4 wp = modelMatrix * vec4(pos, 1.0);
          vWorldX = wp.x;
          gl_Position = projectionMatrix * viewMatrix * wp;
        }
      `,
      fragmentShader: /* glsl */ `
        uniform float uTime;
        uniform float uSpeed;
        uniform float uPhase;
        uniform float uThemeT;
        uniform float uHoverBias;
        uniform vec3 uColLeft;
        uniform vec3 uColRight;
        varying vec2 vUv;
        varying float vWorldX;

        void main() {
          float endFade = smoothstep(0.0, 0.08, vUv.x) * smoothstep(1.0, 0.92, vUv.x);
          float pulseA = pow(max(0.0, sin(vUv.x * 16.0 - uTime * (uSpeed * 10.0) + uPhase)), 5.0);
          float pulseB = pow(max(0.0, sin(vUv.x * 9.0 + uTime * (uSpeed * 7.0) + uPhase * 1.7)), 4.0);

          float sideMix = smoothstep(-14.0, 14.0, vWorldX + uHoverBias * 8.0);
          vec3 baseCol = mix(uColLeft, uColRight, sideMix);
          vec3 hotWhite = vec3(0.96, 0.98, 1.0);
          vec3 finalCol = mix(baseCol, hotWhite, clamp(pulseA * 0.65 + pulseB * 0.35, 0.0, 0.85));

          float silkAlpha = (0.18 + pulseA * 0.56 + pulseB * 0.28) * endFade;
          float themeScale = mix(0.84, 0.28, uThemeT);
          gl_FragColor = vec4(finalCol * silkAlpha * themeScale, clamp(silkAlpha * themeScale, 0.0, 1.0));
        }
      `,
    })
    materials.push(rMat)
    ribbonMats.push(rMat)
    const mesh = new THREE.Mesh(rGeo, rMat)
    group.add(mesh)
  })

  // 6. Full-Viewport 5,000 / 2,000 GPU-Advected Star-River & Foreground Bokeh Particles
  const particleCount = highTier ? 5000 : 2000
  const pPositions = new Float32Array(particleCount * 3)
  const pParams = new Float32Array(particleCount * 4) // size, speed, phase, colorMix

  for (let i = 0; i < particleCount; i++) {
    const h1 = Math.abs(Math.sin(i * 12.9898 + 1.2) * 43758.5453) % 1
    const h2 = Math.abs(Math.sin(i * 78.233 + 3.7) * 43758.5453) % 1
    const h3 = Math.abs(Math.sin(i * 45.164 + 7.1) * 43758.5453) % 1

    const isStreamParticle = i < particleCount * 0.45
    if (isStreamParticle) {
      // Spiral river linking left world (-14), center eclipse (0), and right world (+14)
      const streamT = (h1 - 0.5) * 2.0 // -1..1
      const arm = i % 2 === 0 ? 1 : -1
      const spreadR = 0.6 + h2 * 4.2
      const angle = streamT * 4.2 + arm * Math.PI
      pPositions[i * 3] = streamT * 24.0 + Math.cos(angle) * spreadR * 0.45
      pPositions[i * 3 + 1] = 3.8 + Math.sin(angle * 0.8) * spreadR * 0.65 + (h3 - 0.5) * 3.8
      pPositions[i * 3 + 2] = -7.8 + Math.sin(streamT * 2.4) * 5.2 + (h2 - 0.5) * 8.5
    } else {
      // Full-viewport wide cosmic bokeh field (foreground -> deep background)
      pPositions[i * 3] = (h1 - 0.5) * 56.0 // -28..28
      pPositions[i * 3 + 1] = 0.25 + Math.pow(h2, 1.15) * 15.5 // 0.25..15.75
      pPositions[i * 3 + 2] = -36.0 + h3 * 47.0 // -36..+11
    }

    pParams[i * 4] = 2.2 + ((i * 19) % 13) * 0.38 // 2.2..6.76 px base
    pParams[i * 4 + 1] = 0.25 + ((i * 11) % 7) * 0.11
    pParams[i * 4 + 2] = (i * 0.73) % (Math.PI * 2)
    pParams[i * 4 + 3] = pPositions[i * 3] >= 0 ? 0.65 + (i % 4) * 0.1 : (i % 4) * 0.1
  }

  const particleGeo = new THREE.BufferGeometry()
  particleGeo.setAttribute('position', new THREE.BufferAttribute(pPositions, 3))
  particleGeo.setAttribute('aParam', new THREE.BufferAttribute(pParams, 4))
  geometries.push(particleGeo)

  const particleMat = new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    fog: false,
    blending: THREE.AdditiveBlending,
    uniforms: {
      uTime: { value: 0 },
      uThemeT: { value: 0 },
      uHoverBias: { value: 0 },
      uPointerPos: { value: new THREE.Vector3(0, 4.35, -1.5) },
      uPointerSpeed: { value: 0 },
      uPixelRatio: { value: Math.min(window.devicePixelRatio || 1, 1.25) },
    },
    vertexShader: /* glsl */ `
      attribute vec4 aParam;
      uniform float uTime;
      uniform vec3 uPointerPos;
      uniform float uPointerSpeed;
      uniform float uPixelRatio;
      varying float vTint;
      varying float vAlpha;
      varying float vWake;

      void main() {
        vec3 pos = position;
        float t = uTime * aParam.y + aParam.z;
        pos.x += sin(t * 0.75 + pos.z * 0.08) * 0.55;
        pos.y += cos(t * 0.92 + pos.x * 0.09) * 0.42;
        pos.z += sin(t * 0.61) * 0.45;

        // AXON-inspired cursor fluid wake & radial particle dispersion on GPU
        vec2 dPtr = pos.xy - uPointerPos.xy;
        float dist2 = dot(dPtr, dPtr) + 0.35;
        float influence = exp(-dist2 * 0.045) * (0.85 + clamp(uPointerSpeed, 0.0, 2.5) * 2.2);
        vec2 dir2 = normalize(dPtr + vec2(0.0001));
        vec2 swirl = vec2(-dir2.y, dir2.x);
        pos.xy += (dir2 * 1.45 + swirl * 1.25) * influence;
        pos.z += sin(dist2 * 0.5 - uTime * 2.2) * 0.75 * influence;

        vec4 mvPosition = modelViewMatrix * vec4(pos, 1.0);
        float dist = max(3.0, -mvPosition.z);
        vWake = clamp(influence * 0.65, 0.0, 1.0);
        gl_PointSize = clamp(aParam.x * (1.0 + vWake * 0.35) * uPixelRatio * (20.0 / dist), 1.8, 12.5);
        vTint = aParam.w;
        vAlpha = smoothstep(52.0, 12.0, dist) * (0.58 + 0.42 * sin(t * 1.4)) * (1.0 + vWake * 0.45);
        gl_Position = projectionMatrix * mvPosition;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uThemeT;
      uniform float uHoverBias;
      varying float vTint;
      varying float vAlpha;
      varying float vWake;

      void main() {
        vec2 c = gl_PointCoord - vec2(0.5);
        float r2 = dot(c, c);
        if (r2 > 0.25) discard;

        float bokeh = smoothstep(0.25, 0.02, r2);
        vec3 tealDot = vec3(0.22, 0.90, 0.84);
        vec3 champagneDot = vec3(0.96, 0.78, 0.46);
        vec3 pearlDot = vec3(0.88, 0.92, 0.98);
        vec3 baseCol = mix(mix(tealDot, pearlDot, 0.24), champagneDot, clamp(vTint + uHoverBias * 0.15, 0.0, 1.0));
        vec3 col = mix(baseCol, vec3(0.98, 0.99, 1.0), vWake * 0.65);

        float themeScale = mix(0.82, 0.26, uThemeT);
        gl_FragColor = vec4(col, vAlpha * bokeh * themeScale);
      }
    `,
  })
  materials.push(particleMat)
  const particleCloud = new THREE.Points(particleGeo, particleMat)
  group.add(particleCloud)

  // Reusable math temporaries for zero-GC per-frame InstancedMesh updates
  const dummyObj = new THREE.Object3D()
  const localPos = new THREE.Vector3()
  const ringQuat = new THREE.Quaternion()

  const update = (
    elapsed: number,
    dt: number,
    hoverBias: number,
    themeT: number,
    pointerX = 0,
    pointerY = 0,
    pointerSpeed = 0,
  ) => {
    const safeDt = Math.min(dt, 0.05)

    coronaMat.uniforms.uTime.value = elapsed
    coronaMat.uniforms.uHoverBias.value = hoverBias
    coronaMat.uniforms.uThemeT.value = themeT

    godRayMat.uniforms.uTime.value = elapsed
    godRayMat.uniforms.uHoverBias.value = hoverBias
    godRayMat.uniforms.uThemeT.value = themeT

    haloSpriteMat.opacity = THREE.MathUtils.lerp(0.32, 0.10, themeT)
    haloSpriteMat.color.setHex(hoverBias < -0.25 ? 0x38e8d5 : hoverBias > 0.25 ? 0xf0c27b : 0xcbd5e1)

    for (let i = 0; i < ringMeshes.length; i++) {
      const cfg = ringConfigs[i]
      const mesh = ringMeshes[i]
      mesh.rotation.x += cfg.speed[0] * safeDt
      mesh.rotation.y += cfg.speed[1] * safeDt
      mesh.rotation.z += cfg.speed[2] * safeDt
      ringMats[i].emissiveIntensity = THREE.MathUtils.lerp(0.58, 0.28, themeT)
    }

    // Orbit 48 crystal nodes cleanly along each ring's rotated plane
    for (let i = 0; i < nodeCount; i++) {
      const seed = nodeSeeds[i]
      const cfg = ringConfigs[seed.ringIndex]
      const ringMesh = ringMeshes[seed.ringIndex]
      const angle = seed.baseAngle + elapsed * seed.speed
      const r = cfg.radius + seed.radialOffset
      localPos.set(Math.cos(angle) * r, Math.sin(angle) * r, 0)
      ringQuat.copy(ringMesh.quaternion)
      localPos.applyQuaternion(ringQuat)

      dummyObj.position.copy(localPos)
      dummyObj.rotation.set(elapsed * 0.9 + seed.baseAngle, elapsed * 1.2, 0)
      const pulse = seed.scale * (0.9 + 0.16 * Math.sin(elapsed * 2.5 + seed.baseAngle))
      dummyObj.scale.setScalar(pulse)
      dummyObj.updateMatrix()
      nodeInstanced.setMatrixAt(i, dummyObj.matrix)
    }
    nodeInstanced.instanceMatrix.needsUpdate = true
    nodeMat.emissiveIntensity = THREE.MathUtils.lerp(1.15, 0.5, themeT)

    for (const rMat of ribbonMats) {
      rMat.uniforms.uTime.value = elapsed
      rMat.uniforms.uThemeT.value = themeT
      rMat.uniforms.uHoverBias.value = hoverBias
    }

    particleMat.uniforms.uTime.value = elapsed
    particleMat.uniforms.uThemeT.value = themeT
    particleMat.uniforms.uHoverBias.value = hoverBias
    particleMat.uniforms.uPointerPos.value.set(pointerX * 16.0, 4.35 + pointerY * 7.5, -1.5)
    particleMat.uniforms.uPointerSpeed.value = pointerSpeed

    corePivot.position.y = coreCenter.y + Math.sin(elapsed * 0.72) * 0.16
    corePivot.rotation.y = THREE.MathUtils.lerp(corePivot.rotation.y, -hoverBias * 0.18, Math.min(1, safeDt * 4))
  }

  const dispose = () => {
    for (const geo of geometries) geo.dispose()
    for (const mat of materials) mat.dispose()
    for (const tex of textures) tex.dispose()
    group.clear()
  }

  return {
    group,
    update,
    dispose,
  }
}
