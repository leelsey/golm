// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package fts_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/sessionstore/fts"
)

func open(t *testing.T) *fts.Store {
	t.Helper()
	s, err := fts.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func save(t *testing.T, st *fts.Store, id, title string, turns ...string) *golm.Session {
	t.Helper()
	s := golm.NewSession()
	s.SetTitle(title)
	for _, turn := range turns {
		s.Append(golm.UserText(turn), golm.AssistantText("understood"))
	}
	d := s.Snapshot()
	d.ID = id
	out := d.Session()
	if err := st.Save(context.Background(), out); err != nil {
		t.Fatalf("Save %s: %v", id, err)
	}
	return out
}

func hitIDs(hs []golm.SessionHit) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.Meta.ID
	}
	return out
}

func TestRoundTrip(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	orig := save(t, st, "AAAAAA", "a title", "the build gate is lint")
	if err := orig.SetState("k", 42); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(ctx, orig); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := st.Load(ctx, "AAAAAA")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ID() != "AAAAAA" || got.Title() != "a title" || got.Len() != 2 {
		t.Errorf("loaded %s %q with %d messages", got.ID(), got.Title(), got.Len())
	}

	v, ok, err := golm.GetState[int](got, "k")
	if err != nil || !ok || v != 42 {
		t.Errorf("state = %v, %v, %v", v, ok, err)
	}
	if _, err := st.Load(ctx, "NOSUCH"); !errors.Is(err, golm.ErrSessionNotFound) {
		t.Errorf("missing session err = %v", err)
	}
}

func TestDelete(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	save(t, st, "AAAAAA", "t", "the needle")
	if err := st.Delete(ctx, "AAAAAA"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Load(ctx, "AAAAAA"); !errors.Is(err, golm.ErrSessionNotFound) {
		t.Errorf("load after delete: %v", err)
	}

	hits, err := st.Search(ctx, golm.SessionQuery{Text: "needle"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("a deleted session still matches: %v", hitIDs(hits))
	}
	if err := st.Delete(ctx, "AAAAAA"); !errors.Is(err, golm.ErrSessionNotFound) {
		t.Errorf("second delete = %v", err)
	}
}

func TestListFiltersAndOrder(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour).UTC()
	for i, id := range []string{"AAAAAA", "BBBBBB", "CCCCCC"} {
		s := golm.NewSession()
		s.Append(golm.UserText("x"))
		d := s.Snapshot()
		d.ID, d.Updated, d.Created = id, base.Add(time.Duration(i)*time.Minute), base
		if i == 2 {
			d.Parent = "AAAAAA"
		}
		if err := st.Save(ctx, d.Session()); err != nil {
			t.Fatal(err)
		}
	}
	metas, err := st.List(ctx, golm.SessionQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 3 || metas[0].ID != "CCCCCC" || metas[2].ID != "AAAAAA" {
		t.Errorf("listing is not newest-first: %+v", metas)
	}
	byParent, err := st.List(ctx, golm.SessionQuery{Parent: "AAAAAA"})
	if err != nil || len(byParent) != 1 || byParent[0].ID != "CCCCCC" {
		t.Errorf("parent filter: %+v, %v", byParent, err)
	}
	since, err := st.List(ctx, golm.SessionQuery{Since: base.Add(30 * time.Second)})
	if err != nil || len(since) != 2 {
		t.Errorf("since filter returned %d, want 2", len(since))
	}
	page, err := st.List(ctx, golm.SessionQuery{Limit: 1, Offset: 1})
	if err != nil || len(page) != 1 || page[0].ID != "BBBBBB" {
		t.Errorf("paging: %+v, %v", page, err)
	}
}

// The query language is the reason this backend exists.
func TestFTS5QuerySyntax(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	save(t, st, "AAAAAA", "security", "we should audit the authentication flow")
	save(t, st, "BBBBBB", "perf", "the authorisation cache is slow")
	save(t, st, "CCCCCC", "misc", "nothing related at all")

	cases := []struct {
		query string
		want  []string
	}{
		{"audit", []string{"AAAAAA"}},
		{`"authentication flow"`, []string{"AAAAAA"}},
		{"auth*", []string{"AAAAAA", "BBBBBB"}},
		{"audit OR cache", []string{"AAAAAA", "BBBBBB"}},
		{"audit AND authentication", []string{"AAAAAA"}},
		{"cache NOT slow", nil},
		{"NEAR(audit authentication, 3)", []string{"AAAAAA"}},
		{"nothingatall", nil},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			hits, err := st.Search(ctx, golm.SessionQuery{Text: c.query})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			got := hitIDs(hits)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			seen := map[string]bool{}
			for _, id := range got {
				seen[id] = true
			}
			for _, id := range c.want {
				if !seen[id] {
					t.Errorf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

// A malformed expression and "no results" look identical to a user and have opposite remedies.
func TestMalformedQueryIsReportedAsSuch(t *testing.T) {
	st := open(t)
	save(t, st, "AAAAAA", "t", "anything")
	_, err := st.Search(context.Background(), golm.SessionQuery{Text: `"unterminated`})
	if err == nil {
		t.Fatal("a malformed expression returned no error")
	}
	if !errors.Is(err, fts.ErrBadQuery) {
		t.Errorf("err = %v, want ErrBadQuery", err)
	}
}

// BM25 is the other reason to run a database here.
func TestSearchOrdersByRelevance(t *testing.T) {
	st := open(t)
	ctx := context.Background()

	save(t, st, "BURIED", "t", strings.Repeat("unrelated filler words here ", 60)+" kerberos")
	save(t, st, "FOCUSED", "t", "kerberos")

	hits, err := st.Search(ctx, golm.SessionQuery{Text: "kerberos"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %v", hitIDs(hits))
	}
	if hits[0].Meta.ID != "FOCUSED" {
		t.Errorf("ranked %v; the focused session should come first", hitIDs(hits))
	}
}

// SessionHit carries which message matched.
func TestHitNamesTheMatchingMessage(t *testing.T) {
	st := open(t)
	save(t, st, "AAAAAA", "a title", "first turn", "second turn with pangolin")
	hits, err := st.Search(context.Background(), golm.SessionQuery{Text: "pangolin"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits", len(hits))
	}
	if hits[0].Index != 2 {
		t.Errorf("Index = %d, want 2 (the third message)", hits[0].Index)
	}
	if !strings.Contains(hits[0].Snippet, "pangolin") {
		t.Errorf("snippet does not show the match: %q", hits[0].Snippet)
	}

	titleHits, err := st.Search(context.Background(), golm.SessionQuery{Text: "title"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(titleHits) != 1 || titleHits[0].Index != -1 {
		t.Errorf("title hit = %+v", titleHits)
	}
}

// A session can be trimmed or compacted.
func TestReindexOnSaveDropsRemovedMessages(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	s := save(t, st, "AAAAAA", "t", "keep this", "forget the marmoset")
	s.Trim(0)
	s.Append(golm.UserText("only this remains"))
	if err := st.Save(ctx, s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	hits, err := st.Search(ctx, golm.SessionQuery{Text: "marmoset"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 {
		t.Error("a message removed from the session still matches")
	}
	if hits, _ := st.Search(ctx, golm.SessionQuery{Text: "remains"}); len(hits) != 1 {
		t.Error("the new message was not indexed")
	}
}

func TestSearchHonoursFiltersAndPaging(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		s := golm.NewSession()
		s.Append(golm.UserText("shared keyword here"))
		d := s.Snapshot()
		d.ID = fmt.Sprintf("SESS%02d", i)
		if i%2 == 0 {
			d.Parent = "ROOT"
		}
		if err := st.Save(ctx, d.Session()); err != nil {
			t.Fatal(err)
		}
	}
	all, err := st.Search(ctx, golm.SessionQuery{Text: "keyword"})
	if err != nil || len(all) != 5 {
		t.Fatalf("got %d, %v", len(all), err)
	}
	kids, err := st.Search(ctx, golm.SessionQuery{Text: "keyword", Parent: "ROOT"})
	if err != nil || len(kids) != 3 {
		t.Errorf("parent filter returned %d, want 3", len(kids))
	}
	page, err := st.Search(ctx, golm.SessionQuery{Text: "keyword", Limit: 2})
	if err != nil || len(page) != 2 {
		t.Errorf("limit returned %d, want 2", len(page))
	}
}

// An empty query lists everything, matching the default store.
func TestEmptySearchListsEverything(t *testing.T) {
	st := open(t)
	save(t, st, "AAAAAA", "t", "x")
	save(t, st, "BBBBBB", "t", "y")
	hits, err := st.Search(context.Background(), golm.SessionQuery{})
	if err != nil || len(hits) != 2 {
		t.Errorf("got %d, %v", len(hits), err)
	}
}

// It is a golm.SessionStore.
func TestLineageWalksParents(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	save(t, st, "OLDEST", "t", "first")
	for _, pair := range [][2]string{{"MIDDLE", "OLDEST"}, {"NEWEST", "MIDDLE"}} {
		s := golm.NewSession()
		s.Append(golm.UserText("x"))
		d := s.Snapshot()
		d.ID, d.Parent = pair[0], pair[1]
		if err := st.Save(ctx, d.Session()); err != nil {
			t.Fatal(err)
		}
	}
	line, err := golm.Lineage(ctx, st, "NEWEST")
	if err != nil {
		t.Fatalf("Lineage: %v", err)
	}
	if len(line) != 3 || line[0].ID != "NEWEST" || line[2].ID != "OLDEST" {
		t.Errorf("lineage = %+v", line)
	}
}

// A row moved or rewritten by hand must not be served under another name.
func TestSwappedRowIsRefused(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	save(t, st, "GOODID", "t", "x")
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE sessions SET data = replace(CAST(data AS TEXT), '"GOODID"', '"EVILID"') WHERE id = 'GOODID'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load(ctx, "GOODID"); err == nil {
		t.Error("a row holding another id was served")
	}
}

func TestOversizedSessionIsRefused(t *testing.T) {
	st := open(t)
	st.MaxBytes = 512
	s := golm.NewSession()
	s.Append(golm.UserText(strings.Repeat("x", 4096)))
	d := s.Snapshot()
	d.ID = "BIGONE"
	if err := st.Save(context.Background(), d.Session()); err == nil {
		t.Error("an oversized session was stored")
	}
}

func TestConcurrentUse(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			s := golm.NewSession()
			s.Append(golm.UserText(fmt.Sprintf("concurrent turn %d", i)))
			d := s.Snapshot()
			d.ID = fmt.Sprintf("CONC%02d", i)
			if err := st.Save(ctx, d.Session()); err != nil {
				done <- err
				return
			}
			_, err := st.Search(ctx, golm.SessionQuery{Text: "concurrent"})
			done <- err
		}(i)
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent use: %v", err)
		}
	}
	metas, err := st.List(ctx, golm.SessionQuery{})
	if err != nil || len(metas) != 8 {
		t.Errorf("stored %d of 8: %v", len(metas), err)
	}
}
