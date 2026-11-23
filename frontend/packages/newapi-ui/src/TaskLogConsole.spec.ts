import { defineComponent, markRaw } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import TaskLogConsole from './TaskLogConsole.vue'
import type { TaskLog, TaskLogClient, TaskLogDetail, TaskLogPage } from './taskLogs'
import type { RunPluginMutation } from './types'

const Dialog = markRaw(defineComponent({ props: ['show'], template: '<div v-if="show" data-detail><slot /></div>' }))
const Pagination = markRaw(defineComponent({ template: '<div />' }))
const task: TaskLog = { id: 'video_fixture', model: 'video-model', group_id: 1, group_name: 'video-group', platform: 'openai', protocol: 'openai_video', action: 'text_to_video', plugin_key: 'example', plugin_version: '1.0', status: 'running', delivery_status: 'pending', billing_status: 'held', progress: 0, hold_amount: '1', actual_amount: null, created_at: '2026-11-21T00:00:00Z', updated_at: '2026-11-21T00:00:00Z', finished_at: null }
const detail: TaskLogDetail = { ...task, source: 'native', fields: { prompt: 'first prompt' }, events: [], native_actions: ['resume_poll'] }
const page = (items: TaskLog[]): TaskLogPage => ({ items, total: items.length, page: 1, page_size: 20, pages: 1 })
const wrappers: VueWrapper[] = []
function client(): TaskLogClient {
  return { listTaskLogs: vi.fn().mockResolvedValue(page([task])), getTaskLog: vi.fn().mockResolvedValue(detail), maintainNativeTask: vi.fn().mockResolvedValue({ updated: true }) }
}
function render(api: TaskLogClient, extra = {}) {
  const wrapper = mount(TaskLogConsole, { props: { client: api, t: (key: string) => key, locale: 'en', dialogComponent: Dialog, paginationComponent: Pagination, ...extra } })
  wrappers.push(wrapper)
  return wrapper
}
async function openFirst(wrapper: VueWrapper) {
  await wrapper.findAll('button').find(button => button.text() === 'taskLogs.details')!.trigger('click')
  await flushPromises()
}
beforeEach(() => vi.useFakeTimers())
afterEach(() => { wrappers.splice(0).forEach(wrapper => { if (wrapper.exists()) wrapper.unmount() }); vi.restoreAllMocks(); vi.useRealTimers() })

describe('standalone task log console', () => {
  it('needs no host router or stores and remains read-only without explicit mutation authority', async () => {
    const api = client()
    const wrapper = render(api, { admin: true, runMutation: (action: () => Promise<unknown>) => action() })
    await flushPromises()
    expect(api.listTaskLogs).toHaveBeenCalledWith(expect.objectContaining({ scope: 'all' }), expect.any(AbortSignal))
    expect(wrapper.text()).not.toContain('taskLogs.tabs.review')
    await openFirst(wrapper)
    expect(wrapper.find('[data-detail]').text()).toContain('first prompt')
    expect(wrapper.find('[data-native-maintenance]').exists()).toBe(false)
    expect(api.maintainNativeTask).not.toHaveBeenCalled()
  })

  it('aborts and discards old identity responses when the host replaces its scoped client', async () => {
    const oldAPI = client()
    let resolveOld!: (page: TaskLogPage) => void
    vi.mocked(oldAPI.listTaskLogs).mockImplementation(() => new Promise(resolve => { resolveOld = resolve }))
    const wrapper = render(oldAPI, { admin: true })
    await flushPromises()
    const oldSignal = vi.mocked(oldAPI.listTaskLogs).mock.calls[0]![1]!
    const nextAPI = client()
    vi.mocked(nextAPI.listTaskLogs).mockResolvedValue(page([{ ...task, model: 'mine-only' }]))
    await wrapper.setProps({ client: nextAPI, admin: false })
    await flushPromises()
    expect(oldSignal.aborted).toBe(true)
    expect(nextAPI.listTaskLogs).toHaveBeenLastCalledWith(expect.objectContaining({ scope: 'mine' }), expect.any(AbortSignal))
    resolveOld(page([{ ...task, model: 'private-admin-model' }]))
    await flushPromises()
    expect(wrapper.text()).toContain('mine-only')
    expect(wrapper.text()).not.toContain('private-admin-model')
    expect(wrapper.text()).not.toContain('taskLogs.tabs.all')
  })

  it('pauses hidden reads, resumes on visibility and clears its refresh timer on unmount', async () => {
    const api = client()
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    const wrapper = render(api)
    await flushPromises()
    let finish!: (value: TaskLogDetail) => void
    vi.mocked(api.getTaskLog).mockImplementation(() => new Promise(resolve => { finish = resolve }))
    await openFirst(wrapper)
    const signal = vi.mocked(api.getTaskLog).mock.calls[0]![1]!
    hidden.mockReturnValue(true)
    document.dispatchEvent(new Event('visibilitychange'))
    expect(signal.aborted).toBe(true)
    const calls = vi.mocked(api.listTaskLogs).mock.calls.length
    await vi.advanceTimersByTimeAsync(15000)
    expect(api.listTaskLogs).toHaveBeenCalledTimes(calls)
    finish({ ...detail, fields: { prompt: 'stale hidden response' } })
    await flushPromises()
    expect(wrapper.text()).not.toContain('stale hidden response')
    vi.mocked(api.getTaskLog).mockResolvedValue(detail)
    hidden.mockReturnValue(false)
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(api.listTaskLogs).toHaveBeenCalledTimes(calls + 1)
    expect(wrapper.text()).toContain('first prompt')
    wrapper.unmount()
    const stopped = vi.mocked(api.listTaskLogs).mock.calls.length
    await vi.advanceTimersByTimeAsync(15000)
    document.dispatchEvent(new Event('visibilitychange'))
    expect(api.listTaskLogs).toHaveBeenCalledTimes(stopped)
  })

  it('does not execute a delayed verification action after the user switches task details', async () => {
    const api = client()
    vi.mocked(api.listTaskLogs).mockResolvedValue(page([task, { ...task, id: 'video_other', model: 'second' }]))
    vi.mocked(api.getTaskLog).mockImplementation(async id => ({ ...detail, id, fields: { prompt: id } }))
    let execute!: () => Promise<unknown>
    let finish!: () => void
    const verified = new Promise<void>(resolve => { finish = resolve })
    const runMutation: RunPluginMutation = async action => { execute = action; await verified; return action() }
    const cancelMutation = vi.fn()
    const wrapper = render(api, { admin: true, editable: true, runMutation, cancelMutation })
    await flushPromises()
    await openFirst(wrapper)
    await wrapper.find('[data-native-maintenance] textarea').setValue('checked original upstream')
    await wrapper.find('[data-native-maintenance]').trigger('submit')
    await flushPromises()
    expect(execute).toBeTypeOf('function')
    const details = wrapper.findAll('button').filter(button => button.text() === 'taskLogs.details')
    await details[1]!.trigger('click')
    await flushPromises()
    expect(cancelMutation).toHaveBeenCalled()
    finish()
    await flushPromises()
    expect(api.maintainNativeTask).not.toHaveBeenCalled()
    expect(wrapper.find('[data-detail]').text()).toContain('video_other')
    expect(wrapper.find('[data-detail]').text()).not.toContain('taskLogs.loadFailed')
  })

  it('renders a public error instead of raw transport details', async () => {
    const api = client()
    vi.mocked(api.listTaskLogs).mockRejectedValue(new Error('Authorization: Bearer secret upstream URL'))
    const wrapper = render(api)
    await flushPromises()
    expect(wrapper.find('[role="alert"]').text()).toBe('taskLogs.loadFailed')
    expect(wrapper.text()).not.toContain('secret')
  })
})
