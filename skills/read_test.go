// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package skills_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm/skills"
)

// A single Read is allowed to return short.
func TestReadReturnsTheWholeBody(t *testing.T) {
	dir := t.TempDir()
	sk := filepath.Join(dir, "big")
	if err := os.MkdirAll(sk, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	body := strings.Repeat("instruction line that must survive the read\n", 4000)
	doc := "---\nname: big\ndescription: a large skill\n---\n\n" + body + "\nFINAL-MARKER\n"
	if err := os.WriteFile(filepath.Join(sk, skills.File), []byte(doc), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	set, err := skills.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := set.Read("big")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "FINAL-MARKER") {
		t.Fatalf("body ends %q — the read stopped short", tail(got, 60))
	}
	if n := strings.Count(got, "instruction line"); n != 4000 {
		t.Errorf("kept %d of 4000 lines", n)
	}
	if strings.Contains(got, "description:") {
		t.Error("front matter should be stripped; the index already said it")
	}
}

// The cap is a byte count, but the text is UTF-8.
func TestReadTruncatesOnARuneBoundary(t *testing.T) {
	dir := t.TempDir()
	sk := filepath.Join(dir, "wide")
	if err := os.MkdirAll(sk, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	doc := "---\nname: wide\n---\n" + strings.Repeat("한글", 200000)
	if err := os.WriteFile(filepath.Join(sk, skills.File), []byte(doc), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	set, err := skills.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := set.Read("wide")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(got, "[skill truncated]") {
		t.Fatal("an oversized body should be marked as cut")
	}
	for i, r := range got {
		if r == '�' {
			t.Fatalf("byte %d is a replacement character: the cut split a rune", i)
		}
	}
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
