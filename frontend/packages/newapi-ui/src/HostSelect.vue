<template>
  <component
    :is="select"
    v-if="select"
    v-bind="$attrs"
    :model-value="modelValue"
    :options="options"
    :disabled="disabled"
    :aria-label="label"
    @update:model-value="emit('update:modelValue', $event)"
  />
  <select
    v-else
    v-bind="$attrs"
    class="input"
    :value="modelValue"
    :disabled="disabled"
    :aria-label="label"
    @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value as Value)"
  >
    <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
  </select>
</template>

<script setup lang="ts" generic="Value extends string">
import { computed, toRaw, type Component } from 'vue'

defineOptions({ inheritAttrs: false })
const props = defineProps<{
  modelValue: Value
  options: { value: Value; label: string }[]
  label: string
  selectComponent?: Component
  disabled?: boolean
}>()
const select = computed(() => props.selectComponent ? toRaw(props.selectComponent) : undefined)
const emit = defineEmits<{ 'update:modelValue': [value: Value] }>()
</script>
