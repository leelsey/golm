// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package memory

import (
	"strings"
	"testing"
)

// Remember is the one path where content an agent chose.
func FuzzRememberStaysOneItem(f *testing.F) {
	f.Add("a plain note")
	f.Add("two\nlines")
	f.Add("carriage\rreturn")
	f.Add("- already a bullet")
	f.Add("\x1b[31mescape\x00nul\x07bell")
	f.Add("bidi\u202Eoverride")
	f.Add(strings.Repeat("x", 4096))
	f.Add("## a heading")

	f.Fuzz(func(t *testing.T, entry string) {
		s, err := Open(t.TempDir(), Options{})
		if err != nil {
			t.Skip()
		}
		if err := s.Remember(MemoryFile, entry); err != nil {
			return
		}
		sec := s.Section()
		var items int
		for _, line := range strings.Split(sec, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "- ") {
				items++
			}
		}
		if items != 1 {
			t.Fatalf("one Remember(%q) produced %d list items:\n%s", entry, items, sec)
		}
		for _, r := range sec {
			if r == '\n' {
				continue
			}
			if r < 0x20 || r == 0x7f {
				t.Fatalf("control character %q reached the prompt from %q", r, entry)
			}
			if (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
				t.Fatalf("bidi control %q reached the prompt from %q", r, entry)
			}
		}
	})
}
