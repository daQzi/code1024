import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import PluginCenter from './PluginCenter.vue'
import type { NewAPIPluginVersion, PluginCenterClient } from './types'

const version: NewAPIPluginVersion = {
  id: 7, key: 'fixture', version: '1.0.0', sha256: 'a'.repeat(64), origin: 'uploaded',
  meta: { name: 'Independent video driver', models: ['video-fixture'] },
  capabilities: { task: true, provider: false, transform: false },
  compatibility: { runnable: true, kinds: ['task'], warnings: [], blockers: [] },
  enabled: false, active: true, created_at: '2026-11-22T00:00:00Z'
}

function fixture() {
  return {
    list: vi.fn().mockResolvedValue({ plugins: [version], builtins: [], factories: [], baseline: 'fixture' }),
    versions: vi.fn().mockResolvedValue([version]), install: vi.fn(), activate: vi.fn(), restore: vi.fn()
  } satisfies PluginCenterClient
}

const dialogComponent = { props: ['show'], template: '<section v-if="show" data-detail><slot /></section>' }
const translate = (key: string) => key

describe('standalone plugin UI', () => {
  it('pins uploads opened from details to the selected plugin key', async () => {
    const client = fixture()
    client.install.mockResolvedValue(version)
    const wrapper = mount(PluginCenter, { props: { client, t: translate, locale: 'en', dialogComponent, editable: true, runMutation: action => action() } })
    await flushPromises()
    await wrapper.find('[data-plugin-details]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-detail]').findAll('button').find(button => button.text() === 'admin.plugins.detail.uploadVersion')!.trigger('click')
    await wrapper.find('textarea').setValue('export const meta = { key: "fixture" }')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(client.install).toHaveBeenCalledWith({ source: 'export const meta = { key: "fixture" }', expected_key: 'fixture', allow_builtin_override: false })
    wrapper.unmount()
  })

  it('does not report a skipped mutation as saved after authorization is removed', async () => {
    const client = fixture()
    let proceed!: () => Promise<unknown>
    const runMutation = vi.fn((action: () => Promise<unknown>) => new Promise(resolve => {
      proceed = async () => resolve(await action())
    }))
    const wrapper = mount(PluginCenter, { props: { client, t: translate, locale: 'en', dialogComponent, editable: true, runMutation } })
    await flushPromises()
    await wrapper.find('article [role="switch"]').trigger('click')
    await wrapper.setProps({ editable: false })
    await proceed()
    await flushPromises()
    expect(client.activate).not.toHaveBeenCalled()
    expect(client.list).toHaveBeenCalledOnce()
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('confirms custom deletion and removes an empty plugin from the list and drawer', async () => {
    const client = { ...fixture(), remove: vi.fn().mockResolvedValue({ deleted_id: 7, key: 'fixture', version: '1.0.0', removed_plugin: true }) }
    const runMutation = vi.fn((action: () => Promise<unknown>) => action())
    const wrapper = mount(PluginCenter, { props: { client, t: translate, locale: 'en', dialogComponent, editable: true, runMutation } })
    await flushPromises()
    await wrapper.find('[data-plugin-details]').trigger('click')
    await flushPromises()
    const deletion = wrapper.find('[data-detail]').findAll('button').find(button => button.text() === 'admin.plugins.detail.deleteVersion')!
    await deletion.trigger('click')
    expect(client.remove).not.toHaveBeenCalled()
    client.list.mockResolvedValue({ plugins: [], factories: [], builtins: [], baseline: 'fixture' })
    await wrapper.find('[data-confirm-delete]').trigger('click')
    await flushPromises()
    expect(runMutation).toHaveBeenCalledOnce()
    expect(client.remove).toHaveBeenCalledWith(7)
    expect(wrapper.findAll('article')).toHaveLength(0)
    expect(wrapper.find('[data-detail]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('protects built-in digests and limits model previews to four models', async () => {
    const builtin = { ...version, meta: { ...version.meta, models: ['one', 'two', 'three', 'four', 'five', 'six'] } }
    const client = { ...fixture(), remove: vi.fn() }
    client.list.mockResolvedValue({ plugins: [builtin], factories: [builtin], builtins: ['fixture'], baseline: 'fixture' })
    const wrapper = mount(PluginCenter, { props: { client, t: translate, locale: 'en', dialogComponent, editable: true, runMutation: action => action() } })
    await flushPromises()
    expect(wrapper.find('article').text()).toContain('+2')
    expect(wrapper.find('article').text()).not.toContain('five')
    const deletion = wrapper.find('article').findAll('button').find(button => button.text() === 'admin.plugins.detail.deleteVersion')!
    expect(deletion.attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
  it('uses only injected clients, language and authorization', async () => {
    const client = fixture()
    const runMutation = vi.fn((action: () => Promise<unknown>) => action())
    const wrapper = mount(PluginCenter, { props: { client, t: translate, locale: 'en', dialogComponent, runMutation } })
    await flushPromises()
    expect(wrapper.text()).toContain('Independent video driver')
    expect(wrapper.find('nav').text()).not.toContain('admin.plugins.center.cleanup')
    expect(wrapper.find('nav').text()).not.toContain('admin.plugins.center.developer')
    await wrapper.find('article button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-detail]').text()).toContain('video-fixture')
    const enable = wrapper.find('[data-detail]').findAll('button').find(button => button.text() === 'admin.plugins.enable')!
    expect(enable.attributes('disabled')).toBeDefined()
    expect(client.activate).not.toHaveBeenCalled()
    await wrapper.setProps({ editable: true })
    await enable.trigger('click')
    await flushPromises()
    expect(runMutation).toHaveBeenCalledOnce()
    expect(client.activate).toHaveBeenCalledWith(7, true)
    const signal = client.list.mock.calls[0]![0] as AbortSignal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
  })

  it('shows optional host panels only when provided', async () => {
    const wrapper = mount(PluginCenter, {
      props: { client: fixture(), t: translate, locale: 'en', dialogComponent, runMutation: action => action() },
      slots: { cleanup: '<p>Credential cleanup</p>', registry: '<p>Registry</p>', 'external-market': '<p>External market</p>' }
    })
    await flushPromises()
    const navigation = wrapper.find('nav').findAll('button')
    expect(navigation.map(button => button.text())).toContain('admin.plugins.center.cleanup')
    await navigation.find(button => button.text() === 'admin.plugins.center.market')!.trigger('click')
    expect(wrapper.text()).toContain('admin.plugins.center.sources.external')
    expect(wrapper.findAll('button').map(button => button.text())).not.toContain('admin.plugins.center.sources.community')
    await navigation.find(button => button.text() === 'admin.plugins.center.cleanup')!.trigger('click')
    expect(wrapper.text()).toContain('Credential cleanup')
    wrapper.unmount()
  })

  it('scopes dialog errors away from the main page, resets orphaned detail tabs, and exposes table actions', async () => {
    const richVersion: NewAPIPluginVersion = {
      ...version,
      meta: { ...version.meta, usageSchema: { seconds: { type: 'number', unit: 'second' } } }
    }
    const plainFallback: NewAPIPluginVersion = {
      ...version,
      id: 6,
      version: '0.9.0',
      capabilities: { task: false, provider: true, transform: false },
      compatibility: { runnable: true, kinds: ['provider'], warnings: [], blockers: [] },
      meta: { name: 'Independent video driver', models: ['video-fixture'] }
    }
    const client = {
      ...fixture(),
      list: vi.fn().mockResolvedValue({ plugins: [richVersion], builtins: [], factories: [], baseline: 'fixture' }),
      versions: vi.fn().mockResolvedValue([richVersion, plainFallback]),
      remove: vi.fn().mockRejectedValueOnce(new Error('Version is pinned by active account'))
    }
    const wrapper = mount(PluginCenter, {
      props: { client, t: translate, locale: 'en', dialogComponent, editable: true, runMutation: action => action() }
    })
    await flushPromises()

    await wrapper.findAll('button').find(button => button.attributes('aria-label') === 'admin.plugins.center.table')!.trigger('click')
    expect(wrapper.find('thead').text()).toContain('admin.plugins.center.source')
    expect(wrapper.find('thead').text()).not.toContain('admin.plugins.detail.source')

    const rowDelete = wrapper.find('tbody').findAll('button').find(button => button.text() === 'admin.plugins.detail.deleteVersion')!
    await rowDelete.trigger('click')
    await wrapper.find('[data-confirm-delete]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Version is pinned by active account')

    await wrapper.findAll('button').find(button => button.text() === 'common.cancel')!.trigger('click')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)

    await wrapper.find('tbody [data-plugin-details]').trigger('click')
    await flushPromises()
    const billingTab = wrapper.find('[data-detail]').findAll('[role="tab"]').find(button => button.text() === 'admin.plugins.detail.billing')!
    await billingTab.trigger('click')
    expect(billingTab.attributes('aria-selected')).toBe('true')

    client.remove.mockResolvedValueOnce({ deleted_id: 7, key: 'fixture', version: '1.0.0', removed_plugin: false })
    client.list.mockResolvedValueOnce({ plugins: [plainFallback], builtins: [], factories: [], baseline: 'fixture' })
    client.versions.mockResolvedValueOnce([plainFallback])
    await wrapper.find('[data-detail]').findAll('button').find(button => button.text() === 'admin.plugins.detail.deleteVersion')!.trigger('click')
    await wrapper.find('[data-confirm-delete]').trigger('click')
    await flushPromises()

    const activeTabs = wrapper.find('[data-detail]').findAll('[role="tab"]')
    expect(activeTabs.some(button => button.text() === 'admin.plugins.detail.billing')).toBe(false)
    expect(activeTabs[0]!.attributes('aria-selected')).toBe('true')
    wrapper.unmount()
  })
})
