import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import PluginSandboxPanel from './PluginSandboxPanel.vue'
import type { NewAPIPluginVersion, PluginCenterClient } from './types'

const plugin = {
  id: 7, key: 'fixture', meta: { name: 'Fixture', models: ['video'] },
  capabilities: { task: true, hooks: ['extractUsage', 'login', 'buildSubmitRequest'] }
} as NewAPIPluginVersion

describe('installed plugin offline sandbox', () => {
  it('offers only offline hooks and validates fixture arrays before calling the API', async () => {
    const dryRun = vi.fn().mockResolvedValue({ result: { seconds: 8 }, duration_ms: 12 })
    const wrapper = mount(PluginSandboxPanel, { props: { plugin, client: { dryRun } as unknown as PluginCenterClient, t: key => key } })
    expect(wrapper.findAll('option').map(option => option.text())).toEqual(['extractUsage', 'buildSubmitRequest'])
    await wrapper.find('textarea').setValue('{"seconds":8}')
    await wrapper.find('button').trigger('click')
    expect(wrapper.find('[role="alert"]').text()).toContain('admin.plugins.detail.invalidArgs')
    expect(dryRun).not.toHaveBeenCalled()
    await wrapper.find('textarea').setValue('[{"seconds":8}]')
    await wrapper.find('button').trigger('click')
    await flushPromises()
    expect(dryRun).toHaveBeenCalledWith(7, { hook: 'extractUsage', args: [{ seconds: 8 }] }, expect.any(AbortSignal))
    expect(wrapper.find('pre').text()).toContain('"seconds": 8')
    expect(wrapper.text()).toContain('12 ms')
    wrapper.unmount()
    expect((dryRun.mock.calls[0]![2] as AbortSignal).aborted).toBe(true)
  })

  it('cancels old execution when switching plugin versions', async () => {
    let finish!: (result: unknown) => void
    const dryRun = vi.fn().mockReturnValue(new Promise(resolve => { finish = resolve }))
    const wrapper = mount(PluginSandboxPanel, { props: { plugin, client: { dryRun } as unknown as PluginCenterClient, t: key => key } })
    await wrapper.find('button').trigger('click')
    const signal = dryRun.mock.calls[0]![2] as AbortSignal
    await wrapper.setProps({ plugin: { ...plugin, id: 8 } })
    expect(signal.aborted).toBe(true)
    finish({ result: 'Old output', duration_ms: 100 })
    await flushPromises()
    expect(wrapper.find('pre').exists()).toBe(false)
    wrapper.unmount()
  })
})
