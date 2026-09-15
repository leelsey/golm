// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestCLIBackendFlag(t *testing.T) {
	var out bytes.Buffer
	code := run([]string{"--cli", "cat", "ping-xyz"}, strings.NewReader(""), &out, io.Discard)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "ping-xyz") {
		t.Errorf("cli backend did not echo prompt: %q", out.String())
	}
}
