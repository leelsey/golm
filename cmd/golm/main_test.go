// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	var out bytes.Buffer
	code := run([]string{"-version"}, strings.NewReader(""), &out, io.Discard)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "golm") {
		t.Errorf("version output = %q", out.String())
	}
}

func TestNoPrompt(t *testing.T) {
	code := run([]string{"-m", "x"}, strings.NewReader(""), io.Discard, io.Discard)
	if code != 2 {
		t.Errorf("exit = %d, want 2 (no prompt)", code)
	}
}

func TestAdHocRequiresModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GOLM_CONFIG", "")
	code := run([]string{"hello"}, strings.NewReader(""), io.Discard, io.Discard)
	if code != 1 {
		t.Errorf("exit = %d, want 1 (missing model)", code)
	}
}
