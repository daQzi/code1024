import * as THREE from 'three'
import {
  createLiveCodeScreen,
  createProtocolHubTexture,
  createRoutingMatrixTexture,
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

function createCurvedRailGeometry(radius: number, tube: number, spanRad: number): THREE.TorusGeometry {
  const geo = new THREE.TorusGeometry(radius, tube, 8, 44, spanRad)
  // Shift arc [0, spanRad] to center around -PI/2, then map XY plane -> XZ plane around -Z
  geo.rotateZ(-Math.PI / 2 - spanRad / 2)
  geo.rotateX(Math.PI / 2)
  return geo
}

export function buildAiCodeWorld(highTier: boolean): AiCodeWorldHandle {
  const group = new THREE.Group()
  group.position.set(-14.5, 0, -8.5)
  group.rotation.y = 0.44

  const disposables: Array<{ dispose: () => void }> = []
  const softGlow = createSoftGlowTexture()
  const codeScreen = createLiveCodeScreen()
  const routingTex = createRoutingMatrixTexture()
  const protocolTex = createProtocolHubTexture()
  disposables.push(softGlow, codeScreen, routingTex, protocolTex)

  const railPlatinumMat = new THREE.MeshStandardMaterial({
    color: 0xcbd5e1,
    metalness: 0.94,
    roughness: 0.2,
    envMapIntensity: 1.5,
  })
  const darkSpireMat = new THREE.MeshStandardMaterial({
    color: 0x09111f,
    metalness: 0.88,
    roughness: 0.34,
    envMapIntensity: 1.25,
  })
  const titaniumMat = new THREE.MeshStandardMaterial({
    color: 0x94a3b8,
    metalness: 0.95,
    roughness: 0.2,
    envMapIntensity: 1.5,
  })
  disposables.push(railPlatinumMat, darkSpireMat, titaniumMat)

  // =========================================================================
  // 1. Three Sweeping Curved Frameless Architectural Smoked-Glass Monoliths
  // =========================================================================
  const arcPivot = new THREE.Group()
  arcPivot.position.set(0, 3.38, 7.2)
  group.add(arcPivot)

  const arcRadius = 9.2

  const addCurvedPanel = (
    widthMeters: number,
    heightMeters: number,
    yawAngle: number,
    texture: THREE.Texture,
    opacity = 0.96,
  ) => {
    const holder = new THREE.Group()
    holder.rotation.y = yawAngle
    arcPivot.add(holder)

    const span = widthMeters / arcRadius
    const screenGeo = new THREE.CylinderGeometry(
      arcRadius,
      arcRadius,
      heightMeters,
      36,
      1,
      true,
      Math.PI - span / 2,
      span,
    )
    screenGeo.scale(-1, 1, 1)
    const screenMat = new THREE.MeshBasicMaterial({
      map: texture,
      side: THREE.FrontSide,
      transparent: true,
      opacity,
    })
    const screenMesh = new THREE.Mesh(screenGeo, screenMat)
    holder.add(screenMesh)

    const railGeo = createCurvedRailGeometry(arcRadius - 0.02, 0.014, span + 0.01)
    const topRail = new THREE.Mesh(railGeo, railPlatinumMat)
    topRail.position.y = heightMeters / 2 + 0.016
    holder.add(topRail)

    const botRail = new THREE.Mesh(railGeo, railPlatinumMat)
    botRail.position.y = -heightMeters / 2 - 0.016
    holder.add(botRail)

    disposables.push(screenGeo, screenMat, railGeo)
    return holder
  }

  // Center Hero Gateway Stream, Left Routing Matrix, Right Unified Protocol Hub
  const centerPanel = addCurvedPanel(4.4, 2.55, 0, codeScreen.texture, 0.98)
  const leftPanel = addCurvedPanel(2.85, 2.15, 0.46, routingTex, 0.94)
  const rightPanel = addCurvedPanel(2.85, 2.15, -0.46, protocolTex, 0.94)

  // Soft atmospheric cyan radial glow gently lifting glass panels from nebula backdrop
  const haloGeo = new THREE.PlaneGeometry(14, 7.5)
  const haloMat = new THREE.MeshBasicMaterial({
    map: softGlow,
    color: 0x38e8d5,
    transparent: true,
    opacity: 0.16,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  })
  const haloMesh = new THREE.Mesh(haloGeo, haloMat)
  haloMesh.position.set(0, 3.4, -3.2)
  group.add(haloMesh)
  disposables.push(haloGeo, haloMat)

  // =========================================================================
  // 2. 1,800-Particle GPU Multimodal Neural Singularity (Foreground-Left Lower)
  // =========================================================================
  const singularityPivot = new THREE.Group()
  singularityPivot.position.set(-2.25, 1.48, 2.45)
  group.add(singularityPivot)

  const gimbalGeo = new THREE.TorusGeometry(1.18, 0.012, 10, 96)
  const gimbalRing = new THREE.Mesh(gimbalGeo, titaniumMat)
  gimbalRing.rotation.set(0.42, 0.25, 0)
  singularityPivot.add(gimbalRing)
  disposables.push(gimbalGeo)

  const count = highTier ? 1800 : 800
  const posA = new Float32Array(count * 3) // Neural Sphere
  const posB = new Float32Array(count * 3) // Image Diffusion Planes
  const posC = new Float32Array(count * 3) // Video Cinema Helix
  const posD = new Float32Array(count * 3) // Audio Harmonic Ripples
  const seeds = new Float32Array(count)

  for (let i = 0; i < count; i++) {
    const t = i / count
    const phi = Math.acos(1 - 2 * t)
    const theta = Math.PI * (1 + Math.sqrt(5)) * i
    seeds[i] = (i * 0.6180339887) % 1

    const rA = 0.78 + (seeds[i] - 0.5) * 0.1
    posA[i * 3] = rA * Math.sin(phi) * Math.cos(theta)
    posA[i * 3 + 1] = rA * Math.cos(phi)
    posA[i * 3 + 2] = rA * Math.sin(phi) * Math.sin(theta)

    const layer = i % 3
    const gridIdx = Math.floor(i / 3)
    const cols = Math.ceil(Math.sqrt(count / 3))
    posB[i * 3] = ((gridIdx % cols) / cols - 0.5) * 1.25
    posB[i * 3 + 1] = (Math.floor(gridIdx / cols) / cols - 0.5) * 1.25
    posB[i * 3 + 2] = (layer - 1) * 0.34

    const hAngle = t * Math.PI * 6 + (i % 2) * Math.PI
    posC[i * 3] = Math.cos(hAngle) * 0.58
    posC[i * 3 + 1] = (t - 0.5) * 1.45
    posC[i * 3 + 2] = Math.sin(hAngle) * 0.58

    const ringIdx = i % 5
    const rAngle = t * Math.PI * 16
    const rr = 0.2 + ringIdx * 0.14
    posD[i * 3] = Math.cos(rAngle) * rr
    posD[i * 3 + 1] = Math.sin(rr * 10) * 0.26 + (ringIdx - 2) * 0.14
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
    uTheme: { value: 0 },
  }
  const morphMat = new THREE.ShaderMaterial({
    uniforms: morphUniforms,
    transparent: true,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
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
        float c = cos(uTime * 0.36);
        float s = sin(uTime * 0.36);
        p.xz = mat2(c, -s, s, c) * p.xz;
        p.y += sin(uTime * 1.4 + aSeed * 6.2831) * 0.03;
        vec4 mvPosition = modelViewMatrix * vec4(p, 1.0);
        gl_PointSize = clamp((30.0 / max(1.0, -mvPosition.z)) * (0.75 + aSeed * 0.65), 1.6, 4.8);
        gl_Position = projectionMatrix * mvPosition;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTheme;
      varying float vPhase;
      varying float vSeed;

      void main() {
        vec2 c = gl_PointCoord - 0.5;
        float d2 = dot(c, c);
        if (d2 > 0.25) discard;
        float mask = smoothstep(0.25, 0.02, d2);

        vec3 cyan = vec3(0.22, 0.91, 0.84);
        vec3 sky = vec3(0.24, 0.72, 0.98);
        vec3 indigo = vec3(0.58, 0.52, 0.98);
        vec3 champagne = vec3(0.94, 0.76, 0.48);

        float m = mod(vPhase, 4.0) / 4.0;
        vec3 col = mix(mix(cyan, sky, vSeed), mix(indigo, champagne, vSeed), 0.5 + 0.5 * sin(m * 6.2831));
        gl_FragColor = vec4(col, mask * mix(0.84, 0.36, uTheme));
      }
    `,
  })
  const morphCloud = new THREE.Points(morphGeo, morphMat)
  singularityPivot.add(morphCloud)
  disposables.push(morphGeo, morphMat)

  // =========================================================================
  // 3. 24 Recessed Architectural Compute Light-Spires (stay cleanly below top header!)
  // =========================================================================
  const spireCount = 24
  const spireGeo = new THREE.BoxGeometry(0.36, 1.0, 0.72)
  const spireEdgeGeo = new THREE.BoxGeometry(0.028, 1.0, 0.028)
  const spireEdgeMat = new THREE.MeshBasicMaterial({
    color: 0x38e8d5,
    transparent: true,
    opacity: 0.42,
  })
  const spireInstances = new THREE.InstancedMesh(spireGeo, darkSpireMat, spireCount)
  const spireEdgeInstances = new THREE.InstancedMesh(spireEdgeGeo, spireEdgeMat, spireCount)
  group.add(spireInstances, spireEdgeInstances)
  disposables.push(spireGeo, spireEdgeGeo, spireEdgeMat)

  const dummy = new THREE.Object3D()
  for (let i = 0; i < spireCount; i++) {
    const t = i / (spireCount - 1)
    const ang = (t - 0.5) * 1.9
    const r = 15.5 + ((i * 7) % 5) * 2.1
    const h = 6.2 + ((i * 11) % 6) * 1.35
    const sx = Math.sin(ang) * r
    const sz = -Math.cos(ang) * r

    dummy.position.set(sx, h / 2 - 0.4, sz)
    dummy.rotation.set(0, -ang, 0)
    dummy.scale.set(1, h, 1)
    dummy.updateMatrix()
    spireInstances.setMatrixAt(i, dummy.matrix)

    dummy.position.set(sx + Math.sin(-ang) * 0.37, h / 2 - 0.4, sz + Math.cos(-ang) * 0.37)
    dummy.scale.set(1, h * 0.9, 1)
    dummy.updateMatrix()
    spireEdgeInstances.setMatrixAt(i, dummy.matrix)
  }
  spireInstances.instanceMatrix.needsUpdate = true
  spireEdgeInstances.instanceMatrix.needsUpdate = true

  const darkSpireCol = new THREE.Color(0x09111f)
  const lightSpireCol = new THREE.Color(0x9aa8bd)

  const update = (
    elapsed: number,
    _dt: number,
    focusWeight: number,
    themeT: number,
    active = true,
  ) => {
    morphUniforms.uTime.value = elapsed
    morphUniforms.uTheme.value = themeT
    codeScreen.update(elapsed, active)

    darkSpireMat.color.copy(darkSpireCol).lerp(lightSpireCol, themeT)
    haloMat.opacity = (0.14 + focusWeight * 0.06) * (1 - themeT * 0.72)
    spireEdgeMat.opacity = THREE.MathUtils.lerp(0.42, 0.25, themeT)

    gimbalRing.rotation.y = 0.25 + elapsed * 0.16
    gimbalRing.rotation.z = Math.sin(elapsed * 0.45) * 0.12

    // Gentle floating breath on frameless curved glass panels
    centerPanel.position.y = Math.sin(elapsed * 0.75) * 0.04
    leftPanel.position.y = Math.sin(elapsed * 0.75 + 1.1) * 0.04
    rightPanel.position.y = Math.sin(elapsed * 0.75 + 2.2) * 0.04
  }

  return {
    group,
    update,
    dispose: () => {
      disposables.forEach((d) => d.dispose())
    },
  }
}
