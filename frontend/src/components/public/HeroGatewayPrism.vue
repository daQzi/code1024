<template>
  <div
    ref="stageRef"
    class="hgp-stage"
    :class="[`hgp-stage--${activeMode}`, { 'is-tilting': pointerActive }]"
    :style="stageStyle"
    @pointermove="onPointerMove"
    @pointerleave="onPointerLeave"
  >
    <div class="hgp-shell glass-panel">
      <div class="hgp-specular" aria-hidden="true" />

      <div class="hgp-header">
        <div class="hgp-brand-pill">
          <span class="hgp-live-dot" aria-hidden="true" />
          <span class="hgp-brand-name">{{ siteName }}</span>
          <span class="hgp-brand-sep" aria-hidden="true">/</span>
          <span class="hgp-brand-role">{{ t('referenceLanding.prism.badge') }}</span>
        </div>

        <div class="hgp-modes" role="group" :aria-label="t('referenceLanding.prism.modeLabel')">
          <button
            v-for="mode in modes"
            :key="mode"
            type="button"
            class="hgp-mode-btn"
            :class="{ 'is-active': activeMode === mode }"
            :aria-pressed="activeMode === mode"
            @click="activeMode = mode"
          >
            {{ t(`referenceLanding.prism.modes.${mode}`) }}
          </button>
        </div>
      </div>

      <div class="hgp-canvas-wrap">
        <svg
          class="hgp-svg"
          viewBox="0 0 580 356"
          fill="none"
          role="img"
          :aria-label="t('referenceLanding.prism.ariaLabel')"
        >
          <defs>
            <pattern :id="`${uid}-dots`" width="20" height="20" patternUnits="userSpaceOnUse">
              <circle cx="2" cy="2" r="0.9" fill="currentColor" opacity="0.16" />
            </pattern>

            <linearGradient :id="`${uid}-beam-teal`" x1="0%" y1="0%" x2="100%" y2="0%">
              <stop offset="0%" stop-color="#2dd4bf" stop-opacity="0.1" />
              <stop offset="50%" stop-color="#22d3ee" stop-opacity="1" />
              <stop offset="100%" stop-color="#818cf8" stop-opacity="0.2" />
            </linearGradient>

            <linearGradient :id="`${uid}-beam-amber`" x1="0%" y1="0%" x2="100%" y2="0%">
              <stop offset="0%" stop-color="#f59e0b" stop-opacity="0.15" />
              <stop offset="52%" stop-color="#fbbf24" stop-opacity="1" />
              <stop offset="100%" stop-color="#a78bfa" stop-opacity="0.3" />
            </linearGradient>

            <linearGradient :id="`${uid}-prism-face`" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stop-color="#5eead4" stop-opacity="0.85" />
              <stop offset="48%" stop-color="#38bdf8" stop-opacity="0.45" />
              <stop offset="100%" stop-color="#fbbf24" stop-opacity="0.72" />
            </linearGradient>

            <radialGradient :id="`${uid}-core-glow`" cx="50%" cy="50%" r="50%">
              <stop offset="0%" stop-color="#22d3ee" stop-opacity="0.55" />
              <stop offset="55%" stop-color="#818cf8" stop-opacity="0.18" />
              <stop offset="100%" stop-color="#818cf8" stop-opacity="0" />
            </radialGradient>

            <linearGradient :id="`${uid}-cinema-sky`" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stop-color="#0f172a" />
              <stop offset="55%" stop-color="#1e1b4b" />
              <stop offset="100%" stop-color="#451a03" />
            </linearGradient>

            <linearGradient :id="`${uid}-cinema-cone`" x1="20%" y1="0%" x2="80%" y2="100%">
              <stop offset="0%" stop-color="#fbbf24" stop-opacity="0.52" />
              <stop offset="65%" stop-color="#f59e0b" stop-opacity="0.14" />
              <stop offset="100%" stop-color="#f59e0b" stop-opacity="0" />
            </linearGradient>

            <filter :id="`${uid}-glow`" x="-35%" y="-35%" width="170%" height="170%">
              <feGaussianBlur stdDeviation="3.2" result="blur" />
              <feMerge>
                <feMergeNode in="blur" />
                <feMergeNode in="SourceGraphic" />
              </feMerge>
            </filter>
          </defs>

          <rect x="8" y="8" width="564" height="340" rx="18" :fill="`url(#${uid}-dots)`" class="hgp-dot-field" />

          <!-- Left optical waveguides into central prism -->
          <g class="hgp-paths-in" stroke-linecap="round">
            <g class="hgp-fiber hgp-fiber--code">
              <path d="M 148 70 C 194 70, 206 144, 246 164" class="hgp-wire-base" />
              <path d="M 148 70 C 194 70, 206 144, 246 164" :stroke="`url(#${uid}-beam-teal)`" class="hgp-wire-pulse hgp-wire-pulse--1" :filter="`url(#${uid}-glow)`" />
            </g>
            <g class="hgp-fiber hgp-fiber--code">
              <path d="M 148 140 C 196 140, 208 166, 244 174" class="hgp-wire-base" />
              <path d="M 148 140 C 196 140, 208 166, 244 174" :stroke="`url(#${uid}-beam-teal)`" class="hgp-wire-pulse hgp-wire-pulse--2" :filter="`url(#${uid}-glow)`" />
            </g>
            <g class="hgp-fiber hgp-fiber--code">
              <path d="M 148 212 C 196 212, 208 190, 244 182" class="hgp-wire-base" />
              <path d="M 148 212 C 196 212, 208 190, 244 182" :stroke="`url(#${uid}-beam-teal)`" class="hgp-wire-pulse hgp-wire-pulse--3" :filter="`url(#${uid}-glow)`" />
            </g>
            <g class="hgp-fiber hgp-fiber--cinema">
              <path d="M 148 286 C 198 286, 208 212, 246 192" class="hgp-wire-base" />
              <path d="M 148 286 C 198 286, 208 212, 246 192" :stroke="`url(#${uid}-beam-amber)`" class="hgp-wire-pulse hgp-wire-pulse--4" :filter="`url(#${uid}-glow)`" />
            </g>
          </g>

          <!-- Right refracted dual-core waveguides -->
          <g class="hgp-paths-out" stroke-linecap="round">
            <g class="hgp-fiber hgp-fiber--code">
              <path d="M 304 164 C 334 150, 342 98, 372 98" class="hgp-wire-base" />
              <path d="M 304 164 C 334 150, 342 98, 372 98" :stroke="`url(#${uid}-beam-teal)`" class="hgp-wire-pulse hgp-wire-pulse--out-a" :filter="`url(#${uid}-glow)`" />
            </g>
            <g class="hgp-fiber hgp-fiber--cinema">
              <path d="M 304 192 C 334 208, 342 258, 372 258" class="hgp-wire-base" />
              <path d="M 304 192 C 334 208, 342 258, 372 258" :stroke="`url(#${uid}-beam-amber)`" class="hgp-wire-pulse hgp-wire-pulse--out-b" :filter="`url(#${uid}-glow)`" />
            </g>
          </g>

          <!-- Left 4 Input Port Nodes -->
          <g class="hgp-inputs">
            <g class="hgp-port hgp-port--code" transform="translate(20, 48)">
              <rect width="128" height="44" rx="12" class="hgp-node-rect" />
              <circle cx="18" cy="22" r="4" fill="#2dd4bf" />
              <text x="31" y="19" class="hgp-node-title">Claude Code</text>
              <text x="31" y="33" class="hgp-node-sub">Messages API</text>
              <circle cx="128" cy="22" r="3" class="hgp-pin hgp-pin--teal" />
            </g>

            <g class="hgp-port hgp-port--code" transform="translate(20, 118)">
              <rect width="128" height="44" rx="12" class="hgp-node-rect" />
              <circle cx="18" cy="22" r="4" fill="#22d3ee" />
              <text x="31" y="19" class="hgp-node-title">Codex CLI</text>
              <text x="31" y="33" class="hgp-node-sub">Responses API</text>
              <circle cx="128" cy="22" r="3" class="hgp-pin hgp-pin--teal" />
            </g>

            <g class="hgp-port hgp-port--code" transform="translate(20, 190)">
              <rect width="128" height="44" rx="12" class="hgp-node-rect" />
              <circle cx="18" cy="22" r="4" fill="#818cf8" />
              <text x="31" y="19" class="hgp-node-title">Gemini / SDK</text>
              <text x="31" y="33" class="hgp-node-sub">v1beta · OpenAI</text>
              <circle cx="128" cy="22" r="3" class="hgp-pin hgp-pin--teal" />
            </g>

            <g class="hgp-port hgp-port--cinema" transform="translate(20, 264)">
              <rect width="128" height="44" rx="12" class="hgp-node-rect hgp-node-rect--amber" />
              <circle cx="18" cy="22" r="4" fill="#fbbf24" />
              <text x="31" y="19" class="hgp-node-title">{{ t('referenceLanding.prism.canvasPort') }}</text>
              <text x="31" y="33" class="hgp-node-sub">Canvas · Drama</text>
              <circle cx="128" cy="22" r="3" class="hgp-pin hgp-pin--amber" />
            </g>
          </g>

          <!-- Central Optical Gateway Prism -->
          <g class="hgp-core" transform="translate(274, 178)">
            <circle r="68" :fill="`url(#${uid}-core-glow)`" class="hgp-core-halo" />
            <circle r="54" class="hgp-ring-outer" stroke-dasharray="4 7" />
            <circle r="43" class="hgp-ring-inner" stroke-dasharray="18 8 4 8" />
            <polygon points="0,-32 28,0 0,32 -28,0" class="hgp-prism-body" :stroke="`url(#${uid}-prism-face)`" />
            <polygon points="0,-20 17,0 0,20 -17,0" :fill="`url(#${uid}-prism-face)`" opacity="0.28" />
            <path d="M 0 -32 L 0 32 M -28 0 L 28 0" stroke="currentColor" stroke-opacity="0.22" stroke-width="0.8" />
            <circle r="6.5" fill="#2dd4bf" class="hgp-core-heart" :filter="`url(#${uid}-glow)`" />
            <text y="69" text-anchor="middle" class="hgp-core-label">GATEWAY CORE</text>
          </g>

          <!-- Top-Right Engine: Code AST & Streaming Terminal -->
          <g class="hgp-engine hgp-engine--code" transform="translate(372, 26)">
            <rect width="188" height="142" rx="16" class="hgp-panel-card" />
            <rect x="0.75" y="0.75" width="186.5" height="28" rx="15" class="hgp-panel-bar" />
            <circle cx="16" cy="14" r="3" fill="#f87171" opacity="0.85" />
            <circle cx="27" cy="14" r="3" fill="#fbbf24" opacity="0.85" />
            <circle cx="38" cy="14" r="3" fill="#34d399" opacity="0.85" />
            <text x="52" y="18" class="hgp-panel-caption">CODE ENGINE</text>
            <circle cx="174" cy="14" r="3.5" fill="#2dd4bf" class="hgp-blink-dot" />

            <!-- Code syntax stream lines -->
            <g transform="translate(14, 42)" class="hgp-code-lines">
              <text x="0" y="9" class="hgp-code-prompt">$ codex --gateway</text>
              <rect x="0" y="18" width="96" height="5" rx="2.5" fill="#2dd4bf" opacity="0.8" class="hgp-code-bar hgp-code-bar--1" />
              <rect x="102" y="18" width="44" height="5" rx="2.5" fill="#818cf8" opacity="0.72" />
              <rect x="0" y="30" width="68" height="5" rx="2.5" fill="#38bdf8" opacity="0.75" />
              <rect x="74" y="30" width="84" height="5" rx="2.5" fill="#94a3b8" opacity="0.45" class="hgp-code-bar hgp-code-bar--2" />
              <rect x="0" y="42" width="132" height="5" rx="2.5" fill="#34d399" opacity="0.72" class="hgp-code-bar hgp-code-bar--3" />
              <rect x="0" y="54" width="54" height="5" rx="2.5" fill="#38bdf8" opacity="0.65" />
              <rect x="60" y="54" width="6" height="9" rx="1.5" fill="#2dd4bf" class="hgp-cursor" />
            </g>

            <!-- Bottom protocol strip + live equalizer -->
            <g transform="translate(14, 114)">
              <rect width="50" height="16" rx="8" class="hgp-micro-pill" />
              <text x="25" y="11" text-anchor="middle" class="hgp-micro-text">OpenAI</text>
              <rect x="55" width="50" height="16" rx="8" class="hgp-micro-pill" />
              <text x="80" y="11" text-anchor="middle" class="hgp-micro-text">Claude</text>
              <g transform="translate(120, 2)" class="hgp-eq">
                <rect x="0" y="5" width="3" height="8" rx="1.5" fill="#2dd4bf" class="hgp-eq-bar hgp-eq-bar--1" />
                <rect x="6" y="2" width="3" height="11" rx="1.5" fill="#22d3ee" class="hgp-eq-bar hgp-eq-bar--2" />
                <rect x="12" y="6" width="3" height="7" rx="1.5" fill="#818cf8" class="hgp-eq-bar hgp-eq-bar--3" />
                <rect x="18" y="3" width="3" height="10" rx="1.5" fill="#2dd4bf" class="hgp-eq-bar hgp-eq-bar--4" />
                <rect x="24" y="7" width="3" height="6" rx="1.5" fill="#38bdf8" class="hgp-eq-bar hgp-eq-bar--5" />
              </g>
            </g>
            <circle cx="0" cy="72" r="3.5" class="hgp-pin hgp-pin--teal" />
          </g>

          <!-- Bottom-Right Engine: Cinema Canvas 2.39:1 Viewfinder -->
          <g class="hgp-engine hgp-engine--cinema" transform="translate(372, 188)">
            <rect width="188" height="142" rx="16" class="hgp-panel-card hgp-panel-card--cinema" />
            <text x="14" y="20" class="hgp-panel-caption hgp-panel-caption--amber">CINEMA CANVAS</text>
            <text x="174" y="20" text-anchor="end" class="hgp-timecode">24fps · 2.39:1</text>

            <!-- Anamorphic cinema viewport -->
            <g transform="translate(14, 30)">
              <rect width="160" height="72" rx="10" :fill="`url(#${uid}-cinema-sky)`" />
              <!-- Volumetric projector beam + stylized mountain/horizon silhouette -->
              <polygon points="34,0 132,72 18,72" :fill="`url(#${uid}-cinema-cone)`" class="hgp-cinema-beam" />
              <path d="M 0 58 Q 36 38 72 52 T 160 44 L 160 72 L 0 72 Z" fill="#0b1120" opacity="0.88" />
              <circle cx="122" cy="24" r="9" fill="#fbbf24" opacity="0.82" />
              <circle cx="122" cy="24" r="16" stroke="#fbbf24" stroke-opacity="0.35" stroke-width="1" class="hgp-reticle-pulse" />
              <!-- Optical camera framing brackets -->
              <path d="M 10 16 V 10 H 18 M 150 16 V 10 H 142 M 10 56 V 62 H 18 M 150 56 V 62 H 142" stroke="#f8fafc" stroke-opacity="0.62" stroke-width="1.2" stroke-linecap="round" />
              <!-- Focus crosshair -->
              <g transform="translate(80, 36)" stroke="#fbbf24" stroke-opacity="0.72" stroke-width="1">
                <line x1="-8" y1="0" x2="-3" y2="0" />
                <line x1="3" y1="0" x2="8" y2="0" />
                <line x1="0" y1="-8" x2="0" y2="-3" />
                <line x1="0" y1="3" x2="0" y2="8" />
              </g>
              <!-- Animated optical scan sweep -->
              <rect x="0" y="0" width="2" height="72" fill="#fbbf24" opacity="0.65" class="hgp-playhead" />
            </g>

            <!-- Storyboard timeline strip -->
            <g transform="translate(14, 111)">
              <rect width="48" height="17" rx="5" class="hgp-shot-chip hgp-shot-chip--1" />
              <rect x="56" width="48" height="17" rx="5" class="hgp-shot-chip hgp-shot-chip--2" />
              <rect x="112" width="48" height="17" rx="5" class="hgp-shot-chip hgp-shot-chip--3" />
              <text x="24" y="11.5" text-anchor="middle" class="hgp-shot-text">SHOT 01</text>
              <text x="80" y="11.5" text-anchor="middle" class="hgp-shot-text">SHOT 02</text>
              <text x="136" y="11.5" text-anchor="middle" class="hgp-shot-text">SHOT 03</text>
            </g>
            <circle cx="0" cy="70" r="3.5" class="hgp-pin hgp-pin--amber" />
          </g>
        </svg>
      </div>

      <div class="hgp-footer">
        <p class="hgp-summary">{{ t(`referenceLanding.prism.summary.${activeMode}`) }}</p>
        <a
          v-if="canvasUrl"
          :href="canvasUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="hgp-canvas-link"
        >
          <span>{{ canvasLabel || t('referenceLanding.prism.openCanvas') }}</span>
          <Icon name="externalLink" size="sm" aria-hidden="true" />
        </a>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

defineProps<{
  siteName: string
  canvasUrl?: string
  canvasLabel?: string
}>()

const { t } = useI18n()
const uid = `hgp-${useId().replace(/:/g, '')}`
const modes = ['all', 'code', 'cinema'] as const
type PrismMode = (typeof modes)[number]

const activeMode = ref<PrismMode>('all')
const stageRef = ref<HTMLElement | null>(null)
const pointerActive = ref(false)
const mx = ref(50)
const my = ref(30)
const rx = ref(0)
const ry = ref(0)

let rafId = 0
let pendingX = 50
let pendingY = 30
let pendingRx = 0
let pendingRy = 0

const stageStyle = computed(() => ({
  '--prism-mx': `${mx.value}%`,
  '--prism-my': `${my.value}%`,
  '--prism-rx': `${rx.value}deg`,
  '--prism-ry': `${ry.value}deg`,
}))

function flushTilt() {
  rafId = 0
  mx.value = pendingX
  my.value = pendingY
  rx.value = pendingRx
  ry.value = pendingRy
}

function onPointerMove(event: PointerEvent) {
  if (event.pointerType === 'touch') return
  if (typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return
  const el = stageRef.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  if (!rect.width || !rect.height) return
  const normX = Math.max(0, Math.min(1, (event.clientX - rect.left) / rect.width))
  const normY = Math.max(0, Math.min(1, (event.clientY - rect.top) / rect.height))
  pendingX = Math.round(normX * 100)
  pendingY = Math.round(normY * 100)
  pendingRy = Number(((normX - 0.5) * 5.2).toFixed(2))
  pendingRx = Number(((0.5 - normY) * 4.4).toFixed(2))
  pointerActive.value = true
  if (!rafId) rafId = window.requestAnimationFrame(flushTilt)
}

function onPointerLeave() {
  pointerActive.value = false
  pendingX = 50
  pendingY = 26
  pendingRx = 0
  pendingRy = 0
  if (!rafId) rafId = window.requestAnimationFrame(flushTilt)
}

onBeforeUnmount(() => {
  if (rafId) window.cancelAnimationFrame(rafId)
})
</script>

<style scoped>
.hgp-stage {
  position: relative;
  perspective: 1200px;
  min-width: 0;
}

.hgp-shell {
  position: relative;
  border-radius: 30px;
  padding: 18px;
  overflow: hidden;
  background:
    linear-gradient(155deg, rgb(255 255 255 / 0.72) 0%, rgb(241 247 255 / 0.44) 55%, rgb(255 255 255 / 0.56) 100%);
  border: 1px solid rgb(255 255 255 / 0.8);
  box-shadow:
    inset 0 1px 0 rgb(255 255 255 / 0.92),
    inset 0 -1px 0 rgb(15 23 42 / 0.06),
    0 28px 64px -26px rgb(15 23 42 / 0.24),
    0 0 0 1px rgb(15 23 42 / 0.08);
  transform: rotateX(var(--prism-rx, 0deg)) rotateY(var(--prism-ry, 0deg));
  transition: transform 340ms cubic-bezier(0.16, 1, 0.3, 1), box-shadow 260ms ease;
  will-change: transform;
}

.hgp-stage.is-tilting .hgp-shell {
  transition: transform 90ms linear;
}

.dark .hgp-shell {
  background:
    linear-gradient(155deg, rgb(15 23 42 / 0.76) 0%, rgb(9 14 27 / 0.68) 55%, rgb(17 24 39 / 0.74) 100%);
  border-color: rgb(255 255 255 / 0.16);
  box-shadow:
    inset 0 1px 0 rgb(255 255 255 / 0.22),
    inset 0 -1px 0 rgb(0 0 0 / 0.5),
    0 34px 76px -26px rgb(0 0 0 / 0.72),
    0 0 0 1px rgb(45 212 191 / 0.14);
}

.hgp-specular {
  position: absolute;
  inset: 0;
  pointer-events: none;
  background: radial-gradient(
    380px circle at var(--prism-mx, 50%) var(--prism-my, 30%),
    rgb(255 255 255 / 0.34),
    rgb(45 212 191 / 0.1) 36%,
    transparent 68%
  );
  mix-blend-mode: plus-lighter;
  opacity: 0.7;
  transition: opacity 220ms ease;
}

.dark .hgp-specular {
  background: radial-gradient(
    360px circle at var(--prism-mx, 50%) var(--prism-my, 30%),
    rgb(94 234 212 / 0.22),
    rgb(129 140 248 / 0.1) 38%,
    transparent 70%
  );
  opacity: 0.85;
}

.hgp-header {
  position: relative;
  z-index: 2;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}

.hgp-brand-pill {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 5px 12px;
  border-radius: 999px;
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--ui-text, #0b1220);
  background: rgb(255 255 255 / 0.56);
  border: 1px solid rgb(15 23 42 / 0.08);
}

.dark .hgp-brand-pill {
  color: var(--ui-text, #f5f8fc);
  background: rgb(255 255 255 / 0.07);
  border-color: rgb(255 255 255 / 0.12);
}

.hgp-live-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #14b8a6;
  box-shadow: 0 0 0 4px rgb(20 184 166 / 0.22);
}

.dark .hgp-live-dot {
  background: #2dd4bf;
  box-shadow: 0 0 0 4px rgb(45 212 191 / 0.24);
}

.hgp-brand-sep,
.hgp-brand-role {
  color: var(--ui-text-muted, #4a5b73);
  font-weight: 500;
}

.hgp-modes {
  display: inline-flex;
  padding: 3px;
  border-radius: 999px;
  background: rgb(15 23 42 / 0.06);
  border: 1px solid rgb(15 23 42 / 0.07);
}

.dark .hgp-modes {
  background: rgb(255 255 255 / 0.06);
  border-color: rgb(255 255 255 / 0.1);
}

.hgp-mode-btn {
  border: 0;
  background: transparent;
  color: var(--ui-text-muted, #4a5b73);
  padding: 5px 11px;
  border-radius: 999px;
  font-size: 0.75rem;
  font-weight: 600;
  cursor: pointer;
  transition: background 180ms ease, color 180ms ease, box-shadow 180ms ease;
}

.hgp-mode-btn.is-active {
  background: rgb(255 255 255 / 0.92);
  color: #0f766e;
  box-shadow: 0 2px 8px -2px rgb(15 23 42 / 0.18);
}

.dark .hgp-mode-btn.is-active {
  background: linear-gradient(125deg, rgb(45 212 191 / 0.24), rgb(129 140 248 / 0.22));
  color: #f8fafc;
  box-shadow: inset 0 1px 0 rgb(255 255 255 / 0.22);
}

.hgp-canvas-wrap {
  position: relative;
  z-index: 2;
  border-radius: 20px;
  background: linear-gradient(160deg, rgb(12 20 36 / 0.95) 0%, rgb(15 24 42 / 0.92) 60%, rgb(19 26 46 / 0.96) 100%);
  border: 1px solid rgb(255 255 255 / 0.12);
  box-shadow: inset 0 1px 0 rgb(255 255 255 / 0.14);
  overflow: hidden;
}

.hgp-svg {
  display: block;
  width: 100%;
  height: auto;
  color: #e2e8f0;
}

.hgp-wire-base {
  stroke: rgb(148 163 184 / 0.26);
  stroke-width: 1.5;
  transition: opacity 260ms ease;
}

.hgp-wire-pulse {
  stroke-width: 2.3;
  stroke-dasharray: 28 210;
  stroke-dashoffset: 238;
  animation: hgp-beam-travel 3.2s linear infinite;
  transition: opacity 260ms ease;
}

.hgp-wire-pulse--2 { animation-delay: -0.9s; }
.hgp-wire-pulse--3 { animation-delay: -1.8s; }
.hgp-wire-pulse--4 { animation-delay: -1.3s; animation-duration: 3.6s; }
.hgp-wire-pulse--out-a { animation-duration: 2.6s; animation-delay: -0.4s; }
.hgp-wire-pulse--out-b { animation-duration: 3s; animation-delay: -1.1s; }

.hgp-node-rect {
  fill: rgb(15 23 42 / 0.78);
  stroke: rgb(148 163 184 / 0.24);
  stroke-width: 1;
  transition: stroke 220ms ease, opacity 240ms ease;
}

.hgp-node-rect--amber {
  stroke: rgb(251 191 36 / 0.36);
}

.hgp-node-title {
  fill: #f8fafc;
  font-size: 11px;
  font-weight: 650;
  font-family: var(--ui-font, -apple-system, sans-serif);
}

.hgp-node-sub {
  fill: #94a3b8;
  font-size: 9px;
  font-family: var(--ui-font-mono, ui-monospace, monospace);
}

.hgp-pin--teal { fill: #2dd4bf; }
.hgp-pin--amber { fill: #fbbf24; }

.hgp-ring-outer {
  stroke: rgb(56 189 248 / 0.42);
  stroke-width: 1.1;
  transform-origin: 0 0;
  animation: hgp-spin-cw 28s linear infinite;
}

.hgp-ring-inner {
  stroke: rgb(251 191 36 / 0.42);
  stroke-width: 1.1;
  transform-origin: 0 0;
  animation: hgp-spin-ccw 20s linear infinite;
}

.hgp-prism-body {
  fill: rgb(15 23 42 / 0.88);
  stroke-width: 1.8;
}

.hgp-core-heart {
  animation: hgp-pulse 2.8s ease-in-out infinite;
}

.hgp-core-label {
  fill: #94a3b8;
  font-size: 8.5px;
  font-weight: 700;
  letter-spacing: 0.16em;
  font-family: var(--ui-font-mono, ui-monospace, monospace);
}

.hgp-panel-card {
  fill: rgb(11 18 32 / 0.88);
  stroke: rgb(56 189 248 / 0.32);
  stroke-width: 1.1;
}

.hgp-panel-card--cinema {
  stroke: rgb(251 191 36 / 0.36);
}

.hgp-panel-bar {
  fill: rgb(255 255 255 / 0.04);
}

.hgp-panel-caption {
  fill: #5eead4;
  font-size: 8.5px;
  font-weight: 700;
  letter-spacing: 0.14em;
  font-family: var(--ui-font-mono, ui-monospace, monospace);
}

.hgp-panel-caption--amber {
  fill: #fbbf24;
}

.hgp-timecode {
  fill: #cbd5e1;
  font-size: 8.5px;
  font-family: var(--ui-font-mono, ui-monospace, monospace);
}

.hgp-code-prompt {
  fill: #e2e8f0;
  font-size: 9.5px;
  font-family: var(--ui-font-mono, ui-monospace, monospace);
}

.hgp-micro-pill {
  fill: rgb(45 212 191 / 0.12);
  stroke: rgb(45 212 191 / 0.28);
  stroke-width: 0.8;
}

.hgp-micro-text {
  fill: #cbd5e1;
  font-size: 8.5px;
  font-weight: 600;
  font-family: var(--ui-font-mono, ui-monospace, monospace);
}

.hgp-shot-chip {
  fill: rgb(251 191 36 / 0.12);
  stroke: rgb(251 191 36 / 0.34);
  stroke-width: 0.85;
}

.hgp-shot-text {
  fill: #fde68a;
  font-size: 8px;
  font-weight: 650;
  font-family: var(--ui-font-mono, ui-monospace, monospace);
}

.hgp-cursor {
  animation: hgp-blink 1.1s steps(2, start) infinite;
}

.hgp-blink-dot {
  animation: hgp-pulse 2.2s ease-in-out infinite;
}

.hgp-eq-bar {
  transform-origin: center bottom;
  transform-box: fill-box;
  animation: hgp-eq-dance 1.5s ease-in-out infinite alternate;
}

.hgp-eq-bar--2 { animation-delay: -0.3s; }
.hgp-eq-bar--3 { animation-delay: -0.7s; }
.hgp-eq-bar--4 { animation-delay: -0.45s; }
.hgp-eq-bar--5 { animation-delay: -0.95s; }

.hgp-playhead {
  animation: hgp-scan 4.4s linear infinite;
}

.hgp-reticle-pulse {
  transform-origin: 122px 24px;
  animation: hgp-pulse 3s ease-in-out infinite;
}

.hgp-fiber,
.hgp-port,
.hgp-engine {
  transition: opacity 280ms ease;
}

.hgp-stage--code .hgp-fiber--cinema,
.hgp-stage--code .hgp-port--cinema,
.hgp-stage--code .hgp-engine--cinema {
  opacity: 0.28;
}

.hgp-stage--cinema .hgp-fiber--code,
.hgp-stage--cinema .hgp-port--code,
.hgp-stage--cinema .hgp-engine--code {
  opacity: 0.28;
}

.hgp-footer {
  position: relative;
  z-index: 2;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: 12px;
  padding: 2px 4px 0;
}

.hgp-summary {
  margin: 0;
  font-size: 0.8125rem;
  line-height: 1.55;
  color: var(--ui-text-muted, #4a5b73);
  flex: 1 1 220px;
}

.hgp-canvas-link {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  border-radius: 999px;
  font-size: 0.78rem;
  font-weight: 600;
  text-decoration: none;
  color: #b45309;
  background: rgb(245 158 11 / 0.14);
  border: 1px solid rgb(245 158 11 / 0.34);
  transition: transform 180ms ease, background 180ms ease;
}

.dark .hgp-canvas-link {
  color: #fde68a;
  background: rgb(251 191 36 / 0.14);
  border-color: rgb(251 191 36 / 0.34);
}

.hgp-canvas-link:hover {
  transform: translateY(-1px);
}

@keyframes hgp-beam-travel {
  from { stroke-dashoffset: 238; }
  to { stroke-dashoffset: 0; }
}

@keyframes hgp-spin-cw {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

@keyframes hgp-spin-ccw {
  from { transform: rotate(360deg); }
  to { transform: rotate(0deg); }
}

@keyframes hgp-pulse {
  0%, 100% { opacity: 0.65; }
  50% { opacity: 1; }
}

@keyframes hgp-blink {
  0%, 49% { opacity: 1; }
  50%, 100% { opacity: 0; }
}

@keyframes hgp-eq-dance {
  from { transform: scaleY(0.45); }
  to { transform: scaleY(1.1); }
}

@keyframes hgp-scan {
  from { transform: translateX(0); }
  to { transform: translateX(158px); }
}

@media (prefers-reduced-motion: reduce), (max-width: 767px) {
  .hgp-shell { transform: none; }
  .hgp-wire-pulse,
  .hgp-ring-outer,
  .hgp-ring-inner,
  .hgp-core-heart,
  .hgp-cursor,
  .hgp-blink-dot,
  .hgp-eq-bar,
  .hgp-playhead,
  .hgp-reticle-pulse {
    animation: none;
  }
}
</style>
