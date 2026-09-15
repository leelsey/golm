// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package sse

import (
	"bytes"
	"testing"
)

func FuzzScanner(f *testing.F) {
	f.Add([]byte("data: hi\n\n"))
	f.Add([]byte("\xEF\xBB\xBFevent: x\nid: 1\nretry: 2\ndata: a\ndata: b\r\n\r\n"))
	f.Add([]byte(": comment\r\ndata:no-space\rdata\n\n"))
	f.Add([]byte("id: 4\x002\ndata: nul-id\n\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		s := NewScanner(bytes.NewReader(data))
		for i := 0; i <= len(data); i++ {
			if _, err := s.Next(); err != nil {
				return
			}
		}
		t.Fatalf("scanner yielded more events than input bytes (%d)", len(data))
	})
}
