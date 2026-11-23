import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import PluginRegistryPanel from './PluginRegistryPanel.vue'
import PluginMarketplacePanel from './PluginMarketplacePanel.vue'
import PluginCredentialCleanupPanel from './PluginCredentialCleanupPanel.vue'
import type { PluginRegistryClient, PluginSubmission } from './types'

const t = (key: string) => key
const dialogComponent = { props: ['show'], template: '<section v-if="show" data-dialog><slot /></section>' }
const paginationComponent = { template: '<div />' }
const runMutation = async <T,>(action: () => Promise<T>) => action()
const ui = { t, dialogComponent, paginationComponent, runMutation }

function registryFixture() {
  const item: PluginSubmission = {
    id: 8, author_id: 4, key: 'community-4-fixture', version: '1.0.0', sha256: 'a'.repeat(64),
    meta: { name: 'Fixture', models: [] }, source: 'fixture-source', readme: '', status: 'submitted', revision: 2,
    capabilities: { task: true, provider: false, transform: false },
    compatibility: { runnable: true, kinds: ['task'], warnings: [], blockers: [] },
    review_reason: '', created_at: '2026-11-22T00:00:00Z', updated_at: '2026-11-22T00:00:00Z'
  }
  const client = {
    listPluginSubmissions: vi.fn().mockResolvedValue({ namespace: 'community-4-', items: [item], total: 1 }),
    getPluginSubmission: vi.fn().mockResolvedValue(item), savePluginSubmission: vi.fn(),
    transitionPluginSubmission: vi.fn().mockResolvedValue(undefined), installPluginSubmission: vi.fn(),
    listPluginTemplates: vi.fn().mockResolvedValue([{ kind: 'ingress', source: 'fresh-template', fixture: { hook: 'ingress', args: [] } }]),
    dryRunPlugin: vi.fn()
  } satisfies PluginRegistryClient
  return { item, client }
}

describe('independent market and control panels', () => {
  it('keeps registry mutations read-only until authorized and preserves review revision', async () => {
    const { client } = registryFixture()
    const authorize = vi.fn(runMutation)
    const wrapper = mount(PluginRegistryPanel, { props: { ...ui, mode: 'review', client, runMutation: authorize } })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'pluginRegistry.open')!.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('button').map(button => button.text())).not.toContain('pluginRegistry.publish')
    await wrapper.setProps({ canWrite: true })
    await wrapper.findAll('button').find(button => button.text() === 'pluginRegistry.publish')!.trigger('click')
    await flushPromises()
    expect(authorize).toHaveBeenCalledOnce()
    expect(client.transitionPluginSubmission).toHaveBeenCalledWith(8, 2, 'publish', '', true)
    const signal = client.listPluginSubmissions.mock.calls[0]![3] as AbortSignal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
  })

  it('discards a delayed local upload after switching to a new draft', async () => {
    const { client } = registryFixture()
    const wrapper = mount(PluginRegistryPanel, { props: { ...ui, mode: 'mine', canWrite: true, client } })
    await flushPromises()
    const create = () => wrapper.findAll('button').find(button => button.text() === 'pluginRegistry.create')!
    await create().trigger('click')
    let finish!: (source: string) => void
    const input = wrapper.find('input[type=file]')
    Object.defineProperty(input.element, 'files', { value: [{ size: 10, text: () => new Promise<string>(resolve => { finish = resolve }) }] })
    const upload = input.trigger('change')
    await create().trigger('click')
    finish('stale-upload')
    await upload
    await flushPromises()
    expect((wrapper.find('textarea').element as HTMLTextAreaElement).value).toBe('fresh-template')
    expect(client.savePluginSubmission).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('injects marketplace network reads and aborts them when leaving the panel', async () => {
    const client = { listPluginMarketplaceSources: vi.fn().mockResolvedValue([{ name: 'Fixture', index_url: 'https://example.test/index.json' }]), savePluginMarketplaceSources: vi.fn(), installNewAPIPlugin: vi.fn() }
    const marketplace = { fetchMarketplaceIndex: vi.fn().mockResolvedValue({ plugins: [], revoked: [], name: 'Fixture' }), fetchMarketplaceDocument: vi.fn(), fetchMarketplacePinIndex: vi.fn(), fetchMarketplaceSource: vi.fn(), findMarketplacePluginByCode: vi.fn() }
    const wrapper = mount(PluginMarketplacePanel, { props: { ...ui, locale: 'en', installedPlugins: [], client, marketplace, sourceClient: { getNewAPIPluginSource: vi.fn(), getPluginFactorySource: vi.fn() } } })
    await flushPromises()
    expect(marketplace.fetchMarketplaceIndex).toHaveBeenCalledWith('https://example.test/index.json', expect.any(AbortSignal))
    expect(wrapper.find('header button').attributes('disabled')).toBeDefined()
    const signal = marketplace.fetchMarketplaceIndex.mock.calls[0]![1] as AbortSignal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    expect(client.installNewAPIPlugin).not.toHaveBeenCalled()
  })

  it('injects cleanup authorization and hides raw credential errors', async () => {
    const client = { listPluginCredentialRevocations: vi.fn().mockResolvedValue([{ id: 9, account_id: 1, plugin_version_id: 2, status: 'attention', attempts: 1, last_error: '', created_at: '2026-11-22T00:00:00Z' }]), retryPluginCredentialRevocation: vi.fn().mockRejectedValue(new Error('credential-canary')) }
    const authorize = vi.fn(runMutation)
    const wrapper = mount(PluginCredentialCleanupPanel, { props: { client, t, locale: 'en', runMutation: authorize } })
    await flushPromises()
    expect(wrapper.find('[data-cleanup-retry]').attributes('disabled')).toBeDefined()
    await wrapper.setProps({ editable: true })
    await wrapper.find('[data-cleanup-retry]').trigger('click')
    await flushPromises()
    expect(authorize).toHaveBeenCalledOnce()
    expect(wrapper.find('[role=alert]').text()).toBe('admin.plugins.cleanup.failed')
    expect(wrapper.text()).not.toContain('credential-canary')
    wrapper.unmount()
  })
})
