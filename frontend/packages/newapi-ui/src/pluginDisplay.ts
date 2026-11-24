import type { LocalizedText, NewAPIPluginVersion, PluginTranslator, UsageFieldSchema, UsageProfile } from './types'

const knownFieldTypes = new Set(['number', 'boolean'])
const knownDetailUnits = new Set(['second', 'count', 'image', 'request', 'token', 'credit'])

export function localizedPluginText(value: LocalizedText | undefined, locale: string): string {
  if (typeof value === 'string') return value
  return value?.[locale] ?? value?.[locale.split('-')[0]!] ?? value?.en ?? value?.zh ?? ''
}

export function matchesPluginModelPattern(pattern: string, model: string): boolean {
  const target = model.trim().toLowerCase()
  const declared = pattern.trim().toLowerCase()
  if (!target || target.includes('*') || !declared) return false
  if (declared === '*') return true
  if (declared.endsWith('*') && !declared.slice(0, -1).includes('*') && declared.length > 1) {
    return target.startsWith(declared.slice(0, -1))
  }
  return declared === target
}

export function pluginUsageGroups(meta: NewAPIPluginVersion['meta']): UsageProfile[] {
  const profiles = meta.usageProfiles ?? []
  const profiled = new Set(profiles.flatMap(profile => profile.models.map(model => model.toLowerCase())))
  const defaults = (meta.models ?? []).filter(model => !profiled.has(model.toLowerCase()))
  const groups = [...profiles]
  if (Object.keys(meta.usageSchema ?? {}).length && (defaults.length || !profiles.length)) {
    groups.unshift({ models: defaults, schema: meta.usageSchema!, examples: meta.usageExamples })
  }
  return groups
}

export function pluginUsageGroupsForModel(meta: NewAPIPluginVersion['meta'], model: string): UsageProfile[] {
  const trimmed = model.trim()
  if (!trimmed) return []
  if (trimmed === '*') return pluginUsageGroups(meta)
  const matchedProfiles = (meta.usageProfiles ?? []).filter(profile =>
    profile.models.some(declared => matchesPluginModelPattern(declared, trimmed))
  )
  if (matchedProfiles.length) return matchedProfiles
  if (Object.keys(meta.usageSchema ?? {}).length) {
    return [{ models: [trimmed], schema: meta.usageSchema!, examples: meta.usageExamples }]
  }
  return []
}

export function pluginUsageFieldTypeLabel(field: UsageFieldSchema, t: PluginTranslator): string {
  if (field.enum?.length) return t('admin.plugins.detail.string')
  const kind = field.type || 'number'
  if (knownFieldTypes.has(kind)) return t(`admin.plugins.detail.${kind}`)
  if (kind === 'string') return t('admin.plugins.detail.text')
  return kind
}

export function pluginUsageUnitLabel(field: Pick<UsageFieldSchema, 'unit' | 'unitLabel'>, t: PluginTranslator, locale: string): string {
  const custom = localizedPluginText(field.unitLabel, locale)
  if (custom) return custom
  if (!field.unit) return ''
  return knownDetailUnits.has(field.unit) ? t(`admin.plugins.detail.units.${field.unit}`) : field.unit
}
