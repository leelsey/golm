// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tui

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// trunc bounds what a trace line shows.
func TestTruncCutsOnRuneBoundaries(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"", 5, ""},
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"elevenchars", 10, "elevenchar…"},
		{"한국어테스트문자열", 4, "한국어테…"},
		{"a한b국c", 3, "a한b…"},
		{"🧪🧪🧪🧪", 2, "🧪🧪…"},
	} {
		got := trunc(c.in, c.n)
		if got != c.want {
			t.Errorf("trunc(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("trunc(%q, %d) produced invalid UTF-8", c.in, c.n)
		}
	}
}

// A string whose byte length exceeds n but whose RUNE count does not must come back whole.
func TestTruncKeepsTextThatFitsInRunes(t *testing.T) {
	s := "한국어"
	if got := trunc(s, 5); got != s {
		t.Errorf("trunc(%q, 5) = %q, want it unchanged", s, got)
	}
	if strings.Contains(trunc(s, 5), "…") {
		t.Error("a string that fits was given an ellipsis")
	}
}

// indent is how nested delegation reads as nested.
func TestIndentSaturates(t *testing.T) {
	for _, c := range []struct{ depth, want int }{
		{-1, 0}, {0, 0}, {1, 2}, {2, 4}, {4, 8}, {5, 8}, {99, 8},
	} {
		if got := len(indent(c.depth)); got != c.want {
			t.Errorf("indent(%d) is %d columns, want %d", c.depth, got, c.want)
		}
	}
	if strings.TrimSpace(indent(3)) != "" {
		t.Error("indent produced something other than spaces")
	}
}

// Colour is only ever written to a terminal.
func TestPaintOnlyWhenAsked(t *testing.T) {
	if got := paint(false, "31", "text"); got != "text" {
		t.Errorf("paint(false) = %q, want the text unchanged", got)
	}
	got := paint(true, "31", "text")
	if !strings.HasPrefix(got, "\033[31m") || !strings.HasSuffix(got, "\033[0m") {
		t.Errorf("paint(true) = %q", got)
	}
	if strings.Contains(paint(false, "31", "text"), "\033") {
		t.Error("an escape sequence reached a non-terminal writer")
	}
}

// isTTY decides whether colour is written at all.
func TestIsTTY(t *testing.T) {
	if isTTY(&strings.Builder{}) {
		t.Error("a strings.Builder was taken for a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTTY(f) {
		t.Error("a regular file was taken for a terminal")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Skip("no pipes here")
	}
	defer r.Close()
	defer w.Close()
	if isTTY(w) {
		t.Error("a pipe was taken for a terminal")
	}
	if devNull, err := os.Open(os.DevNull); err == nil {
		defer devNull.Close()

		if !isTTY(devNull) {
			t.Log("note: /dev/null is not reported as a character device on this host")
		}
	}
}
