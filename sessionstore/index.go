// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package sessionstore

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/leelsey/golm"
)

const indexExt = ".idx"

const indexVersion = 1

const maxIndexTokens = 4096

const maxTokenLen = 64

type indexEntry struct {
	Version int `json:"v"`

	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`

	ID       string    `json:"id"`
	Parent   string    `json:"parent,omitempty"`
	Title    string    `json:"title,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Messages int       `json:"messages"`

	Tokens []string `json:"tokens,omitempty"`

	Partial bool `json:"partial,omitempty"`
}

func (s *Files) indexPath(id string) string {
	return filepath.Join(s.Dir, id+indexExt)
}

func (e *indexEntry) meta() golm.SessionMeta {
	return golm.SessionMeta{
		ID: e.ID, Parent: e.Parent, Title: e.Title,
		Created: e.Created, Updated: e.Updated, Messages: e.Messages,
	}
}

func (e *indexEntry) mayContain(terms []string) bool {
	if e.Partial {
		return true
	}
	for _, want := range terms {
		if !indexable(want) {
			continue
		}
		var found bool
		for _, tok := range e.Tokens {
			if strings.Contains(tok, want) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func indexable(term string) bool {
	if term == "" || len(term) > maxTokenLen {
		return false
	}
	for _, r := range term {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func buildIndex(d golm.SessionData, size int64, mod time.Time) indexEntry {
	e := indexEntry{
		Version: indexVersion, Size: size, ModTime: mod,
		ID: d.ID, Parent: d.Parent, Title: d.Title,
		Created: d.Created, Updated: d.Updated, Messages: len(d.History),
	}
	seen := make(map[string]struct{}, 256)
	add := func(text string) {
		for _, tok := range tokenise(text) {
			if len(seen) >= maxIndexTokens {
				e.Partial = true
				return
			}
			seen[tok] = struct{}{}
		}
	}
	add(d.Title)
	for _, m := range d.History {
		add(m.Text())
	}
	e.Tokens = make([]string, 0, len(seen))
	for tok := range seen {
		e.Tokens = append(e.Tokens, tok)
	}
	sort.Strings(e.Tokens)
	return e
}

func tokenise(text string) []string {
	if text == "" {
		return nil
	}
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := fields[:0]
	for _, f := range fields {
		if len(f) > maxTokenLen {
			f = truncRunes(f, maxTokenLen)
		}
		out = append(out, f)
	}
	return out
}

func truncRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func (s *Files) readIndex(id string, fi os.FileInfo) (indexEntry, bool) {
	b, err := os.ReadFile(s.indexPath(id))
	if err != nil {
		return indexEntry{}, false
	}
	var e indexEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return indexEntry{}, false
	}
	if e.Version != indexVersion || e.ID != id {
		return indexEntry{}, false
	}
	if fi != nil && (e.Size != fi.Size() || !e.ModTime.Equal(fi.ModTime())) {
		return indexEntry{}, false
	}
	return e, true
}

func (s *Files) writeIndex(id string, e indexEntry) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.CreateTemp(s.Dir, ".golm-idx-*.tmp")
	if err != nil {
		return
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, s.indexPath(id)); err != nil {
		os.Remove(tmp)
	}
}

func (s *Files) removeIndex(id string) {
	if err := os.Remove(s.indexPath(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.skip(id, err)
	}
}

func (s *Files) entryFor(id string) (indexEntry, error) {
	path, err := s.path(id)
	if err != nil {
		return indexEntry{}, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return indexEntry{}, golm.ErrSessionNotFound
		}
		return indexEntry{}, err
	}
	if e, ok := s.readIndex(id, fi); ok {
		return e, nil
	}

	d, err := s.data(id)
	if err != nil {
		return indexEntry{}, err
	}
	e := buildIndex(d, fi.Size(), fi.ModTime())
	s.writeIndex(id, e)
	return e, nil
}

// Reindex rebuilds every sidecar, returning how many it wrote.
func (s *Files) Reindex() (int, error) {
	ids, err := s.ids()
	if err != nil {
		return 0, err
	}
	var n int
	for _, id := range ids {
		path, err := s.path(id)
		if err != nil {
			continue
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		d, err := s.data(id)
		if err != nil {
			s.skip(id, err)
			continue
		}
		s.writeIndex(id, buildIndex(d, fi.Size(), fi.ModTime()))
		n++
	}
	return n, nil
}
