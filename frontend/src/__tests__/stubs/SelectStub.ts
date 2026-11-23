import { defineComponent, h, type PropType } from 'vue'
import type { SelectOption } from '@/components/common/Select.vue'

export default defineComponent({
  name: 'SelectStub',
  props: {
    modelValue: { type: [String, Number, Boolean], default: null },
    options: { type: Array as PropType<SelectOption[]>, default: () => [] },
    disabled: Boolean,
    remote: Boolean,
    ariaLabel: String,
    id: String
  },
  emits: ['update:modelValue', 'change', 'search'],
  setup(props, { emit }) {
    return () => h('select', {
      value: props.modelValue,
      disabled: props.disabled,
      'aria-label': props.ariaLabel,
      id: props.id,
      onChange(event: Event) {
        const option = props.options.find(option => String(option.value) === (event.target as HTMLSelectElement).value)
        if (!option || option.disabled || props.disabled) return
        emit('update:modelValue', option.value)
        emit('change', option.value, option)
      }
    }, props.options.map(option => h('option', {
      value: option.value,
      disabled: option.disabled
    }, option.label)))
  }
})
