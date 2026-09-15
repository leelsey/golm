// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/sessionstore"
)

func seed(t *testing.T, st *sessionstore.Files, id, title string, turns ...string) *golm.Session {
	t.Helper()
	s := golm.NewSession()
	s.SetTitle(title)
	for _, turn := range turns {
		s.Append(golm.UserText(turn), golm.AssistantText("noted"))
	}
	d := s.Snapshot()
	d.ID = id
	out := d.Session()
	if err := st.Save(context.Background(), out); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return out
}

// The sidecar exists so that listing and searching do not have to read the sessions themselves.
func TestSaveWritesASidecar(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	seed(t, st, "AAAAAA", "a title", "the build gate is lint")

	idx := filepath.Join(dir, "AAAAAA.idx")
	fi, err := os.Stat(idx)
	if err != nil {
		t.Fatalf("no sidecar written: %v", err)
	}
	full, err := os.Stat(filepath.Join(dir, "AAAAAA.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() >= full.Size() {
		t.Errorf("sidecar (%d bytes) is not smaller than the session (%d)", fi.Size(), full.Size())
	}

	metas, err := st.List(context.Background(), golm.SessionQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 {
		t.Errorf("listed %d sessions; a sidecar was counted as one", len(metas))
	}
}

// Identical results with and without sidecars.
func TestIndexedAndUnindexedAgree(t *testing.T) {
	queries := []golm.SessionQuery{
		{},
		{Text: "research"},
		{Text: "resear"},
		{Text: "RESEARCH"},
		{Text: "gate lint"},
		{Text: "gate nonexistent"},
		{Text: "a title"},
		{Parent: "AAAAAA"},
	}
	build := func(dir string) *sessionstore.Files {
		st := sessionstore.NewFiles(dir)
		seed(t, st, "AAAAAA", "a title", "the build gate is lint")
		seed(t, st, "BBBBBB", "another", "please research the protocol")
		seed(t, st, "CCCCCC", "third", "nothing relevant here")
		child := seed(t, st, "DDDDDD", "child", "gate and lint and research")
		d := child.Snapshot()
		d.Parent = "AAAAAA"
		if err := st.Save(context.Background(), d.Session()); err != nil {
			t.Fatalf("Save: %v", err)
		}
		return st
	}
	indexed := build(t.TempDir())

	bare := build(t.TempDir())

	files, _ := filepath.Glob(filepath.Join(bare.Dir, "*"+".idx"))
	for _, f := range files {
		if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	for _, q := range queries {
		wantHits, err := bare.Search(ctx, q)
		if err != nil {
			t.Fatalf("bare Search %+v: %v", q, err)
		}
		gotHits, err := indexed.Search(ctx, q)
		if err != nil {
			t.Fatalf("indexed Search %+v: %v", q, err)
		}
		if !sameHits(wantHits, gotHits) {
			t.Errorf("query %+v: indexed %v, unindexed %v", q, hitIDs(gotHits), hitIDs(wantHits))
		}
	}
}

func hitIDs(hs []golm.SessionHit) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.Meta.ID
	}
	return out
}

func sameHits(a, b []golm.SessionHit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Meta.ID != b[i].Meta.ID || a[i].Index != b[i].Index || a[i].Snippet != b[i].Snippet {
			return false
		}
	}
	return true
}

// A sidecar that does not match the file it describes must be ignored.
func TestStaleSidecarIsIgnored(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	s := seed(t, st, "AAAAAA", "old title", "the original text")

	d := s.Snapshot()
	d.Title = "new title"
	d.History = append(d.History, golm.UserText("a completely different subject"))
	raw, err := os.ReadFile(filepath.Join(dir, "AAAAAA.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = raw
	if err := writeJSON(filepath.Join(dir, "AAAAAA.json"), d); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	hits, err := st.Search(ctx, golm.SessionQuery{Text: "completely different"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("the stale sidecar hid new content: %d hits", len(hits))
	}
	metas, err := st.List(ctx, golm.SessionQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if metas[0].Title != "new title" {
		t.Errorf("listing served a stale title %q", metas[0].Title)
	}
}

// A sidecar written by an older golm, or corrupted, must be rebuilt rather than misread.
func TestUnusableSidecarIsRebuilt(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	seed(t, st, "AAAAAA", "t", "find the needle here")
	idx := filepath.Join(dir, "AAAAAA.idx")

	for _, body := range []string{`{"v":0,"id":"AAAAAA"}`, `not json`, ``} {
		if err := os.WriteFile(idx, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		hits, err := st.Search(context.Background(), golm.SessionQuery{Text: "needle"})
		if err != nil {
			t.Fatalf("Search with sidecar %q: %v", body, err)
		}
		if len(hits) != 1 {
			t.Errorf("sidecar %q lost the hit", body)
		}
	}
}

// A store with no sidecars behaves exactly as it did before they existed.
func TestStoreWorksWithNoSidecarsAtAll(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	seed(t, st, "AAAAAA", "t", "the needle")
	matches, _ := filepath.Glob(filepath.Join(dir, "*.idx"))
	for _, f := range matches {
		os.Remove(f)
	}
	hits, err := st.Search(context.Background(), golm.SessionQuery{Text: "needle"})
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits = %d, err = %v", len(hits), err)
	}

	if _, err := os.Stat(filepath.Join(dir, "AAAAAA.idx")); err != nil {
		t.Errorf("the sidecar was not rebuilt: %v", err)
	}
}

// A transcript with more distinct words than the cap cannot rule anything out.
func TestPartialSidecarStillFindsEverything(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	var huge strings.Builder
	for i := 0; i < 6000; i++ {
		fmt.Fprintf(&huge, "w%d ", i)
	}
	huge.WriteString("theneedle")
	seed(t, st, "AAAAAA", "t", huge.String())

	hits, err := st.Search(context.Background(), golm.SessionQuery{Text: "theneedle"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Error("a word past the token cap was lost; a partial index must not rule anything out")
	}
}

func TestDeleteRemovesTheSidecar(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	seed(t, st, "AAAAAA", "t", "x")
	if err := st.Delete(context.Background(), "AAAAAA"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AAAAAA.idx")); !os.IsNotExist(err) {
		t.Error("the sidecar outlived its session")
	}
}

func TestReindexRebuildsEverything(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	for _, id := range []string{"AAAAAA", "BBBBBB", "CCCCCC"} {
		seed(t, st, id, "t", "content "+id)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.idx"))
	for _, f := range matches {
		os.Remove(f)
	}
	n, err := st.Reindex()
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if n != 3 {
		t.Errorf("rebuilt %d, want 3", n)
	}
	again, _ := filepath.Glob(filepath.Join(dir, "*.idx"))
	if len(again) != 3 {
		t.Errorf("%d sidecars on disk, want 3", len(again))
	}
}

// The point of the whole thing.
func TestSearchDoesNotReadEveryTranscript(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	body := strings.Repeat("filler words that are not the needle ", 200)
	for i := 0; i < 40; i++ {
		seed(t, st, fmt.Sprintf("SESS%03d", i), "t", body)
	}
	seed(t, st, "NEEDLE", "t", "the unmistakable zzyzx marker")

	for i := 0; i < 40; i++ {
		p := filepath.Join(dir, fmt.Sprintf("SESS%03d.json", i))
		if err := os.Chmod(p, 0o000); err != nil {
			t.Skipf("cannot revoke read permission here: %v", err)
		}
		defer os.Chmod(p, 0o600)
	}
	var skipped int
	st.OnError = func(string, error) { skipped++ }

	hits, err := st.Search(context.Background(), golm.SessionQuery{Text: "zzyzx"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Meta.ID != "NEEDLE" {
		t.Fatalf("hits = %v", hitIDs(hits))
	}
	if skipped != 0 {
		t.Errorf("%d transcripts were opened that the index had already ruled out", skipped)
	}
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// The index is an accelerator and never an authority.
func TestIndexDoesNotPruneTermsItCannotAnswer(t *testing.T) {
	dir := t.TempDir()
	st := &sessionstore.Files{Dir: dir}
	long := strings.Repeat("z", 200)
	seed(t, st, "s1", "notes", "please call read_skill for the a2a/grpc notes "+long)

	for _, q := range []string{
		"read_skill",
		"a2a/grpc",
		"user@example.com",
		long,
		"read",
		"notes",
	} {
		hits, err := st.Search(context.Background(), golm.SessionQuery{Text: q})
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		want := 1
		if q == "user@example.com" {
			want = 0
		}
		if len(hits) != want {
			t.Errorf("search %q returned %d hits, want %d", q, len(hits), want)
		}
	}
}

// The pruning that DOES apply must still apply, or the index saves nothing.
func TestIndexStillPrunesPlainTerms(t *testing.T) {
	dir := t.TempDir()
	st := &sessionstore.Files{Dir: dir}
	seed(t, st, "s1", "alpha", "the quick brown fox")

	hits, err := st.Search(context.Background(), golm.SessionQuery{Text: "aardvark"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("%d hits for a word nobody said", len(hits))
	}
}
