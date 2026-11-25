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

/**
 * Pre-baked 256x256 seamlessly tileable 3-channel FBM noise DataTexture
 * generated once at startup so sky shaders avoid expensive per-pixel loops.
 */
export function createSeamlessNoiseTexture(): THREE.DataTexture {
  const size = 256
  const data = new Uint8Array(size * size * 4)

  const hash2 = (ix: number, iy: number, period: number, seed: number): number => {
    const x = ((ix % period) + period) % period
    const y = ((iy % period) + period) % period
    const n = Math.sin(x * 127.1 + y * 311.7 + seed * 74.7) * 43758.5453
    return n - Math.floor(n)
  }

  const periodicNoise = (u: number, v: number, period: number, seed: number): number => {
    const px = u * period
    const py = v * period
    const ix = Math.floor(px)
    const iy = Math.floor(py)
    const fx = px - ix
    const fy = py - iy
    const sx = fx * fx * (3 - 2 * fx)
    const sy = fy * fy * (3 - 2 * fy)

    const n00 = hash2(ix, iy, period, seed)
    const n10 = hash2(ix + 1, iy, period, seed)
    const n01 = hash2(ix, iy + 1, period, seed)
    const n11 = hash2(ix + 1, iy + 1, period, seed)

    const nx0 = n00 + (n10 - n00) * sx
    const nx1 = n01 + (n11 - n01) * sx
    return nx0 + (nx1 - nx0) * sy
  }

  const fbmChannel = (u: number, v: number, basePeriod: number, seed: number): number => {
    let val = 0
    let amp = 0.52
    let p = basePeriod
    for (let oct = 0; oct < 4; oct++) {
      val += amp * periodicNoise(u, v, p, seed + oct * 19.3)
      p *= 2
      amp *= 0.5
    }
    return Math.max(0, Math.min(255, Math.round(val * 255)))
  }

  for (let y = 0; y < size; y++) {
    const v = y / size
    for (let x = 0; x < size; x++) {
      const u = x / size
      const idx = (y * size + x) * 4
      data[idx] = fbmChannel(u, v, 4, 1.1) // R: broad volumetric cloud strata
      data[idx + 1] = fbmChannel(u, v, 8, 5.7) // G: mid-scale silky wisps
      data[idx + 2] = fbmChannel(u, v, 16, 11.3) // B: fine detail ripples
      data[idx + 3] = 255
    }
  }

  const tex = new THREE.DataTexture(data, size, size, THREE.RGBAFormat)
  tex.wrapS = THREE.RepeatWrapping
  tex.wrapT = THREE.RepeatWrapping
  tex.magFilter = THREE.LinearFilter
  tex.minFilter = THREE.LinearFilter
  tex.needsUpdate = true
  return tex
}

/**
 * Smooth circular bokeh / soft glow sprite texture for GPU particles and ambient halos.
 */
export function createSoftGlowTexture(): THREE.CanvasTexture {
  const size = 128
  const [canvas, ctx] = createCanvas(size, size)
  const grad = ctx.createRadialGradient(size / 2, size / 2, 0, size / 2, size / 2, size / 2)
  grad.addColorStop(0, 'rgba(255,255,255,1)')
  grad.addColorStop(0.2, 'rgba(255,255,255,0.82)')
  grad.addColorStop(0.54, 'rgba(255,255,255,0.16)')
  grad.addColorStop(1, 'rgba(255,255,255,0)')
  ctx.fillStyle = grad
  ctx.fillRect(0, 0, size, size)
  const tex = new THREE.CanvasTexture(canvas)
  tex.colorSpace = THREE.SRGBColorSpace
  return tex
}

/**
 * Razor-thin horizontal optical cinema lens streak texture.
 */
export function createAnamorphicFlareTexture(): THREE.CanvasTexture {
  const width = 512
  const height = 128
  const [canvas, ctx] = createCanvas(width, height)

  ctx.clearRect(0, 0, width, height)

  const hGrad = ctx.createLinearGradient(0, 0, width, 0)
  hGrad.addColorStop(0, 'rgba(255,255,255,0)')
  hGrad.addColorStop(0.18, 'rgba(56,232,213,0.16)')
  hGrad.addColorStop(0.44, 'rgba(240,194,123,0.72)')
  hGrad.addColorStop(0.5, 'rgba(255,255,255,0.98)')
  hGrad.addColorStop(0.56, 'rgba(240,194,123,0.72)')
  hGrad.addColorStop(0.82, 'rgba(167,139,250,0.16)')
  hGrad.addColorStop(1, 'rgba(255,255,255,0)')

  ctx.save()
  ctx.translate(0, height / 2)
  ctx.scale(1, 0.08)
  ctx.fillStyle = hGrad
  ctx.beginPath()
  ctx.arc(width / 2, 0, width * 0.48, 0, Math.PI * 2)
  ctx.fill()
  ctx.restore()

  const tex = new THREE.CanvasTexture(canvas)
  tex.colorSpace = THREE.SRGBColorSpace
  return tex
}

/**
 * Live 10Hz-throttled Editorial Smoked-Glass Gateway OLED Display (zero shadowBlur, zero mock traffic-light dots).
 */
export function createLiveCodeScreen(): AnimatedScreenTexture {
  const width = 640
  const height = 384
  const [canvas, ctx] = createCanvas(width, height)
  const texture = new THREE.CanvasTexture(canvas)
  texture.colorSpace = THREE.SRGBColorSpace

  const apiLines: Array<Array<{ text: string; color: string }>> = [
    [
      { text: 'POST ', color: '#8fb5a8' },
      { text: '/v1/responses  ', color: '#f5f7fb' },
      { text: '200 ', color: '#7fa88b' },
      { text: '38ms · 148 tok/s', color: '#a3c3ba' },
    ],
    [
      { text: '  model: ', color: '#6b716b' },
      { text: '"claude-opus-4"  ', color: '#7ba79b' },
      { text: 'route: ', color: '#6b716b' },
      { text: '"edge-mesh"', color: '#c9a876' },
    ],
    [
      { text: 'POST ', color: '#8fb5a8' },
      { text: '/v1/chat/completions  ', color: '#f5f7fb' },
      { text: 'gpt-5  ', color: '#a3c3ba' },
      { text: 'stream: true', color: '#7fa88b' },
    ],
    [
      { text: 'MCP  ', color: '#c9a876' },
      { text: 'gemini-2.5-pro  ', color: '#b9b2a3' },
      { text: 'tool: ', color: '#6b716b' },
      { text: '"qimu.storyboard"', color: '#f5f7fb' },
    ],
    [
      { text: 'POST ', color: '#8fb5a8' },
      { text: '/v1/messages  ', color: '#f5f7fb' },
      { text: '200 ', color: '#7fa88b' },
      { text: 'cache: HIT · 29ms', color: '#8fb5a8' },
    ],
    [
      { text: 'SDK  ', color: '#a3c3ba' },
      { text: 'media.render  ', color: '#c9a876' },
      { text: '4K · 24fps  ', color: '#cbd5e1' },
      { text: 'dispatched', color: '#7ba79b' },
    ],
    [
      { text: 'CLI  ', color: '#7ba79b' },
      { text: 'aicode --provider ', color: '#cbd5e1' },
      { text: 'auto  ', color: '#8fb5a8' },
      { text: '99.98% SLA', color: '#7fa88b' },
    ],
  ]

  let lastDraw = -1

  const draw = (elapsed: number, active = true) => {
    if (!active && lastDraw >= 0) return
    if (lastDraw >= 0 && elapsed - lastDraw < 1 / 6) return
    lastDraw = elapsed

    ctx.clearRect(0, 0, width, height)

    // Translucent obsidian smoked-glass field
    const bg = ctx.createLinearGradient(0, 0, width, height)
    bg.addColorStop(0, 'rgba(5, 9, 18, 0.94)')
    bg.addColorStop(0.5, 'rgba(8, 15, 30, 0.92)')
    bg.addColorStop(1, 'rgba(4, 8, 16, 0.95)')
    ctx.fillStyle = bg
    ctx.fillRect(0, 0, width, height)

    // Ultra-subtle hairline architectural grid
    ctx.strokeStyle = 'rgba(148, 163, 184, 0.045)'
    ctx.lineWidth = 1
    for (let x = 40; x < width; x += 40) {
      ctx.beginPath()
      ctx.moveTo(x, 44)
      ctx.lineTo(x, height - 16)
      ctx.stroke()
    }
    for (let y = 76; y < height - 16; y += 36) {
      ctx.beginPath()
      ctx.moveTo(16, y)
      ctx.lineTo(width - 16, y)
      ctx.stroke()
    }

    // Minimal Swiss editorial header bar (NO window traffic-light dots)
    ctx.fillStyle = 'rgba(255, 255, 255, 0.025)'
    ctx.fillRect(0, 0, width, 44)
    ctx.strokeStyle = 'rgba(56, 232, 213, 0.24)'
    ctx.lineWidth = 1
    ctx.beginPath()
    ctx.moveTo(0, 44)
    ctx.lineTo(width, 44)
    ctx.stroke()

    ctx.fillStyle = '#8fb5a8'
    ctx.beginPath()
    ctx.arc(24, 22, 3.5, 0, Math.PI * 2)
    ctx.fill()

    ctx.fillStyle = '#9a9c94'
    ctx.font = '600 11px ui-monospace, SFMono-Regular, Menlo, monospace'
    ctx.textAlign = 'left'
    ctx.textBaseline = 'middle'
    ctx.fillText('AICODE GATEWAY // LIVE STREAM', 38, 22.5)

    // Right active protocol pills
    const protocols = [
      { label: 'OpenAI', accent: '#8fb5a8' },
      { label: 'Claude', accent: '#7ba79b' },
      { label: 'Gemini', accent: '#a3c3ba' },
    ]
    protocols.forEach((p, idx) => {
      const px = width - 252 + idx * 78
      const isLit = Math.floor(elapsed * 0.7) % 3 === idx
      ctx.fillStyle = isLit ? 'rgba(56, 232, 213, 0.14)' : 'rgba(255, 255, 255, 0.03)'
      ctx.strokeStyle = isLit ? p.accent : 'rgba(148, 163, 184, 0.18)'
      ctx.lineWidth = 1
      ctx.beginPath()
      ctx.roundRect(px, 11, 70, 22, 11)
      ctx.fill()
      ctx.stroke()

      ctx.fillStyle = isLit ? '#f5f7fb' : '#9a9c94'
      ctx.font = '600 10.5px ui-monospace, SFMono-Regular, Menlo, monospace'
      ctx.textAlign = 'center'
      ctx.fillText(p.label, px + 35, 22.5)
    })

    // Streaming trace rows
    ctx.save()
    ctx.beginPath()
    ctx.rect(18, 52, width - 36, 212)
    ctx.clip()

    const rowH = 29
    const scrollPx = (elapsed * 16) % (apiLines.length * rowH)
    ctx.textAlign = 'left'
    ctx.font = '500 12.5px ui-monospace, SFMono-Regular, Menlo, monospace'

    for (let pass = 0; pass < 2; pass++) {
      apiLines.forEach((parts, rIdx) => {
        const y = 68 + rIdx * rowH - scrollPx + pass * apiLines.length * rowH
        if (y < 42 || y > 272) return

        ctx.fillStyle = 'rgba(100, 116, 139, 0.42)'
        ctx.fillText(String(1 + rIdx).padStart(2, '0'), 24, y)

        let x = 54
        parts.forEach((seg) => {
          ctx.fillStyle = seg.color
          ctx.fillText(seg.text, x, y)
          x += ctx.measureText(seg.text).width
        })
      })
    }

    const cursorA = Math.sin(elapsed * 4.5) > 0 ? 0.92 : 0.2
    ctx.fillStyle = `rgba(56, 232, 213, ${cursorA})`
    ctx.fillRect(54, 246, 7, 13)
    ctx.restore()

    // Bottom oscilloscope telemetry deck
    ctx.fillStyle = 'rgba(6, 12, 24, 0.78)'
    ctx.strokeStyle = 'rgba(56, 232, 213, 0.16)'
    ctx.lineWidth = 1
    ctx.beginPath()
    ctx.roundRect(22, 276, width - 44, 86, 10)
    ctx.fill()
    ctx.stroke()

    const waveMidY = 320
    const drawWave = (amp: number, freq: number, speed: number, color: string, lw: number, alpha: number) => {
      ctx.beginPath()
      for (let x = 36; x <= width - 160; x += 6) {
        const t = (x - 36) / (width - 196)
        const env = Math.sin(t * Math.PI)
        const y =
          waveMidY +
          (Math.sin(t * freq - elapsed * speed) * 0.65 + Math.cos(t * freq * 1.85 + elapsed * 2.0) * 0.35) *
            amp *
            env
        if (x === 36) ctx.moveTo(x, y)
        else ctx.lineTo(x, y)
      }
      ctx.strokeStyle = color
      ctx.lineWidth = lw
      ctx.globalAlpha = alpha
      ctx.stroke()
    }

    drawWave(24, 12, 3.4, 'rgba(56, 232, 213, 0.36)', 5, 0.4)
    drawWave(24, 12, 3.4, '#8fb5a8', 2, 0.96)
    drawWave(17, 16, -2.6, '#6d9187', 1.5, 0.8)
    ctx.globalAlpha = 1

    for (let b = 0; b < 10; b++) {
      const bh = 10 + Math.abs(Math.sin(elapsed * 3.2 + b * 0.58)) * 42
      const bx = width - 142 + b * 11
      ctx.fillStyle = b % 2 === 0 ? '#8fb5a8' : '#7ba79b'
      ctx.globalAlpha = 0.8
      ctx.fillRect(bx, 344 - bh, 5.5, bh)
    }
    ctx.globalAlpha = 1

    // Outer glass edge hairline
    ctx.strokeStyle = 'rgba(245, 247, 251, 0.14)'
    ctx.lineWidth = 1.5
    ctx.strokeRect(0.75, 0.75, width - 1.5, height - 1.5)

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
 * Swiss-editorial smoked-glass canvas for Multi-Model Intelligent Routing (Act 2 left panel).
 */
export function createRoutingMatrixTexture(): THREE.CanvasTexture {
  const width = 512
  const height = 420
  const [canvas, ctx] = createCanvas(width, height)

  const bg = ctx.createLinearGradient(0, 0, width, height)
  bg.addColorStop(0, 'rgba(6, 11, 22, 0.92)')
  bg.addColorStop(1, 'rgba(4, 7, 15, 0.96)')
  ctx.fillStyle = bg
  ctx.fillRect(0, 0, width, height)

  ctx.strokeStyle = 'rgba(56, 232, 213, 0.16)'
  ctx.lineWidth = 1.5
  ctx.strokeRect(1, 1, width - 2, height - 2)

  ctx.fillStyle = '#9a9c94'
  ctx.font = '600 12px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.fillText('INTELLIGENT ROUTING // 99.98% SLA', 28, 36)

  ctx.strokeStyle = 'rgba(148, 163, 184, 0.14)'
  ctx.beginPath()
  ctx.moveTo(28, 54)
  ctx.lineTo(width - 28, 54)
  ctx.stroke()

  const models = [
    { name: 'OpenAI · GPT-5', latency: '34ms', pct: 0.92, color: '#8fb5a8' },
    { name: 'Claude · Opus 4', latency: '38ms', pct: 0.96, color: '#7ba79b' },
    { name: 'Gemini · 2.5 Pro', latency: '31ms', pct: 0.89, color: '#a3c3ba' },
    { name: 'DeepSeek · R1', latency: '27ms', pct: 0.84, color: '#c9a876' },
  ]

  models.forEach((m, i) => {
    const y = 96 + i * 56
    ctx.fillStyle = '#f5f7fb'
    ctx.font = '600 15px -apple-system, BlinkMacSystemFont, "SF Pro Display", sans-serif'
    ctx.textAlign = 'left'
    ctx.fillText(m.name, 28, y)

    ctx.fillStyle = m.color
    ctx.font = '600 13px ui-monospace, SFMono-Regular, Menlo, monospace'
    ctx.textAlign = 'right'
    ctx.fillText(m.latency, width - 28, y)

    ctx.fillStyle = 'rgba(148, 163, 184, 0.12)'
    ctx.beginPath()
    ctx.roundRect(28, y + 14, width - 56, 7, 3.5)
    ctx.fill()

    ctx.fillStyle = m.color
    ctx.beginPath()
    ctx.roundRect(28, y + 14, (width - 56) * m.pct, 7, 3.5)
    ctx.fill()
  })

  // Lower mesh routing curves
  ctx.textAlign = 'left'
  ctx.fillStyle = 'rgba(56, 232, 213, 0.08)'
  ctx.beginPath()
  ctx.roundRect(28, 326, width - 56, 68, 10)
  ctx.fill()

  const curveCols = ['#8fb5a8', '#7ba79b', '#a3c3ba']
  curveCols.forEach((c, idx) => {
    ctx.strokeStyle = c
    ctx.lineWidth = 2
    ctx.globalAlpha = 0.82
    ctx.beginPath()
    ctx.moveTo(48, 360 + (idx - 1) * 14)
    ctx.bezierCurveTo(180, 336 + idx * 12, 320, 386 - idx * 10, width - 48, 360)
    ctx.stroke()
  })
  ctx.globalAlpha = 1

  const tex = new THREE.CanvasTexture(canvas)
  tex.colorSpace = THREE.SRGBColorSpace
  return tex
}

/**
 * Swiss-editorial smoked-glass canvas for Unified Protocol & Multimodal Hub (Act 2 right panel).
 */
export function createProtocolHubTexture(): THREE.CanvasTexture {
  const width = 512
  const height = 420
  const [canvas, ctx] = createCanvas(width, height)

  const bg = ctx.createLinearGradient(0, 0, width, height)
  bg.addColorStop(0, 'rgba(6, 11, 22, 0.92)')
  bg.addColorStop(1, 'rgba(4, 7, 15, 0.96)')
  ctx.fillStyle = bg
  ctx.fillRect(0, 0, width, height)

  ctx.strokeStyle = 'rgba(165, 180, 252, 0.18)'
  ctx.lineWidth = 1.5
  ctx.strokeRect(1, 1, width - 2, height - 2)

  ctx.fillStyle = '#9a9c94'
  ctx.font = '600 12px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.fillText('UNIFIED PROTOCOL & MULTIMODAL HUB', 28, 36)

  ctx.strokeStyle = 'rgba(148, 163, 184, 0.14)'
  ctx.beginPath()
  ctx.moveTo(28, 54)
  ctx.lineTo(width - 28, 54)
  ctx.stroke()

  // 2x2 Protocol chips
  const chips = [
    { title: 'API', sub: 'OpenAI Compat', accent: '#8fb5a8' },
    { title: 'CLI', sub: 'Terminal Native', accent: '#7ba79b' },
    { title: 'SDK', sub: 'Streaming Typed', accent: '#a3c3ba' },
    { title: 'MCP', sub: 'Agent Tooling', accent: '#c9a876' },
  ]
  chips.forEach((chip, i) => {
    const col = i % 2
    const row = Math.floor(i / 2)
    const cx = 28 + col * 234
    const cy = 76 + row * 86
    ctx.fillStyle = 'rgba(255, 255, 255, 0.035)'
    ctx.strokeStyle = 'rgba(148, 163, 184, 0.18)'
    ctx.lineWidth = 1
    ctx.beginPath()
    ctx.roundRect(cx, cy, 220, 70, 12)
    ctx.fill()
    ctx.stroke()

    ctx.fillStyle = chip.accent
    ctx.fillRect(cx + 16, cy + 20, 4, 30)

    ctx.fillStyle = '#f5f7fb'
    ctx.font = '700 18px -apple-system, BlinkMacSystemFont, "SF Pro Display", sans-serif'
    ctx.fillText(chip.title, cx + 32, cy + 32)

    ctx.fillStyle = '#9a9c94'
    ctx.font = '500 12.5px ui-monospace, SFMono-Regular, Menlo, monospace'
    ctx.fillText(chip.sub, cx + 32, cy + 53)
  })

  // Lower Multimodal signal bars: Text / Image / Video / Audio
  const mods = [
    { name: 'TEXT', color: '#8fb5a8' },
    { name: 'IMAGE', color: '#7ba79b' },
    { name: 'VIDEO', color: '#c9a876' },
    { name: 'AUDIO', color: '#b9b2a3' },
  ]
  mods.forEach((mod, i) => {
    const my = 272 + i * 32
    ctx.fillStyle = '#cbd5e1'
    ctx.font = '600 11px ui-monospace, SFMono-Regular, Menlo, monospace'
    ctx.fillText(mod.name, 28, my + 8)

    ctx.strokeStyle = mod.color
    ctx.lineWidth = 2
    ctx.beginPath()
    for (let x = 96; x <= width - 28; x += 6) {
      const t = (x - 96) / (width - 124)
      const amp = 7 * Math.sin(t * Math.PI)
      const y = my + 5 + Math.sin(t * (10 + i * 5) + i) * amp
      if (x === 96) ctx.moveTo(x, y)
      else ctx.lineTo(x, y)
    }
    ctx.stroke()
  })

  const tex = new THREE.CanvasTexture(canvas)
  tex.colorSpace = THREE.SRGBColorSpace
  return tex
}

/**
 * Live 10Hz-throttled Apple/DaVinci VisionOS-inspired translucent glass 24fps film timeline ribbon.
 */
export function createLiveTimelineScreen(): AnimatedScreenTexture {
  const width = 512
  const height = 128
  const [canvas, ctx] = createCanvas(width, height)
  const texture = new THREE.CanvasTexture(canvas)
  texture.colorSpace = THREE.SRGBColorSpace

  let lastDraw = -1

  const draw = (elapsed: number, active = true) => {
    if (!active && lastDraw >= 0) return
    if (lastDraw >= 0 && elapsed - lastDraw < 1 / 6) return
    lastDraw = elapsed

    ctx.fillStyle = 'rgba(5, 8, 16, 0.94)'
    ctx.fillRect(0, 0, width, height)

    ctx.strokeStyle = 'rgba(240, 194, 123, 0.22)'
    ctx.lineWidth = 1
    ctx.strokeRect(0.5, 0.5, width - 1, height - 1)

    // Ruler ticks
    ctx.strokeStyle = 'rgba(203, 213, 225, 0.18)'
    for (let x = 16; x < width - 16; x += 14) {
      const major = ((x - 16) / 14) % 4 === 0
      ctx.beginPath()
      ctx.moveTo(x, major ? 7 : 12)
      ctx.lineTo(x, 20)
      ctx.stroke()
    }

    const totalFrames = Math.floor(elapsed * 24)
    const ff = String(totalFrames % 24).padStart(2, '0')
    const ss = String((14 + Math.floor(totalFrames / 24)) % 60).padStart(2, '0')
    ctx.fillStyle = '#c9a876'
    ctx.font = '600 10px ui-monospace, SFMono-Regular, Menlo, monospace'
    ctx.fillText(`00:01:${ss}:${ff}`, width - 82, 15)

    const tracks = [
      { y: 26, h: 22, color: '#c9a876', clips: [[18, 98], [122, 92], [220, 138], [364, 128]] },
      { y: 54, h: 22, color: '#b9b2a3', clips: [[32, 84], [142, 128], [282, 102], [392, 94]] },
      { y: 82, h: 32, color: '#8fb5a8', clips: [[18, 474]] },
    ]

    tracks.forEach((track, tIdx) => {
      ctx.fillStyle = 'rgba(255,255,255,0.025)'
      ctx.fillRect(14, track.y, width - 28, track.h)

      track.clips.forEach(([cx, cw], cIdx) => {
        ctx.fillStyle = track.color
        ctx.globalAlpha = 0.2 + (cIdx % 2) * 0.08
        ctx.beginPath()
        ctx.roundRect(cx, track.y + 2, cw, track.h - 4, 4)
        ctx.fill()

        ctx.globalAlpha = 0.72
        ctx.strokeStyle = track.color
        ctx.lineWidth = 1
        ctx.stroke()
        ctx.globalAlpha = 1

        if (tIdx === 2) {
          ctx.fillStyle = '#a3c3ba'
          for (let wx = cx + 8; wx < cx + cw - 8; wx += 6) {
            const wh = 4 + Math.abs(Math.sin(wx * 0.1 + elapsed * 2.8)) * 18
            ctx.fillRect(wx, track.y + track.h / 2 - wh / 2, 2.2, wh)
          }
        }
      })
    })

    // Crisp champagne cinema playhead
    const playheadX = 24 + ((elapsed * 44) % (width - 48))
    ctx.strokeStyle = 'rgba(240, 194, 123, 0.26)'
    ctx.lineWidth = 5
    ctx.beginPath()
    ctx.moveTo(playheadX, 6)
    ctx.lineTo(playheadX, height - 6)
    ctx.stroke()

    ctx.strokeStyle = '#c9a876'
    ctx.lineWidth = 1.8
    ctx.beginPath()
    ctx.moveTo(playheadX, 6)
    ctx.lineTo(playheadX, height - 6)
    ctx.stroke()

    ctx.fillStyle = '#c9a876'
    ctx.beginPath()
    ctx.moveTo(playheadX - 4.5, 6)
    ctx.lineTo(playheadX + 4.5, 6)
    ctx.lineTo(playheadX, 13)
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
