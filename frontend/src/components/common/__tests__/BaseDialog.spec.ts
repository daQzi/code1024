import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import BaseDialog from '../BaseDialog.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

describe('BaseDialog', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    document.body.classList.remove('modal-open')
  })

  it('resets body scroll position when reopened', async () => {
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: false, title: 'Details' },
      slots: { default: '<div style="height: 2000px">content</div>' },
      global: { stubs: { Icon: true } }
    })

    await wrapper.setProps({ show: true })
    await nextTick()
    const body = document.body.querySelector<HTMLElement>('.modal-body')
    expect(body).not.toBeNull()
    body!.scrollTop = 480

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await nextTick()

    expect(document.body.querySelector<HTMLElement>('.modal-body')?.scrollTop).toBe(0)
    wrapper.unmount()
  })

  it('traps Tab and Shift+Tab focus within the open dialog', async () => {
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Focus Trap' },
      slots: {
        default: '<input id="first-input" type="text" /><button id="last-btn" type="button">Done</button>'
      },
      global: { stubs: { Icon: true } }
    })
    await nextTick()

    const closeBtn = document.body.querySelector<HTMLButtonElement>('button[aria-label="Close modal"]')!
    const lastBtn = document.body.querySelector<HTMLButtonElement>('#last-btn')!

    lastBtn.focus()
    expect(document.activeElement).toBe(lastBtn)

    const forwardTab = new KeyboardEvent('keydown', { key: 'Tab', cancelable: true })
    document.dispatchEvent(forwardTab)
    expect(forwardTab.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(closeBtn)

    const backwardTab = new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, cancelable: true })
    document.dispatchEvent(backwardTab)
    expect(backwardTab.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(lastBtn)

    wrapper.unmount()
  })

  it('uses drawer-right transition name when placement is right', async () => {
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: true, title: 'Drawer', placement: 'right' },
      global: {
        stubs: {
          Icon: true,
          Transition: {
            props: ['name'],
            template: '<div data-test-transition :data-name="name"><slot /></div>'
          }
        }
      }
    })
    await nextTick()

    const transitionEl = document.body.querySelector<HTMLElement>('[data-test-transition]')
    expect(transitionEl?.dataset.name).toBe('drawer-right')
    wrapper.unmount()
  })
})
