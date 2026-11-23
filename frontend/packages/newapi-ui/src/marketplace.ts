/*
 * Marketplace index v1 contract derived from QuantumNous/new-api at
 * 48f9df96ea7c68eae04a0f3687869f232f48929f, web/src/features/task-plugins/lib/marketplace.ts.
 * Copyright (C) 2023-2026 QuantumNous. AGPL-3.0-or-later.
 * See newapi-engine/{NOTICE.md,LICENSE} at the repository root.
 */
import type { LocalizedText, PluginMarketplacePin, PluginMarketplaceSource } from './types'

export interface MarketplaceDocument { path: string; sha256?: string; translations?: Record<string, MarketplaceDocument> }
export interface MarketplaceVersion {
  version: string; path: string; sha256?: string; minApiVersion: number; kind: string
  allowedHosts: string[]; baseUrl?: string; auth?: string
  readme?: MarketplaceDocument; changelog?: MarketplaceDocument
}
export interface MarketplacePlugin {
  key: string; name: string; description?: LocalizedText; latest: string; versions: MarketplaceVersion[]
  author?: string; repo?: string; code?: string; models: string[]; recommended: boolean; installs30d: number; sortPriority: number
}
export interface MarketplaceIndex { name: string; plugins: MarketplacePlugin[]; revoked: { sha256: string; reason?: LocalizedText }[] }
export const communityMarketplaceURL = 'https://www.newapi.ai/api/v1/community/index.json'
const digestPattern = /^[a-f0-9]{64}$/i
const record = (value: unknown): Record<string, unknown> | undefined => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined
const text = (value: unknown, max = 2048) => typeof value === 'string' ? value.trim().slice(0, max) : ''
const strings = (value: unknown) => Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string').slice(0, 256).map(v => v.slice(0, 512)) : []
const localized = (value: unknown): LocalizedText | undefined => {
  if (typeof value === 'string') return value.slice(0, 8192)
  const raw = record(value)
  return raw ? Object.fromEntries(Object.entries(raw).slice(0, 16).filter((entry): entry is [string, string] => typeof entry[1] === 'string').map(([key, value]) => [key.slice(0, 32), value.slice(0, 8192)])) : undefined
}

export function marketplaceSourceKind(url: string): 'official' | 'community' | 'third_party' {
  if (['https://www.newapi.ai/api/v1/plugins/index.json', 'https://raw.githubusercontent.com/QuantumNous/new-api-plugins/main/index.json'].includes(url)) return 'official'
  return url === 'https://www.newapi.ai/api/v1/community/index.json' ? 'community' : 'third_party'
}

export function marketplaceURL(value: string): URL {
  const url = new URL(value)
  if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password || url.hash || url.search || value.length > 2048 || /[\r\n\t\\]/.test(value)) throw new Error('pluginRegistry.external.errors.url')
  return url
}

export function resolveMarketplaceFile(indexURL: string, path: string): string {
  const base = marketplaceURL(indexURL)
  // A relative file may not smuggle credentials, a fragment or another origin.
  const url = marketplaceURL(new URL(path, base).href)
  if (!path.trim() || path.length > 2048 || url.origin !== base.origin) throw new Error('pluginRegistry.external.errors.path')
  return url.href
}

function document(value: unknown, translations = true): MarketplaceDocument | undefined {
  const raw = record(value)
  if (!raw || typeof raw.path !== 'string' || !raw.path.trim() || raw.path.length > 2048 || raw.sha256 !== undefined && (typeof raw.sha256 !== 'string' || !digestPattern.test(raw.sha256))) return undefined
  const result: MarketplaceDocument = { path: raw.path, sha256: raw.sha256 as string | undefined }
  const variants = record(raw.translations)
  if (translations && variants) result.translations = Object.fromEntries(Object.entries(variants).slice(0, 10).flatMap(([tag, value]) => { const file = document(value, false); return file ? [[tag, file]] : [] }))
  return result
}

export function parseMarketplaceIndex(payload: unknown): MarketplaceIndex {
  const raw = record(payload)
  if (!raw || raw.indexVersion !== 1 || !Array.isArray(raw.plugins) || raw.plugins.length > 2000 || raw.revoked !== undefined && !Array.isArray(raw.revoked)) throw new Error('pluginRegistry.external.errors.index')
  const plugins: MarketplacePlugin[] = []
  const keys = new Set<string>()
  for (const candidate of raw.plugins) {
    const entry = record(candidate)
    if (!entry || typeof entry.key !== 'string' || !/^[a-z0-9][a-z0-9_-]{0,29}$/.test(entry.key) || !Array.isArray(entry.versions) || entry.versions.length > 100) continue
    if (keys.has(entry.key)) throw new Error('pluginRegistry.external.errors.index')
    keys.add(entry.key)
    const versions: MarketplaceVersion[] = []
    const versionNames = new Set<string>()
    for (const candidate of entry.versions) {
      const v = record(candidate)
      if (!v || typeof v.version !== 'string' || !v.version.trim() || v.version.length > 128 || typeof v.path !== 'string' || !v.path.trim() || v.path.length > 2048 || v.kind !== undefined && !['task', 'provider', 'transform'].includes(String(v.kind))) continue
      if (versionNames.has(v.version)) throw new Error('pluginRegistry.external.errors.index')
      versionNames.add(v.version)
      versions.push({ version: v.version, path: v.path, sha256: typeof v.sha256 === 'string' && digestPattern.test(v.sha256) ? v.sha256.toLowerCase() : undefined, minApiVersion: v.minApiVersion === undefined ? 1 : typeof v.minApiVersion === 'number' && Number.isSafeInteger(v.minApiVersion) && v.minApiVersion > 0 ? v.minApiVersion : Infinity, kind: text(v.kind) || 'task', allowedHosts: strings(v.allowedHosts), baseUrl: text(v.baseUrl) || undefined, auth: text(v.auth) || undefined, readme: document(v.readme), changelog: document(v.changelog) })
    }
    if (!versions.length) continue
    let repo: string | undefined
    try { if (typeof entry.repo === 'string') repo = marketplaceURL(entry.repo).href } catch { /* Invalid cosmetic links are omitted. */ }
    plugins.push({ key: entry.key, name: text(entry.name, 200) || entry.key, description: localized(entry.description), latest: versions.some(v => v.version === entry.latest) ? entry.latest as string : versions[0]!.version, versions, author: text(record(entry.author)?.login, 64) || undefined, repo, code: text(entry.code, 64) || undefined, models: strings(entry.models), recommended: entry.recommended === true, installs30d: typeof entry.installs30d === 'number' && Number.isSafeInteger(entry.installs30d) && entry.installs30d >= 0 ? entry.installs30d : 0, sortPriority: typeof entry.sortPriority === 'number' && Number.isSafeInteger(entry.sortPriority) ? entry.sortPriority : 0 })
  }
  return { name: text(raw.name, 200), plugins: plugins.sort((a, b) => Number(b.recommended) - Number(a.recommended) || b.installs30d - a.installs30d || b.sortPriority - a.sortPriority || a.key.localeCompare(b.key)), revoked: (raw.revoked as unknown[] | undefined ?? []).flatMap(value => { const item = record(value); return item && typeof item.sha256 === 'string' && digestPattern.test(item.sha256) ? [{ sha256: item.sha256.toLowerCase(), reason: localized(item.reason) }] : [] }) }
}

// Each browser operation has a byte cap and an overall deadline, including
// consumption of the response body. No credentials or referring page are sent.
export async function fetchMarketplaceText(url: string, limit: number, signal: AbortSignal): Promise<string> {
  marketplaceURL(url)
  return readMarketplaceText(url, limit, signal)
}

class MarketplaceHTTPError extends Error {
  constructor(readonly status: number) { super(`HTTP ${status}`) }
}

async function readMarketplaceText(url: string, limit: number, signal: AbortSignal): Promise<string> {
  const controller = new AbortController()
  const abort = () => controller.abort(signal.reason)
  signal.addEventListener('abort', abort, { once: true })
  if (signal.aborted) abort()
  const timer = setTimeout(() => controller.abort(new Error('pluginRegistry.external.errors.timeout')), 20000)
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
  let body: ReadableStream<Uint8Array> | null = null
  try {
    const response = await fetch(url, { signal: controller.signal, credentials: 'omit', redirect: 'error', cache: 'no-store', referrerPolicy: 'no-referrer' })
    body = response.body
    if (!response.ok) throw new MarketplaceHTTPError(response.status)
    if (response.redirected) throw new Error('pluginRegistry.external.errors.path')
    if (!response.body) throw new Error('pluginRegistry.external.errors.empty')
    if (Number(response.headers.get('content-length')) > limit) throw new Error('pluginRegistry.external.errors.large')
    reader = response.body.getReader()
    const buffer = new Uint8Array(limit)
    let length = 0
    for (;;) {
      controller.signal.throwIfAborted()
      const { value, done } = await reader.read()
      controller.signal.throwIfAborted()
      if (done) break
      if (value.byteLength > limit - length) throw new Error('pluginRegistry.external.errors.large')
      buffer.set(value, length); length += value.byteLength
    }
    return new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(buffer.subarray(0, length))
  } catch (error) {
    if (controller.signal.aborted) throw controller.signal.reason
    throw error
  } finally {
    clearTimeout(timer); signal.removeEventListener('abort', abort)
    if (reader) { void reader.cancel().catch(() => {}); reader.releaseLock() }
    else if (body) void body.cancel().catch(() => {})
    controller.abort()
  }
}

export async function sourceSHA256(source: string): Promise<string> {
  const hash = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(source))
  return Array.from(new Uint8Array(hash), value => value.toString(16).padStart(2, '0')).join('')
}

export async function fetchMarketplaceIndex(url: string, signal: AbortSignal): Promise<MarketplaceIndex> {
  return parseMarketplaceIndex(JSON.parse(await fetchMarketplaceText(url, 2 << 20, signal)))
}

// The only query-bearing download is New API's fixed community resolver.
// User input can supply eight digits, never a URL or arbitrary query string.
export async function resolveCommunityPlugin(code: string, signal: AbortSignal): Promise<MarketplaceIndex | null> {
  if (!/^\d{8}$/.test(code)) throw new Error('pluginRegistry.external.errors.code')
  const url = new URL('resolve', communityMarketplaceURL)
  url.searchParams.set('code', code)
  try {
    const index = parseMarketplaceIndex(JSON.parse(await readMarketplaceText(url.href, 2 << 20, signal)))
    if (index.plugins.length !== 1 || index.plugins[0]?.code !== code) throw new Error('pluginRegistry.external.errors.index')
    return index
  } catch (error) {
    if (error instanceof MarketplaceHTTPError && error.status === 404) return null
    throw error
  }
}

export function mergeMarketplaceLookup(index: MarketplaceIndex, resolved: MarketplaceIndex): MarketplaceIndex {
  // Replace a key as one immutable listing; never mix file paths/hashes from
  // separate releases. The current index's revocations always win.
  const keys = new Set(resolved.plugins.map(plugin => plugin.key))
  return { name: index.name, plugins: [...resolved.plugins, ...index.plugins.filter(plugin => !keys.has(plugin.key))], revoked: [...index.revoked, ...resolved.revoked] }
}

export interface MarketplaceLookup { source: PluginMarketplaceSource; index: MarketplaceIndex; code?: string }

export async function findMarketplacePluginByCode(code: string, sources: PluginMarketplaceSource[], signal: AbortSignal): Promise<MarketplaceLookup | null> {
  const wanted = code.trim()
  if (!/^\d{8}$/.test(wanted)) throw new Error('pluginRegistry.external.errors.code')
  const controller = new AbortController()
  const abort = () => controller.abort(signal.reason)
  signal.addEventListener('abort', abort, { once: true })
  if (signal.aborted) abort()
  const timer = setTimeout(() => controller.abort(new Error('pluginRegistry.external.errors.timeout')), 45000)
  let failure: unknown
  let communityIndex: MarketplaceIndex | undefined
  try {
    const ordered = [...sources].sort((a, b) => Number(marketplaceSourceKind(b.index_url) === 'official') - Number(marketplaceSourceKind(a.index_url) === 'official'))
    for (const source of ordered.slice(0, 16)) {
      controller.signal.throwIfAborted()
      try {
        const index = await fetchMarketplaceIndex(source.index_url, controller.signal)
        if (source.index_url === communityMarketplaceURL) communityIndex = index
        const plugin = index.plugins.find(item => item.code === wanted)
        if (plugin) return { source, index: { ...index, plugins: [plugin] } }
      } catch (error) { failure = error }
    }
    controller.signal.throwIfAborted()
    const community = sources.find(source => source.index_url === communityMarketplaceURL)
    if (community) {
      const index = await resolveCommunityPlugin(wanted, controller.signal)
      if (index) return { source: community, index: { ...(communityIndex ? mergeMarketplaceLookup(communityIndex, index) : index), plugins: index.plugins }, code: wanted }
    }
    if (failure) throw failure
    return null
  } finally { clearTimeout(timer); signal.removeEventListener('abort', abort); controller.abort() }
}

export async function fetchMarketplacePinIndex(indexURL: string, code: string | undefined, signal: AbortSignal): Promise<MarketplaceIndex> {
  const index = await fetchMarketplaceIndex(indexURL, signal)
  if (!code) return index
  if (indexURL !== communityMarketplaceURL) throw new Error('pluginRegistry.external.errors.url')
  const resolved = await resolveCommunityPlugin(code, signal)
  if (!resolved) throw new Error('pluginRegistry.external.errors.changed')
  return mergeMarketplaceLookup(index, resolved)
}

export function requireMarketplaceVersion(index: MarketplaceIndex, pin: PluginMarketplacePin): MarketplaceVersion {
  const entry = index.plugins.find(item => item.key === pin.key)?.versions.find(item => item.version === pin.version && item.sha256 === pin.sha256)
  if (!pin.sha256 || !entry) throw new Error('pluginRegistry.external.errors.changed')
  if (index.revoked.some(item => item.sha256 === pin.sha256)) throw new Error('pluginRegistry.external.errors.revoked')
  if (entry.minApiVersion > 1) throw new Error('pluginRegistry.external.errors.apiVersion')
  return entry
}

export async function fetchMarketplaceSource(indexURL: string, entry: MarketplaceVersion, signal: AbortSignal): Promise<string> {
  if (!entry.sha256) throw new Error('pluginRegistry.external.errors.hashMissing')
  const source = await fetchMarketplaceText(resolveMarketplaceFile(indexURL, entry.path), 1 << 20, signal)
  if (await sourceSHA256(source) !== entry.sha256) throw new Error('pluginRegistry.external.errors.hashMismatch')
  signal.throwIfAborted()
  return source
}

export async function fetchMarketplaceDocument(indexURL: string, doc: MarketplaceDocument, locale: string, signal: AbortSignal): Promise<string> {
  const selected = doc.translations?.[locale] ?? (locale.startsWith('zh') ? doc.translations?.['zh-CN'] : undefined) ?? doc
  const source = await fetchMarketplaceText(resolveMarketplaceFile(indexURL, selected.path), 64 << 10, signal)
  if (selected.sha256 && await sourceSHA256(source) !== selected.sha256.toLowerCase()) throw new Error('pluginRegistry.external.errors.hashMismatch')
  return source
}

// Browser I/O can be replaced by an embedding host without changing the panels.
export interface MarketplaceReadClient {
  fetchMarketplaceDocument: typeof fetchMarketplaceDocument
  fetchMarketplaceIndex: typeof fetchMarketplaceIndex
  fetchMarketplacePinIndex: typeof fetchMarketplacePinIndex
  fetchMarketplaceSource: typeof fetchMarketplaceSource
  findMarketplacePluginByCode: typeof findMarketplacePluginByCode
}
