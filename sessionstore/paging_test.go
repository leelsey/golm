// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package sessionstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

// Paging arithmetic decides which slice of a listing a caller is shown.
func TestPageBounds(t *testing.T) {
	for _, c := range []struct {
		name           string
		n              int
		offset, limit  int
		wantLo, wantHi int
	}{
		{"no paging", 10, 0, 0, 0, 10},
		{"limit only", 10, 0, 3, 0, 3},
		{"offset only", 10, 4, 0, 4, 10},
		{"offset and limit", 10, 4, 3, 4, 7},
		{"limit past the end", 10, 8, 5, 8, 10},
		{"offset at the end", 10, 10, 5, 10, 10},
		{"offset past the end", 10, 25, 5, 10, 10},
		{"negative offset", 10, -3, 2, 0, 2},
		{"negative limit is no limit", 10, 0, -1, 0, 10},
		{"empty listing", 0, 0, 5, 0, 0},
		{"empty listing with offset", 0, 7, 5, 0, 0},
	} {
		lo, hi := pageBounds(c.n, golm.SessionQuery{Offset: c.offset, Limit: c.limit})
		if lo != c.wantLo || hi != c.wantHi {
			t.Errorf("%s: pageBounds(%d, off=%d lim=%d) = (%d,%d), want (%d,%d)",
				c.name, c.n, c.offset, c.limit, lo, hi, c.wantLo, c.wantHi)
		}

		if lo < 0 || hi < lo || hi > c.n {
			t.Errorf("%s: (%d,%d) is not a valid range over %d", c.name, lo, hi, c.n)
		}
	}
}

// Listings are newest first.
func TestByUpdatedOrdersNewestFirstAndBreaksTiesStably(t *testing.T) {
	now := time.Now()
	older := golm.SessionMeta{ID: "a", Updated: now.Add(-time.Hour)}
	newer := golm.SessionMeta{ID: "b", Updated: now}
	if !byUpdated(newer, older) {
		t.Error("the newer session did not sort first")
	}
	if byUpdated(older, newer) {
		t.Error("the older session sorted first")
	}

	tieA := golm.SessionMeta{ID: "aaa", Updated: now}
	tieB := golm.SessionMeta{ID: "bbb", Updated: now}
	if !byUpdated(tieA, tieB) || byUpdated(tieB, tieA) {
		t.Error("a tie did not break by id; the order would wobble between listings")
	}
	if byUpdated(tieA, tieA) {
		t.Error("a session sorted before itself; the comparator is not a strict order")
	}
}

// A save that cannot complete must leave NOTHING behind.
func TestSaveLeavesNothingBehindWhenItCannotFinish(t *testing.T) {
	dir := t.TempDir()
	st := NewFiles(dir)
	ctx := context.Background()

	good := golm.NewSession()
	good.Append(golm.UserText("first"))
	if err := st.Save(ctx, good); err != nil {
		t.Fatalf("first save: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(dir, good.ID()+".json"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("cannot make the directory read-only here")
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	good.Append(golm.AssistantText("second"))
	if err := st.Save(ctx, good); err == nil {
		t.Fatal("a save into a read-only directory reported success")
	}

	_ = os.Chmod(dir, 0o700)
	after, err := os.ReadFile(filepath.Join(dir, good.ID()+".json"))
	if err != nil {
		t.Fatalf("the previous session is gone: %v", err)
	}
	if string(after) != string(before) {
		t.Error("a failed save changed the stored session")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" && filepath.Ext(e.Name()) != ".idx" {
			t.Errorf("a failed save left %q behind", e.Name())
		}
	}
}

// Reindex rebuilds the sidecars deliberately.
func TestReindexRebuildsFromNothing(t *testing.T) {
	dir := t.TempDir()
	st := NewFiles(dir)
	ctx := context.Background()

	for _, text := range []string{"alpha beta", "gamma delta"} {
		s := golm.NewSession()
		s.Append(golm.UserText(text))
		if err := st.Save(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".idx" {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}

	hits, err := st.Search(ctx, golm.SessionQuery{Text: "gamma"})
	if err != nil {
		t.Fatalf("search without sidecars: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("search without sidecars found %d, want 1", len(hits))
	}

	n, err := st.Reindex()
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if n != 2 {
		t.Errorf("Reindex reported %d rebuilt, want 2", n)
	}
	var idx int
	entries, _ = os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".idx" {
			idx++
		}
	}
	if idx != 2 {
		t.Errorf("Reindex produced %d sidecars, want 2", idx)
	}
	hits, err = st.Search(ctx, golm.SessionQuery{Text: "gamma"})
	if err != nil {
		t.Fatalf("search after reindex: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("search after reindex found %d, want 1", len(hits))
	}
}

// Deleting removes the session AND its sidecar.
func TestDeleteRemovesTheSidecarToo(t *testing.T) {
	dir := t.TempDir()
	st := NewFiles(dir)
	ctx := context.Background()
	s := golm.NewSession()
	s.Append(golm.UserText("hello"))
	if err := st.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, s.ID()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("Delete left %v behind", names)
	}
	if err := st.Delete(ctx, s.ID()); err == nil {
		t.Error("deleting a session twice reported success the second time")
	}
}
