<template>
  <div class="gp-backdrop" aria-hidden="true">
    <div class="gp-backdrop__wash" />
    <div class="gp-backdrop__orb gp-backdrop__orb--teal" />
    <div class="gp-backdrop__orb gp-backdrop__orb--indigo" />
    <div class="gp-backdrop__orb gp-backdrop__orb--amber" />

    <svg class="gp-backdrop__optics" viewBox="0 0 1600 1000" preserveAspectRatio="xMidYMid slice">
      <defs>
        <pattern :id="`${uid}-grid`" width="64" height="64" patternUnits="userSpaceOnUse">
          <path d="M 64 0 L 0 0 0 64" fill="none" stroke="currentColor" stroke-width="0.7" opacity="0.28" />
          <path d="M 0 -3 V 3 M -3 0 H 3" fill="none" stroke="currentColor" stroke-width="0.9" opacity="0.55" />
        </pattern>
        <radialGradient :id="`${uid}-mask-grad`" cx="50%" cy="22%" r="68%">
          <stop offset="0%" stop-color="#ffffff" stop-opacity="0.95" />
          <stop offset="56%" stop-color="#ffffff" stop-opacity="0.35" />
          <stop offset="100%" stop-color="#ffffff" stop-opacity="0" />
        </radialGradient>
        <mask :id="`${uid}-grid-mask`">
          <rect width="1600" height="1000" :fill="`url(#${uid}-mask-grad)`" />
        </mask>
        <linearGradient :id="`${uid}-ray-code`" x1="0%" y1="0%" x2="100%" y2="0%">
          <stop offset="0%" stop-color="#2dd4bf" stop-opacity="0" />
          <stop offset="32%" stop-color="#22d3ee" stop-opacity="0.6" />
          <stop offset="70%" stop-color="#818cf8" stop-opacity="0.48" />
          <stop offset="100%" stop-color="#818cf8" stop-opacity="0" />
        </linearGradient>
        <linearGradient :id="`${uid}-ray-cinema`" x1="100%" y1="0%" x2="0%" y2="100%">
          <stop offset="0%" stop-color="#fbbf24" stop-opacity="0" />
          <stop offset="45%" stop-color="#f59e0b" stop-opacity="0.42" />
          <stop offset="78%" stop-color="#818cf8" stop-opacity="0.34" />
          <stop offset="100%" stop-color="#22d3ee" stop-opacity="0" />
        </linearGradient>
      </defs>

      <rect
        width="1600"
        height="1000"
        :fill="`url(#${uid}-grid)`"
        :mask="`url(#${uid}-grid-mask)`"
        class="gp-backdrop__grid"
      />

      <g class="gp-backdrop__arcs" fill="none" stroke-linecap="round">
        <path
          d="M -120 420 C 260 220, 620 180, 960 310 S 1420 440, 1740 210"
          :stroke="`url(#${uid}-ray-code)`"
          stroke-width="1.6"
          class="gp-backdrop__beam gp-backdrop__beam--a"
        />
        <path
          d="M -80 560 C 310 390, 700 470, 1080 340 S 1460 180, 1720 350"
          :stroke="`url(#${uid}-ray-cinema)`"
          stroke-width="1.35"
          class="gp-backdrop__beam gp-backdrop__beam--b"
        />
        <path
          d="M 140 -40 C 380 260, 740 460, 1340 520"
          :stroke="`url(#${uid}-ray-code)`"
          stroke-width="1"
          opacity="0.48"
          class="gp-backdrop__beam gp-backdrop__beam--c"
        />
      </g>
    </svg>

    <div class="gp-backdrop__grain" />
    <div class="gp-backdrop__fade" />
  </div>
</template>

<script setup lang="ts">
import { useId } from 'vue'

const uid = `gp-bg-${useId().replace(/:/g, '')}`
</script>

<style scoped>
.gp-backdrop {
  position: fixed;
  inset: 0;
  z-index: 0;
  overflow: hidden;
  pointer-events: none;
  contain: strict;
}

.gp-backdrop__wash {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(110% 74% at 10% 0%, var(--ui-canvas-tint-1, #6ee7d7) 0%, transparent 46%),
    radial-gradient(96% 66% at 92% 6%, var(--ui-canvas-tint-2, #93c5fd) 0%, transparent 44%),
    radial-gradient(108% 82% at 62% 102%, var(--ui-canvas-tint-3, #c4b5fd) 0%, transparent 50%),
    linear-gradient(172deg, var(--ui-canvas, #d5dff2) 0%, var(--ui-canvas-deep, #c2d0ea) 100%);
}

.gp-backdrop__orb {
  position: absolute;
  border-radius: 50%;
  filter: blur(64px);
  opacity: 0.42;
  will-change: transform;
}

.dark .gp-backdrop__orb {
  opacity: 0.34;
}

.gp-backdrop__orb--teal {
  width: 44vw;
  height: 44vw;
  top: -16vw;
  left: -8vw;
  background: radial-gradient(circle, rgb(45 212 191 / 0.62) 0%, transparent 68%);
  animation: gp-orb-a 26s ease-in-out infinite alternate;
}

.gp-backdrop__orb--indigo {
  width: 42vw;
  height: 42vw;
  top: 2vw;
  right: -12vw;
  background: radial-gradient(circle, rgb(99 102 241 / 0.54) 0%, transparent 68%);
  animation: gp-orb-b 32s ease-in-out infinite alternate;
}

.gp-backdrop__orb--amber {
  width: 30vw;
  height: 30vw;
  top: 34vh;
  left: 44vw;
  background: radial-gradient(circle, rgb(245 158 11 / 0.32) 0%, transparent 70%);
  animation: gp-orb-c 36s ease-in-out infinite alternate;
}

.gp-backdrop__optics {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  color: rgb(15 23 42 / 0.28);
}

.dark .gp-backdrop__optics {
  color: rgb(186 230 253 / 0.24);
}

.gp-backdrop__beam {
  stroke-dasharray: 220 960;
  stroke-dashoffset: 0;
  opacity: 0.72;
}

.gp-backdrop__beam--a {
  animation: gp-ray-flow 13s linear infinite;
}

.gp-backdrop__beam--b {
  stroke-dasharray: 180 860;
  animation: gp-ray-flow-rev 17s linear infinite;
}

.gp-backdrop__beam--c {
  stroke-dasharray: 140 760;
  animation: gp-ray-flow 21s linear infinite;
}

.gp-backdrop__grain {
  position: absolute;
  inset: 0;
  opacity: 0.14;
  mix-blend-mode: overlay;
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='160' height='160'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.82' numOctaves='3' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='160' height='160' filter='url(%23n)' opacity='0.52'/%3E%3C/svg%3E");
}

.gp-backdrop__fade {
  position: absolute;
  inset: 0;
  background: radial-gradient(90% 60% at 50% 0%, transparent 0%, rgb(213 223 242 / 0.22) 100%);
}

.dark .gp-backdrop__fade {
  background: radial-gradient(90% 60% at 50% 0%, transparent 0%, rgb(4 7 13 / 0.42) 100%);
}

@keyframes gp-orb-a {
  from { transform: translate3d(0, 0, 0) scale(1); }
  to { transform: translate3d(5vw, 4vh, 0) scale(1.1); }
}

@keyframes gp-orb-b {
  from { transform: translate3d(0, 0, 0) scale(1.04); }
  to { transform: translate3d(-6vw, 6vh, 0) scale(0.94); }
}

@keyframes gp-orb-c {
  from { transform: translate3d(0, 0, 0) scale(0.96); }
  to { transform: translate3d(-4vw, -5vh, 0) scale(1.08); }
}

@keyframes gp-ray-flow {
  from { stroke-dashoffset: 1180; }
  to { stroke-dashoffset: 0; }
}

@keyframes gp-ray-flow-rev {
  from { stroke-dashoffset: 0; }
  to { stroke-dashoffset: 1040; }
}

@media (prefers-reduced-motion: reduce), (max-width: 767px) {
  .gp-backdrop__orb,
  .gp-backdrop__beam {
    animation: none;
  }
}

@media (prefers-reduced-transparency: reduce), (prefers-contrast: more) {
  .gp-backdrop__optics,
  .gp-backdrop__orb {
    opacity: 0.16;
  }
}
</style>
