// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/sessionstore"
)

func TestTurnsArePersisted(t *testing.T) {
	store := sessionstore.NewMemory()
	sess := golm.SessionData{ID: "kept"}.Session()
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	var out bytes.Buffer

	in := strings.NewReader("one\ntwo\n/exit\n")
	if err := Run(context.Background(), Options{Agent: agent, In: in, Out: &out, Store: store, Session: sess}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "session kept") {
		t.Errorf("output %q should name the session being saved", out.String())
	}
	loaded, err := store.Load(context.Background(), "kept")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.Len() != 4 {
		t.Fatalf("stored history = %d, want 4: %v", loaded.Len(), loaded.History())
	}
}

func TestResetStartsANewStoredSession(t *testing.T) {
	store := sessionstore.NewMemory()
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	var out bytes.Buffer

	in := strings.NewReader("one\n/reset\ntwo\n/exit\n")
	if err := Run(context.Background(), Options{Agent: agent, In: in, Out: &out,
		Store: store, Session: golm.SessionData{ID: "first"}.Session()}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	first, err := store.Load(context.Background(), "first")
	if err != nil {
		t.Fatalf("load first: %v", err)
	}
	if first.Len() != 2 {
		t.Errorf("first session = %d messages, want the exchange before the reset", first.Len())
	}
	metas, err := store.List(context.Background(), golm.SessionQuery{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(metas) != 2 {
		t.Errorf("stored sessions = %d, want 2 (before and after the reset)", len(metas))
	}
}

func TestTitleCommand(t *testing.T) {
	store := sessionstore.NewMemory()
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	var out bytes.Buffer

	in := strings.NewReader("one\n/title weekly review\n/session\n/exit\n")
	if err := Run(context.Background(), Options{Agent: agent, In: in, Out: &out,
		Store: store, Session: golm.SessionData{ID: "titled"}.Session()}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "title weekly review") {
		t.Errorf("/session did not report the title: %q", out.String())
	}
	loaded, _ := store.Load(context.Background(), "titled")
	if loaded.Title() != "weekly review" {
		t.Errorf("stored title = %q, want %q", loaded.Title(), "weekly review")
	}
}

func TestTitleCommandNeedsText(t *testing.T) {
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	var out bytes.Buffer
	in := strings.NewReader("/title\n/exit\n")
	if err := Run(context.Background(), Options{Agent: agent, In: in, Out: &out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "usage: /title") {
		t.Errorf("output %q should explain the command", out.String())
	}
}

func TestSessionCommandSaysWhenNothingIsPersisted(t *testing.T) {
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	var out bytes.Buffer
	in := strings.NewReader("/session\n/exit\n")
	if err := Run(context.Background(), Options{Agent: agent, In: in, Out: &out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "not persisted") {
		t.Errorf("output %q should say the conversation is not being kept", out.String())
	}
}

func TestCompactCommand(t *testing.T) {
	store := sessionstore.NewMemory()
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	var out bytes.Buffer

	in := strings.NewReader("a\nb\nc\nd\ne\nf\ng\n/compact\n/session\n/exit\n")
	if err := Run(context.Background(), Options{Agent: agent, In: in, Out: &out,
		Store: store, Session: golm.SessionData{ID: "long"}.Session()}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "compacted:") {
		t.Fatalf("output %q should report the compaction", s)
	}
	if !strings.Contains(s, "archived as") {
		t.Errorf("output %q should name the archive", s)
	}

	live, err := store.Load(context.Background(), "long")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if live.Parent() == "" {
		t.Error("the compacted session should record its archive as Parent")
	}
	if live.Len() >= 14 {
		t.Errorf("live history = %d, want it shorter than the 14 messages before compaction", live.Len())
	}
	if _, err := store.Load(context.Background(), live.Parent()); err != nil {
		t.Errorf("archive %s not in the store: %v", live.Parent(), err)
	}
	chain, err := golm.Lineage(context.Background(), store, "long")
	if err != nil {
		t.Fatalf("lineage: %v", err)
	}
	if len(chain) != 2 {
		t.Errorf("lineage = %d sessions, want the live one and its archive", len(chain))
	}
}

func TestCompactWithNothingToCompact(t *testing.T) {
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	var out bytes.Buffer
	in := strings.NewReader("/compact\n/exit\n")
	if err := Run(context.Background(), Options{Agent: agent, In: in, Out: &out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to compact") {
		t.Errorf("output %q should say there was nothing to do", out.String())
	}
}

func TestCompactUsesTheAgentsPolicy(t *testing.T) {
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x",
		Compaction: &golm.CompactPolicy{
			CompactConfig: golm.CompactConfig{KeepLast: 2},
			AtMessages:    1000,
		}}
	store := sessionstore.NewMemory()
	var out bytes.Buffer
	in := strings.NewReader("a\nb\nc\nd\n/compact\n/exit\n")
	if err := Run(context.Background(), Options{Agent: agent, In: in, Out: &out,
		Store: store, Session: golm.SessionData{ID: "policy"}.Session()}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "compacted:") {
		t.Fatalf("output %q should report the compaction", out.String())
	}
	live, _ := store.Load(context.Background(), "policy")

	if live.Len() > 4 {
		t.Errorf("live history = %d, want the policy's keep_last of 2 plus the summary", live.Len())
	}
}

func TestSaveFailureDoesNotEndTheSession(t *testing.T) {
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	var out bytes.Buffer
	in := strings.NewReader("one\ntwo\n/exit\n")

	if err := Run(context.Background(), Options{Agent: agent, In: in, Out: &out,
		Store: failingStore{}, Session: golm.NewSession()}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "not saved") {
		t.Errorf("output %q should report the failed save", s)
	}
	if !strings.Contains(s, "echo:two") {
		t.Errorf("output %q should still contain the second answer", s)
	}
}

type failingStore struct{ golm.SessionStore }

func (failingStore) Save(context.Context, *golm.Session) error { return errFull }
func (failingStore) Load(context.Context, string) (*golm.Session, error) {
	return nil, golm.ErrSessionNotFound
}

var errFull = errStr("no space left on device")

type errStr string

func (e errStr) Error() string { return string(e) }
