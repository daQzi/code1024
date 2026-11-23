import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { webcrypto } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PluginMarketplacePanel from './PluginMarketplacePanel.vue'
import SelectStub from '@/__tests__/stubs/SelectStub'
import { getNewAPIPluginSource, installNewAPIPlugin, savePluginMarketplaceSources, type NewAPIPluginVersion } from '@/api/admin/newapiPlugins'
import { fetchMarketplaceIndex, fetchMarketplacePinIndex, fetchMarketplaceSource, findMarketplacePluginByCode, sourceSHA256 } from '@/utils/pluginMarketplace'

const { run, index, pendingSource } = vi.hoisted(() => ({
  run: vi.fn((action: () => Promise<unknown>) => action()),
  index: { name: 'Example', plugins: [{ key: 'fixture', name: 'Fixture video', description: 'A sample', latest: '2.0.0', versions: [{ version: '2.0.0', kind: 'task', path: 'v2.js', sha256: 'b'.repeat(64), minApiVersion: 1, allowedHosts: [] }, { version: '1.0.0', kind: 'task', path: 'v1.js', sha256: 'a'.repeat(64), minApiVersion: 1, allowedHosts: [] }], models: [] }], revoked: [] },
  pendingSource: { current: undefined as undefined | ((source: string) => void) }
}))
vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key, locale: ref('en') }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run }), isStepUpCancelled: () => false }))
vi.mock('@/api/admin/newapiPlugins', () => ({ getNewAPIPluginSource: vi.fn(), getPluginFactorySource: vi.fn(), listPluginMarketplaceSources: vi.fn().mockResolvedValue([{ name: 'Example', index_url: 'https://example.test/index.json' }]), savePluginMarketplaceSources: vi.fn().mockImplementation(async items => items), installNewAPIPlugin: vi.fn().mockResolvedValue({ compatibility: { blockers: ['not implemented'], warnings: [] }, enabled: false }) }))
vi.mock('@/utils/pluginMarketplace', async original => ({ ...(await original<typeof import('@/utils/pluginMarketplace')>()), fetchMarketplaceIndex: vi.fn().mockImplementation(async () => structuredClone(index)), fetchMarketplacePinIndex: vi.fn(), findMarketplacePluginByCode: vi.fn(), fetchMarketplaceSource: vi.fn().mockImplementation(async (_url, version) => `// ${version.version}`) }))

function render(installedPlugins: NewAPIPluginVersion[] = []) { return mount(PluginMarketplacePanel, { props: { installedPlugins }, global: { stubs: { Select: SelectStub, BaseDialog: { props: ['show'], template: '<div v-if="show" data-dialog><slot /></div>' }, Pagination: true, TotpStepUpDialog: true } } }) }
async function open(wrapper: ReturnType<typeof render>) { await flushPromises(); await wrapper.find('article button').trigger('click'); await flushPromises() }
const button = (wrapper: ReturnType<typeof render>, key: string) => wrapper.findAll('button').find(item => item.text() === key)!

describe('external marketplace review and install', () => {
  beforeEach(() => { vi.clearAllMocks(); vi.stubGlobal('crypto', webcrypto); vi.mocked(fetchMarketplaceIndex).mockImplementation(async () => structuredClone(index)); vi.mocked(fetchMarketplaceSource).mockImplementation(async (_url, version) => `// ${version.version}`); pendingSource.current = undefined })
  afterEach(() => vi.unstubAllGlobals())
  it('installs a code lookup from its own source and re-resolves after step-up', async () => {
    const lookup = { source: { name: 'Community', index_url: 'https://www.newapi.ai/api/v1/community/index.json' }, index: structuredClone(index), code: '12345678' }
    vi.mocked(findMarketplacePluginByCode).mockResolvedValue(lookup)
    vi.mocked(fetchMarketplacePinIndex).mockResolvedValue(structuredClone(index))
    const wrapper = render()
    try {
      await flushPromises()
      await wrapper.find('input[inputmode="numeric"]').setValue('12345678')
      await wrapper.find('form').trigger('submit')
      await flushPromises()
      await open(wrapper)
      expect(fetchMarketplaceSource).toHaveBeenLastCalledWith(lookup.source.index_url, expect.any(Object), expect.any(AbortSignal))
      await button(wrapper, 'pluginRegistry.install').trigger('click')
      await flushPromises()
      expect(fetchMarketplacePinIndex).toHaveBeenCalledWith(lookup.source.index_url, '12345678', expect.any(AbortSignal))
      expect(installNewAPIPlugin).toHaveBeenCalledWith(expect.objectContaining({ marketplace: expect.objectContaining({ index_url: lookup.source.index_url, key: 'fixture', sha256: 'b'.repeat(64) }) }))
    } finally { wrapper.unmount() }
  })
  it('blocks resolved code installation after withdrawal and cancels stale lookups', async () => {
    const lookup = { source: { name: 'Community', index_url: 'https://www.newapi.ai/api/v1/community/index.json' }, index: structuredClone(index), code: '12345678' }
    vi.mocked(findMarketplacePluginByCode).mockResolvedValueOnce(lookup)
    vi.mocked(fetchMarketplacePinIndex).mockResolvedValue({ ...structuredClone(index), revoked: [{ sha256: 'b'.repeat(64) }] })
    const wrapper = render()
    try {
      await flushPromises()
      await wrapper.find('input[inputmode="numeric"]').setValue('12345678')
      await wrapper.find('form').trigger('submit'); await flushPromises(); await open(wrapper)
      await button(wrapper, 'pluginRegistry.install').trigger('click'); await flushPromises()
      expect(installNewAPIPlugin).not.toHaveBeenCalled()
      expect(wrapper.text()).toContain('pluginRegistry.external.errors.revoked')
      // A late lookup from a source we have since refreshed must not replace
      // the new source list or reopen a stale plugin selection.
      let finish!: (result: typeof lookup) => void
      vi.mocked(findMarketplacePluginByCode).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
      await wrapper.find('form').trigger('submit'); await flushPromises()
      await button(wrapper, 'common.refresh').trigger('click'); await flushPromises()
      finish(lookup); await flushPromises()
      expect(wrapper.find('button[type="submit"]').attributes('disabled')).toBeUndefined()
      expect(wrapper.text()).not.toContain('pluginRegistry.external.lookupSource')
    } finally { wrapper.unmount() }
  })
  it('compares downloaded source to a local artifact and keeps the reviewed immutable install path', async () => {
    const original = index.plugins[0]!.versions[0]!.sha256
    const sha256 = await sourceSHA256('// 2.0.0')
    const local = { id: 7, key: 'fixture', version: '1.0.0', sha256: await sourceSHA256('// 1.0.0') } as NewAPIPluginVersion
    index.plugins[0]!.versions[0]!.sha256 = sha256
    vi.mocked(getNewAPIPluginSource).mockResolvedValue({ ...local, source: '// 1.0.0' })
    const wrapper = render([local])
    try {
      await open(wrapper)
      expect(getNewAPIPluginSource).not.toHaveBeenCalled()
      await wrapper.find('[data-source-toggle]').trigger('click')
      await vi.waitFor(() => expect(wrapper.find('[data-source-diff]').exists()).toBe(true))
      expect(wrapper.find('[data-source-diff]').text()).toContain('// 1.0.0')
      expect(wrapper.find('[data-source-diff]').text()).toContain('// 2.0.0')
      expect(installNewAPIPlugin).not.toHaveBeenCalled()
      await button(wrapper, 'pluginRegistry.install').trigger('click')
      await flushPromises()
      expect(installNewAPIPlugin).toHaveBeenCalledWith({ source: '// 2.0.0', marketplace: { index_url: 'https://example.test/index.json', key: 'fixture', version: '2.0.0', sha256 }, allow_builtin_override: false })
      expect(getNewAPIPluginSource).toHaveBeenCalledTimes(1)
    } finally {
      wrapper.unmount()
      index.plugins[0]!.versions[0]!.sha256 = original
    }
  })
  it('revalidates index and source inside step-up and submits the reviewed immutable pin without activation', async () => {
    const wrapper = render(); await open(wrapper)
    expect((wrapper.find('textarea').element as HTMLTextAreaElement).value).toBe('// 2.0.0')
    await button(wrapper, 'pluginRegistry.install').trigger('click'); await flushPromises()
    expect(fetchMarketplaceIndex).toHaveBeenCalledTimes(2)
    expect(fetchMarketplaceSource).toHaveBeenCalledTimes(2)
    expect(run).toHaveBeenCalledTimes(1)
    expect(installNewAPIPlugin).toHaveBeenCalledWith({ source: '// 2.0.0', marketplace: { index_url: 'https://example.test/index.json', key: 'fixture', version: '2.0.0', sha256: 'b'.repeat(64) }, allow_builtin_override: false })
    expect(wrapper.text()).toContain('not implemented')
    expect(wrapper.emitted('installed')).toHaveLength(1)
    wrapper.unmount()
  })
  it('blocks a version revoked between review and installation', async () => {
    const wrapper = render(); await open(wrapper)
    vi.mocked(fetchMarketplaceIndex).mockResolvedValue({ ...structuredClone(index), revoked: [{ sha256: 'b'.repeat(64) }] })
    await button(wrapper, 'pluginRegistry.install').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('pluginRegistry.external.errors.revoked')
    expect(installNewAPIPlugin).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('does not let an older version download overwrite the newly selected source', async () => {
    vi.mocked(fetchMarketplaceSource).mockImplementationOnce(() => new Promise(resolve => { pendingSource.current = resolve }))
    const wrapper = render(); await open(wrapper)
    await wrapper.findAll('select').at(-1)!.setValue('1.0.0'); await flushPromises()
    expect((wrapper.find('textarea').element as HTMLTextAreaElement).value).toBe('// 1.0.0')
    pendingSource.current!('// stale 2.0.0'); await flushPromises()
    expect((wrapper.find('textarea').element as HTMLTextAreaElement).value).toBe('// 1.0.0')
    wrapper.unmount()
  })
  it('persists administrator sources with step-up and rejects credential-bearing addresses', async () => {
    const wrapper = render(); await flushPromises()
    await button(wrapper, 'pluginRegistry.external.manageSources').trigger('click')
    const inputs = wrapper.find('[data-dialog]').findAll('input')
    await inputs[1]!.setValue('https://user:secret@example.test/index.json')
    await button(wrapper, 'common.save').trigger('click'); await flushPromises()
    expect(savePluginMarketplaceSources).not.toHaveBeenCalled()
    await inputs[1]!.setValue('https://mirror.example/index.json')
    await button(wrapper, 'common.save').trigger('click'); await flushPromises()
    expect(savePluginMarketplaceSources).toHaveBeenCalledWith([{ name: 'Example', index_url: 'https://mirror.example/index.json' }])
    expect(run).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
