// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { URL } from 'node:url'

describe('independent UI dependencies', () => {
  it('does not import host APIs, routes, stores or native extensions', () => {
    const moduleURL = import.meta.url
    const root = new URL('.', moduleURL)
    for (const file of readdirSync(root).filter(file => /\.(ts|vue)$/.test(file) && !file.endsWith('.spec.ts'))) {
      const source = readFileSync(new URL(file, root), 'utf8')
      expect(source).not.toMatch(/from\s+['"](?:@\/|.*frontend\/src|.*src\/api|vue-router|vue-i18n|pinia)/)
      expect(source).not.toContain('NativePluginsPanel')
    }
  })
})
