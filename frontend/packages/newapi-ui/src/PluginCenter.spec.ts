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
})
