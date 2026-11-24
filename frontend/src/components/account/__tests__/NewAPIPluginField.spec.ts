import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import NewAPIPluginField from '../NewAPIPluginField.vue'
import { getNewAPIPluginVersion, listNewAPIPlugins } from '@/api/admin/newapiPlugins'
import Select from '@/components/common/Select.vue'
import SelectStub from '@/__tests__/stubs/SelectStub'

vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh' } }) }))
vi.mock('@/api/admin/newapiPlugins', () => ({
  getNewAPIPluginVersion: vi.fn(),
  listNewAPIPlugins: vi.fn().mockResolvedValue({ plugins: [
    { id: 1, version: '1.0.0', enabled: true, meta: { name: 'Text Provider', relay: [{ incoming_path: '/v1/models' }] }, capabilities: { provider: true, hooks: ['buildBalanceRequest', 'parseBalance'] }, compatibility: { kinds: ['provider'] } },
    { id: 2, version: '1.0.0', enabled: true, meta: { name: 'Video Task' }, capabilities: { task: true }, compatibility: { kinds: ['task'] } },
    { id: 3, version: '1.0.0', enabled: true, meta: { name: 'Unsupported Provider' }, capabilities: { provider: true }, compatibility: { kinds: [] } },
    { id: 4, version: '1.0.0', enabled: true, meta: { name: 'Login Provider' }, capabilities: { provider: true, hooks: ['login', 'refresh', 'scheduled'] }, compatibility: { kinds: ['provider'] } },
    { id: 5, version: '1.0.0', enabled: true, meta: { name: 'Login Video Task' }, capabilities: { task: true, hooks: ['login', 'refresh', 'revoke', 'scheduled'] }, compatibility: { kinds: ['task'] } },
    { id: 6, version: '1.0.0', enabled: true, meta: { name: 'Generic Video', models: ['*'], description: { zh: '默认 multipart；仅在上游支持时选择 JSON。', en: 'Use multipart by default.' }, protocols: [{ name: 'openai_video' }], channelConfig: { type: 'object', properties: { submission_format: { type: 'string', enum: ['multipart', 'json'] } } } }, capabilities: { task: true }, compatibility: { kinds: ['task'] } },
    { id: 7, version: '1.0.0', enabled: true, meta: { name: 'Explicit Video', models: ['*'], protocols: [{ name: 'openai_video', models: ['known-video'] }] }, capabilities: { task: true }, compatibility: { kinds: ['task'] } },
    { id: 9, version: '1.0.0', enabled: true, meta: { name: 'Prefix Wildcard Video', models: ['cc-seedance-*'], protocols: [{ name: 'openai_video' }] }, capabilities: { task: true }, compatibility: { kinds: ['task'] } },
  ] }),
}))

describe('account protocol plugin selection', () => {
  it('preserves numeric plugin and enum values through the actual host dropdown', async () => {
    vi.mocked(listNewAPIPlugins).mockResolvedValueOnce({ plugins: [{
      id: 8, enabled: true, version: '1', meta: { name: 'Numeric config', channelConfig: { type: 'object', properties: {
        retries: { type: 'integer', enum: [0, 1] }
      } } }, compatibility: { kinds: ['provider'] }, capabilities: { provider: true }
    }] } as Awaited<ReturnType<typeof listNewAPIPlugins>>)
    const wrapper = mount(NewAPIPluginField)
    await flushPromises()
    await wrapper.findComponent(Select).get('button').trigger('click')
    Array.from(document.body.querySelectorAll<HTMLElement>('[role="option"]')).find(option => option.textContent?.includes('Numeric config'))!.click()
    await flushPromises()
    const field = wrapper.findAllComponents(Select)[2]!
    await field.get('button').trigger('click')
    Array.from(document.body.querySelectorAll<HTMLElement>('[role="option"]')).find(option => option.textContent?.trim() === '0')!.click()
    await flushPromises()
    expect(field.props('modelValue')).toBe(0)
    const api = wrapper.vm as unknown as { apply: (value: Record<string, unknown>) => Record<string, unknown> }
    expect(api.apply({})).toMatchObject({ newapi_plugin_version_id: 8, newapi_plugin_config: { retries: 0 } })
    wrapper.unmount()
  })
  it('shows localized instructions and concrete model requirements for generic video plugins', async () => {
    const wrapper = mount(NewAPIPluginField, {
      global: { stubs: { Select: SelectStub, NewAPIVideoPricingField: { template: '<div />', methods: { rules: () => ({}) } } } },
    })
    await flushPromises()
    await wrapper.find('select').setValue(6)
    expect(wrapper.text()).toContain('默认 multipart；仅在上游支持时选择 JSON。')
    expect(wrapper.text()).toContain('admin.plugins.js.genericVideoHint')
    const format = wrapper.findAll('select')[2]!
    await format.setValue('json')
    const api = wrapper.vm as unknown as { apply: (value: Record<string, unknown>) => Record<string, unknown> }
    expect(api.apply({ model_mapping: { 'my-video': 'vendor-movie' } }).newapi_plugin_config).toEqual({ submission_format: 'json' })
    await wrapper.find('select').setValue(7)
    expect(wrapper.text()).not.toContain('admin.plugins.js.genericVideoHint')
    await wrapper.find('select').setValue(9)
    expect(wrapper.text()).toContain('admin.plugins.js.genericVideoHint')
    await wrapper.find('select').setValue(0)
    expect(wrapper.text()).not.toContain('默认 multipart')
    wrapper.unmount()
  })
  it('uses the same saved credential controls for Task-only video plugins', async () => {
    const wrapper = mount(NewAPIPluginField, {
      props: { accountId: 17, credentials: { newapi_plugin_version_id: 5 } },
      global: { stubs: { Select: SelectStub, NewAPIPluginCredential: { template: '<div data-task-login />' }, NewAPIVideoPricingField: { template: '<div data-task-price />' }, NewAPIPluginModels: true, NewAPIPluginBalance: true } },
    })
    await flushPromises()
    const api = wrapper.vm as unknown as { requiresLogin: boolean }
    expect(api.requiresLogin).toBe(true)
    expect(wrapper.find('[data-task-login]').exists()).toBe(true)
    expect(wrapper.find('[data-task-price]').exists()).toBe(true)
    await wrapper.setProps({ accountId: undefined })
    expect(wrapper.find('[data-task-login]').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.plugins.credential.saveFirst')
    wrapper.unmount()
  })
  it('allows a saved login provider without a dummy key and retains its explicit pause policy', async () => {
    const wrapper = mount(NewAPIPluginField, {
      props: { accountId: 17, credentials: { newapi_plugin_version_id: 4, newapi_plugin_auto_disable: true } },
      global: { stubs: { Select: SelectStub, NewAPIPluginCredential: { template: '<div data-login-card />' }, NewAPIVideoPricingField: true, NewAPIPluginModels: true, NewAPIPluginBalance: true } },
    })
    await flushPromises()
    const api = wrapper.vm as unknown as { requiresLogin: boolean; apply: (value: Record<string, unknown>) => Record<string, unknown> }
    expect(api.requiresLogin).toBe(true)
    expect(wrapper.find('[data-login-card]').exists()).toBe(true)
    expect(api.apply({}).newapi_plugin_auto_disable).toBe(true)
    await wrapper.find('select').setValue(1)
    expect(api.requiresLogin).toBe(false)
    expect(wrapper.find('[data-login-card]').exists()).toBe(false)
    expect(api.apply({}).newapi_plugin_auto_disable).toBe(false)
    await wrapper.find('select').setValue(4)
    expect(api.requiresLogin).toBe(true)
    expect(api.apply({}).newapi_plugin_auto_disable).toBe(true)
    await wrapper.find('select').setValue(0)
    expect(api.apply({ newapi_plugin_auto_disable: true }).newapi_plugin_auto_disable).toBeUndefined()
    wrapper.unmount()
  })
  it('shows model discovery only for a saved revision with an explicit discovery route', async () => {
    const wrapper = mount(NewAPIPluginField, {
      props: { accountId: 17, credentials: { newapi_plugin_version_id: 1 } },
      global: { stubs: { Select: SelectStub, NewAPIVideoPricingField: true, NewAPIPluginBalance: true, NewAPIPluginModels: { template: '<div data-discovery-card />' } } },
    })
    await flushPromises()
    expect(wrapper.find('[data-discovery-card]').exists()).toBe(true)
    await wrapper.find('select').setValue(2)
    expect(wrapper.find('[data-discovery-card]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('loads the saved historical revision without switching to the active version', async () => {
    vi.mocked(getNewAPIPluginVersion).mockResolvedValueOnce({ id: 91, version: '0.9.0', enabled: true, meta: { name: 'Saved provider' }, capabilities: { provider: true, hooks: ['buildBalanceRequest', 'parseBalance'] }, compatibility: { kinds: ['provider'] } } as never)
    const wrapper = mount(NewAPIPluginField, {
      props: { accountId: 17, credentials: { newapi_plugin_version_id: 91 } },
      global: { stubs: { Select: SelectStub, NewAPIPluginCredential: true, NewAPIVideoPricingField: true, NewAPIPluginModels: true, NewAPIPluginBalance: { template: '<div data-saved-balance />' } } },
    })
    await flushPromises()
    expect(getNewAPIPluginVersion).toHaveBeenLastCalledWith(91, expect.any(AbortSignal))
    expect((wrapper.find('select').element as HTMLSelectElement).value).toBe('91')
    expect(wrapper.find('[data-saved-balance]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Saved provider')
    wrapper.unmount()
  })

  it('cancels historical metadata reads on close and ignores the late result', async () => {
    let resolve!: (value: never) => void
    vi.mocked(getNewAPIPluginVersion).mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const wrapper = mount(NewAPIPluginField, {
      props: { accountId: 17, credentials: { newapi_plugin_version_id: 92 } },
      global: { stubs: { Select: SelectStub, NewAPIVideoPricingField: true, NewAPIPluginBalance: true } },
    })
    await flushPromises()
    const signal = vi.mocked(getNewAPIPluginVersion).mock.calls.at(-1)?.[1]
    wrapper.unmount()
    expect(signal?.aborted).toBe(true)
    resolve({ id: 92, meta: { name: 'Late metadata' } } as never)
    await flushPromises()
  })
  it('reloads a new account pin after the catalog is ready and cancels the previous metadata read', async () => {
    let resolve!: (value: never) => void
    vi.mocked(getNewAPIPluginVersion).mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const wrapper = mount(NewAPIPluginField, {
      props: { accountId: 17, credentials: { newapi_plugin_version_id: 1 } },
      global: { stubs: { Select: SelectStub, NewAPIVideoPricingField: true, NewAPIPluginModels: true, NewAPIPluginBalance: true } },
    })
    await flushPromises()
    await wrapper.setProps({ accountId: 18, credentials: { newapi_plugin_version_id: 93 } })
    await flushPromises()
    const signal = vi.mocked(getNewAPIPluginVersion).mock.calls.at(-1)![1]!
    expect(getNewAPIPluginVersion).toHaveBeenLastCalledWith(93, expect.any(AbortSignal))
    await wrapper.setProps({ accountId: 17, credentials: { newapi_plugin_version_id: 1 } })
    expect(signal.aborted).toBe(true)
    resolve({ id: 93, meta: { name: 'Late old account pin' } } as never)
    await flushPromises()
    expect(wrapper.text()).not.toContain('Late old account pin')
    expect((wrapper.find('select').element as HTMLSelectElement).value).toBe('1')
    wrapper.unmount()
  })
  it('shows balance only for the saved account revision and hides it while another plugin is selected', async () => {
    const wrapper = mount(NewAPIPluginField, {
      props: { accountId: 17, credentials: { newapi_plugin_version_id: 1 } },
      global: { stubs: { Select: SelectStub, NewAPIVideoPricingField: true, NewAPIPluginModels: true, NewAPIPluginBalance: { template: '<div data-saved-balance />' } } },
    })
    await flushPromises()
    expect(wrapper.find('[data-saved-balance]').exists()).toBe(true)
    await wrapper.find('select').setValue(2)
    expect(wrapper.find('[data-saved-balance]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('offers supported providers without exposing video pricing or erasing saved pricing', async () => {
    const pricing = { '*': { mode: 'fixed', unit: 'second', unit_price: 1 } }
    const wrapper = mount(NewAPIPluginField, {
      props: { credentials: { newapi_plugin_version_id: 1, newapi_video_pricing: pricing } },
      global: { stubs: { Select: SelectStub, NewAPIVideoPricingField: { template: '<div data-video-pricing />' } } },
    })
    await flushPromises()
    expect(wrapper.findAll('option').some(option => option.text().includes('Text Provider'))).toBe(true)
    expect(wrapper.findAll('option').some(option => option.text().includes('Unsupported Provider'))).toBe(false)
    expect(wrapper.find('[data-video-pricing]').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.plugins.js.providerHint')
    const api = wrapper.vm as unknown as { apply: (value: Record<string, unknown>) => Record<string, unknown> }
    expect(api.apply({ newapi_video_pricing: pricing }).newapi_video_pricing).toEqual(pricing)
    wrapper.unmount()
  })

  it('keeps video pricing available for task accounts', async () => {
    const wrapper = mount(NewAPIPluginField, {
      props: { credentials: { newapi_plugin_version_id: 2 } },
      global: { stubs: { Select: SelectStub, NewAPIVideoPricingField: { template: '<div data-video-pricing />' } } },
    })
    await flushPromises()
    expect(wrapper.find('[data-video-pricing]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('admin.plugins.js.providerHint')
    wrapper.unmount()
  })
})
