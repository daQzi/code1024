import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import Toast from '../Toast.vue'
import { useAppStore } from '@/stores/app'

describe('Toast interactions', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setActivePinia(createPinia())
  })

  afterEach(() => {
    document.body.innerHTML = ''
    vi.useRealTimers()
  })

  it('pauses auto-dismiss and progress animation on hover and resumes on leave', async () => {
    const appStore = useAppStore()
    const wrapper = mount(Toast, {
      attachTo: document.body,
      global: { stubs: { Icon: true } }
    })

    appStore.showSuccess('Saved changes', 3000)
    await nextTick()

    const card = document.body.querySelector<HTMLElement>('.toast-card')
    const progress = document.body.querySelector<HTMLElement>('.toast-progress')
    expect(card).not.toBeNull()
    expect(progress?.style.animationPlayState).toBe('running')

    vi.advanceTimersByTime(1200)
    card!.dispatchEvent(new MouseEvent('mouseenter'))
    await nextTick()

    expect(appStore.toasts[0]?.paused).toBe(true)
    expect(document.body.querySelector<HTMLElement>('.toast-progress')?.style.animationPlayState).toBe('paused')

    vi.advanceTimersByTime(5000)
    expect(appStore.toasts).toHaveLength(1)

    card!.dispatchEvent(new MouseEvent('mouseleave'))
    await nextTick()
    expect(appStore.toasts[0]?.paused).toBe(false)

    vi.advanceTimersByTime(1800)
    expect(appStore.toasts).toHaveLength(0)
    wrapper.unmount()
  })
})
