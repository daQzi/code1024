<template>
  <div class="flex flex-wrap items-center gap-1.5">
    <span v-for="model in visible" :key="model" class="max-w-full break-all rounded-md border border-gray-200 bg-gray-50 px-2 py-0.5 font-mono text-xs leading-5 text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-200">{{ model }}</span>
    <button v-if="models.length > limit && expandable" type="button" class="rounded-md px-2 py-1 text-xs text-primary-600 hover:bg-primary-50 dark:text-primary-400 dark:hover:bg-dark-800" :aria-expanded="expanded" @click="expanded = !expanded">{{ expanded ? collapseLabel : moreLabel }} <span v-if="!expanded">({{ models.length - limit }})</span></button>
    <span v-else-if="models.length > limit" class="badge badge-gray" :title="models.slice(limit).join('\n')">+{{ models.length - limit }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
const props = withDefaults(defineProps<{ models: string[]; limit?: number; expandable?: boolean; moreLabel?: string; collapseLabel?: string }>(), { limit: 4, expandable: false })
const expanded = ref(false)
const visible = computed(() => expanded.value ? props.models : props.models.slice(0, props.limit))
</script>
