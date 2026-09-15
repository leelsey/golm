// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/sessionstore"
)

// A save goes through a temp file and a rename.
func TestConcurrentSavesLeaveNoDebris(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := golm.NewSession()
			s.Append(golm.UserText(strings.Repeat("content ", 200)))
			d := s.Snapshot()
			d.ID = fmt.Sprintf("SESS%02d", i)
			if err := st.Save(context.Background(), d.Session()); err != nil {
				t.Errorf("save %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") || strings.HasPrefix(e.Name(), ".golm-") {
			t.Errorf("a temporary file survived: %s", e.Name())
		}
	}
	metas, err := st.List(context.Background(), golm.SessionQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != n {
		t.Errorf("%d of %d sessions listed", len(metas), n)
	}
}

// The sidecar must never CHANGE an answer, only save work.
func TestIndexedAndUnindexedAgreeOverManyQueries(t *testing.T) {
	build := func(dir string) *sessionstore.Files {
		st := sessionstore.NewFiles(dir)
		bodies := []string{
			"the quick brown fox jumps",
			"RESEARCH the protocol thoroughly",
			"nothing of interest",
			"a CVE-2026-1234 advisory",
			"한글 텍스트와 english mixed",
			strings.Repeat("filler ", 300) + " needle",
		}
		for i, body := range bodies {
			s := golm.NewSession()
			s.SetTitle(fmt.Sprintf("session %d", i))
			s.Append(golm.UserText(body), golm.AssistantText("understood"))
			d := s.Snapshot()
			d.ID = fmt.Sprintf("SESS%02d", i)
			if err := st.Save(context.Background(), d.Session()); err != nil {
				t.Fatal(err)
			}
		}
		return st
	}
	indexed := build(t.TempDir())
	bare := build(t.TempDir())
	for _, f := range must(filepath.Glob(filepath.Join(bare.Dir, "*.idx"))) {
		if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
	}

	queries := []string{
		"", "quick", "QUICK", "uick", "research protocol", "research nothing",
		"cve", "CVE-2026", "한글", "mixed", "needle", "session 3", "zzz",
		"the quick", "fox jumps", "2026-1234",
	}
	ctx := context.Background()
	for _, q := range queries {
		want, err := bare.Search(ctx, golm.SessionQuery{Text: q})
		if err != nil {
			t.Fatalf("bare %q: %v", q, err)
		}
		got, err := indexed.Search(ctx, golm.SessionQuery{Text: q})
		if err != nil {
			t.Fatalf("indexed %q: %v", q, err)
		}
		if len(got) != len(want) {
			t.Errorf("query %q: indexed %v, unindexed %v", q, hitIDs(got), hitIDs(want))
			continue
		}
		for i := range want {
			if got[i].Meta.ID != want[i].Meta.ID || got[i].Index != want[i].Index ||
				got[i].Snippet != want[i].Snippet {
				t.Errorf("query %q hit %d differs:\n indexed:   %+v\n unindexed: %+v",
					q, i, got[i], want[i])
			}
		}

		for _, f := range must(filepath.Glob(filepath.Join(bare.Dir, "*.idx"))) {
			_ = os.Remove(f)
		}
	}
}

// A sidecar for a session that no longer exists must not turn up as a session.
func TestOrphanSidecarIsNotASession(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	seed(t, st, "AAAAAA", "t", "x")
	if err := st.Delete(context.Background(), "AAAAAA"); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "GHOSTID.idx"),
		[]byte(`{"v":1,"id":"GHOSTID","messages":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	metas, err := st.List(context.Background(), golm.SessionQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 0 {
		t.Errorf("listed %v; a sidecar without a session is not a session", metas)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
