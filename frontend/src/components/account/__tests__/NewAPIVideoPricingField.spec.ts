import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import NewAPIVideoPricingField from '../NewAPIVideoPricingField.vue'
import SelectStub from '@/__tests__/stubs/SelectStub'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

function render(maximum?: string) {
  return mount(NewAPIVideoPricingField, { global: { stubs: { Select: SelectStub } }, props: { value: { '*': { mode: 'cost_plus', source_unit: 'upstream_cost', source_currency: 'CNY', exchange_rate: '0.14', markup: '1.2', service_fee: '0', minimum: '0', ...(maximum ? { maximum } : {}) } } } })
}
describe('final-bill pricing configuration', () => {
  it('requires an explicit positive maximum hold', () => {
    for (const limit of [undefined, '0']) {
      const wrapper = render(limit)
      expect(() => (wrapper.vm as unknown as { rules: () => unknown }).rules()).toThrow()
    }
  })
  it('saves final-bill pricing without inventing a source unit price', () => {
    const wrapper = render('10')
    const rule = (wrapper.vm as unknown as { rules: () => Record<string, Record<string, unknown>> }).rules()['*']!
    expect(rule.source_unit).toBe('upstream_cost')
    expect(rule.source_unit_price).toBeUndefined()
    expect(rule.maximum).toBe('10')
    expect(rule.exchange_rate).toBe('0.14')
  })
})

describe('Task expression pricing configuration', () => {
  it('offers declared models and only the selected model’s usage fields', async () => {
    const wrapper = mount(NewAPIVideoPricingField, {
      global: { stubs: { Select: SelectStub } },
      props: {
        value: { image: { mode: 'expression', expression: '', maximum: '10', extension: true } },
        pluginMeta: {
          name: 'Fixture', models: ['image', 'video'],
          usageSchema: { seconds: { type: 'number', unit: 'second' } },
          usageProfiles: [{ models: ['image'], schema: { image_count: { type: 'number', unit: 'count' } } }]
        }
      }
    })
    expect(wrapper.findAll('datalist option').map(option => option.attributes('value'))).toEqual(['image', 'video'])
    const reference = wrapper.find('[data-usage-reference]')
    expect(reference.text()).toContain('image_count')
    expect(reference.text()).not.toContain('seconds')
    await reference.find('button').trigger('click')
    expect(wrapper.find('textarea').element.value).toBe('u("image_count")')
    const rule = (wrapper.vm as unknown as { rules: () => Record<string, Record<string, unknown>> }).rules().image!
    expect(rule.expression).toBe('u("image_count")')
    expect(rule.extension).toBe(true)
    await wrapper.find('input[list]').setValue('video')
    expect(wrapper.find('[data-usage-reference]').text()).toContain('seconds')
    expect(wrapper.find('[data-usage-reference]').text()).not.toContain('image_count')
    wrapper.unmount()
  })

  function expression(value: Record<string, unknown> = {}) {
    return mount(NewAPIVideoPricingField, { global: { stubs: { Select: SelectStub } }, props: { value: { '*': { mode: 'expression', expression: 'u("seconds") * 0.2', maximum: '10', ...value } } } })
  }
  function rules(wrapper: ReturnType<typeof expression>) {
    return (wrapper.vm as unknown as { rules: () => Record<string, Record<string, unknown>> }).rules()
  }
  it('persists a USD expression without a unit price and keeps extension fields', () => {
    const wrapper = expression({ unit_price: '1', source_unit_price: '2', source_unit: 'token', exchange_rate: '0.1', extension: true })
    const rule = rules(wrapper)['*']!
    expect(rule).toEqual({ mode: 'expression', expression: 'u("seconds") * 0.2', maximum: '10', service_fee: '0', minimum: '0', extension: true })
    expect(wrapper.findAll('textarea')).toHaveLength(1)
  })
  it.each(['', ' ', '0', '-1', 'Infinity'])('requires a positive finite ceiling: %s', maximum => {
    expect(() => rules(expression({ maximum }))).toThrow()
  })
  it.each(['', ' ', '1'.repeat(4097), '中'.repeat(1366)])('validates expression byte length', body => {
    expect(() => rules(expression({ expression: body }))).toThrow()
  })
  it('rejects unrecognized pricing modes', () => {
    expect(() => rules(expression({ mode: 'unknown' }))).toThrow()
  })
  it('removes expression fields when switching back to fixed pricing', async () => {
    const wrapper = expression({ unit: 'second', unit_price: '0.4' })
    await wrapper.find('select').setValue('fixed')
    const rule = rules(wrapper)['*']!
    expect(rule.mode).toBe('fixed')
    expect(rule.unit_price).toBe('0.4')
    expect(rule.expression).toBeUndefined()
  })
  it('resets the final-bill unit when switching to fixed pricing', async () => {
    const wrapper = render('10')
    await wrapper.findAll('select')[0]!.setValue('fixed')
    await wrapper.findAll('input[type="number"]')[0]!.setValue('0.2')
    const rule = rules(wrapper)['*']!
    expect(rule.unit).toBe('second')
    expect(rule.source_unit).toBeUndefined()
  })
})
