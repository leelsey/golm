// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtOfRejectsUnsafe(t *testing.T) {
	cases := map[string]string{
		"image/png":              ".png",
		"audio/L16;rate=24000":   ".L16",
		"image/../../etc/passwd": ".bin",
		"x/with space":           ".bin",
		"noslash":                ".bin",
	}
	for mt, want := range cases {
		if got := extOf(mt); got != want {
			t.Errorf("extOf(%q) = %q, want %q", mt, got, want)
		}
	}
}

func TestWriteMediaFileNoTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeMediaFile(io.Discard, "image", 1, "image/../../pwned", []byte{1, 2, 3})

	if _, err := os.Stat(filepath.Join(dir, "..", "pwned")); err == nil {
		t.Fatal("traversal: file created outside working directory")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "golm-image-1") {
			return
		}
	}
	t.Errorf("expected a safe golm-image-1.* file in cwd, got %v", entries)
}
