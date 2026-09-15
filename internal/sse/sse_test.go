// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package sse

import (
	"io"
	"strings"
	"testing"
)

func TestScannerNamedEvents(t *testing.T) {
	stream := "event: message_start\ndata: {\"a\":1}\n\n" +
		"event: delta\ndata: line1\ndata: line2\n\n" +
		": this is a comment\n" +
		"data: no-event\n\n"
	s := NewScanner(strings.NewReader(stream))

	want := []Event{
		{Type: "message_start", Data: `{"a":1}`},
		{Type: "delta", Data: "line1\nline2"},
		{Type: "", Data: "no-event"},
	}
	for i, w := range want {
		ev, err := s.Next()
		if err != nil {
			t.Fatalf("event %d: unexpected err %v", i, err)
		}
		if ev != w {
			t.Errorf("event %d = %+v, want %+v", i, ev, w)
		}
	}
	if _, err := s.Next(); err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestLeadingBOMStripped(t *testing.T) {
	s := NewScanner(strings.NewReader("\xEF\xBB\xBFdata: hello\n\n"))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ev.Data != "hello" {
		t.Errorf("data = %q, want %q (BOM not stripped)", ev.Data, "hello")
	}
}

func TestScannerParsesIDAndRetry(t *testing.T) {
	s := NewScanner(strings.NewReader("id: 42\nretry: 3000\ndata: hi\n\n"))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ev.ID != "42" || ev.Retry != "3000" || ev.Data != "hi" {
		t.Errorf("got %+v, want ID=42 Retry=3000 Data=hi", ev)
	}
}

func TestScannerNoTrailingBlankLine(t *testing.T) {
	s := NewScanner(strings.NewReader("data: last"))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ev.Data != "last" {
		t.Errorf("data = %q, want %q", ev.Data, "last")
	}
	if _, err := s.Next(); err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}
