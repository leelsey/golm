// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func skillDir(t *testing.T, frontMatter string) string {
	t.Helper()
	root := t.TempDir()
	d := filepath.Join(root, "probe")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, File), []byte(frontMatter), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// The index is in the system prompt of every turn.
func TestControlCharactersNeverReachTheIndex(t *testing.T) {
	root := skillDir(t, "---\nname: ev\x1b[31mil\rone\x00two\ndescription: d\x07esc\n---\nbody")
	set, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	idx := set.Index()
	for _, bad := range []string{"\x1b", "\x00", "\r", "\x07"} {
		if strings.Contains(idx, bad) {
			t.Errorf("control character %q reached the prompt index", bad)
		}
	}

	var entries int
	for _, line := range strings.Split(idx, "\n") {
		if strings.HasPrefix(line, "- ") {
			entries++
		}
	}
	if entries != 1 {
		t.Errorf("one skill rendered as %d entries:\n%s", entries, idx)
	}
}

// Bidi overrides reorder what a REVIEWER sees without changing what the model reads.
func TestBidiOverridesAreStripped(t *testing.T) {
	root := skillDir(t, "---\nname: safe\u202Eelif.exe\ndescription: d\n---\n")
	set, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, r := range []string{"\u202A", "\u202B", "\u202C", "\u202D", "\u202E", "\u2066", "\u2069"} {
		if strings.Contains(set.Index(), r) {
			t.Errorf("bidi control %q survived into the index", r)
		}
	}
}

// One skill must not be able to occupy the prompt of every turn.
func TestFrontMatterFieldsAreBounded(t *testing.T) {
	root := skillDir(t, "---\nname: "+strings.Repeat("A", 50_000)+
		"\ndescription: "+strings.Repeat("B", 50_000)+"\n---\n")
	set, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk := set.List()[0]
	if len(sk.Name) > maxFieldBytes {
		t.Errorf("name is %d bytes, over the %d bound", len(sk.Name), maxFieldBytes)
	}
	if len(sk.Description) > maxFieldBytes {
		t.Errorf("description is %d bytes, over the %d bound", len(sk.Description), maxFieldBytes)
	}
	if len(set.Index()) > 4*maxFieldBytes {
		t.Errorf("one skill produced a %d-byte index", len(set.Index()))
	}
}

// A name that cleans away to nothing falls back to the directory name.
func TestANameThatCleansToNothingFallsBack(t *testing.T) {
	root := skillDir(t, "---\nname: \x00\x01\x02\ndescription: still here\n---\n")
	set, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk := set.List()[0]
	if sk.Name != "probe" {
		t.Errorf("name = %q, want the directory name", sk.Name)
	}
	if sk.Description != "still here" {
		t.Errorf("description = %q", sk.Description)
	}
}

// A tab is spacing, not corruption.
func TestTabsBecomeSpaces(t *testing.T) {
	root := skillDir(t, "---\nname: n\ndescription: two\twords\n---\n")
	set, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := set.List()[0].Description; got != "two words" {
		t.Errorf("description = %q, want the tab flattened to a space", got)
	}
}

// Ordinary non-ASCII names are not corruption and must survive intact.
func TestUnicodeNamesSurvive(t *testing.T) {
	root := skillDir(t, "---\nname: 한국어-스킬\ndescription: 설명입니다\n---\n")
	set, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk := set.List()[0]
	if sk.Name != "한국어-스킬" || sk.Description != "설명입니다" {
		t.Errorf("unicode was mangled: %q / %q", sk.Name, sk.Description)
	}
}
