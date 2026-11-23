import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import NewAPIPluginModels from '../NewAPIPluginModels.vue'
import { queryNewAPIPluginModels, type NewAPIPluginModels as Models } from '@/api/admin/newapiPlugins'

const { run } = vi.hoisted(() => ({ run: vi.fn((action: () => Promise<unknown>) => action()) }))
vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run }), isStepUpCancelled: () => false }))
vi.mock('@/api/admin/newapiPlugins', () => ({ queryNewAPIPluginModels: vi.fn() }))
const models = (): Models => ({ account_id: 17, plugin_version_id: 2, fetched_at: '2026-11-22T00:00:00Z', models: ['model-one', '<script>alert(1)</script>'] })
function render() { return mount(NewAPIPluginModels, { props: { accountId: 17, versionId: 2 }, global: { stubs: { TotpStepUpDialog: true } } }) }

describe('plugin account model discovery', () => {
  beforeEach(() => { vi.clearAllMocks(); vi.mocked(queryNewAPIPluginModels).mockResolvedValue(models()) })
  it('queries the saved account through step-up only on explicit action and presents selectable plain text', async () => {
    const wrapper = render()
    expect(queryNewAPIPluginModels).not.toHaveBeenCalled()
    expect(wrapper.get('[data-models-query]').attributes('type')).toBe('button')
    await wrapper.get('[data-models-query]').trigger('click')
    await flushPromises()
    expect(run).toHaveBeenCalledOnce()
    expect(queryNewAPIPluginModels).toHaveBeenCalledWith(17, expect.any(AbortSignal))
    expect((wrapper.get('[data-models-result]').element as HTMLTextAreaElement).value).toBe('model-one\n<script>alert(1)</script>')
    expect(wrapper.get('[data-models-result]').attributes('readonly')).toBeDefined()
    expect(wrapper.find('script').exists()).toBe(false)
    wrapper.unmount()
  })
  it('renders an authoritative empty list distinctly', async () => {
    vi.mocked(queryNewAPIPluginModels).mockResolvedValueOnce({ ...models(), models: [] })
    const wrapper = render()
    await wrapper.get('[data-models-query]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-models-empty]').text()).toBe('admin.plugins.js.modelsEmpty')
    expect(wrapper.find('[data-models-result]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('cancels changed connections and ignores late or mismatched data', async () => {
    let resolve!: (value: Models) => void
    vi.mocked(queryNewAPIPluginModels).mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const wrapper = render()
    await wrapper.get('[data-models-query]').trigger('click')
    await flushPromises()
    const signal = vi.mocked(queryNewAPIPluginModels).mock.calls[0]![1]!
    await wrapper.setProps({ versionId: 3 })
    expect(signal.aborted).toBe(true)
    resolve(models())
    await flushPromises()
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    await wrapper.get('[data-models-query]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.plugins.js.balanceChanged')
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('hides raw failures and cancels on close', async () => {
    vi.mocked(queryNewAPIPluginModels).mockRejectedValueOnce(new Error('private-credential'))
    const wrapper = render()
    await wrapper.get('[data-models-query]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.plugins.js.modelsFailed')
    expect(wrapper.text()).not.toContain('private-credential')
    let resolve!: (value: Models) => void
    vi.mocked(queryNewAPIPluginModels).mockImplementationOnce(() => new Promise(done => { resolve = done }))
    await wrapper.get('[data-models-query]').trigger('click')
    await flushPromises()
    const signal = vi.mocked(queryNewAPIPluginModels).mock.calls.at(-1)![1]!
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    resolve(models())
    await flushPromises()
  })
})
