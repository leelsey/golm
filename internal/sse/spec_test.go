// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package sse

import (
	"strings"
	"testing"
)

func TestTypeOnlyFrameFiresNothing(t *testing.T) {
	sc := NewScanner(strings.NewReader("event: ping\n\ndata: real\n\n"))
	ev, err := sc.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev.Data != "real" {
		t.Errorf("got %+v, want the real data event (phantom type-only frame should not fire)", ev)
	}
}

func TestLoneCRLineTermination(t *testing.T) {
	sc := NewScanner(strings.NewReader("data: a\rdata: b\r\r"))
	ev, err := sc.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev.Data != "a\nb" {
		t.Errorf("CR-terminated data = %q, want %q", ev.Data, "a\nb")
	}
}
