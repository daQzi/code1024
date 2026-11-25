import * as THREE from 'three'
import {
  createLiveCodeScreen,
  createProtocolHubTexture,
  createRoutingMatrixTexture,
  createSoftGlowTexture,
} from './proceduralTextures'

export interface AiCodeWorldHandle {
  group: THREE.Group
  update: (elapsed: number, dt: number, focusWeight: number, themeT: number, active?: boolean) => void
  dispose: () => void
}

function createCurvedScreenGeometry(
  width: number,
  height: number,
  curveDepth: number,
  widthSegments = 28,
): THREE.BufferGeometry {
  const geo = new THREE.PlaneGeometry(width, height, widthSegments, 1)
  const pos = geo.attributes.position as THREE.BufferAttribute
  const halfW = width * 0.5
  for (let i = 0; i < pos.count; i++) {
    const x = pos.getX(i)
    const normX = halfW > 0 ? x / halfW : 0
    // Concave cylindrical wrap toward viewer (+Z at outer edges)
    const z = normX * normX * curveDepth
    pos.setZ(i, z)
  }
  pos.needsUpdate = true
  geo.computeVertexNormals()
  return geo
}

export function buildAiCodeWorld(highTier: boolean): AiCodeWorldHandle {
  const group = new THREE.Group()
  // Positioned closer (-11.2) so both Act 1 panoramic split and Act 2 hero closeup fill viewport richly
  group.position.set(-11.2, 0, -7.2)
  group.rotation.y = 0.5

  const geometries: THREE.BufferGeometry[] = []
  const materials: THREE.Material[] = []
  const textures: THREE.Texture[] = []

  const softGlowTex = createSoftGlowTexture()
  textures.push(softGlowTex)

  const liveCode = createLiveCodeScreen()
  const routingTex = createRoutingMatrixTexture()
  const protocolTex = createProtocolHubTexture()
  textures.push(routingTex, protocolTex)

  // Architectural brushed chassis materials (zero transmission, instant single-pass MSAA)
  const chassisMat = new THREE.MeshStandardMaterial({
    color: 0x101312,
    metalness: 0.72,
    roughness: 0.42,
    envMapIntensity: 0.45,
  })
  const trimCyanMat = new THREE.MeshStandardMaterial({
    color: 0x7ba79b,
    emissive: 0x7ba79b,
    emissiveIntensity: 0.95,
    metalness: 0.72,
    roughness: 0.2,
  })
  materials.push(chassisMat, trimCyanMat)

  // 1. Stepped Illuminated Base Plinth & Circular Holodeck Platform
  const plinthOuterGeo = new THREE.CylinderGeometry(4.6, 5.1, 0.24, 48)
  const plinthInnerGeo = new THREE.CylinderGeometry(3.4, 3.7, 0.2, 48)
  const plinthRingGeo = new THREE.TorusGeometry(4.35, 0.038, 10, 72)
  geometries.push(plinthOuterGeo, plinthInnerGeo, plinthRingGeo)

  const plinthOuter = new THREE.Mesh(plinthOuterGeo, chassisMat)
  plinthOuter.position.set(0, 0.12, 0.4)
  const plinthInner = new THREE.Mesh(plinthInnerGeo, chassisMat)
  plinthInner.position.set(0, 0.34, 0.4)
  const plinthRing = new THREE.Mesh(plinthRingGeo, trimCyanMat)
  plinthRing.rotation.x = Math.PI / 2
  plinthRing.position.set(0, 0.26, 0.4)
  group.add(plinthOuter, plinthInner, plinthRing)

  // 2. Neural Singularity Core (2,200 / 900 Quantum Synapse Particles + Twin Gimbal Rings + Core Glow)
  const singularityPivot = new THREE.Group()
  singularityPivot.position.set(0, 1.55, 0.85)
  group.add(singularityPivot)

  const coreOrbGeo = new THREE.SphereGeometry(0.46, 32, 24)
  geometries.push(coreOrbGeo)
  const coreOrbMat = new THREE.MeshStandardMaterial({
    color: 0x171a18,
    emissive: 0x6d9187,
    emissiveIntensity: 1.1,
    metalness: 0.92,
    roughness: 0.12,
  })
  materials.push(coreOrbMat)
  const coreOrb = new THREE.Mesh(coreOrbGeo, coreOrbMat)
  singularityPivot.add(coreOrb)

  const coreGlowMat = new THREE.SpriteMaterial({
    map: softGlowTex,
    color: 0x7ba79b,
    transparent: true,
    opacity: 0.62,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
  })
  materials.push(coreGlowMat)
  const coreGlowSprite = new THREE.Sprite(coreGlowMat)
  coreGlowSprite.scale.set(3.4, 3.4, 1)
  singularityPivot.add(coreGlowSprite)

  const gimbalGeoA = new THREE.TorusGeometry(1.18, 0.028, 10, 64)
  const gimbalGeoB = new THREE.TorusGeometry(1.42, 0.022, 10, 64)
  geometries.push(gimbalGeoA, gimbalGeoB)
  const gimbalMatB = new THREE.MeshStandardMaterial({
    color: 0x6d9187,
    emissive: 0x3f635a,
    emissiveIntensity: 0.95,
    metalness: 0.84,
    roughness: 0.18,
  })
  materials.push(gimbalMatB)
  const gimbalA = new THREE.Mesh(gimbalGeoA, trimCyanMat)
  gimbalA.rotation.set(0.58, 0.22, 0.18)
  const gimbalB = new THREE.Mesh(gimbalGeoB, gimbalMatB)
  gimbalB.rotation.set(-0.62, -0.35, 0.44)
  singularityPivot.add(gimbalA, gimbalB)

  const neuralCount = highTier ? 2200 : 900
  const nPos = new Float32Array(neuralCount * 3)
  const nMeta = new Float32Array(neuralCount * 3) // speed, phase, colorMix
  for (let i = 0; i < neuralCount; i++) {
    const phi = Math.acos(1 - (2 * (i + 0.5)) / neuralCount)
    const theta = Math.PI * (1 + Math.sqrt(5)) * i
    const rShell = 0.62 + ((i * 17) % 11) * 0.055
    nPos[i * 3] = Math.sin(phi) * Math.cos(theta) * rShell
    nPos[i * 3 + 1] = Math.cos(phi) * rShell
    nPos[i * 3 + 2] = Math.sin(phi) * Math.sin(theta) * rShell
    nMeta[i * 3] = 0.7 + ((i * 13) % 7) * 0.18
    nMeta[i * 3 + 1] = (i * 0.57) % (Math.PI * 2)
    nMeta[i * 3 + 2] = (i % 6) / 5
  }

  const neuralGeo = new THREE.BufferGeometry()
  neuralGeo.setAttribute('position', new THREE.BufferAttribute(nPos, 3))
  neuralGeo.setAttribute('aMeta', new THREE.BufferAttribute(nMeta, 3))
  geometries.push(neuralGeo)

  const neuralMat = new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
    uniforms: {
      uTime: { value: 0 },
      uThemeT: { value: 0 },
      uPixelRatio: { value: Math.min(window.devicePixelRatio || 1, 1.25) },
    },
    vertexShader: /* glsl */ `
      attribute vec3 aMeta;
      uniform float uTime;
      uniform float uPixelRatio;
      varying float vMix;
      varying float vPulse;

      void main() {
        float ang = uTime * 0.35 * aMeta.x;
        float c = cos(ang);
        float s = sin(ang);
        vec3 p = position;
        p.xz = mat2(c, -s, s, c) * p.xz;
        float breathe = 1.0 + 0.08 * sin(uTime * 2.2 + aMeta.y);
        p *= breathe;

        vec4 mv = modelViewMatrix * vec4(p, 1.0);
        gl_PointSize = clamp((2.6 + aMeta.z * 1.6) * uPixelRatio * (11.0 / max(2.5, -mv.z)), 1.6, 8.5);
        vMix = aMeta.z;
        vPulse = 0.55 + 0.45 * sin(uTime * 3.1 + aMeta.y);
        gl_Position = projectionMatrix * mv;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uThemeT;
      varying float vMix;
      varying float vPulse;

      void main() {
        vec2 c = gl_PointCoord - vec2(0.5);
        float r2 = dot(c, c);
        if (r2 > 0.25) discard;
        float alpha = smoothstep(0.25, 0.02, r2) * vPulse * mix(0.92, 0.42, uThemeT);
        vec3 col = mix(vec3(0.49, 0.66, 0.60), vec3(0.69, 0.57, 0.40), vMix);
        gl_FragColor = vec4(col, alpha);
      }
    `,
  })
  materials.push(neuralMat)
  const neuralPoints = new THREE.Points(neuralGeo, neuralMat)
  singularityPivot.add(neuralPoints)

  // 3. Enlarged 3 Primary Curved Holographic Glass Displays + 2 Floating Telemetry Wings
  const screensPivot = new THREE.Group()
  screensPivot.position.set(0, 3.8, -0.45)
  group.add(screensPivot)

  const addScreenCluster = (
    width: number,
    height: number,
    curveDepth: number,
    tex: THREE.Texture,
    pos: [number, number, number],
    rotY: number,
    rotX = 0,
  ) => {
    const faceGeo = createCurvedScreenGeometry(width, height, curveDepth, 28)
    const bezelGeo = createCurvedScreenGeometry(width + 0.14, height + 0.14, curveDepth + 0.02, 28)
    geometries.push(faceGeo, bezelGeo)

    const faceMat = new THREE.MeshBasicMaterial({
      map: tex,
      transparent: true,
      opacity: 0.95,
      side: THREE.FrontSide,
    })
    const bezelMat = new THREE.MeshStandardMaterial({
      color: 0x141715,
      emissive: 0x4a7268,
      emissiveIntensity: 0.24,
      metalness: 0.88,
      roughness: 0.2,
      side: THREE.DoubleSide,
    })
    materials.push(faceMat, bezelMat)

    const holder = new THREE.Group()
    holder.position.set(pos[0], pos[1], pos[2])
    holder.rotation.set(rotX, rotY, 0)

    const bezelMesh = new THREE.Mesh(bezelGeo, bezelMat)
    bezelMesh.position.z = -0.045
    const faceMesh = new THREE.Mesh(faceGeo, faceMat)
    faceMesh.position.z = 0.025

    holder.add(bezelMesh, faceMesh)
    screensPivot.add(holder)
    return { holder, faceMat, bezelMat }
  }

  const mainCluster = addScreenCluster(5.0, 2.9, 0.42, liveCode.texture, [0, 0.25, -0.35], 0, 0.03)
  const leftCluster = addScreenCluster(3.3, 2.45, 0.3, routingTex, [-4.35, 0.0, 0.72], 0.42, 0.02)
  const rightCluster = addScreenCluster(3.3, 2.45, 0.3, protocolTex, [4.35, 0.0, 0.72], -0.42, 0.02)

  // Secondary layered floating glass holographic accent wings (left & right upper-outer flank depth)
  const wingGeo = createCurvedScreenGeometry(2.45, 1.35, 0.18, 16)
  geometries.push(wingGeo)
  const wingMat = new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    side: THREE.DoubleSide,
    blending: THREE.AdditiveBlending,
    uniforms: {
      uTime: { value: 0 },
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
      uniform float uThemeT;
      varying vec2 vUv;
      void main() {
        float borderX = smoothstep(0.0, 0.04, vUv.x) * smoothstep(1.0, 0.96, vUv.x);
        float borderY = smoothstep(0.0, 0.06, vUv.y) * smoothstep(1.0, 0.94, vUv.y);
        float frame = 1.0 - min(borderX, borderY);
        float scan = smoothstep(0.65, 1.0, sin(vUv.y * 42.0 - uTime * 3.5) * 0.5 + 0.5);
        float bar = smoothstep(0.75, 0.98, sin(vUv.x * 18.0 + floor(vUv.y * 6.0) * 1.4 - uTime * 2.2) * 0.5 + 0.5);
        vec3 cyanCol = vec3(0.49, 0.66, 0.60);
        vec3 indigoCol = vec3(0.69, 0.57, 0.40);
        vec3 col = mix(indigoCol, cyanCol, vUv.x * 0.7 + scan * 0.3);
        float alpha = (0.12 + frame * 0.45 + scan * 0.14 + bar * 0.18) * mix(0.85, 0.45, uThemeT);
        gl_FragColor = vec4(col, alpha);
      }
    `,
  })
  materials.push(wingMat)

  const wingLeft = new THREE.Mesh(wingGeo, wingMat)
  wingLeft.position.set(-4.85, 2.2, 0.25)
  wingLeft.rotation.set(0.06, 0.54, 0.04)
  const wingRight = new THREE.Mesh(wingGeo, wingMat)
  wingRight.position.set(4.85, 2.2, 0.25)
  wingRight.rotation.set(0.06, -0.54, -0.04)
  screensPivot.add(wingLeft, wingRight)

  // 4. Upgraded 32 Architectural Data Spires with Upward-Flowing Data-Pulse Strips (InstancedMesh-aware GLSL)
  const spireCount = 32
  const spireGeo = new THREE.BoxGeometry(0.46, 1.0, 0.46)
  // Translate geometry base to y = 0 so scaling Y grows strictly upward from floor
  spireGeo.translate(0, 0.5, 0)
  geometries.push(spireGeo)

  const spireMat = new THREE.ShaderMaterial({
    transparent: false,
    depthWrite: true,
    fog: false,
    uniforms: {
      uTime: { value: 0 },
      uThemeT: { value: 0 },
    },
    vertexShader: /* glsl */ `
      varying vec2 vUv;
      varying vec3 vNormalW;
      varying float vWorldY;
      varying float vInstPhase;

      void main() {
        vUv = uv;
        vec4 localPos = vec4(position, 1.0);
        #ifdef USE_INSTANCING
          localPos = instanceMatrix * localPos;
          mat3 instRot = mat3(instanceMatrix[0].xyz, instanceMatrix[1].xyz, instanceMatrix[2].xyz);
          vNormalW = normalize(mat3(modelMatrix) * instRot * normal);
          vInstPhase = instanceMatrix[3][0] * 0.43 + instanceMatrix[3][2] * 0.67;
        #else
          vNormalW = normalize(mat3(modelMatrix) * normal);
          vInstPhase = 0.0;
        #endif
        vec4 wp = modelMatrix * localPos;
        vWorldY = wp.y;
        gl_Position = projectionMatrix * viewMatrix * wp;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTime;
      uniform float uThemeT;
      varying vec2 vUv;
      varying vec3 vNormalW;
      varying float vWorldY;
      varying float vInstPhase;

      void main() {
        float isVerticalSide = 1.0 - step(0.85, abs(vNormalW.y));
        // Sleek chamfer edge & vertical telemetry channels
        float edgeStrip = smoothstep(0.36, 0.47, abs(vUv.x - 0.5));
        float centerSlot = 1.0 - smoothstep(0.04, 0.11, abs(vUv.x - 0.5));
        float flowWave = pow(max(0.0, sin(vWorldY * 1.15 - uTime * 3.4 + vInstPhase)), 6.0);
        float heightFade = smoothstep(0.1, 1.5, vWorldY);

        vec3 n = normalize(vNormalW);
        float lightMix = clamp(dot(n, normalize(vec3(0.4, 0.85, 0.55))) * 0.5 + 0.5, 0.0, 1.0);

        // Dark obsidian basalt vs Light Mode frosted alabaster stone monoliths
        vec3 darkStone = mix(vec3(0.030, 0.036, 0.034), vec3(0.055, 0.065, 0.061), lightMix);
        vec3 lightStone = mix(vec3(0.83, 0.82, 0.80), vec3(0.94, 0.94, 0.92), lightMix);
        vec3 baseBody = mix(darkStone, lightStone, uThemeT);

        vec3 cyanGlow = vec3(0.49, 0.66, 0.60);
        vec3 indigoGlow = vec3(0.69, 0.57, 0.40);
        vec3 pulseColor = mix(cyanGlow, indigoGlow, fract(vInstPhase * 0.37));

        float energy = (edgeStrip * 0.38 + centerSlot * flowWave * 1.15) * isVerticalSide * heightFade;
        vec3 darkCol = baseBody + pulseColor * energy * 1.15;
        vec3 lightCol = mix(baseBody, vec3(0.25, 0.44, 0.40), clamp(energy * 0.48, 0.0, 0.65));

        gl_FragColor = vec4(mix(darkCol, lightCol, uThemeT), 1.0);
      }
    `,
  })
  materials.push(spireMat)

  const spiresInstanced = new THREE.InstancedMesh(spireGeo, spireMat, spireCount)
  const dummy = new THREE.Object3D()
  for (let i = 0; i < spireCount; i++) {
    const t = i / (spireCount - 1)
    // Keep strictly behind & along outer-left semicircular horizon (sin(angle) <= -0.1) so screens & plinth stay unobstructed
    const arcAngle = -Math.PI * 1.02 + t * Math.PI * 0.96
    const radius = 6.4 + (i % 4) * 1.5
    const x = Math.cos(arcAngle) * radius - 1.1
    const z = Math.sin(arcAngle) * radius * 0.78 - 3.2
    const height = 6.5 + ((i * 19) % 13) * 0.48 // 6.5m .. 12.26m
    const wScale = 0.7 + (i % 3) * 0.22

    dummy.position.set(x, 0, z)
    dummy.rotation.set(0, -arcAngle * 0.45, 0)
    dummy.scale.set(wScale, height, wScale)
    dummy.updateMatrix()
    spiresInstanced.setMatrixAt(i, dummy.matrix)
  }
  spiresInstanced.instanceMatrix.needsUpdate = true
  group.add(spiresInstanced)

  const update = (elapsed: number, dt: number, focusWeight: number, themeT: number, active = true) => {
    const safeDt = Math.min(dt, 0.05)

    // 6Hz gated live code terminal update when screen is visible/active
    liveCode.update(elapsed, active)

    neuralMat.uniforms.uTime.value = elapsed
    neuralMat.uniforms.uThemeT.value = themeT
    wingMat.uniforms.uTime.value = elapsed
    wingMat.uniforms.uThemeT.value = themeT
    spireMat.uniforms.uTime.value = elapsed
    spireMat.uniforms.uThemeT.value = themeT

    singularityPivot.position.y = 1.55 + Math.sin(elapsed * 1.4) * 0.08
    coreOrb.rotation.y += safeDt * 0.55
    gimbalA.rotation.y += safeDt * (0.48 + focusWeight * 0.25)
    gimbalA.rotation.x += safeDt * 0.16
    gimbalB.rotation.y -= safeDt * (0.38 + focusWeight * 0.22)
    gimbalB.rotation.z += safeDt * 0.21

    // Smooth holographic parallax breathing & focus enlargement
    screensPivot.position.y = 3.8 + Math.sin(elapsed * 0.9 + 0.6) * 0.07
    wingLeft.position.y = 2.2 + Math.sin(elapsed * 1.15) * 0.09
    wingRight.position.y = 2.2 + Math.cos(elapsed * 1.15) * 0.09

    const themeChassis = themeT > 0.5 ? 0xece9e2 : 0x101312
    chassisMat.color.setHex(themeChassis)
    mainCluster.bezelMat.color.setHex(themeT > 0.5 ? 0xd2cec7 : 0x141715)
    leftCluster.bezelMat.color.setHex(themeT > 0.5 ? 0xd2cec7 : 0x141715)
    rightCluster.bezelMat.color.setHex(themeT > 0.5 ? 0xd2cec7 : 0x141715)
    trimCyanMat.emissiveIntensity = THREE.MathUtils.lerp(0.95, 0.35, themeT)
    coreGlowMat.opacity = THREE.MathUtils.lerp(0.62, 0.25, themeT) * (0.85 + 0.15 * focusWeight)
  }

  const dispose = () => {
    liveCode.dispose()
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
