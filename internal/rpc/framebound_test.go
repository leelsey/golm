// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package rpc

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
)

type endlessReader struct {
	n   int64
	cap int64
}

func (e *endlessReader) Read(p []byte) (int, error) {
	if e.cap > 0 && e.n > e.cap {
		return 0, io.EOF
	}
	for i := range p {
		p[i] = 'x'
	}
	e.n += int64(len(p))
	return len(p), nil
}

// The framing is newline-delimited.
func TestAFrameWithNoEndIsRefusedRatherThanHeld(t *testing.T) {
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)

	tr := NewStreamTransport(&endlessReader{}, io.Discard, nil)
	_, err := tr.ReadMessage()
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("err = %v, want ErrMessageTooLarge", err)
	}

	runtime.ReadMemStats(&m1)

	if grew := m1.TotalAlloc - m0.TotalAlloc; grew > 4*DefaultMaxMessageBytes {
		t.Errorf("refusing a %d MiB frame allocated %d MiB", DefaultMaxMessageBytes>>20, grew>>20)
	}
}

// NewPipe builds the transport with a struct literal.
func TestPipeTransportIsBoundedToo(t *testing.T) {
	a, b := NewPipe()
	defer a.Close()
	defer b.Close()

	done := make(chan error, 1)
	go func() {
		_, err := a.ReadMessage()
		done <- err
	}()

	go func() { _ = b.WriteMessage(bytes.Repeat([]byte("x"), DefaultMaxMessageBytes+1)) }()
	if err := <-done; !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("the pipe transport answered %v, want ErrMessageTooLarge", err)
	}
}

// An ordinary message must be unaffected, including one comfortably larger than bufio's own buffer.
func TestOrdinaryAndLargeFramesStillRead(t *testing.T) {
	big := strings.Repeat("a", 1<<20)
	in := `{"jsonrpc":"2.0","id":1,"method":"m"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"params":"` + big + `"}` + "\n"

	tr := NewStreamTransport(strings.NewReader(in), io.Discard, nil)
	first, err := tr.ReadMessage()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if string(first) != `{"jsonrpc":"2.0","id":1,"method":"m"}` {
		t.Errorf("first = %q", first)
	}
	second, err := tr.ReadMessage()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if len(second) < 1<<20 {
		t.Errorf("a megabyte frame came back as %d bytes", len(second))
	}
	if _, err := tr.ReadMessage(); !errors.Is(err, io.EOF) {
		t.Errorf("after the last frame: %v, want io.EOF", err)
	}
}

// Blank keep-alive lines are skipped.
func TestKeepAlivesAndSplitFramesAreHandled(t *testing.T) {
	body := `{"a":"` + strings.Repeat("b", 9000) + `"}`
	tr := NewStreamTransport(strings.NewReader("\n\n"+body+"\n\n"), io.Discard, nil)
	got, err := tr.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got) != body {
		t.Errorf("frame was not reassembled: %d bytes, want %d", len(got), len(body))
	}
}

// A caller may tighten the cap.
func TestExplicitLimit(t *testing.T) {
	tr := NewStreamTransportWithLimit(strings.NewReader(strings.Repeat("x", 100)+"\n"), io.Discard, nil, 16)
	if _, err := tr.ReadMessage(); !errors.Is(err, ErrMessageTooLarge) {
		t.Errorf("a 100-byte frame passed a 16-byte cap: %v", err)
	}
	unbounded := NewStreamTransportWithLimit(&endlessReader{}, io.Discard, nil, 0)
	if _, err := unbounded.ReadMessage(); !errors.Is(err, ErrMessageTooLarge) {
		t.Errorf("a zero limit was taken as unbounded: %v", err)
	}
	negative := NewStreamTransportWithLimit(&endlessReader{}, io.Discard, nil, -1)
	if _, err := negative.ReadMessage(); !errors.Is(err, ErrMessageTooLarge) {
		t.Errorf("a negative limit was taken as unbounded: %v", err)
	}
}
