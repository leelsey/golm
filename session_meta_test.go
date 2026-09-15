// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"testing"
	"time"
)

func sessionRev(s *Session) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rev
}

func TestSessionIDsAreUniqueAndStable(t *testing.T) {
	seen := make(map[string]bool, 128)
	for i := 0; i < 128; i++ {
		s := NewSession()
		id := s.ID()
		if id == "" {
			t.Fatal("NewSession minted an empty id")
		}
		if seen[id] {
			t.Fatalf("duplicate session id %q", id)
		}
		seen[id] = true
		if s.ID() != id || s.Meta().ID != id || s.Snapshot().ID != id {
			t.Fatalf("id %q is not stable across ID/Meta/Snapshot", id)
		}
	}
}

func TestZeroValueSessionMintsID(t *testing.T) {
	var s Session
	id := s.ID()
	if id == "" {
		t.Fatal("zero-value Session returned an empty id")
	}
	if got := s.ID(); got != id {
		t.Fatalf("second ID = %q, want the minted %q", got, id)
	}

	s.Append(UserText("hi"))
	s.SetTitle("zero")
	if m := s.Meta(); m.ID != id || m.Title != "zero" || m.Messages != 1 {
		t.Fatalf("Meta = %+v, want id %q, title \"zero\", 1 message", m, id)
	}
	d := s.Snapshot()
	if d.ID != id || len(d.History) != 1 {
		t.Fatalf("Snapshot = %+v, want id %q and the appended turn", d, id)
	}
	if d.Created.IsZero() {
		t.Error("Snapshot.Created is zero; minting an id sets a creation time")
	}

	var fresh Session
	if fresh.Meta().ID == "" {
		t.Error("Meta on a zero-value Session returned an empty id")
	}
}

func TestMetaCountsMessages(t *testing.T) {
	s := NewSession()
	for i := 0; i < 4; i++ {
		s.Append(mediaMessage())
	}
	m := s.Meta()
	if m.Messages != 4 || s.Len() != 4 || len(s.History()) != 4 {
		t.Fatalf("Meta.Messages = %d, Len = %d, History = %d; want 4 each", m.Messages, s.Len(), len(s.History()))
	}
	if m.Parent != "" {
		t.Errorf("Meta.Parent = %q on a session never compacted", m.Parent)
	}

	s.Append(UserText("more"))
	if got := s.Meta().Messages; got != 5 {
		t.Errorf("Meta.Messages = %d after a further append, want 5", got)
	}
}

func TestSetTitleMovesUpdated(t *testing.T) {
	s := NewSession()
	before := s.Meta()
	time.Sleep(time.Millisecond)

	s.SetTitle("release notes")
	after := s.Meta()
	if after.Title != "release notes" || s.Title() != "release notes" {
		t.Fatalf("Title = %q, want \"release notes\"", after.Title)
	}
	if !after.Updated.After(before.Updated) {
		t.Errorf("Updated = %v, want later than %v", after.Updated, before.Updated)
	}
	if !after.Created.Equal(before.Created) {
		t.Errorf("Created moved to %v from %v", after.Created, before.Created)
	}
}

func TestRevisionBumpsOnlyOnDrops(t *testing.T) {
	s := NewSession()
	for i := 0; i < 3; i++ {
		s.Append(UserText("q"), AssistantText("a"))
	}
	base := sessionRev(s)

	s.Append(UserText("q"), AssistantText("a"))
	if got := sessionRev(s); got != base {
		t.Errorf("revision moved to %d from %d on Append; appending rewrites nothing", got, base)
	}

	s.Trim(100)
	if got := sessionRev(s); got != base {
		t.Errorf("revision moved to %d from %d on a Trim that dropped nothing", got, base)
	}

	s.Trim(2)
	trimmed := sessionRev(s)
	if trimmed <= base {
		t.Fatalf("revision = %d after Trim, want above %d", trimmed, base)
	}

	s.DropBefore(1)
	if got := sessionRev(s); got <= trimmed {
		t.Errorf("revision = %d after DropBefore, want above %d", got, trimmed)
	}
}
