import * as THREE from 'three'

export interface OpticalCoreHandle {
  group: THREE.Group
  update: (elapsed: number, dt: number, hoverBias: number, themeT: number) => void
  dispose: () => void
}

export function buildOpticalCore(highTier: boolean): OpticalCoreHandle {
  const group = new THREE.Group()
  const disposables: Array<{ dispose: () => void }> = []

  // =========================================================================
  // 1. Central Gravitational Eclipse Singularity & Optical Corona (y = 4.45, z = -9.5)
  // =========================================================================
  const eclipsePivot = new THREE.Group()
  eclipsePivot.position.set(0, 4.45, -9.5)
  group.add(eclipsePivot)

  const eclipseUniforms = {
    uTime: { value: 0 },
    uBias: { value: 0 },
    uTheme: { value: 0 },
  }

  const eclipseGeo = new THREE.PlaneGeometry(15.5, 15.5)
  const eclipseMat = new THREE.ShaderMaterial({
    uniforms: eclipseUniforms,
    transparent: true,
    depthWrite: false,
    fog: false,
    vertexShader: /* glsl */ `
      varying vec2 vUv;
      void main() {
        vUv = uv;
        gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uTime;
      uniform float uBias;
      uniform float uTheme;
      varying vec2 vUv;

      void main() {
        vec2 p = vUv * 2.0 - 1.0;
        float r = length(p);
        if (r > 0.99) discard;

        float angle = (r > 0.0005) ? atan(p.y, p.x) : 0.0;
        float wingMix = clamp(0.5 + uBias * 0.42 + p.x * 0.65, 0.0, 1.0);

        vec3 cyanHue = vec3(0.22, 0.91, 0.84);
        vec3 champagneHue = vec3(0.94, 0.76, 0.48);
        vec3 lavenderHue = vec3(0.76, 0.68, 0.98);
        vec3 sideGlow = mix(cyanHue, mix(champagneHue, lavenderHue, 0.28), wingMix);

        // Razor-crisp optical eclipse event rim at r = 0.258 with deep obsidian interior shadow
        float rimR = 0.258;
        float dRim = r - rimR;
        float outsideCore = smoothstep(rimR - 0.008, rimR + 0.008, r);
        float coreMask = 1.0 - outsideCore;

        // Ultra-crisp photon ring focused outside event horizon
        float brightRing = exp(-abs(dRim) * 145.0) * 1.65 + exp(-max(0.0, dRim) * 34.0) * 0.46;

        // Delicate asymmetric orbital corona wisps (pure outside rimR)
        float wispA = 0.5 + 0.5 * sin(angle * 3.0 - uTime * 0.55 + sin(r * 14.0 - uTime * 0.8));
        float wispB = 0.5 + 0.5 * cos(angle * 5.0 + uTime * 0.38 - r * 10.0);
        float wispMod = 0.58 + 0.42 * (wispA * 0.65 + wispB * 0.35);

        float outerCorona = outsideCore * exp(-max(0.0, r - rimR) * 7.2) * wispMod;
        float haloWash = outsideCore * exp(-max(0.0, r - rimR) * 4.4);

        // Razor-thin anamorphic optical streak clipped cleanly outside dark eclipse core
        float outsideStreak = smoothstep(rimR * 0.88, rimR * 1.12, r);
        float streakHoriz = outsideStreak * exp(-abs(p.y) * 68.0) * exp(-abs(p.x) * 2.2) * 0.72;
        float wideHoriz = outsideStreak * exp(-abs(p.y) * 22.0) * exp(-abs(p.x) * 2.8) * 0.16;

        vec3 coreInterior = mix(vec3(0.004, 0.007, 0.015), vec3(0.07, 0.10, 0.17), uTheme);
        vec3 rimWhiteHot = mix(vec3(0.95, 0.97, 1.0), sideGlow, 0.34);

        vec3 coronaLight = rimWhiteHot * brightRing * (0.82 + 0.22 * wispA)
          + sideGlow * (outerCorona * 0.88 + haloWash * 0.32)
          + mix(vec3(0.94, 0.96, 1.0), sideGlow, 0.38) * (streakHoriz + wideHoriz);

        vec3 col = mix(coronaLight, coreInterior, coreMask * 0.92);

        float edgeFade = smoothstep(0.99, 0.62, r);
        float darkAlpha = clamp(
          coreMask * 0.98 + brightRing * 0.86 + outerCorona * 0.66 + haloWash * 0.28 + (streakHoriz + wideHoriz) * 0.55,
          0.0,
          0.99
        );
        float lightAlpha = clamp(
          coreMask * 0.92 + brightRing * 0.72 + outerCorona * 0.42 + streakHoriz * 0.35,
          0.0,
          0.94
        );

        float alpha = mix(darkAlpha, lightAlpha, uTheme) * edgeFade;
        gl_FragColor = vec4(col * mix(1.0, 0.9, uTheme), alpha);
      }
    `,
  })
  const eclipsePlane = new THREE.Mesh(eclipseGeo, eclipseMat)
  eclipsePivot.add(eclipsePlane)
  disposables.push(eclipseGeo, eclipseMat)

  // =========================================================================
  // 2. Three Whisper-Thin Watchmaker Orbital Hairline Rings
  // =========================================================================
  const platinumMat = new THREE.MeshStandardMaterial({
    color: 0xe2e8f0,
    metalness: 0.95,
    roughness: 0.18,
    envMapIntensity: 1.5,
  })
  const champagneGoldMat = new THREE.MeshStandardMaterial({
    color: 0xf0c27b,
    metalness: 0.95,
    roughness: 0.19,
    envMapIntensity: 1.55,
  })
  const silverSteelMat = new THREE.MeshStandardMaterial({
    color: 0x94a3b8,
    metalness: 0.94,
    roughness: 0.22,
    envMapIntensity: 1.45,
  })
  disposables.push(platinumMat, champagneGoldMat, silverSteelMat)

  const ring1Geo = new THREE.TorusGeometry(4.3, 0.024, 12, 120)
  const ring1 = new THREE.Mesh(ring1Geo, platinumMat)
  ring1.rotation.set(0.32, 0.22, 0.14)
  eclipsePivot.add(ring1)

  const ring2Geo = new THREE.TorusGeometry(5.35, 0.02, 12, 128)
  const ring2 = new THREE.Mesh(ring2Geo, champagneGoldMat)
  ring2.rotation.set(-0.46, 0.34, -0.22)
  eclipsePivot.add(ring2)

  const ring3Geo = new THREE.TorusGeometry(6.6, 0.015, 12, 136)
  const ring3 = new THREE.Mesh(ring3Geo, silverSteelMat)
  ring3.rotation.set(0.18, -0.38, 0.42)
  eclipsePivot.add(ring3)
  disposables.push(ring1Geo, ring2Geo, ring3Geo)

  // =========================================================================
  // 3. 3,600-Particle GPU Macro Silk Flow-Field Universe (1 draw call, analytical bokeh)
  // =========================================================================
  const flowCount = highTier ? 3600 : 1400
  const flowBasePos = new Float32Array(flowCount * 3)
  const flowParams = new Float32Array(flowCount * 3) // [ribbonIndex, ribbonU, seed]

  for (let i = 0; i < flowCount; i++) {
    const ribbon = i % 5
    const u = ((Math.floor(i / 5) + ((i * 0.6180339) % 1) * 0.85) / Math.max(1, flowCount / 5))
    const seed = ((Math.sin(i * 43.173) * 43758.5453) % 1 + 1) % 1

    // Map u in [0, 1] across macro 56m span from x=-28 (AICode) -> 0 (Eclipse) -> x=+28 (Qimu)
    const x = (u - 0.5) * 56.0
    const centerProximity = Math.exp(-Math.pow(x / 10.5, 2))
    const ribbonPhase = (ribbon - 2) * 0.85
    const baseY = 3.8 + Math.sin(u * Math.PI * 2.0 + ribbonPhase) * (1.7 + ribbon * 0.22) + centerProximity * 0.75
    const baseZ =
      -9.2 +
      Math.cos(u * Math.PI * 2.0 - ribbonPhase) * 3.8 +
      (seed - 0.5) * 2.6 -
      Math.pow(Math.abs(x) / 28, 1.5) * 2.2

    flowBasePos[i * 3] = x
    flowBasePos[i * 3 + 1] = Math.max(0.55, baseY)
    flowBasePos[i * 3 + 2] = baseZ

    flowParams[i * 3] = ribbon
    flowParams[i * 3 + 1] = u
    flowParams[i * 3 + 2] = seed
  }

  const flowGeo = new THREE.BufferGeometry()
  flowGeo.setAttribute('position', new THREE.BufferAttribute(flowBasePos, 3))
  flowGeo.setAttribute('aParam', new THREE.BufferAttribute(flowParams, 3))

  const flowUniforms = {
    uTime: { value: 0 },
    uBias: { value: 0 },
    uTheme: { value: 0 },
  }
  const flowMat = new THREE.ShaderMaterial({
    uniforms: flowUniforms,
    transparent: true,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
    vertexShader: /* glsl */ `
      attribute vec3 aParam;
      uniform float uTime;
      uniform float uBias;
      varying float vXNorm;
      varying float vSeed;

      void main() {
        float ribbon = aParam.x;
        float u = aParam.y;
        float seed = aParam.z;

        // Smooth GPU harmonic silk wave sweep across 56m horizon
        vec3 p = position;
        float wave = sin(u * 9.0 - uTime * (0.42 + ribbon * 0.04) + ribbon * 1.3);
        float swell = cos(u * 5.0 + uTime * 0.28 - seed * 6.2831);
        p.y += wave * (0.55 + seed * 0.35);
        p.z += swell * 0.85;
        p.x += sin(uTime * 0.22 + seed * 6.2831) * 0.35;

        vXNorm = clamp(p.x / 24.0, -1.0, 1.0);
        vSeed = seed;

        vec4 mvPosition = modelViewMatrix * vec4(p, 1.0);
        float depthSize = 32.0 / max(1.0, -mvPosition.z);
        gl_PointSize = clamp(depthSize * (0.8 + seed * 0.72), 2.0, 5.6);
        gl_Position = projectionMatrix * mvPosition;
      }
    `,
    fragmentShader: /* glsl */ `
      uniform float uBias;
      uniform float uTheme;
      varying float vXNorm;
      varying float vSeed;

      void main() {
        vec2 c = gl_PointCoord - 0.5;
        float d2 = dot(c, c);
        if (d2 > 0.25) discard;
        float mask = smoothstep(0.25, 0.02, d2);

        vec3 cyan = vec3(0.22, 0.91, 0.84);
        vec3 platinum = vec3(0.88, 0.92, 0.98);
        vec3 champagne = vec3(0.94, 0.76, 0.48);
        vec3 lavender = vec3(0.76, 0.68, 0.98);

        float t = clamp(vXNorm * 0.5 + 0.5 + uBias * 0.16, 0.0, 1.0);
        vec3 col = t < 0.5
          ? mix(cyan, platinum, t * 2.0)
          : mix(platinum, mix(champagne, lavender, step(0.62, vSeed)), (t - 0.5) * 2.0);

        float alpha = mask * mix(0.82, 0.34, uTheme) * (0.55 + vSeed * 0.45);
        gl_FragColor = vec4(col, alpha);
      }
    `,
  })
  const flowPoints = new THREE.Points(flowGeo, flowMat)
  group.add(flowPoints)
  disposables.push(flowGeo, flowMat)

  const update = (elapsed: number, _dt: number, hoverBias: number, themeT: number) => {
    eclipseUniforms.uTime.value = elapsed
    eclipseUniforms.uBias.value = hoverBias
    eclipseUniforms.uTheme.value = themeT

    flowUniforms.uTime.value = elapsed
    flowUniforms.uBias.value = hoverBias
    flowUniforms.uTheme.value = themeT

    ring1.rotation.z = 0.14 + elapsed * 0.045
    ring1.rotation.y = 0.22 + hoverBias * 0.14
    ring2.rotation.x = -0.46 + Math.sin(elapsed * 0.18) * 0.06
    ring2.rotation.z = -0.22 - elapsed * 0.032
    ring3.rotation.y = -0.38 - elapsed * 0.025 + hoverBias * 0.1
  }

  return {
    group,
    update,
    dispose: () => {
      disposables.forEach((d) => d.dispose())
    },
  }
}
