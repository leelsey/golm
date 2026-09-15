// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package clibackend

import "testing"

func TestCompleteUTF8Prefix(t *testing.T) {
	full := []byte("héllo")

	if got := completeUTF8Prefix(full[:2]); got != 1 {
		t.Errorf("incomplete-rune prefix = %d, want 1", got)
	}
	if got := completeUTF8Prefix(full); got != len(full) {
		t.Errorf("complete prefix = %d, want %d", got, len(full))
	}
	if got := completeUTF8Prefix([]byte("abc")); got != 3 {
		t.Errorf("ascii prefix = %d, want 3", got)
	}

	var out, pending []byte
	for _, ch := range [][]byte{full[:2], full[2:]} {
		pending = append(pending, ch...)
		k := completeUTF8Prefix(pending)
		out = append(out, pending[:k]...)
		pending = pending[k:]
	}
	out = append(out, pending...)
	if string(out) != "héllo" {
		t.Errorf("reassembled = %q, want héllo", string(out))
	}
}
