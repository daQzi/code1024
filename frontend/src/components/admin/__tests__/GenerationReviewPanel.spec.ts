import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import GenerationReviewPanel from '../GenerationReviewPanel.vue'
import SelectStub from '@/__tests__/stubs/SelectStub'
import { listGenerationReviews, reconcileGeneration, type GenerationReviewTask } from '@/api/admin/newapiPlugins'

vi.mock('vue-i18n', async () => ({ ...(await vi.importActual('vue-i18n')), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/api/admin/newapiPlugins', () => ({ listGenerationReviews: vi.fn(), reconcileGeneration: vi.fn() }))
vi.mock('@/composables/useStepUp', () => ({ isStepUpCancelled: () => false, useStepUp: () => ({ run: (action: () => Promise<unknown>) => action() }) }))

const task: GenerationReviewTask = { id: 'video_fixture', model: 'fixture-video', account_id: 3, user_id: 4, status: 'submission_uncertain', billing_status: 'review', hold_amount: '1', upstream_id: '', error_code: 'submission_uncertain', created_at: '2026-11-21T00:00:00Z' }
async function render(override: Partial<GenerationReviewTask> = {}) {
  vi.mocked(listGenerationReviews).mockResolvedValue([{ ...task, ...override }])
  const wrapper = mount(GenerationReviewPanel, { global: { stubs: { Select: SelectStub, TotpStepUpDialog: true } } })
  await flushPromises()
  await wrapper.findAll('button').find(button => button.text() === 'admin.plugins.js.review.open')!.trigger('click')
  return wrapper
}
beforeEach(() => vi.clearAllMocks())

describe('video task reconciliation', () => {
  it('resumes polling with confirmed evidence and never resubmits generation', async () => {
    const wrapper = await render()
    await wrapper.find('input').setValue('confirmed-upstream-id')
    await wrapper.find('textarea').setValue('upstream confirmed acceptance')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(reconcileGeneration).toHaveBeenCalledWith('video_fixture', { action: 'resume_poll', upstream_id: 'confirmed-upstream-id', reason: 'upstream confirmed acceptance' })
  })
  it('refunds only after an explicit action and written evidence', async () => {
    const wrapper = await render()
    await wrapper.find('select').setValue('refund')
    await wrapper.find('form').trigger('submit')
    expect(reconcileGeneration).not.toHaveBeenCalled()
    await wrapper.find('textarea').setValue('upstream confirms no job exists')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(reconcileGeneration).toHaveBeenCalledWith('video_fixture', { action: 'refund', reason: 'upstream confirms no job exists' })
  })
  it('rejects an amount above the hold and cannot reopen a terminal task', async () => {
    const wrapper = await render({ status: 'succeeded', upstream_id: 'completed-id' })
    expect(wrapper.find('option[value="resume_poll"]').exists()).toBe(false)
    await wrapper.find('input').setValue('2')
    await wrapper.find('textarea').setValue('upstream invoice checked')
    await wrapper.find('form').trigger('submit')
    expect(reconcileGeneration).not.toHaveBeenCalled()
    await wrapper.find('input').setValue('0.4')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(reconcileGeneration).toHaveBeenCalledWith('video_fixture', { action: 'settle', amount: '0.4', reason: 'upstream invoice checked' })
  })
})
