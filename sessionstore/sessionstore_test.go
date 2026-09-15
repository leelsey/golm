// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/sessionstore"
)

var base = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

func mk(id, parent, title string, updated time.Time, msgs ...golm.Message) *golm.Session {
	return golm.SessionData{
		ID: id, Parent: parent, Title: title,
		Created: updated, Updated: updated, History: msgs,
	}.Session()
}

func save(t *testing.T, st golm.SessionStore, sessions ...*golm.Session) {
	t.Helper()
	for _, s := range sessions {
		if err := st.Save(context.Background(), s); err != nil {
			t.Fatalf("save %s: %v", s.ID(), err)
		}
	}
}

func ids(ms []golm.SessionMeta) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var conformance = []struct {
	name string
	run  func(t *testing.T, st golm.SessionStore)
}{
	{"round trip", func(t *testing.T, st golm.SessionStore) {
		s := mk("AAAAA", "PARENT", "Kick off", base,
			golm.UserText("what is the plan"), golm.AssistantText("ship it"))
		if err := s.SetState("step", 7); err != nil {
			t.Fatalf("set state: %v", err)
		}
		save(t, st, s)

		got, err := st.Load(context.Background(), "AAAAA")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got.ID() != "AAAAA" || got.Parent() != "PARENT" || got.Title() != "Kick off" {
			t.Fatalf("identity lost: %+v", got.Meta())
		}
		if got.Len() != 2 {
			t.Fatalf("messages = %d, want 2", got.Len())
		}
		h := got.History()
		if h[0].Text() != "what is the plan" || h[1].Text() != "ship it" {
			t.Fatalf("transcript lost: %q / %q", h[0].Text(), h[1].Text())
		}
		if h[0].Role != golm.RoleUser || h[1].Role != golm.RoleAssistant {
			t.Fatalf("roles lost: %q / %q", h[0].Role, h[1].Role)
		}
		step, ok, err := golm.GetState[int](got, "step")
		if err != nil || !ok || step != 7 {
			t.Fatalf("state = %v, %v, %v; want 7, true, nil", step, ok, err)
		}
	}},

	{"save overwrites", func(t *testing.T, st golm.SessionStore) {
		s := mk("AAAAA", "", "first", base)
		save(t, st, s)
		s.SetTitle("second")
		s.Append(golm.UserText("more"))
		save(t, st, s)

		got, err := st.Load(context.Background(), "AAAAA")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got.Title() != "second" || got.Len() != 1 {
			t.Fatalf("stale session: title %q, %d messages", got.Title(), got.Len())
		}
	}},

	{"unknown id", func(t *testing.T, st golm.SessionStore) {
		if _, err := st.Load(context.Background(), "NOSUCH"); !errors.Is(err, golm.ErrSessionNotFound) {
			t.Fatalf("load unknown: %v, want ErrSessionNotFound", err)
		}
		if err := st.Delete(context.Background(), "NOSUCH"); !errors.Is(err, golm.ErrSessionNotFound) {
			t.Fatalf("delete unknown: %v, want ErrSessionNotFound", err)
		}
	}},

	{"delete", func(t *testing.T, st golm.SessionStore) {
		save(t, st, mk("AAAAA", "", "a", base), mk("BBBBB", "", "b", base))
		if err := st.Delete(context.Background(), "AAAAA"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := st.Load(context.Background(), "AAAAA"); !errors.Is(err, golm.ErrSessionNotFound) {
			t.Fatalf("load deleted: %v, want ErrSessionNotFound", err)
		}
		ms, err := st.List(context.Background(), golm.SessionQuery{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if !equal(ids(ms), []string{"BBBBB"}) {
			t.Fatalf("list after delete = %v, want [BBBBB]", ids(ms))
		}
	}},

	{"list ordering and paging", func(t *testing.T, st golm.SessionStore) {
		save(t, st,
			mk("AAAAA", "", "oldest", base),
			mk("BBBBB", "", "middle", base.Add(time.Minute)),
			mk("CCCCC", "", "newest", base.Add(2*time.Minute)),
		)
		ctx := context.Background()

		all, err := st.List(ctx, golm.SessionQuery{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if !equal(ids(all), []string{"CCCCC", "BBBBB", "AAAAA"}) {
			t.Fatalf("order = %v, want newest first", ids(all))
		}
		if all[0].Messages != 0 || all[0].Title != "newest" {
			t.Fatalf("meta = %+v", all[0])
		}

		page, err := st.List(ctx, golm.SessionQuery{Limit: 2})
		if err != nil {
			t.Fatalf("list limit: %v", err)
		}
		if !equal(ids(page), []string{"CCCCC", "BBBBB"}) {
			t.Fatalf("limit = %v", ids(page))
		}

		page, err = st.List(ctx, golm.SessionQuery{Limit: 2, Offset: 1})
		if err != nil {
			t.Fatalf("list offset: %v", err)
		}
		if !equal(ids(page), []string{"BBBBB", "AAAAA"}) {
			t.Fatalf("offset = %v", ids(page))
		}

		page, err = st.List(ctx, golm.SessionQuery{Offset: 9})
		if err != nil {
			t.Fatalf("list past end: %v", err)
		}
		if len(page) != 0 {
			t.Fatalf("offset past end = %v, want none", ids(page))
		}
	}},

	{"list filters", func(t *testing.T, st golm.SessionStore) {
		save(t, st,
			mk("AAAAA", "", "root", base),
			mk("BBBBB", "AAAAA", "child", base.Add(time.Minute)),
			mk("CCCCC", "AAAAA", "child", base.Add(2*time.Minute)),
		)
		ctx := context.Background()

		kids, err := st.List(ctx, golm.SessionQuery{Parent: "AAAAA"})
		if err != nil {
			t.Fatalf("list parent: %v", err)
		}
		if !equal(ids(kids), []string{"CCCCC", "BBBBB"}) {
			t.Fatalf("parent filter = %v", ids(kids))
		}

		win, err := st.List(ctx, golm.SessionQuery{
			Since: base.Add(time.Minute), Until: base.Add(time.Minute),
		})
		if err != nil {
			t.Fatalf("list window: %v", err)
		}
		if !equal(ids(win), []string{"BBBBB"}) {
			t.Fatalf("window filter = %v, want [BBBBB]", ids(win))
		}
	}},

	{"search", func(t *testing.T, st golm.SessionStore) {
		save(t, st,
			mk("AAAAA", "", "Untitled", base,
				golm.UserText("morning"),
				golm.AssistantText("The Kraken sleeps beneath the Thunders of the upper deep"),
			),
			mk("BBBBB", "", "Kraken notes", base.Add(time.Minute)),
			mk("CCCCC", "", "Untitled", base.Add(2*time.Minute), golm.UserText("nothing relevant here")),
		)
		ctx := context.Background()

		hits, err := st.Search(ctx, golm.SessionQuery{Text: "kraken"})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(hits) != 2 {
			t.Fatalf("hits = %d, want 2", len(hits))
		}
		if hits[0].Meta.ID != "BBBBB" || hits[1].Meta.ID != "AAAAA" {
			t.Fatalf("hit order = %s, %s", hits[0].Meta.ID, hits[1].Meta.ID)
		}
		if hits[0].Index != -1 {
			t.Fatalf("title hit index = %d, want -1", hits[0].Index)
		}
		if hits[0].Snippet != "Kraken notes" {
			t.Fatalf("title snippet = %q", hits[0].Snippet)
		}
		if hits[1].Index != 1 {
			t.Fatalf("message hit index = %d, want 1", hits[1].Index)
		}
		if !strings.Contains(strings.ToLower(hits[1].Snippet), "kraken") {
			t.Fatalf("message snippet = %q", hits[1].Snippet)
		}

		both, err := st.Search(ctx, golm.SessionQuery{Text: "KRAKEN thunders"})
		if err != nil {
			t.Fatalf("search terms: %v", err)
		}
		if len(both) != 1 || both[0].Meta.ID != "AAAAA" {
			t.Fatalf("every term must match: %v", both)
		}

		none, err := st.Search(ctx, golm.SessionQuery{Text: "kraken unicorn"})
		if err != nil {
			t.Fatalf("search miss: %v", err)
		}
		if len(none) != 0 {
			t.Fatalf("missing term still matched: %v", none)
		}

		scoped, err := st.Search(ctx, golm.SessionQuery{Text: "kraken", Limit: 1})
		if err != nil {
			t.Fatalf("search limit: %v", err)
		}
		if len(scoped) != 1 || scoped[0].Meta.ID != "BBBBB" {
			t.Fatalf("search paging = %v", scoped)
		}
	}},

	{"search snippet is bounded", func(t *testing.T, st golm.SessionStore) {
		long := strings.Repeat("filler ", 400) + "needle " + strings.Repeat("filler ", 400)
		save(t, st, mk("AAAAA", "", "", base, golm.UserText(long)))

		hits, err := st.Search(context.Background(), golm.SessionQuery{Text: "needle"})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(hits) != 1 {
			t.Fatalf("hits = %d, want 1", len(hits))
		}
		if n := len([]rune(hits[0].Snippet)); n > 200 {
			t.Fatalf("snippet is %d runes, want bounded", n)
		}
		if !strings.Contains(hits[0].Snippet, "needle") {
			t.Fatalf("snippet lost the term: %q", hits[0].Snippet)
		}
	}},

	{"lineage", func(t *testing.T, st golm.SessionStore) {
		save(t, st,
			mk("GEN000", "", "first", base),
			mk("GEN111", "GEN000", "second", base.Add(time.Minute)),
			mk("GEN222", "GEN111", "third", base.Add(2*time.Minute)),
		)
		line, err := golm.Lineage(context.Background(), st, "GEN222")
		if err != nil {
			t.Fatalf("lineage: %v", err)
		}
		if !equal(ids(line), []string{"GEN222", "GEN111", "GEN000"}) {
			t.Fatalf("lineage = %v", ids(line))
		}
	}},

	{"lineage stops at a pruned archive", func(t *testing.T, st golm.SessionStore) {
		save(t, st, mk("GEN111", "GONE00", "orphan", base))
		line, err := golm.Lineage(context.Background(), st, "GEN111")
		if err != nil {
			t.Fatalf("lineage: %v", err)
		}
		if !equal(ids(line), []string{"GEN111"}) {
			t.Fatalf("lineage = %v, want just the survivor", ids(line))
		}
	}},

	{"lineage terminates on a cycle", func(t *testing.T, st golm.SessionStore) {
		save(t, st,
			mk("CYCAAA", "CYCBBB", "a", base),
			mk("CYCBBB", "CYCAAA", "b", base),
		)
		line, err := golm.Lineage(context.Background(), st, "CYCAAA")
		if err != nil {
			t.Fatalf("lineage: %v", err)
		}
		if !equal(ids(line), []string{"CYCAAA", "CYCBBB"}) {
			t.Fatalf("lineage = %v, want each id once", ids(line))
		}
	}},
}

func TestStores(t *testing.T) {
	backends := []struct {
		name string
		open func(t *testing.T) golm.SessionStore
	}{
		{"memory", func(t *testing.T) golm.SessionStore { return sessionstore.NewMemory() }},
		{"files", func(t *testing.T) golm.SessionStore { return sessionstore.NewFiles(t.TempDir()) }},
	}
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			for _, c := range conformance {
				t.Run(c.name, func(t *testing.T) { c.run(t, b.open(t)) })
			}
		})
	}
}

func TestFilesRejectsUnsafeIDs(t *testing.T) {
	bad := []string{"", ".", "..", "../escape", "/etc/passwd", "a/b", `a\b`, "sess.json", strings.Repeat("A", 200)}
	for _, id := range bad {
		dir := t.TempDir()
		st := sessionstore.NewFiles(dir)
		ctx := context.Background()

		if id != "" {
			if err := st.Save(ctx, mk(id, "", "hostile", base)); !errors.Is(err, sessionstore.ErrInvalidID) {
				t.Fatalf("save %q: %v, want ErrInvalidID", id, err)
			}
		}
		if _, err := st.Load(ctx, id); !errors.Is(err, sessionstore.ErrInvalidID) {
			t.Fatalf("load %q: %v, want ErrInvalidID", id, err)
		}
		if err := st.Delete(ctx, id); !errors.Is(err, sessionstore.ErrInvalidID) {
			t.Fatalf("delete %q: %v, want ErrInvalidID", id, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("id %q wrote %d entries", id, len(entries))
		}
	}
}

func TestFilesRejectsSwappedID(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	ctx := context.Background()

	save(t, st, mk("GOODID", "", "mine", base))
	swapped := filepath.Join(dir, "GOODID.json")
	b, err := os.ReadFile(filepath.Join(dir, "GOODID.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(swapped, []byte(strings.Replace(string(b), `"id":"GOODID"`, `"id":"EVILID"`, 1)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := st.Load(ctx, "GOODID"); err == nil {
		t.Fatal("load served a file filed under another id")
	} else if errors.Is(err, golm.ErrSessionNotFound) {
		t.Fatalf("load: %v, want a mismatch error", err)
	}
	if _, err := st.List(ctx, golm.SessionQuery{}); err == nil {
		t.Fatal("list served a file filed under another id")
	}
}

func TestFilesRejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	st := sessionstore.NewFiles(dir)
	save(t, st, mk("BIGONE", "", "big", base, golm.UserText(strings.Repeat("x", 4096))))

	st.MaxBytes = 512
	if _, err := st.Load(context.Background(), "BIGONE"); !errors.Is(err, sessionstore.ErrTooLarge) {
		t.Fatalf("load oversized: %v, want ErrTooLarge", err)
	}
}
