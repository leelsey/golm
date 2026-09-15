// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/leelsey/golm"
)

var _ golm.SessionStore = (*Files)(nil)

// ErrInvalidID is returned for an id that is not safe as a filename.
var ErrInvalidID = fmt.Errorf("%w: not safe as a filename", golm.ErrInvalidSessionID)

var errIDMismatch = errors.New("sessionstore: session file holds another id")

// ErrTooLarge is returned for a session file beyond MaxBytes.
var ErrTooLarge = errors.New("sessionstore: session file exceeds MaxBytes")

// DefaultMaxBytes bounds a session file's decode when MaxBytes is unset.
const DefaultMaxBytes int64 = 32 << 20

const maxIDLen = 128

const fileExt = ".json"

// Files stores one JSON file per session in Dir, written 0600 through a temp file.
type Files struct {
	Dir string

	MaxBytes int64

	OnError func(id string, err error)
}

func (s *Files) skip(id string, err error) {
	if s.OnError == nil {
		return
	}
	defer func() { _ = recover() }()
	s.OnError(id, err)
}

// NewFiles returns a store over dir.
func NewFiles(dir string) *Files { return &Files{Dir: dir} }

func validID(id string) bool {
	if id == "" || len(id) > maxIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func (s *Files) path(id string) (string, error) {
	if !validID(id) {
		return "", fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	return filepath.Join(s.Dir, id+fileExt), nil
}

func (s *Files) limit() int64 {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return DefaultMaxBytes
}

func (s *Files) read(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, golm.ErrSessionNotFound
		}
		return nil, err
	}
	defer f.Close()
	max := s.limit()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%w: %s", ErrTooLarge, filepath.Base(path))
	}
	return b, nil
}

// Save writes a snapshot of sess to Dir/<id>.json.
func (s *Files) Save(ctx context.Context, sess *golm.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d := sess.Snapshot()
	path, err := s.path(d.ID)
	if err != nil {
		return err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}

	f, err := os.CreateTemp(s.Dir, ".golm-session-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}

	if fi, serr := os.Stat(path); serr == nil {
		s.writeIndex(d.ID, buildIndex(d, fi.Size(), fi.ModTime()))
	}
	return nil
}

func (s *Files) data(id string) (golm.SessionData, error) {
	path, err := s.path(id)
	if err != nil {
		return golm.SessionData{}, err
	}
	b, err := s.read(path)
	if err != nil {
		return golm.SessionData{}, err
	}
	var d golm.SessionData
	if err := json.Unmarshal(b, &d); err != nil {
		return golm.SessionData{}, fmt.Errorf("sessionstore: parse %s: %w", id+fileExt, err)
	}
	if d.ID != id {
		return golm.SessionData{}, fmt.Errorf("%w: %s holds id %q", errIDMismatch, id+fileExt, d.ID)
	}
	return d, nil
}

// Load rebuilds the session stored under id.
func (s *Files) Load(ctx context.Context, id string) (*golm.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d, err := s.data(id)
	if err != nil {
		return nil, err
	}
	return d.Session(), nil
}

func (s *Files) ids() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		id, ok := strings.CutSuffix(name, fileExt)
		if !ok || !validID(id) {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// List returns matching sessions, newest first.
func (s *Files) List(ctx context.Context, q golm.SessionQuery) ([]golm.SessionMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids, err := s.ids()
	if err != nil {
		return nil, err
	}
	out := make([]golm.SessionMeta, 0, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		e, err := s.entryFor(id)
		if err != nil {
			if errors.Is(err, golm.ErrSessionNotFound) {
				continue
			}
			if errors.Is(err, errIDMismatch) {
				return nil, err
			}
			s.skip(id, err)
			continue
		}
		if m := e.meta(); selects(m, q) {
			out = append(out, m)
		}
	}
	sortMetas(out)
	lo, hi := pageBounds(len(out), q)
	return out[lo:hi], nil
}

// Search returns matching sessions newest first, one hit each.
func (s *Files) Search(ctx context.Context, q golm.SessionQuery) ([]golm.SessionHit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids, err := s.ids()
	if err != nil {
		return nil, err
	}
	want := terms(q.Text)
	var out []golm.SessionHit
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		e, err := s.entryFor(id)
		if err != nil {
			if errors.Is(err, golm.ErrSessionNotFound) {
				continue
			}
			if errors.Is(err, errIDMismatch) {
				return nil, err
			}
			s.skip(id, err)
			continue
		}
		if !selects(e.meta(), q) || !e.mayContain(want) {
			continue
		}
		d, err := s.data(id)
		if err != nil {
			if errors.Is(err, golm.ErrSessionNotFound) {
				continue
			}
			if errors.Is(err, errIDMismatch) {
				return nil, err
			}
			s.skip(id, err)
			continue
		}
		if !selects(metaOf(d), q) {
			continue
		}
		if h, ok := hitFor(d, want); ok {
			out = append(out, h)
		}
	}
	sortHits(out)
	lo, hi := pageBounds(len(out), q)
	return out[lo:hi], nil
}

// Delete removes the session stored under id.
func (s *Files) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return golm.ErrSessionNotFound
		}
		return err
	}
	s.removeIndex(id)
	return nil
}
