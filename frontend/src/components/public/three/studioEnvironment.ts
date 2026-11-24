import * as THREE from 'three'
import { createSeamlessNoiseTexture, createSoftGlowTexture } from './proceduralTextures'

export interface StudioEnvironmentHandle {
  applyTheme: (themeT: number) => void
  update?: (elapsed: number, hoverBias: number) => void
  dispose: () => void
}

function buildRestrainedEnvRenderTarget(
  pmrem: THREE.PMREMGenerator,
  dark: boolean,
): THREE.WebGLRenderTarget {
  const envScene = new THREE.Scene()
  envScene.background = new THREE.Color(dark ? 0x02040a : 0xdde6f4)

  const geometries: THREE.BufferGeometry[] = []
  const materials: THREE.Material[] = []

  const addPanel = (
    w: number,
    h: number,
    pos: [number, number, number],
    lookAt: [number, number, number],
    rgb: [number, number, number],
  ) => {
    const geo = new THREE.PlaneGeometry(w, h)
    const mat = new THREE.MeshBasicMaterial({
      color: new THREE.Color(rgb[0], rgb[1], rgb[2]),
      side: THREE.DoubleSide,
      toneMapped: false,
    })
    const mesh = new THREE.Mesh(geo, mat)
    mesh.position.set(...pos)
    mesh.lookAt(...lookAt)
    envScene.add(mesh)
    geometries.push(geo)
    materials.push(mat)
  }

  if (dark) {
    addPanel(20, 1.8, [0, 16, -2], [0, 3, -6], [2.2, 2.5, 3.0])
    addPanel(5, 11, [-18, 6, 2], [0, 3, -6], [0.45, 2.8, 2.6])
    addPanel(5, 11, [18, 6, 2], [0, 3, -6], [2.9, 2.0, 0.85])
    addPanel(16, 3.5, [0, 7, -18], [0, 3, 0], [1.1, 1.4, 2.6])
  } else {
    addPanel(24, 5, [0, 16, 0], [0, 3, -6], [3.2, 3.4, 3.6])
    addPanel(6, 12, [-18, 6, 2], [0, 3, -6], [0.8, 2.8, 2.7])
    addPanel(6, 12, [18, 6, 2], [0, 3, -6], [3.2, 2.4, 1.1])
  }

  const rt = pmrem.fromScene(envScene, 0.03)
  geometries.forEach((g) => g.dispose())
  materials.forEach((m) => m.dispose())
  return rt
}

/**
 * Builds the distant horizon mountain & terraced monolith ridge ring once on CPU
 * so the GPU fragment shader executes zero noise loops.
 */
function buildSculptedHorizonRidgeGeometry(radius: number, radialSegs: number): THREE.CylinderGeometry {
  const geo = new THREE.CylinderGeometry(radius, radius, 1, radialSegs, 1, true)
  const posAttr = geo.getAttribute('position') as THREE.BufferAttribute

  for (let i = 0; i < posAttr.count; i++) {
    const x = posAttr.getX(i)
    const y = posAttr.getY(i)
    const z = posAttr.getZ(i)
    const theta = Math.atan2(z, x)
    const nx = Math.cos(theta)
    const nz = Math.sin(theta)

    if (y > 0) {
      // Sculpt natural canyon / jagged horizon skyline combined with subtle monolithic steps
      const wave1 = Math.sin(theta * 3.0 + 0.4) * 3.2 + Math.cos(theta * 5.0 - 0.8) * 2.4
      const wave2 = Math.abs(Math.sin(theta * 11.0 + 1.1)) * 4.2 + Math.cos(theta * 17.0) * 1.4
      const terrace = Math.floor((Math.sin(theta * 7.0) * 0.5 + 0.5) * 4.0) * 0.85
      let h = 2.6 + Math.max(0, 4.5 + wave1 + wave2 * 0.65 + terrace)

      // Open a dramatic lower horizon canyon gap behind center eclipse (-Z axis: nz < 0, nx near 0)
      if (nz < 0) {
        const valleyWeight = Math.exp(-Math.pow(nx * 2.8, 2))
        h *= 1.0 - valleyWeight * 0.62
      }
      posAttr.setY(i, h)
    } else {
      // Skirt down gently below sea level y = 0 to prevent any hairline gap
      posAttr.setY(i, -0.8)
    }
  }

  geo.computeVertexNormals()
  return geo
}

export function createStudioEnvironment(
  scene: THREE.Scene,
  renderer: THREE.WebGLRenderer,
  highTier: boolean,
): StudioEnvironmentHandle {
  const pmrem = new THREE.PMREMGenerator(renderer)
  pmrem.compileEquirectangularShader()

  const darkEnvRT = buildRestrainedEnvRenderTarget(pmrem, true)
  const lightEnvRT = buildRestrainedEnvRenderTarget(pmrem, false)
  pmrem.dispose()

  const noiseTex = createSeamlessNoiseTexture()
  const starTex = createSoftGlowTexture()
  const disposables: Array<{ dispose: () => void }> = [darkEnvRT, lightEnvRT, noiseTex, starTex]

  const darkBg = new THREE.Color(0x03060d)
  const lightBg = new THREE.Color(0xe9eef7)
  const workingBg = darkBg.clone()

  scene.background = workingBg
  const fog = new THREE.FogExp2(0x03060d, 0.0085)
  scene.fog = fog

  // =========================================================================
  // 1. Macro Volumetric Nebula & Horizon Sky Dome (Texture-Sampled, Zero Loops)
  // =========================================================================
  const skyUniforms = {
    uTime: { value: 0 },
    uTheme: { value: 0 },
    uHoverBias: { value: 0 },
    uNoiseTex: { value: noiseTex },
  }
  const skyGeo = new THREE.SphereGeometry(180, 40, 28)
  const skyMat = new THREE.ShaderMaterial({
    uniforms: skyUniforms,
    side: THREE.BackSide,
    depthWrite: false,
    fog: false,
    vertexShader: /* glsl */ `
      varying vec3 vWorldDir;
      void main() {
        vWorldDir = normalize(position);
        gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTime;
      uniform float uTheme;
      uniform float uHoverBias;
      uniform sampler2D uNoiseTex;
      varying vec3 vWorldDir;

      float hash12(vec2 p) {
        vec3 p3 = fract(vec3(p.xyx) * 0.1031);
        p3 += dot(p3, p3.yzx + 33.33);
        return fract((p3.x + p3.y) * p3.z);
      }

      void main() {
        vec3 d = normalize(vWorldDir);
        float horizonBand = exp(-abs(d.y - 0.04) * 6.0);
        float cloudStrataBand = smoothstep(-0.02, 0.13, d.y) * smoothstep(0.45, 0.11, d.y);

        // Two fast hardware texture lookups (seamless cylindrical UV around Y, no seams!)
        float az = atan(d.z, d.x) / 6.2831853 + 0.5;
        vec2 uvA = vec2(az * 2.0 + uTime * 0.004, d.y * 2.2);
        vec2 uvB = vec2(az * 4.0 - uTime * 0.006, d.y * 3.8 + 0.27);
        vec3 nA = texture2D(uNoiseTex, uvA).rgb;
        vec3 nB = texture2D(uNoiseTex, uvB).rgb;
        float nebulaMask = cloudStrataBand * smoothstep(0.28, 0.78, nA.r * 0.62 + nB.g * 0.38);

        float wingMix = smoothstep(-0.62, 0.62, d.x);
        float aicodeBoost = 1.0 + max(0.0, -uHoverBias) * 0.45;
        float qimuBoost = 1.0 + max(0.0, uHoverBias) * 0.45;

        // Dark Macro Cosmos Sky Palette
        vec3 darkZenith = vec3(0.008, 0.014, 0.032);
        vec3 leftCyanAurora = vec3(0.05, 0.40, 0.49) * aicodeBoost;
        vec3 rightChampagneCloud = mix(vec3(0.44, 0.25, 0.11), vec3(0.24, 0.15, 0.38), nB.b) * qimuBoost;
        vec3 horizonHue = mix(leftCyanAurora, rightChampagneCloud, wingMix);

        float frontFacing = pow(clamp(-d.z, 0.0, 1.0), 3.0);
        vec3 darkCol = mix(darkZenith, vec3(0.024, 0.038, 0.078), horizonBand * 0.9);
        darkCol += horizonHue * nebulaMask * 0.64;
        darkCol += vec3(0.15, 0.21, 0.34) * horizonBand * frontFacing * 0.42;
        darkCol += mix(vec3(0.04, 0.22, 0.28), vec3(0.28, 0.17, 0.07), wingMix) * horizonBand * 0.34;

        // Light Architectural High-Altitude Gallery Sky
        vec3 lightZenith = vec3(0.86, 0.90, 0.96);
        vec3 lightHorizon = mix(vec3(0.80, 0.91, 0.94), vec3(0.95, 0.89, 0.81), wingMix);
        vec3 lightCol = mix(lightZenith, lightHorizon, horizonBand * 0.85);
        lightCol += mix(vec3(0.04, 0.14, 0.16), vec3(0.18, 0.12, 0.06), wingMix) * nebulaMask * 0.42;

        vec3 col = mix(darkCol, lightCol, uTheme);
        col += (hash12(gl_FragCoord.xy) - 0.5) * (1.2 / 255.0);
        gl_FragColor = vec4(max(col, 0.0), 1.0);
      }
    `,
  })
  const skyDome = new THREE.Mesh(skyGeo, skyMat)
  scene.add(skyDome)
  disposables.push(skyGeo, skyMat)

  // =========================================================================
  // 2. Crisp Pinpoint Macro Starfield (1200 desktop / 500 mobile)
  // =========================================================================
  const starCount = highTier ? 1200 : 500
  const starPos = new Float32Array(starCount * 3)
  const starColors = new Float32Array(starCount * 3)

  const starPalette = [
    new THREE.Color(0xf5f7fb),
    new THREE.Color(0xbae6fd),
    new THREE.Color(0xfde68a),
    new THREE.Color(0xc4b5fd),
  ]
  const sTmp = new THREE.Color()

  for (let i = 0; i < starCount; i++) {
    const u = ((Math.sin(i * 12.9898 + 1.2) * 43758.5453) % 1 + 1) % 1
    const v = ((Math.sin(i * 78.233 + 4.7) * 28411.172) % 1 + 1) % 1
    const w = ((Math.sin(i * 39.425 + 9.1) * 19283.391) % 1 + 1) % 1

    const theta = u * Math.PI * 2
    const sinPhi = 0.08 + Math.pow(v, 0.85) * 0.9
    const cosPhi = Math.sqrt(Math.max(0, 1 - sinPhi * sinPhi))
    const r = 132 + w * 26

    starPos[i * 3] = Math.cos(theta) * cosPhi * r
    starPos[i * 3 + 1] = sinPhi * r
    starPos[i * 3 + 2] = Math.sin(theta) * cosPhi * r

    const baseCol = starPalette[i % starPalette.length]
    const horizonAtten = Math.min(1, Math.max(0.25, (sinPhi - 0.06) * 4.2))
    const bright = (0.55 + ((i * 7) % 10) * 0.045) * horizonAtten
    sTmp.copy(baseCol).multiplyScalar(bright)
    starColors[i * 3] = sTmp.r
    starColors[i * 3 + 1] = sTmp.g
    starColors[i * 3 + 2] = sTmp.b
  }

  const starGeo = new THREE.BufferGeometry()
  starGeo.setAttribute('position', new THREE.BufferAttribute(starPos, 3))
  starGeo.setAttribute('color', new THREE.BufferAttribute(starColors, 3))
  const starMat = new THREE.PointsMaterial({
    map: starTex,
    size: 0.46,
    vertexColors: true,
    transparent: true,
    opacity: 0.9,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
    fog: false,
  })
  const starField = new THREE.Points(starGeo, starMat)
  scene.add(starField)
  disposables.push(starGeo, starMat)

  // =========================================================================
  // 3. Vertex-Sculpted Horizon Ridge Ring (Zero Fragment Loops, Zero discard)
  // =========================================================================
  const ridgeUniforms = {
    uTheme: { value: 0 },
  }
  const ridgeGeo = buildSculptedHorizonRidgeGeometry(132, 128)
  const ridgeMat = new THREE.ShaderMaterial({
    uniforms: ridgeUniforms,
    side: THREE.BackSide,
    depthWrite: true,
    fog: false,
    vertexShader: /* glsl */ `
      varying vec3 vWorldPos;
      varying float vUvY;
      void main() {
        vUvY = uv.y;
        vec4 wp = modelMatrix * vec4(position, 1.0);
        vWorldPos = wp.xyz;
        gl_Position = projectionMatrix * viewMatrix * wp;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTheme;
      varying vec3 vWorldPos;
      varying float vUvY;

      void main() {
        float ridgeRim = pow(clamp(vUvY, 0.0, 1.0), 4.0);
        float baseMist = 1.0 - smoothstep(0.0, 0.45, vUvY);
        float wingMix = smoothstep(-55.0, 55.0, vWorldPos.x);

        vec3 darkBody = vec3(0.011, 0.018, 0.038);
        vec3 darkRim = mix(vec3(0.08, 0.54, 0.60), vec3(0.72, 0.46, 0.18), wingMix);
        vec3 darkMistCol = mix(vec3(0.025, 0.11, 0.16), vec3(0.12, 0.08, 0.05), wingMix);
        vec3 darkCol = mix(darkBody, darkRim, ridgeRim * 0.42);
        darkCol = mix(darkCol, darkMistCol, baseMist * 0.72);

        vec3 lightBody = vec3(0.69, 0.76, 0.87);
        vec3 lightRim = vec3(0.92, 0.95, 0.99);
        vec3 lightCol = mix(lightBody, lightRim, ridgeRim * 0.45);
        lightCol = mix(lightCol, vec3(0.85, 0.89, 0.95), baseMist * 0.82);

        gl_FragColor = vec4(mix(darkCol, lightCol, uTheme), 1.0);
      }
    `,
  })
  const ridgeMesh = new THREE.Mesh(ridgeGeo, ridgeMat)
  scene.add(ridgeMesh)
  disposables.push(ridgeGeo, ridgeMat)

  // =========================================================================
  // 4. Infinite Calm Dark Mirror Horizon Ocean (Lean 2-Wave Specular Shader)
  // =========================================================================
  const oceanUniforms = {
    uTime: { value: 0 },
    uTheme: { value: 0 },
    uHoverBias: { value: 0 },
  }
  const oceanGeo = new THREE.PlaneGeometry(320, 320)
  const oceanMat = new THREE.ShaderMaterial({
    uniforms: oceanUniforms,
    depthWrite: true,
    fog: false,
    vertexShader: /* glsl */ `
      varying vec3 vWorldPos;
      varying vec3 vToCam;
      void main() {
        vec4 wp = modelMatrix * vec4(position, 1.0);
        vWorldPos = wp.xyz;
        vToCam = cameraPosition - wp.xyz;
        gl_Position = projectionMatrix * viewMatrix * wp;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTime;
      uniform float uTheme;
      uniform float uHoverBias;
      varying vec3 vWorldPos;
      varying vec3 vToCam;

      void main() {
        float dist = length(vToCam);
        vec3 viewDir = vToCam / max(dist, 0.001);

        // Lean 2-wave analytical capillary ripples attenuated with distance & foreground edge
        float damp = 1.0 / (1.0 + dist * 0.05);
        float p1 = vWorldPos.x * 1.25 + vWorldPos.z * 0.85 + uTime * 0.65;
        float p2 = vWorldPos.x * -0.92 + vWorldPos.z * 2.35 - uTime * 0.52;
        float p3 = vWorldPos.z * 4.4 + uTime * 1.1 + sin(vWorldPos.x * 1.4) * 0.8;
        float dx = (cos(p1) * 1.25 - cos(p2) * 0.92) * 0.014 * damp;
        float dz = (cos(p1) * 0.85 + cos(p2) * 2.35 + cos(p3) * 1.35) * 0.022 * damp;

        vec3 n = normalize(vec3(-dx, 1.0, -dz));
        vec3 refDir = reflect(-viewDir, n);
        float ndv = clamp(dot(n, viewDir), 0.0, 1.0);
        float fresnel = pow(1.0 - ndv, 4.2);

        float wingMix = smoothstep(-22.0, 22.0, vWorldPos.x);
        vec3 darkSurface = mix(
          vec3(0.008, 0.014, 0.028),
          mix(vec3(0.02, 0.10, 0.16), vec3(0.13, 0.08, 0.045), wingMix),
          fresnel * 0.36
        );
        vec3 lightSurface = mix(
          vec3(0.78, 0.84, 0.91),
          mix(vec3(0.80, 0.90, 0.96), vec3(0.96, 0.91, 0.85), wingMix),
          fresnel * 0.62
        );

        // Crisp rippled mirror glint columns (fade cleanly before bottom HUD zone z > 2.5)
        float hudFade = smoothstep(8.5, -1.5, vWorldPos.z);
        float rippleMod = 0.55 + 0.45 * sin(vWorldPos.z * 4.8 + uTime * 1.25 + sin(vWorldPos.x * 1.9));

        vec3 eclipseDir = normalize(vec3(0.0, 4.45, -9.5) - vWorldPos);
        float eclipseDot = max(dot(refDir, eclipseDir), 0.0);
        float eclipseSpec = pow(eclipseDot, 165.0) * rippleMod;
        float eclipseTrack = pow(eclipseDot, 42.0) * (0.28 + 0.72 * rippleMod);
        vec3 eclipseHue = mix(
          vec3(0.22, 0.91, 0.84),
          vec3(0.94, 0.76, 0.48),
          clamp(0.5 + uHoverBias * 0.36 + vWorldPos.x * 0.04, 0.0, 1.0)
        );
        vec3 centerSheen = eclipseHue * (eclipseSpec * 1.25 + eclipseTrack * 0.24) * hudFade;

        float aicodeDot = max(dot(refDir, normalize(vec3(-14.5, 3.2, -8.5) - vWorldPos)), 0.0);
        vec3 leftSheen = vec3(0.22, 0.91, 0.84) * pow(aicodeDot, 68.0) * rippleMod * (0.45 + max(0.0, -uHoverBias) * 0.3) * hudFade;

        float qimuDot = max(dot(refDir, normalize(vec3(14.5, 3.6, -8.5) - vWorldPos)), 0.0);
        vec3 rightSheen = vec3(0.94, 0.76, 0.48) * pow(qimuDot, 68.0) * rippleMod * (0.45 + max(0.0, uHoverBias) * 0.3) * hudFade;

        vec3 sheen = (centerSheen + leftSheen + rightSheen) * mix(1.0, 0.32, uTheme);
        darkSurface += sheen;
        lightSurface += sheen * 0.45;

        vec3 waterCol = mix(darkSurface, lightSurface, uTheme);
        vec3 horizonCol = mix(
          mix(vec3(0.025, 0.08, 0.13), vec3(0.11, 0.07, 0.05), wingMix),
          vec3(0.87, 0.90, 0.95),
          uTheme
        );
        float horizonFade = 1.0 - exp(-pow(dist * 0.0082, 1.65));
        waterCol = mix(waterCol, horizonCol, clamp(horizonFade, 0.0, 1.0));

        gl_FragColor = vec4(waterCol, 1.0);
      }
    `,
  })
  const oceanMesh = new THREE.Mesh(oceanGeo, oceanMat)
  oceanMesh.rotation.x = -Math.PI / 2
  oceanMesh.position.y = 0
  scene.add(oceanMesh)
  disposables.push(oceanGeo, oceanMat)

  // =========================================================================
  // 5. Restrained 3-Point Cinematography Lights
  // =========================================================================
  const fillHemi = new THREE.HemisphereLight(0xc8daf7, 0x050914, 0.65)
  scene.add(fillHemi)

  const overheadKey = new THREE.DirectionalLight(0xf1f5f9, 1.35)
  overheadKey.position.set(0, 18, 12)
  scene.add(overheadKey)

  const leftCyanRim = new THREE.DirectionalLight(0x38e8d5, 1.15)
  leftCyanRim.position.set(-22, 10, -8)
  scene.add(leftCyanRim)

  const rightWarmRim = new THREE.DirectionalLight(0xf0c27b, 1.15)
  rightWarmRim.position.set(22, 10, -8)
  scene.add(rightWarmRim)

  const darkHemiSky = new THREE.Color(0xc8daf7)
  const lightHemiSky = new THREE.Color(0xffffff)
  const darkHemiGround = new THREE.Color(0x050914)
  const lightHemiGround = new THREE.Color(0x9fb2ce)

  let lastAppliedBucket = -1

  const applyTheme = (themeT: number) => {
    const t = Math.max(0, Math.min(1, themeT))
    skyUniforms.uTheme.value = t
    ridgeUniforms.uTheme.value = t
    oceanUniforms.uTheme.value = t

    workingBg.copy(darkBg).lerp(lightBg, t)
    fog.color.copy(workingBg)
    fog.density = THREE.MathUtils.lerp(0.0085, 0.0065, t)

    starMat.opacity = THREE.MathUtils.lerp(0.9, 0.06, t)

    fillHemi.color.copy(darkHemiSky).lerp(lightHemiSky, t)
    fillHemi.groundColor.copy(darkHemiGround).lerp(lightHemiGround, t)
    fillHemi.intensity = THREE.MathUtils.lerp(0.65, 1.35, t)

    overheadKey.intensity = THREE.MathUtils.lerp(1.35, 1.85, t)
    leftCyanRim.intensity = THREE.MathUtils.lerp(1.15, 0.85, t)
    rightWarmRim.intensity = THREE.MathUtils.lerp(1.15, 0.85, t)

    const bucket = t > 0.5 ? 1 : 0
    if (bucket !== lastAppliedBucket) {
      lastAppliedBucket = bucket
      scene.environment = bucket === 1 ? lightEnvRT.texture : darkEnvRT.texture
    }
  }

  const update = (elapsed: number, hoverBias: number) => {
    skyUniforms.uTime.value = elapsed
    skyUniforms.uHoverBias.value = hoverBias
    oceanUniforms.uTime.value = elapsed
    oceanUniforms.uHoverBias.value = hoverBias
    starField.rotation.y = elapsed * 0.0022
  }

  applyTheme(0)

  return {
    applyTheme,
    update,
    dispose: () => {
      disposables.forEach((d) => d.dispose())
    },
  }
}
