export interface AmbientParticlesOptions {
  canvas: HTMLCanvasElement
  initialDark: boolean
}

export interface AmbientParticlesController {
  setDark: (dark: boolean) => void
  resize: (width: number, height: number) => void
  dispose: () => void
}

interface Spore {
  x: number
  y: number
  vx: number
  vy: number
  baseVy: number
  radius: number
  phase: number
  tint: 'jade' | 'bronze' | 'bone'
}

interface Meteor {
  active: boolean
  x: number
  y: number
  vx: number
  vy: number
  life: number
  maxLife: number
  length: number
  variant: 'jade' | 'bronze' | 'bone'
}

interface Ripple {
  active: boolean
  x: number
  y: number
  radius: number
  maxRadius: number
  life: number
  maxLife: number
  variant: 'jade' | 'bronze' | 'bone'
}

const SPORE_COUNT = 72
const MAX_METEORS = 5
const MAX_RIPPLES = 3

/**
 * Interactive 2D cosmic particle layer for the authenticated console:
 * renders dual-halo astral spores pushed by gentle pointer wake, shooting stars
 * with luminous star flares, and subtle expanding energy ripples.
 */
export function createAmbientParticlesEngine(options: AmbientParticlesOptions): AmbientParticlesController {
  const { canvas } = options
  const ctx = canvas.getContext('2d')
  const dpr = Math.min(typeof window !== 'undefined' ? window.devicePixelRatio || 1 : 1, 1.0)

  let width = Math.max(1, canvas.clientWidth || (typeof window !== 'undefined' ? window.innerWidth : 1440))
  let height = Math.max(1, canvas.clientHeight || (typeof window !== 'undefined' ? window.innerHeight : 900))
  let dark = options.initialDark

  const applyCanvasSize = (w: number, h: number) => {
    width = Math.max(1, w)
    height = Math.max(1, h)
    canvas.width = Math.round(width * dpr)
    canvas.height = Math.round(height * dpr)
  }
  applyCanvasSize(width, height)

  const tints: Spore['tint'][] = ['jade', 'bronze', 'bone']
  const spores: Spore[] = Array.from({ length: SPORE_COUNT }, (_, i) => {
    const baseVy = -(8 + Math.random() * 16)
    return {
      x: Math.random() * width,
      y: Math.random() * height,
      vx: (Math.random() - 0.5) * 8,
      vy: baseVy,
      baseVy,
      radius: 1.2 + Math.random() * 2.2,
      phase: Math.random() * Math.PI * 2,
      tint: tints[i % 3]!,
    }
  })

  const meteors: Meteor[] = Array.from({ length: MAX_METEORS }, () => ({
    active: false,
    x: 0,
    y: 0,
    vx: 0,
    vy: 0,
    life: 0,
    maxLife: 1,
    length: 160,
    variant: 'jade',
  }))

  const ripples: Ripple[] = Array.from({ length: MAX_RIPPLES }, () => ({
    active: false,
    x: 0,
    y: 0,
    radius: 0,
    maxRadius: 160,
    life: 0,
    maxLife: 1.8,
    variant: 'jade',
  }))

  let pointerX = -9999
  let pointerY = -9999
  let pointerVx = 0
  let pointerVy = 0
  let lastPointerX = -9999
  let lastPointerY = -9999
  let nextMeteorIn = 0.8 + Math.random() * 1.4
  let nextRippleIn = 2.4 + Math.random() * 2.2
  let elapsed = 0
  let lastTimestamp = typeof performance !== 'undefined' ? performance.now() : 0
  let rafId: number | null = null
  let disposed = false

  const spawnRipple = (x: number, y: number, maxRadius = 150 + Math.random() * 90) => {
    const slot = ripples.find((r) => !r.active)
    if (!slot) return
    const variants: Ripple['variant'][] = ['jade', 'bone', 'bronze']
    slot.active = true
    slot.x = x
    slot.y = y
    slot.radius = 6
    slot.maxRadius = maxRadius
    slot.life = 0
    slot.maxLife = 1.6 + Math.random() * 0.8
    slot.variant = variants[Math.floor(Math.random() * variants.length)]!
  }

  const handlePointerMove = (e: PointerEvent) => {
    if (e.pointerType === 'touch') return
    if (lastPointerX > -9000) {
      pointerVx = Math.max(-420, Math.min(420, (e.clientX - lastPointerX) * 18))
      pointerVy = Math.max(-420, Math.min(420, (e.clientY - lastPointerY) * 18))
    }
    pointerX = e.clientX
    pointerY = e.clientY
    lastPointerX = e.clientX
    lastPointerY = e.clientY
  }

  const handlePointerDown = (e: PointerEvent) => {
    if (e.pointerType === 'touch') return
    spawnRipple(e.clientX, e.clientY, 130)
  }

  const handleVisibilityChange = () => {
    if (typeof document !== 'undefined' && !document.hidden) {
      lastTimestamp = performance.now()
    }
  }

  if (typeof window !== 'undefined') {
    window.addEventListener('pointermove', handlePointerMove, { passive: true })
    window.addEventListener('pointerdown', handlePointerDown, { passive: true })
  }
  if (typeof document !== 'undefined') {
    document.addEventListener('visibilitychange', handleVisibilityChange)
  }

  const spawnMeteor = () => {
    const slot = meteors.find((m) => !m.active)
    if (!slot) return
    const fromRight = Math.random() > 0.35
    const variants: Meteor['variant'][] = ['jade', 'bronze', 'bone']
    slot.active = true
    slot.x = fromRight ? width * (0.42 + Math.random() * 0.58) : width * (0.06 + Math.random() * 0.45)
    slot.y = -24
    const speed = 580 + Math.random() * 320
    const angle = fromRight ? Math.PI * 0.72 + Math.random() * 0.14 : Math.PI * 0.24 + Math.random() * 0.14
    slot.vx = Math.cos(angle) * speed
    slot.vy = Math.sin(angle) * speed
    slot.life = 0
    slot.maxLife = 0.72 + Math.random() * 0.48
    slot.length = 140 + Math.random() * 100
    slot.variant = variants[Math.floor(Math.random() * variants.length)]!
  }

  const renderFrame = (now: number) => {
    if (disposed) return
    rafId = window.requestAnimationFrame(renderFrame)
    if (!ctx || (typeof document !== 'undefined' && document.hidden)) {
      lastTimestamp = now
      return
    }

    const dt = Math.min(0.05, Math.max(0.001, (now - lastTimestamp) * 0.001))
    lastTimestamp = now
    elapsed += dt
    pointerVx *= Math.max(0, 1 - dt * 6)
    pointerVy *= Math.max(0, 1 - dt * 6)

    nextMeteorIn -= dt
    if (nextMeteorIn <= 0) {
      spawnMeteor()
      nextMeteorIn = 1.1 + Math.random() * 1.3
    }

    nextRippleIn -= dt
    if (nextRippleIn <= 0) {
      spawnRipple(width * (0.15 + Math.random() * 0.7), height * (0.18 + Math.random() * 0.65))
      nextRippleIn = 3.5 + Math.random() * 2.0
    }

    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, width, height)

    const themeScale = dark ? 1 : 0.28
    const nearbyIndices: number[] = []

    // 1. Update & render dual-layer astral spores
    for (let i = 0; i < spores.length; i++) {
      const s = spores[i]!
      const dx = s.x - pointerX
      const dy = s.y - pointerY
      const distSq = dx * dx + dy * dy

      if (distSq < 185 * 185 && distSq > 1) {
        const dist = Math.sqrt(distSq)
        const falloff = 1 - dist / 185
        s.vx += ((dx / dist) * 62 + pointerVx * 0.22) * falloff * dt
        s.vy += ((dy / dist) * 62 + pointerVy * 0.22) * falloff * dt
      }
      if (distSq < 220 * 220) {
        nearbyIndices.push(i)
      }

      s.vx += (Math.sin(elapsed * 0.7 + s.phase) * 8.5 - s.vx) * dt * 1.4
      s.vy += (s.baseVy - s.vy) * dt * 1.6
      s.x += s.vx * dt
      s.y += s.vy * dt

      if (s.y < -16) {
        s.y = height + 16
        s.x = Math.random() * width
      } else if (s.y > height + 24) {
        s.y = -12
      }
      if (s.x < -16) s.x = width + 16
      else if (s.x > width + 16) s.x = -16

      const alpha = (0.38 + 0.28 * Math.sin(elapsed * 1.85 + s.phase)) * themeScale
      const rgb =
        s.tint === 'jade'
          ? '143, 181, 168'
          : s.tint === 'bronze'
            ? '201, 168, 118'
            : '216, 212, 200'

      // Outer luminous halo
      ctx.beginPath()
      ctx.arc(s.x, s.y, s.radius * 2.8, 0, Math.PI * 2)
      ctx.fillStyle = `rgba(${rgb}, ${(alpha * 0.20).toFixed(3)})`
      ctx.fill()

      // Crisp bright star core
      ctx.beginPath()
      ctx.arc(s.x, s.y, s.radius, 0, Math.PI * 2)
      ctx.fillStyle = `rgba(${rgb}, ${alpha.toFixed(3)})`
      ctx.fill()
    }

    // 2. Subtle proximity filaments between spores near the pointer
    if (nearbyIndices.length > 1) {
      ctx.lineWidth = 0.7
      for (let a = 0; a < nearbyIndices.length; a++) {
        const sa = spores[nearbyIndices[a]!]!
        for (let b = a + 1; b < nearbyIndices.length; b++) {
          const sb = spores[nearbyIndices[b]!]!
          const distSq = (sa.x - sb.x) ** 2 + (sa.y - sb.y) ** 2
          if (distSq < 105 * 105) {
            const dist = Math.sqrt(distSq)
            const lineAlpha = (1 - dist / 105) * 0.20 * themeScale
            if (lineAlpha <= 0.01) continue
            ctx.beginPath()
            ctx.moveTo(sa.x, sa.y)
            ctx.lineTo(sb.x, sb.y)
            ctx.strokeStyle =
              sa.tint === 'bronze' || sb.tint === 'bronze'
                ? `rgba(251, 191, 36, ${lineAlpha.toFixed(3)})`
                : `rgba(56, 232, 213, ${lineAlpha.toFixed(3)})`
            ctx.stroke()
          }
        }
      }
    }

    // 3. Expanding astral energy ripples
    for (const r of ripples) {
      if (!r.active) continue
      r.life += dt
      if (r.life >= r.maxLife) {
        r.active = false
        continue
      }
      const p = r.life / r.maxLife
      const eased = 1 - Math.pow(1 - p, 3)
      const currentRadius = r.radius + (r.maxRadius - r.radius) * eased
      const alpha = (1 - p) * Math.sin(p * Math.PI) * 0.24 * themeScale
      const rgb =
        r.variant === 'bronze'
          ? '201, 168, 118'
          : r.variant === 'bone'
            ? '184, 178, 160'
            : '143, 181, 168'
      ctx.beginPath()
      ctx.arc(r.x, r.y, currentRadius, 0, Math.PI * 2)
      ctx.lineWidth = 1.25
      ctx.strokeStyle = `rgba(${rgb}, ${alpha.toFixed(3)})`
      ctx.stroke()
    }

    // 4. Multi-chromatic shooting stars with glowing star-head flare
    for (const m of meteors) {
      if (!m.active) continue
      m.life += dt
      if (m.life >= m.maxLife) {
        m.active = false
        continue
      }
      m.x += m.vx * dt
      m.y += m.vy * dt
      const p = m.life / m.maxLife
      const fade = Math.sin(p * Math.PI) * 0.90 * themeScale
      const mag = Math.hypot(m.vx, m.vy) || 1
      const tailX = m.x - (m.vx / mag) * m.length
      const tailY = m.y - (m.vy / mag) * m.length

      const rgb =
        m.variant === 'bronze'
          ? '201, 168, 118'
          : m.variant === 'bone'
            ? '184, 178, 160'
            : '143, 181, 168'

      const grad = ctx.createLinearGradient(tailX, tailY, m.x, m.y)
      grad.addColorStop(0, `rgba(${rgb}, 0)`)
      grad.addColorStop(0.72, `rgba(${rgb}, ${(fade * 0.78).toFixed(3)})`)
      grad.addColorStop(1, `rgba(255, 255, 255, ${fade.toFixed(3)})`)

      ctx.beginPath()
      ctx.moveTo(tailX, tailY)
      ctx.lineTo(m.x, m.y)
      ctx.lineWidth = 1.85
      ctx.strokeStyle = grad
      ctx.stroke()

      // Luminous comet head flare
      ctx.beginPath()
      ctx.arc(m.x, m.y, 4.2, 0, Math.PI * 2)
      ctx.fillStyle = `rgba(${rgb}, ${(fade * 0.34).toFixed(3)})`
      ctx.fill()

      ctx.beginPath()
      ctx.arc(m.x, m.y, 1.8, 0, Math.PI * 2)
      ctx.fillStyle = `rgba(255, 255, 255, ${fade.toFixed(3)})`
      ctx.fill()
    }
  }

  rafId = window.requestAnimationFrame(renderFrame)

  return {
    setDark(nextDark: boolean) {
      dark = nextDark
    },
    resize(w: number, h: number) {
      if (disposed) return
      applyCanvasSize(w, h)
    },
    dispose() {
      if (disposed) return
      disposed = true
      if (rafId !== null) {
        window.cancelAnimationFrame(rafId)
        rafId = null
      }
      if (typeof window !== 'undefined') {
        window.removeEventListener('pointermove', handlePointerMove)
        window.removeEventListener('pointerdown', handlePointerDown)
      }
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', handleVisibilityChange)
      }
      if (ctx) {
        ctx.setTransform(1, 0, 0, 1, 0, 0)
        ctx.clearRect(0, 0, canvas.width, canvas.height)
      }
    },
  }
}
