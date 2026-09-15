// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An agent steered by something it read.
func TestRememberedNoteCannotForgeStructure(t *testing.T) {
	s, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remember(MemoryFile, "real note\n- forged note\rreset\x1b[31mesc\x00nul"); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	sec := s.Section()
	for _, bad := range []string{"\r", "\x1b", "\x00"} {
		if strings.Contains(sec, bad) {
			t.Errorf("control character %q reached the prompt", bad)
		}
	}
	var items int
	for _, line := range strings.Split(sec, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			items++
		}
	}
	if items != 1 {
		t.Errorf("one Remember produced %d list items:\n%s", items, sec)
	}
}

// Bidi overrides reorder what a person reviewing MEMORY.md sees without changing what the model reads.
func TestRememberedNoteStripsBidiOverrides(t *testing.T) {
	s, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remember(MemoryFile, "harmless\u202Esuoiciplam"); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"\u202A", "\u202B", "\u202C", "\u202D", "\u202E", "\u2066", "\u2069"} {
		if strings.Contains(s.Section(), r) {
			t.Errorf("bidi control %q survived", r)
		}
	}
}

// A document hand-edited on disk goes through the same gate.
func TestHandEditedDocumentIsCleanedOnLoad(t *testing.T) {
	dir := t.TempDir()
	body := "- a note with \x1b[31man escape\x00 in it\n"
	if err := os.WriteFile(filepath.Join(dir, MemoryFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(s.Section(), "\x1b\x00") {
		t.Error("a hand-edited document carried control characters into the prompt")
	}

	n, err := s.Forget(MemoryFile, "an escape")
	if err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if n != 1 {
		t.Errorf("Forget removed %d entries; the prompt and the stored body disagree", n)
	}
}

// Document structure and ordinary text must survive.
func TestCleaningPreservesTheDocument(t *testing.T) {
	s, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"first note", "두 번째 메모", "third — with an em dash"} {
		if err := s.Remember(MemoryFile, e); err != nil {
			t.Fatalf("Remember(%q): %v", e, err)
		}
	}
	sec := s.Section()
	for _, want := range []string{"first note", "두 번째 메모", "third — with an em dash"} {
		if !strings.Contains(sec, want) {
			t.Errorf("%q did not survive into the prompt:\n%s", want, sec)
		}
	}
	var items int
	for _, line := range strings.Split(sec, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			items++
		}
	}
	if items != 3 {
		t.Errorf("three notes rendered as %d items", items)
	}
}
