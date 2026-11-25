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
  tint: 'teal' | 'gold' | 'lavender'
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
  gold: boolean
}

const SPORE_COUNT = 56
const MAX_METEORS = 3

/**
 * Lightweight interactive 2D particle engine for the authenticated console:
 * renders floating astral spores pushed by pointer wake, proximity filaments,
 * and periodic shooting stars.
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

  const tints: Spore['tint'][] = ['teal', 'gold', 'lavender']
  const spores: Spore[] = Array.from({ length: SPORE_COUNT }, (_, i) => {
    const baseVy = -(6 + Math.random() * 14)
    return {
      x: Math.random() * width,
      y: Math.random() * height,
      vx: (Math.random() - 0.5) * 6,
      vy: baseVy,
      baseVy,
      radius: 1.1 + Math.random() * 1.9,
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
    length: 110,
    gold: false,
  }))

  let pointerX = -9999
  let pointerY = -9999
  let pointerVx = 0
  let pointerVy = 0
  let lastPointerX = -9999
  let lastPointerY = -9999
  let nextMeteorIn = 1.6 + Math.random() * 2.2
  let elapsed = 0
  let lastTimestamp = typeof performance !== 'undefined' ? performance.now() : 0
  let rafId: number | null = null
  let disposed = false

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

  const handleVisibilityChange = () => {
    if (typeof document !== 'undefined' && !document.hidden) {
      lastTimestamp = performance.now()
    }
  }

  if (typeof window !== 'undefined') {
    window.addEventListener('pointermove', handlePointerMove, { passive: true })
  }
  if (typeof document !== 'undefined') {
    document.addEventListener('visibilitychange', handleVisibilityChange)
  }

  const spawnMeteor = () => {
    const slot = meteors.find((m) => !m.active)
    if (!slot) return
    const fromRight = Math.random() > 0.35
    slot.active = true
    slot.x = fromRight ? width * (0.45 + Math.random() * 0.55) : width * (0.1 + Math.random() * 0.45)
    slot.y = -24
    const speed = 520 + Math.random() * 280
    const angle = fromRight ? Math.PI * 0.72 + Math.random() * 0.14 : Math.PI * 0.24 + Math.random() * 0.14
    slot.vx = Math.cos(angle) * speed
    slot.vy = Math.sin(angle) * speed
    slot.life = 0
    slot.maxLife = 0.75 + Math.random() * 0.45
    slot.length = 95 + Math.random() * 65
    slot.gold = Math.random() > 0.58
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
      nextMeteorIn = 2.6 + Math.random() * 2.4
    }

    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, width, height)

    const themeScale = dark ? 1 : 0.28
    const nearbyIndices: number[] = []

    // 1. Update & render astral spores
    for (let i = 0; i < spores.length; i++) {
      const s = spores[i]!
      const dx = s.x - pointerX
      const dy = s.y - pointerY
      const distSq = dx * dx + dy * dy

      if (distSq < 175 * 175 && distSq > 1) {
        const dist = Math.sqrt(distSq)
        const falloff = 1 - dist / 175
        // Aerodynamic wake swirl around moving cursor
        s.vx += ((dx / dist) * 58 + pointerVx * 0.24) * falloff * dt
        s.vy += ((dy / dist) * 58 + pointerVy * 0.24) * falloff * dt
      }
      if (distSq < 210 * 210) {
        nearbyIndices.push(i)
      }

      s.vx += (Math.sin(elapsed * 0.65 + s.phase) * 7 - s.vx) * dt * 1.4
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

      const alpha = (0.34 + 0.24 * Math.sin(elapsed * 1.7 + s.phase)) * themeScale
      ctx.beginPath()
      ctx.arc(s.x, s.y, s.radius, 0, Math.PI * 2)
      ctx.fillStyle =
        s.tint === 'teal'
          ? `rgba(56, 232, 213, ${alpha.toFixed(3)})`
          : s.tint === 'gold'
            ? `rgba(240, 194, 123, ${alpha.toFixed(3)})`
            : `rgba(216, 212, 255, ${(alpha * 0.85).toFixed(3)})`
      ctx.fill()
    }

    // 2. Subtle proximity filaments between spores near the pointer
    if (nearbyIndices.length > 1) {
      ctx.lineWidth = 0.65
      for (let a = 0; a < nearbyIndices.length; a++) {
        const sa = spores[nearbyIndices[a]!]!
        for (let b = a + 1; b < nearbyIndices.length; b++) {
          const sb = spores[nearbyIndices[b]!]!
          const distSq = (sa.x - sb.x) ** 2 + (sa.y - sb.y) ** 2
          if (distSq < 92 * 92) {
            const dist = Math.sqrt(distSq)
            const lineAlpha = (1 - dist / 92) * 0.18 * themeScale
            if (lineAlpha <= 0.01) continue
            ctx.beginPath()
            ctx.moveTo(sa.x, sa.y)
            ctx.lineTo(sb.x, sb.y)
            ctx.strokeStyle =
              sa.tint === 'gold' || sb.tint === 'gold'
                ? `rgba(240, 194, 123, ${lineAlpha.toFixed(3)})`
                : `rgba(56, 232, 213, ${lineAlpha.toFixed(3)})`
            ctx.stroke()
          }
        }
      }
    }

    // 3. Shooting stars
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
      const fade = Math.sin(p * Math.PI) * 0.82 * themeScale
      const mag = Math.hypot(m.vx, m.vy) || 1
      const tailX = m.x - (m.vx / mag) * m.length
      const tailY = m.y - (m.vy / mag) * m.length

      const grad = ctx.createLinearGradient(tailX, tailY, m.x, m.y)
      if (m.gold) {
        grad.addColorStop(0, 'rgba(240, 194, 123, 0)')
        grad.addColorStop(0.75, `rgba(240, 194, 123, ${(fade * 0.72).toFixed(3)})`)
        grad.addColorStop(1, `rgba(255, 250, 235, ${fade.toFixed(3)})`)
      } else {
        grad.addColorStop(0, 'rgba(56, 232, 213, 0)')
        grad.addColorStop(0.75, `rgba(56, 232, 213, ${(fade * 0.72).toFixed(3)})`)
        grad.addColorStop(1, `rgba(235, 255, 255, ${fade.toFixed(3)})`)
      }
      ctx.beginPath()
      ctx.moveTo(tailX, tailY)
      ctx.lineTo(m.x, m.y)
      ctx.lineWidth = 1.6
      ctx.strokeStyle = grad
      ctx.stroke()
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
