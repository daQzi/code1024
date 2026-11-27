import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import ModelPlazaContent from '../ModelPlazaContent.vue'
import type { ModelPlazaGroup, ModelPlazaResponse } from '@/api/modelPlaza'

const mockReplace = vi.fn().mockResolvedValue(undefined)
let mockRouteQuery: Record<string, string> = {}

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: mockRouteQuery }),
  useRouter: () => ({ replace: mockReplace })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
      locale: { value: 'zh' }
    })
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isAuthenticated: true })
}))

function makeGroup(overrides: Partial<ModelPlazaGroup> & { id: number; name: string }): ModelPlazaGroup {
  return {
    description: '',
    platform: 'openai',
    subscription_type: 'standard',
    rate_multiplier: 1,
    peak_rate_enabled: false,
    peak_start: '',
    peak_end: '',
    peak_rate_multiplier: 1,
    is_exclusive: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    video_rate_independent: false,
    video_rate_multiplier: 1,
    long_context_pricing_enabled: false,
    models: [],
    ...overrides
  }
}

const sampleResponse: ModelPlazaResponse = {
  description: '',
  groups: [
    makeGroup({
      id: 1,
      name: '国模专用',
      platform: 'openai',
      rate_multiplier: 0.15,
      models: [
        {
          id: 'mdl_gpt',
          name: 'gpt-5.4',
          platform: 'openai',
          protocols: ['openai_responses'],
          pricing: {
            billing_mode: 'token',
            input_price: 1e-6,
            output_price: 2e-6,
            cache_write_price: null,
            cache_read_price: null,
            image_input_price: null,
            image_output_price: null,
            per_request_price: null,
            intervals: []
          },
          official_pricing: null
        },
        {
          id: 'mdl_sora',
          name: 'sora-2',
          platform: 'openai',
          protocols: ['openai_video'],
          pricing: {
            billing_mode: 'per_request',
            input_price: null,
            output_price: null,
            cache_write_price: null,
            cache_read_price: null,
            image_input_price: null,
            image_output_price: null,
            per_request_price: 0.1,
            intervals: []
          },
          official_pricing: null
        }
      ]
    }),
    makeGroup({
      id: 2,
      name: 'Claude专线',
      platform: 'anthropic',
      rate_multiplier: 1,
      models: [
        {
          id: 'mdl_claude',
          name: 'claude-sonnet-4-6',
          platform: 'anthropic',
          protocols: ['anthropic_messages'],
          pricing: null,
          official_pricing: null
        }
      ]
    })
  ]
}

const CatalogStub = {
  name: 'PlazaModelCatalog',
  props: ['groups', 'view', 'groupId', 'window'],
  template: '<div data-testid="catalog-stub" :data-window="window" :data-view="view" />'
}

function mountContent(response: ModelPlazaResponse = sampleResponse) {
  return mount(ModelPlazaContent, {
    props: {
      response,
      loading: false,
      error: false,
      embedded: true
    },
    global: {
      stubs: {
        PlazaModelCatalog: CatalogStub,
        PlatformIcon: true,
        Icon: true,
        Teleport: true
      }
    }
  })
}

describe('ModelPlazaContent compact status and filter layout', () => {
  beforeEach(() => {
    mockRouteQuery = { group_id: 'all' }
    mockReplace.mockClear()
  })

  it('renders compact status banner with tooltip description and switches metric window', async () => {
    const wrapper = mountContent()

    const banner = wrapper.get('[data-testid="plaza-status-banner"]')
    expect(banner.find('[role="tooltip"]').text()).toContain('modelPlaza.catalog.recentStatusDesc')

    const catalog = wrapper.getComponent(CatalogStub)
    expect(catalog.props('window')).toBe('24h')

    await wrapper.get('[data-testid="plaza-window-90m"]').trigger('click')
    expect(catalog.props('window')).toBe('90m')

    wrapper.unmount()
  })

  it('renders unified filter panel with search input, inline select labels, view toggle, and facet chips', async () => {
    const wrapper = mountContent()

    const filterPanel = wrapper.get('[data-testid="plaza-filter-panel"]')
    const searchInput = filterPanel.get('input[type="text"]')
    expect(searchInput.attributes('placeholder')).toBe('modelPlaza.filters.searchPlaceholder')

    const selectTriggers = filterPanel.findAll('.select-trigger')
    expect(selectTriggers).toHaveLength(2)
    expect(selectTriggers[0]!.text()).toContain('modelPlaza.catalog.kind')
    expect(selectTriggers[0]!.text()).toContain('modelPlaza.filters.all')
    expect(selectTriggers[1]!.text()).toContain('modelPlaza.catalog.protocol')
    expect(selectTriggers[1]!.text()).toContain('modelPlaza.filters.all')

    expect(filterPanel.get('[role="status"]').text()).toContain('"count":3')

    const catalog = wrapper.getComponent(CatalogStub)
    expect(catalog.props('view')).toBe('cards')

    const tableBtn = filterPanel
      .findAll('button')
      .find((btn) => btn.text() === 'modelPlaza.catalog.table')!
    await tableBtn.trigger('click')
    expect(catalog.props('view')).toBe('table')

    wrapper.unmount()
  })

  it('filters models by search query and resets cleanly with the clear button', async () => {
    const wrapper = mountContent()
    const filterPanel = wrapper.get('[data-testid="plaza-filter-panel"]')
    const searchInput = filterPanel.get('input[type="text"]')
    const catalog = wrapper.getComponent(CatalogStub)

    await searchInput.setValue('sora')
    expect(filterPanel.get('[role="status"]').text()).toContain('"count":1')
    const filtered = catalog.props('groups') as ModelPlazaGroup[]
    expect(filtered).toHaveLength(1)
    expect(filtered[0]!.models.map((m) => m.name)).toEqual(['sora-2'])

    const clearBtn = filterPanel.get('button[aria-label="common.clear"]')
    await clearBtn.trigger('click')
    expect((searchInput.element as HTMLInputElement).value).toBe('')
    expect(filterPanel.get('[role="status"]').text()).toContain('"count":3')

    wrapper.unmount()
  })

  it('coordinates platform, rate, and group chips with faceted disabling', async () => {
    const wrapper = mountContent()
    const filterPanel = wrapper.get('[data-testid="plaza-filter-panel"]')

    const anthropicChip = filterPanel
      .findAll('button')
      .find((btn) => btn.text().trim() === 'anthropic')!
    await anthropicChip.trigger('click')

    expect(filterPanel.get('[role="status"]').text()).toContain('"count":1')
    const catalog = wrapper.getComponent(CatalogStub)
    expect((catalog.props('groups') as ModelPlazaGroup[]).map((g) => g.name)).toEqual(['Claude专线'])

    // 0.15x only belongs to openai/国模专用, so it should be disabled when anthropic is active.
    const rate015Chip = filterPanel
      .findAll('button')
      .find((btn) => btn.text().trim() === '0.15x')!
    expect(rate015Chip.attributes('disabled')).toBeDefined()

    const rate1Chip = filterPanel
      .findAll('button')
      .find((btn) => btn.text().trim() === '1x')!
    expect(rate1Chip.attributes('disabled')).toBeUndefined()

    wrapper.unmount()
  })
})
