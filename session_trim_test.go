// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import "testing"

func TestSessionTrim(t *testing.T) {
	s := NewSession()
	for i := 0; i < 5; i++ {
		s.Append(UserText("m"))
	}
	s.Trim(2)
	if got := len(s.History()); got != 2 {
		t.Errorf("Trim(2) len=%d, want 2", got)
	}
	s.Trim(10)
	if got := len(s.History()); got != 2 {
		t.Errorf("Trim(10) len=%d, want 2", got)
	}
	s.Trim(0)
	if got := len(s.History()); got != 0 {
		t.Errorf("Trim(0) len=%d, want 0", got)
	}
}

// TestTrimNoUserTurnLeavesHistory.
func TestTrimNoUserTurnLeavesHistory(t *testing.T) {
	s := NewSession()
	for i := 0; i < 3; i++ {
		s.Append(AssistantText("a"))
	}
	s.Trim(1)
	if got := len(s.History()); got != 3 {
		t.Errorf("Trim(1) with no user turn len=%d, want 3 (unchanged)", got)
	}
}

func TestSessionDropBefore(t *testing.T) {
	s := NewSession()
	for i := 0; i < 4; i++ {
		s.Append(AssistantText("x"))
	}
	s.DropBefore(0)
	if got := len(s.History()); got != 4 {
		t.Errorf("DropBefore(0) len=%d, want 4", got)
	}
	s.DropBefore(3)
	if got := len(s.History()); got != 1 {
		t.Errorf("DropBefore(3) len=%d, want 1", got)
	}
	s.DropBefore(99)
	if got := len(s.History()); got != 0 {
		t.Errorf("DropBefore(99) len=%d, want 0", got)
	}
}
