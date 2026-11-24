import * as THREE from 'three'

export interface AnimatedScreenTexture {
  texture: THREE.CanvasTexture
  update: (elapsed: number, active?: boolean) => void
  dispose: () => void
}

function createCanvas(width: number, height: number): [HTMLCanvasElement, CanvasRenderingContext2D] {
  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const ctx = canvas.getContext('2d')
  if (!ctx) {
    throw new Error('2D canvas context unavailable')
  }
  return [canvas, ctx]
}

function heightBufferToNormalMap(
  heights: Float32Array,
  width: number,
  height: number,
  strength: number,
): THREE.DataTexture {
  const data = new Uint8Array(width * height * 4)
  for (let y = 0; y < height; y++) {
    const yPrev = (y - 1 + height) % height
    const yNext = (y + 1) % height
    for (let x = 0; x < width; x++) {
      const xPrev = (x - 1 + width) % width
      const xNext = (x + 1) % width

      const dx = (heights[y * width + xNext] - heights[y * width + xPrev]) * strength
      const dy = (heights[yNext * width + x] - heights[yPrev * width + x]) * strength

      let nx = -dx
      let ny = -dy
      let nz = 1.0
      const len = Math.hypot(nx, ny, nz) || 1
      nx /= len
      ny /= len
      nz /= len

      const idx = (y * width + x) * 4
      data[idx] = Math.round((nx * 0.5 + 0.5) * 255)
      data[idx + 1] = Math.round((ny * 0.5 + 0.5) * 255)
      data[idx + 2] = Math.round((nz * 0.5 + 0.5) * 255)
      data[idx + 3] = 255
    }
  }
  const tex = new THREE.DataTexture(data, width, height, THREE.RGBAFormat)
  tex.wrapS = THREE.RepeatWrapping
  tex.wrapT = THREE.RepeatWrapping
  tex.needsUpdate = true
  return tex
}

/**
 * Generates procedural brushed titanium roughness + tangent normal maps
 * so studio HDR softboxes produce crisp anisotropic metallic highlights.
 */
export function createBrushedMetalMaps(): {
  roughnessMap: THREE.CanvasTexture
  normalMap: THREE.DataTexture
  dispose: () => void
} {
  const size = 512
  const [canvas, ctx] = createCanvas(size, size)
  const heights = new Float32Array(size * size)

  ctx.fillStyle = '#565656'
  ctx.fillRect(0, 0, size, size)

  let seed = 1337
  const rand = () => {
    seed = (seed * 16807) % 2147483647
    return (seed - 1) / 2147483646
  }

  for (let y = 0; y < size; y++) {
    const base = 0.34 + rand() * 0.24
    const shade = Math.round(base * 255)
    ctx.fillStyle = `rgb(${shade},${shade},${shade})`
    ctx.fillRect(0, y, size, 1)

    const streakCount = 4 + Math.floor(rand() * 5)
    for (let s = 0; s < streakCount; s++) {
      const sx = Math.floor(rand() * size)
      const sw = 28 + Math.floor(rand() * 150)
      const delta = (rand() - 0.5) * 0.18
      const v = Math.max(0.16, Math.min(0.84, base + delta))
      const sv = Math.round(v * 255)
      ctx.fillStyle = `rgba(${sv},${sv},${sv},0.55)`
      ctx.fillRect(sx, y, sw, 1)
      if (sx + sw > size) {
        ctx.fillRect(0, y, sx + sw - size, 1)
      }
    }
  }

  const img = ctx.getImageData(0, 0, size, size).data
  for (let i = 0; i < size * size; i++) {
    heights[i] = img[i * 4] / 255
  }

  const roughnessMap = new THREE.CanvasTexture(canvas)
  roughnessMap.wrapS = THREE.RepeatWrapping
  roughnessMap.wrapT = THREE.RepeatWrapping
  roughnessMap.repeat.set(2, 2)

  const normalMap = heightBufferToNormalMap(heights, size, size, 1.45)
  normalMap.repeat.set(2, 2)

  return {
    roughnessMap,
    normalMap,
    dispose: () => {
      roughnessMap.dispose()
      normalMap.dispose()
    },
  }
}

/**
 * Precision machined cinema lens gear knurling normal map.
 */
export function createKnurledRingNormalMap(): THREE.DataTexture {
  const width = 512
  const height = 128
  const heights = new Float32Array(width * height)
  const ridges = 76
  for (let y = 0; y < height; y++) {
    const edgeFade = Math.sin((y / (height - 1)) * Math.PI)
    const bevel = Math.min(1, edgeFade * 3.4)
    for (let x = 0; x < width; x++) {
      const phase = ((x / width) * ridges) % 1
      const ridge = phase < 0.44 ? phase / 0.44 : phase < 0.56 ? 1 : Math.max(0, 1 - (phase - 0.56) / 0.44)
      heights[y * width + x] = ridge * bevel
    }
  }
  const normalMap = heightBufferToNormalMap(heights, width, height, 3.4)
  normalMap.repeat.set(2, 1)
  return normalMap
}

/**
 * Poured dark architectural basalt & damp honed concrete floor maps
 * with macro stone slab seams, micro-pitted roughness variation, and subtle sheen.
 */
export function createArchitecturalConcreteMaps(): {
  roughnessMap: THREE.CanvasTexture
  normalMap: THREE.DataTexture
  dispose: () => void
} {
  const size = 512
  const [canvas, ctx] = createCanvas(size, size)
  const heights = new Float32Array(size * size)

  // Base mid-low roughness for honed basalt concrete
  ctx.fillStyle = '#4c4c4c'
  ctx.fillRect(0, 0, size, size)

  let seed = 4242
  const rand = () => {
    seed = (seed * 48271) % 2147483647
    return (seed - 1) / 2147483646
  }

  // Broad damp/honed polish clouds across floor slabs
  for (let i = 0; i < 65; i++) {
    const cx = rand() * size
    const cy = rand() * size
    const rad = 28 + rand() * 88
    const g = ctx.createRadialGradient(cx, cy, 0, cx, cy, rad)
    const bright = rand() > 0.48
    const lum = bright ? 42 : 118 // Darker = glossier wet sheen, lighter = matte basalt
    const alpha = 0.08 + rand() * 0.1
    g.addColorStop(0, `rgba(${lum},${lum},${lum},${alpha})`)
    g.addColorStop(1, `rgba(${lum},${lum},${lum},0)`)
    ctx.fillStyle = g
    ctx.fillRect(cx - rad, cy - rad, rad * 2, rad * 2)
  }

  // Fine mineral pitting & aggregate speckle
  for (let i = 0; i < 5200; i++) {
    const x = rand() * size
    const y = rand() * size
    const r = 0.8 + rand() * 5.2
    const lum = rand() > 0.55 ? 165 : 28
    const alpha = 0.04 + rand() * 0.08
    ctx.fillStyle = `rgba(${lum},${lum},${lum},${alpha})`
    ctx.beginPath()
    ctx.arc(x, y, r, 0, Math.PI * 2)
    ctx.fill()
  }

  // Refined architectural stone expansion joints (2x2 large slabs per repeat)
  ctx.strokeStyle = 'rgba(225, 225, 225, 0.24)'
  ctx.lineWidth = 2
  ctx.strokeRect(1, 1, size - 2, size - 2)
  ctx.beginPath()
  ctx.moveTo(size / 2, 0)
  ctx.lineTo(size / 2, size)
  ctx.moveTo(0, size / 2)
  ctx.lineTo(size, size / 2)
  ctx.stroke()

  ctx.strokeStyle = 'rgba(18, 18, 18, 0.35)'
  ctx.lineWidth = 1
  ctx.beginPath()
  ctx.moveTo(size / 2 + 2, 0)
  ctx.lineTo(size / 2 + 2, size)
  ctx.moveTo(0, size / 2 + 2)
  ctx.lineTo(size, size / 2 + 2)
  ctx.stroke()

  const img = ctx.getImageData(0, 0, size, size).data
  for (let i = 0; i < size * size; i++) {
    heights[i] = img[i * 4] / 255
  }

  const roughnessMap = new THREE.CanvasTexture(canvas)
  roughnessMap.wrapS = THREE.RepeatWrapping
  roughnessMap.wrapT = THREE.RepeatWrapping
  roughnessMap.repeat.set(12, 10)

  const normalMap = heightBufferToNormalMap(heights, size, size, 0.85)
  normalMap.repeat.set(12, 10)

  return {
    roughnessMap,
    normalMap,
    dispose: () => {
      roughnessMap.dispose()
      normalMap.dispose()
    },
  }
}

/**
 * Baked soft ambient-occlusion contact shadow decal to physically ground objects on floors/decks.
 */
export function createContactShadowTexture(): THREE.CanvasTexture {
  const size = 256
  const [canvas, ctx] = createCanvas(size, size)
  const half = size / 2
  const grad = ctx.createRadialGradient(half, half, size * 0.04, half, half, half)
  grad.addColorStop(0, 'rgba(2, 5, 12, 0.78)')
  grad.addColorStop(0.32, 'rgba(2, 5, 12, 0.62)')
  grad.addColorStop(0.66, 'rgba(2, 5, 12, 0.24)')
  grad.addColorStop(1, 'rgba(2, 5, 12, 0)')
  ctx.fillStyle = grad
  ctx.fillRect(0, 0, size, size)

  const tex = new THREE.CanvasTexture(canvas)
  tex.colorSpace = THREE.SRGBColorSpace
  return tex
}

/**
 * Smooth circular bokeh / soft glow sprite texture for dust particles and floor bounce pools.
 */
export function createSoftGlowTexture(): THREE.CanvasTexture {
  const size = 128
  const [canvas, ctx] = createCanvas(size, size)
  const grad = ctx.createRadialGradient(size / 2, size / 2, 0, size / 2, size / 2, size / 2)
  grad.addColorStop(0, 'rgba(255,255,255,1)')
  grad.addColorStop(0.24, 'rgba(255,255,255,0.78)')
  grad.addColorStop(0.58, 'rgba(255,255,255,0.16)')
  grad.addColorStop(1, 'rgba(255,255,255,0)')
  ctx.fillStyle = grad
  ctx.fillRect(0, 0, size, size)
  const tex = new THREE.CanvasTexture(canvas)
  tex.colorSpace = THREE.SRGBColorSpace
  return tex
}

/**
 * Razor-thin horizontal anamorphic cinema lens flare streak texture.
 */
export function createAnamorphicFlareTexture(): THREE.CanvasTexture {
  const width = 512
  const height = 128
  const [canvas, ctx] = createCanvas(width, height)

  ctx.clearRect(0, 0, width, height)

  const hGrad = ctx.createLinearGradient(0, 0, width, 0)
  hGrad.addColorStop(0, 'rgba(255,255,255,0)')
  hGrad.addColorStop(0.18, 'rgba(94,234,212,0.18)')
  hGrad.addColorStop(0.45, 'rgba(251,191,36,0.72)')
  hGrad.addColorStop(0.5, 'rgba(255,255,255,0.98)')
  hGrad.addColorStop(0.55, 'rgba(251,191,36,0.72)')
  hGrad.addColorStop(0.82, 'rgba(167,139,250,0.18)')
  hGrad.addColorStop(1, 'rgba(255,255,255,0)')

  ctx.save()
  ctx.translate(0, height / 2)
  ctx.scale(1, 0.11)
  ctx.fillStyle = hGrad
  ctx.beginPath()
  ctx.arc(width / 2, 0, width * 0.48, 0, Math.PI * 2)
  ctx.fill()
  ctx.restore()

  const cGrad = ctx.createRadialGradient(width / 2, height / 2, 0, width / 2, height / 2, height * 0.42)
  cGrad.addColorStop(0, 'rgba(255,255,255,0.92)')
  cGrad.addColorStop(0.32, 'rgba(251,191,36,0.32)')
  cGrad.addColorStop(1, 'rgba(251,191,36,0)')
  ctx.fillStyle = cGrad
  ctx.fillRect(0, 0, width, height)

  const tex = new THREE.CanvasTexture(canvas)
  tex.colorSpace = THREE.SRGBColorSpace
  return tex
}

/**
 * Dark etched-glass architectural cornice fascia texture (zero shadowBlur).
 */
export function createArchLabelTexture(text: string, accentHex: string): THREE.CanvasTexture {
  const width = 512
  const height = 72
  const [canvas, ctx] = createCanvas(width, height)

  const bg = ctx.createLinearGradient(0, 0, width, height)
  bg.addColorStop(0, 'rgba(8, 13, 24, 0.96)')
  bg.addColorStop(0.5, 'rgba(14, 22, 38, 0.98)')
  bg.addColorStop(1, 'rgba(8, 13, 24, 0.96)')
  ctx.fillStyle = bg
  ctx.fillRect(0, 0, width, height)

  // Hairline brushed-metal frame border
  ctx.strokeStyle = 'rgba(148, 163, 184, 0.24)'
  ctx.lineWidth = 1.5
  ctx.strokeRect(2, 2, width - 4, height - 4)

  // Subtle etched glow bar underneath
  const barGrad = ctx.createLinearGradient(28, 0, width - 28, 0)
  barGrad.addColorStop(0, 'rgba(255,255,255,0)')
  barGrad.addColorStop(0.5, accentHex)
  barGrad.addColorStop(1, 'rgba(255,255,255,0)')
  ctx.fillStyle = barGrad
  ctx.globalAlpha = 0.85
  ctx.fillRect(28, height - 6, width - 56, 2)
  ctx.globalAlpha = 0.22
  ctx.fillRect(28, height - 10, width - 56, 6)
  ctx.globalAlpha = 1

  ctx.fillStyle = '#e2e8f0'
  ctx.font = '600 20px -apple-system, BlinkMacSystemFont, "SF Pro Display", "Inter", sans-serif'
  ctx.textAlign = 'center'
  ctx.textBaseline = 'middle'
  ctx.fillText(text, width / 2, height / 2 - 1)

  const tex = new THREE.CanvasTexture(canvas)
  tex.colorSpace = THREE.SRGBColorSpace
  return tex
}

/**
 * Live 10Hz-throttled developer gateway OLED screen (zero shadowBlur).
 */
export function createLiveCodeScreen(): AnimatedScreenTexture {
  const width = 640
  const height = 384
  const [canvas, ctx] = createCanvas(width, height)
  const texture = new THREE.CanvasTexture(canvas)
  texture.colorSpace = THREE.SRGBColorSpace

  const apiLines: Array<Array<{ text: string; color: string }>> = [
    [
      { text: 'POST ', color: '#2dd4bf' },
      { text: '/v1/responses ', color: '#e2e8f0' },
      { text: '200 ', color: '#34d399' },
      { text: '38ms', color: '#38bdf8' },
    ],
    [
      { text: '  route: ', color: '#64748b' },
      { text: '"edge-tok-01" ', color: '#fbbf24' },
      { text: 'pool:', color: '#64748b' },
      { text: ' "multi-provider"', color: '#818cf8' },
    ],
    [
      { text: '  model: ', color: '#64748b' },
      { text: '"claude-opus-4" ', color: '#38bdf8' },
      { text: 'stream:', color: '#64748b' },
      { text: ' true', color: '#2dd4bf' },
    ],
    [
      { text: 'SSE  ', color: '#818cf8' },
      { text: 'delta.tokens ', color: '#cbd5e1' },
      { text: '142 t/s ', color: '#34d399' },
      { text: '[openai-compat]', color: '#2dd4bf' },
    ],
    [
      { text: 'MCP  ', color: '#fbbf24' },
      { text: 'tool_call: ', color: '#cbd5e1' },
      { text: '"storyboard.render" ', color: '#e2e8f0' },
      { text: 'ok', color: '#34d399' },
    ],
    [
      { text: 'POST ', color: '#2dd4bf' },
      { text: '/v1/messages ', color: '#e2e8f0' },
      { text: '200 ', color: '#34d399' },
      { text: '41ms', color: '#38bdf8' },
    ],
    [
      { text: 'CLI  ', color: '#38bdf8' },
      { text: 'gemini-2.5-pro ', color: '#818cf8' },
      { text: 'cache:', color: '#64748b' },
      { text: ' HIT', color: '#34d399' },
    ],
    [
      { text: 'SDK  ', color: '#2dd4bf' },
      { text: 'video.gen ', color: '#fbbf24' },
      { text: '4K · 24fps ', color: '#cbd5e1' },
      { text: 'dispatched', color: '#38bdf8' },
    ],
  ]

  let lastDraw = -1

  const draw = (elapsed: number, active = true) => {
    if (!active && lastDraw >= 0) return
    if (lastDraw >= 0 && elapsed - lastDraw < 1 / 10) return
    lastDraw = elapsed

    const bg = ctx.createLinearGradient(0, 0, width, height)
    bg.addColorStop(0, '#050a14')
    bg.addColorStop(1, '#091224')
    ctx.fillStyle = bg
    ctx.fillRect(0, 0, width, height)

    // Subtle matte grid
    ctx.strokeStyle = 'rgba(148, 163, 184, 0.055)'
    ctx.lineWidth = 1
    for (let x = 0; x < width; x += 32) {
      ctx.beginPath()
      ctx.moveTo(x, 0)
      ctx.lineTo(x, height)
      ctx.stroke()
    }
    for (let y = 0; y < height; y += 32) {
      ctx.beginPath()
      ctx.moveTo(0, y)
      ctx.lineTo(width, y)
      ctx.stroke()
    }

    // Top console chrome bar
    ctx.fillStyle = 'rgba(255,255,255,0.035)'
    ctx.fillRect(0, 0, width, 42)
    ctx.strokeStyle = 'rgba(45, 212, 191, 0.2)'
    ctx.beginPath()
    ctx.moveTo(0, 42)
    ctx.lineTo(width, 42)
    ctx.stroke()

    const dots = ['#ef4444', '#f59e0b', '#10b981']
    dots.forEach((c, i) => {
      ctx.fillStyle = c
      ctx.beginPath()
      ctx.arc(24 + i * 16, 21, 4.5, 0, Math.PI * 2)
      ctx.fill()
    })

    const protocols = [
      { label: 'OpenAI', accent: '#2dd4bf' },
      { label: 'Claude', accent: '#38bdf8' },
      { label: 'Gemini', accent: '#818cf8' },
    ]
    protocols.forEach((p, idx) => {
      const px = width - 256 + idx * 80
      const isLit = Math.floor(elapsed * 0.8) % 3 === idx
      ctx.fillStyle = isLit ? 'rgba(45, 212, 191, 0.16)' : 'rgba(255,255,255,0.04)'
      ctx.strokeStyle = isLit ? p.accent : 'rgba(148, 163, 184, 0.22)'
      ctx.lineWidth = 1
      ctx.beginPath()
      ctx.roundRect(px, 10, 72, 22, 11)
      ctx.fill()
      ctx.stroke()

      ctx.fillStyle = isLit ? '#f8fafc' : '#94a3b8'
      ctx.font = '600 10.5px ui-monospace, SFMono-Regular, Menlo, monospace'
      ctx.textBaseline = 'middle'
      ctx.fillText(p.label, px + 14, 21.5)
    })

    // Streaming API trace rows
    ctx.save()
    ctx.beginPath()
    ctx.rect(20, 52, width - 40, 212)
    ctx.clip()

    const rowH = 26
    const scrollPx = (elapsed * 18) % (apiLines.length * rowH)
    ctx.font = '500 12px ui-monospace, SFMono-Regular, Menlo, monospace'
    ctx.textBaseline = 'middle'

    for (let pass = 0; pass < 2; pass++) {
      apiLines.forEach((parts, rIdx) => {
        const y = 66 + rIdx * rowH - scrollPx + pass * apiLines.length * rowH
        if (y < 38 || y > 272) return

        ctx.fillStyle = 'rgba(100, 116, 139, 0.45)'
        ctx.fillText(String(10 + rIdx).padStart(2, '0'), 24, y)

        let x = 52
        parts.forEach((seg) => {
          ctx.fillStyle = seg.color
          ctx.fillText(seg.text, x, y)
          x += ctx.measureText(seg.text).width
        })
      })
    }

    const cursorA = Math.sin(elapsed * 5.0) > 0 ? 0.9 : 0.2
    ctx.fillStyle = `rgba(45, 212, 191, ${cursorA})`
    ctx.fillRect(52, 246, 8, 13)
    ctx.restore()

    // Bottom multi-channel latency oscilloscope panel
    ctx.fillStyle = 'rgba(10, 18, 34, 0.84)'
    ctx.strokeStyle = 'rgba(56, 189, 248, 0.22)'
    ctx.lineWidth = 1
    ctx.beginPath()
    ctx.roundRect(22, 278, width - 44, 86, 10)
    ctx.fill()
    ctx.stroke()

    const waveMidY = 324
    const drawWave = (ampScale: number, freq: number, speed: number, color: string, lw: number, alpha: number) => {
      ctx.beginPath()
      for (let x = 36; x <= width - 166; x += 6) {
        const t = (x - 36) / (width - 202)
        const env = Math.sin(t * Math.PI)
        const y =
          waveMidY +
          (Math.sin(t * freq - elapsed * speed) * 0.65 + Math.cos(t * freq * 1.9 + elapsed * 2.1) * 0.35) *
            ampScale *
            env
        if (x === 36) ctx.moveTo(x, y)
        else ctx.lineTo(x, y)
      }
      ctx.strokeStyle = color
      ctx.lineWidth = lw
      ctx.globalAlpha = alpha
      ctx.stroke()
    }

    drawWave(24, 13, 3.6, 'rgba(45, 212, 191, 0.45)', 5, 0.38)
    drawWave(24, 13, 3.6, '#2dd4bf', 2.2, 0.95)
    drawWave(17, 17, -2.9, '#818cf8', 1.6, 0.78)
    ctx.globalAlpha = 1

    // Equalizer throughput bars on right
    for (let b = 0; b < 10; b++) {
      const bh = 10 + Math.abs(Math.sin(elapsed * 3.8 + b * 0.6)) * 42
      const bx = width - 146 + b * 11
      ctx.fillStyle = b % 2 === 0 ? '#2dd4bf' : '#38bdf8'
      ctx.globalAlpha = 0.82
      ctx.fillRect(bx, 348 - bh, 6, bh)
    }
    ctx.globalAlpha = 1

    texture.needsUpdate = true
  }

  draw(0, true)

  return {
    texture,
    update: draw,
    dispose: () => texture.dispose(),
  }
}

/**
 * Live 10Hz-throttled 24fps NLE timeline monitor texture for Qimu Act 3 (zero shadowBlur).
 */
export function createLiveTimelineScreen(): AnimatedScreenTexture {
  const width = 512
  const height = 140
  const [canvas, ctx] = createCanvas(width, height)
  const texture = new THREE.CanvasTexture(canvas)
  texture.colorSpace = THREE.SRGBColorSpace

  let lastDraw = -1

  const draw = (elapsed: number, active = true) => {
    if (!active && lastDraw >= 0) return
    if (lastDraw >= 0 && elapsed - lastDraw < 1 / 10) return
    lastDraw = elapsed

    ctx.fillStyle = '#070b14'
    ctx.fillRect(0, 0, width, height)

    // Subtle top timecode bar + ticks
    ctx.strokeStyle = 'rgba(203, 213, 225, 0.2)'
    ctx.lineWidth = 1
    for (let x = 16; x < width - 16; x += 14) {
      const major = ((x - 16) / 14) % 4 === 0
      ctx.beginPath()
      ctx.moveTo(x, major ? 8 : 14)
      ctx.lineTo(x, 22)
      ctx.stroke()
    }

    const totalFrames = Math.floor(elapsed * 24)
    const ff = String(totalFrames % 24).padStart(2, '0')
    const ss = String((14 + Math.floor(totalFrames / 24)) % 60).padStart(2, '0')
    ctx.fillStyle = '#fbbf24'
    ctx.font = '600 10px ui-monospace, SFMono-Regular, Menlo, monospace'
    ctx.fillText(`00:01:${ss}:${ff}`, width - 82, 16)

    const tracks = [
      { y: 28, h: 25, color: '#fbbf24', clips: [[18, 98], [122, 92], [220, 138], [364, 128]] },
      { y: 58, h: 25, color: '#a78bfa', clips: [[32, 84], [142, 128], [282, 102], [392, 94]] },
      { y: 88, h: 34, color: '#2dd4bf', clips: [[18, 474]] },
    ]

    tracks.forEach((track, tIdx) => {
      ctx.fillStyle = 'rgba(255,255,255,0.03)'
      ctx.fillRect(14, track.y, width - 28, track.h)

      track.clips.forEach(([cx, cw], cIdx) => {
        ctx.fillStyle = track.color
        ctx.globalAlpha = 0.22 + (cIdx % 2) * 0.08
        ctx.beginPath()
        ctx.roundRect(cx, track.y + 2, cw, track.h - 4, 4)
        ctx.fill()

        ctx.globalAlpha = 0.72
        ctx.strokeStyle = track.color
        ctx.lineWidth = 1
        ctx.stroke()
        ctx.globalAlpha = 1

        if (tIdx === 2) {
          ctx.fillStyle = '#5eead4'
          for (let wx = cx + 8; wx < cx + cw - 8; wx += 6) {
            const wh = 4 + Math.abs(Math.sin(wx * 0.1 + elapsed * 2.8)) * 18
            ctx.fillRect(wx, track.y + track.h / 2 - wh / 2, 2.5, wh)
          }
        }
      })
    })

    // Crisp dual-stroke playhead (simulated bloom without shadowBlur)
    const playheadX = 24 + ((elapsed * 48) % (width - 48))
    ctx.strokeStyle = 'rgba(251, 191, 36, 0.28)'
    ctx.lineWidth = 6
    ctx.beginPath()
    ctx.moveTo(playheadX, 8)
    ctx.lineTo(playheadX, height - 6)
    ctx.stroke()

    ctx.strokeStyle = '#fbbf24'
    ctx.lineWidth = 2
    ctx.beginPath()
    ctx.moveTo(playheadX, 6)
    ctx.lineTo(playheadX, height - 6)
    ctx.stroke()

    ctx.fillStyle = '#fbbf24'
    ctx.beginPath()
    ctx.moveTo(playheadX - 5, 6)
    ctx.lineTo(playheadX + 5, 6)
    ctx.lineTo(playheadX, 14)
    ctx.closePath()
    ctx.fill()

    texture.needsUpdate = true
  }

  draw(0, true)

  return {
    texture,
    update: draw,
    dispose: () => texture.dispose(),
  }
}
