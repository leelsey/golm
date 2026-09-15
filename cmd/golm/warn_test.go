// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWarnIgnoredConfigFlags(t *testing.T) {
	var out bytes.Buffer
	warnIgnoredConfigFlags(&out, "claude -p", "openai", "http://x", "KEY", "hermes")
	s := out.String()
	for _, want := range []string{"--cli", "--provider", "--base-url", "--api-key-env", "--tool-tags"} {
		if !strings.Contains(s, want) {
			t.Errorf("warning missing %s: %q", want, s)
		}
	}
	out.Reset()
	warnIgnoredConfigFlags(&out, "", "anthropic", "", "", "")
	if out.Len() != 0 {
		t.Errorf("expected no warning for defaults, got %q", out.String())
	}
}

func TestExtOfStripsParameters(t *testing.T) {
	if got := extOf("audio/L16;codec=pcm;rate=24000"); strings.ContainsAny(got, ";=") {
		t.Errorf("extOf returned a parameterised extension: %q", got)
	}
}

// --tool-tags is the one whose silence hurts most.
func TestWarnNamesToolTagsOnItsOwn(t *testing.T) {
	var out bytes.Buffer
	warnIgnoredConfigFlags(&out, "", "", "", "", "hermes")
	s := out.String()
	if !strings.Contains(s, "--tool-tags") {
		t.Errorf("warning did not name --tool-tags: %q", s)
	}
	if !strings.Contains(s, "tool_tags") {
		t.Errorf("warning did not say where to set it instead: %q", s)
	}
}
