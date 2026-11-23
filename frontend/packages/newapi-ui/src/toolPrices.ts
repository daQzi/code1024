export interface ProviderToolPriceSettings {
  prices: Record<string, number>
  revision: string
}

export interface ProviderToolPriceClient {
  read(signal?: AbortSignal): Promise<ProviderToolPriceSettings>
  save(input: ProviderToolPriceSettings, signal?: AbortSignal): Promise<ProviderToolPriceSettings>
}

export function parseProviderToolPrices(text: string): Record<string, number> {
  if (new TextEncoder().encode(text).length > 256 * 1024) throw new Error('invalid prices')
  const value: unknown = JSON.parse(text)
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('invalid prices')
  const entries = Object.entries(value)
  if (entries.length > 512) throw new Error('invalid prices')
  const seen = new Set<string>()
  for (const [key, price] of entries) {
    const colon = key.indexOf(':')
    const tool = colon < 0 ? key : key.slice(0, colon)
    const model = colon < 0 ? undefined : key.slice(colon + 1)
    if (!tool.trim() || key !== key.trim() || new TextEncoder().encode(key).length > 384 ||
      new TextEncoder().encode(tool).length > 128 || /\p{Cc}/u.test(key) || tool.includes('*') ||
      model !== undefined && (!model || /[:*]/.test(model.replace(/\*$/, ''))) ||
      typeof price !== 'number' || !Number.isFinite(price) || price < 0 || price > 1e12) throw new Error('invalid prices')
    const normalized = model === undefined ? key : `${tool}:${model.replace(/\*$/, '')}`
    if (seen.has(normalized)) throw new Error('ambiguous prices')
    seen.add(normalized)
  }
  return value as Record<string, number>
}
