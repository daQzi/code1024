import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { describe, expect, it } from 'vitest'
import HostSelect from './HostSelect.vue'

const options = [{ value: '', label: 'All' }, { value: 'running', label: 'Running' }]

describe('host select adapter', () => {
  it('keeps native selection available without an injected component', async () => {
    const wrapper = mount(HostSelect, { props: { modelValue: '', options, label: 'Task status' } })
    await wrapper.get('select').setValue('running')
    expect(wrapper.emitted('update:modelValue')).toEqual([['running']])
    expect(wrapper.get('select').attributes('aria-label')).toBe('Task status')
    wrapper.unmount()
  })

  it('forwards values, labels, attributes and disabled state to the host component', async () => {
    const selectComponent = defineComponent({
      props: ['modelValue', 'options', 'disabled', 'ariaLabel'],
      emits: ['update:modelValue'],
      template: '<button type="button" :disabled="disabled" :aria-label="ariaLabel" @click="$emit(\'update:modelValue\', options[1].value)">{{ modelValue }}</button>'
    })
    const wrapper = mount(HostSelect, {
      props: { selectComponent, modelValue: '', options, label: 'Task status' },
      attrs: { 'data-filter': 'status' }
    })
    expect(wrapper.find('select').exists()).toBe(false)
    expect(wrapper.get('button').attributes('aria-label')).toBe('Task status')
    expect(wrapper.get('button').attributes('data-filter')).toBe('status')
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([['running']])
    await wrapper.setProps({ modelValue: 'running', disabled: true })
    expect(wrapper.get('button').text()).toBe('running')
    expect(wrapper.get<HTMLButtonElement>('button').element.disabled).toBe(true)
    wrapper.unmount()
  })
})
