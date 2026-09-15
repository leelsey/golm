// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package sessionstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/leelsey/golm"
)

// A session file is read back from disk.
func FuzzLoadSessionFile(f *testing.F) {
	f.Add(`{"id":"s1","messages":[]}`)
	f.Add(`{"id":"s1","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	f.Add(`{"id":"other","messages":[]}`)
	f.Add(`{"id":"s1","messages":[{"role":"bogus","content":null}]}`)
	f.Add(`{"id":"s1","parent":"p","title":"t","meta":{"k":"v"},"state":{"x":1}}`)
	f.Add(`{`)
	f.Add(``)
	f.Add(`null`)

	f.Fuzz(func(t *testing.T, body string) {
		dir := t.TempDir()
		st := NewFiles(dir)
		if err := os.WriteFile(filepath.Join(dir, "s1.json"), []byte(body), 0o600); err != nil {
			t.Skip()
		}
		s, err := st.Load(context.Background(), "s1")
		if err != nil {
			return
		}
		if s == nil {
			t.Fatalf("Load returned no error and no session for %q", body)
		}

		if s.ID() != "s1" {
			t.Fatalf("asked for s1 and got %q from %q", s.ID(), body)
		}

		before := s.History()
		if err := st.Save(context.Background(), s); err != nil {
			t.Fatalf("a loaded session will not save: %v (%q)", err, body)
		}
		again, err := st.Load(context.Background(), "s1")
		if err != nil {
			t.Fatalf("a session this store wrote will not load: %v", err)
		}
		if len(again.History()) != len(before) {
			t.Fatalf("round trip changed the transcript: %d then %d", len(before), len(again.History()))
		}
	})
}

// The sidecar index is an ACCELERATOR, never an authority.
func FuzzSidecarIndexIsNeverAnAuthority(f *testing.F) {
	f.Add(`{"id":"s1","words":["hello"],"size":10,"modified":"2026-01-01T00:00:00Z"}`)
	f.Add(`{"id":"s1","words":[],"partial":true}`)
	f.Add(`{"id":"elsewhere","words":["hello"]}`)
	f.Add(`{"words":null}`)
	f.Add(`garbage`)
	f.Add(``)

	f.Fuzz(func(t *testing.T, sidecar string) {
		dir := t.TempDir()
		st := NewFiles(dir)
		ctx := context.Background()

		s := golm.NewSession()
		s.Append(golm.UserText("the quick brown fox"))
		if err := st.Save(ctx, s); err != nil {
			t.Skip()
		}

		if err := os.WriteFile(st.indexPath(s.ID()), []byte(sidecar), 0o600); err != nil {
			t.Skip()
		}

		hits, err := st.Search(ctx, golm.SessionQuery{Text: "brown"})
		if err != nil {
			return
		}
		if len(hits) != 1 {
			t.Fatalf("a damaged sidecar changed the result: %d hits, want 1 (sidecar %q)", len(hits), sidecar)
		}
		metas, err := st.List(ctx, golm.SessionQuery{})
		if err != nil {
			return
		}
		if len(metas) != 1 {
			t.Fatalf("a damaged sidecar changed the listing: %d entries, want 1", len(metas))
		}
	})
}
