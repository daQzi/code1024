import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import PluginRegistryPanel from './PluginRegistryPanel.vue'
import { dryRunPlugin, savePluginSubmission, transitionPluginSubmission } from '@/api/pluginRegistry'

const { run, item } = vi.hoisted(() => ({ run: vi.fn((action: () => Promise<unknown>) => action()), item: { id: 1, author_id: 7, key: 'community-7-example', version: '1.0.0', sha256: 'a'.repeat(64), source: 'export const meta = {};', readme: 'docs', status: 'submitted', revision: 2, meta: { name: 'Example' }, compatibility: { blockers: [], warnings: [] }, updated_at: '2026-11-21T00:00:00Z' } }))
vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run }), isStepUpCancelled: () => false }))
vi.mock('@/api/pluginRegistry', () => ({
  listPluginSubmissions: vi.fn().mockResolvedValue({ namespace: 'community-7-', items: [item], total: 1 }),
  getPluginSubmission: vi.fn().mockResolvedValue(item),
  savePluginSubmission: vi.fn().mockImplementation(async input => ({ ...item, ...input, status: 'draft' })),
  transitionPluginSubmission: vi.fn().mockResolvedValue(undefined), installPluginSubmission: vi.fn(),
  listPluginTemplates: vi.fn().mockResolvedValue(['ingress', 'task', 'provider'].map(kind => ({ kind, source: `export const meta = {key:"community-7-example"}; export function ${kind === 'ingress' ? 'ingress' : kind === 'task' ? 'buildSubmitRequest' : 'convertRequest'}(){}`, fixture: { hook: kind === 'ingress' ? 'ingress' : kind === 'task' ? 'buildSubmitRequest' : 'convertRequest', args: [{ config: {} }] } }))),
  dryRunPlugin: vi.fn().mockResolvedValue({ hook: 'ingress', result: { action: 'continue' }, sha256: 'b'.repeat(64), duration_ms: 4, offline: true })
}))
function render(mode: 'mine' | 'review') { return mount(PluginRegistryPanel, { props: { mode }, global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show" data-editor><slot /></div>' }, Pagination: true, TotpStepUpDialog: true } } }) }

describe('community plugin workflow', () => {
  beforeEach(() => vi.clearAllMocks())
  it('creates a namespaced draft and requires saving changed source before submission', async () => {
    const wrapper = render('mine'); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'pluginRegistry.create')!.trigger('click')
    expect((wrapper.find('textarea').element as HTMLTextAreaElement).value).toContain('community-7-example')
    await wrapper.findAll('button').find(b => b.text() === 'pluginRegistry.validateSave')!.trigger('click'); await flushPromises()
    expect(savePluginSubmission).toHaveBeenCalledTimes(1)
    await wrapper.find('textarea').setValue('changed source')
    const submit = wrapper.findAll('button').find(b => b.text() === 'pluginRegistry.submit')!
    expect(submit.attributes('disabled')).toBeDefined()
    expect(transitionPluginSubmission).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('keeps submitted source read-only and uses step-up for publication', async () => {
    const wrapper = render('review'); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'pluginRegistry.open')!.trigger('click'); await flushPromises()
    expect(wrapper.find('textarea').attributes('readonly')).toBeDefined()
    expect(wrapper.findAll('button').some(b => b.text() === 'pluginRegistry.validateSave')).toBe(false)
    await wrapper.findAll('button').find(b => b.text() === 'pluginRegistry.publish')!.trigger('click'); await flushPromises()
    expect(run).toHaveBeenCalledTimes(1)
    expect(transitionPluginSubmission).toHaveBeenCalledWith(1, 2, 'publish', '', true)
    wrapper.unmount()
  })
  it('uses a supported template and runs unsaved fixture source without publication or installation', async () => {
    const wrapper = render('mine'); await flushPromises()
    await wrapper.find('select').setValue('task')
    await wrapper.findAll('button').find(b => b.text() === 'pluginRegistry.create')!.trigger('click')
    await flushPromises()
    const source = (wrapper.find('textarea').element as HTMLTextAreaElement).value
    expect(source).toContain('buildSubmitRequest')
    const playground = wrapper.find('[data-playground]')
    expect((playground.find('textarea').element as HTMLTextAreaElement).value).toContain('buildSubmitRequest')
    await playground.find('button').trigger('click'); await flushPromises()
    expect(dryRunPlugin).toHaveBeenCalledWith({ source, hook: 'buildSubmitRequest', args: [{ config: {} }] }, expect.any(AbortSignal))
    expect(playground.find('pre').text()).toContain('continue')
    expect(savePluginSubmission).not.toHaveBeenCalled()
    expect(transitionPluginSubmission).not.toHaveBeenCalled()
    await wrapper.find('textarea').setValue('changed source')
    expect(playground.find('pre').exists()).toBe(false)
    await playground.find('textarea').setValue('broken JSON')
    await playground.find('button').trigger('click'); await flushPromises()
    expect(playground.text()).toContain('pluginRegistry.playground.invalidFixture')
    expect(dryRunPlugin).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
