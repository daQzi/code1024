import * as THREE from 'three'
import {
  createAnamorphicFlareTexture,
  createArchLabelTexture,
  createBrushedMetalMaps,
  createContactShadowTexture,
  createKnurledRingNormalMap,
  createLiveTimelineScreen,
  createSoftGlowTexture,
} from './proceduralTextures'

export interface QimuStudioWorldHandle {
  group: THREE.Group
  update: (
    elapsed: number,
    dt: number,
    pointerX: number,
    pointerY: number,
    focusWeight: number,
    themeT: number,
    active?: boolean,
  ) => void
  dispose: () => void
}

function loadStudioTexture(
  loader: THREE.TextureLoader,
  url: string,
  anisotropy: number,
): THREE.Texture {
  const tex = loader.load(url)
  tex.colorSpace = THREE.SRGBColorSpace
  tex.anisotropy = anisotropy
  return tex
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

function createHollowFrameGeometry(
  outerW: number,
  outerH: number,
  border: number,
  depth: number,
  bevel: number,
): THREE.ExtrudeGeometry {
  const ow = outerW / 2
  const oh = outerH / 2
  const shape = new THREE.Shape()
  shape.moveTo(-ow, -oh)
  shape.lineTo(ow, -oh)
  shape.lineTo(ow, oh)
  shape.lineTo(-ow, oh)
  shape.closePath()

  const iw = ow - border
  const ih = oh - border
  const hole = new THREE.Path()
  hole.moveTo(-iw, -ih)
  hole.lineTo(iw, -ih)
  hole.lineTo(iw, ih)
  hole.lineTo(-iw, ih)
  hole.closePath()
  shape.holes.push(hole)

  const geo = new THREE.ExtrudeGeometry(shape, {
    depth,
    bevelEnabled: true,
    bevelThickness: bevel,
    bevelSize: bevel,
    bevelSegments: 2,
  })
  geo.translate(0, 0, -depth / 2)
  return geo
}

export function buildQimuStudioWorld(
  renderer: THREE.WebGLRenderer,
  highTier: boolean,
): QimuStudioWorldHandle {
  const group = new THREE.Group()
  group.position.set(14.5, 0, -6.0)
  group.rotation.y = -0.48

  const disposables: Array<{ dispose: () => void }> = []
  const metalMaps = createBrushedMetalMaps()
  const knurledNormal = createKnurledRingNormalMap()
  const contactShadowTex = createContactShadowTexture()
  const softGlow = createSoftGlowTexture()
  const flareTex = createAnamorphicFlareTexture()
  const manjuLabelTex = createArchLabelTexture('漫剧  ·  3D STORYBOARD', '#a78bfa')
  const timelineScreen = createLiveTimelineScreen()
  disposables.push(
    metalMaps,
    knurledNormal,
    contactShadowTex,
    softGlow,
    flareTex,
    manjuLabelTex,
    timelineScreen,
  )

  const maxAniso = Math.min(8, renderer.capabilities.getMaxAnisotropy())
  const texLoader = new THREE.TextureLoader()
  const cinemaStillTex = loadStudioTexture(texLoader, '/landing-3d/cinema-still.jpg', maxAniso)
  const manjuBgTex = loadStudioTexture(texLoader, '/landing-3d/manju-bg.jpg', maxAniso)
  const manjuHeroTex = loadStudioTexture(texLoader, '/landing-3d/manju-hero.jpg', maxAniso)
  const posterTextures = [
    loadStudioTexture(texLoader, '/landing-3d/poster-1.jpg', maxAniso),
    loadStudioTexture(texLoader, '/landing-3d/poster-2.jpg', maxAniso),
    loadStudioTexture(texLoader, '/landing-3d/poster-3.jpg', maxAniso),
  ]
  disposables.push(cinemaStillTex, manjuBgTex, manjuHeroTex, ...posterTextures)

  const anodizedBlack = new THREE.MeshStandardMaterial({
    color: 0x0b0f18,
    metalness: 0.9,
    roughness: 0.28,
    roughnessMap: metalMaps.roughnessMap,
    normalMap: metalMaps.normalMap,
    normalScale: new THREE.Vector2(0.24, 0.24),
    envMapIntensity: 1.6,
  })
  const carbonMatteMat = new THREE.MeshStandardMaterial({
    color: 0x161d2b,
    metalness: 0.35,
    roughness: 0.52,
    envMapIntensity: 1.1,
  })
  const titaniumMat = new THREE.MeshStandardMaterial({
    color: 0x334155,
    metalness: 0.92,
    roughness: 0.24,
    roughnessMap: metalMaps.roughnessMap,
    envMapIntensity: 1.55,
  })
  const champagneGold = new THREE.MeshStandardMaterial({
    color: 0xd9a13b,
    metalness: 0.94,
    roughness: 0.22,
    roughnessMap: metalMaps.roughnessMap,
    envMapIntensity: 1.75,
  })
  const knurledBrass = new THREE.MeshStandardMaterial({
    color: 0xb9852b,
    metalness: 0.92,
    roughness: 0.3,
    normalMap: knurledNormal,
    normalScale: new THREE.Vector2(0.85, 0.85),
    envMapIntensity: 1.55,
  })
  const coatedLensGlass = new THREE.MeshPhysicalMaterial({
    color: 0x0f2544,
    metalness: 0.12,
    roughness: 0.03,
    iridescence: 1.0,
    iridescenceIOR: 1.38,
    clearcoat: 1.0,
    clearcoatRoughness: 0.03,
    envMapIntensity: 2.3,
    transparent: true,
    opacity: 0.82,
  })
  const shadowMat = new THREE.MeshBasicMaterial({
    map: contactShadowTex,
    transparent: true,
    opacity: 0.88,
    depthWrite: false,
  })
  disposables.push(
    anodizedBlack,
    carbonMatteMat,
    titaniumMat,
    champagneGold,
    knurledBrass,
    coatedLensGlass,
    shadowMat,
  )

  // =========================================================================
  // 1. Raised Soundstage Deck (radius 7.2m, y = 0..0.18m)
  // =========================================================================
  const stageShadowGeo = new THREE.PlaneGeometry(18.5, 18.5)
  const stageShadow = new THREE.Mesh(stageShadowGeo, shadowMat)
  stageShadow.rotation.x = -Math.PI / 2
  stageShadow.position.y = 0.008
  group.add(stageShadow)

  const stageDeckGeo = new THREE.LatheGeometry(
    [
      new THREE.Vector2(7.35, 0),
      new THREE.Vector2(7.2, 0.15),
      new THREE.Vector2(7.05, 0.18),
      new THREE.Vector2(0, 0.18),
    ],
    highTier ? 64 : 44,
  )
  const stageDeck = new THREE.Mesh(stageDeckGeo, anodizedBlack)
  group.add(stageDeck)

  const deckRimGeo = new THREE.TorusGeometry(7.12, 0.016, 10, 84)
  const deckRimMat = new THREE.MeshBasicMaterial({ color: 0xfbbf24, transparent: true, opacity: 0.54 })
  const deckRim = new THREE.Mesh(deckRimGeo, deckRimMat)
  deckRim.rotation.x = Math.PI / 2
  deckRim.position.y = 0.165
  group.add(deckRim)
  disposables.push(stageShadowGeo, stageDeckGeo, deckRimGeo, deckRimMat)

  // =========================================================================
  // 2. Colossal Curved IMAX LED Volume Wall + Stage Truss + NLE Console
  // =========================================================================
  const cinemaRoot = new THREE.Group()
  cinemaRoot.position.set(-0.1, 0.18, -2.5)
  group.add(cinemaRoot)

  const cineRadius = 11.2
  const cineSpan = 0.68 // 11.2 * 0.68 ≈ 7.6m wide!
  const cineScreenGeo = new THREE.CylinderGeometry(
    cineRadius,
    cineRadius,
    3.45,
    44,
    1,
    true,
    Math.PI - cineSpan / 2,
    cineSpan,
  )
  cineScreenGeo.scale(-1, 1, 1)
  const cineScreenMat = new THREE.MeshBasicMaterial({
    map: cinemaStillTex,
    side: THREE.FrontSide,
  })
  const cineScreen = new THREE.Mesh(cineScreenGeo, cineScreenMat)
  cineScreen.position.set(0, 2.85, cineRadius)
  cinemaRoot.add(cineScreen)

  const cineBezelGeo = new THREE.CylinderGeometry(
    cineRadius + 0.08,
    cineRadius + 0.08,
    3.66,
    44,
    1,
    true,
    Math.PI - (cineSpan + 0.03) / 2,
    cineSpan + 0.03,
  )
  const cineBezelMat = anodizedBlack.clone()
  cineBezelMat.side = THREE.DoubleSide
  const cineBezel = new THREE.Mesh(cineBezelGeo, cineBezelMat)
  cineBezel.position.set(0, 2.85, cineRadius)
  cinemaRoot.add(cineBezel)
  disposables.push(cineScreenGeo, cineScreenMat, cineBezelGeo, cineBezelMat)

  // Industrial aluminum box-truss towers + overhead lighting rig (InstancedMesh)
  const trussBarGeo = new THREE.BoxGeometry(0.11, 5.4, 0.11)
  const trussTransforms: Array<{ pos: [number, number, number]; rot: [number, number, number]; scale: [number, number, number] }> = []
  // Left & Right twin truss columns (4 vertical chords per tower = 8 uprights)
  for (const sx of [-4.15, 4.15]) {
    for (const dx of [-0.18, 0.18]) {
      for (const dz of [-0.18, 0.18]) {
        trussTransforms.push({ pos: [sx + dx, 2.7, -0.35 + dz], rot: [0, 0, 0], scale: [1, 1, 1] })
      }
    }
  }
  // Overhead horizontal box-truss spanning across top (4 chords)
  for (const dy of [-0.18, 0.18]) {
    for (const dz of [-0.18, 0.18]) {
      trussTransforms.push({ pos: [0, 5.3 + dy, -0.25 + dz], rot: [0, 0, Math.PI / 2], scale: [1, 1.66, 1] })
    }
  }
  const trussInstances = new THREE.InstancedMesh(trussBarGeo, titaniumMat, trussTransforms.length)
  const tDummy = new THREE.Object3D()
  trussTransforms.forEach((tr, idx) => {
    tDummy.position.set(...tr.pos)
    tDummy.rotation.set(...tr.rot)
    tDummy.scale.set(...tr.scale)
    tDummy.updateMatrix()
    trussInstances.setMatrixAt(idx, tDummy.matrix)
  })
  trussInstances.instanceMatrix.needsUpdate = true
  cinemaRoot.add(trussInstances)
  disposables.push(trussBarGeo)

  // 3 Overhead Fresnel spotlights + soft view-angle-faded light wash beams
  const fresnelHousingGeo = new THREE.CylinderGeometry(0.14, 0.16, 0.34, 20)
  const spotWashGeo = new THREE.PlaneGeometry(2.2, 4.4)
  const spotWashMat = new THREE.ShaderMaterial({
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
      varying vec2 vUv;
      varying vec3 vNormalView;
      varying vec3 vViewDir;
      void main() {
        float facing = abs(dot(normalize(vNormalView), normalize(vViewDir)));
        float angleFade = pow(facing, 1.45);
        float spread = smoothstep(0.0, 0.35, vUv.x) * smoothstep(1.0, 0.65, vUv.x);
        float beam = pow(vUv.y, 1.7) * smoothstep(1.0, 0.9, vUv.y);
        float alpha = angleFade * spread * beam * 0.085;
        gl_FragColor = vec4(1.0, 0.84, 0.52, alpha);
      }
    `,
  })
  disposables.push(fresnelHousingGeo, spotWashGeo, spotWashMat)

  for (const fx of [-2.4, 0, 2.4]) {
    const can = new THREE.Mesh(fresnelHousingGeo, anodizedBlack)
    can.position.set(fx, 4.95, 0.15)
    can.rotation.x = 0.42
    cinemaRoot.add(can)

    const wash = new THREE.Mesh(spotWashGeo, spotWashMat)
    wash.position.set(fx, 2.85, 0.55)
    wash.rotation.x = -0.14
    cinemaRoot.add(wash)
  }

  // Grounded Director's NLE & Color Grading Desk directly below cinema wall
  const deskGroup = new THREE.Group()
  deskGroup.position.set(0, 0, 0.92)
  cinemaRoot.add(deskGroup)

  const deskShadowGeo = new THREE.PlaneGeometry(4.4, 1.8)
  const deskShadow = new THREE.Mesh(deskShadowGeo, shadowMat)
  deskShadow.rotation.x = -Math.PI / 2
  deskShadow.position.y = 0.008
  deskGroup.add(deskShadow)

  const deskLegGeo = createChamferedBoxGeometry(0.16, 0.92, 0.72, 0.025)
  for (const lx of [-1.55, 1.55]) {
    const leg = new THREE.Mesh(deskLegGeo, titaniumMat)
    leg.position.set(lx, 0, 0)
    deskGroup.add(leg)
  }

  const deskTopGeo = createChamferedBoxGeometry(3.75, 0.08, 0.92, 0.02)
  const deskTop = new THREE.Mesh(deskTopGeo, anodizedBlack)
  deskTop.position.set(0, 0.92, 0)
  deskGroup.add(deskTop)

  const nleFrameGeo = createChamferedBoxGeometry(3.45, 0.98, 0.08, 0.02)
  const nleFrame = new THREE.Mesh(nleFrameGeo, anodizedBlack)
  nleFrame.position.set(0, 1.4, -0.05)
  nleFrame.rotation.x = -0.24
  deskGroup.add(nleFrame)

  const nleGeo = new THREE.PlaneGeometry(3.26, 0.86)
  const nleMat = new THREE.MeshBasicMaterial({ map: timelineScreen.texture })
  const nleMesh = new THREE.Mesh(nleGeo, nleMat)
  nleMesh.position.set(0, 1.4, -0.006)
  nleMesh.rotation.x = -0.24
  deskGroup.add(nleMesh)
  disposables.push(deskShadowGeo, deskLegGeo, deskTopGeo, nleFrameGeo, nleGeo, nleMat)

  // =========================================================================
  // 3. Hero Foreground Prop: ARRI-Style Cinema Camera on Dolly & Steel Rails
  // =========================================================================
  const dollyStage = new THREE.Group()
  dollyStage.position.set(-2.3, 0.18, 3.05)
  dollyStage.rotation.y = 0.56
  group.add(dollyStage)

  const railShadowGeo = new THREE.PlaneGeometry(5.2, 1.8)
  const railShadow = new THREE.Mesh(railShadowGeo, shadowMat)
  railShadow.rotation.x = -Math.PI / 2
  railShadow.position.y = 0.008
  dollyStage.add(railShadow)

  // Two brushed-steel camera tracking rails (4.6m long along local X) + 14 cross-ties
  const railTubeGeo = new THREE.CylinderGeometry(0.032, 0.032, 4.6, 16)
  railTubeGeo.rotateZ(Math.PI / 2)
  for (const rz of [-0.36, 0.36]) {
    const rail = new THREE.Mesh(railTubeGeo, titaniumMat)
    rail.position.set(0, 0.055, rz)
    dollyStage.add(rail)
  }

  const tieGeo = createChamferedBoxGeometry(0.08, 0.025, 0.88, 0.008)
  const tieInstances = new THREE.InstancedMesh(tieGeo, anodizedBlack, 14)
  for (let i = 0; i < 14; i++) {
    tDummy.position.set(-2.15 + i * (4.3 / 13), 0.014, 0)
    tDummy.rotation.set(0, 0, 0)
    tDummy.scale.set(1, 1, 1)
    tDummy.updateMatrix()
    tieInstances.setMatrixAt(i, tDummy.matrix)
  }
  tieInstances.instanceMatrix.needsUpdate = true
  dollyStage.add(tieInstances)
  disposables.push(railShadowGeo, railTubeGeo, tieGeo)

  // Rolling heavy studio dolly chassis facing across/forward onto the soundstage
  const dollyCart = new THREE.Group()
  dollyCart.rotation.y = -Math.PI / 2 // Point camera along +Z of dollyStage (angled nicely toward screen/viewer)
  dollyStage.add(dollyCart)

  const wheelGeo = new THREE.CylinderGeometry(0.068, 0.068, 0.05, 20)
  wheelGeo.rotateZ(Math.PI / 2)
  for (const wx of [-0.36, 0.36]) {
    for (const wz of [-0.42, 0.42]) {
      const wheel = new THREE.Mesh(wheelGeo, carbonMatteMat)
      wheel.position.set(wx, 0.125, wz)
      dollyCart.add(wheel)
    }
  }

  const dollyBaseGeo = createChamferedBoxGeometry(0.84, 0.12, 1.02, 0.025)
  const dollyBase = new THREE.Mesh(dollyBaseGeo, anodizedBlack)
  dollyBase.position.y = 0.16
  dollyCart.add(dollyBase)

  const columnGeo = new THREE.CylinderGeometry(0.11, 0.135, 1.38, 28)
  const column = new THREE.Mesh(columnGeo, anodizedBlack)
  column.position.y = 0.94
  dollyCart.add(column)

  const fluidHeadGeo = createChamferedBoxGeometry(0.34, 0.16, 0.36, 0.03)
  const fluidHead = new THREE.Mesh(fluidHeadGeo, champagneGold)
  fluidHead.position.y = 1.62
  dollyCart.add(fluidHead)
  disposables.push(wheelGeo, dollyBaseGeo, columnGeo, fluidHeadGeo)

  // Cinema Camera Assembly
  const cameraHead = new THREE.Group()
  cameraHead.position.set(0, 1.94, 0)
  cameraHead.rotation.x = 0.06
  dollyCart.add(cameraHead)

  const camChassisGeo = createChamferedBoxGeometry(0.48, 0.42, 0.72, 0.038)
  const camChassis = new THREE.Mesh(camChassisGeo, anodizedBlack)
  camChassis.position.set(0, -0.21, -0.18)
  cameraHead.add(camChassis)

  // Rear V-mount battery
  const batteryGeo = createChamferedBoxGeometry(0.32, 0.34, 0.16, 0.022)
  const battery = new THREE.Mesh(batteryGeo, carbonMatteMat)
  battery.position.set(0, -0.16, -0.6)
  cameraHead.add(battery)

  // Top Handle + Dual Risers
  const topHandleGeo = createChamferedBoxGeometry(0.09, 0.06, 0.48, 0.015)
  const topHandle = new THREE.Mesh(topHandleGeo, anodizedBlack)
  topHandle.position.set(0, 0.28, -0.16)
  cameraHead.add(topHandle)

  const riserGeo = new THREE.CylinderGeometry(0.024, 0.024, 0.1, 14)
  for (const rz of [-0.34, 0.02]) {
    const riser = new THREE.Mesh(riserGeo, champagneGold)
    riser.position.set(0, 0.24, rz)
    cameraHead.add(riser)
  }

  // Side Mini Director Monitor
  const sideMonitorGeo = createChamferedBoxGeometry(0.04, 0.22, 0.36, 0.01)
  const sideMonitor = new THREE.Mesh(sideMonitorGeo, anodizedBlack)
  sideMonitor.position.set(0.31, -0.02, -0.16)
  sideMonitor.rotation.y = 0.28
  cameraHead.add(sideMonitor)

  const sideScreenGeo = new THREE.PlaneGeometry(0.32, 0.18)
  const sideScreenMat = new THREE.MeshBasicMaterial({ map: cinemaStillTex })
  const sideScreen = new THREE.Mesh(sideScreenGeo, sideScreenMat)
  sideScreen.position.set(0.333, 0.09, -0.16)
  sideScreen.rotation.y = Math.PI / 2 + 0.28
  cameraHead.add(sideScreen)

  // Stepped LatheGeometry Anamorphic Prime Lens Barrel
  const lensBarrelProfile = [
    new THREE.Vector2(0.145, 0.16),
    new THREE.Vector2(0.175, 0.18),
    new THREE.Vector2(0.175, 0.36),
    new THREE.Vector2(0.205, 0.38),
    new THREE.Vector2(0.205, 0.54),
    new THREE.Vector2(0.24, 0.58),
    new THREE.Vector2(0.24, 0.72),
    new THREE.Vector2(0.205, 0.72),
  ]
  const lensBarrelGeo = new THREE.LatheGeometry(lensBarrelProfile, 40)
  lensBarrelGeo.rotateX(Math.PI / 2)
  const lensBarrel = new THREE.Mesh(lensBarrelGeo, anodizedBlack)
  cameraHead.add(lensBarrel)

  const focusGearGeo = new THREE.CylinderGeometry(0.214, 0.214, 0.12, 48)
  focusGearGeo.rotateX(Math.PI / 2)
  const focusGear = new THREE.Mesh(focusGearGeo, knurledBrass)
  focusGear.position.z = 0.46
  cameraHead.add(focusGear)

  // Carbon-Fiber Matte Box + Angled French Flag
  const matteBoxGeo = createHollowFrameGeometry(0.68, 0.48, 0.07, 0.18, 0.015)
  const matteBox = new THREE.Mesh(matteBoxGeo, carbonMatteMat)
  matteBox.position.z = 0.78
  cameraHead.add(matteBox)

  const flagGeo = createChamferedBoxGeometry(0.68, 0.018, 0.28, 0.005)
  const flag = new THREE.Mesh(flagGeo, carbonMatteMat)
  flag.position.set(0, 0.26, 0.94)
  flag.rotation.x = 0.32
  cameraHead.add(flag)

  // Convex multi-coated iridescent optical front element (NO transmission)
  const frontGlassGeo = new THREE.SphereGeometry(0.21, 32, 20, 0, Math.PI * 2, 0, Math.PI * 0.34)
  frontGlassGeo.rotateX(Math.PI / 2)
  const frontGlass = new THREE.Mesh(frontGlassGeo, coatedLensGlass)
  frontGlass.position.z = 0.54
  cameraHead.add(frontGlass)

  // Ruby tally REC light + horizontal anamorphic lens flare
  const tallyGeo = new THREE.SphereGeometry(0.022, 10, 10)
  const tallyMat = new THREE.MeshBasicMaterial({ color: 0xff3b3b })
  const tallyLed = new THREE.Mesh(tallyGeo, tallyMat)
  tallyLed.position.set(-0.245, 0.14, 0.04)
  cameraHead.add(tallyLed)

  const flareGeo = new THREE.PlaneGeometry(1.55, 0.24)
  const flareMat = new THREE.MeshBasicMaterial({
    map: flareTex,
    transparent: true,
    opacity: 0.4,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
    side: THREE.DoubleSide,
  })
  const flarePlane = new THREE.Mesh(flareGeo, flareMat)
  flarePlane.position.z = 0.82
  cameraHead.add(flarePlane)

  disposables.push(
    camChassisGeo,
    batteryGeo,
    topHandleGeo,
    riserGeo,
    sideMonitorGeo,
    sideScreenGeo,
    sideScreenMat,
    lensBarrelGeo,
    focusGearGeo,
    matteBoxGeo,
    flagGeo,
    frontGlassGeo,
    tallyGeo,
    tallyMat,
    flareGeo,
    flareMat,
  )

  // =========================================================================
  // 4. Signature 漫剧 Architectural Museum Lightbox Diorama (left flank)
  // =========================================================================
  const manjuGroup = new THREE.Group()
  manjuGroup.position.set(-4.35, 0.18, -0.35)
  manjuGroup.rotation.y = 0.42
  group.add(manjuGroup)

  const manjuShadowGeo = new THREE.PlaneGeometry(4.0, 2.4)
  const manjuShadow = new THREE.Mesh(manjuShadowGeo, shadowMat)
  manjuShadow.rotation.x = -Math.PI / 2
  manjuShadow.position.y = 0.008
  manjuGroup.add(manjuShadow)

  // Grounded honed plinth supporting vitrine cleanly from y = 0 to y = 1.02
  const manjuPlinthGeo = createChamferedBoxGeometry(3.38, 1.02, 1.08, 0.04)
  const manjuPlinth = new THREE.Mesh(manjuPlinthGeo, anodizedBlack)
  manjuGroup.add(manjuPlinth)

  // Outer vitrine frame box + recessed architectural trim
  const vitrineFrameGeo = createHollowFrameGeometry(3.26, 2.22, 0.14, 0.92, 0.035)
  const vitrineFrame = new THREE.Mesh(vitrineFrameGeo, anodizedBlack)
  vitrineFrame.position.set(0, 2.13, -0.02)
  manjuGroup.add(vitrineFrame)

  const brassTrimGeo = createHollowFrameGeometry(3.04, 2.0, 0.035, 0.06, 0.008)
  const brassTrim = new THREE.Mesh(brassTrimGeo, champagneGold)
  brassTrim.position.set(0, 2.13, 0.42)
  manjuGroup.add(brassTrim)

  const manjuHeaderGeo = new THREE.PlaneGeometry(2.8, 0.34)
  const manjuHeaderMat = new THREE.MeshBasicMaterial({ map: manjuLabelTex })
  const manjuHeader = new THREE.Mesh(manjuHeaderGeo, manjuHeaderMat)
  manjuHeader.position.set(0, 3.36, 0.44)
  manjuGroup.add(manjuHeader)

  // Deep Layer A (Back, z = -0.52): twilight anime sky-lantern landscape
  const manjuBgGeo = new THREE.PlaneGeometry(2.94, 1.92)
  const manjuBgMat = new THREE.MeshBasicMaterial({ map: manjuBgTex })
  const manjuBgMesh = new THREE.Mesh(manjuBgGeo, manjuBgMat)
  manjuBgMesh.position.set(0, 2.13, -0.52)
  manjuGroup.add(manjuBgMesh)

  const backSealGeo = createChamferedBoxGeometry(3.22, 2.2, 0.08, 0.02)
  const backSeal = new THREE.Mesh(backSealGeo, anodizedBlack)
  backSeal.position.set(0, 1.03, -0.58)
  manjuGroup.add(backSeal)

  // Mid Layer B (Embers inside vitrine chamber)
  const moteCount = highTier ? 56 : 28
  const motePositions = new Float32Array(moteCount * 3)
  for (let i = 0; i < moteCount; i++) {
    motePositions[i * 3] = Math.sin(i * 12.9898) * 1.35
    motePositions[i * 3 + 1] = 1.25 + (i / moteCount) * 1.75
    motePositions[i * 3 + 2] = -0.35 + ((i * 0.37) % 1) * 0.56
  }
  const moteGeo = new THREE.BufferGeometry()
  moteGeo.setAttribute('position', new THREE.BufferAttribute(motePositions, 3))
  const moteMat = new THREE.PointsMaterial({
    map: softGlow,
    color: 0xfbbf24,
    size: 0.1,
    transparent: true,
    opacity: 0.85,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  })
  const motePoints = new THREE.Points(moteGeo, moteMat)
  manjuGroup.add(motePoints)

  // Foreground Layer C (Hero Portrait Plate at z = +0.32 shifting smoothly via parallax)
  const manjuHeroHolder = new THREE.Group()
  manjuHeroHolder.position.set(0.28, 2.06, 0.32)
  manjuGroup.add(manjuHeroHolder)

  const manjuHeroGeo = new THREE.PlaneGeometry(1.16, 1.54)
  const manjuHeroMat = new THREE.MeshBasicMaterial({ map: manjuHeroTex })
  const manjuHeroMesh = new THREE.Mesh(manjuHeroGeo, manjuHeroMat)
  manjuHeroHolder.add(manjuHeroMesh)

  const heroRimGeo = createHollowFrameGeometry(1.24, 1.62, 0.04, 0.04, 0.01)
  const heroRim = new THREE.Mesh(heroRimGeo, champagneGold)
  heroRim.position.z = -0.012
  manjuHeroHolder.add(heroRim)

  disposables.push(
    manjuShadowGeo,
    manjuPlinthGeo,
    vitrineFrameGeo,
    brassTrimGeo,
    manjuHeaderGeo,
    manjuHeaderMat,
    manjuBgGeo,
    manjuBgMat,
    backSealGeo,
    moteGeo,
    moteMat,
    manjuHeroGeo,
    manjuHeroMat,
    heroRimGeo,
  )

  // =========================================================================
  // 5. Museum-Grade 海报设计 Monolith Gallery (3 Grounded Stepped Monoliths)
  // =========================================================================
  const galleryGroup = new THREE.Group()
  galleryGroup.position.set(4.35, 0.18, 0.65)
  galleryGroup.rotation.y = -0.45
  group.add(galleryGroup)

  const monolithPedGeo = createChamferedBoxGeometry(1.86, 1.08, 0.56, 0.04)
  const monolithBodyGeo = createChamferedBoxGeometry(1.62, 2.48, 0.24, 0.03)
  const posterBezelGeo = createHollowFrameGeometry(1.54, 2.05, 0.045, 0.04, 0.01)
  const posterArtGeo = new THREE.PlaneGeometry(1.45, 1.96)
  const posterShadowGeo = new THREE.PlaneGeometry(2.4, 1.6)
  disposables.push(monolithPedGeo, monolithBodyGeo, posterBezelGeo, posterArtGeo, posterShadowGeo)

  const posterSlots: Array<[number, number, number]> = [
    [-2.05, 0.32, 0.18],
    [0, 0, 0],
    [2.05, 0.32, -0.18],
  ]

  posterSlots.forEach(([px, pz, ry], idx) => {
    const totem = new THREE.Group()
    totem.position.set(px, 0, pz)
    totem.rotation.y = ry
    galleryGroup.add(totem)

    const tShadow = new THREE.Mesh(posterShadowGeo, shadowMat)
    tShadow.rotation.x = -Math.PI / 2
    tShadow.position.y = 0.008
    totem.add(tShadow)

    const ped = new THREE.Mesh(monolithPedGeo, anodizedBlack)
    totem.add(ped)

    const upperBody = new THREE.Mesh(monolithBodyGeo, titaniumMat)
    upperBody.position.y = 1.06
    totem.add(upperBody)

    const frame = new THREE.Mesh(posterBezelGeo, champagneGold)
    frame.position.set(0, 2.34, 0.13)
    totem.add(frame)

    const artMat = new THREE.MeshPhysicalMaterial({
      map: posterTextures[idx],
      roughness: 0.09,
      metalness: 0.04,
      clearcoat: 1.0,
      clearcoatRoughness: 0.04,
      emissive: new THREE.Color(0xffffff),
      emissiveMap: posterTextures[idx],
      emissiveIntensity: 0.36,
      envMapIntensity: 1.5,
    })
    const artMesh = new THREE.Mesh(posterArtGeo, artMat)
    artMesh.position.set(0, 2.34, 0.145)
    totem.add(artMesh)

    const trackWash = new THREE.Mesh(spotWashGeo, spotWashMat)
    trackWash.scale.set(0.75, 0.65, 1)
    trackWash.position.set(0, 2.95, 0.42)
    trackWash.rotation.x = -0.16
    totem.add(trackWash)

    disposables.push(artMat)
  })

  const update = (
    elapsed: number,
    _dt: number,
    pointerX: number,
    pointerY: number,
    focusWeight: number,
    _themeT: number,
    active = true,
  ) => {
    timelineScreen.update(elapsed, active)

    // Smooth dolly slide along steel tracking rails + lens follow focus
    dollyCart.position.x = Math.sin(elapsed * 0.42) * 0.28
    focusGear.rotation.z = Math.sin(elapsed * 0.72) * 0.85
    cameraHead.rotation.y = Math.sin(elapsed * 0.35) * 0.05 + pointerX * 0.04
    cameraHead.rotation.x = 0.06 + pointerY * 0.025
    flarePlane.material.opacity = 0.32 + 0.14 * Math.sin(elapsed * 1.8) + focusWeight * 0.08

    // 漫剧 3D vitrine pointer parallax
    manjuHeroHolder.position.x = 0.28 + pointerX * 0.14
    manjuHeroHolder.position.y = 2.06 + pointerY * 0.08 + Math.sin(elapsed * 1.1) * 0.025

    // Gently rise golden lanterns inside 漫剧 vitrine
    if (active) {
      const posAttr = moteGeo.getAttribute('position') as THREE.BufferAttribute
      for (let i = 0; i < moteCount; i++) {
        let y = posAttr.getY(i) + 0.0028
        if (y > 3.0) y = 1.25
        posAttr.setY(i, y)
      }
      posAttr.needsUpdate = true
    }
  }

  return {
    group,
    update,
    dispose: () => {
      disposables.forEach((d) => d.dispose())
    },
  }
}
