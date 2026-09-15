// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"encoding/json"
	"sort"
	"testing"
	"time"
)

type stateProfile struct {
	Name  string `json:"name"`
	Score int    `json:"score"`
	Tags  []string
}

func roundTrip(t *testing.T, s *Session) *Session {
	t.Helper()
	b, err := json.Marshal(s.Snapshot())
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var d SessionData
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	return d.Session()
}

// The round trip used to yield float64 for every number.
func TestStateRoundTripKeepsTypes(t *testing.T) {
	s := NewSession()
	profile := stateProfile{Name: "leelsey", Score: 7, Tags: []string{"a", "b"}}
	when := time.Date(2026, 9, 11, 14, 23, 45, 123456789, time.UTC)
	for k, v := range map[string]any{"n": 1, "profile": profile, "when": when} {
		if err := s.SetState(k, v); err != nil {
			t.Fatalf("SetState(%s): %v", k, err)
		}
	}

	loaded := roundTrip(t, s)

	n, ok, err := GetState[int](loaded, "n")
	if err != nil || !ok {
		t.Fatalf("GetState[int](n) = %v, %v, %v", n, ok, err)
	}
	if n != 1 {
		t.Errorf("GetState[int](n) = %d, want exactly int(1)", n)
	}

	got, ok, err := GetState[stateProfile](loaded, "profile")
	if err != nil || !ok {
		t.Fatalf("GetState[stateProfile] = %+v, %v, %v", got, ok, err)
	}
	if got.Name != profile.Name || got.Score != profile.Score || len(got.Tags) != len(profile.Tags) {
		t.Errorf("GetState[stateProfile] = %+v, want %+v", got, profile)
	}

	ts, ok, err := GetState[time.Time](loaded, "when")
	if err != nil || !ok {
		t.Fatalf("GetState[time.Time] = %v, %v, %v", ts, ok, err)
	}
	if !ts.Equal(when) {
		t.Errorf("GetState[time.Time] = %v, want %v", ts, when)
	}
}

func TestGetStateMissingKey(t *testing.T) {
	s := NewSession()
	v, ok, err := GetState[int](s, "absent")
	if ok || err != nil || v != 0 {
		t.Fatalf("GetState on a missing key = %v, %v, %v; want 0, false, nil", v, ok, err)
	}
}

func TestGetStateWrongType(t *testing.T) {
	s := NewSession()
	if err := s.SetState("n", 1); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	_, ok, err := GetState[string](s, "n")
	if !ok {
		t.Error("GetState into the wrong type reported the key absent; it is present and undecodable")
	}
	if err == nil {
		t.Error("GetState into the wrong type returned no error")
	}
}

func TestGetStateRawMessageIsUndecoded(t *testing.T) {
	s := NewSession()
	profile := stateProfile{Name: "leelsey", Score: 7}
	if err := s.SetState("profile", profile); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	want, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw, ok, err := GetState[json.RawMessage](s, "profile")
	if err != nil || !ok {
		t.Fatalf("GetState[json.RawMessage] = %s, %v, %v", raw, ok, err)
	}
	if string(raw) != string(want) {
		t.Errorf("GetState[json.RawMessage] = %s, want the stored JSON %s", raw, want)
	}
}

func TestStateKeysAndDelete(t *testing.T) {
	s := NewSession()
	for _, k := range []string{"a", "b"} {
		if err := s.SetState(k, k); err != nil {
			t.Fatalf("SetState(%s): %v", k, err)
		}
	}
	keys := s.StateKeys()
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Fatalf("StateKeys = %v, want [a b]", keys)
	}

	s.DeleteState("a")
	if keys := s.StateKeys(); len(keys) != 1 || keys[0] != "b" {
		t.Fatalf("StateKeys after delete = %v, want [b]", keys)
	}
	if _, ok, _ := GetState[string](s, "a"); ok {
		t.Error("deleted key still present")
	}
	s.DeleteState("absent")
}

func TestSetStateUnmarshallableLeavesSessionUnchanged(t *testing.T) {
	s := NewSession()
	if err := s.SetState("keep", 1); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	before := sessionRev(s)

	if err := s.SetState("ch", make(chan int)); err == nil {
		t.Fatal("SetState of a channel returned no error")
	}
	if keys := s.StateKeys(); len(keys) != 1 || keys[0] != "keep" {
		t.Errorf("StateKeys = %v, want only [keep]", keys)
	}
	if v, ok, err := GetState[int](s, "keep"); !ok || err != nil || v != 1 {
		t.Errorf("GetState(keep) = %v, %v, %v; want 1, true, nil", v, ok, err)
	}
	if got := sessionRev(s); got != before {
		t.Errorf("revision moved to %d from %d on a failed SetState", got, before)
	}
}
