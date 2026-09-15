// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package sessionstore

import (
	"context"
	"sync"

	"github.com/leelsey/golm"
)

var _ golm.SessionStore = (*Memory)(nil)

// Memory keeps sessions in a map for the life of the process.
type Memory struct {
	mu sync.RWMutex
	m  map[string]golm.SessionData
}

// NewMemory returns an empty store.
func NewMemory() *Memory { return &Memory{} }

// Save stores a snapshot of s under its id.
func (s *Memory) Save(ctx context.Context, sess *golm.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d := sess.Snapshot()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = make(map[string]golm.SessionData)
	}
	s.m[d.ID] = d
	return nil
}

// Load rebuilds the session stored under id.
func (s *Memory) Load(ctx context.Context, id string) (*golm.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	d, ok := s.m[id]
	s.mu.RUnlock()
	if !ok {
		return nil, golm.ErrSessionNotFound
	}
	return d.Session(), nil
}

// List returns matching sessions, newest first.
func (s *Memory) List(ctx context.Context, q golm.SessionQuery) ([]golm.SessionMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	out := make([]golm.SessionMeta, 0, len(s.m))
	for _, d := range s.m {
		if m := metaOf(d); selects(m, q) {
			out = append(out, m)
		}
	}
	s.mu.RUnlock()
	sortMetas(out)
	lo, hi := pageBounds(len(out), q)
	return out[lo:hi], nil
}

// Search returns matching sessions newest first, one hit each.
func (s *Memory) Search(ctx context.Context, q golm.SessionQuery) ([]golm.SessionHit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	want := terms(q.Text)
	s.mu.RLock()
	var out []golm.SessionHit
	for _, d := range s.m {
		if !selects(metaOf(d), q) {
			continue
		}
		if h, ok := hitFor(d, want); ok {
			out = append(out, h)
		}
	}
	s.mu.RUnlock()
	sortHits(out)
	lo, hi := pageBounds(len(out), q)
	return out[lo:hi], nil
}

// Delete removes the session stored under id.
func (s *Memory) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[id]; !ok {
		return golm.ErrSessionNotFound
	}
	delete(s.m, id)
	return nil
}
