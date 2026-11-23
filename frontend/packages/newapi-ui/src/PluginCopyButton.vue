<template>
  <button type="button" class="shrink-0 rounded-lg p-1.5 text-gray-500 hover:bg-gray-100 focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-dark-400 dark:hover:bg-dark-700" :aria-label="label" :title="feedback || label" @click="copy">
    <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path v-if="copied" d="m5 12 4 4L19 6" /><template v-else><rect x="8" y="8" width="12" height="12" rx="2" /><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3" /></template></svg>
    <span class="sr-only" role="status">{{ feedback }}</span>
  </button>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
const props = defineProps<{ value: string; label: string; failedLabel: string }>()
const copied = ref(false)
const feedback = ref('')
let timer: ReturnType<typeof setTimeout> | undefined
async function copy() {
  try {
    await navigator.clipboard.writeText(props.value)
    copied.value = true
    feedback.value = ''
  } catch {
    feedback.value = props.failedLabel
  }
  clearTimeout(timer)
  timer = setTimeout(() => { copied.value = false; feedback.value = '' }, 2000)
}
onBeforeUnmount(() => clearTimeout(timer))
</script>
