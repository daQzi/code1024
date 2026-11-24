import type { CustomTopbarLink } from '@/types'
import { sanitizeSvg } from '@/utils/sanitize'
import { sanitizeUrl } from '@/utils/url'

export const TOPBAR_LINK_PRESET_ICONS = [
  'palette',
  'shoppingBag',
  'wallet',
  'creditCard',
  'gift',
  'sparkles',
  'photo',
  'rocket',
  'globe',
  'book',
  'grid',
  'bolt',
  'chat',
  'cube',
  'terminal',
  'externalLink',
] as const

export type TopbarPresetIconName = (typeof TOPBAR_LINK_PRESET_ICONS)[number]

export interface ResolvedTopbarLink {
  id: string
  label: string
  url: string
  iconName: TopbarPresetIconName
  iconSvg: string
}

const PRESET_ICON_SET = new Set<string>(TOPBAR_LINK_PRESET_ICONS)

export function resolveTopbarLinkIcon(icon?: string): TopbarPresetIconName {
  const trimmed = icon?.trim() || ''
  if (PRESET_ICON_SET.has(trimmed)) {
    return trimmed as TopbarPresetIconName
  }
  return 'externalLink'
}

function isLinkVisibleForViewer(
  visibility: string | undefined,
  options: { isAdmin: boolean; isAuthenticated: boolean },
): boolean {
  const normalized = visibility?.trim() || 'all'
  if (!options.isAuthenticated) {
    return normalized === 'all'
  }
  if (options.isAdmin) {
    return normalized !== 'user'
  }
  return normalized !== 'admin'
}

export function resolveVisibleTopbarLinks(options: {
  publicLinks?: CustomTopbarLink[]
  adminLinks?: CustomTopbarLink[]
  adminLoaded?: boolean
  isAdmin: boolean
  isAuthenticated: boolean
}): ResolvedTopbarLink[] {
  const source =
    options.isAdmin && options.adminLoaded
      ? (options.adminLinks ?? [])
      : (options.publicLinks ?? [])

  return source
    .map((item, idx) => ({ item, idx }))
    .filter(({ item }) => isLinkVisibleForViewer(item.visibility, options))
    .sort((a, b) => {
      const orderA = Number.isFinite(a.item.sort_order) ? a.item.sort_order : a.idx
      const orderB = Number.isFinite(b.item.sort_order) ? b.item.sort_order : b.idx
      if (orderA !== orderB) return orderA - orderB
      return a.idx - b.idx
    })
    .reduce<ResolvedTopbarLink[]>((acc, { item, idx }) => {
      const label = item.label?.trim() || ''
      const url = sanitizeUrl(item.url || '')
      if (!label || !url) return acc
      acc.push({
        id: item.id?.trim() || `${idx}-${url}`,
        label,
        url,
        iconName: resolveTopbarLinkIcon(item.icon),
        iconSvg: sanitizeSvg(item.icon_svg || ''),
      })
      return acc
    }, [])
}
