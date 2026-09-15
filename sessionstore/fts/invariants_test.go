// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package fts_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/leelsey/golm"
)

// A half-indexed session answers searches wrongly.
func TestSaveIsAllOrNothing(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	save(t, st, "AAAAAA", "t", "the original text")

	st.MaxBytes = 300
	s := golm.NewSession()
	s.Append(golm.UserText(strings.Repeat("x", 4096)))
	d := s.Snapshot()
	d.ID = "AAAAAA"
	if err := st.Save(ctx, d.Session()); err == nil {
		t.Fatal("an oversized session was stored")
	}
	st.MaxBytes = 0

	got, err := st.Load(ctx, "AAAAAA")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(got.History()[0].Text(), "original text") {
		t.Errorf("the stored session changed: %q", got.History()[0].Text())
	}
	hits, err := st.Search(ctx, golm.SessionQuery{Text: "original"})
	if err != nil || len(hits) != 1 {
		t.Errorf("the index and the row disagree: %d hits, %v", len(hits), err)
	}
}

// SQLite takes one writer at a time.
func TestConcurrentSavesAllLand(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := golm.NewSession()
			s.Append(golm.UserText(fmt.Sprintf("conversation %d about pangolins", i)))
			d := s.Snapshot()
			d.ID = fmt.Sprintf("SESS%02d", i)
			if err := st.Save(ctx, d.Session()); err != nil {
				t.Errorf("save %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	metas, err := st.List(ctx, golm.SessionQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != n {
		t.Errorf("%d of %d sessions stored", len(metas), n)
	}
	hits, err := st.Search(ctx, golm.SessionQuery{Text: "pangolins"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != n {
		t.Errorf("%d of %d sessions indexed", len(hits), n)
	}
}

// Re-saving the same session must not accumulate index rows.
func TestResavingDoesNotDuplicateHits(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	s := golm.NewSession()
	s.Append(golm.UserText("a unique marmoset phrase"))
	d := s.Snapshot()
	d.ID = "AAAAAA"
	sess := d.Session()
	for i := 0; i < 5; i++ {
		if err := st.Save(ctx, sess); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	hits, err := st.Search(ctx, golm.SessionQuery{Text: "marmoset"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("%d hits for one conversation saved five times", len(hits))
	}
}

// A query the parser refuses and a query that matches nothing look identical to a user.
func TestEveryMalformedQueryShapeIsDistinguished(t *testing.T) {
	st := open(t)
	save(t, st, "AAAAAA", "t", "anything at all")
	for _, q := range []string{`"unterminated`, `NEAR(`, `AND`, `((`, `"a" NEAR`} {
		_, err := st.Search(context.Background(), golm.SessionQuery{Text: q})
		if err == nil {
			continue
		}
		if !strings.Contains(err.Error(), "malformed search expression") {
			t.Errorf("query %q failed as a database error rather than a query error: %v", q, err)
		}
	}
}

// Ordering must be TOTAL, or a paged result is unstable.
func TestSearchOrderIsStableAcrossPages(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		s := golm.NewSession()
		s.Append(golm.UserText("identical text in every session"))
		d := s.Snapshot()
		d.ID = fmt.Sprintf("SESS%02d", i)
		if err := st.Save(ctx, d.Session()); err != nil {
			t.Fatal(err)
		}
	}
	var paged []string
	for offset := 0; offset < 10; offset += 3 {
		hits, err := st.Search(ctx, golm.SessionQuery{Text: "identical", Limit: 3, Offset: offset})
		if err != nil {
			t.Fatalf("page at %d: %v", offset, err)
		}
		for _, h := range hits {
			paged = append(paged, h.Meta.ID)
		}
	}
	seen := map[string]bool{}
	for _, id := range paged {
		if seen[id] {
			t.Errorf("%s appeared on two pages; the order is not total", id)
		}
		seen[id] = true
	}
	if len(seen) != 10 {
		t.Errorf("paging returned %d of 10 sessions", len(seen))
	}
}
