import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import PluginsView from '../PluginsView.vue'
import { adminAPI } from '@/api/admin'
import { activateNewAPIPlugin, listNewAPIPlugins, listNewAPIPluginVersions, restorePluginFactory, type NewAPIPluginVersion } from '@/api/admin/newapiPlugins'

const { run } = vi.hoisted(() => ({ run: vi.fn((action: () => Promise<unknown>) => action()) }))
vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key, locale: ref('en') }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run }), isStepUpCancelled: () => false }))
vi.mock('@/api/admin', () => ({ adminAPI: { plugins: { list: vi.fn().mockResolvedValue([{ plugin_key: 'native.test', name: 'Native transport', version: '1.0.0', state: 'disabled', description: '', compatibility: { compatible: true } }]) } } }))
vi.mock('@/api/admin/newapiPlugins', () => {
  const task = { id: 1, key: 'test-video', version: '1.0.0', sha256: 'a'.repeat(64), origin: 'builtin', meta: { name: 'Video driver', models: ['model-a'] }, capabilities: { task: true }, compatibility: { runnable: true, kinds: ['task'], warnings: [], blockers: [] }, enabled: false, active: true }
  const provider = { id: 2, key: 'test-provider', version: '1.0.0', sha256: 'b'.repeat(64), origin: 'uploaded', meta: { name: 'Provider driver', models: [] }, capabilities: { provider: true }, compatibility: { runnable: false, kinds: [], warnings: [], blockers: ['no host path'] }, enabled: false, active: true }
  const transform = { id: 3, key: 'test-transform', version: '1.0.0', sha256: 'c'.repeat(64), origin: 'uploaded', meta: { name: 'Ingress filter', models: [] }, capabilities: { transform: true, hooks: ['ingress'] }, compatibility: { runnable: true, kinds: ['transform'], warnings: [], blockers: [] }, enabled: false, active: true }
  return { getProviderToolPrices: vi.fn().mockResolvedValue({ prices: {}, revision: 'r1' }), saveProviderToolPrices: vi.fn(), deleteNewAPIPluginVersion: vi.fn(), dryRunNewAPIPluginVersion: vi.fn(), getNewAPIPluginPricing: vi.fn(), listNewAPIPlugins: vi.fn().mockResolvedValue({ plugins: [task, provider, transform], factories: [task], builtins: [], baseline: 'fixture' }), listNewAPIPluginVersions: vi.fn().mockResolvedValue([task]), activateNewAPIPlugin: vi.fn().mockResolvedValue(undefined), restorePluginFactory: vi.fn().mockResolvedValue(task), installNewAPIPlugin: vi.fn() }
})

function render() {
  return mount(PluginsView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, BaseDialog: { props: ['show'], template: '<section v-if="show" data-detail><slot /></section>' }, NativePluginsPanel: true, TotpStepUpDialog: true, RouterLink: { template: '<a><slot /></a>' } } } })
}

describe('independent New API plugin center', () => {
  beforeEach(() => vi.clearAllMocks())
  it.each(['resolve', 'reject'])('cancels stale history and ignores its late %s after switching plugins', async outcome => {
    const catalog = await listNewAPIPlugins()
    const previous = { ...catalog.plugins[0]!, id: 71, version: 'old-history-marker' }
    const next = { ...catalog.plugins[1]!, id: 72, version: 'new-history-marker' }
    let finish!: (items: NewAPIPluginVersion[]) => void
    let fail!: (err: Error) => void
    vi.mocked(listNewAPIPluginVersions).mockReturnValueOnce(new Promise((resolve, reject) => { finish = resolve; fail = reject })).mockResolvedValueOnce([next])
    const wrapper = render()
    await flushPromises()
    await wrapper.findAll('article')[0]!.find('button').trigger('click')
    const first = vi.mocked(listNewAPIPluginVersions).mock.calls[0]![1]!
    await wrapper.findAll('article')[1]!.find('button').trigger('click')
    await flushPromises()
    expect(first.aborted).toBe(true)
    if (outcome === 'resolve') finish([previous])
    else fail(new Error('old-history-marker'))
    await flushPromises()
    await wrapper.find('[data-detail]').findAll('button').find(button => button.text() === 'admin.plugins.detail.history')!.trigger('click')
    expect(wrapper.find('[data-detail]').text()).toContain('new-history-marker')
    expect(wrapper.text()).not.toContain('old-history-marker')
    const current = vi.mocked(listNewAPIPluginVersions).mock.calls.at(-1)![1]!
    wrapper.unmount()
    expect(current.aborted).toBe(true)
  })
  it('lists only JS plugins without depending on native extensions and rejects unsupported roles', async () => {
    const wrapper = render()
    await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(3)
    expect(wrapper.text()).not.toContain('Native transport')
    expect(adminAPI.plugins.list).not.toHaveBeenCalled()
    await wrapper.findAll('article')[1]!.find('button').trigger('click')
    await flushPromises()
    const enable = wrapper.find('[data-detail]').findAll('button').find(b => b.text() === 'admin.plugins.enable')!
    expect(enable.attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-detail]').text()).toContain('no host path')
    wrapper.unmount()
  })
  it('directs ingress-only plugins to independently controlled processing instances', async () => {
    const wrapper = render()
    await flushPromises()
    const transform = wrapper.findAll('article').find(card => card.text().includes('Ingress filter'))!
    expect(transform.text()).toContain('admin.plugins.instanceControlled')
    await transform.find('button').trigger('click')
    await flushPromises()
    const detail = wrapper.find('[data-detail]')
    expect(detail.text()).toContain('requestPlugins.title')
    expect(detail.findAll('button').some(b => b.text() === 'admin.plugins.enable')).toBe(false)
    expect(activateNewAPIPlugin).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('keeps activation and factory restore behind the existing step-up flow', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.find('article button').trigger('click')
    await flushPromises()
    const detail = wrapper.find('[data-detail]')
    await detail.findAll('button').find(b => b.text() === 'admin.plugins.enable')!.trigger('click')
    await flushPromises()
    expect(activateNewAPIPlugin).toHaveBeenCalledWith(1, true)
    expect(run).toHaveBeenCalledTimes(1)
    await detail.findAll('button').find(b => b.text() === 'admin.plugins.center.restore')!.trigger('click')
    await flushPromises()
    expect(restorePluginFactory).toHaveBeenCalledWith('test-video')
    expect(run).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
})
