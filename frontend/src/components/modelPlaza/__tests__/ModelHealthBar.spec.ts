import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import ModelHealthBar from '../ModelHealthBar.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }),
}))

describe('ModelHealthBar', () => {
  it('colors >=95% green, 80-95% amber, <80% red, and unsampled intervals gray', () => {
    const end = '2026-11-25T10:00:00.000Z'
    const wrapper = mount(ModelHealthBar, {
      props: {
        end,
        bucketSeconds: 3600,
        recent: [
          { bucket: '2026-11-25T07:00:00.000Z', requests: 100, successes: 96 }, // 96% -> green
          { bucket: '2026-11-25T08:00:00.000Z', requests: 100, successes: 88 }, // 88% -> amber
          { bucket: '2026-11-25T09:00:00.000Z', requests: 100, successes: 50 }, // 50% -> red
        ],
      },
    })

    const bars = wrapper.findAll('[role="img"] span')
    expect(bars).toHaveLength(24)
    expect(bars[20]!.classes()).toContain('bg-emerald-500')
    expect(bars[21]!.classes()).toContain('bg-amber-400')
    expect(bars[22]!.classes()).toContain('bg-red-500')
    expect(bars[23]!.classes()).toContain('bg-gray-200')
  })

  it('renders 18 buckets for 90min (300s) and 12 buckets for 1h (300s)', () => {
    const w90 = mount(ModelHealthBar, {
      props: {
        start: '2026-11-25T08:30:00.000Z',
        end: '2026-11-25T10:00:00.000Z',
        bucketSeconds: 300,
      },
    })
    expect(w90.findAll('[role="img"] span')).toHaveLength(18)

    const w60 = mount(ModelHealthBar, {
      props: {
        start: '2026-11-25T09:00:00.000Z',
        end: '2026-11-25T10:00:00.000Z',
        bucketSeconds: 300,
      },
    })
    expect(w60.findAll('[role="img"] span')).toHaveLength(12)
  })
})
