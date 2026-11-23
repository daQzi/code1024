// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2023-2026 QuantumNous
// Adapted from QuantumNous/new-api source-diff.tsx at
// 48f9df96ea7c68eae04a0f3687869f232f48929f. The flat bounded table and line
// numbers are local; large sources use the separate plain source viewer.
export interface PluginSourceLine {
  kind: 'same' | 'added' | 'removed'
  text: string
  before?: number
  after?: number
}
export const MAX_DIFF_UNITS = 64 * 1024
export const MAX_DIFF_LINES = 1500

export function diffPluginSource(before: string, after: string): PluginSourceLine[] | null {
  if (before.length > MAX_DIFF_UNITS || after.length > MAX_DIFF_UNITS) return null
  const left = before.split('\n')
  const right = after.split('\n')
  if (left.length > MAX_DIFF_LINES || right.length > MAX_DIFF_LINES) return null
  const width = right.length + 1
  const lengths = new Uint16Array((left.length + 1) * width)
  for (let i = left.length - 1; i >= 0; i--) {
    for (let j = right.length - 1; j >= 0; j--) {
      lengths[i * width + j] = left[i] === right[j]
        ? lengths[(i + 1) * width + j + 1]! + 1
        : Math.max(lengths[(i + 1) * width + j]!, lengths[i * width + j + 1]!)
    }
  }
  const lines: PluginSourceLine[] = []
  let i = 0
  let j = 0
  while (i < left.length || j < right.length) {
    if (i < left.length && j < right.length && left[i] === right[j]) {
      lines.push({ kind: 'same', text: left[i]!, before: ++i, after: ++j })
    } else if (j < right.length && (i === left.length || lengths[i * width + j + 1]! >= lengths[(i + 1) * width + j]!)) {
      lines.push({ kind: 'added', text: right[j]!, after: ++j })
    } else {
      lines.push({ kind: 'removed', text: left[i]!, before: ++i })
    }
  }
  return lines
}
