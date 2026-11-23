import type { LocalizedText, NewAPIPluginVersion, UsageProfile } from './types'

export function localizedPluginText(value: LocalizedText | undefined, locale: string): string {
  if (typeof value === 'string') return value
  return value?.[locale] ?? value?.[locale.split('-')[0]!] ?? value?.en ?? value?.zh ?? ''
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
