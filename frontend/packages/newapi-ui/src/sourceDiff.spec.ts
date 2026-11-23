import { describe, expect, it } from 'vitest'
import { diffPluginSource, MAX_DIFF_LINES, MAX_DIFF_UNITS } from './sourceDiff'

describe('bounded plugin source diff', () => {
  it('reconstructs both sources with correct line numbers and preserves exact line endings', () => {
    for (const [before, after] of [['a\nb\nc', 'a\nnew\nc'], ['', 'new\n'], ['a\r\nb', 'a\nb'], ['中\n文', '中\n🚀\n文'], ['same', 'same']]) {
      const lines = diffPluginSource(before!, after!)!
      expect(lines.filter(line => line.kind !== 'added').map(line => line.text).join('\n')).toBe(before)
      expect(lines.filter(line => line.kind !== 'removed').map(line => line.text).join('\n')).toBe(after)
      expect(lines.flatMap(line => line.before ? [line.before] : [])).toEqual(before!.split('\n').map((_, i) => i + 1))
      expect(lines.flatMap(line => line.after ? [line.after] : [])).toEqual(after!.split('\n').map((_, i) => i + 1))
    }
  })
  it('bounds allocation before splitting long source or creating the LCS table', () => {
    expect(diffPluginSource('x'.repeat(MAX_DIFF_UNITS + 1), 'x')).toBeNull()
    expect(diffPluginSource('x', 'x'.repeat(MAX_DIFF_UNITS + 1))).toBeNull()
    expect(diffPluginSource('\n'.repeat(MAX_DIFF_LINES), 'x')).toBeNull()
    expect(diffPluginSource('x', '\n'.repeat(MAX_DIFF_LINES))).toBeNull()
    expect(diffPluginSource('x'.repeat(MAX_DIFF_UNITS), 'x')).not.toBeNull()
  })
})
