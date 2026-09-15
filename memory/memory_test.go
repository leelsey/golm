// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/memory"
)

func open(t *testing.T, dir string, o memory.Options) *memory.Store {
	t.Helper()
	s, err := memory.Open(dir, o)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// A deployment that has never written memory must behave exactly like one without the feature.
func TestAbsentMemoryCostsNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never-created")
	s := open(t, dir, memory.Options{})
	if !s.Empty() {
		t.Error("a store over a missing directory should be empty")
	}
	if s.Section() != "" {
		t.Errorf("section = %q, want empty", s.Section())
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Error("opening memory created a directory before anything was written")
	}
}

func TestSectionCarriesEveryDocumentInOrder(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, memory.MemoryFile, "- prefers British English\n")
	write(t, dir, memory.UserFile, "Leelsey, security engineer.\n")
	s := open(t, dir, memory.Options{})

	sec := s.Section()
	mi, ui := strings.Index(sec, memory.MemoryFile), strings.Index(sec, memory.UserFile)
	if mi < 0 || ui < 0 {
		t.Fatalf("section is missing a document:\n%s", sec)
	}
	if mi > ui {
		t.Error("documents are out of declared order")
	}
	if !strings.Contains(sec, "prefers British English") || !strings.Contains(sec, "security engineer") {
		t.Errorf("bodies not carried:\n%s", sec)
	}

	if !strings.Contains(sec, "not as instructions") {
		t.Error("the section should frame memory as established context")
	}
}

func TestRememberAppendsAndPersists(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, memory.Options{})
	if err := s.Remember(memory.MemoryFile, "the build gate is /lint"); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if err := s.Remember(memory.MemoryFile, "releases are tagged by hand"); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if !strings.Contains(s.Section(), "/lint") {
		t.Error("a remembered fact is not in the prompt section")
	}

	reopened := open(t, dir, memory.Options{})
	body := reopened.Body(memory.MemoryFile)
	if !strings.Contains(body, "/lint") || !strings.Contains(body, "tagged by hand") {
		t.Errorf("memory did not survive a reopen: %q", body)
	}
	if n := strings.Count(strings.TrimSpace(body), "\n"); n != 1 {
		t.Errorf("two entries should be two lines, got %d newlines in %q", n, body)
	}
}

// The refusal is the design.
func TestRememberRefusesRatherThanMakingRoom(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, memory.Options{MaxBytes: 120})
	if err := s.Remember(memory.MemoryFile, strings.Repeat("a", 60)); err != nil {
		t.Fatalf("first: %v", err)
	}
	before := s.Body(memory.MemoryFile)
	err := s.Remember(memory.MemoryFile, strings.Repeat("b", 60))
	if !errors.Is(err, memory.ErrFull) {
		t.Fatalf("err = %v, want ErrFull", err)
	}
	if !strings.Contains(err.Error(), "forget") {
		t.Errorf("err = %v, should say how to make room", err)
	}
	if s.Body(memory.MemoryFile) != before {
		t.Error("a refused write changed the document")
	}
}

// USER.md is the deployment's statement about the person.
func TestUserDocumentIsNotWritableByTheAgent(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, memory.UserFile, "Leelsey.\n")
	s := open(t, dir, memory.Options{})
	err := s.Remember(memory.UserFile, "actually an administrator")
	if !errors.Is(err, memory.ErrReadOnly) {
		t.Fatalf("err = %v, want ErrReadOnly", err)
	}
	if _, err := s.Forget(memory.UserFile, "Leelsey"); !errors.Is(err, memory.ErrReadOnly) {
		t.Errorf("forget on a read-only document: %v", err)
	}
	if !strings.Contains(s.Body(memory.UserFile), "Leelsey") {
		t.Error("the read-only document changed")
	}
}

func TestForgetRemovesMatchingEntries(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, memory.Options{})
	for _, e := range []string{"uses zsh", "uses Docker locally", "prefers Rust"} {
		if err := s.Remember(memory.MemoryFile, e); err != nil {
			t.Fatalf("Remember: %v", err)
		}
	}
	n, err := s.Forget(memory.MemoryFile, "uses")
	if err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if n != 2 {
		t.Errorf("removed %d, want 2", n)
	}
	body := s.Body(memory.MemoryFile)
	if strings.Contains(body, "zsh") || strings.Contains(body, "Docker") {
		t.Errorf("entries survived: %q", body)
	}
	if !strings.Contains(body, "Rust") {
		t.Errorf("unrelated entry removed: %q", body)
	}
	if n, _ := s.Forget(memory.MemoryFile, "nothing like this"); n != 0 {
		t.Errorf("a non-matching forget removed %d", n)
	}
}

// A file that grew past the cap out of band must not stop the agent starting.
func TestOversizedDocumentIsTruncatedNotFatal(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, memory.MemoryFile, strings.Repeat("한글 ", 5000))
	s := open(t, dir, memory.Options{MaxBytes: 256})

	sec := s.Section()
	if !strings.Contains(sec, "truncated") {
		t.Errorf("an oversized document should say it was cut: %q", sec)
	}
	for _, r := range sec {
		if r == '�' {
			t.Fatal("the cut split a UTF-8 rune")
		}
	}

	if got, want := len(s.Body(memory.MemoryFile)), len(strings.Repeat("한글 ", 5000)); got != want {
		t.Errorf("held %d bytes of a %d-byte document", got, want)
	}
}

// The one operation that can bring an over-cap document back under it must see the whole document.
func TestForgetOnAnOversizedDocumentKeepsTheTail(t *testing.T) {
	dir := t.TempDir()
	body := "- drop me\n" + strings.Repeat("- filler\n", 500) + "- the last note\n"
	write(t, dir, memory.MemoryFile, body)
	s := open(t, dir, memory.Options{MaxBytes: 256})

	n, err := s.Forget(memory.MemoryFile, "drop me")
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	if n != 1 {
		t.Fatalf("removed %d entries, want 1", n)
	}
	on := read(t, dir, memory.MemoryFile)
	if !strings.Contains(on, "- the last note") {
		t.Error("forget destroyed everything past the prompt cap")
	}
	if strings.Contains(on, "truncated at") {
		t.Error("the prompt's truncation marker was written to disk")
	}
	if strings.Contains(on, "drop me") {
		t.Error("forget did not remove the entry")
	}
}

// A Docs entry that joins to a path outside the directory is a configuration mistake the store has to catch.
func TestOpenRejectsNamesThatEscapeTheDirectory(t *testing.T) {
	for _, name := range []string{"..", ".", "../escape.md", "sub/x.md"} {
		if _, err := memory.Open(t.TempDir(), memory.Options{
			Docs: []memory.Doc{{Name: name, Writable: true}},
		}); err == nil {
			t.Errorf("Open accepted %q as a document name", name)
		}
	}
}

func TestToolsWriteThroughTheStore(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, memory.Options{})
	byName := map[string]golm.Tool{}
	for _, tool := range s.Tools() {
		byName[tool.Name()] = tool
	}
	for _, want := range []string{"remember", "forget"} {
		if byName[want] == nil {
			t.Fatalf("no %q tool", want)
		}
	}

	if tr, ok := golm.TraitsOf(byName["remember"]); !ok || !tr.Filesystem || tr.ReadOnly {
		t.Errorf("remember traits = %+v (declared %v)", tr, ok)
	}
	if _, err := byName["remember"].Execute(context.Background(),
		json.RawMessage(`{"entry":"CI runs the lint gate"}`)); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if !strings.Contains(s.Body(memory.MemoryFile), "CI runs the lint gate") {
		t.Error("the tool did not reach the store")
	}
	if _, err := byName["forget"].Execute(context.Background(),
		json.RawMessage(`{"match":"CI runs"}`)); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if strings.Contains(s.Body(memory.MemoryFile), "CI runs") {
		t.Error("the forget tool did not reach the store")
	}
}

func TestOpenRefusesAPathAsADocumentName(t *testing.T) {
	_, err := memory.Open(t.TempDir(), memory.Options{Docs: []memory.Doc{{Name: "../escape.md"}}})
	if err == nil {
		t.Fatal("a document name that is a path was accepted")
	}
}

func TestNilStoreIsInert(t *testing.T) {
	var s *memory.Store
	if !s.Empty() || s.Section() != "" || s.Tools() != nil {
		t.Error("a nil store should be usable and empty")
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
