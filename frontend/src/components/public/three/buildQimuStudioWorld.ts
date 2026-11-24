import * as THREE from 'three'
import { createLiveTimelineScreen, createSoftGlowTexture } from './proceduralTextures'

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

function createCurvedCanvasGeometry(
  width: number,
  height: number,
  curveDepth: number,
  widthSegments = 32,
): THREE.BufferGeometry {
  const geo = new THREE.PlaneGeometry(width, height, widthSegments, 1)
  const pos = geo.attributes.position as THREE.BufferAttribute
  const halfW = width * 0.5
  for (let i = 0; i < pos.count; i++) {
    const x = pos.getX(i)
    const normX = halfW > 0 ? x / halfW : 0
    const z = normX * normX * curveDepth
    pos.setZ(i, z)
  }
  pos.needsUpdate = true
  geo.computeVertexNormals()
  return geo
}

export function buildQimuStudioWorld(renderer: THREE.WebGLRenderer, highTier: boolean): QimuStudioWorldHandle {
  const group = new THREE.Group()
  // Symmetric mirror placement to AICode (-11.2, 0, -7.2)
  group.position.set(11.2, 0, -7.2)
  group.rotation.y = -0.5

  const geometries: THREE.BufferGeometry[] = []
  const materials: THREE.Material[] = []
  const textures: THREE.Texture[] = []

  const maxAniso = Math.min(8, renderer.capabilities.getMaxAnisotropy())
  const texLoader = new THREE.TextureLoader()
  const loadArtTexture = (url: string): THREE.Texture => {
    const tex = texLoader.load(url)
    tex.colorSpace = THREE.SRGBColorSpace
    tex.wrapS = THREE.ClampToEdgeWrapping
    tex.wrapT = THREE.ClampToEdgeWrapping
    tex.anisotropy = maxAniso
    textures.push(tex)
    return tex
  }

  const softGlowTex = createSoftGlowTexture()
  textures.push(softGlowTex)

  const liveTimeline = createLiveTimelineScreen()
  const cinemaStillTex = loadArtTexture('/landing-3d/cinema-still.jpg')
  const manjuBgTex = loadArtTexture('/landing-3d/manju-bg.jpg')
  const manjuHeroTex = loadArtTexture('/landing-3d/manju-hero.jpg')
  const posterTextures = [
    loadArtTexture('/landing-3d/poster-1.jpg'),
    loadArtTexture('/landing-3d/poster-2.jpg'),
    loadArtTexture('/landing-3d/poster-3.jpg'),
  ]

  // Brushed architectural chassis & gold optical accents
  const stageMat = new THREE.MeshStandardMaterial({
    color: 0x0e0c18,
    metalness: 0.82,
    roughness: 0.34,
    envMapIntensity: 0.62,
  })
  const goldTrimMat = new THREE.MeshStandardMaterial({
    color: 0xfbbf24,
    emissive: 0xd97706,
    emissiveIntensity: 0.92,
    metalness: 0.82,
    roughness: 0.18,
  })
  materials.push(stageMat, goldTrimMat)

  // 1. Stepped Cinema Base Plinth & Champagne-Gold Halo Ring
  const plinthOuterGeo = new THREE.CylinderGeometry(4.8, 5.3, 0.24, 48)
  const plinthInnerGeo = new THREE.CylinderGeometry(3.6, 3.9, 0.2, 48)
  const plinthRingGeo = new THREE.TorusGeometry(4.55, 0.038, 10, 72)
  geometries.push(plinthOuterGeo, plinthInnerGeo, plinthRingGeo)

  const plinthOuter = new THREE.Mesh(plinthOuterGeo, stageMat)
  plinthOuter.position.set(0, 0.12, 0.35)
  const plinthInner = new THREE.Mesh(plinthInnerGeo, stageMat)
  plinthInner.position.set(0, 0.34, 0.35)
  const plinthRing = new THREE.Mesh(plinthRingGeo, goldTrimMat)
  plinthRing.rotation.x = Math.PI / 2
  plinthRing.position.set(0, 0.26, 0.35)
  group.add(plinthOuter, plinthInner, plinthRing)

  // 2. Enlarged IMAX Cinema Curved Display (7.6 x 3.65m) + Optical Corner Accents + 6.0 x 0.85m NLE Timeline
  const imaxGroup = new THREE.Group()
  imaxGroup.position.set(-0.2, 4.15, -0.6)
  group.add(imaxGroup)

  const imaxGeo = createCurvedCanvasGeometry(7.6, 3.65, 0.62, 36)
  const imaxBezelGeo = createCurvedCanvasGeometry(7.82, 3.86, 0.65, 36)
  geometries.push(imaxGeo, imaxBezelGeo)

  const imaxMat = new THREE.MeshBasicMaterial({
    map: cinemaStillTex,
    side: THREE.FrontSide,
  })
  const imaxBezelMat = new THREE.MeshStandardMaterial({
    color: 0x161224,
    emissive: 0x78350f,
    emissiveIntensity: 0.2,
    metalness: 0.88,
    roughness: 0.22,
    side: THREE.DoubleSide,
  })
  materials.push(imaxMat, imaxBezelMat)

  const imaxBezel = new THREE.Mesh(imaxBezelGeo, imaxBezelMat)
  imaxBezel.position.z = -0.045
  const imaxScreen = new THREE.Mesh(imaxGeo, imaxMat)
  imaxScreen.position.z = 0.025
  imaxGroup.add(imaxBezel, imaxScreen)

  // Four glowing champagne-gold corner optical frame L-brackets around IMAX edges
  const bracketHGeo = new THREE.BoxGeometry(0.62, 0.045, 0.05)
  const bracketVGeo = new THREE.BoxGeometry(0.045, 0.48, 0.05)
  geometries.push(bracketHGeo, bracketVGeo)
  const corners: [number, number, number, number][] = [
    [-3.72, 1.88, -1, 1],
    [3.72, 1.88, 1, 1],
    [-3.72, -1.88, -1, -1],
    [3.72, -1.88, 1, -1],
  ]
  for (const [cx, cy, sx, sy] of corners) {
    const hBar = new THREE.Mesh(bracketHGeo, goldTrimMat)
    hBar.position.set(cx - sx * 0.28, cy, 0.66)
    hBar.rotation.y = -sx * 0.22
    const vBar = new THREE.Mesh(bracketVGeo, goldTrimMat)
    vBar.position.set(cx, cy - sy * 0.22, 0.66)
    vBar.rotation.y = -sx * 0.22
    imaxGroup.add(hBar, vBar)
  }

  // Live 6Hz NLE Timeline Ribbon mounted below IMAX screen
  const timelineGeo = createCurvedCanvasGeometry(6.0, 0.85, 0.34, 24)
  const timelineBackGeo = createCurvedCanvasGeometry(6.14, 0.96, 0.35, 24)
  geometries.push(timelineGeo, timelineBackGeo)
  const timelineMat = new THREE.MeshBasicMaterial({
    map: liveTimeline.texture,
    transparent: true,
    opacity: 0.96,
    side: THREE.FrontSide,
  })
  materials.push(timelineMat)
  const timelineBack = new THREE.Mesh(timelineBackGeo, imaxBezelMat)
  timelineBack.position.set(0, -2.42, 0.42)
  const timelineMesh = new THREE.Mesh(timelineGeo, timelineMat)
  timelineMesh.position.set(0, -2.42, 0.45)
  imaxGroup.add(timelineBack, timelineMesh)

  // Warm ambient optical glow behind IMAX
  const imaxGlowMat = new THREE.SpriteMaterial({
    map: softGlowTex,
    color: 0xf59e0b,
    transparent: true,
    opacity: 0.42,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
  })
  materials.push(imaxGlowMat)
  const imaxGlow = new THREE.Sprite(imaxGlowMat)
  imaxGlow.scale.set(11.5, 6.2, 1)
  imaxGlow.position.set(0, 0.1, -1.1)
  imaxGroup.add(imaxGlow)

  // 3. Seamless Luminance-Keyed 漫剧 Diorama + Flowing Lantern Particles
  const dioramaGroup = new THREE.Group()
  dioramaGroup.position.set(-2.3, 1.76, 2.2)
  dioramaGroup.rotation.y = 0.26
  group.add(dioramaGroup)

  const bgGeo = new THREE.PlaneGeometry(3.85, 2.35, 1, 1)
  geometries.push(bgGeo)
  const bgMat = new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    uniforms: {
      uMap: { value: manjuBgTex },
      uFeather: { value: 0.11 },
    },
    vertexShader: /* glsl */ `
      varying vec2 vUv;
      void main() {
        vUv = uv;
        gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
      }
    `,
    fragmentShader: /* glsl */ `
      uniform sampler2D uMap;
      uniform float uFeather;
      varying vec2 vUv;
      void main() {
        vec4 tex = texture2D(uMap, vUv);
        float fx = smoothstep(0.0, uFeather, vUv.x) * smoothstep(1.0, 1.0 - uFeather, vUv.x);
        float fy = smoothstep(0.0, uFeather, vUv.y) * smoothstep(1.0, 1.0 - uFeather, vUv.y);
        float vignette = fx * fy;
        gl_FragColor = vec4(tex.rgb, vignette * 0.94);
      }
    `,
  })
  materials.push(bgMat)
  const dioramaBg = new THREE.Mesh(bgGeo, bgMat)
  dioramaGroup.add(dioramaBg)

  const heroGeo = new THREE.PlaneGeometry(3.65, 2.25, 1, 1)
  geometries.push(heroGeo)
  const heroMat = new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    uniforms: {
      uMap: { value: manjuHeroTex },
    },
    vertexShader: /* glsl */ `
      varying vec2 vUv;
      void main() {
        vUv = uv;
        gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
      }
    `,
    fragmentShader: /* glsl */ `
      uniform sampler2D uMap;
      varying vec2 vUv;
      void main() {
        vec3 col = texture2D(uMap, vUv).rgb;
        float luma = dot(col, vec3(0.299, 0.587, 0.114));
        float peak = max(luma, max(col.r, max(col.g, col.b)) * 0.78);
        // Smooth luminance key dissolves dark studio background seamlessly onto scene
        float keyAlpha = smoothstep(0.06, 0.22, peak);
        float edgeX = smoothstep(0.0, 0.08, vUv.x) * smoothstep(1.0, 0.92, vUv.x);
        float edgeY = smoothstep(0.0, 0.09, vUv.y) * smoothstep(1.0, 0.91, vUv.y);
        float alpha = keyAlpha * edgeX * edgeY;
        if (alpha < 0.02) discard;
        gl_FragColor = vec4(col, alpha);
      }
    `,
  })
  materials.push(heroMat)
  const dioramaHero = new THREE.Mesh(heroGeo, heroMat)
  dioramaHero.position.set(0, -0.02, 0.42)
  dioramaGroup.add(dioramaHero)

  // 96 / 44 Floating Warm Lantern & Spark Particles spilling from Diorama into 3D space
  const lanternCount = highTier ? 96 : 44
  const lPos = new Float32Array(lanternCount * 3)
  const lMeta = new Float32Array(lanternCount * 3) // speed, phase, scale
  for (let i = 0; i < lanternCount; i++) {
    const a = (i * 2.3999) % (Math.PI * 2)
    const r = 0.6 + ((i * 19) % 14) * 0.18
    lPos[i * 3] = Math.cos(a) * r * 1.15
    lPos[i * 3 + 1] = -0.8 + ((i * 13) % 15) * 0.16
    lPos[i * 3 + 2] = 0.15 + Math.sin(a) * r * 0.65
    lMeta[i * 3] = 0.45 + ((i * 7) % 5) * 0.16
    lMeta[i * 3 + 1] = (i * 0.89) % (Math.PI * 2)
    lMeta[i * 3 + 2] = 3.2 + ((i * 11) % 7) * 0.65
  }
  const lanternGeo = new THREE.BufferGeometry()
  lanternGeo.setAttribute('position', new THREE.BufferAttribute(lPos, 3))
  lanternGeo.setAttribute('aMeta', new THREE.BufferAttribute(lMeta, 3))
  geometries.push(lanternGeo)

  const lanternMat = new THREE.ShaderMaterial({
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
      varying float vPulse;

      void main() {
        vec3 p = position;
        float t = uTime * aMeta.x + aMeta.y;
        p.x += sin(t * 0.9) * 0.34;
        p.y += mod(uTime * 0.28 + aMeta.y, 2.2) - 0.6;
        p.z += cos(t * 0.75) * 0.26;
        vec4 mv = modelViewMatrix * vec4(p, 1.0);
        gl_PointSize = clamp(aMeta.z * uPixelRatio * (11.0 / max(2.5, -mv.z)), 2.0, 10.0);
        vPulse = 0.55 + 0.45 * sin(t * 2.4);
        gl_Position = projectionMatrix * mv;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uThemeT;
      varying float vPulse;
      void main() {
        vec2 c = gl_PointCoord - vec2(0.5);
        float r2 = dot(c, c);
        if (r2 > 0.25) discard;
        float glow = smoothstep(0.25, 0.01, r2);
        vec3 warmLantern = mix(vec3(1.0, 0.68, 0.18), vec3(1.0, 0.92, 0.68), glow);
        gl_FragColor = vec4(warmLantern, glow * vPulse * mix(0.92, 0.42, uThemeT));
      }
    `,
  })
  materials.push(lanternMat)
  const lanternPoints = new THREE.Points(lanternGeo, lanternMat)
  dioramaGroup.add(lanternPoints)

  // 4. Three Non-Overlapping Design Posters (1.52 x 2.04m) Inside Right Arc
  const posterGeo = new THREE.PlaneGeometry(1.52, 2.04)
  const posterFrameGeo = new THREE.BoxGeometry(1.62, 2.14, 0.06)
  geometries.push(posterGeo, posterFrameGeo)

  const posterSlots = [
    { pos: [2.25, 2.02, 2.85] as const, rotY: -0.26, phase: 0.0 },
    { pos: [4.1, 2.38, 1.45] as const, rotY: -0.52, phase: 1.3 },
    { pos: [5.45, 2.08, -0.35] as const, rotY: -0.78, phase: 2.6 },
  ]
  const posterHolders: { holder: THREE.Group; baseY: number; phase: number }[] = []

  posterSlots.forEach((slot, idx) => {
    const holder = new THREE.Group()
    holder.position.set(slot.pos[0], slot.pos[1], slot.pos[2])
    holder.rotation.y = slot.rotY

    const frame = new THREE.Mesh(posterFrameGeo, stageMat)
    const pMat = new THREE.MeshBasicMaterial({
      map: posterTextures[idx],
      side: THREE.FrontSide,
    })
    materials.push(pMat)
    const art = new THREE.Mesh(posterGeo, pMat)
    art.position.z = 0.036

    const topAccent = new THREE.Mesh(new THREE.BoxGeometry(1.64, 0.04, 0.08), goldTrimMat)
    geometries.push(topAccent.geometry)
    topAccent.position.set(0, 1.07, 0.01)

    holder.add(frame, art, topAccent)
    group.add(holder)
    posterHolders.push({ holder, baseY: slot.pos[1], phase: slot.phase })
  })

  // 5. Twin Sweeping Overhead Cinema Filaments (Gold + Violet) with 3 Orbiting Spark Beads Each
  const filamentDefs = [
    {
      points: [
        new THREE.Vector3(-4.6, 5.2, 2.4),
        new THREE.Vector3(-2.1, 6.95, 0.4),
        new THREE.Vector3(1.6, 6.85, -0.9),
        new THREE.Vector3(5.2, 5.1, 1.4),
      ],
      colorA: new THREE.Color(0xfbbf24),
      colorB: new THREE.Color(0xfef08a),
      speed: 0.24,
    },
    {
      points: [
        new THREE.Vector3(-4.2, 1.4, 3.2),
        new THREE.Vector3(-1.4, 5.9, 1.8),
        new THREE.Vector3(2.4, 6.2, -1.6),
        new THREE.Vector3(5.6, 2.8, -0.4),
      ],
      colorA: new THREE.Color(0xc084fc),
      colorB: new THREE.Color(0xf472b6),
      speed: 0.31,
    },
  ]

  interface FilamentRunner {
    curve: THREE.CatmullRomCurve3
    mat: THREE.ShaderMaterial
    sparks: THREE.Mesh[]
    speed: number
  }
  const filamentRunners: FilamentRunner[] = []
  const sparkGeo = new THREE.OctahedronGeometry(0.11, 0)
  geometries.push(sparkGeo)
  const sparkMat = new THREE.MeshBasicMaterial({ color: 0xfef3c7 })
  materials.push(sparkMat)

  filamentDefs.forEach((def, fIdx) => {
    const curve = new THREE.CatmullRomCurve3(def.points)
    const tubeGeo = new THREE.TubeGeometry(curve, highTier ? 72 : 48, 0.042, 6, false)
    geometries.push(tubeGeo)
    const tubeMat = new THREE.ShaderMaterial({
      transparent: true,
      depthWrite: false,
      blending: THREE.AdditiveBlending,
      uniforms: {
        uTime: { value: 0 },
        uThemeT: { value: 0 },
        uColorA: { value: def.colorA },
        uColorB: { value: def.colorB },
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
        varying vec2 vUv;
        void main() {
          float endMask = smoothstep(0.0, 0.08, vUv.x) * smoothstep(1.0, 0.92, vUv.x);
          float pulse = pow(max(0.0, sin(vUv.x * 14.0 - uTime * 3.6)), 5.0);
          vec3 col = mix(uColorA, uColorB, pulse);
          float alpha = (0.34 + pulse * 0.62) * endMask * mix(0.92, 0.38, uThemeT);
          gl_FragColor = vec4(col, alpha);
        }
      `,
    })
    materials.push(tubeMat)
    const tubeMesh = new THREE.Mesh(tubeGeo, tubeMat)
    group.add(tubeMesh)

    const sparks: THREE.Mesh[] = []
    for (let s = 0; s < 3; s++) {
      const sp = new THREE.Mesh(sparkGeo, sparkMat)
      group.add(sp)
      sparks.push(sp)
    }
    filamentRunners.push({
      curve,
      mat: tubeMat,
      sparks,
      speed: def.speed * (fIdx === 0 ? 1 : -1),
    })
  })

  // 6. 16 Warm Right-Horizon Architectural Light Spires (InstancedMesh-aware GLSL for Symmetric Balance)
  const spireCount = 16
  const spireGeo = new THREE.BoxGeometry(0.48, 1.0, 0.48)
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
          vInstPhase = instanceMatrix[3][0] * 0.41 + instanceMatrix[3][2] * 0.59;
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
        float edgeStrip = smoothstep(0.36, 0.47, abs(vUv.x - 0.5));
        float centerSlot = 1.0 - smoothstep(0.04, 0.11, abs(vUv.x - 0.5));
        float flowWave = pow(max(0.0, sin(vWorldY * 1.1 - uTime * 3.1 + vInstPhase)), 6.0);
        float heightFade = smoothstep(0.1, 1.5, vWorldY);

        vec3 n = normalize(vNormalW);
        float lightMix = clamp(dot(n, normalize(vec3(-0.4, 0.85, 0.55))) * 0.5 + 0.5, 0.0, 1.0);

        vec3 darkStone = mix(vec3(0.032, 0.025, 0.068), vec3(0.088, 0.065, 0.14), lightMix);
        vec3 lightStone = mix(vec3(0.86, 0.83, 0.88), vec3(0.98, 0.96, 0.94), lightMix);
        vec3 baseBody = mix(darkStone, lightStone, uThemeT);

        vec3 goldGlow = vec3(1.0, 0.74, 0.22);
        vec3 violetGlow = vec3(0.84, 0.36, 0.94);
        vec3 pulseColor = mix(goldGlow, violetGlow, fract(vInstPhase * 0.43));

        float energy = (edgeStrip * 0.38 + centerSlot * flowWave * 1.15) * isVerticalSide * heightFade;
        vec3 darkCol = baseBody + pulseColor * energy * 1.15;
        vec3 lightCol = mix(baseBody, vec3(0.82, 0.52, 0.08), clamp(energy * 0.46, 0.0, 0.65));

        gl_FragColor = vec4(mix(darkCol, lightCol, uThemeT), 1.0);
      }
    `,
  })
  materials.push(spireMat)

  const qimuSpires = new THREE.InstancedMesh(spireGeo, spireMat, spireCount)
  const spireDummy = new THREE.Object3D()
  for (let i = 0; i < spireCount; i++) {
    const t = i / (spireCount - 1)
    // Strictly behind & flanking outer-right semicircular horizon (zero foreground pillars blocking IMAX/posters)
    const arcAngle = -Math.PI * 0.96 + t * Math.PI * 0.98
    const radius = 6.6 + (i % 4) * 1.45
    const x = Math.cos(arcAngle) * radius + 1.1
    const z = Math.sin(arcAngle) * radius * 0.78 - 3.4
    const height = 6.5 + ((i * 23) % 13) * 0.46 // 6.5m .. 12.02m
    const wScale = 0.7 + (i % 3) * 0.22

    spireDummy.position.set(x, 0, z)
    spireDummy.rotation.set(0, -arcAngle * 0.4, 0)
    spireDummy.scale.set(wScale, height, wScale)
    spireDummy.updateMatrix()
    qimuSpires.setMatrixAt(i, spireDummy.matrix)
  }
  qimuSpires.instanceMatrix.needsUpdate = true
  group.add(qimuSpires)

  const tempSparkPos = new THREE.Vector3()

  const update = (
    elapsed: number,
    dt: number,
    pointerX: number,
    pointerY: number,
    focusWeight: number,
    themeT: number,
    active = true,
  ) => {
    const safeDt = Math.min(dt, 0.05)

    // 6Hz NLE timeline canvas update gated when visible
    liveTimeline.update(elapsed, active)

    lanternMat.uniforms.uTime.value = elapsed
    lanternMat.uniforms.uThemeT.value = themeT
    spireMat.uniforms.uTime.value = elapsed
    spireMat.uniforms.uThemeT.value = themeT

    // Smooth parallax tilt & micro-hover on 漫剧 diorama hero silhouette
    const pFactor = 0.14 * (0.4 + focusWeight * 0.6)
    dioramaHero.position.x = THREE.MathUtils.lerp(dioramaHero.position.x, pointerX * pFactor, Math.min(1, safeDt * 6))
    dioramaHero.position.y = THREE.MathUtils.lerp(
      dioramaHero.position.y,
      -0.02 + pointerY * pFactor * 0.7 + Math.sin(elapsed * 1.1) * 0.03,
      Math.min(1, safeDt * 6),
    )

    // Gentle hover drift on poster cards
    for (const p of posterHolders) {
      p.holder.position.y = p.baseY + Math.sin(elapsed * 0.95 + p.phase) * 0.08
    }

    // Update filament shader & 3 traveling spark beads along each curve
    for (const runner of filamentRunners) {
      runner.mat.uniforms.uTime.value = elapsed
      runner.mat.uniforms.uThemeT.value = themeT
      const count = runner.sparks.length
      for (let i = 0; i < count; i++) {
        const uRaw = elapsed * runner.speed + i / count
        const u = ((uRaw % 1) + 1) % 1
        runner.curve.getPointAt(u, tempSparkPos)
        runner.sparks[i].position.copy(tempSparkPos)
        runner.sparks[i].rotation.x += safeDt * 2.2
        runner.sparks[i].rotation.y += safeDt * 2.6
      }
    }

    stageMat.color.setHex(themeT > 0.5 ? 0xe7e5e4 : 0x110e1c)
    imaxBezelMat.color.setHex(themeT > 0.5 ? 0xd6d3d1 : 0x161224)
    goldTrimMat.emissiveIntensity = THREE.MathUtils.lerp(0.92, 0.38, themeT)
    imaxGlowMat.opacity = THREE.MathUtils.lerp(0.42, 0.16, themeT) * (0.85 + 0.15 * focusWeight)
  }

  const dispose = () => {
    liveTimeline.dispose()
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
