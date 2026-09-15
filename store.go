// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"errors"
	"time"
)

// ErrSessionNotFound is returned by a SessionStore for an unknown id.
var ErrSessionNotFound = errors.New("golm: session not found")

// ErrInvalidSessionID is returned by a SessionStore for an id it cannot address.
var ErrInvalidSessionID = errors.New("golm: invalid session id")

// SessionStore is a set of sessions addressable by id.
type SessionStore interface {
	Save(ctx context.Context, s *Session) error
	Load(ctx context.Context, id string) (*Session, error)
	List(ctx context.Context, q SessionQuery) ([]SessionMeta, error)
	Search(ctx context.Context, q SessionQuery) ([]SessionHit, error)
	Delete(ctx context.Context, id string) error
}

// SessionQuery filters a List or Search.
type SessionQuery struct {
	Text   string
	Parent string
	Since  time.Time
	Until  time.Time
	Limit  int
	Offset int
}

// SessionMeta is a session without its transcript.
type SessionMeta struct {
	ID       string    `json:"id"`
	Parent   string    `json:"parent,omitempty"`
	Title    string    `json:"title,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Messages int       `json:"messages"`
}

// SessionHit is one search result.
type SessionHit struct {
	Meta    SessionMeta
	Index   int
	Snippet string
}

// Lineage walks Parent back from id, newest first and including id itself.
func Lineage(ctx context.Context, store SessionStore, id string) ([]SessionMeta, error) {
	var out []SessionMeta
	seen := make(map[string]bool)
	for id != "" && !seen[id] {
		seen[id] = true
		s, err := store.Load(ctx, id)
		if err != nil {
			if errors.Is(err, ErrSessionNotFound) {
				return out, nil
			}
			return out, err
		}
		m := s.Meta()
		out = append(out, m)
		id = m.Parent
	}
	return out, nil
}
