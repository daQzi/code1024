import * as THREE from 'three'
import { createSeamlessNoiseTexture } from './proceduralTextures'

export interface StudioEnvironmentHandle {
  applyTheme: (themeT: number) => void
  update?: (elapsed: number, hoverBias: number, pointerX?: number, pointerY?: number) => void
  dispose: () => void
}

function createStudioPmrem(renderer: THREE.WebGLRenderer): {
  renderTarget: THREE.WebGLRenderTarget
  texture: THREE.Texture
} {
  const pmrem = new THREE.PMREMGenerator(renderer)
  pmrem.compileEquirectangularShader()

  const envScene = new THREE.Scene()
  envScene.background = new THREE.Color(0x040712)

  const geometries: THREE.BufferGeometry[] = []
  const materials: THREE.Material[] = []

  const addLightCard = (
    color: number,
    intensity: number,
    pos: [number, number, number],
    rot: [number, number, number],
    scale: [number, number, number],
  ) => {
    const geo = new THREE.PlaneGeometry(1, 1)
    const mat = new THREE.MeshBasicMaterial({
      color: new THREE.Color(color).multiplyScalar(intensity),
      side: THREE.DoubleSide,
    })
    const mesh = new THREE.Mesh(geo, mat)
    mesh.position.set(pos[0], pos[1], pos[2])
    mesh.rotation.set(rot[0], rot[1], rot[2])
    mesh.scale.set(scale[0], scale[1], scale[2])
    envScene.add(mesh)
    geometries.push(geo)
    materials.push(mat)
  }

  // Zenith white-champagne specular strip
  addLightCard(0xf8fafc, 2.2, [0, 14, 0], [Math.PI / 2, 0, 0], [28, 12, 1])
  // Left AICode teal studio key card
  addLightCard(0x38e8d5, 1.9, [-16, 5, -4], [0, Math.PI / 2.2, 0], [18, 11, 1])
  // Right Qimu champagne-gold cinema key card
  addLightCard(0xf0c27b, 1.9, [16, 5, -4], [0, -Math.PI / 2.2, 0], [18, 11, 1])
  // Neutral champagne-platinum cosmic crown rim (replaces violet wash on plinths)
  addLightCard(0xe2e8f0, 1.3, [0, 8, -18], [0, 0, 0], [26, 8, 1])

  const renderTarget = pmrem.fromScene(envScene, 0.04)
  for (const geo of geometries) geo.dispose()
  for (const mat of materials) mat.dispose()
  pmrem.dispose()

  return { renderTarget, texture: renderTarget.texture }
}

function buildSculptedHorizonRidgeGeometry(radialSegments = 132, radius = 128): THREE.BufferGeometry {
  const cols = radialSegments + 1
  const positions = new Float32Array(cols * 2 * 3)
  const uvs = new Float32Array(cols * 2 * 2)
  const indices: number[] = []

  for (let i = 0; i <= radialSegments; i++) {
    const t = i / radialSegments
    // Cover front + side horizon from -130 deg to +130 deg around -Z axis
    const angle = (t - 0.5) * Math.PI * 1.44
    const x = Math.sin(angle) * radius
    const z = -Math.cos(angle) * radius

    // Valley at center (angle ~ 0) so the optical eclipse rises majestically above the ridge
    const centerValley = 1.0 - Math.exp(-angle * angle * 5.8)
    const ridgeNoise =
      Math.sin(angle * 7.0 + 0.6) * 3.8 +
      Math.cos(angle * 15.0 - 1.1) * 2.1 +
      Math.sin(angle * 31.0 + 0.4) * 0.9
    const height = 1.8 + centerValley * (12.5 + Math.max(-2.5, ridgeNoise))

    const baseIdx = i * 2
    positions[baseIdx * 3] = x
    positions[baseIdx * 3 + 1] = -0.4
    positions[baseIdx * 3 + 2] = z

    positions[(baseIdx + 1) * 3] = x
    positions[(baseIdx + 1) * 3 + 1] = height
    positions[(baseIdx + 1) * 3 + 2] = z

    uvs[baseIdx * 2] = t
    uvs[baseIdx * 2 + 1] = 0

    uvs[(baseIdx + 1) * 2] = t
    uvs[(baseIdx + 1) * 2 + 1] = 1

    if (i < radialSegments) {
      const a = baseIdx
      const b = baseIdx + 1
      const c = baseIdx + 2
      const d = baseIdx + 3
      indices.push(a, c, b, b, c, d)
    }
  }

  const geo = new THREE.BufferGeometry()
  geo.setAttribute('position', new THREE.BufferAttribute(positions, 3))
  geo.setAttribute('uv', new THREE.BufferAttribute(uvs, 2))
  geo.setIndex(indices)
  geo.computeVertexNormals()
  return geo
}

export function createStudioEnvironment(
  scene: THREE.Scene,
  renderer: THREE.WebGLRenderer,
  highTier: boolean,
): StudioEnvironmentHandle {
  const noiseTex = createSeamlessNoiseTexture()
  const pmrem = createStudioPmrem(renderer)
  scene.environment = pmrem.texture

  const fogColorDark = new THREE.Color(0x03050c)
  const fogColorLight = new THREE.Color(0xeef2fa)
  const fog = new THREE.FogExp2(fogColorDark.getHex(), 0.0115)
  scene.fog = fog

  // Studio 3-point + hemisphere directional illumination for high-FPS clarity
  const hemiLight = new THREE.HemisphereLight(0x93c5fd, 0x0b1022, 0.72)
  scene.add(hemiLight)

  const keyLight = new THREE.DirectionalLight(0xf8fafc, 1.35)
  keyLight.position.set(0, 24, 16)
  scene.add(keyLight)

  const aiCodeRim = new THREE.DirectionalLight(0x22d3ee, 1.65)
  aiCodeRim.position.set(-22, 11, -10)
  scene.add(aiCodeRim)

  const qimuRim = new THREE.DirectionalLight(0xfbbf24, 1.65)
  qimuRim.position.set(22, 11, -10)
  scene.add(qimuRim)

  // 1. Domain-Warped Flowing Aurora & Cosmic Nebula Sky Dome
  const skyGeo = new THREE.SphereGeometry(180, 48, 32)
  const skyMat = new THREE.ShaderMaterial({
    side: THREE.BackSide,
    depthWrite: false,
    fog: false,
    uniforms: {
      uTime: { value: 0 },
      uThemeT: { value: 0 },
      uHoverBias: { value: 0 },
      uPointer: { value: new THREE.Vector2(0, 0) },
      uNoiseTex: { value: noiseTex },
    },
    vertexShader: /* glsl */ `
      varying vec3 vWorldDir;
      void main() {
        vec4 worldPos = modelMatrix * vec4(position, 1.0);
        vWorldDir = normalize(worldPos.xyz);
        gl_Position = projectionMatrix * viewMatrix * worldPos;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTime;
      uniform float uThemeT;
      uniform float uHoverBias;
      uniform vec2 uPointer;
      uniform sampler2D uNoiseTex;
      varying vec3 vWorldDir;

      void main() {
        vec3 dir = normalize(vWorldDir);
        float horizon = clamp(dir.y, -0.3, 1.0);
        float angleX = dir.x;

        // 3-lookup domain-warped seamless noise for rich full-viewport flowing aurora
        vec2 baseUv = vec2(atan(dir.x, -dir.z) * 0.28 + 0.5, dir.y * 0.92 + 0.14);
        vec2 drift1 = vec2(uTime * 0.018, -uTime * 0.011);
        vec3 n1 = texture2D(uNoiseTex, baseUv * 1.45 + drift1).rgb;

        vec2 warp = (n1.rg - 0.5) * 0.26;
        vec3 n2 = texture2D(uNoiseTex, baseUv * 2.8 + warp + vec2(-uTime * 0.024, uTime * 0.015)).rgb;
        vec3 n3 = texture2D(uNoiseTex, baseUv * 4.4 + (n2.rg - 0.5) * 0.18 + vec2(uTime * 0.013, uTime * 0.021)).rgb;

        // Cascading aurora curtains spanning lower-mid to zenith sky (-0.08..+0.92)
        float bandLow = smoothstep(-0.08, 0.26, dir.y) * (1.0 - smoothstep(0.32, 0.92, dir.y));
        float bandZenith = smoothstep(0.14, 0.55, dir.y) * (1.0 - smoothstep(0.68, 1.0, dir.y));
        float curtain1 = smoothstep(0.32, 0.78, n2.r * 0.65 + n3.g * 0.35);
        float curtain2 = smoothstep(0.38, 0.84, n1.b * 0.55 + n3.r * 0.45);
        float verticalStreaks = smoothstep(0.28, 0.85, sin(baseUv.x * 38.0 + n2.g * 6.2831) * 0.5 + 0.5);

        float sideRight = smoothstep(-0.48, 0.48, angleX + n1.r * 0.16);
        float biasShift = clamp(uHoverBias * 0.35, -0.35, 0.35);
        float splitMix = clamp(sideRight + biasShift * 0.45, 0.0, 1.0);

        // Dark Obsidian palette: crisp obsidian zenith + architectural teal left & warm champagne right silk
        vec3 darkZenith = vec3(0.008, 0.014, 0.032);
        vec3 darkHorizon = vec3(0.018, 0.034, 0.072);
        vec3 darkBase = mix(darkHorizon, darkZenith, smoothstep(-0.02, 0.65, horizon));

        vec3 tealAurora = vec3(0.10, 0.84, 0.78);
        vec3 slateBlueAurora = vec3(0.24, 0.44, 0.88);
        vec3 champagneAurora = vec3(0.95, 0.72, 0.34);
        vec3 warmSilkAurora = vec3(0.86, 0.56, 0.28);

        vec3 leftTint = mix(tealAurora, slateBlueAurora, n2.b * 0.55);
        vec3 rightTint = mix(champagneAurora, warmSilkAurora, n3.b * 0.55);
        vec3 auroraColor = mix(leftTint, rightTint, splitMix);

        float auroraEnergy = (curtain1 * bandLow * 0.82 + curtain2 * bandZenith * 0.58) * (0.58 + 0.42 * verticalStreaks);
        float centerHalo = exp(-angleX * angleX * 2.6) * exp(-max(dir.y - 0.06, 0.0) * 5.2);
        float dither = (n3.r - 0.5) * 0.012;
        vec3 darkCol = darkBase + auroraColor * auroraEnergy * 0.78 + mix(vec3(0.14, 0.72, 0.76), vec3(0.94, 0.70, 0.34), splitMix) * centerHalo * 0.36 + vec3(dither);

        // Light Mode: MotionSites 31-deg Fluted (Reeded) Glass + Marble Swirl + Pointer Chroma Blob
        vec2 screenP = vec2(atan(dir.x, -dir.z) * 1.15, dir.y * 1.15);
        float angle = 0.541;
        float ca = cos(angle);
        float sa = sin(angle);
        vec2 rotP = vec2(ca * screenP.x - sa * screenP.y, sa * screenP.x + ca * screenP.y);
        float fluteCurve = sin(rotP.x * 18.0 * 3.14159);
        vec2 fluteShift = vec2(ca, sa) * fluteCurve * 0.035;

        vec2 lightUvR = baseUv * 1.35 + fluteShift * 1.12 + vec2(uTime * 0.014, -uTime * 0.010);
        vec2 lightUvB = baseUv * 1.35 + fluteShift * 0.88 + vec2(uTime * 0.014, -uTime * 0.010);
        float swirlR = texture2D(uNoiseTex, lightUvR).r;
        float swirlG = texture2D(uNoiseTex, (lightUvR + lightUvB) * 0.5).g;
        float swirlB = texture2D(uNoiseTex, lightUvB).b;
        vec3 marble = mix(vec3(0.925, 0.945, 0.978), vec3(0.985, 0.988, 0.996), vec3(swirlR, swirlG, swirlB));

        vec2 pAim = vec2(uPointer.x * 0.55, uPointer.y * 0.25 + 0.12);
        vec2 chromaDelta = vec2(atan(dir.x, -dir.z), dir.y) + fluteShift * 0.65 - pAim;
        float pointerBlob = exp(-dot(chromaDelta, chromaDelta) * 2.8);
        vec3 chromaCol = mix(vec3(0.22, 0.82, 0.80), vec3(0.94, 0.72, 0.36), splitMix);

        float ridgeHighlight = pow(max(0.0, fluteCurve), 4.0) * 0.085;
        vec3 lightBase = mix(vec3(0.955, 0.962, 0.982), marble, smoothstep(-0.08, 0.65, horizon));
        vec3 lightCol = mix(lightBase, chromaCol, clamp(pointerBlob * 0.26 + auroraEnergy * 0.14 + centerHalo * 0.08, 0.0, 0.38)) + vec3(ridgeHighlight) + vec3(dither * 0.5);

        vec3 finalCol = mix(darkCol, lightCol, uThemeT);
        gl_FragColor = vec4(finalCol, 1.0);
      }
    `,
  })
  const skyDome = new THREE.Mesh(skyGeo, skyMat)
  scene.add(skyDome)

  // 2. Twinkling Starfield across Sky Dome
  const starCount = highTier ? 1600 : 700
  const starPos = new Float32Array(starCount * 3)
  const starMeta = new Float32Array(starCount * 4) // size, phase, speed, tintMix
  for (let i = 0; i < starCount; i++) {
    const theta = ((i * 2.3999632) % (Math.PI * 2)) + Math.sin(i * 0.73) * 0.35
    const u = Math.abs(Math.sin(i * 12.9898) * 43758.5453) % 1
    const yNorm = 0.03 + Math.pow(u, 0.82) * 0.94
    const rXZ = Math.sqrt(Math.max(0.001, 1 - yNorm * yNorm))
    const radius = 82 + ((i * 29) % 72)

    starPos[i * 3] = Math.cos(theta) * rXZ * radius
    starPos[i * 3 + 1] = yNorm * radius
    starPos[i * 3 + 2] = Math.sin(theta) * rXZ * radius

    starMeta[i * 4] = 1.6 + ((i * 17) % 10) * 0.34
    starMeta[i * 4 + 1] = (i * 1.37) % (Math.PI * 2)
    starMeta[i * 4 + 2] = 0.9 + ((i * 13) % 9) * 0.25
    starMeta[i * 4 + 3] = starPos[i * 3] >= 0 ? 0.65 + (i % 5) * 0.08 : (i % 5) * 0.08
  }

  const starGeo = new THREE.BufferGeometry()
  starGeo.setAttribute('position', new THREE.BufferAttribute(starPos, 3))
  starGeo.setAttribute('aMeta', new THREE.BufferAttribute(starMeta, 4))

  const starMat = new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    fog: false,
    blending: THREE.AdditiveBlending,
    uniforms: {
      uTime: { value: 0 },
      uThemeT: { value: 0 },
      uPixelRatio: { value: Math.min(window.devicePixelRatio || 1, 1.25) },
    },
    vertexShader: /* glsl */ `
      attribute vec4 aMeta;
      uniform float uTime;
      uniform float uPixelRatio;
      varying float vTwinkle;
      varying float vTint;

      void main() {
        vec4 mvPosition = modelViewMatrix * vec4(position, 1.0);
        float twinkle = 0.45 + 0.55 * sin(uTime * aMeta.z + aMeta.y);
        vTwinkle = twinkle;
        vTint = aMeta.w;
        gl_PointSize = aMeta.x * (0.75 + 0.45 * twinkle) * uPixelRatio * (115.0 / max(20.0, -mvPosition.z));
        gl_Position = projectionMatrix * mvPosition;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uThemeT;
      varying float vTwinkle;
      varying float vTint;

      void main() {
        vec2 c = gl_PointCoord - vec2(0.5);
        float r2 = dot(c, c);
        if (r2 > 0.25) discard;
        float core = exp(-r2 * 18.0);
        vec3 cyanStar = vec3(0.55, 0.92, 1.0);
        vec3 goldStar = vec3(1.0, 0.84, 0.52);
        vec3 whiteStar = vec3(0.96, 0.98, 1.0);
        vec3 tint = mix(cyanStar, goldStar, vTint);
        vec3 col = mix(tint, whiteStar, core * 0.72);
        float alpha = core * vTwinkle * (1.0 - 0.78 * uThemeT);
        gl_FragColor = vec4(col, alpha);
      }
    `,
  })
  const starField = new THREE.Points(starGeo, starMat)
  scene.add(starField)

  // 3. Sculpted Horizon Canyon Ridge with Luminous Cyan/Gold Rim
  const ridgeGeo = buildSculptedHorizonRidgeGeometry(132, 128)
  const ridgeMat = new THREE.ShaderMaterial({
    transparent: false,
    depthWrite: true,
    fog: false,
    side: THREE.DoubleSide,
    uniforms: {
      uTime: { value: 0 },
      uThemeT: { value: 0 },
      uHoverBias: { value: 0 },
    },
    vertexShader: /* glsl */ `
      varying vec2 vUv;
      varying vec3 vWorldPos;
      void main() {
        vUv = uv;
        vec4 wp = modelMatrix * vec4(position, 1.0);
        vWorldPos = wp.xyz;
        gl_Position = projectionMatrix * viewMatrix * wp;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTime;
      uniform float uThemeT;
      uniform float uHoverBias;
      varying vec2 vUv;
      varying vec3 vWorldPos;

      void main() {
        float rimBand = pow(clamp(vUv.y, 0.0, 1.0), 6.0);
        float sideRight = smoothstep(-32.0, 32.0, vWorldPos.x + uHoverBias * 14.0);
        vec3 darkBody = vec3(0.015, 0.022, 0.048);
        vec3 lightBody = vec3(0.82, 0.86, 0.94);

        vec3 cyanRim = vec3(0.16, 0.86, 0.98);
        vec3 goldRim = vec3(0.98, 0.72, 0.24);
        vec3 rimColor = mix(cyanRim, goldRim, sideRight);

        float pulse = 0.85 + 0.15 * sin(uTime * 1.4 + vWorldPos.x * 0.08);
        vec3 darkCol = darkBody + rimColor * rimBand * 0.95 * pulse;
        vec3 lightCol = mix(lightBody, rimColor * 0.92, rimBand * 0.45);
        vec3 finalCol = mix(darkCol, lightCol, uThemeT);
        gl_FragColor = vec4(finalCol, 1.0);
      }
    `,
  })
  const horizonRidge = new THREE.Mesh(ridgeGeo, ridgeMat)
  scene.add(horizonRidge)

  // 4. Full-Screen Luminous Aurora-Mirror & fwidth Architectural Energy-Grid Floor
  const floorGeo = new THREE.PlaneGeometry(320, 320, 1, 1)
  const floorMat = new THREE.ShaderMaterial({
    transparent: false,
    depthWrite: true,
    fog: false,
    uniforms: {
      uTime: { value: 0 },
      uThemeT: { value: 0 },
      uHoverBias: { value: 0 },
      uNoiseTex: { value: noiseTex },
    },
    vertexShader: /* glsl */ `
      varying vec3 vWorldPos;
      void main() {
        vec4 wp = modelMatrix * vec4(position, 1.0);
        vWorldPos = wp.xyz;
        gl_Position = projectionMatrix * viewMatrix * wp;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTime;
      uniform float uThemeT;
      uniform float uHoverBias;
      uniform sampler2D uNoiseTex;
      varying vec3 vWorldPos;

      void main() {
        vec2 xz = vWorldPos.xz;
        float distFromCore = length(xz - vec2(0.0, -8.0));
        float radialFade = smoothstep(108.0, 10.0, length(xz));

        // Rippled aurora reflections across dark obsidian mirror
        vec2 rippleUv = xz * 0.018 + vec2(uTime * 0.022, -uTime * 0.016);
        vec3 nA = texture2D(uNoiseTex, rippleUv).rgb;
        vec3 nB = texture2D(uNoiseTex, xz * 0.036 + (nA.rg - 0.5) * 0.3 + vec2(-uTime * 0.015, uTime * 0.028)).rgb;

        float sideMix = smoothstep(-18.0, 18.0, xz.x + uHoverBias * 9.0);
        vec3 tealCol = vec3(0.14, 0.88, 0.82);
        vec3 slateBlueCol = vec3(0.28, 0.48, 0.92);
        vec3 champagneCol = vec3(0.96, 0.76, 0.38);
        vec3 warmGoldCol = vec3(0.88, 0.58, 0.26);

        vec3 leftFloorCol = mix(tealCol, slateBlueCol, nA.b * 0.55);
        vec3 rightFloorCol = mix(champagneCol, warmGoldCol, nB.b * 0.55);
        vec3 auraTint = mix(leftFloorCol, rightFloorCol, sideMix);

        // Mirror specular reflection from horizon & side worlds
        float mirrorBand = exp(-max(distFromCore - 3.0, 0.0) * 0.042);
        float auroraReflect = smoothstep(0.35, 0.82, nA.r * 0.55 + nB.g * 0.45) * radialFade;

        // Crisp 3.5m architectural energy grid with hardware fwidth anti-aliasing
        vec2 gridCoord = xz / 3.5;
        vec2 gridDeriv = max(fwidth(gridCoord), vec2(0.0008));
        vec2 gridDist = abs(fract(gridCoord - 0.5) - 0.5) / gridDeriv;
        float gridLine = 1.0 - min(min(gridDist.x, gridDist.y), 1.0);
        float gridMask = smoothstep(92.0, 12.0, length(xz));
        gridLine *= gridMask;

        // Secondary 17.5m macro grid lines
        vec2 macroCoord = xz / 17.5;
        vec2 macroDeriv = max(fwidth(macroCoord), vec2(0.0008));
        vec2 macroDist = abs(fract(macroCoord - 0.5) - 0.5) / macroDeriv;
        float macroLine = (1.0 - min(min(macroDist.x, macroDist.y), 1.0)) * gridMask;

        // Racing data pulses traveling continuously along X and Z grid channels
        float pulseZ = pow(max(0.0, sin(xz.y * 0.42 - uTime * 3.8 + nA.r * 2.0)), 6.0);
        float pulseX = pow(max(0.0, sin(xz.x * 0.38 + uTime * 3.2 - nB.g * 2.0)), 6.0);
        float channelPulse = max(pulseZ * (1.0 - min(gridDist.x, 1.0)), pulseX * (1.0 - min(gridDist.y, 1.0))) * gridMask;

        // 4-second periodic expanding shockwave ring radiating from optical core (0, -8)
        float shockPhase = fract(uTime * 0.25);
        float shockRadius = shockPhase * 62.0;
        float shockRing = exp(-abs(distFromCore - shockRadius) * 0.55) * (1.0 - shockPhase) * smoothstep(2.0, 6.0, shockRadius);

        // Foreground-center quiet zone for crisp scroll cue legibility (z > 10.5 && |x| < 4.5)
        float fgQuiet = smoothstep(9.0, 13.5, xz.y) * (1.0 - smoothstep(2.2, 5.8, abs(xz.x)));

        vec3 darkObsidian = vec3(0.008, 0.013, 0.028);
        vec3 darkCol = darkObsidian;
        darkCol += auraTint * (auroraReflect * 0.20 + mirrorBand * 0.24);
        darkCol += auraTint * (gridLine * (0.26 - 0.12 * fgQuiet) + macroLine * 0.36);
        darkCol += mix(vec3(0.45, 0.94, 0.92), vec3(0.98, 0.86, 0.52), sideMix) * (channelPulse * 0.82 * (1.0 - 0.45 * fgQuiet));
        darkCol += mix(vec3(0.28, 0.88, 0.86), vec3(0.98, 0.78, 0.36), sideMix) * (shockRing * (0.30 + 0.68 * gridLine));
        darkCol = mix(darkCol, vec3(0.007, 0.011, 0.024), fgQuiet * 0.45);

        // Light mode: polished champagne-alabaster studio glass floor with crisp slate-cyan/gold grid
        vec3 lightGlass = vec3(0.93, 0.95, 0.985);
        vec3 lightGridTint = mix(vec3(0.06, 0.54, 0.78), vec3(0.76, 0.48, 0.08), sideMix);
        vec3 lightCol = lightGlass;
        lightCol = mix(lightCol, mix(lightGlass, auraTint, 0.16), auroraReflect * 0.45 + mirrorBand * 0.35);
        lightCol = mix(lightCol, lightGridTint, clamp(gridLine * 0.24 + macroLine * 0.34 + shockRing * 0.22, 0.0, 0.45));
        lightCol += lightGridTint * channelPulse * 0.26;
        lightCol = mix(lightCol, vec3(0.94, 0.955, 0.985), fgQuiet * 0.38);

        // Soft analytical horizon blend into sky dome (eliminates any far plane cutoff edge)
        vec3 horizonMist = mix(vec3(0.022, 0.040, 0.095), vec3(0.955, 0.965, 0.985), uThemeT);
        vec3 finalCol = mix(mix(darkCol, lightCol, uThemeT), horizonMist, smoothstep(54.0, 118.0, length(xz)));
        gl_FragColor = vec4(finalCol, 1.0);
      }
    `,
  })
  const floorMesh = new THREE.Mesh(floorGeo, floorMat)
  floorMesh.rotation.x = -Math.PI / 2
  floorMesh.position.y = -0.02
  scene.add(floorMesh)

  let currentThemeT = 0

  const applyTheme = (themeT: number) => {
    currentThemeT = THREE.MathUtils.clamp(themeT, 0, 1)
    skyMat.uniforms.uThemeT.value = currentThemeT
    starMat.uniforms.uThemeT.value = currentThemeT
    ridgeMat.uniforms.uThemeT.value = currentThemeT
    floorMat.uniforms.uThemeT.value = currentThemeT

    fog.color.copy(fogColorDark).lerp(fogColorLight, currentThemeT)
    fog.density = THREE.MathUtils.lerp(0.0115, 0.0085, currentThemeT)

    hemiLight.intensity = THREE.MathUtils.lerp(0.72, 1.15, currentThemeT)
    hemiLight.groundColor.setHex(currentThemeT > 0.5 ? 0xdbeafe : 0x0b1022)
    keyLight.intensity = THREE.MathUtils.lerp(1.35, 1.6, currentThemeT)
    aiCodeRim.intensity = THREE.MathUtils.lerp(1.65, 1.15, currentThemeT)
    qimuRim.intensity = THREE.MathUtils.lerp(1.65, 1.15, currentThemeT)
  }

  const update = (elapsed: number, hoverBias: number, pointerX = 0, pointerY = 0) => {
    skyMat.uniforms.uTime.value = elapsed
    skyMat.uniforms.uHoverBias.value = hoverBias
    skyMat.uniforms.uPointer.value.set(pointerX, pointerY)
    starMat.uniforms.uTime.value = elapsed
    ridgeMat.uniforms.uTime.value = elapsed
    ridgeMat.uniforms.uHoverBias.value = hoverBias
    floorMat.uniforms.uTime.value = elapsed
    floorMat.uniforms.uHoverBias.value = hoverBias
  }

  const dispose = () => {
    scene.remove(hemiLight, keyLight, aiCodeRim, qimuRim, skyDome, starField, horizonRidge, floorMesh)
    skyGeo.dispose()
    skyMat.dispose()
    starGeo.dispose()
    starMat.dispose()
    ridgeGeo.dispose()
    ridgeMat.dispose()
    floorGeo.dispose()
    floorMat.dispose()
    noiseTex.dispose()
    pmrem.renderTarget.dispose()
    scene.environment = null
    scene.fog = null
  }

  return {
    applyTheme,
    update,
    dispose,
  }
}
