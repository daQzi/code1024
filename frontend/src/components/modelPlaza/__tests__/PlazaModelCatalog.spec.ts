import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import PlazaModelCatalog from '../PlazaModelCatalog.vue'
import type { ModelPlazaGroup } from '@/api/modelPlaza'
import { getModelPlazaPerformanceSummaries } from '@/api/modelPlaza'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/modelPlaza', () => ({ getModelPlazaPerformanceSummaries: vi.fn().mockResolvedValue({ models: {} }) }))

function group(id: number, modelId: string, platform = 'openai'): ModelPlazaGroup {
  return {
    id, name: `group-${id}`, description: '', platform, subscription_type: 'standard',
    rate_multiplier: 1, peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1,
    is_exclusive: false, image_rate_independent: false, image_rate_multiplier: 1,
    video_rate_independent: false, video_rate_multiplier: 1, long_context_pricing_enabled: false,
    models: [{ id: modelId, name: 'vendor/model/video', platform, pricing: null, official_pricing: null }]
  }
}

describe('model catalog navigation', () => {
  it('labels final-bill pricing as dynamic instead of advertising a free price', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: '/model-plaza', component: { template: '<div />' } },
      { path: '/model-plaza/models/:modelId', name: 'ModelPlazaDetail', component: { template: '<div />' } }
    ] })
    await router.push('/model-plaza')
    const item = group(1, 'mdl_dynamic')
    item.models[0]!.protocols = ['openai_video']
    item.models[0]!.generation_pricing = [{ unit: 'upstream_cost', unit_price: '0', minimum: '0', service_fee: '0' }]
    const wrapper = mount(PlazaModelCatalog, { props: { groups: [item] }, global: { plugins: [router] } })
    expect(wrapper.text()).toContain('modelPlaza.catalog.dynamicPrice')
    expect(wrapper.text()).not.toContain('modelPlaza.catalog.priceFrom')
    await flushPromises()
  })
  it('deduplicates models across groups and links to a stable independent detail page', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: '/model-plaza', name: 'ModelPlaza', component: { template: '<div />' } },
      { path: '/model-plaza/models/:modelId', name: 'ModelPlazaDetail', component: { template: '<div />' } }
    ] })
    await router.push('/model-plaza?embedded=1')
    const wrapper = mount(PlazaModelCatalog, { props: { groups: [group(1, 'mdl_a'), group(2, 'mdl_a'), group(3, 'mdl_b', 'grok')] }, global: { plugins: [router] } })
    expect(wrapper.findAll('article')).toHaveLength(2)
    const links = wrapper.findAll('a')
    expect(links[0].attributes('href')).toBe('/model-plaza/models/mdl_b?embedded=1')
    expect(wrapper.text()).toContain('group-1')
    expect(wrapper.text()).toContain('group-2')
    await flushPromises()
    expect(getModelPlazaPerformanceSummaries).toHaveBeenCalledWith(['mdl_b', 'mdl_a'], expect.any(AbortSignal))
    await links[0].trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('ModelPlazaDetail')
    expect(router.currentRoute.value.params.modelId).toBe('mdl_b')
  })
})
