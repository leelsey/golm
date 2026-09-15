// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func TestWarnUnsupportedAttachments(t *testing.T) {
	var out bytes.Buffer
	warnUnsupportedAttachments(&out, "anthropic", golm.Capabilities{}, stringList{"a.png"}, stringList{"b.wav"})
	s := out.String()
	if !strings.Contains(s, "does not support image input") {
		t.Errorf("missing image warning: %q", s)
	}
	if !strings.Contains(s, "does not support audio input") {
		t.Errorf("missing audio warning: %q", s)
	}

	out.Reset()
	warnUnsupportedAttachments(&out, "google", golm.Capabilities{Images: true, Audio: true}, stringList{"a.png"}, stringList{"b.wav"})
	if out.Len() != 0 {
		t.Errorf("expected no warnings when supported, got %q", out.String())
	}
}
