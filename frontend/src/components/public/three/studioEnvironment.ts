import * as THREE from 'three'
import { createArchitecturalConcreteMaps, createSoftGlowTexture } from './proceduralTextures'

export interface StudioEnvironmentHandle {
  applyTheme: (themeT: number) => void
  dispose: () => void
}

function buildStudioEnvRenderTarget(
  pmrem: THREE.PMREMGenerator,
  dark: boolean,
): THREE.WebGLRenderTarget {
  const envScene = new THREE.Scene()
  envScene.background = new THREE.Color(dark ? 0x02050c : 0xcad8ec)

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
    // Architectural cinema studio lighting dome: negative fill walls + bright softbox strips
    addPanel(24, 2.2, [0, 15, -4], [0, 0, -4], [6.5, 7.0, 7.8]) // Overhead skylight 1
    addPanel(24, 2.2, [0, 15, 5], [0, 0, 0], [5.8, 6.2, 6.9]) // Overhead skylight 2
    addPanel(6.5, 13, [-18, 5.5, 2], [0, 2.5, -4], [0.85, 5.8, 5.4]) // Left AICode cyan-teal reflector
    addPanel(6.5, 13, [18, 5.5, 2], [0, 2.5, -4], [6.6, 4.4, 1.35]) // Right Qimu warm tungsten softbox
    addPanel(20, 4.5, [0, 8.5, -20], [0, 2.5, 0], [2.1, 2.8, 5.8]) // Deep indigo rear rim
    addPanel(14, 2.0, [0, 1.0, 18], [0, 3, -3], [1.6, 2.0, 2.6]) // Subtle front lens fill
    // Dark negative-fill baffles for crisp architectural metal contrast
    addPanel(12, 10, [-10, 4, 12], [0, 2, 0], [0.02, 0.03, 0.05])
    addPanel(12, 10, [10, 4, 12], [0, 2, 0], [0.02, 0.03, 0.05])
  } else {
    // Sunlit architectural gallery concrete softboxes
    addPanel(26, 6, [0, 16, 0], [0, 0, -4], [4.6, 4.8, 5.1])
    addPanel(7, 13, [-18, 6, 2], [0, 2.5, -4], [0.9, 4.2, 4.0])
    addPanel(7, 13, [18, 6, 2], [0, 2.5, -4], [4.6, 3.3, 1.4])
    addPanel(20, 5, [0, 8, -20], [0, 2.5, 0], [2.4, 2.9, 4.4])
  }

  const rt = pmrem.fromScene(envScene, 0.025)
  geometries.forEach((g) => g.dispose())
  materials.forEach((m) => m.dispose())
  return rt
}

function createChamferedPillarGeometry(width: number, height: number, depth: number, bevel: number): THREE.ExtrudeGeometry {
  const hw = width / 2 - bevel
  const hd = depth / 2 - bevel
  const shape = new THREE.Shape()
  shape.moveTo(-hw, -hd)
  shape.lineTo(hw, -hd)
  shape.lineTo(hw, hd)
  shape.lineTo(-hw, hd)
  shape.closePath()

  const geo = new THREE.ExtrudeGeometry(shape, {
    depth: height - bevel * 2,
    bevelEnabled: true,
    bevelThickness: bevel,
    bevelSize: bevel,
    bevelSegments: 2,
  })
  // Orient extrusion along +Y starting at y = 0
  geo.rotateX(-Math.PI / 2)
  geo.translate(0, bevel, 0)
  return geo
}

export function createStudioEnvironment(
  scene: THREE.Scene,
  renderer: THREE.WebGLRenderer,
  highTier: boolean,
): StudioEnvironmentHandle {
  const pmrem = new THREE.PMREMGenerator(renderer)
  pmrem.compileEquirectangularShader()

  const darkEnvRT = buildStudioEnvRenderTarget(pmrem, true)
  const lightEnvRT = buildStudioEnvRenderTarget(pmrem, false)
  pmrem.dispose()

  const disposables: Array<{ dispose: () => void }> = [darkEnvRT, lightEnvRT]

  const darkBg = new THREE.Color(0x04070f)
  const lightBg = new THREE.Color(0xdde6f4)
  const workingBg = darkBg.clone()

  scene.background = workingBg
  const fog = new THREE.FogExp2(0x04070f, 0.0145)
  scene.fog = fog

  // =========================================================================
  // 1. Custom GLSL Atmospheric Horizon Cyclorama Dome
  // =========================================================================
  const skyUniforms = {
    uTheme: { value: 0 },
  }
  const skyGeo = new THREE.SphereGeometry(88, 32, 20)
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
      uniform float uTheme;
      varying vec3 vWorldDir;

      float hash12(vec2 p) {
        vec3 p3 = fract(vec3(p.xyx) * 0.1031);
        p3 += dot(p3, p3.yzx + 33.33);
        return fract((p3.x + p3.y) * p3.z);
      }

      void main() {
        vec3 d = normalize(vWorldDir);
        float elev = clamp(d.y * 0.5 + 0.5, 0.0, 1.0);
        float horizonBand = exp(-abs(d.y - 0.03) * 6.5);
        float leftRight = smoothstep(-0.75, 0.75, d.x);

        // Dark architectural cinema soundstage atmosphere
        vec3 darkZenith = vec3(0.014, 0.024, 0.052);
        vec3 darkHorizonLeft = vec3(0.035, 0.16, 0.21);
        vec3 darkHorizonRight = vec3(0.18, 0.11, 0.045);
        vec3 darkHorizon = mix(darkHorizonLeft, darkHorizonRight, leftRight);
        vec3 darkCol = mix(darkZenith, darkHorizon, horizonBand * 0.82);
        darkCol = mix(darkCol, vec3(0.012, 0.018, 0.038), smoothstep(0.0, -0.35, d.y));

        // Light gallery alabaster-slate sky
        vec3 lightZenith = vec3(0.82, 0.87, 0.96);
        vec3 lightLeft = vec3(0.76, 0.90, 0.92);
        vec3 lightRight = vec3(0.95, 0.89, 0.80);
        vec3 lightHorizon = mix(lightLeft, lightRight, leftRight);
        vec3 lightCol = mix(lightZenith, lightHorizon, horizonBand * 0.72);
        lightCol = mix(lightCol, vec3(0.84, 0.88, 0.95), smoothstep(0.0, -0.3, d.y));

        vec3 col = mix(darkCol, lightCol, uTheme);
        // Ultra-fine dithering eliminates dark atmospheric gradient banding
        col += (hash12(gl_FragCoord.xy) - 0.5) * (1.2 / 255.0);
        gl_FragColor = vec4(max(col, 0.0), 1.0);
      }
    `,
  })
  const skyDome = new THREE.Mesh(skyGeo, skyMat)
  scene.add(skyDome)
  disposables.push(skyGeo, skyMat)

  // =========================================================================
  // 2. Three-Point Cinematography Lighting Rig (Zero RectAreaLight)
  // =========================================================================
  const fillHemi = new THREE.HemisphereLight(0xc8daf7, 0x090f1d, 0.85)
  scene.add(fillHemi)

  const overheadKey = new THREE.DirectionalLight(0xeef5ff, 2.2)
  overheadKey.position.set(0, 18, 9)
  scene.add(overheadKey)

  const leftCyanRim = new THREE.DirectionalLight(0x2dd4bf, 1.55)
  leftCyanRim.position.set(-22, 9, -8)
  scene.add(leftCyanRim)

  const rightWarmRim = new THREE.DirectionalLight(0xfbbf24, 1.48)
  rightWarmRim.position.set(22, 9, -8)
  scene.add(rightWarmRim)

  const aicodePoint = new THREE.PointLight(0x38bdf8, 12.0, 26, 1.45)
  aicodePoint.position.set(-14.5, 5.5, -3.5)
  scene.add(aicodePoint)

  const qimuPoint = new THREE.PointLight(0xf59e0b, 12.5, 28, 1.45)
  qimuPoint.position.set(14.5, 5.5, -3.5)
  scene.add(qimuPoint)

  // =========================================================================
  // 3. Monumental Brutalist Concrete Colonnade, Horizon Portal & Skylights
  // =========================================================================
  const concreteMaps = createArchitecturalConcreteMaps()
  disposables.push(concreteMaps)

  const architecturalConcreteMat = new THREE.MeshStandardMaterial({
    color: 0x0e1523,
    roughness: 0.78,
    metalness: 0.14,
    roughnessMap: concreteMaps.roughnessMap,
    normalMap: concreteMaps.normalMap,
    normalScale: new THREE.Vector2(0.42, 0.42),
    envMapIntensity: 1.05,
  })
  const matteSteelMat = new THREE.MeshStandardMaterial({
    color: 0x0a101b,
    roughness: 0.36,
    metalness: 0.88,
    envMapIntensity: 1.4,
  })
  const inlayTrimMat = new THREE.MeshStandardMaterial({
    color: 0x64748b,
    roughness: 0.25,
    metalness: 0.92,
    envMapIntensity: 1.5,
  })
  disposables.push(architecturalConcreteMat, matteSteelMat, inlayTrimMat)

  // 14 towering chamfered concrete monolith ribs along x = ±23 (InstancedMesh)
  const ribGeo = createChamferedPillarGeometry(1.1, 16.0, 2.6, 0.08)
  const ribMesh = new THREE.InstancedMesh(ribGeo, architecturalConcreteMat, 14)
  const dummy = new THREE.Object3D()
  let ribIdx = 0
  for (let i = 0; i < 7; i++) {
    const z = 10 - i * 7.3
    for (const side of [-1, 1]) {
      dummy.position.set(side * 23.0, 0, z)
      dummy.rotation.set(0, 0, 0)
      dummy.scale.set(1, 1, 1)
      dummy.updateMatrix()
      ribMesh.setMatrixAt(ribIdx++, dummy.matrix)
    }
  }
  ribMesh.instanceMatrix.needsUpdate = true
  scene.add(ribMesh)
  disposables.push(ribGeo)

  // Grand Distant Horizon Architectural Portal Frame at z = -31
  const vistaArchGroup = new THREE.Group()
  vistaArchGroup.position.set(0, 0, -31)
  scene.add(vistaArchGroup)

  const vistaPierGeo = createChamferedPillarGeometry(2.1, 15.6, 2.8, 0.12)
  const vistaLeftPier = new THREE.Mesh(vistaPierGeo, architecturalConcreteMat)
  vistaLeftPier.position.set(-10.5, 0, 0)
  vistaArchGroup.add(vistaLeftPier)

  const vistaRightPier = new THREE.Mesh(vistaPierGeo, architecturalConcreteMat)
  vistaRightPier.position.set(10.5, 0, 0)
  vistaArchGroup.add(vistaRightPier)

  const vistaLintelGeo = createChamferedPillarGeometry(23.6, 1.8, 3.0, 0.12)
  const vistaLintel = new THREE.Mesh(vistaLintelGeo, architecturalConcreteMat)
  vistaLintel.position.set(0, 13.8, 0)
  vistaArchGroup.add(vistaLintel)
  disposables.push(vistaPierGeo, vistaLintelGeo)

  // Overhead matte-black ceiling trusses & 6 linear skylight diffuser strips
  const trussGeo = new THREE.BoxGeometry(46.0, 0.55, 0.65)
  const trussMesh = new THREE.InstancedMesh(trussGeo, matteSteelMat, 6)
  const skylightGeo = new THREE.PlaneGeometry(18.0, 0.72)
  const skylightMat = new THREE.MeshBasicMaterial({
    color: 0xe6f1ff,
    side: THREE.DoubleSide,
  })
  const skylightMesh = new THREE.InstancedMesh(skylightGeo, skylightMat, 6)

  for (let i = 0; i < 6; i++) {
    const z = 8 - i * 7.6
    dummy.position.set(0, 14.5, z)
    dummy.rotation.set(0, 0, 0)
    dummy.scale.set(1, 1, 1)
    dummy.updateMatrix()
    trussMesh.setMatrixAt(i, dummy.matrix)

    dummy.position.set(0, 14.16, z)
    dummy.rotation.set(Math.PI / 2, 0, 0)
    dummy.updateMatrix()
    skylightMesh.setMatrixAt(i, dummy.matrix)
  }
  trussMesh.instanceMatrix.needsUpdate = true
  skylightMesh.instanceMatrix.needsUpdate = true
  scene.add(trussMesh, skylightMesh)
  disposables.push(trussGeo, skylightGeo, skylightMat)

  // Subtle fresnel-faded vertical skylight atmospheric curtains (zero hard edges)
  const curtainUniforms = {
    uTheme: { value: 0 },
  }
  const curtainGeo = new THREE.PlaneGeometry(16.0, 10.5)
  const curtainMat = new THREE.ShaderMaterial({
    uniforms: curtainUniforms,
    transparent: true,
    depthWrite: false,
    side: THREE.DoubleSide,
    blending: THREE.AdditiveBlending,
    vertexShader: /* glsl */ `
      varying vec2 vUv;
      varying vec3 vNormalView;
      varying vec3 vViewDir;
      void main() {
        vUv = uv;
        vec4 mvPos = modelViewMatrix * vec4(position, 1.0);
        vNormalView = normalize(normalMatrix * normal);
        vViewDir = normalize(-mvPos.xyz);
        gl_Position = projectionMatrix * mvPos;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTheme;
      varying vec2 vUv;
      varying vec3 vNormalView;
      varying vec3 vViewDir;
      void main() {
        float facing = abs(dot(normalize(vNormalView), normalize(vViewDir)));
        float angleFade = pow(facing, 1.55);
        float edgeX = smoothstep(0.0, 0.25, vUv.x) * smoothstep(1.0, 0.75, vUv.x);
        float vert = pow(vUv.y, 1.8) * smoothstep(1.0, 0.88, vUv.y);
        float alpha = angleFade * edgeX * vert * mix(0.042, 0.008, uTheme);
        vec3 col = mix(vec3(0.68, 0.84, 1.0), vec3(0.95, 0.98, 1.0), vUv.y);
        gl_FragColor = vec4(col, alpha);
      }
    `,
  })
  const curtainZPositions = [4.0, -4.0, -12.0, -20.0]
  curtainZPositions.forEach((cz) => {
    const curtain = new THREE.Mesh(curtainGeo, curtainMat)
    curtain.position.set(0, 9.0, cz)
    scene.add(curtain)
  })
  disposables.push(curtainGeo, curtainMat)

  // =========================================================================
  // 4. Wet Honed Basalt Floor + Specular Bounce Pools & Brushed Steel Trims
  // =========================================================================
  const floorGeo = new THREE.PlaneGeometry(130, 110)
  const floorMat = new THREE.MeshPhysicalMaterial({
    color: 0x09101c,
    roughness: 0.28,
    metalness: 0.42,
    clearcoat: 0.85,
    clearcoatRoughness: 0.16,
    roughnessMap: concreteMaps.roughnessMap,
    normalMap: concreteMaps.normalMap,
    normalScale: new THREE.Vector2(0.22, 0.22),
    envMapIntensity: 1.65,
  })
  const floorMesh = new THREE.Mesh(floorGeo, floorMat)
  floorMesh.rotation.x = -Math.PI / 2
  floorMesh.position.set(0, 0, -10)
  scene.add(floorMesh)
  disposables.push(floorGeo, floorMat)

  // Inlaid brushed-steel architectural perspective aisle strips
  const aisleStripGeo = new THREE.BoxGeometry(0.045, 0.008, 48)
  for (const ax of [-4.6, 4.6]) {
    const strip = new THREE.Mesh(aisleStripGeo, inlayTrimMat)
    strip.position.set(ax, 0.004, -10)
    scene.add(strip)
  }
  disposables.push(aisleStripGeo)

  // Grounded elongated specular bounce light pools beneath the 3 major stages
  const bounceTex = createSoftGlowTexture()
  disposables.push(bounceTex)

  const makeBouncePool = (w: number, h: number, color: number, x: number, z: number, opacity: number) => {
    const geo = new THREE.PlaneGeometry(w, h)
    const mat = new THREE.MeshBasicMaterial({
      map: bounceTex,
      color,
      transparent: true,
      opacity,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
    })
    const mesh = new THREE.Mesh(geo, mat)
    mesh.rotation.x = -Math.PI / 2
    mesh.position.set(x, 0.012, z)
    scene.add(mesh)
    disposables.push(geo, mat)
    return { mat, baseOpacity: opacity }
  }

  const centerBounceL = makeBouncePool(8.5, 16.0, 0x2dd4bf, -2.0, 1.5, highTier ? 0.2 : 0.14)
  const centerBounceR = makeBouncePool(8.5, 16.0, 0xf59e0b, 2.0, 1.5, highTier ? 0.19 : 0.13)
  const aicodeBounce = makeBouncePool(11.0, 17.5, 0x2dd4bf, -14.5, -2.8, highTier ? 0.24 : 0.16)
  const qimuBounce = makeBouncePool(11.5, 18.0, 0xfbbf24, 14.5, -2.8, highTier ? 0.23 : 0.15)

  // Color LUTs for Dark <-> Light smooth transitions
  const darkFloorColor = new THREE.Color(0x09101c)
  const lightFloorColor = new THREE.Color(0xc9d5e8)
  const darkConcreteColor = new THREE.Color(0x0e1523)
  const lightConcreteColor = new THREE.Color(0xb6c3d8)
  const darkHemiSky = new THREE.Color(0xc8daf7)
  const lightHemiSky = new THREE.Color(0xffffff)
  const darkHemiGround = new THREE.Color(0x090f1d)
  const lightHemiGround = new THREE.Color(0x9fb2ce)

  let lastAppliedBucket = -1

  const applyTheme = (themeT: number) => {
    const t = Math.max(0, Math.min(1, themeT))
    skyUniforms.uTheme.value = t
    curtainUniforms.uTheme.value = t

    workingBg.copy(darkBg).lerp(lightBg, t)
    fog.color.copy(workingBg)
    fog.density = THREE.MathUtils.lerp(0.0145, 0.0095, t)

    floorMat.color.copy(darkFloorColor).lerp(lightFloorColor, t)
    floorMat.metalness = THREE.MathUtils.lerp(0.42, 0.16, t)
    floorMat.roughness = THREE.MathUtils.lerp(0.28, 0.34, t)

    architecturalConcreteMat.color.copy(darkConcreteColor).lerp(lightConcreteColor, t)
    architecturalConcreteMat.roughness = THREE.MathUtils.lerp(0.78, 0.86, t)

    centerBounceL.mat.opacity = centerBounceL.baseOpacity * (1 - t * 0.72)
    centerBounceR.mat.opacity = centerBounceR.baseOpacity * (1 - t * 0.72)
    aicodeBounce.mat.opacity = aicodeBounce.baseOpacity * (1 - t * 0.72)
    qimuBounce.mat.opacity = qimuBounce.baseOpacity * (1 - t * 0.72)

    fillHemi.color.copy(darkHemiSky).lerp(lightHemiSky, t)
    fillHemi.groundColor.copy(darkHemiGround).lerp(lightHemiGround, t)
    fillHemi.intensity = THREE.MathUtils.lerp(0.85, 1.45, t)

    overheadKey.intensity = THREE.MathUtils.lerp(2.2, 2.75, t)
    leftCyanRim.intensity = THREE.MathUtils.lerp(1.55, 1.05, t)
    rightWarmRim.intensity = THREE.MathUtils.lerp(1.48, 1.0, t)
    aicodePoint.intensity = THREE.MathUtils.lerp(12.0, 6.5, t)
    qimuPoint.intensity = THREE.MathUtils.lerp(12.5, 6.8, t)

    const bucket = t > 0.5 ? 1 : 0
    if (bucket !== lastAppliedBucket) {
      lastAppliedBucket = bucket
      scene.environment = bucket === 1 ? lightEnvRT.texture : darkEnvRT.texture
    }
  }

  applyTheme(0)

  return {
    applyTheme,
    dispose: () => {
      disposables.forEach((d) => d.dispose())
    },
  }
}
