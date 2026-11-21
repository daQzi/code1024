import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import NewAPIVideoPricingField from '../NewAPIVideoPricingField.vue'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

function render(maximum?: string) {
  return mount(NewAPIVideoPricingField, { props: { value: { '*': { mode: 'cost_plus', source_unit: 'upstream_cost', source_currency: 'CNY', exchange_rate: '0.14', markup: '1.2', service_fee: '0', minimum: '0', ...(maximum ? { maximum } : {}) } } } })
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
