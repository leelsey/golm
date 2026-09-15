// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package memory_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/leelsey/golm/memory"
)

// An agent with ParallelTools runs a turn's calls at once.
func TestConcurrentRememberAndForget(t *testing.T) {
	dir := t.TempDir()
	s, err := memory.Open(dir, memory.Options{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.Remember(memory.MemoryFile, "fact number "+string(rune('a'+i))); err != nil {
				t.Errorf("remember: %v", err)
			}
		}(i)
	}
	wg.Wait()

	body := s.Body(memory.MemoryFile)
	for i := 0; i < 16; i++ {
		if !strings.Contains(body, "fact number "+string(rune('a'+i))) {
			t.Errorf("fact %c was lost to a concurrent write", 'a'+i)
		}
	}

	b, err := os.ReadFile(filepath.Join(dir, memory.MemoryFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != body {
		t.Errorf("the file and the loaded body differ:\n file: %q\n body: %q", b, body)
	}
	for _, e := range must(os.ReadDir(dir)) {
		if strings.HasPrefix(e.Name(), ".golm-") {
			t.Errorf("a temporary file survived: %s", e.Name())
		}
	}
}

// Remember refuses rather than making room.
func TestRefusedRememberIsAtomic(t *testing.T) {
	dir := t.TempDir()
	s, err := memory.Open(dir, memory.Options{MaxBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remember(memory.MemoryFile, strings.Repeat("a", 100)); err != nil {
		t.Fatal(err)
	}
	before := s.Body(memory.MemoryFile)
	for i := 0; i < 5; i++ {
		if err := s.Remember(memory.MemoryFile, strings.Repeat("b", 100)); err == nil {
			t.Fatal("an oversized entry was accepted")
		}
	}
	if got := s.Body(memory.MemoryFile); got != before {
		t.Errorf("a refused write changed the document:\n before: %q\n after:  %q", before, got)
	}

	b, err := os.ReadFile(filepath.Join(dir, memory.MemoryFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != before {
		t.Errorf("the file diverged from the document: %q", b)
	}
}

// A document name that escapes the directory must be refused at Open, not at the first write.
func TestDocumentNamesCannotEscape(t *testing.T) {
	for _, name := range []string{"../escape.md", "a/b.md", "/etc/passwd", ""} {
		if _, err := memory.Open(t.TempDir(), memory.Options{
			Docs: []memory.Doc{{Name: name, Writable: true}},
		}); err == nil {
			t.Errorf("document name %q was accepted", name)
		}
	}
}

// Forget removes every matching entry and nothing else.
func TestForgetIsPrecise(t *testing.T) {
	dir := t.TempDir()
	s, err := memory.Open(dir, memory.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"keep this", "drop this", "keep that", "drop that"} {
		if err := s.Remember(memory.MemoryFile, e); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Forget(memory.MemoryFile, "   "); err == nil {
		t.Error("an empty match was accepted; it would clear the document")
	}
	n, err := s.Forget(memory.MemoryFile, "drop")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("removed %d, want 2", n)
	}
	body := s.Body(memory.MemoryFile)
	if strings.Contains(body, "drop") {
		t.Errorf("an entry survived: %q", body)
	}
	if !strings.Contains(body, "keep this") || !strings.Contains(body, "keep that") {
		t.Errorf("an unrelated entry was removed: %q", body)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
