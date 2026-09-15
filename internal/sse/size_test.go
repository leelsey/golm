// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package sse

import (
	"io"
	"strings"
	"testing"
)

type infiniteA struct{ read int }

func (r *infiniteA) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	r.read += len(p)
	return len(p), nil
}

func TestOversizedNoNewlineLineBounded(t *testing.T) {
	r := &infiniteA{}
	sc := NewScanner(io.MultiReader(strings.NewReader("data: "), r))
	if _, err := sc.Next(); err == nil {
		t.Fatal("expected error for an unbounded newline-less line")
	}
	if r.read > 4*maxEventBytes {
		t.Errorf("read %d bytes; should stop near the %d-byte cap", r.read, maxEventBytes)
	}
}

func TestEventSizeGuard(t *testing.T) {
	big := strings.Repeat("a", maxEventBytes+1)
	sc := NewScanner(strings.NewReader("data: " + big + "\n\n"))
	if _, err := sc.Next(); err == nil {
		t.Fatal("expected an error for an oversized event")
	}
}

func TestNormalEventUnaffected(t *testing.T) {
	sc := NewScanner(strings.NewReader("event: x\ndata: hello\n\n"))
	ev, err := sc.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != "x" || ev.Data != "hello" {
		t.Errorf("got %+v", ev)
	}
}
