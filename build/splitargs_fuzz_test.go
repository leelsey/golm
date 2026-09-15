// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package build

import "testing"

func FuzzSplitArgs(f *testing.F) {
	f.Add(`tool --x "a b" 'c d'`)
	f.Add(`op read "op://vault/item/field with spaces"`)
	f.Add("a\\\"b 'unterminated")
	f.Fuzz(func(t *testing.T, s string) {
		total := 0
		for _, a := range SplitArgs(s) {
			total += len(a)
		}
		if total > len(s) {
			t.Fatalf("output bytes %d exceed input bytes %d", total, len(s))
		}
	})
}
