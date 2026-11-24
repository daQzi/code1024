import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import PluginBillingPanel from './PluginBillingPanel.vue'
import { pluginUsageGroups } from './pluginDisplay'
import type { NewAPIPluginVersion, PluginCenterClient } from './types'

const meta: NewAPIPluginVersion['meta'] = {
  name: 'Fixture', models: ['video', 'image'],
  usageSchema: { seconds: { type: 'number', unit: 'second' } },
  usageProfiles: [{ models: ['image'], schema: { image_count: { type: 'number', unit: 'count', unitLabel: { zh: '张', en: 'Image' } } } }]
}

describe('plugin billing metadata and configured prices', () => {
  it('labels enum-only fields as enums rather than numeric usage', () => {
    const wrapper = mount(PluginBillingPanel, { props: {
      plugin: { key: 'fixture', meta: { name: 'Fixture', models: ['video'], usageSchema: { resolution: { enum: ['720P', '1080P'] } } } },
      client: {} as PluginCenterClient, locale: 'en', t: key => key
    } })
    const row = wrapper.find('tbody tr')
    expect(row.text()).toContain('admin.plugins.detail.string')
    expect(row.text()).not.toContain('admin.plugins.detail.number')
    wrapper.unmount()
  })

  it('distinguishes plain string fields from enums and falls back cleanly on custom units', async () => {
    const pricing = vi.fn().mockResolvedValue([
      { account_id: 9, account_name: 'Custom unit account', version: '1.0.0', version_id: 3, status: 'active', schedulable: true, pricing_rules: { '*': { mode: 'fixed', unit: 'minute', unit_price: '0.5' } } }
    ])
    const wrapper = mount(PluginBillingPanel, {
      props: {
        plugin: {
          key: 'fixture',
          meta: {
            name: 'Fixture',
            models: ['video'],
            usageSchema: {
              note: { type: 'string', unit: 'character' },
              frames: { type: 'integer', unit: 'frame' }
            }
          }
        },
        client: { pricing } as unknown as PluginCenterClient,
        locale: 'en',
        t: key => key
      }
    })
    await flushPromises()
    const rows = wrapper.findAll('tbody tr')
    expect(rows[0]!.text()).toContain('admin.plugins.detail.text')
    expect(rows[0]!.text()).toContain('character')
    expect(rows[0]!.text()).not.toContain('admin.plugins.detail.units.')
    expect(rows[1]!.text()).toContain('integer')
    expect(rows[1]!.text()).toContain('frame')
    expect(wrapper.text()).toContain('$0.5 / minute')
    expect(wrapper.text()).not.toContain('admin.plugins.js.units.minute')
    wrapper.unmount()
  })

  it('uses model-specific profiles without applying default fields to profiled models', () => {
    expect(pluginUsageGroups(meta)).toEqual([
      { models: ['video'], schema: meta.usageSchema, examples: undefined },
      meta.usageProfiles![0]
    ])
  })

  it('loads actual account rules and cancels stale pricing after a plugin change', async () => {
    let resolvePrevious!: (value: unknown[]) => void
    const pricing = vi.fn().mockReturnValueOnce(new Promise(resolve => { resolvePrevious = resolve })).mockResolvedValueOnce([
      { account_id: 8, account_name: 'New account', version: '1.2.0', version_id: 22, status: 'active', schedulable: true, pricing_rules: { video: { mode: 'fixed', unit: 'second', unit_price: '0.15', maximum: '10' } } }
    ])
    const client = { pricing } as unknown as PluginCenterClient
    const wrapper = mount(PluginBillingPanel, { props: { plugin: { key: 'old', meta }, client, locale: 'zh-CN', t: key => key } })
    const previousSignal = pricing.mock.calls[0]![1] as AbortSignal
    await wrapper.setProps({ plugin: { key: 'new', meta } })
    await flushPromises()
    resolvePrevious([{ account_name: 'Stale account' }])
    await flushPromises()
    expect(previousSignal.aborted).toBe(true)
    expect(wrapper.text()).toContain('New account')
    expect(wrapper.text()).toContain('$0.15')
    expect(wrapper.text()).toContain('张')
    expect(wrapper.text()).not.toContain('Stale account')
    wrapper.unmount()
    expect((pricing.mock.calls[1]![1] as AbortSignal).aborted).toBe(true)
  })
})
