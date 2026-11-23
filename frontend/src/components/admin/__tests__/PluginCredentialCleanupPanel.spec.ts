import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Panel from '../PluginCredentialCleanupPanel.vue'
import { listPluginCredentialRevocations, retryPluginCredentialRevocation } from '@/api/admin/newapiPlugins'

const { run } = vi.hoisted(() => ({ run: vi.fn((action: () => Promise<unknown>) => action()) }))
vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key, locale: ref('en') }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run }), isStepUpCancelled: (e: Error) => e.message === 'cancelled' }))
vi.mock('@/api/admin/newapiPlugins', () => ({ listPluginCredentialRevocations: vi.fn(), retryPluginCredentialRevocation: vi.fn() }))
const fixture = (status: 'pending' | 'completed' | 'protected' | 'attention', id: number) => ({ id, account_id: 17, plugin_version_id: 1, status, attempts: 2, last_error: '', created_at: '2026-11-22T00:00:00Z' })
const render = () => mount(Panel, { global: { stubs: { TotpStepUpDialog: true } } })

describe('credential cleanup', () => {
  beforeEach(() => { vi.clearAllMocks(); vi.mocked(listPluginCredentialRevocations).mockResolvedValue([fixture('pending', 3), fixture('completed', 4), fixture('protected', 5), fixture('attention', 6)]); vi.mocked(retryPluginCredentialRevocation).mockResolvedValue({ queued: true }) })
  it('shows public records and limits retries to pending or attention jobs', async () => {
    const wrapper = render()
    await flushPromises()
    expect(wrapper.findAll('li')).toHaveLength(4)
    expect(wrapper.findAll('[data-cleanup-retry]')).toHaveLength(2)
    await wrapper.find('[data-cleanup-retry]').trigger('click')
    await flushPromises()
    expect(run).toHaveBeenCalledTimes(1)
    expect(retryPluginCredentialRevocation).toHaveBeenCalledWith(3, expect.any(AbortSignal))
    expect(listPluginCredentialRevocations).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('admin.plugins.cleanup.queued')
    wrapper.unmount()
  })
  it('suppresses raw errors and handles cancelled step-up', async () => {
    const wrapper = render()
    await flushPromises()
    vi.mocked(retryPluginCredentialRevocation).mockRejectedValueOnce(new Error('private-credential-canary'))
    await wrapper.find('[data-cleanup-retry]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role=alert]').text()).toBe('admin.plugins.cleanup.failed')
    expect(wrapper.text()).not.toContain('private-credential-canary')
    run.mockRejectedValueOnce(new Error('cancelled'))
    await wrapper.find('[data-cleanup-retry]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role=alert]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('cancels requests and ignores responses after leaving the page', async () => {
    let finish!: (value: Awaited<ReturnType<typeof listPluginCredentialRevocations>>) => void
    vi.mocked(listPluginCredentialRevocations).mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    const wrapper = render()
    await flushPromises()
    const signal = vi.mocked(listPluginCredentialRevocations).mock.calls[0]![0]!
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    finish([fixture('pending', 7)])
    await flushPromises()
    expect(retryPluginCredentialRevocation).not.toHaveBeenCalled()
  })
})
