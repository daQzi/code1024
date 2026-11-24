import * as THREE from 'three'
import {
  createBrushedMetalMaps,
  createContactShadowTexture,
  createKnurledRingNormalMap,
} from './proceduralTextures'

export interface OpticalCoreHandle {
  group: THREE.Group
  update: (elapsed: number, dt: number, hoverBias: number, themeT: number) => void
  dispose: () => void
}

interface FloorPulse {
  curve: THREE.CatmullRomCurve3
  side: 'aicode' | 'qimu'
  offset: number
  speed: number
}

function createButtressGeometry(): THREE.ExtrudeGeometry {
  // Architectural tapered leg rising cleanly from plinth (y=0.45) to embrace stator side (y=3.15)
  const shape = new THREE.Shape()
  shape.moveTo(-0.55, 0)
  shape.lineTo(0.45, 0)
  shape.lineTo(0.28, 2.75)
  shape.lineTo(-0.22, 2.75)
  shape.closePath()

  const geo = new THREE.ExtrudeGeometry(shape, {
    depth: 0.56,
    bevelEnabled: true,
    bevelThickness: 0.045,
    bevelSize: 0.045,
    bevelSegments: 2,
  })
  geo.translate(0, 0, -0.28)
  return geo
}

function createIrisBladeGeometry(): THREE.ExtrudeGeometry {
  // Curved precision cinema diaphragm blade pivoting around origin (0,0) at outer mount
  const shape = new THREE.Shape()
  shape.moveTo(0, -0.18)
  shape.quadraticCurveTo(-1.35, -0.58, -2.55, -0.18)
  shape.quadraticCurveTo(-1.6, 0.36, -0.15, 0.24)
  shape.closePath()

  const geo = new THREE.ExtrudeGeometry(shape, {
    depth: 0.032,
    bevelEnabled: true,
    bevelThickness: 0.008,
    bevelSize: 0.008,
    bevelSegments: 1,
  })
  geo.translate(0, 0, -0.016)
  return geo
}

function createFloorChannelMeshGeometry(
  curve: THREE.CatmullRomCurve3,
  halfWidth: number,
  segments: number,
  yHeight: number,
): THREE.BufferGeometry {
  const positions: number[] = []
  const normals: number[] = []
  const uvs: number[] = []
  const indices: number[] = []

  const up = new THREE.Vector3(0, 1, 0)
  const pt = new THREE.Vector3()
  const tan = new THREE.Vector3()
  const side = new THREE.Vector3()

  for (let i = 0; i <= segments; i++) {
    const t = i / segments
    curve.getPointAt(t, pt)
    curve.getTangentAt(t, tan)
    side.crossVectors(up, tan).normalize()

    const lx = pt.x - side.x * halfWidth
    const lz = pt.z - side.z * halfWidth
    const rx = pt.x + side.x * halfWidth
    const rz = pt.z + side.z * halfWidth

    positions.push(lx, yHeight, lz, rx, yHeight, rz)
    normals.push(0, 1, 0, 0, 1, 0)
    uvs.push(0, t, 1, t)

    if (i < segments) {
      const a = i * 2
      const b = a + 1
      const c = a + 2
      const d = a + 3
      indices.push(a, c, b, b, c, d)
    }
  }

  const geo = new THREE.BufferGeometry()
  geo.setAttribute('position', new THREE.Float32BufferAttribute(positions, 3))
  geo.setAttribute('normal', new THREE.Float32BufferAttribute(normals, 3))
  geo.setAttribute('uv', new THREE.Float32BufferAttribute(uvs, 2))
  geo.setIndex(indices)
  return geo
}

export function buildOpticalCore(highTier: boolean): OpticalCoreHandle {
  const group = new THREE.Group()
  const portalCenterZ = -3.5
  const disposables: Array<{ dispose: () => void }> = []

  const metalMaps = createBrushedMetalMaps()
  const knurledNormal = createKnurledRingNormalMap()
  const contactShadowTex = createContactShadowTexture()
  disposables.push(metalMaps, knurledNormal, contactShadowTex)

  const obsidianChromeMat = new THREE.MeshStandardMaterial({
    color: 0x0b101b,
    metalness: 0.94,
    roughness: 0.19,
    roughnessMap: metalMaps.roughnessMap,
    normalMap: metalMaps.normalMap,
    normalScale: new THREE.Vector2(0.22, 0.22),
    envMapIntensity: 1.75,
  })
  const titaniumMat = new THREE.MeshStandardMaterial({
    color: 0x2a374c,
    metalness: 0.9,
    roughness: 0.28,
    roughnessMap: metalMaps.roughnessMap,
    normalMap: metalMaps.normalMap,
    normalScale: new THREE.Vector2(0.32, 0.32),
    envMapIntensity: 1.55,
  })
  const champagneKnurlMat = new THREE.MeshStandardMaterial({
    color: 0xd49a35,
    metalness: 0.94,
    roughness: 0.24,
    normalMap: knurledNormal,
    normalScale: new THREE.Vector2(0.75, 0.75),
    envMapIntensity: 1.7,
  })
  const bladeMetalMat = new THREE.MeshStandardMaterial({
    color: 0x182234,
    metalness: 0.88,
    roughness: 0.32,
    roughnessMap: metalMaps.roughnessMap,
    envMapIntensity: 1.5,
  })
  disposables.push(obsidianChromeMat, titaniumMat, champagneKnurlMat, bladeMetalMat)

  // =========================================================================
  // 1. Tiered Obsidian & Brushed-Titanium Reflecting Plinth (grounded at y=0)
  // =========================================================================
  const plinthGroup = new THREE.Group()
  plinthGroup.position.set(0, 0, portalCenterZ)
  group.add(plinthGroup)

  const shadowGeo = new THREE.PlaneGeometry(14.5, 14.5)
  const shadowMat = new THREE.MeshBasicMaterial({
    map: contactShadowTex,
    transparent: true,
    opacity: 0.9,
    depthWrite: false,
  })
  const shadowDecal = new THREE.Mesh(shadowGeo, shadowMat)
  shadowDecal.rotation.x = -Math.PI / 2
  shadowDecal.position.y = 0.008
  plinthGroup.add(shadowDecal)

  const lowerPlinthGeo = new THREE.LatheGeometry(
    [
      new THREE.Vector2(5.15, 0),
      new THREE.Vector2(5.15, 0.08),
      new THREE.Vector2(4.92, 0.28),
      new THREE.Vector2(3.85, 0.28),
    ],
    64,
  )
  const lowerPlinth = new THREE.Mesh(lowerPlinthGeo, obsidianChromeMat)
  plinthGroup.add(lowerPlinth)

  const upperPlinthGeo = new THREE.LatheGeometry(
    [
      new THREE.Vector2(4.42, 0.28),
      new THREE.Vector2(4.36, 0.44),
      new THREE.Vector2(4.12, 0.52),
      new THREE.Vector2(3.75, 0.52),
      new THREE.Vector2(3.75, 0.47),
      new THREE.Vector2(0, 0.47),
    ],
    64,
  )
  const upperPlinth = new THREE.Mesh(upperPlinthGeo, titaniumMat)
  plinthGroup.add(upperPlinth)

  // Dark mirror optical pool recessed inside the upper plinth
  const poolGeo = new THREE.CircleGeometry(3.74, 64)
  const poolMat = new THREE.MeshPhysicalMaterial({
    color: 0x050912,
    metalness: 0.15,
    roughness: 0.05,
    clearcoat: 1.0,
    clearcoatRoughness: 0.03,
    envMapIntensity: 2.1,
  })
  const poolMesh = new THREE.Mesh(poolGeo, poolMat)
  poolMesh.rotation.x = -Math.PI / 2
  poolMesh.position.y = 0.505
  plinthGroup.add(poolMesh)

  // Subtle dual-tone inlaid architectural plinth trim rings
  const leftInlayGeo = new THREE.TorusGeometry(4.4, 0.014, 8, 48, Math.PI * 0.9)
  const leftInlayMat = new THREE.MeshBasicMaterial({ color: 0x2dd4bf, transparent: true, opacity: 0.72 })
  const leftInlay = new THREE.Mesh(leftInlayGeo, leftInlayMat)
  leftInlay.rotation.set(Math.PI / 2, 0, Math.PI * 0.55)
  leftInlay.position.y = 0.295
  plinthGroup.add(leftInlay)

  const rightInlayGeo = new THREE.TorusGeometry(4.4, 0.014, 8, 48, Math.PI * 0.9)
  const rightInlayMat = new THREE.MeshBasicMaterial({ color: 0xfbbf24, transparent: true, opacity: 0.72 })
  const rightInlay = new THREE.Mesh(rightInlayGeo, rightInlayMat)
  rightInlay.rotation.set(Math.PI / 2, 0, -Math.PI * 0.45)
  rightInlay.position.y = 0.295
  plinthGroup.add(rightInlay)

  disposables.push(
    shadowGeo,
    shadowMat,
    lowerPlinthGeo,
    upperPlinthGeo,
    poolGeo,
    poolMat,
    leftInlayGeo,
    leftInlayMat,
    rightInlayGeo,
    rightInlayMat,
  )

  // Sculpted chamfered titanium buttresses anchoring stator housing onto plinth
  const buttressGeo = createButtressGeometry()
  const leftButtress = new THREE.Mesh(buttressGeo, titaniumMat)
  leftButtress.position.set(-3.22, 0.45, 0)
  plinthGroup.add(leftButtress)

  const rightButtress = new THREE.Mesh(buttressGeo, titaniumMat)
  rightButtress.position.set(3.22, 0.45, 0)
  rightButtress.scale.x = -1
  plinthGroup.add(rightButtress)
  disposables.push(buttressGeo)

  // =========================================================================
  // 2. Colossal Precision Lathed Stator Ring (7.3m diameter, centered y=4.15)
  // =========================================================================
  const aperturePivot = new THREE.Group()
  aperturePivot.position.set(0, 4.15, portalCenterZ)
  group.add(aperturePivot)

  const statorProfile = [
    new THREE.Vector2(2.86, -0.44),
    new THREE.Vector2(3.38, -0.44),
    new THREE.Vector2(3.68, -0.22),
    new THREE.Vector2(3.68, 0.22),
    new THREE.Vector2(3.38, 0.44),
    new THREE.Vector2(2.86, 0.44),
    new THREE.Vector2(2.86, -0.44),
  ]
  const statorGeo = new THREE.LatheGeometry(statorProfile, highTier ? 72 : 52)
  statorGeo.rotateX(Math.PI / 2)
  const statorMesh = new THREE.Mesh(statorGeo, obsidianChromeMat)
  aperturePivot.add(statorMesh)

  // Stepped inner brushed-titanium flange
  const innerFlangeGeo = new THREE.LatheGeometry(
    [
      new THREE.Vector2(2.8, -0.34),
      new THREE.Vector2(3.04, -0.34),
      new THREE.Vector2(3.04, 0.34),
      new THREE.Vector2(2.8, 0.34),
    ],
    highTier ? 64 : 48,
  )
  innerFlangeGeo.rotateX(Math.PI / 2)
  const innerFlange = new THREE.Mesh(innerFlangeGeo, titaniumMat)
  aperturePivot.add(innerFlange)

  // Knurled champagne-brass precision bezel ring on front & back
  const brassBezelGeo = new THREE.TorusGeometry(3.26, 0.055, 16, highTier ? 84 : 56)
  const frontBezel = new THREE.Mesh(brassBezelGeo, champagneKnurlMat)
  frontBezel.position.z = 0.43
  aperturePivot.add(frontBezel)

  const rearBezel = new THREE.Mesh(brassBezelGeo, champagneKnurlMat)
  rearBezel.position.z = -0.43
  aperturePivot.add(rearBezel)

  // 24 machined calibration lugs around stator perimeter (InstancedMesh)
  const lugCount = 24
  const lugGeo = new THREE.CylinderGeometry(0.055, 0.065, 0.14, 12)
  const lugInstances = new THREE.InstancedMesh(lugGeo, champagneKnurlMat, lugCount)
  const lugDummy = new THREE.Object3D()
  for (let i = 0; i < lugCount; i++) {
    const a = (i / lugCount) * Math.PI * 2
    lugDummy.position.set(Math.cos(a) * 3.55, Math.sin(a) * 3.55, 0.3)
    lugDummy.rotation.set(Math.PI / 2, 0, a)
    lugDummy.updateMatrix()
    lugInstances.setMatrixAt(i, lugDummy.matrix)
  }
  lugInstances.instanceMatrix.needsUpdate = true
  aperturePivot.add(lugInstances)
  disposables.push(statorGeo, innerFlangeGeo, brassBezelGeo, lugGeo)

  // =========================================================================
  // 3. 24 Articulated Mechanical Cinema Iris Blades (InstancedMesh)
  // =========================================================================
  const bladeCount = 24
  const bladeGeo = createIrisBladeGeometry()
  const bladeInstances = new THREE.InstancedMesh(bladeGeo, bladeMetalMat, bladeCount)
  const bladeDummy = new THREE.Object3D()
  aperturePivot.add(bladeInstances)
  disposables.push(bladeGeo)

  const updateIrisBlades = (openAmount: number) => {
    const mountR = 2.94
    const swing = THREE.MathUtils.lerp(0.68, 0.18, openAmount)
    for (let i = 0; i < bladeCount; i++) {
      const a = (i / bladeCount) * Math.PI * 2
      bladeDummy.position.set(Math.cos(a) * mountR, Math.sin(a) * mountR, (i % 4 - 1.5) * 0.018)
      bladeDummy.rotation.set(0, 0, a + swing)
      bladeDummy.scale.set(1, 1, 1)
      bladeDummy.updateMatrix()
      bladeInstances.setMatrixAt(i, bladeDummy.matrix)
    }
    bladeInstances.instanceMatrix.needsUpdate = true
  }
  updateIrisBlades(0.65)

  // =========================================================================
  // 4. Internal Chromatic Lens Field (Custom GLSL Meniscus, ZERO transmission)
  // =========================================================================
  const lensUniforms = {
    uTime: { value: 0 },
    uBias: { value: 0 },
    uTheme: { value: 0 },
  }
  const lensGeo = new THREE.CircleGeometry(2.86, highTier ? 72 : 48)
  const lensMat = new THREE.ShaderMaterial({
    uniforms: lensUniforms,
    transparent: true,
    depthWrite: false,
    side: THREE.DoubleSide,
    vertexShader: /* glsl */ `
      varying vec2 vUv;
      varying vec3 vViewPos;
      void main() {
        vUv = uv;
        vec4 mvPos = modelViewMatrix * vec4(position, 1.0);
        vViewPos = -mvPos.xyz;
        gl_Position = projectionMatrix * mvPos;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTime;
      uniform float uBias;
      uniform float uTheme;
      varying vec2 vUv;
      varying vec3 vViewPos;

      void main() {
        vec2 p = vUv * 2.0 - 1.0;
        float r = length(p);
        if (r > 1.0) discard;

        float angle = (r > 0.0005) ? atan(p.y, p.x) : 0.0;
        // Simulated biconvex cinema glass surface normal for studio fresnel reflection
        vec3 n = normalize(vec3(p.x * 0.42, p.y * 0.42, sqrt(max(0.05, 1.0 - r * r * 0.35))));
        vec3 v = normalize(vViewPos);
        float ndv = clamp(dot(n, v), 0.0, 1.0);
        float fresnel = pow(1.0 - ndv, 2.8);

        // Multi-coated optical anti-reflective magenta/cyan sheen + studio overhead strip
        vec3 coating = mix(vec3(0.06, 0.28, 0.36), vec3(0.32, 0.16, 0.48), clamp(p.y * 0.5 + 0.5, 0.0, 1.0));
        float softboxSpec = exp(-pow(p.y - 0.55, 2.0) * 18.0) * smoothstep(0.95, 0.25, abs(p.x));

        // Chromatic caustic equilibrium: AICode cyan-sapphire <-> Qimu tungsten-gold-violet
        float waveA = sin(r * 11.0 - uTime * 1.45 + sin(angle * 3.0 + uTime * 0.6)) * 0.5 + 0.5;
        float waveB = cos(r * 7.5 + uTime * 1.1 - angle * 2.0) * 0.5 + 0.5;
        float wingMix = clamp(0.5 + uBias * 0.46 + p.x * 0.42, 0.0, 1.0);

        vec3 cyanWing = mix(vec3(0.08, 0.72, 0.76), vec3(0.22, 0.56, 0.98), waveA);
        vec3 warmWing = mix(vec3(0.98, 0.66, 0.18), vec3(0.64, 0.38, 0.94), waveB);
        vec3 portalCol = mix(cyanWing, warmWing, wingMix);

        float coreDepth = smoothstep(0.96, 0.12, r);
        float causticRing = smoothstep(0.22, 0.55, r) * smoothstep(0.96, 0.6, r);
        vec3 deepGlass = mix(vec3(0.015, 0.032, 0.065), vec3(0.75, 0.84, 0.96), uTheme * 0.55);

        vec3 col = deepGlass;
        col += portalCol * (0.28 * coreDepth + 0.52 * causticRing * waveA) * mix(1.0, 0.75, uTheme);
        col += coating * fresnel * 1.35;
        col += vec3(0.88, 0.94, 1.0) * softboxSpec * (0.38 + fresnel * 0.45);
        col += portalCol * pow(1.0 - smoothstep(0.0, 0.26, r), 2.0) * 0.42;

        float alpha = clamp(0.42 + coreDepth * 0.34 + fresnel * 0.48 + softboxSpec * 0.25, 0.0, 0.94);
        gl_FragColor = vec4(col, alpha);
      }
    `,
  })
  const opticalLensDisc = new THREE.Mesh(lensGeo, lensMat)
  opticalLensDisc.position.z = -0.06
  aperturePivot.add(opticalLensDisc)
  disposables.push(lensGeo, lensMat)

  // =========================================================================
  // 5. Inlaid Architectural Floor Optical Channels & 36 Light Pulses
  // =========================================================================
  const leftCurves = [
    new THREE.CatmullRomCurve3([
      new THREE.Vector3(-4.2, 0.025, -3.7),
      new THREE.Vector3(-7.4, 0.025, -4.15),
      new THREE.Vector3(-10.8, 0.025, -5.05),
      new THREE.Vector3(-14.5, 0.025, -6.0),
    ]),
    new THREE.CatmullRomCurve3([
      new THREE.Vector3(-4.0, 0.025, -3.15),
      new THREE.Vector3(-7.0, 0.025, -2.55),
      new THREE.Vector3(-10.8, 0.025, -3.65),
      new THREE.Vector3(-14.1, 0.025, -5.2),
    ]),
  ]

  const rightCurves = [
    new THREE.CatmullRomCurve3([
      new THREE.Vector3(4.2, 0.025, -3.7),
      new THREE.Vector3(7.4, 0.025, -4.15),
      new THREE.Vector3(10.8, 0.025, -5.05),
      new THREE.Vector3(14.5, 0.025, -6.0),
    ]),
    new THREE.CatmullRomCurve3([
      new THREE.Vector3(4.0, 0.025, -3.15),
      new THREE.Vector3(7.0, 0.025, -2.55),
      new THREE.Vector3(10.8, 0.025, -3.65),
      new THREE.Vector3(14.1, 0.025, -5.2),
    ]),
  ]

  const channelGlassMat = new THREE.MeshPhysicalMaterial({
    color: 0x0b1628,
    metalness: 0.45,
    roughness: 0.14,
    clearcoat: 1.0,
    clearcoatRoughness: 0.08,
    envMapIntensity: 1.8,
  })
  const channelTrimMat = new THREE.MeshStandardMaterial({
    color: 0x475569,
    metalness: 0.9,
    roughness: 0.25,
  })
  disposables.push(channelGlassMat, channelTrimMat)

  const allCurves = [...leftCurves, ...rightCurves]
  allCurves.forEach((curve) => {
    const outerTrimGeo = createFloorChannelMeshGeometry(curve, 0.16, 48, 0.015)
    const innerGlassGeo = createFloorChannelMeshGeometry(curve, 0.11, 48, 0.02)
    group.add(new THREE.Mesh(outerTrimGeo, channelTrimMat))
    group.add(new THREE.Mesh(innerGlassGeo, channelGlassMat))
    disposables.push(outerTrimGeo, innerGlassGeo)
  })

  // 36 instanced optical light pulses racing along the inlaid floor channels
  const pulses: FloorPulse[] = []
  leftCurves.forEach((curve, idx) => {
    for (let j = 0; j < 9; j++) {
      pulses.push({ curve, side: 'aicode', offset: (j / 9 + idx * 0.07) % 1, speed: 0.24 + idx * 0.05 })
    }
  })
  rightCurves.forEach((curve, idx) => {
    for (let j = 0; j < 9; j++) {
      pulses.push({ curve, side: 'qimu', offset: (j / 9 + idx * 0.09) % 1, speed: 0.23 + idx * 0.05 })
    }
  })

  const pulseGeo = new THREE.BoxGeometry(0.08, 0.018, 0.56)
  const pulseMat = new THREE.MeshBasicMaterial({ color: 0xffffff })
  const pulseInstances = new THREE.InstancedMesh(pulseGeo, pulseMat, pulses.length)
  const cyanPulseCol = new THREE.Color(0x2dd4bf)
  const amberPulseCol = new THREE.Color(0xfbbf24)
  pulses.forEach((p, idx) => {
    pulseInstances.setColorAt(idx, p.side === 'aicode' ? cyanPulseCol : amberPulseCol)
  })
  if (pulseInstances.instanceColor) pulseInstances.instanceColor.needsUpdate = true
  group.add(pulseInstances)
  disposables.push(pulseGeo, pulseMat)

  const pDummy = new THREE.Object3D()
  const posVec = new THREE.Vector3()
  const tanVec = new THREE.Vector3()
  const lookVec = new THREE.Vector3()
  const pulseProgress = pulses.map((p) => p.offset)

  const update = (elapsed: number, dt: number, hoverBias: number, themeT: number) => {
    lensUniforms.uTime.value = elapsed
    lensUniforms.uBias.value = hoverBias
    lensUniforms.uTheme.value = themeT

    // Articulated iris diaphragm breathes gently and opens wider when targeting a wing
    const openAmount = Math.min(1, 0.56 + Math.sin(elapsed * 0.72) * 0.09 + Math.abs(hoverBias) * 0.28)
    updateIrisBlades(openAmount)

    // Subtle rotation on brass knurled front bezel ring for tactile mechanical craft
    frontBezel.rotation.z = elapsed * 0.08 + hoverBias * 0.25

    // Stream floor pulses toward/from hovered platform
    for (let i = 0; i < pulses.length; i++) {
      const p = pulses[i]
      const boost =
        p.side === 'aicode'
          ? 1 + Math.max(0, -hoverBias) * 1.4
          : 1 + Math.max(0, hoverBias) * 1.4
      pulseProgress[i] = (pulseProgress[i] + dt * p.speed * boost) % 1
      const t = pulseProgress[i]
      p.curve.getPointAt(t, posVec)
      p.curve.getTangentAt(t, tanVec)
      posVec.y = 0.032
      lookVec.copy(posVec).add(tanVec)
      pDummy.position.copy(posVec)
      pDummy.lookAt(lookVec)
      const fadeScale = 0.65 + Math.sin(t * Math.PI) * 0.65
      pDummy.scale.set(1, 1, fadeScale)
      pDummy.updateMatrix()
      pulseInstances.setMatrixAt(i, pDummy.matrix)
    }
    pulseInstances.instanceMatrix.needsUpdate = true
  }

  return {
    group,
    update,
    dispose: () => {
      disposables.forEach((d) => d.dispose())
    },
  }
}
