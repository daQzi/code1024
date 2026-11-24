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

function createCurvedRailGeometry(radius: number, tube: number, spanRad: number): THREE.TorusGeometry {
  const geo = new THREE.TorusGeometry(radius, tube, 8, 56, spanRad)
  geo.rotateZ(-Math.PI / 2 - spanRad / 2)
  geo.rotateX(Math.PI / 2)
  return geo
}

export function buildQimuStudioWorld(
  renderer: THREE.WebGLRenderer,
  highTier: boolean,
): QimuStudioWorldHandle {
  const group = new THREE.Group()
  group.position.set(14.5, 0, -8.5)
  group.rotation.y = -0.44

  const disposables: Array<{ dispose: () => void }> = []
  const softGlow = createSoftGlowTexture()
  const timelineScreen = createLiveTimelineScreen()
  disposables.push(softGlow, timelineScreen)

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

  const champagneRailMat = new THREE.MeshStandardMaterial({
    color: 0xf0c27b,
    metalness: 0.94,
    roughness: 0.2,
    envMapIntensity: 1.6,
  })
  const posterTrimMat = new THREE.MeshBasicMaterial({
    color: 0xf0c27b,
    transparent: true,
    opacity: 0.52,
  })
  disposables.push(champagneRailMat, posterTrimMat)

  // =========================================================================
  // 1. Colossal Frameless Curved Panoramic IMAX Cinema Canvas (7.2m x 3.45m)
  // =========================================================================
  const cinemaArcPivot = new THREE.Group()
  cinemaArcPivot.position.set(0, 3.62, 9.5) // Curve radius 12.0 places center cleanly at z = -2.5
  group.add(cinemaArcPivot)

  const cineRadius = 12.0
  const cineSpan = 0.6
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
  cinemaArcPivot.add(cineScreen)

  const cineRailGeo = createCurvedRailGeometry(cineRadius - 0.02, 0.018, cineSpan + 0.01)
  const cineTopRail = new THREE.Mesh(cineRailGeo, champagneRailMat)
  cineTopRail.position.y = 3.45 / 2 + 0.02
  cinemaArcPivot.add(cineTopRail)

  const cineBotRail = new THREE.Mesh(cineRailGeo, champagneRailMat)
  cineBotRail.position.y = -3.45 / 2 - 0.02
  cinemaArcPivot.add(cineBotRail)

  // Soft warm ambient backlight sprite behind IMAX canvas (no hard spotlight triangles)
  const cineHaloGeo = new THREE.PlaneGeometry(14.5, 8.2)
  const cineHaloMat = new THREE.MeshBasicMaterial({
    map: softGlow,
    color: 0xf0c27b,
    transparent: true,
    opacity: 0.16,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  })
  const cineHalo = new THREE.Mesh(cineHaloGeo, cineHaloMat)
  cineHalo.position.set(0, 3.65, -3.15)
  group.add(cineHalo)
  disposables.push(cineScreenGeo, cineScreenMat, cineRailGeo, cineHaloGeo, cineHaloMat)

  // Floating Translucent Smoked-Glass 24fps Spatial Timeline Ribbon (NO park bench)
  const timelinePivot = new THREE.Group()
  timelinePivot.position.set(0, 1.18, 9.8) // Radius 11.4 puts ribbon nicely forward at z = -1.6
  group.add(timelinePivot)

  const tlRadius = 11.4
  const tlSpan = 0.49
  const tlGeo = new THREE.CylinderGeometry(
    tlRadius,
    tlRadius,
    0.76,
    36,
    1,
    true,
    Math.PI - tlSpan / 2,
    tlSpan,
  )
  tlGeo.scale(-1, 1, 1)
  const tlMat = new THREE.MeshBasicMaterial({
    map: timelineScreen.texture,
    side: THREE.FrontSide,
    transparent: true,
    opacity: 0.96,
  })
  const tlMesh = new THREE.Mesh(tlGeo, tlMat)
  timelinePivot.add(tlMesh)

  const tlRailGeo = createCurvedRailGeometry(tlRadius - 0.02, 0.012, tlSpan + 0.008)
  const tlTopRail = new THREE.Mesh(tlRailGeo, champagneRailMat)
  tlTopRail.position.y = 0.76 / 2 + 0.014
  timelinePivot.add(tlTopRail)

  const tlBotRail = new THREE.Mesh(tlRailGeo, champagneRailMat)
  tlBotRail.position.y = -0.76 / 2 - 0.014
  timelinePivot.add(tlBotRail)
  disposables.push(tlGeo, tlMat, tlRailGeo)

  // =========================================================================
  // 2. Seamless 3D Depth-Parallax 漫剧 Diorama (Left Wing, crisp magazine gap)
  // =========================================================================
  const manjuGroup = new THREE.Group()
  manjuGroup.position.set(-5.75, 2.95, 0.85)
  manjuGroup.rotation.y = 0.42
  group.add(manjuGroup)

  const manjuBgGeo = new THREE.PlaneGeometry(3.55, 2.18)
  const manjuBgMat = new THREE.ShaderMaterial({
    uniforms: { uMap: { value: manjuBgTex } },
    transparent: true,
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
        vec4 tex = texture2D(uMap, vUv);
        float edgeX = smoothstep(0.0, 0.06, vUv.x) * smoothstep(1.0, 0.94, vUv.x);
        float edgeY = smoothstep(0.0, 0.06, vUv.y) * smoothstep(1.0, 0.94, vUv.y);
        gl_FragColor = vec4(tex.rgb, tex.a * edgeX * edgeY * 0.96);
      }
    `,
  })
  const manjuBgMesh = new THREE.Mesh(manjuBgGeo, manjuBgMat)
  manjuBgMesh.position.z = -0.52
  manjuGroup.add(manjuBgMesh)

  const manjuRailGeo = new THREE.BoxGeometry(3.48, 0.018, 0.018)
  const manjuTopRail = new THREE.Mesh(manjuRailGeo, champagneRailMat)
  manjuTopRail.position.set(0, 2.18 / 2 + 0.01, -0.51)
  manjuGroup.add(manjuTopRail)

  const manjuBotRail = new THREE.Mesh(manjuRailGeo, champagneRailMat)
  manjuBotRail.position.set(0, -2.18 / 2 - 0.01, -0.51)
  manjuGroup.add(manjuBotRail)

  // Mid 3D Sky-Lantern Bokeh Volume
  const moteCount = highTier ? 64 : 32
  const motePositions = new Float32Array(moteCount * 3)
  for (let i = 0; i < moteCount; i++) {
    motePositions[i * 3] = Math.sin(i * 12.9898) * 1.55
    motePositions[i * 3 + 1] = ((i / moteCount) - 0.5) * 1.95
    motePositions[i * 3 + 2] = -0.2 + ((i * 0.37) % 1) * 0.45
  }
  const moteGeo = new THREE.BufferGeometry()
  moteGeo.setAttribute('position', new THREE.BufferAttribute(motePositions, 3))
  const moteMat = new THREE.PointsMaterial({
    map: softGlow,
    color: 0xf0c27b,
    size: 0.11,
    transparent: true,
    opacity: 0.84,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  })
  const motePoints = new THREE.Points(moteGeo, moteMat)
  manjuGroup.add(motePoints)

  // Foreground Seamless Holographic Character (Luminance-Keyed + Soft Edge Feathering!)
  const manjuHeroGeo = new THREE.PlaneGeometry(1.45, 1.92)
  const manjuHeroMat = new THREE.ShaderMaterial({
    uniforms: { uMap: { value: manjuHeroTex } },
    transparent: true,
    depthWrite: false,
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
        vec4 tex = texture2D(uMap, vUv);
        float luma = dot(tex.rgb, vec3(0.2126, 0.7152, 0.0722));
        float maxCh = max(tex.r, max(tex.g, tex.b));
        float minCh = min(tex.r, min(tex.g, tex.b));
        float chroma = maxCh - minCh;
        float signal = luma + chroma * 0.42;

        float keyAlpha = smoothstep(0.055, 0.155, signal);
        float edgeX = smoothstep(0.0, 0.12, vUv.x) * smoothstep(1.0, 0.88, vUv.x);
        float edgeY = smoothstep(0.0, 0.16, vUv.y) * smoothstep(1.0, 0.92, vUv.y);
        float alpha = keyAlpha * edgeX * edgeY;
        if (alpha < 0.02) discard;

        gl_FragColor = vec4(tex.rgb, alpha);
      }
    `,
  })
  const manjuHeroMesh = new THREE.Mesh(manjuHeroGeo, manjuHeroMat)
  manjuHeroMesh.position.set(0.18, -0.05, 0.42)
  manjuGroup.add(manjuHeroMesh)

  disposables.push(
    manjuBgGeo,
    manjuBgMat,
    manjuRailGeo,
    moteGeo,
    moteMat,
    manjuHeroGeo,
    manjuHeroMat,
  )

  // =========================================================================
  // 3. Curated Floating Editorial 海报设计 Gallery Arc (Right Wing, cleanly separated)
  // =========================================================================
  const galleryGroup = new THREE.Group()
  galleryGroup.position.set(5.65, 2.85, 0.95)
  galleryGroup.rotation.y = -0.4
  group.add(galleryGroup)

  const posterGeo = new THREE.PlaneGeometry(1.42, 1.9)
  const posterBevelGeo = new THREE.PlaneGeometry(1.455, 1.935)
  const posterHaloGeo = new THREE.PlaneGeometry(2.5, 3.1)
  const posterHaloMat = new THREE.MeshBasicMaterial({
    map: softGlow,
    color: 0xf0c27b,
    transparent: true,
    opacity: 0.14,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  })
  disposables.push(posterGeo, posterBevelGeo, posterHaloGeo, posterHaloMat)

  const posterSlots: Array<[number, number, number, number]> = [
    [-1.56, -0.08, 0.24, 0.14],
    [0, 0.16, 0, 0],
    [1.56, -0.06, 0.24, -0.14],
  ]

  const posterCards: Array<{ mesh: THREE.Group; baseY: number }> = []

  posterSlots.forEach(([px, py, pz, ry], idx) => {
    const card = new THREE.Group()
    card.position.set(px, py, pz)
    card.rotation.y = ry
    galleryGroup.add(card)
    posterCards.push({ mesh: card, baseY: py })

    const trim = new THREE.Mesh(posterBevelGeo, posterTrimMat)
    trim.position.z = -0.012
    card.add(trim)

    const artMat = new THREE.MeshPhysicalMaterial({
      map: posterTextures[idx],
      roughness: 0.08,
      metalness: 0.04,
      clearcoat: 1.0,
      clearcoatRoughness: 0.04,
      emissive: new THREE.Color(0xffffff),
      emissiveMap: posterTextures[idx],
      emissiveIntensity: 0.38,
      envMapIntensity: 1.35,
    })
    const artMesh = new THREE.Mesh(posterGeo, artMat)
    card.add(artMesh)

    const halo = new THREE.Mesh(posterHaloGeo, posterHaloMat)
    halo.position.z = -0.18
    card.add(halo)

    disposables.push(artMat)
  })

  // =========================================================================
  // 4. Single Graceful Overhead Luminous Cinema Filament Arc
  // =========================================================================
  const arcCurve = new THREE.CatmullRomCurve3([
    new THREE.Vector3(-5.8, 4.9, 0.65),
    new THREE.Vector3(-2.6, 5.85, -1.2),
    new THREE.Vector3(0, 6.0, -2.35),
    new THREE.Vector3(2.8, 5.65, -1.05),
    new THREE.Vector3(5.6, 4.85, 0.8),
  ])
  const arcTubeGeo = new THREE.TubeGeometry(arcCurve, 56, 0.014, 8, false)
  const arcTubeMat = new THREE.MeshBasicMaterial({
    color: 0xc4b5fd,
    transparent: true,
    opacity: 0.44,
  })
  const arcTube = new THREE.Mesh(arcTubeGeo, arcTubeMat)
  group.add(arcTube)

  const sparkGeo = new THREE.SphereGeometry(0.052, 12, 12)
  const sparkMat = new THREE.MeshBasicMaterial({ color: 0xfde68a })
  const sparkBead = new THREE.Mesh(sparkGeo, sparkMat)
  group.add(sparkBead)
  disposables.push(arcTubeGeo, arcTubeMat, sparkGeo, sparkMat)

  const update = (
    elapsed: number,
    _dt: number,
    pointerX: number,
    pointerY: number,
    focusWeight: number,
    themeT: number,
    active = true,
  ) => {
    timelineScreen.update(elapsed, active)

    cineHaloMat.opacity = (0.14 + focusWeight * 0.06) * (1 - themeT * 0.72)
    posterHaloMat.opacity = 0.13 * (1 - themeT * 0.72)
    arcTubeMat.opacity = THREE.MathUtils.lerp(0.44, 0.24, themeT)

    // Seamless 漫剧 3D depth parallax response
    manjuHeroMesh.position.x = 0.18 + pointerX * 0.16
    manjuHeroMesh.position.y = -0.05 + pointerY * 0.08 + Math.sin(elapsed * 1.1) * 0.025
    manjuGroup.rotation.y = 0.42 + pointerX * 0.045

    if (active) {
      const posAttr = moteGeo.getAttribute('position') as THREE.BufferAttribute
      for (let i = 0; i < moteCount; i++) {
        let y = posAttr.getY(i) + 0.0024
        if (y > 0.98) y = -0.98
        posAttr.setY(i, y)
      }
      posAttr.needsUpdate = true
    }

    // Subtle museum gallery levitation breath
    posterCards.forEach((p, idx) => {
      p.mesh.position.y = p.baseY + Math.sin(elapsed * 0.9 + idx * 1.25) * 0.04
    })

    const sparkT = (elapsed * 0.18) % 1
    arcCurve.getPointAt(sparkT, sparkBead.position)
  }

  return {
    group,
    update,
    dispose: () => {
      disposables.forEach((d) => d.dispose())
    },
  }
}
