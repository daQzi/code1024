// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f.
// See ../NOTICE.md for provenance and local adaptations.

package jsplugin

// asciiFold maps only 'A'–'Z' onto 'a'–'z'. Every other byte is unchanged.
// Unicode case folding is intentionally not applied: U+212A (KELVIN SIGN)
// must not become 'k' and impersonate an ASCII model name.
func asciiFold(s string) string {
	var buf []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if buf == nil {
				buf = []byte(s)
			}
			buf[i] = c + ('a' - 'A')
		}
	}
	if buf == nil {
		return s
	}
	return string(buf)
}

// ASCIIFold is the exported form of asciiFold for consumers outside this package.
func ASCIIFold(s string) string {
	return asciiFold(s)
}
