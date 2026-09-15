// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package rpc

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// The framing faces a peer process.
func FuzzStreamFraming(f *testing.F) {
	f.Add("{\"jsonrpc\":\"2.0\",\"id\":1}\n")
	f.Add("\n\n\n")
	f.Add("no newline at all")
	f.Add("a\r\nb\r\n")
	f.Add("\r\n\r\n")
	f.Add(strings.Repeat("x", 9000) + "\n")
	f.Add("first\nsecond\n\nthird\n")
	f.Add("\x00\x01\x02\n")

	f.Fuzz(func(t *testing.T, in string) {
		tr := NewStreamTransportWithLimit(strings.NewReader(in), io.Discard, nil, 64<<10)
		total := 0
		for i := 0; ; i++ {
			if i > len(in)+8 {
				t.Fatalf("ReadMessage did not terminate over %q", in)
			}
			msg, err := tr.ReadMessage()
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, ErrMessageTooLarge) {
					t.Fatalf("unexpected error %v for %q", err, in)
				}
				break
			}
			if len(msg) == 0 {
				t.Fatalf("an empty frame was returned as a message from %q", in)
			}

			if bytes.ContainsAny(msg, "\n") {
				t.Fatalf("frame %q still contains the delimiter (from %q)", msg, in)
			}
			total += len(msg)
			if total > len(in)+8 {
				t.Fatalf("frames total %d bytes from a %d-byte input", total, len(in))
			}
		}
	})
}

// A frame written by this transport must be readable by it, unchanged, however odd the payload.
func FuzzWriteThenRead(f *testing.F) {
	f.Add("plain")
	f.Add("")
	f.Add("with\ttabs and \x00 nul")
	f.Add(strings.Repeat("y", 70000))
	f.Add("\r")

	f.Fuzz(func(t *testing.T, payload string) {
		if strings.Contains(payload, "\n") {
			t.Skip()
		}
		var buf bytes.Buffer
		w := NewStreamTransport(strings.NewReader(""), &buf, nil)
		if err := w.WriteMessage([]byte(payload)); err != nil {
			t.Fatalf("write: %v", err)
		}
		r := NewStreamTransport(bytes.NewReader(buf.Bytes()), io.Discard, nil)
		got, err := r.ReadMessage()
		if err != nil {
			if payload == "" || strings.TrimRight(payload, "\r") == "" {
				return
			}
			t.Fatalf("read back: %v (payload %q)", err, payload)
		}
		want := strings.TrimRight(payload, "\r")
		if string(got) != want {
			t.Fatalf("round trip changed the frame:\n  wrote %q\n  read  %q", payload, got)
		}
	})
}
