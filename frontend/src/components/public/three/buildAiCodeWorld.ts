import * as THREE from 'three'
import {
  createArchLabelTexture,
  createBrushedMetalMaps,
  createContactShadowTexture,
  createLiveCodeScreen,
  createSoftGlowTexture,
} from './proceduralTextures'

export interface AiCodeWorldHandle {
  group: THREE.Group
  update: (
    elapsed: number,
    dt: number,
    focusWeight: number,
    themeT: number,
    active?: boolean,
  ) => void
  dispose: () => void
}

function createChamferedBoxGeometry(w: number, h: number, d: number, bevel: number): THREE.ExtrudeGeometry {
  const hw = w / 2 - bevel
  const hd = d / 2 - bevel
  const shape = new THREE.Shape()
  shape.moveTo(-hw, -hd)
  shape.lineTo(hw, -hd)
  shape.lineTo(hw, hd)
  shape.lineTo(-hw, hd)
  shape.closePath()

  const geo = new THREE.ExtrudeGeometry(shape, {
    depth: h - bevel * 2,
    bevelEnabled: true,
    bevelThickness: bevel,
    bevelSize: bevel,
    bevelSegments: 2,
  })
  geo.rotateX(-Math.PI / 2)
  geo.translate(0, bevel, 0)
  return geo
}

export function buildAiCodeWorld(highTier: boolean): AiCodeWorldHandle {
  const group = new THREE.Group()
  group.position.set(-14.5, 0, -6.0)
  group.rotation.y = 0.48

  const disposables: Array<{ dispose: () => void }> = []
  const metalMaps = createBrushedMetalMaps()
  const contactShadowTex = createContactShadowTexture()
  const softGlow = createSoftGlowTexture()
  const archHeaderTex = createArchLabelTexture('OPENAI  ·  CLAUDE  ·  GEMINI  ·  MCP', '#2dd4bf')
  const codeScreen = createLiveCodeScreen()
  disposables.push(metalMaps, contactShadowTex, softGlow, archHeaderTex, codeScreen)

  const obsidianSteelMat = new THREE.MeshStandardMaterial({
    color: 0x0b111c,
    metalness: 0.92,
    roughness: 0.24,
    roughnessMap: metalMaps.roughnessMap,
    normalMap: metalMaps.normalMap,
    normalScale: new THREE.Vector2(0.26, 0.26),
    envMapIntensity: 1.65,
  })
  const titaniumMat = new THREE.MeshStandardMaterial({
    color: 0x2a374b,
    metalness: 0.9,
    roughness: 0.28,
    roughnessMap: metalMaps.roughnessMap,
    normalMap: metalMaps.normalMap,
    normalScale: new THREE.Vector2(0.32, 0.32),
    envMapIntensity: 1.5,
  })
  const brushedFinMat = new THREE.MeshStandardMaterial({
    color: 0x475569,
    metalness: 0.92,
    roughness: 0.22,
    envMapIntensity: 1.65,
  })
  const smokedGlassDoorMat = new THREE.MeshPhysicalMaterial({
    color: 0x091628,
    metalness: 0.12,
    roughness: 0.06,
    clearcoat: 1.0,
    clearcoatRoughness: 0.04,
    envMapIntensity: 2.1,
    transparent: true,
    opacity: 0.24,
    depthWrite: false,
  })
  disposables.push(obsidianSteelMat, titaniumMat, brushedFinMat, smokedGlassDoorMat)

  // =========================================================================
  // 1. Raised Dark-Steel Compute Stage Deck (radius 6.6m, y = 0..0.18m)
  // =========================================================================
  const floorShadowGeo = new THREE.PlaneGeometry(17.0, 17.0)
  const shadowMat = new THREE.MeshBasicMaterial({
    map: contactShadowTex,
    transparent: true,
    opacity: 0.88,
    depthWrite: false,
  })
  const stageShadow = new THREE.Mesh(floorShadowGeo, shadowMat)
  stageShadow.rotation.x = -Math.PI / 2
  stageShadow.position.y = 0.008
  group.add(stageShadow)

  const stageDeckGeo = new THREE.LatheGeometry(
    [
      new THREE.Vector2(6.75, 0),
      new THREE.Vector2(6.6, 0.15),
      new THREE.Vector2(6.48, 0.18),
      new THREE.Vector2(0, 0.18),
    ],
    64,
  )
  const stageDeck = new THREE.Mesh(stageDeckGeo, obsidianSteelMat)
  group.add(stageDeck)

  const deckRimGeo = new THREE.TorusGeometry(6.54, 0.016, 10, 84)
  const deckRimMat = new THREE.MeshBasicMaterial({ color: 0x2dd4bf, transparent: true, opacity: 0.55 })
  const deckRim = new THREE.Mesh(deckRimGeo, deckRimMat)
  deckRim.rotation.x = Math.PI / 2
  deckRim.position.y = 0.165
  group.add(deckRim)
  disposables.push(floorShadowGeo, shadowMat, stageDeckGeo, deckRimGeo, deckRimMat)

  // =========================================================================
  // 2. 7 Monolithic Curved Supercomputer Rack Towers (height 5.8m)
  // =========================================================================
  const rackCenterPivot = new THREE.Group()
  rackCenterPivot.position.set(-0.35, 0.18, 2.8)
  group.add(rackCenterPivot)

  const rackCount = 7
  const rackArcRadius = 5.4
  const rackAngles = [-0.6, -0.4, -0.2, 0.0, 0.2, 0.4, 0.6]

  const cabinetGeo = createChamferedBoxGeometry(1.14, 5.8, 1.02, 0.055)
  const doorGeo = new THREE.PlaneGeometry(0.94, 5.35)
  const rackShadowGeo = new THREE.PlaneGeometry(2.1, 2.1)
  disposables.push(cabinetGeo, doorGeo, rackShadowGeo)

  // Instanced CNC cooling louver fins (7 racks * 22 fins = 154)
  const finsPerRack = 22
  const totalFins = rackCount * finsPerRack
  const finGeo = new THREE.BoxGeometry(0.92, 0.024, 0.06)
  const finInstances = new THREE.InstancedMesh(finGeo, brushedFinMat, totalFins)
  rackCenterPivot.add(finInstances)
  disposables.push(finGeo)

  // Instanced 280 micro-telemetry status LEDs (7 racks * 40 LEDs)
  const ledsPerRack = 40
  const totalLeds = rackCount * ledsPerRack
  const ledGeo = new THREE.BoxGeometry(0.036, 0.018, 0.022)
  const ledMat = new THREE.MeshBasicMaterial({ color: 0xffffff })
  const ledInstances = new THREE.InstancedMesh(ledGeo, ledMat, totalLeds)
  rackCenterPivot.add(ledInstances)
  disposables.push(ledGeo, ledMat)

  const rackMatrix = new THREE.Matrix4()
  const rackPos = new THREE.Vector3()
  const rackQuat = new THREE.Quaternion()
  const rackScale = new THREE.Vector3(1, 1, 1)
  const euler = new THREE.Euler()
  const ledMeta: Array<{ rackIdx: number; row: number; col: number }> = []

  let finIdx = 0
  let ledIdx = 0

  rackAngles.forEach((ang, rIdx) => {
    const rackSlot = new THREE.Group()
    const rx = Math.sin(ang) * rackArcRadius
    const rz = -Math.cos(ang) * rackArcRadius
    rackSlot.position.set(rx, 0, rz)
    rackSlot.rotation.y = -ang
    rackCenterPivot.add(rackSlot)

    const rShadow = new THREE.Mesh(rackShadowGeo, shadowMat)
    rShadow.rotation.x = -Math.PI / 2
    rShadow.position.y = 0.008
    rackSlot.add(rShadow)

    const cabinet = new THREE.Mesh(cabinetGeo, obsidianSteelMat)
    rackSlot.add(cabinet)

    const glassDoor = new THREE.Mesh(doorGeo, smokedGlassDoorMat)
    glassDoor.position.set(0, 2.9, 0.525)
    rackSlot.add(glassDoor)

    rackSlot.updateMatrixWorld(true)

    // Place 22 cooling fins in world space of rackCenterPivot
    for (let f = 0; f < finsPerRack; f++) {
      const fy = 0.55 + f * 0.22
      euler.set(0, -ang, 0)
      rackQuat.setFromEuler(euler)
      rackPos.set(rx + Math.sin(-ang) * 0.505, fy, rz + Math.cos(-ang) * 0.505)
      rackMatrix.compose(rackPos, rackQuat, rackScale)
      finInstances.setMatrixAt(finIdx++, rackMatrix)
    }

    // Place 40 micro telemetry LEDs in 2 columns along rack face
    for (let l = 0; l < ledsPerRack; l++) {
      const col = l % 2
      const row = Math.floor(l / 2)
      const localX = col === 0 ? -0.34 : 0.34
      const ly = 0.66 + row * 0.22
      const cosA = Math.cos(-ang)
      const sinA = Math.sin(-ang)
      const wx = rx + localX * cosA + 0.528 * sinA
      const wz = rz - localX * sinA + 0.528 * cosA
      euler.set(0, -ang, 0)
      rackQuat.setFromEuler(euler)
      rackPos.set(wx, ly, wz)
      rackMatrix.compose(rackPos, rackQuat, rackScale)
      ledInstances.setMatrixAt(ledIdx++, rackMatrix)
      ledMeta.push({ rackIdx: rIdx, row, col })
    }
  })
  finInstances.instanceMatrix.needsUpdate = true
  ledInstances.instanceMatrix.needsUpdate = true

  // Overhead structural cable gantry + backlit etched-glass cornice header
  const gantryGeo = new THREE.CylinderGeometry(
    rackArcRadius + 0.3,
    rackArcRadius + 0.3,
    0.42,
    40,
    1,
    true,
    Math.PI - 0.68,
    1.36,
  )
  const gantryMat = titaniumMat.clone()
  gantryMat.side = THREE.DoubleSide
  const gantry = new THREE.Mesh(gantryGeo, gantryMat)
  gantry.position.y = 6.02
  rackCenterPivot.add(gantry)

  const headerRadius = rackArcRadius - 0.42
  const headerGeo = new THREE.CylinderGeometry(
    headerRadius,
    headerRadius,
    0.58,
    40,
    1,
    true,
    Math.PI - 0.56,
    1.12,
  )
  headerGeo.scale(-1, 1, 1)
  const headerMat = new THREE.MeshBasicMaterial({
    map: archHeaderTex,
    side: THREE.FrontSide,
    transparent: true,
    opacity: 0.94,
  })
  const headerMesh = new THREE.Mesh(headerGeo, headerMat)
  headerMesh.position.y = 6.08
  rackCenterPivot.add(headerMesh)
  disposables.push(gantryGeo, gantryMat, headerGeo, headerMat)

  // =========================================================================
  // 3. Multi-Modal Neural Field Pedestal (grounded at local (0.2, 0.18, 0.4))
  // =========================================================================
  const fieldPedestal = new THREE.Group()
  fieldPedestal.position.set(0.2, 0.18, 0.4)
  group.add(fieldPedestal)

  const pedShadowGeo = new THREE.PlaneGeometry(2.8, 2.8)
  const pedShadow = new THREE.Mesh(pedShadowGeo, shadowMat)
  pedShadow.rotation.x = -Math.PI / 2
  pedShadow.position.y = 0.008
  fieldPedestal.add(pedShadow)

  const pedBaseGeo = new THREE.LatheGeometry(
    [
      new THREE.Vector2(1.02, 0),
      new THREE.Vector2(0.98, 0.16),
      new THREE.Vector2(0.74, 0.28),
      new THREE.Vector2(0.68, 0.96),
      new THREE.Vector2(0.84, 1.05),
      new THREE.Vector2(0, 1.05),
    ],
    48,
  )
  const pedBase = new THREE.Mesh(pedBaseGeo, titaniumMat)
  fieldPedestal.add(pedBase)

  // Twin sculpted brushed-steel caliper arcs cradling the neural cloud
  const caliperGeo = new THREE.TorusGeometry(1.18, 0.036, 14, 56, Math.PI * 1.18)
  const caliperLeft = new THREE.Mesh(caliperGeo, obsidianSteelMat)
  caliperLeft.position.y = 2.05
  caliperLeft.rotation.set(0.18, 0.35, -Math.PI * 0.59)
  fieldPedestal.add(caliperLeft)

  const caliperRight = new THREE.Mesh(caliperGeo, obsidianSteelMat)
  caliperRight.position.y = 2.05
  caliperRight.rotation.set(-0.18, -0.35, Math.PI * 0.41)
  fieldPedestal.add(caliperRight)
  disposables.push(pedShadowGeo, pedBaseGeo, caliperGeo)

  // 1500-particle GPU Morphing Multi-Modal Field (Text -> Image -> Video -> Audio)
  const count = highTier ? 1500 : 700
  const posA = new Float32Array(count * 3)
  const posB = new Float32Array(count * 3)
  const posC = new Float32Array(count * 3)
  const posD = new Float32Array(count * 3)
  const seeds = new Float32Array(count)

  for (let i = 0; i < count; i++) {
    const t = i / count
    const phi = Math.acos(1 - 2 * t)
    const theta = Math.PI * (1 + Math.sqrt(5)) * i
    seeds[i] = (i * 0.6180339887) % 1

    const rA = 0.72 + (seeds[i] - 0.5) * 0.12
    posA[i * 3] = rA * Math.sin(phi) * Math.cos(theta)
    posA[i * 3 + 1] = rA * Math.cos(phi)
    posA[i * 3 + 2] = rA * Math.sin(phi) * Math.sin(theta)

    const layer = i % 3
    const gridIdx = Math.floor(i / 3)
    const cols = Math.ceil(Math.sqrt(count / 3))
    posB[i * 3] = ((gridIdx % cols) / cols - 0.5) * 1.25
    posB[i * 3 + 1] = (Math.floor(gridIdx / cols) / cols - 0.5) * 1.25
    posB[i * 3 + 2] = (layer - 1) * 0.36

    const hAngle = t * Math.PI * 6 + (i % 2) * Math.PI
    posC[i * 3] = Math.cos(hAngle) * 0.58
    posC[i * 3 + 1] = (t - 0.5) * 1.65
    posC[i * 3 + 2] = Math.sin(hAngle) * 0.58

    const ringIdx = i % 5
    const rAngle = t * Math.PI * 16
    const rr = 0.18 + ringIdx * 0.14
    posD[i * 3] = Math.cos(rAngle) * rr
    posD[i * 3 + 1] = Math.sin(rr * 10) * 0.28 + (ringIdx - 2) * 0.16
    posD[i * 3 + 2] = Math.sin(rAngle) * rr
  }

  const morphGeo = new THREE.BufferGeometry()
  morphGeo.setAttribute('position', new THREE.BufferAttribute(posA, 3))
  morphGeo.setAttribute('aPosB', new THREE.BufferAttribute(posB, 3))
  morphGeo.setAttribute('aPosC', new THREE.BufferAttribute(posC, 3))
  morphGeo.setAttribute('aPosD', new THREE.BufferAttribute(posD, 3))
  morphGeo.setAttribute('aSeed', new THREE.BufferAttribute(seeds, 1))

  const morphUniforms = {
    uTime: { value: 0 },
    uTex: { value: softGlow },
  }
  const morphMat = new THREE.ShaderMaterial({
    uniforms: morphUniforms,
    vertexShader: /* glsl */ `
      attribute vec3 aPosB;
      attribute vec3 aPosC;
      attribute vec3 aPosD;
      attribute float aSeed;
      uniform float uTime;
      varying float vPhase;
      varying float vSeed;

      vec3 morphPos(float phase) {
        float seg = mod(phase, 4.0);
        if (seg < 1.0) return mix(position, aPosB, smoothstep(0.15, 0.85, seg));
        if (seg < 2.0) return mix(aPosB, aPosC, smoothstep(0.15, 0.85, seg - 1.0));
        if (seg < 3.0) return mix(aPosC, aPosD, smoothstep(0.15, 0.85, seg - 2.0));
        return mix(aPosD, position, smoothstep(0.15, 0.85, seg - 3.0));
      }

      void main() {
        float phase = uTime * 0.25;
        vPhase = phase;
        vSeed = aSeed;
        vec3 p = morphPos(phase);
        float c = cos(uTime * 0.35);
        float s = sin(uTime * 0.35);
        p.xz = mat2(c, -s, s, c) * p.xz;
        p.y += sin(uTime * 1.4 + aSeed * 6.283) * 0.035;
        vec4 mvPosition = modelViewMatrix * vec4(p, 1.0);
        gl_PointSize = clamp((52.0 / -mvPosition.z) * (0.8 + aSeed * 0.6), 2.5, 14.0);
        gl_Position = projectionMatrix * mvPosition;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform sampler2D uTex;
      varying float vPhase;
      varying float vSeed;
      void main() {
        vec4 sprite = texture2D(uTex, gl_PointCoord);
        if (sprite.a < 0.05) discard;
        vec3 cyan = vec3(0.16, 0.92, 0.84);
        vec3 blue = vec3(0.22, 0.7, 0.98);
        vec3 violet = vec3(0.54, 0.45, 0.98);
        vec3 amber = vec3(0.98, 0.72, 0.24);
        float m = mod(vPhase, 4.0) / 4.0;
        vec3 col = mix(mix(cyan, blue, vSeed), mix(violet, amber, vSeed), 0.5 + 0.5 * sin(m * 6.2831));
        gl_FragColor = vec4(col * 1.15, sprite.a * 0.86);
      }
    `,
    transparent: true,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
  })
  const morphCloud = new THREE.Points(morphGeo, morphMat)
  morphCloud.position.y = 2.08
  fieldPedestal.add(morphCloud)
  disposables.push(morphGeo, morphMat)

  // =========================================================================
  // 4. Grounded Curved Reference Developer Console (local (2.2, 0.18, 2.35))
  // =========================================================================
  const consoleGroup = new THREE.Group()
  consoleGroup.position.set(2.2, 0.18, 2.35)
  consoleGroup.rotation.y = -0.34
  group.add(consoleGroup)

  const consoleShadowGeo = new THREE.PlaneGeometry(3.2, 2.4)
  const consoleShadow = new THREE.Mesh(consoleShadowGeo, shadowMat)
  consoleShadow.rotation.x = -Math.PI / 2
  consoleShadow.position.y = 0.008
  consoleGroup.add(consoleShadow)

  const stanchionBaseGeo = createChamferedBoxGeometry(1.55, 0.14, 0.92, 0.03)
  const stanchionBase = new THREE.Mesh(stanchionBaseGeo, obsidianSteelMat)
  consoleGroup.add(stanchionBase)

  const stanchionNeckGeo = createChamferedBoxGeometry(0.46, 1.38, 0.34, 0.03)
  const stanchionNeck = new THREE.Mesh(stanchionNeckGeo, titaniumMat)
  stanchionNeck.position.set(0, 0.1, -0.1)
  stanchionNeck.rotation.x = -0.14
  consoleGroup.add(stanchionNeck)

  const screenRadius = 4.6
  const screenSpan = 0.72
  const screenGeo = new THREE.CylinderGeometry(
    screenRadius,
    screenRadius,
    1.95,
    36,
    1,
    true,
    Math.PI - screenSpan / 2,
    screenSpan,
  )
  screenGeo.scale(-1, 1, 1)
  const screenMat = new THREE.MeshBasicMaterial({
    map: codeScreen.texture,
    side: THREE.FrontSide,
  })
  const screenMesh = new THREE.Mesh(screenGeo, screenMat)
  screenMesh.position.set(0, 2.15, screenRadius)
  consoleGroup.add(screenMesh)

  const bezelGeo = new THREE.CylinderGeometry(
    screenRadius + 0.055,
    screenRadius + 0.055,
    2.08,
    36,
    1,
    true,
    Math.PI - (screenSpan + 0.03) / 2,
    screenSpan + 0.03,
  )
  const bezelMat = obsidianSteelMat.clone()
  bezelMat.side = THREE.DoubleSide
  const bezelMesh = new THREE.Mesh(bezelGeo, bezelMat)
  bezelMesh.position.set(0, 2.15, screenRadius)
  consoleGroup.add(bezelMesh)
  disposables.push(consoleShadowGeo, stanchionBaseGeo, stanchionNeckGeo, screenGeo, screenMat, bezelGeo, bezelMat)

  const colDim = new THREE.Color(0x0f766e)
  const colTeal = new THREE.Color(0x2dd4bf)
  const colSky = new THREE.Color(0x38bdf8)
  const colWork = new THREE.Color()

  const update = (
    elapsed: number,
    _dt: number,
    focusWeight: number,
    _themeT: number,
    active = true,
  ) => {
    morphUniforms.uTime.value = elapsed
    codeScreen.update(elapsed, active)

    caliperLeft.rotation.y = 0.35 + Math.sin(elapsed * 0.45) * 0.08
    caliperRight.rotation.y = -0.35 - Math.sin(elapsed * 0.45) * 0.08

    // Restrained emerald / cyan / ice-blue telemetry waves across rack bays
    const speed = 2.4 + focusWeight * 1.4
    for (let i = 0; i < totalLeds; i++) {
      const m = ledMeta[i]
      const pulse = Math.sin(elapsed * speed - m.row * 0.48 + m.rackIdx * 0.85 + m.col * 1.2)
      if (pulse > 0.55) {
        colWork.copy(m.col === 0 ? colTeal : colSky)
      } else if (pulse > -0.1) {
        colWork.copy(colDim).lerp(colTeal, (pulse + 0.1) * 0.9)
      } else {
        colWork.copy(colDim).multiplyScalar(0.32)
      }
      ledInstances.setColorAt(i, colWork)
    }
    if (ledInstances.instanceColor) ledInstances.instanceColor.needsUpdate = true
  }

  return {
    group,
    update,
    dispose: () => {
      disposables.forEach((d) => d.dispose())
    },
  }
}
