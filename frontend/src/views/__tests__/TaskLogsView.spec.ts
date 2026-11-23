import { ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import TaskLogsView from '../TaskLogsView.vue'
import { getTaskLog, listTaskLogs, maintainNativeTask, type TaskLog, type TaskLogDetail } from '@/api/taskLogs'

vi.mock('vue-i18n', async () => ({ ...(await vi.importActual('vue-i18n')), useI18n: () => ({ t: (key: string) => key, locale: ref('en') }) }))
vi.mock('@/api/taskLogs', () => ({ listTaskLogs: vi.fn(), getTaskLog: vi.fn(), downloadTaskContent: vi.fn(), maintainNativeTask: vi.fn() }))
vi.mock('@/components/admin/GenerationReviewPanel.vue', () => ({ default: { template: '<div data-review />' } }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div data-step-up />' } }))

const task: TaskLog = { id: 'video_fixture', model: 'video-model', group_id: 1, group_name: 'video-group', platform: 'openai', protocol: 'openai_video', action: 'text_to_video', plugin_key: 'example', plugin_version: '1.0', status: 'running', delivery_status: 'pending', billing_status: 'held', progress: 0, hold_amount: '1', actual_amount: null, created_at: '2026-11-21T00:00:00Z', updated_at: '2026-11-21T00:00:00Z', finished_at: null }
beforeEach(() => {
  vi.useFakeTimers(); vi.clearAllMocks()
  vi.mocked(listTaskLogs).mockResolvedValue({ items: [task], total: 1, page: 1, page_size: 20, pages: 1 })
  vi.mocked(getTaskLog).mockResolvedValue({ ...task, fields: { prompt: 'Ocean' }, events: [] })
})
afterEach(() => { vi.useRealTimers() })
async function render(admin = false) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/tasks', component: TaskLogsView, meta: { requiresAdmin: admin } }] })
  await router.push('/tasks')
  const wrapper = mount(TaskLogsView, { global: { plugins: [router], stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    BaseDialog: { props: ['show'], template: '<div v-if="show" data-detail><slot /></div>' },
    Pagination: true
  } } })
  await flushPromises()
  return wrapper
}

describe('task log console', () => {
  it('loads user history, applies filters and displays owned task details', async () => {
    const wrapper = await render()
    expect(listTaskLogs).toHaveBeenCalledWith(false, expect.objectContaining({ scope: 'mine', page: 1 }), expect.any(AbortSignal))
    expect(wrapper.text()).toContain('video-model')
    expect(wrapper.text()).toContain('taskLogs.waitingProgress')
    expect(wrapper.find('[data-review]').exists()).toBe(false)
    await wrapper.findAll('input')[1]!.setValue('video_fixture')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(listTaskLogs).toHaveBeenLastCalledWith(false, expect.objectContaining({ task_id: 'video_fixture' }), expect.any(AbortSignal))
    await wrapper.findAll('button').find(button => button.text() === 'taskLogs.details')!.trigger('click')
    await flushPromises()
    expect(getTaskLog).toHaveBeenCalledWith(false, task.id, expect.any(AbortSignal))
    expect(wrapper.find('[data-detail]').text()).toContain('Ocean')
    wrapper.unmount()
  })
  it('moves reconciliation into its own admin task view and stops polling after unmount', async () => {
    const wrapper = await render(true)
    await wrapper.findAll('button').find(button => button.text() === 'taskLogs.tabs.review')!.trigger('click')
    expect(wrapper.find('[data-review]').exists()).toBe(true)
    expect(wrapper.find('table').exists()).toBe(false)
    wrapper.unmount()
    const calls = vi.mocked(listTaskLogs).mock.calls.length
    await vi.advanceTimersByTimeAsync(10000)
    expect(listTaskLogs).toHaveBeenCalledTimes(calls)
  })
  it('filters tasks needing attention independently of the financial reconciliation view', async () => {
    const wrapper = await render(true)
    await wrapper.find('form input[type="checkbox"]').setValue(true)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(listTaskLogs).toHaveBeenLastCalledWith(true, expect.objectContaining({ review: true, scope: 'all' }), expect.any(AbortSignal))
    expect(wrapper.find('table').exists()).toBe(true)
    expect(wrapper.find('[data-review]').exists()).toBe(false)
    await wrapper.findAll('button').find(button => button.text() === 'common.reset')!.trigger('click')
    await flushPromises()
    expect(listTaskLogs).toHaveBeenLastCalledWith(true, expect.objectContaining({ review: false }), expect.any(AbortSignal))
    wrapper.unmount()
  })
  it('shows unconfirmed native costs as unknown and keeps native downloads on their original API', async () => {
    const native: TaskLog = { ...task, id: 'native_video_fixture', source: 'native', plugin_key: 'native-xai_video', plugin_version: '', status: 'succeeded', delivery_status: 'ready', billing_status: 'unconfirmed', hold_amount: '0', finished_at: '2026-11-21T00:01:00Z' }
    vi.mocked(listTaskLogs).mockResolvedValue({ items: [native], total: 1, page: 1, page_size: 20, pages: 1 })
    vi.mocked(getTaskLog).mockResolvedValue({ ...native, fields: {}, events: [{ type: 'native_succeeded', created_at: native.finished_at! }] })
    const wrapper = await render()
    expect(wrapper.text()).toContain('taskLogs.nativeSource')
    expect(wrapper.text()).toContain('taskLogs.billingStatuses.unconfirmed')
    expect(wrapper.text()).not.toContain('$0')
    await wrapper.findAll('button').find(button => button.text() === 'taskLogs.details')!.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-detail]').text()).toContain('taskLogs.nativeHint')
    expect(wrapper.find('[data-detail]').text()).toContain('taskLogs.events.native_succeeded')
    expect(wrapper.findAll('button').some(button => button.text() === 'taskLogs.download')).toBe(false)
    wrapper.unmount()
  })
  it('shows a zero-cost native ledger entry as settled', async () => {
    vi.mocked(listTaskLogs).mockResolvedValue({ items: [{ ...task, source: 'native', billing_status: 'settled', actual_amount: '0' }], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = await render()
    expect(wrapper.text()).toContain('taskLogs.billingStatuses.settled')
    expect(wrapper.text()).toContain('$0')
    wrapper.unmount()
  })
  it.each(['pending', 'review', 'not_charged'])('keeps native %s billing separate from successful generation', async (billing) => {
    const native: TaskLog = { ...task, source: 'native', status: 'succeeded', delivery_status: 'ready', billing_status: billing, hold_amount: '0', actual_amount: null }
    vi.mocked(listTaskLogs).mockResolvedValue({ items: [native], total: 1, page: 1, page_size: 20, pages: 1 })
    vi.mocked(getTaskLog).mockResolvedValue({ ...native, fields: {}, error_code: billing === 'review' ? 'native_billing_review' : '', events: [{ type: billing === 'not_charged' ? 'native_billing_logged' : 'native_billing_review', created_at: native.updated_at }] })
    const wrapper = await render()
    expect(wrapper.text()).toContain('taskLogs.statuses.succeeded')
    expect(wrapper.text()).toContain(`taskLogs.billingStatuses.${billing}`)
    expect(wrapper.text()).not.toContain('$0')
    await wrapper.findAll('button').find(button => button.text() === 'taskLogs.details')!.trigger('click')
    await flushPromises()
    if (billing === 'review') {
      expect(wrapper.find('[data-detail]').text()).toContain('taskLogs.billingIssue')
      expect(wrapper.find('[data-detail]').text()).not.toContain('taskLogs.failure')
    }
    expect(wrapper.find('[data-detail]').text()).toContain(`taskLogs.events.${billing === 'not_charged' ? 'native_billing_logged' : 'native_billing_review'}`)
    wrapper.unmount()
  })
  it.each(['active', 'review', 'complete'] as const)('shows native monitoring %s independently of generation and billing', async (monitorStatus) => {
    const native: TaskLog = {
      ...task, source: 'native', status: 'running', billing_status: 'unconfirmed',
      monitor_status: monitorStatus, monitor_error_code: monitorStatus === 'review' ? 'native_monitor_connection_changed' : ''
    }
    vi.mocked(listTaskLogs).mockResolvedValue({ items: [native], total: 1, page: 1, page_size: 20, pages: 1 })
    vi.mocked(getTaskLog).mockResolvedValue({ ...native, fields: {}, events: monitorStatus === 'review' ? [{ type: 'native_monitor_review', created_at: native.updated_at }] : [] })
    const wrapper = await render()
    expect(wrapper.text()).toContain(`taskLogs.monitorStatuses.${monitorStatus}`)
    expect(wrapper.text()).toContain('taskLogs.statuses.running')
    expect(wrapper.text()).toContain('taskLogs.billingStatuses.unconfirmed')
    expect(wrapper.text()).not.toContain('$1')
    await wrapper.findAll('button').find(button => button.text() === 'taskLogs.details')!.trigger('click')
    await flushPromises()
    const detail = wrapper.find('[data-detail]').text()
    expect(detail).toContain('taskLogs.monitoring')
    expect(detail).toContain(`taskLogs.monitorStatuses.${monitorStatus}`)
    expect(detail).not.toContain('taskLogs.failure')
    if (monitorStatus === 'review') {
      expect(detail).toContain('taskLogs.monitorErrors.connection_changed')
      expect(detail).toContain('taskLogs.events.native_monitor_review')
    }
    wrapper.unmount()
  })
  it.each(['resume_poll', 'retry_settlement'] as const)('lets an administrator submit a reviewed %s without new generation', async (action) => {
    const native: TaskLogDetail = { ...task, id: 'native_video_fixture', source: 'native', status: 'succeeded', billing_status: 'review', fields: {}, events: [], native_actions: [action] }
    vi.mocked(listTaskLogs).mockResolvedValue({ items: [native], total: 1, page: 1, page_size: 20, pages: 1 })
    vi.mocked(getTaskLog).mockResolvedValueOnce(native).mockResolvedValue({ ...native, native_actions: [] })
    vi.mocked(maintainNativeTask).mockResolvedValue({ updated: true })
    const wrapper = await render(true)
    await wrapper.findAll('button').find(button => button.text() === 'taskLogs.details')!.trigger('click')
    await flushPromises()
    const form = wrapper.find('[data-native-maintenance]')
    expect(form.exists()).toBe(true)
    expect(form.find('button[type="submit"]').attributes('disabled')).toBeDefined()
    await form.find('textarea').setValue('  original upstream verified  ')
    await form.trigger('submit')
    await flushPromises()
    expect(maintainNativeTask).toHaveBeenCalledWith(native.id, action, 'original upstream verified', expect.any(AbortSignal))
    expect(wrapper.find('[data-native-maintenance]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('keeps recovery controls out of the user task log', async () => {
    vi.mocked(getTaskLog).mockResolvedValue({ ...task, source: 'native', native_actions: ['resume_poll'], fields: {}, events: [] })
    const wrapper = await render()
    await wrapper.findAll('button').find(button => button.text() === 'taskLogs.details')!.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-native-maintenance]').exists()).toBe(false)
    expect(maintainNativeTask).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
