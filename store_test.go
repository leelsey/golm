// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type funcStore struct {
	save   func(context.Context, *Session) error
	load   func(context.Context, string) (*Session, error)
	list   func(context.Context, SessionQuery) ([]SessionMeta, error)
	search func(context.Context, SessionQuery) ([]SessionHit, error)
	del    func(context.Context, string) error
}

func (f funcStore) Save(ctx context.Context, s *Session) error {
	if f.save == nil {
		return nil
	}
	return f.save(ctx, s)
}

func (f funcStore) Load(ctx context.Context, id string) (*Session, error) {
	if f.load == nil {
		return nil, ErrSessionNotFound
	}
	return f.load(ctx, id)
}

func (f funcStore) List(ctx context.Context, q SessionQuery) ([]SessionMeta, error) {
	if f.list == nil {
		return nil, nil
	}
	return f.list(ctx, q)
}

func (f funcStore) Search(ctx context.Context, q SessionQuery) ([]SessionHit, error) {
	if f.search == nil {
		return nil, nil
	}
	return f.search(ctx, q)
}

func (f funcStore) Delete(ctx context.Context, id string) error {
	if f.del == nil {
		return ErrSessionNotFound
	}
	return f.del(ctx, id)
}

func newMemStore() funcStore {
	var mu sync.Mutex
	data := make(map[string]SessionData)
	return funcStore{
		save: func(_ context.Context, s *Session) error {
			d := s.Snapshot()
			mu.Lock()
			defer mu.Unlock()
			data[d.ID] = d
			return nil
		},
		load: func(_ context.Context, id string) (*Session, error) {
			mu.Lock()
			defer mu.Unlock()
			d, ok := data[id]
			if !ok {
				return nil, ErrSessionNotFound
			}
			return d.Session(), nil
		},
		del: func(_ context.Context, id string) error {
			mu.Lock()
			defer mu.Unlock()
			if _, ok := data[id]; !ok {
				return ErrSessionNotFound
			}
			delete(data, id)
			return nil
		},
	}
}

func saveLinked(t *testing.T, store funcStore, id, parent string) {
	t.Helper()
	s := SessionData{ID: id, Parent: parent}.Session()
	if err := store.Save(context.Background(), s); err != nil {
		t.Fatalf("save %s: %v", id, err)
	}
}

func lineageIDs(t *testing.T, store SessionStore, id string) ([]string, error) {
	t.Helper()
	metas, err := Lineage(context.Background(), store, id)
	ids := make([]string, len(metas))
	for i, m := range metas {
		ids[i] = m.ID
	}
	return ids, err
}

func TestLineageWalksParentsNewestFirst(t *testing.T) {
	store := newMemStore()
	saveLinked(t, store, "a", "")
	saveLinked(t, store, "b", "a")
	saveLinked(t, store, "c", "b")

	got, err := lineageIDs(t, store, "c")
	if err != nil {
		t.Fatalf("Lineage: %v", err)
	}
	want := []string{"c", "b", "a"}
	if len(got) != len(want) {
		t.Fatalf("Lineage = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Lineage = %v, want %v (newest first, starting session included)", got, want)
		}
	}
}

func TestLineageStopsAtPrunedArchive(t *testing.T) {
	store := newMemStore()
	saveLinked(t, store, "c", "pruned")

	got, err := lineageIDs(t, store, "c")
	if err != nil {
		t.Fatalf("Lineage on a pruned parent = %v; a missing archive is not an error", err)
	}
	if len(got) != 1 || got[0] != "c" {
		t.Fatalf("Lineage = %v, want [c]", got)
	}
}

func TestLineageTerminatesOnCycle(t *testing.T) {
	backing := newMemStore()
	saveLinked(t, backing, "x", "y")
	saveLinked(t, backing, "y", "x")

	var calls int
	store := funcStore{load: func(ctx context.Context, id string) (*Session, error) {
		calls++
		if calls > 8 {
			return nil, errors.New("Lineage did not terminate on a cyclic parent chain")
		}
		return backing.Load(ctx, id)
	}}

	got, err := lineageIDs(t, store, "x")
	if err != nil {
		t.Fatalf("Lineage: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Lineage = %v, want each session once", got)
	}
}

func TestLineagePropagatesStoreError(t *testing.T) {
	boom := errors.New("store unavailable")
	store := funcStore{load: func(context.Context, string) (*Session, error) { return nil, boom }}

	if _, err := lineageIDs(t, store, "c"); !errors.Is(err, boom) {
		t.Fatalf("Lineage error = %v, want %v", err, boom)
	}
}
