import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import NewAPIPluginBalance from '../NewAPIPluginBalance.vue'
import { queryNewAPIPluginBalance, type NewAPIPluginBalance as Balance } from '@/api/admin/newapiPlugins'

const { run } = vi.hoisted(() => ({ run: vi.fn((action: () => Promise<unknown>) => action()) }))
vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key, locale: { value: 'en-US' } }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run }), isStepUpCancelled: () => false }))
vi.mock('@/api/admin/newapiPlugins', () => ({ queryNewAPIPluginBalance: vi.fn() }))
const balance = (): Balance => ({ account_id: 17, plugin_version_id: 2, plugin_key: 'fixture', plugin_version: '1.0.0', fetched_at: '2026-11-22T00:00:00Z', balance: { amount: 12.5, currency: 'CNY', unlimited: false, windows: [{ name: { en: '<img src=x onerror=alert(1)>' }, used: 2, limit: 10, unit: 'requests', resetAt: 1800000000 }] } })
function render() { return mount(NewAPIPluginBalance, { props: { accountId: 17, versionId: 2 }, global: { stubs: { TotpStepUpDialog: true } } }) }

describe('saved plugin account balance', () => {
  beforeEach(() => { vi.clearAllMocks(); vi.mocked(queryNewAPIPluginBalance).mockResolvedValue(balance()) })
  it('queries only on an explicit button action through step-up and renders amount and windows as text', async () => {
    const wrapper = render()
    expect(queryNewAPIPluginBalance).not.toHaveBeenCalled()
    expect(wrapper.get('[data-balance-query]').attributes('type')).toBe('button')
    await wrapper.get('[data-balance-query]').trigger('click')
    await flushPromises()
    expect(run).toHaveBeenCalledOnce()
    expect(queryNewAPIPluginBalance).toHaveBeenCalledWith(17, expect.any(AbortSignal))
    expect(wrapper.get('[data-balance-amount]').text()).toBe('12.5 CNY')
    expect(wrapper.get('[data-balance-window]').text()).toContain('2 / 10 requests')
    expect(wrapper.get('[data-balance-window]').text()).toContain('<img src=x onerror=alert(1)>')
    expect(wrapper.find('img').exists()).toBe(false)
    wrapper.unmount()
  })
  it.each(['zero', 'unknown', 'unlimited'])('keeps %s separate from the other amount states', async state => {
    const value = balance()
    value.balance.amount = state === 'zero' ? 0 : null
    value.balance.unlimited = state === 'unlimited'
    vi.mocked(queryNewAPIPluginBalance).mockResolvedValueOnce(value)
    const wrapper = render()
    await wrapper.get('[data-balance-query]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-balance-amount]').text()).toBe(state === 'zero' ? '0 CNY' : state === 'unknown' ? 'admin.plugins.js.balanceUnknown' : 'admin.plugins.js.balanceUnlimited')
    wrapper.unmount()
  })
  it('cancels old account reads and ignores late results and a changed plugin revision', async () => {
    let resolve!: (balance: Balance) => void
    vi.mocked(queryNewAPIPluginBalance).mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const wrapper = render()
    await wrapper.get('[data-balance-query]').trigger('click')
    await flushPromises()
    const signal = vi.mocked(queryNewAPIPluginBalance).mock.calls[0]![1]!
    expect(wrapper.get('[data-balance-query]').attributes('disabled')).toBeDefined()
    await wrapper.setProps({ accountId: 18, versionId: 3 })
    expect(signal.aborted).toBe(true)
    resolve(balance())
    await flushPromises()
    expect(wrapper.find('[data-balance-amount]').exists()).toBe(false)
    await wrapper.get('[data-balance-query]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.plugins.js.balanceChanged')
    expect(wrapper.find('[data-balance-amount]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('hides raw client errors and aborts a pending query on unmount', async () => {
    vi.mocked(queryNewAPIPluginBalance).mockRejectedValueOnce(new Error('private-key-client-diagnostic'))
    const wrapper = render()
    await wrapper.get('[data-balance-query]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.plugins.js.balanceFailed')
    expect(wrapper.text()).not.toContain('private-key')
    let finish!: (balance: Balance) => void
    vi.mocked(queryNewAPIPluginBalance).mockImplementationOnce(() => new Promise(done => { finish = done }))
    await wrapper.get('[data-balance-query]').trigger('click')
    await flushPromises()
    const signal = vi.mocked(queryNewAPIPluginBalance).mock.calls.at(-1)![1]!
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    finish(balance())
    await flushPromises()
  })
})
