import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ProviderToolPricePanel from './ProviderToolPricePanel.vue'
import { parseProviderToolPrices, type ProviderToolPriceClient, type ProviderToolPriceSettings } from './toolPrices'

const fixture = (): ProviderToolPriceSettings => ({ prices: { file_search: 2.5 }, revision: 'r1' })
const clients = (): ProviderToolPriceClient => ({ read: vi.fn().mockResolvedValue(fixture()), save: vi.fn().mockResolvedValue({ prices: { file_search: 0 }, revision: 'r2' }) })
const wrappers: VueWrapper[] = []
function render(client = clients(), extra = {}) {
  const wrapper = mount(ProviderToolPricePanel, { props: { client, t: key => key, ...extra } })
  wrappers.push(wrapper)
  return wrapper
}
async function open(wrapper: VueWrapper) {
  const details = wrapper.get('details')
  ;(details.element as HTMLDetailsElement).open = true
  await details.trigger('toggle')
  await flushPromises()
}
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.restoreAllMocks() })

describe('standalone Provider tool prices', () => {
  it('loads lazily without host dependencies and defaults to read-only', async () => {
    const client = clients(), wrapper = render(client)
    expect(client.read).not.toHaveBeenCalled()
    await open(wrapper)
    expect(client.read).toHaveBeenCalledWith(expect.any(AbortSignal))
    expect(wrapper.get('textarea').attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('button')).toHaveLength(1)
    await wrapper.get('form').trigger('submit')
    expect(client.save).not.toHaveBeenCalled()
  })

  it('saves explicit zero and the loaded revision through injected authority', async () => {
    const client = clients(), run = vi.fn((action: () => Promise<unknown>) => action())
    const wrapper = render(client, { editable: true, runMutation: run })
    await open(wrapper)
    await wrapper.get('textarea').setValue('{"file_search":0}')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(run).toHaveBeenCalledTimes(1)
    expect(client.save).toHaveBeenCalledWith({ prices: { file_search: 0 }, revision: 'r1' }, expect.any(AbortSignal))
    expect(wrapper.get('[role=status]').text()).toBe('providerToolPrices.saved')
  })

  it('preserves conflicts and never renders private transport errors', async () => {
    const client = clients()
    vi.mocked(client.save).mockRejectedValueOnce({ response: { status: 409, data: { message: 'secret URL' } } }).mockRejectedValueOnce(new Error('private transport failure'))
    const wrapper = render(client, { editable: true, runMutation: (action: () => Promise<unknown>) => action() })
    await open(wrapper)
    await wrapper.get('textarea').setValue('{"file_search":7}')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.get('[role=alert]').text()).toBe('providerToolPrices.conflict')
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('{"file_search":7}')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.get('[role=alert]').text()).toBe('providerToolPrices.saveFailed')
    expect(wrapper.text()).not.toContain('private transport')
  })

  it('validates malformed, ambiguous and excessive settings before step-up', async () => {
    const client = clients(), run = vi.fn((action: () => Promise<unknown>) => action())
    const wrapper = render(client, { editable: true, runMutation: run })
    await open(wrapper)
    for (const text of ['null', '[]', '{"tool":-1}', '{"tool":"2"}', '{"tool:m":1,"tool:m*":2}', JSON.stringify(Object.fromEntries(Array.from({ length: 513 }, (_, index) => [`tool${index}`, 1])))]) {
      await wrapper.get('textarea').setValue(text)
      await wrapper.get('form').trigger('submit'); await flushPromises()
      expect(wrapper.get('[role=alert]').text()).toBe('providerToolPrices.invalid')
    }
    expect(run).not.toHaveBeenCalled()
    expect(client.save).not.toHaveBeenCalled()
    expect(parseProviderToolPrices('{}')).toEqual({})
  })

  it('cancels old identity reads and ignores late results', async () => {
    const old = clients()
    let finish!: (value: ProviderToolPriceSettings) => void
    vi.mocked(old.read).mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    const wrapper = render(old)
    await open(wrapper)
    const signal = vi.mocked(old.read).mock.calls[0]![0]!
    const next = clients()
    await wrapper.setProps({ client: next })
    expect(signal.aborted).toBe(true)
    finish({ prices: { private_tool: 100 }, revision: 'private' }); await flushPromises()
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).not.toContain('private')
    const reads = vi.mocked(next.read).mock.calls.length
    await wrapper.get('button').trigger('click'); await flushPromises()
    expect(next.read).toHaveBeenCalledTimes(reads + 1)
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).not.toContain('private')
  })

  it.each(['unmount', 'permissions', 'client'])('rejects delayed authorization after %s changes', async mode => {
    const client = clients(), cancel = vi.fn()
    let authorized!: () => Promise<unknown>
    const run = vi.fn((action: () => Promise<unknown>) => { authorized = action; return new Promise(() => {}) })
    const wrapper = render(client, { editable: true, runMutation: run, cancelMutation: cancel })
    await open(wrapper)
    await wrapper.get('form').trigger('submit')
    expect(run).toHaveBeenCalledTimes(1)
    if (mode === 'unmount') { wrapper.unmount(); wrappers.splice(wrappers.indexOf(wrapper), 1) }
    else if (mode === 'permissions') await wrapper.setProps({ editable: false })
    else await wrapper.setProps({ client: clients() })
    await authorized()
    expect(client.save).not.toHaveBeenCalled()
    expect(cancel).toHaveBeenCalled()
  })
})
