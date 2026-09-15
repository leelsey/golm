// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestReadLinesRejectsOverlongLine(t *testing.T) {
	in := strings.NewReader(strings.Repeat("a", maxLineBytes+2))
	lines, errc := readLines(context.Background(), in)
	for range lines {
	}
	if err := <-errc; err == nil || err == io.EOF {
		t.Fatalf("err = %v, want an overlong-line error", err)
	}
}

func TestReadLinesDeliversNormalLine(t *testing.T) {
	lines, errc := readLines(context.Background(), strings.NewReader("hello\n"))
	got, ok := <-lines
	if !ok || got != "hello\n" {
		t.Fatalf("line = %q ok=%v, want %q", got, ok, "hello\n")
	}
	if _, ok := <-lines; ok {
		t.Fatal("unexpected extra line")
	}
	if err := <-errc; err != io.EOF {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}
