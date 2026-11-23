import { describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import ModelPlazaDetail from '../ModelPlazaDetail.vue'
import type { ModelPlazaGroup } from '@/api/modelPlaza'

vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }) }))
vi.mock('@/api/modelPlaza', () => ({ getModelPlazaPerformance: vi.fn() }))

function group(id: number): ModelPlazaGroup {
  return { id, name: `group-${id}`, platform: 'openai', description: '', subscription_type: 'standard', rate_multiplier: 1, peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1,
    is_exclusive: false, image_rate_independent: false, image_rate_multiplier: 1, video_rate_independent: false, video_rate_multiplier: 1, long_context_pricing_enabled: false,
    models: [{ id: 'mdl_fixture', name: 'public/model', platform: 'openai', pricing: null, official_pricing: null }] }
}
async function routerAt(query = '') {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/model-plaza', name: 'ModelPlaza', component: { template: '<div />' } },
    { path: '/model-plaza/models/:modelId', component: { template: '<div />' } }
  ] })
  await router.push(`/model-plaza/models/mdl_fixture${query}`)
  return router
}

describe('model overview and declared protocols', () => {
  it('shows group-specific known and unknown capabilities without merging groups', async () => {
    const first = group(1), second = group(2)
    first.models[0]!.capabilities = { source: 'catalog_intersection', input_modalities: ['text', 'image'], context_window: 128000, max_output_tokens: 4000, reasoning: false }
    const router = await routerAt('?group_id=1&embedded=1')
    const wrapper = mount(ModelPlazaDetail, { props: { response: { description: '', groups: [first, second] }, loading: false }, global: { plugins: [router] } })
    const articles = wrapper.findAll('article')
    expect(articles).toHaveLength(2)
    expect(articles[0]!.text()).toContain('128,000 Token')
    expect(articles[0]!.text()).toContain('4,000 Token')
    expect(articles[0]!.text()).toContain('modelPlaza.catalog.declaredUnsupported')
    expect(articles[1]!.text()).toContain('modelPlaza.catalog.capabilityUnknown')
    expect(articles[1]!.text()).not.toContain('128,000 Token')
    const apiTab = wrapper.findAll('nav button').find(button => button.text() === 'modelPlaza.catalog.tabs.api')!
    await apiTab.trigger('click'); await flushPromises()
    expect(router.currentRoute.value.query).toMatchObject({ group_id: '1', embedded: '1', tab: 'api' })
    expect(wrapper.findAll('pre')).toHaveLength(0)
    wrapper.unmount()
  })
  it('offers examples only for protocols returned by the server', async () => {
    const item = group(1)
    item.models[0]!.protocols = ['openai_chat']
    const router = await routerAt('?tab=api')
    const wrapper = mount(ModelPlazaDetail, { props: { response: { description: '', groups: [item] }, loading: false }, global: { plugins: [router] } })
    expect(wrapper.findAll('pre')).toHaveLength(1)
    expect(wrapper.find('pre').text()).toContain('/v1/chat/completions')
    expect(wrapper.find('pre').text()).toContain('public/model')
    expect(wrapper.find('pre').text()).not.toContain('/v1/responses')
    item.models[0]!.protocols = ['openai_responses', 'openai_video']
    await wrapper.setProps({ response: { description: '', groups: [item] } })
    expect(wrapper.findAll('pre')).toHaveLength(2)
    expect(wrapper.text()).toContain('/v1/responses')
    expect(wrapper.text()).toContain('/v1/videos')
    item.models[0]!.protocols = ['openai_embeddings']
    await wrapper.setProps({ response: { description: '', groups: [item] } })
    expect(wrapper.findAll('pre')).toHaveLength(1)
    expect(wrapper.text()).toContain('OpenAI Embeddings')
    expect(wrapper.find('pre').text()).toContain('/v1/embeddings')
    expect(wrapper.find('pre').text()).not.toContain('/v1/chat/completions')
    wrapper.unmount()
  })
})
