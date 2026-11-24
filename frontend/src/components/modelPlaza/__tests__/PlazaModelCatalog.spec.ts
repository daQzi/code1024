import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import PlazaModelCatalog from '../PlazaModelCatalog.vue'
import type { ModelPlazaGroup } from '@/api/modelPlaza'
import { getModelPlazaPerformanceSummaries } from '@/api/modelPlaza'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }) }))
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
  afterEach(() => { vi.useRealTimers(); vi.clearAllMocks() })
  it.each([['upstream_cost', 'dynamicPrice'], ['expression', 'expressionPrice']])('labels %s pricing without advertising a free price', async (unit, label) => {
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: '/model-plaza', component: { template: '<div />' } },
      { path: '/model-plaza/models/:modelId', name: 'ModelPlazaDetail', component: { template: '<div />' } }
    ] })
    await router.push('/model-plaza')
    const item = group(1, 'mdl_dynamic')
    item.models[0]!.protocols = ['openai_video']
    item.models[0]!.generation_pricing = [{ unit, unit_price: '0', minimum: '0', service_fee: '0' }]
    const wrapper = mount(PlazaModelCatalog, { props: { groups: [item] }, global: { plugins: [router] } })
    expect(wrapper.text()).toContain(`modelPlaza.catalog.${label}`)
    expect(wrapper.text()).not.toContain('modelPlaza.catalog.priceFrom')
    await flushPromises()
    wrapper.unmount()
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
    expect(getModelPlazaPerformanceSummaries).toHaveBeenCalledWith(['mdl_b', 'mdl_a'], expect.any(AbortSignal), undefined)
    await links[0].trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('ModelPlazaDetail')
    expect(router.currentRoute.value.params.modelId).toBe('mdl_b')
    wrapper.unmount()
  })
  it('scopes metrics and independent detail links to the selected group', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: '/model-plaza', component: { template: '<div />' } },
      { path: '/model-plaza/models/:modelId', name: 'ModelPlazaDetail', component: { template: '<div />' } }
    ] })
    await router.push('/model-plaza?embedded=1&group_id=2')
    const wrapper = mount(PlazaModelCatalog, { props: { groups: [group(2, 'mdl_a')], groupId: 2 }, global: { plugins: [router] } })
    await flushPromises()
    expect(getModelPlazaPerformanceSummaries).toHaveBeenLastCalledWith(['mdl_a'], expect.any(AbortSignal), 2)
    expect(wrapper.find('a').attributes('href')).toBe('/model-plaza/models/mdl_a?embedded=1&group_id=2')
    await wrapper.setProps({ groups: [group(3, 'mdl_a')], groupId: 3 })
    await flushPromises()
    expect(getModelPlazaPerformanceSummaries).toHaveBeenLastCalledWith(['mdl_a'], expect.any(AbortSignal), 3)
    wrapper.unmount()
  })
  it('shows gray unsampled hours and polls visible metrics until unmounted', async () => {
    vi.useFakeTimers()
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: '/model-plaza', component: { template: '<div />' } },
      { path: '/model-plaza/models/:modelId', name: 'ModelPlazaDetail', component: { template: '<div />' } }
    ] })
    await router.push('/model-plaza')
    const wrapper = mount(PlazaModelCatalog, { props: { groups: [group(1, 'mdl_a')] }, global: { plugins: [router] } })
    await flushPromises()
    expect(wrapper.findAll('[role="img"] span')).toHaveLength(24)
    expect(wrapper.findAll('[role="img"] span').every(span => span.classes().includes('bg-gray-200'))).toBe(true)
    const before = vi.mocked(getModelPlazaPerformanceSummaries).mock.calls.length
    await vi.advanceTimersByTimeAsync(15000)
    expect(getModelPlazaPerformanceSummaries).toHaveBeenCalledTimes(before + 1)
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(15000)
    expect(getModelPlazaPerformanceSummaries).toHaveBeenCalledTimes(before + 1)
  })

  it('copies model ID from card header button and reveals health tooltip on hover', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText }
    })
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: '/model-plaza', component: { template: '<div />' } },
      { path: '/model-plaza/models/:modelId', name: 'ModelPlazaDetail', component: { template: '<div />' } }
    ] })
    await router.push('/model-plaza')
    const wrapper = mount(PlazaModelCatalog, {
      props: { groups: [group(1, 'mdl_copy')] },
      global: { plugins: [router], stubs: { Transition: { inheritAttrs: false, template: '<slot />' } } }
    })
    await flushPromises()

    const copyBtn = wrapper.get('button[aria-label="modelPlaza.table.copyModelId"]')
    await copyBtn.trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('vendor/model/video')
    expect(copyBtn.classes()).toContain('is-copied')

    const buckets = wrapper.findAll('[role="img"] span')
    expect(buckets).toHaveLength(24)
    await buckets[0]!.trigger('mouseenter')
    expect(wrapper.find('[role="tooltip"]').exists()).toBe(true)

    await wrapper.find('.health-bar').trigger('mouseleave')
    expect(wrapper.find('[role="tooltip"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
