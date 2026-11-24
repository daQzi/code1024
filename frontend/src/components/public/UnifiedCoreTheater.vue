<template>
  <section
    id="studio"
    ref="sectionRef"
    class="rl-section rl-container uct"
    :class="[`uct--${activeScene}`, { 'uct--static': !scrollDriven }]"
    :style="sceneStyle"
    aria-labelledby="studio-title"
  >
    <div class="rl-section-heading uct-heading" data-reveal>
      <span class="glass-control rl-eyebrow">{{ t('referenceLanding.theater.eyebrow') }}</span>
      <h2 id="studio-title">{{ t('referenceLanding.theater.title') }}</h2>
      <p>{{ t('referenceLanding.theater.description') }}</p>
      <div class="glass-panel rl-demo-tabs" role="group" :aria-label="t('referenceLanding.theater.modeLabel')" @keydown="onTabKey">
        <button
          v-for="scene in scenes"
          :id="`studio-tab-${scene}`"
          :key="scene"
          type="button"
          class="rl-demo-tab"
          :class="{ 'is-active': activeScene === scene }"
          :aria-pressed="activeScene === scene"
          :aria-controls="`studio-panel-${scene}`"
          :tabindex="activeScene === scene ? 0 : -1"
          @click="selectScene(scene)"
        >
          <Icon :name="scene === 'code' ? 'terminal' : scene === 'cinema' ? 'palette' : 'cube'" size="sm" aria-hidden="true" />
          <span>{{ t(`referenceLanding.theater.scenes.${scene}.tab`) }}</span>
        </button>
      </div>
    </div>

    <div ref="trackRef" class="uct-track">
      <div class="uct-sticky">
        <div class="uct-layout">
          <div class="uct-copy" aria-live="polite" aria-atomic="true">
            <span class="uct-scene-count">{{ String(sceneIndex + 1).padStart(2, '0') }} <i /> 03</span>
            <div
              v-for="scene in scenes"
              v-show="activeScene === scene"
              :id="`studio-panel-${scene}`"
              :key="scene"
              class="uct-copy-panel"
              role="region"
              :aria-labelledby="`studio-tab-${scene}`"
            >
              <h3>{{ t(`referenceLanding.theater.scenes.${scene}.title`) }}</h3>
              <p>{{ t(`referenceLanding.theater.scenes.${scene}.description`) }}</p>
              <ul>
                <li v-for="feature in 3" :key="feature">
                  <Icon name="check" size="sm" aria-hidden="true" />
                  {{ t(`referenceLanding.theater.scenes.${scene}.feature${feature}`) }}
                </li>
              </ul>
            </div>
            <div class="uct-actions">
              <a v-if="activeScene === 'cinema' && canvasUrl" :href="canvasUrl" target="_blank" rel="noopener noreferrer" class="uct-canvas-action">
                <Icon name="palette" size="sm" aria-hidden="true" />
                {{ canvasLabel || t('referenceLanding.theater.openCanvas') }}
                <Icon name="externalLink" size="sm" aria-hidden="true" />
              </a>
              <a v-else-if="activeScene === 'gateway' && docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer" class="uct-doc-action">
                <Icon name="book" size="sm" aria-hidden="true" />
                {{ t('glass.nav.docs') }}
                <Icon name="externalLink" size="sm" aria-hidden="true" />
              </a>
              <span v-else class="uct-scroll-hint"><i aria-hidden="true" />{{ t(scrollDriven ? 'referenceLanding.theater.scrollHint' : 'referenceLanding.theater.tapHint') }}</span>
            </div>
          </div>

          <div class="uct-device" data-reveal data-reveal-delay="80">
            <div class="uct-device-bar">
              <span class="uct-window-dots" aria-hidden="true"><i /><i /><i /></span>
              <span class="uct-device-title">{{ siteName }} / Creative OS</span>
              <span class="uct-live"><i aria-hidden="true" />LIVE</span>
            </div>
            <svg viewBox="0 0 940 460" class="uct-svg" role="img" :aria-label="t(`referenceLanding.theater.scenes.${activeScene}.ariaLabel`)">
              <defs>
                <pattern :id="`${uid}-grid`" width="28" height="28" patternUnits="userSpaceOnUse">
                  <path d="M 28 0 L 0 0 0 28" fill="none" stroke="#94a3b8" stroke-opacity="0.12" stroke-width="0.7" />
                </pattern>
                <linearGradient :id="`${uid}-aurora`" x1="0%" y1="0%" x2="100%" y2="100%">
                  <stop offset="0%" stop-color="#14b8a6" stop-opacity="0.3" />
                  <stop offset="52%" stop-color="#38bdf8" stop-opacity="0.14" />
                  <stop offset="100%" stop-color="#818cf8" stop-opacity="0.3" />
                </linearGradient>
                <linearGradient :id="`${uid}-beam`" x1="0%" y1="0%" x2="100%" y2="0%">
                  <stop offset="0%" stop-color="#2dd4bf" stop-opacity="0.12" />
                  <stop offset="52%" stop-color="#22d3ee" />
                  <stop offset="100%" stop-color="#818cf8" stop-opacity="0.2" />
                </linearGradient>
                <linearGradient :id="`${uid}-gold-beam`" x1="0%" y1="0%" x2="100%" y2="0%">
                  <stop offset="0%" stop-color="#fbbf24" stop-opacity="0.1" />
                  <stop offset="55%" stop-color="#fbbf24" />
                  <stop offset="100%" stop-color="#a78bfa" stop-opacity="0.2" />
                </linearGradient>
                <linearGradient :id="`${uid}-cinema-sky`" x1="0%" y1="0%" x2="80%" y2="100%">
                  <stop offset="0%" stop-color="#172554" />
                  <stop offset="52%" stop-color="#312e81" />
                  <stop offset="100%" stop-color="#78350f" />
                </linearGradient>
                <radialGradient :id="`${uid}-core`">
                  <stop offset="0%" stop-color="#22d3ee" stop-opacity="0.4" />
                  <stop offset="100%" stop-color="#22d3ee" stop-opacity="0" />
                </radialGradient>
                <filter :id="`${uid}-glow`" x="-60%" y="-60%" width="220%" height="220%">
                  <feGaussianBlur stdDeviation="3" result="blur" />
                  <feMerge><feMergeNode in="blur" /><feMergeNode in="SourceGraphic" /></feMerge>
                </filter>
              </defs>

              <rect width="940" height="460" fill="#0b1220" />
              <rect width="940" height="460" :fill="`url(#${uid}-grid)`" />
              <ellipse cx="490" cy="170" rx="390" ry="260" :fill="`url(#${uid}-aurora)`" />

              <!-- Scene 1: a coding workspace connected to the real gateway vocabulary. -->
              <g v-show="activeScene === 'code'" class="uct-scene uct-scene--code">
                <g transform="translate(36, 30)">
                  <rect width="602" height="354" rx="18" class="uct-glass-card" />
                  <path d="M 0 42 H 602" stroke="#94a3b8" stroke-opacity="0.16" />
                  <circle cx="22" cy="21" r="4" fill="#fb7185" /><circle cx="38" cy="21" r="4" fill="#fbbf24" /><circle cx="54" cy="21" r="4" fill="#2dd4bf" />
                  <text x="76" y="25" class="uct-svg-label">codex / creative-agent.ts</text>
                  <text x="576" y="25" text-anchor="end" class="uct-svg-micro">UTF-8 · TypeScript</text>
                  <g transform="translate(24, 66)" class="uct-code">
                    <text y="0"><tspan fill="#64748b">01</tspan><tspan x="34" fill="#c084fc">import</tspan><tspan fill="#e2e8f0"> { gateway } </tspan><tspan fill="#c084fc">from</tspan><tspan fill="#5eead4"> '@code1024/sdk'</tspan></text>
                    <text y="34"><tspan fill="#64748b">02</tspan></text>
                    <text y="68"><tspan fill="#64748b">03</tspan><tspan x="34" fill="#c084fc">const</tspan><tspan fill="#7dd3fc"> story</tspan><tspan fill="#94a3b8"> = </tspan><tspan fill="#c084fc">await</tspan><tspan fill="#e2e8f0"> gateway.create({</tspan></text>
                    <text y="102"><tspan fill="#64748b">04</tspan><tspan x="50" fill="#7dd3fc">model:</tspan><tspan fill="#5eead4"> 'your-model-id'</tspan><tspan fill="#e2e8f0">,</tspan></text>
                    <text y="136"><tspan fill="#64748b">05</tspan><tspan x="50" fill="#7dd3fc">prompt:</tspan><tspan fill="#5eead4"> 'Turn an idea into a film'</tspan><tspan fill="#e2e8f0">,</tspan></text>
                    <text y="170"><tspan fill="#64748b">06</tspan><tspan x="50" fill="#7dd3fc">tools:</tspan><tspan fill="#e2e8f0"> [</tspan><tspan fill="#5eead4">'code'</tspan><tspan fill="#e2e8f0">, </tspan><tspan fill="#5eead4">'canvas'</tspan><tspan fill="#e2e8f0">]</tspan></text>
                    <text y="204"><tspan fill="#64748b">07</tspan><tspan x="34" fill="#e2e8f0">})</tspan></text>
                    <rect x="56" y="191" width="8" height="16" rx="2" fill="#2dd4bf" class="uct-cursor" />
                    <text y="256"><tspan fill="#64748b">08</tspan><tspan x="34" fill="#2dd4bf">✓ </tspan><tspan fill="#94a3b8">Gateway connected · Ready to create</tspan></text>
                  </g>
                </g>
                <path d="M 638 218 C 682 218, 684 128, 726 128" class="uct-wire-base" />
                <path d="M 638 218 C 682 218, 684 128, 726 128" :stroke="`url(#${uid}-beam)`" class="uct-wire-pulse" />
                <path d="M 638 248 C 690 248, 680 320, 726 320" class="uct-wire-base" />
                <path d="M 638 248 C 690 248, 680 320, 726 320" :stroke="`url(#${uid}-gold-beam)`" class="uct-wire-pulse uct-wire-pulse--late" />
                <g transform="translate(726, 52)">
                  <rect width="178" height="148" rx="18" class="uct-glass-card" />
                  <text x="20" y="32" class="uct-svg-label">CODE ENGINE</text>
                  <rect x="20" y="54" width="138" height="44" rx="10" class="uct-terminal-card" />
                  <text x="32" y="73" class="uct-svg-micro">$ pnpm dev</text>
                  <text x="32" y="89" fill="#5eead4" class="uct-svg-micro">▸ ready in 1024ms</text>
                  <g transform="translate(22, 118)" class="uct-equalizer">
                    <rect width="5" height="14" rx="2" fill="#2dd4bf" /><rect x="10" y="-7" width="5" height="21" rx="2" fill="#38bdf8" /><rect x="20" y="-3" width="5" height="17" rx="2" fill="#818cf8" /><rect x="30" y="-10" width="5" height="24" rx="2" fill="#2dd4bf" />
                  </g>
                  <text x="158" y="128" text-anchor="end" class="uct-svg-micro">BUILD / RUN</text>
                </g>
                <g transform="translate(726, 258)">
                  <rect width="178" height="126" rx="18" class="uct-glass-card uct-glass-card--gold" />
                  <text x="20" y="30" fill="#fbbf24" class="uct-svg-label">CREATIVE TOOLS</text>
                  <rect x="20" y="46" width="60" height="48" rx="9" fill="#fbbf24" fill-opacity="0.08" stroke="#fbbf24" stroke-opacity="0.24" />
                  <rect x="90" y="46" width="68" height="48" rx="9" fill="#a78bfa" fill-opacity="0.08" stroke="#a78bfa" stroke-opacity="0.24" />
                  <text x="50" y="74" text-anchor="middle" class="uct-svg-micro">STORY</text>
                  <text x="124" y="74" text-anchor="middle" class="uct-svg-micro">CANVAS</text>
                  <text x="89" y="114" text-anchor="middle" class="uct-svg-micro">IDEA → SHOT → FILM</text>
                </g>
              </g>

              <!-- Scene 2: cinema and the qimu canvas share this single stage. -->
              <g v-show="activeScene === 'cinema'" class="uct-scene uct-scene--cinema">
                <g transform="translate(30, 32)">
                  <rect width="664" height="342" rx="18" class="uct-glass-card uct-glass-card--gold" />
                  <text x="20" y="28" fill="#fbbf24" class="uct-svg-label">影策 / CINEMA CANVAS</text>
                  <text x="642" y="28" text-anchor="end" class="uct-svg-micro">2.39:1 · 24 FPS</text>
                  <g transform="translate(20, 46)">
                    <rect width="624" height="230" rx="12" :fill="`url(#${uid}-cinema-sky)`" />
                    <circle cx="464" cy="70" r="28" fill="#fcd34d" opacity="0.8" />
                    <circle cx="464" cy="70" r="43" fill="none" stroke="#fcd34d" stroke-opacity="0.32" class="uct-reticle" />
                    <path d="M 0 184 Q 96 110 190 169 T 388 145 T 624 162 V 230 H 0 Z" fill="#111827" opacity="0.85" />
                    <path d="M 0 203 Q 164 150 306 190 T 624 184 V 230 H 0 Z" fill="#080d18" />
                    <path d="M 90 18 V 8 H 108 M 534 18 V 8 H 516 M 90 186 V 198 H 108 M 534 186 V 198 H 516" fill="none" stroke="#e2e8f0" stroke-opacity="0.72" stroke-width="1.3" />
                    <g transform="translate(302, 104)" stroke="#fbbf24" stroke-opacity="0.78"><circle r="17" fill="none" /><path d="M -29 0 H -8 M 8 0 H 29 M 0 -29 V -8 M 0 8 V 29" /></g>
                    <rect width="2" height="230" fill="#fbbf24" class="uct-scan" />
                    <text x="18" y="216" fill="#e2e8f0" class="uct-svg-micro">SCENE 01 · SHOT 03 · CAMERA A</text>
                  </g>
                  <g transform="translate(20, 292)">
                    <rect width="86" height="30" rx="7" fill="#fbbf24" fill-opacity="0.15" stroke="#fbbf24" stroke-opacity="0.42" />
                    <rect x="96" width="86" height="30" rx="7" fill="#a78bfa" fill-opacity="0.12" stroke="#a78bfa" stroke-opacity="0.3" />
                    <rect x="192" width="86" height="30" rx="7" fill="#38bdf8" fill-opacity="0.1" stroke="#38bdf8" stroke-opacity="0.25" />
                    <text x="43" y="19" text-anchor="middle" fill="#fde68a" class="uct-svg-micro">SCRIPT</text><text x="139" y="19" text-anchor="middle" fill="#ddd6fe" class="uct-svg-micro">STORYBOARD</text><text x="235" y="19" text-anchor="middle" fill="#bae6fd" class="uct-svg-micro">PREVIEW</text>
                    <text x="622" y="19" text-anchor="end" fill="#94a3b8" class="uct-svg-micro">ASSETS · TIMELINE · 3D</text>
                  </g>
                </g>
                <path d="M 694 188 C 732 188, 710 108, 746 108" class="uct-wire-base" />
                <path d="M 694 188 C 732 188, 710 108, 746 108" :stroke="`url(#${uid}-gold-beam)`" class="uct-wire-pulse" />
                <path d="M 694 250 C 730 250, 710 310, 746 310" class="uct-wire-base" />
                <path d="M 694 250 C 730 250, 710 310, 746 310" :stroke="`url(#${uid}-beam)`" class="uct-wire-pulse uct-wire-pulse--late" />
                <g transform="translate(746, 34)">
                  <rect width="160" height="148" rx="18" class="uct-glass-card uct-glass-card--gold" />
                  <text x="18" y="28" fill="#fbbf24" class="uct-svg-label">STORY NODES</text>
                  <g transform="translate(18, 48)">
                    <rect width="56" height="42" rx="9" class="uct-node" /><rect x="70" width="56" height="42" rx="9" class="uct-node" />
                    <circle cx="28" cy="21" r="9" fill="#fbbf24" fill-opacity="0.3" /><path d="M 84 27 L 96 14 L 112 27" fill="none" stroke="#a78bfa" />
                    <path d="M 28 42 V 58 H 98 V 42" fill="none" stroke="#94a3b8" stroke-opacity="0.45" stroke-dasharray="3 3" />
                    <rect y="66" width="56" height="26" rx="7" class="uct-node" /><rect x="70" y="66" width="56" height="26" rx="7" class="uct-node" />
                    <text x="28" y="83" text-anchor="middle" class="uct-svg-micro">ROLE</text><text x="98" y="83" text-anchor="middle" class="uct-svg-micro">SCENE</text>
                  </g>
                </g>
                <g transform="translate(746, 234)">
                  <rect width="160" height="150" rx="18" class="uct-glass-card" />
                  <text x="18" y="28" class="uct-svg-label">TIMELINE</text>
                  <path d="M 24 54 H 140 M 24 88 H 140 M 24 122 H 140" stroke="#94a3b8" stroke-opacity="0.22" />
                  <rect x="24" y="44" width="68" height="18" rx="4" fill="#818cf8" fill-opacity="0.5" /><rect x="42" y="78" width="98" height="18" rx="4" fill="#fbbf24" fill-opacity="0.45" /><rect x="24" y="112" width="88" height="18" rx="4" fill="#2dd4bf" fill-opacity="0.4" />
                  <path d="M 76 40 V 136" stroke="#f8fafc" stroke-width="1.2" class="uct-timeline-head" /><circle cx="76" cy="40" r="3" fill="#f8fafc" class="uct-timeline-head" />
                </g>
              </g>

              <!-- Scene 3: a protocol hub, not a claim of account or data unification. -->
              <g v-show="activeScene === 'gateway'" class="uct-scene uct-scene--gateway">
                <g class="uct-gateway-wires">
                  <path d="M 198 72 C 316 72, 322 187, 422 212 M 198 186 C 316 186, 344 214, 422 224 M 198 302 C 316 302, 344 250, 422 240 M 198 396 C 316 396, 316 282, 422 254" class="uct-wire-base" />
                  <path d="M 198 72 C 316 72, 322 187, 422 212 M 198 186 C 316 186, 344 214, 422 224 M 198 302 C 316 302, 344 250, 422 240 M 198 396 C 316 396, 316 282, 422 254" :stroke="`url(#${uid}-beam)`" class="uct-wire-pulse" />
                  <path d="M 518 224 C 614 214, 624 126, 734 126 M 518 244 C 614 258, 624 330, 734 330" class="uct-wire-base" />
                  <path d="M 518 224 C 614 214, 624 126, 734 126" :stroke="`url(#${uid}-beam)`" class="uct-wire-pulse" />
                  <path d="M 518 244 C 614 258, 624 330, 734 330" :stroke="`url(#${uid}-gold-beam)`" class="uct-wire-pulse uct-wire-pulse--late" />
                </g>
                <g transform="translate(32, 44)">
                  <g v-for="(port, index) in ['OpenAI', 'Claude', 'Gemini', 'Multimodal']" :key="port" :transform="`translate(0, ${index * 108})`">
                    <rect width="166" height="56" rx="14" class="uct-glass-card" />
                    <circle cx="24" cy="28" r="5" :fill="index === 3 ? '#fbbf24' : '#2dd4bf'" />
                    <text x="42" y="26" fill="#f1f5f9" class="uct-svg-label">{{ port }}</text>
                    <text x="42" y="41" class="uct-svg-micro">{{ index === 3 ? 'TEXT · IMAGE · VIDEO' : 'API · SDK · CLI' }}</text>
                  </g>
                </g>
                <g transform="translate(470, 228)">
                  <circle r="126" :fill="`url(#${uid}-core)`" />
                  <circle r="94" fill="none" stroke="#38bdf8" stroke-opacity="0.3" stroke-dasharray="3 9" class="uct-orbit" />
                  <circle r="74" fill="none" stroke="#fbbf24" stroke-opacity="0.28" stroke-dasharray="26 9" class="uct-orbit uct-orbit--reverse" />
                  <path d="M 0 -52 L 45 0 L 0 52 L -45 0 Z" fill="#111c31" stroke="#67e8f9" stroke-opacity="0.65" stroke-width="1.6" />
                  <path d="M 0 -32 L 28 0 L 0 32 L -28 0 Z" fill="#2dd4bf" fill-opacity="0.14" stroke="#fbbf24" stroke-opacity="0.6" />
                  <circle r="9" fill="#5eead4" :filter="`url(#${uid}-glow)`" class="uct-core-pulse" />
                  <text y="86" text-anchor="middle" fill="#bae6fd" class="uct-svg-label">UNIFIED GATEWAY</text>
                  <text y="106" text-anchor="middle" class="uct-svg-micro">ONE API · MANY POSSIBILITIES</text>
                </g>
                <g transform="translate(734, 54)">
                  <rect width="174" height="146" rx="18" class="uct-glass-card" />
                  <text x="20" y="30" class="uct-svg-label">DEVELOPER FLOW</text>
                  <rect x="20" y="50" width="134" height="32" rx="8" class="uct-terminal-card" /><text x="86" y="70" text-anchor="middle" class="uct-svg-micro">CLI · SDK · MCP</text>
                  <path d="M 28 108 H 146" stroke="#2dd4bf" stroke-opacity="0.4" /><text x="86" y="128" text-anchor="middle" fill="#5eead4" class="uct-svg-micro">BUILD & SHIP</text>
                </g>
                <g transform="translate(734, 260)">
                  <rect width="174" height="146" rx="18" class="uct-glass-card uct-glass-card--gold" />
                  <text x="20" y="30" fill="#fbbf24" class="uct-svg-label">CREATIVE FLOW</text>
                  <rect x="20" y="50" width="134" height="32" rx="8" class="uct-terminal-card" /><text x="86" y="70" text-anchor="middle" class="uct-svg-micro">CANVAS · CINEMA</text>
                  <path d="M 28 108 H 146" stroke="#fbbf24" stroke-opacity="0.4" /><text x="86" y="128" text-anchor="middle" fill="#fde68a" class="uct-svg-micro">SCRIPT TO SCREEN</text>
                </g>
              </g>

              <g transform="translate(32, 424)">
                <rect width="876" height="2" rx="1" fill="#94a3b8" fill-opacity="0.16" />
                <rect width="292" height="2" rx="1" :fill="activeScene === 'cinema' ? '#fbbf24' : '#2dd4bf'" :transform="`translate(${sceneIndex * 292} 0)`" class="uct-progress" />
                <text y="23" class="uct-svg-micro" :fill="activeScene === 'code' ? '#5eead4' : undefined">CODE</text><text x="438" y="23" text-anchor="middle" class="uct-svg-micro" :fill="activeScene === 'cinema' ? '#fde68a' : undefined">CINEMA CANVAS</text><text x="876" y="23" text-anchor="end" class="uct-svg-micro" :fill="activeScene === 'gateway' ? '#5eead4' : undefined">GATEWAY</text>
              </g>
            </svg>
            <div class="uct-device-foot">
              <span>{{ t(`referenceLanding.theater.scenes.${activeScene}.caption`) }}</span>
              <span class="uct-render-status"><i aria-hidden="true" />{{ t('referenceLanding.theater.status') }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
    <p v-if="activeScene === 'cinema' && !canvasUrl" class="uct-canvas-empty">{{ t('referenceLanding.theater.canvasEmpty') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

defineProps<{
  siteName: string
  canvasUrl?: string
  canvasLabel?: string
  docUrl?: string
}>()

const emit = defineEmits<{ sceneChange: [index: number] }>()
const { t } = useI18n()
const uid = `uct-${useId().replace(/:/g, '')}`
const scenes = ['code', 'cinema', 'gateway'] as const
type Scene = (typeof scenes)[number]
const activeScene = ref<Scene>('code')
const sceneIndex = computed(() => scenes.indexOf(activeScene.value))
const scrollDriven = ref(false)
const sceneStyle = computed(() => ({ '--uct-progress': String(sceneIndex.value / (scenes.length - 1)) }))
const sectionRef = ref<HTMLElement | null>(null)
const trackRef = ref<HTMLElement | null>(null)
let mediaQuery: MediaQueryList | undefined
let desktopQuery: MediaQueryList | undefined
let observer: IntersectionObserver | undefined
let frame = 0
let inView = false
let listening = false
let manualSceneUntil = 0

function selectScene(scene: Scene) {
  activeScene.value = scene
  manualSceneUntil = Date.now() + 700
  emit('sceneChange', scenes.indexOf(scene))
}

async function onTabKey(event: KeyboardEvent) {
  if (event.altKey || event.ctrlKey || event.metaKey || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  const tabs = Array.from((event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('.rl-demo-tab'))
  const current = tabs.indexOf(document.activeElement as HTMLButtonElement)
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (Math.max(0, current) + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length
  event.preventDefault()
  selectScene(scenes[next])
  await nextTick()
  tabs[next]?.focus()
}

function syncScene() {
  frame = 0
  if (!scrollDriven.value || !inView || !trackRef.value || Date.now() < manualSceneUntil) return
  const rect = trackRef.value.getBoundingClientRect()
  const distance = Math.max(1, rect.height - window.innerHeight)
  const progress = Math.max(0, Math.min(1, -rect.top / distance))
  const index = Math.min(scenes.length - 1, Math.floor(progress * scenes.length))
  if (activeScene.value !== scenes[index]) {
    activeScene.value = scenes[index]
    emit('sceneChange', index)
  }
}

function onScroll() {
  if (!frame) frame = window.requestAnimationFrame(syncScene)
}

function configureMotion() {
  const nextValue = !(mediaQuery?.matches ?? false) && (desktopQuery?.matches ?? false)
  scrollDriven.value = nextValue
  if (nextValue && !listening) {
    window.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', onScroll, { passive: true })
    listening = true
  } else if (!nextValue && listening) {
    window.removeEventListener('scroll', onScroll)
    window.removeEventListener('resize', onScroll)
    listening = false
  }
  if (frame) window.cancelAnimationFrame(frame)
  frame = 0
  syncScene()
}

onMounted(() => {
  mediaQuery = window.matchMedia('(prefers-reduced-motion: reduce)')
  desktopQuery = window.matchMedia('(min-width: 1024px) and (min-height: 700px)')
  configureMotion()
  mediaQuery.addEventListener?.('change', configureMotion)
  desktopQuery.addEventListener?.('change', configureMotion)
  if (typeof IntersectionObserver !== 'undefined') {
    observer = new IntersectionObserver(([entry]) => {
      inView = entry.isIntersecting
      if (inView) onScroll()
    }, { rootMargin: '100px 0px', threshold: 0 })
    if (sectionRef.value) observer.observe(sectionRef.value)
  } else {
    inView = true
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('scroll', onScroll)
  window.removeEventListener('resize', onScroll)
  mediaQuery?.removeEventListener?.('change', configureMotion)
  desktopQuery?.removeEventListener?.('change', configureMotion)
  observer?.disconnect()
  if (frame) window.cancelAnimationFrame(frame)
})
</script>

<style scoped>
.uct {
  --uct-stage: #0b1220;
  position: relative;
  padding-top: 64px;
}
.uct-heading { position: relative; z-index: 2; }
.uct-track { position: relative; height: 220vh; min-height: 1500px; margin-top: 16px; }
.uct-sticky { position: sticky; top: 96px; display: flex; align-items: center; min-height: min(620px, calc(100vh - 120px)); padding-block: 16px; }
.uct-layout { display: grid; grid-template-columns: minmax(230px, .66fr) minmax(0, 1.7fr); align-items: center; gap: 34px; width: 100%; }
.uct-copy { min-width: 0; }
.uct-scene-count { display: flex; align-items: center; gap: 10px; margin-bottom: 22px; color: var(--rl-muted); font-family: var(--ui-font-mono, ui-monospace, monospace); font-size: .7rem; }
.uct-scene-count i { display: block; width: 36px; height: 1px; background: var(--rl-line); }
.uct-copy-panel h3 { font-size: clamp(1.65rem, 2.5vw, 2.3rem); line-height: 1.3; letter-spacing: -.035em; }
.uct-copy-panel > p { margin-top: 18px; color: var(--rl-muted); font-size: .9375rem; line-height: 1.9; }
.uct-copy-panel ul { display: grid; gap: 12px; margin: 26px 0 0; padding: 0; list-style: none; }
.uct-copy-panel li { display: flex; align-items: flex-start; gap: 8px; color: var(--rl-ink); font-size: .8125rem; line-height: 1.65; }
.uct-copy-panel li svg { margin-top: 4px; color: var(--rl-accent); }
.uct--cinema .uct-copy-panel li svg { color: #d97706; }
.dark .uct--cinema .uct-copy-panel li svg { color: #fbbf24; }
.uct-actions { display: flex; align-items: center; min-height: 52px; margin-top: 30px; }
.uct-canvas-action, .uct-doc-action { display: inline-flex; align-items: center; gap: 8px; min-height: 44px; padding: 10px 16px; border: 1px solid rgb(217 119 6 / .32); border-radius: 999px; background: rgb(245 158 11 / .12); color: #b45309; font-size: .8125rem; font-weight: 650; transition: transform 180ms var(--ui-ease-out), background 180ms ease; }
.uct-doc-action { border-color: var(--rl-line); background: var(--glass-control-fill); color: var(--glass-action-ink); }
.dark .uct-canvas-action { color: #fde68a; border-color: rgb(251 191 36 / .32); }
.uct-canvas-action:hover, .uct-doc-action:hover { transform: translateY(-2px); }
.uct-scroll-hint { display: inline-flex; align-items: center; gap: 9px; color: var(--rl-muted); font-size: .75rem; }
.uct-scroll-hint i { display: block; width: 18px; height: 28px; border: 1px solid var(--rl-line); border-radius: 999px; }
.uct-scroll-hint i::after { content: ''; display: block; width: 3px; height: 6px; margin: 5px auto 0; border-radius: 999px; background: var(--rl-accent); animation: uct-scroll 1.8s ease-in-out infinite; }
.uct-device { position: relative; min-width: 0; overflow: hidden; border: 1px solid rgb(255 255 255 / .16); border-radius: 26px; background: var(--uct-stage); box-shadow: inset 0 1px 0 rgb(255 255 255 / .18), 0 34px 70px -30px rgb(15 23 42 / .58), 0 0 70px -35px rgb(45 212 191 / .3); transform: perspective(1500px) rotateY(-3deg) rotateX(1deg); transition: box-shadow 450ms ease, transform 600ms var(--ui-ease-out); }
.uct--cinema .uct-device { transform: perspective(1500px) rotateY(0deg) scale(1.015); box-shadow: inset 0 1px 0 rgb(255 255 255 / .18), 0 34px 70px -30px rgb(15 23 42 / .58), 0 0 85px -35px rgb(245 158 11 / .25); }
.uct--gateway .uct-device { transform: perspective(1500px) rotateY(3deg) rotateX(1deg); }
.uct-device-bar { display: grid; grid-template-columns: 1fr auto 1fr; align-items: center; gap: 12px; min-height: 46px; padding: 0 18px; border-bottom: 1px solid rgb(148 163 184 / .16); color: #94a3b8; background: rgb(15 23 42 / .9); }
.uct-window-dots { display: flex; gap: 6px; }
.uct-window-dots i { width: 7px; height: 7px; border-radius: 50%; background: #fb7185; }
.uct-window-dots i:nth-child(2) { background: #fbbf24; }
.uct-window-dots i:nth-child(3) { background: #2dd4bf; }
.uct-device-title { font-size: .6875rem; font-weight: 550; }
.uct-live { display: inline-flex; align-items: center; justify-self: end; gap: 6px; color: #5eead4; font-family: var(--ui-font-mono, ui-monospace, monospace); font-size: .5625rem; letter-spacing: .1em; }
.uct-live i, .uct-render-status i { width: 5px; height: 5px; border-radius: 50%; background: currentColor; box-shadow: 0 0 8px currentColor; }
.uct-svg { display: block; width: 100%; height: auto; color: #e2e8f0; }
.uct-glass-card { fill: #111c31; fill-opacity: .9; stroke: #67e8f9; stroke-opacity: .2; stroke-width: 1; }
.uct-glass-card--gold { stroke: #fbbf24; stroke-opacity: .3; }
.uct-terminal-card { fill: #080d18; stroke: #94a3b8; stroke-opacity: .16; }
.uct-node { fill: #f8fafc; fill-opacity: .035; stroke: #94a3b8; stroke-opacity: .2; }
.uct-svg-label { font-family: var(--ui-font, -apple-system, sans-serif); font-size: 11px; font-weight: 600; letter-spacing: .025em; }
.uct-svg-label:not([fill]) { fill: #e2e8f0; }
.uct-svg-micro { font-family: var(--ui-font-mono, ui-monospace, monospace); font-size: 9px; letter-spacing: .015em; }
.uct-svg-micro:not([fill]) { fill: #94a3b8; }
.uct-code { font-family: var(--ui-font-mono, ui-monospace, monospace); font-size: 13px; }
.uct-wire-base { fill: none; stroke: #94a3b8; stroke-opacity: .28; stroke-width: 1.3; }
.uct-wire-pulse { fill: none; stroke-width: 2; stroke-dasharray: 34 280; stroke-dashoffset: 314; animation: uct-beam 3.4s linear infinite; }
.uct-wire-pulse--late { animation-delay: -1.7s; }
.uct-cursor { animation: uct-blink 1.2s steps(2, start) infinite; }
.uct-equalizer rect { transform-origin: center bottom; transform-box: fill-box; animation: uct-eq 1.4s ease-in-out infinite alternate; }
.uct-equalizer rect:nth-child(2) { animation-delay: -.4s; }
.uct-equalizer rect:nth-child(3) { animation-delay: -.7s; }
.uct-equalizer rect:nth-child(4) { animation-delay: -.2s; }
.uct-reticle { transform-origin: 464px 70px; animation: uct-reticle 3s ease-in-out infinite; }
.uct-scan { animation: uct-scan 5.2s linear infinite; }
.uct-timeline-head { animation: uct-playhead 4s ease-in-out infinite alternate; }
.uct-orbit { animation: uct-spin 28s linear infinite; }
.uct-orbit--reverse { animation: uct-spin-reverse 22s linear infinite; }
.uct-core-pulse { animation: uct-pulse 2.8s ease-in-out infinite; }
.uct-scene { animation: uct-scene-in 480ms var(--ui-ease-out) both; }
.uct-progress { transition: transform 500ms var(--ui-ease-out), fill 350ms ease; }
.uct-device-foot { display: flex; align-items: center; justify-content: space-between; gap: 16px; min-height: 42px; padding: 8px 18px; border-top: 1px solid rgb(148 163 184 / .16); background: rgb(15 23 42 / .92); color: #94a3b8; font-size: .625rem; line-height: 1.6; }
.uct-render-status { display: inline-flex; align-items: center; gap: 6px; flex-shrink: 0; color: #5eead4; }
.uct-canvas-empty { margin-top: 24px; text-align: center; color: var(--rl-muted); font-size: .75rem; }
.uct--static .uct-track { height: auto; min-height: 0; margin-top: 34px; }
.uct--static .uct-sticky { position: relative; top: auto; min-height: 0; padding: 0; }
.uct--static .uct-scroll-hint i { width: 8px; height: 8px; border: 0; background: var(--rl-accent); }
.uct--static .uct-scroll-hint i::after { display: none; }
.uct :is(a, button):focus-visible { outline: 3px solid var(--rl-accent); outline-offset: 4px; }
@keyframes uct-scene-in { from { opacity: .1; transform: translateY(8px); } to { opacity: 1; transform: none; } }
@keyframes uct-beam { from { stroke-dashoffset: 314; } to { stroke-dashoffset: 0; } }
@keyframes uct-blink { 0%, 49% { opacity: 1; } 50%, 100% { opacity: 0; } }
@keyframes uct-eq { from { transform: scaleY(.4); } to { transform: scaleY(1); } }
@keyframes uct-reticle { 0%, 100% { opacity: .4; transform: scale(.92); } 50% { opacity: 1; transform: scale(1.08); } }
@keyframes uct-scan { from { transform: translateX(0); } to { transform: translateX(624px); } }
@keyframes uct-playhead { from { transform: translateX(-40px); } to { transform: translateX(48px); } }
@keyframes uct-spin { to { transform: rotate(360deg); } }
@keyframes uct-spin-reverse { to { transform: rotate(-360deg); } }
@keyframes uct-pulse { 50% { opacity: .5; } }
@keyframes uct-scroll { 0% { opacity: 0; transform: translateY(0); } 35% { opacity: 1; } 100% { opacity: 0; transform: translateY(10px); } }
@media (min-width: 1024px) {
  .uct { width: min(1320px, calc(100% - 64px)); }
}
@media (max-width: 1100px) {
  .uct-layout { grid-template-columns: minmax(200px, .55fr) minmax(0, 1.45fr); gap: 26px; }
  .uct-copy-panel h3 { font-size: 1.65rem; }
}
@media (max-width: 1023px) {
  .uct-layout { grid-template-columns: 1fr; gap: 24px; }
  .uct-copy { display: grid; grid-template-columns: minmax(0, 1fr) auto; column-gap: 24px; align-items: end; }
  .uct-scene-count { grid-column: 1 / -1; margin-bottom: 14px; }
  .uct-copy-panel { grid-column: 1 / -1; }
  .uct-copy-panel > p { max-width: 42rem; }
  .uct-copy-panel ul { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 14px; margin-top: 20px; }
  .uct-actions { grid-column: 1 / -1; margin-top: 20px; }
  .uct-device { transform: none; }
  .uct--cinema .uct-device, .uct--gateway .uct-device { transform: none; }
}
@media (max-width: 640px) {
  .uct { padding-top: 44px; }
  .uct-copy-panel h3 { font-size: 1.55rem; }
  .uct-copy-panel > p { font-size: .875rem; margin-top: 12px; }
  .uct-copy-panel ul { grid-template-columns: 1fr; gap: 8px; }
  .uct-copy-panel li { font-size: .75rem; }
  .uct-device { border-radius: 20px; }
  .uct-device-bar { min-height: 40px; padding-inline: 12px; }
  .uct-device-title { font-size: .5625rem; }
  .uct-device-foot { align-items: flex-start; padding: 10px 12px; font-size: .5625rem; }
  .uct-svg-label { font-size: 14px; }
  .uct-svg-micro { font-size: 11px; }
  .uct-code { font-size: 15px; }
  .uct-svg { min-height: 210px; }
}
@media (prefers-reduced-motion: reduce) {
  .uct *, .uct *::before, .uct *::after { animation: none !important; transition: none !important; }
  .uct-device, .uct--cinema .uct-device, .uct--gateway .uct-device { transform: none; }
}
</style>
