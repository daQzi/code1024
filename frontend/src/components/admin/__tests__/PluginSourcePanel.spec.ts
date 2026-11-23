import { webcrypto } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PluginSourcePanel from '../PluginSourcePanel.vue'
import { getNewAPIPluginSource, getPluginFactorySource, type NewAPIPluginVersion, type PluginFactory, type PluginSource } from '@/api/admin/newapiPlugins'
import { sourceSHA256 } from '@/utils/pluginMarketplace'

vi.mock('@/components/common/Select.vue', async () => ({ default: (await import('@/__tests__/stubs/SelectStub')).default }))
vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/newapiPlugins', () => ({ getNewAPIPluginSource: vi.fn(), getPluginFactorySource: vi.fn() }))
beforeEach(() => { vi.resetAllMocks(); vi.stubGlobal('crypto', webcrypto) })
afterEach(() => vi.unstubAllGlobals())

async function artifact(id: number, version: string, source: string) {
  const sha256 = await sourceSHA256(source)
  return {
    plugin: { id, key: 'source-fixture', version, sha256 } as NewAPIPluginVersion,
    data: { id, key: 'source-fixture', version, sha256, source } as PluginSource,
  }
}

describe('plugin source inspection', () => {
  it('loads lazily, renders source literally and compares immutable versions with line numbers', async () => {
    const before = await artifact(1, '1.0.0', 'const a = 1\nthrow Error("never execute");')
    const after = await artifact(2, '2.0.0', 'const a = 2\nthrow Error("never execute");\n// <script>throw 1</script>')
    const records = new Map([[1, before.data], [2, after.data]])
    vi.mocked(getNewAPIPluginSource).mockImplementation(async id => records.get(id)!)
    const wrapper = mount(PluginSourcePanel, { props: { plugin: after.plugin, versions: [after.plugin, before.plugin] } })
    expect(getNewAPIPluginSource).not.toHaveBeenCalled()
    await wrapper.find('[data-source-toggle]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-source-content]').exists()).toBe(true))
    expect((wrapper.find('[data-source-content]').element as HTMLTextAreaElement).value).toBe(after.data.source)
    expect(wrapper.find('[data-source-content]').attributes('readonly')).toBeDefined()
    expect(wrapper.find('script').exists()).toBe(false)
    await wrapper.find('[data-source-compare]').setValue('1')
    await vi.waitFor(() => expect(wrapper.find('[data-source-diff]').exists()).toBe(true))
    expect(wrapper.findAll('[data-diff-kind="removed"]').map(row => row.text()).join()).toContain('const a = 1')
    expect(wrapper.findAll('[data-diff-kind="added"]').map(row => row.text()).join()).toContain('const a = 2')
    expect(wrapper.find('[data-source-diff]').text()).toContain('<script>throw 1</script>')
    expect(wrapper.find('script').exists()).toBe(false)
    wrapper.unmount()
  })

  it('can inspect the embedded factory before installation', async () => {
    const factory = await artifact(1, '1.0.0', '\uFEFF// 内置\r\nthrow 1;')
    const { id: _id, ...metadata } = factory.plugin
    vi.mocked(getPluginFactorySource).mockResolvedValue({ ...factory.data, id: undefined })
    const wrapper = mount(PluginSourcePanel, { props: { plugin: metadata as PluginFactory, versions: [] } })
    await wrapper.find('[data-source-toggle]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-source-content]').exists()).toBe(true))
    expect(getPluginFactorySource).toHaveBeenCalledWith('source-fixture', expect.any(AbortSignal))
    expect(getNewAPIPluginSource).not.toHaveBeenCalled()
    expect((wrapper.find('[data-source-content]').element as HTMLTextAreaElement).value).toContain('内置')
    wrapper.unmount()
  })

  it.each(['digest', 'key', 'version', 'id', 'too-large', 'utf8-bytes'])('rejects %s mismatches before displaying source', async boundary => {
    const item = await artifact(1, '1.0.0', '// expected')
    const bad = { ...item.data }
    if (boundary === 'digest') bad.source = '// replaced after version history was loaded'
    if (boundary === 'key') bad.key = 'other-plugin'
    if (boundary === 'version') bad.version = '2.0.0'
    if (boundary === 'id') bad.id = 9
    if (boundary === 'too-large') bad.source = 'a'.repeat(1048577)
    if (boundary === 'utf8-bytes') bad.source = '界'.repeat(400000)
    vi.mocked(getNewAPIPluginSource).mockResolvedValue(bad)
    const wrapper = mount(PluginSourcePanel, { props: { plugin: item.plugin, versions: [] } })
    await wrapper.find('[data-source-toggle]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[role="alert"]').exists()).toBe(true))
    expect(wrapper.text()).toContain('admin.plugins.source.loadFailed')
    expect(wrapper.find('[data-source-content]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('compares a verified market candidate with installed source lazily without fetching a factory or installing it', async () => {
    const before = await artifact(1, '1.0.0', 'const value = 1\n// <script>never run</script>')
    const candidate = await artifact(2, '2.0.0', 'const value = 2\n// <script>never run</script>')
    vi.mocked(getNewAPIPluginSource).mockResolvedValue(before.data)
    const { id: _id, ...remote } = candidate.plugin
    const wrapper = mount(PluginSourcePanel, { props: { plugin: remote, source: candidate.data.source, versions: [before.plugin], comparisonOnly: true } })
    expect(getNewAPIPluginSource).not.toHaveBeenCalled()
    await wrapper.find('[data-source-toggle]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-source-diff]').exists()).toBe(true))
    expect(getNewAPIPluginSource).toHaveBeenCalledWith(1, expect.any(AbortSignal))
    expect(getPluginFactorySource).not.toHaveBeenCalled()
    expect(wrapper.findAll('[data-diff-kind="added"]').map(row => row.text()).join()).toContain('const value = 2')
    expect(wrapper.findAll('[data-diff-kind="removed"]').map(row => row.text()).join()).toContain('const value = 1')
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.find('[data-source-content]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('rechecks candidate hashes and refreshes the default comparison after installed metadata changes', async () => {
    const before = await artifact(1, '1.0.0', '// first local')
    const installed = await artifact(3, '1.1.0', '// replacement local')
    const candidate = await artifact(2, '2.0.0', '// candidate')
    vi.mocked(getNewAPIPluginSource).mockImplementation(async id => id === 1 ? before.data : installed.data)
    const { id: _id, ...remote } = candidate.plugin
    const wrapper = mount(PluginSourcePanel, { props: { plugin: remote, source: candidate.data.source, versions: [before.plugin], comparisonOnly: true } })
    await wrapper.find('[data-source-toggle]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-source-diff]').exists()).toBe(true))
    await wrapper.setProps({ versions: [installed.plugin] })
    await vi.waitFor(() => expect(wrapper.find('[data-source-diff]').text()).toContain('replacement local'))
    await wrapper.setProps({ source: '// changed without changing the digest' })
    await vi.waitFor(() => expect(wrapper.find('[role="alert"]').exists()).toBe(true))
    expect(wrapper.find('[data-source-diff]').exists()).toBe(false)
    expect(getPluginFactorySource).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('aborts close/unmount and prevents stale responses from replacing the latest selection', async () => {
    const item = await artifact(1, '1.0.0', '// expected')
    let resolve!: (value: PluginSource) => void
    vi.mocked(getNewAPIPluginSource).mockReturnValueOnce(new Promise(done => { resolve = done })).mockResolvedValue(item.data)
    const wrapper = mount(PluginSourcePanel, { props: { plugin: item.plugin, versions: [] } })
    await wrapper.find('[data-source-toggle]').trigger('click')
    const signal = vi.mocked(getNewAPIPluginSource).mock.calls[0]![1]!
    await wrapper.find('[data-source-toggle]').trigger('click')
    expect(signal.aborted).toBe(true)
    await wrapper.find('[data-source-toggle]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-source-content]').exists()).toBe(true))
    resolve({ ...item.data, source: '// stale' })
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect((wrapper.find('[data-source-content]').element as HTMLTextAreaElement).value).toBe(item.data.source)
    const latest = vi.mocked(getNewAPIPluginSource).mock.calls.at(-1)![1]!
    wrapper.unmount()
    expect(latest.aborted).toBe(true)
  })

  it('confines comparison to the same plugin and displays a limit without hiding complete source', async () => {
    const before = await artifact(1, '1.0.0', '// old')
    const after = await artifact(2, '2.0.0', '// '+ 'a'.repeat(70000))
    vi.mocked(getNewAPIPluginSource).mockImplementation(async id => id === 1 ? before.data : after.data)
    const wrapper = mount(PluginSourcePanel, { props: { plugin: after.plugin, versions: [before.plugin, { ...before.plugin, id: 3, key: 'other-plugin' }] } })
    await wrapper.find('[data-source-toggle]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-source-content]').exists()).toBe(true))
    expect(wrapper.findAll('[data-source-compare] option').map(option => option.attributes('value'))).toEqual(['', '1'])
    await wrapper.find('[data-source-compare]').setValue('1')
    await vi.waitFor(() => expect(wrapper.find('[data-source-diff-limit]').exists()).toBe(true))
    expect((wrapper.find('[data-source-content]').element as HTMLTextAreaElement).value).toBe(after.data.source)
    wrapper.unmount()
  })
})
