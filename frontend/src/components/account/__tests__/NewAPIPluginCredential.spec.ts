import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import NewAPIPluginCredential from '../NewAPIPluginCredential.vue'
import { getPluginCredential, managePluginCredential, type PluginCredentialView } from '@/api/admin/newapiPlugins'

const { run } = vi.hoisted(() => ({ run: vi.fn((action: () => Promise<unknown>) => action()) }))
vi.mock('vue-i18n', async original => ({ ...(await original<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key, locale: { value: 'en-US' } }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run }), isStepUpCancelled: () => false }))
vi.mock('@/api/admin/newapiPlugins', () => ({ getPluginCredential: vi.fn(), managePluginCredential: vi.fn() }))
const initial = (): PluginCredentialView => ({ account_id: 17, plugin_version_id: 2, revision: 3, status: 'login_required' })
const fields = (): PluginCredentialView => ({ ...initial(), revision: 4, status: 'logging_in', login: { flow: 'host-flow', type: 'fields', expires_at: 1800000000, fields: [{ name: 'password', label: '<b>Password</b>', type: 'password', required: true }] } })
function render() { return mount(NewAPIPluginCredential, { props: { accountId: 17, versionId: 2, hooks: ['login', 'refresh', 'revoke', 'scheduled'] }, global: { stubs: { TotpStepUpDialog: true } } }) }

describe('saved account plugin credentials', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(getPluginCredential).mockResolvedValue(initial())
    vi.mocked(managePluginCredential).mockResolvedValue(fields())
  })

  it('loads only public state, and sends password input through explicit step-up actions', async () => {
    const wrapper = render()
    await flushPromises()
    expect(getPluginCredential).toHaveBeenCalledWith(17, expect.any(AbortSignal))
    expect(managePluginCredential).not.toHaveBeenCalled()
    await wrapper.get('[data-credential-login]').trigger('click')
    await flushPromises()
    expect(run).toHaveBeenCalledOnce()
    expect(managePluginCredential).toHaveBeenCalledWith(17, 'login', 3, {}, expect.any(AbortSignal))
    expect(wrapper.find('b').exists()).toBe(false)
    const password = wrapper.get('input[type="password"]')
    expect(password.attributes('autocomplete')).toBe('off')
    await password.setValue('private-password')
    vi.mocked(managePluginCredential).mockResolvedValueOnce({ ...initial(), revision: 5, status: 'active', credential: { plugin: 'fixture', account_id: 'identity', label: 'Fixture', expires_at: 0, needs_login: false } })
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(managePluginCredential).toHaveBeenLastCalledWith(17, 'continue', 4, { flow: 'host-flow', fields: { password: 'private-password' } }, expect.any(AbortSignal))
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('private-password')
    expect(wrapper.find('[data-credential-refresh]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('displays device information and submits polling only on administrator action', async () => {
    vi.mocked(getPluginCredential).mockResolvedValueOnce({ ...initial(), login: { flow: 'host-flow', type: 'device', user_code: 'ABCD', verification_uri: 'https://authorize.example', interval: 7, expires_at: 1800000000 } })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('ABCD')
    expect(wrapper.get('a').attributes('rel')).toContain('noreferrer')
    expect(managePluginCredential).not.toHaveBeenCalled()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(managePluginCredential).toHaveBeenCalledWith(17, 'continue', 3, { flow: 'host-flow', poll: true }, expect.any(AbortSignal))
    wrapper.unmount()
  })

  it('drops stale responses when switching accounts, and aborts requests on close', async () => {
    let resolve!: (value: PluginCredentialView) => void
    vi.mocked(getPluginCredential).mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const wrapper = render()
    const oldSignal = vi.mocked(getPluginCredential).mock.calls[0]![1]!
    vi.mocked(getPluginCredential).mockResolvedValueOnce({ ...initial(), account_id: 18, plugin_version_id: 3 })
    await wrapper.setProps({ accountId: 18, versionId: 3 })
    await flushPromises()
    expect(oldSignal.aborted).toBe(true)
    resolve(fields())
    await flushPromises()
    expect(wrapper.find('form').exists()).toBe(false)
    let finish!: (value: PluginCredentialView) => void
    vi.mocked(managePluginCredential).mockImplementationOnce(() => new Promise(done => { finish = done }))
    await wrapper.get('[data-credential-login]').trigger('click')
    await flushPromises()
    const signal = vi.mocked(managePluginCredential).mock.calls.at(-1)![4]!
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    finish(fields())
    await flushPromises()
  })

  it('clears password input and hides raw diagnostics on a failed or conflicting operation', async () => {
    vi.mocked(getPluginCredential).mockResolvedValueOnce(fields())
    const wrapper = render()
    await flushPromises()
    await wrapper.get('input[type="password"]').setValue('private-password')
    vi.mocked(managePluginCredential).mockRejectedValueOnce({ response: { status: 409, data: { message: 'diagnostic-private-password' } } })
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.plugins.credential.operationFailed')
    expect((wrapper.get('input[type="password"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.text()).not.toContain('private-password')
    wrapper.unmount()
  })

  it('rejects unsafe authorization URLs and mismatched account bindings', async () => {
    vi.mocked(getPluginCredential).mockResolvedValueOnce({ ...fields(), login: { flow: 'host-flow', type: 'redirect', url: 'javascript:alert(1)', expires_at: 1800000000 } })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.find('a').exists()).toBe(false)
    vi.mocked(managePluginCredential).mockResolvedValueOnce({ ...initial(), plugin_version_id: 99 })
    await wrapper.get('[data-credential-login]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-credential-refresh]').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.plugins.credential.operationFailed')
    wrapper.unmount()
  })
})
