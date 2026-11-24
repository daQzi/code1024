import { describe, expect, it } from 'vitest'
import type { CustomTopbarLink } from '@/types'
import { resolveTopbarLinkIcon, resolveVisibleTopbarLinks } from '../topbarLinks'

describe('topbarLinks utilities', () => {
  const sampleLinks: CustomTopbarLink[] = [
    {
      id: 'store',
      label: '商店入口',
      url: 'https://ai.code1024.cc/',
      icon: 'shoppingBag',
      icon_svg: '',
      visibility: 'all',
      sort_order: 2,
    },
    {
      id: 'canvas',
      label: '画布入口',
      url: 'https://qimu.code1024.cc/',
      icon: 'palette',
      icon_svg: '',
      visibility: 'all',
      sort_order: 0,
    },
    {
      id: 'user-only',
      label: '充值站',
      url: 'https://pay.code1024.cc/',
      icon: 'wallet',
      visibility: 'user',
      sort_order: 1,
    },
    {
      id: 'admin-only',
      label: '运维站',
      url: 'https://ops.code1024.cc/',
      icon: 'terminal',
      visibility: 'admin',
      sort_order: 3,
    },
  ]

  it('resolves valid preset icons and falls back to externalLink for unknown values', () => {
    expect(resolveTopbarLinkIcon('palette')).toBe('palette')
    expect(resolveTopbarLinkIcon('shoppingBag')).toBe('shoppingBag')
    expect(resolveTopbarLinkIcon('unknown-icon')).toBe('externalLink')
    expect(resolveTopbarLinkIcon('')).toBe('externalLink')
    expect(resolveTopbarLinkIcon(undefined)).toBe('externalLink')
  })

  it('filters visibility for regular user and sorts stably by sort_order', () => {
    const links = resolveVisibleTopbarLinks({
      publicLinks: sampleLinks,
      isAdmin: false,
      isAuthenticated: true,
    })
    expect(links.map((l) => l.id)).toEqual(['canvas', 'user-only', 'store'])
  })

  it('filters visibility for anonymous viewers to only all/default entries', () => {
    const links = resolveVisibleTopbarLinks({
      publicLinks: [
        ...sampleLinks,
        { id: 'implicit-all', label: '首页站', url: 'https://home.code1024.cc/', visibility: '' as 'all', sort_order: 5 },
      ],
      isAdmin: false,
      isAuthenticated: false,
    })
    expect(links.map((l) => l.id)).toEqual(['canvas', 'store', 'implicit-all'])
  })

  it('uses adminLinks when adminLoaded is true and hides user-only entries from admin', () => {
    const adminLinks: CustomTopbarLink[] = [
      { id: 'canvas', label: '画布入口', url: 'https://qimu.code1024.cc/', icon: 'palette', visibility: 'all', sort_order: 0 },
      { id: 'user-only', label: '充值站', url: 'https://pay.code1024.cc/', icon: 'wallet', visibility: 'user', sort_order: 1 },
      { id: 'admin-only', label: '运维站', url: 'https://ops.code1024.cc/', icon: 'terminal', visibility: 'admin', sort_order: 2 },
    ]
    const links = resolveVisibleTopbarLinks({
      publicLinks: sampleLinks,
      adminLinks,
      adminLoaded: true,
      isAdmin: true,
      isAuthenticated: true,
    })
    expect(links.map((l) => l.id)).toEqual(['canvas', 'admin-only'])

    const fallbackLinks = resolveVisibleTopbarLinks({
      publicLinks: sampleLinks,
      adminLinks: [],
      adminLoaded: false,
      isAdmin: true,
      isAuthenticated: true,
    })
    expect(fallbackLinks.map((l) => l.id)).toEqual(['canvas', 'store', 'admin-only'])
  })

  it('drops unsafe URLs, empty labels, and sanitizes custom SVG', () => {
    const links = resolveVisibleTopbarLinks({
      publicLinks: [
        { id: 'xss', label: 'Bad JS', url: 'javascript:alert(1)', visibility: 'all', sort_order: 0 },
        { id: 'rel', label: 'Relative', url: '/internal', visibility: 'all', sort_order: 1 },
        { id: 'blank', label: '   ', url: 'https://ok.code1024.cc/', visibility: 'all', sort_order: 2 },
        {
          id: 'svg-link',
          label: '画布入口',
          url: 'https://qimu.code1024.cc/',
          icon: 'unknown',
          icon_svg: '<svg onload="alert(1)" viewBox="0 0 24 24"><script>alert(1)</script><path d="M0 0h24v24H0z"/></svg>',
          visibility: 'all',
          sort_order: 3,
        },
      ],
      isAdmin: false,
      isAuthenticated: true,
    })
    expect(links).toHaveLength(1)
    expect(links[0].id).toBe('svg-link')
    expect(links[0].iconName).toBe('externalLink')
    expect(links[0].iconSvg).toContain('<svg')
    expect(links[0].iconSvg).not.toContain('onload')
    expect(links[0].iconSvg).not.toContain('<script')
  })
})
