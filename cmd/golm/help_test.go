// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestHelp(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		var out bytes.Buffer
		code := run([]string{arg}, strings.NewReader(""), &out, io.Discard)
		if code != 0 {
			t.Errorf("%s: exit = %d, want 0", arg, code)
		}
		if !strings.Contains(out.String(), "Usage:") || !strings.Contains(out.String(), "golm tui") {
			t.Errorf("%s: help output missing usage/subcommands: %q", arg, out.String())
		}
	}
}
