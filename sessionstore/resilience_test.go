// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/sessionstore"
)

// One truncated file — a crash mid-write, a half-copied directory.
func TestListSkipsDamageAndReportsIt(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	ctx := context.Background()

	for _, id := range []string{"AAAAAA", "BBBBBB"} {
		s := golm.NewSession()
		s.Append(golm.UserText("hello " + id))
		if err := st.Save(ctx, sessionFor(id, s)); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "CCCCCC.json"), []byte(`{"id":"CCCC`), 0o600); err != nil {
		t.Fatalf("write damage: %v", err)
	}

	var skipped []string
	st.OnError = func(id string, err error) { skipped = append(skipped, id) }

	metas, err := st.List(ctx, golm.SessionQuery{})
	if err != nil {
		t.Fatalf("List: %v — one damaged file must not take the store down", err)
	}
	if len(metas) != 2 {
		t.Errorf("listed %d sessions, want the 2 intact ones", len(metas))
	}
	if len(skipped) != 1 || skipped[0] != "CCCCCC" {
		t.Errorf("skipped = %v, want the damaged file reported", skipped)
	}

	hits, err := st.Search(ctx, golm.SessionQuery{Text: "hello"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Errorf("searched up %d sessions, want 2", len(hits))
	}
}

// A scan of a large store is thousands of reads.
func TestScansHonourACancelledContext(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	for _, id := range []string{"AAAAAA", "BBBBBB", "CCCCCC"} {
		s := golm.NewSession()
		s.Append(golm.UserText("x"))
		if err := st.Save(context.Background(), sessionFor(id, s)); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.List(ctx, golm.SessionQuery{}); err == nil {
		t.Error("List ignored a cancelled context")
	}
	if _, err := st.Search(ctx, golm.SessionQuery{Text: "x"}); err == nil {
		t.Error("Search ignored a cancelled context")
	}
}

// A file whose stored id is not the one it is filed under means the store was rearranged, not.
func TestIDMismatchIsNotTreatedAsDamage(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	s := golm.NewSession()
	s.Append(golm.UserText("x"))
	if err := st.Save(context.Background(), sessionFor("GOODID", s)); err != nil {
		t.Fatalf("save: %v", err)
	}
	p := filepath.Join(dir, "GOODID.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	swapped := strings.Replace(string(b), `"id":"GOODID"`, `"id":"EVILID"`, 1)
	if err := os.WriteFile(p, []byte(swapped), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	st.OnError = func(string, error) { t.Error("an id mismatch must not be swallowed as damage") }
	if _, err := st.List(context.Background(), golm.SessionQuery{}); err == nil {
		t.Error("List hid a file filed under another id")
	}
	if _, err := st.Search(context.Background(), golm.SessionQuery{Text: "x"}); err == nil {
		t.Error("Search hid a file filed under another id")
	}
}

func sessionFor(id string, s *golm.Session) *golm.Session {
	d := s.Snapshot()
	d.ID = id
	return d.Session()
}

// OnError is the caller's own code on the path of every unreadable file.
func TestPanickingOnErrorDoesNotKillTheListing(t *testing.T) {
	dir := t.TempDir()
	st := &sessionstore.Files{Dir: dir}
	s := golm.NewSession()
	s.Append(golm.UserText("real one"))
	if err := st.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var called bool
	st.OnError = func(string, error) { called = true; panic("boom") }

	metas, err := st.List(context.Background(), golm.SessionQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !called {
		t.Fatal("OnError was never reached; the test proves nothing")
	}
	if len(metas) != 1 {
		t.Errorf("%d sessions listed, want the one good session", len(metas))
	}
}
