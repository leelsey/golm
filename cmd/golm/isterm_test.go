// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"strings"
	"testing"
)

func TestIsTerminalNonTTY(t *testing.T) {
	if isTerminal(strings.NewReader("x")) {
		t.Error("strings.Reader treated as terminal")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(r) {
		t.Error("os.Pipe read end treated as terminal")
	}
}
