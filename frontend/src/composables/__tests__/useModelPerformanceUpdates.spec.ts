import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { useModelPerformanceUpdates } from '../useModelPerformanceUpdates'
import { watchModelPerformance } from '@/utils/modelPerformanceUpdates'

vi.mock('@/utils/modelPerformanceUpdates', () => ({ watchModelPerformance: vi.fn() }))

describe('visible performance subscription lifecycle', () => {
  afterEach(() => { vi.restoreAllMocks(); vi.clearAllMocks(); vi.useRealTimers() })
  it('switches scope, stops while hidden/unmounted, and keeps fallback polling', async () => {
    vi.useFakeTimers()
    let hidden = false
    vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden)
    vi.mocked(watchModelPerformance).mockImplementation(() => new Promise(() => undefined))
    const selected = ref(1), refresh = vi.fn()
    const wrapper = mount(defineComponent({ setup() { useModelPerformanceUpdates(() => ({ ids: [`mdl_${'a'.repeat(32)}`], groupId: selected.value }), refresh); return () => null } }))
    await flushPromises()
    expect(watchModelPerformance).toHaveBeenCalledOnce()
    const first = vi.mocked(watchModelPerformance).mock.calls[0]![0]
    selected.value = 2; await flushPromises()
    expect(first.signal.aborted).toBe(true)
    expect(vi.mocked(watchModelPerformance).mock.calls[1]![0].groupId).toBe(2)
    await vi.advanceTimersByTimeAsync(15000)
    expect(refresh).toHaveBeenCalled()
    hidden = true; document.dispatchEvent(new Event('visibilitychange')); await flushPromises()
    const second = vi.mocked(watchModelPerformance).mock.calls[1]![0]
    expect(second.signal.aborted).toBe(true)
    const before = refresh.mock.calls.length
    await vi.advanceTimersByTimeAsync(30000)
    expect(refresh).toHaveBeenCalledTimes(before)
    hidden = false; document.dispatchEvent(new Event('visibilitychange')); await flushPromises()
    const last = vi.mocked(watchModelPerformance).mock.calls.at(-1)![0]
    last.onScopeChanged()
    expect(refresh).toHaveBeenLastCalledWith(true)
    wrapper.unmount();expect(last.signal.aborted).toBe(true)
    const attempts = vi.mocked(watchModelPerformance).mock.calls.length
    await vi.advanceTimersByTimeAsync(60000)
    expect(watchModelPerformance).toHaveBeenCalledTimes(attempts)
  })
})
