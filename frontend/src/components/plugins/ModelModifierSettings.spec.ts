import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Panel from './ModelModifierSettings.vue'
import { getModelModifierSettings, saveModelModifierSettings, type ModelModifierSettings } from '@/api/admin/requestPlugins'

const { run } = vi.hoisted(() => ({ run: vi.fn((action: () => Promise<unknown>) => action()) }))
vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run }), isStepUpCancelled: (e: Error) => e.message === 'cancelled' }))
vi.mock('@/api/admin/requestPlugins', () => ({ getModelModifierSettings: vi.fn(), saveModelModifierSettings: vi.fn() }))
const fixture = (): ModelModifierSettings => ({ exemptions: ['custom@effort:high'], use_startup: false, revision: 'revision-1', startup_exemptions: ['startup@effort:low'] })
const render = () => mount(Panel, { global: { stubs: { TotpStepUpDialog: true } } })
async function open(wrapper: ReturnType<typeof render>) {
  const details = wrapper.get('details')
  ;(details.element as HTMLDetailsElement).open = true
  await details.trigger('toggle')
  await flushPromises()
}

describe('model name exemption settings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(getModelModifierSettings).mockResolvedValue(fixture())
    vi.mocked(saveModelModifierSettings).mockResolvedValue({ ...fixture(), revision: 'revision-2' })
  })
  it('loads lazily and saves explicit empty overrides with the observed revision and step-up', async () => {
    const wrapper = render()
    await flushPromises()
    expect(getModelModifierSettings).not.toHaveBeenCalled()
    await open(wrapper)
    expect(getModelModifierSettings).toHaveBeenCalledTimes(1)
    await wrapper.get('textarea').setValue('')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(run).toHaveBeenCalledTimes(1)
    expect(saveModelModifierSettings).toHaveBeenCalledWith({ exemptions: [], use_startup: false, revision: 'revision-1' }, expect.any(AbortSignal))
    expect(wrapper.get('[role=status]').text()).toBe('requestPlugins.modifiersSaved')
    wrapper.unmount()
  })
  it('shows startup entries when checked and preserves the custom draft when unchecked', async () => {
    const wrapper = render()
    await open(wrapper)
    await wrapper.get('textarea').setValue('draft@temperature:0.2')
    await wrapper.get('input[type=checkbox]').setValue(true)
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('startup@effort:low')
    expect(wrapper.get('textarea').attributes('disabled')).toBeDefined()
    await wrapper.get('input[type=checkbox]').setValue(false)
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('draft@temperature:0.2')
    await wrapper.get('input[type=checkbox]').setValue(true)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(saveModelModifierSettings).toHaveBeenCalledWith({ exemptions: [], use_startup: true, revision: 'revision-1' }, expect.any(AbortSignal))
    wrapper.unmount()
  })
  it('keeps the draft after a conflict and does not treat step-up cancellation as an error', async () => {
    const wrapper = render()
    await open(wrapper)
    await wrapper.get('textarea').setValue('draft@effort:high')
    vi.mocked(saveModelModifierSettings).mockRejectedValueOnce({ response: { data: { message: 'refresh required' } } })
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role=alert]').text()).toBe('refresh required')
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('draft@effort:high')
    run.mockRejectedValueOnce(new Error('cancelled'))
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[role=alert]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('validates both UTF-8 byte and entry limits before attempting a save', async () => {
    const wrapper = render()
    await open(wrapper)
    for (const text of ['界'.repeat(171), Array.from({ length: 129 }, (_, index) => `model-${index}`).join('\n')]) {
      await wrapper.get('textarea').setValue(text)
      await wrapper.get('form').trigger('submit')
      await flushPromises()
      expect(wrapper.get('[role=alert]').text()).toBe('requestPlugins.modifiersLimit')
    }
    expect(run).not.toHaveBeenCalled()
    expect(saveModelModifierSettings).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('cancels loading on unmount and ignores a late response', async () => {
    let finish!: (value: ModelModifierSettings) => void
    vi.mocked(getModelModifierSettings).mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    const wrapper = render()
    await open(wrapper)
    const signal = vi.mocked(getModelModifierSettings).mock.calls[0]![0]!
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    finish(fixture())
    await flushPromises()
    expect(saveModelModifierSettings).not.toHaveBeenCalled()
  })
})
