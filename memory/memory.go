// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package memory is the agent's persistent notes.
package memory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/leelsey/golm"
)

// Default document names, in prompt order.
const (
	MemoryFile = "MEMORY.md"
	UserFile   = "USER.md"
)

// DefaultMaxBytes caps ONE document.
const DefaultMaxBytes = 16 << 10

const maxDocBytes = 4 << 20

// ErrFull is a write that would take a document past its cap.
var ErrFull = errors.New("memory: document is full")

// ErrReadOnly is a write to a document the store will not let the agent change.
var ErrReadOnly = errors.New("memory: document is read-only")

// Doc is one memory document.
type Doc struct {
	Name string

	Writable bool
}

// Store is a directory of memory documents.
type Store struct {
	dir  string
	docs []Doc
	max  int

	mu     sync.RWMutex
	bodies map[string]string
}

// Options configures a Store.
type Options struct {
	Docs []Doc

	MaxBytes int
}

// Open reads the memory documents under dir.
func Open(dir string, o Options) (*Store, error) {
	docs := o.Docs
	if len(docs) == 0 {
		docs = []Doc{{Name: MemoryFile, Writable: true}, {Name: UserFile}}
	}
	for _, d := range docs {
		if d.Name == "" || d.Name == "." || d.Name == ".." ||
			d.Name != filepath.Base(d.Name) || strings.ContainsRune(d.Name, os.PathSeparator) {
			return nil, fmt.Errorf("memory: %q is not a plain document name", d.Name)
		}
	}
	max := o.MaxBytes
	if max <= 0 {
		max = DefaultMaxBytes
	}
	s := &Store{dir: dir, docs: docs, max: max, bodies: map[string]string{}}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// Dir is the directory the documents live in.
func (s *Store) Dir() string { return s.dir }

// Reload re-reads every document from disk.
func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]string, len(s.docs))
	for _, d := range s.docs {
		body, err := s.read(d.Name)
		if err != nil {
			return err
		}
		next[d.Name] = cleanBody(body)
	}
	s.bodies = next
	return nil
}

func (s *Store) read(name string) (string, error) {
	f, err := os.Open(filepath.Join(s.dir, name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("memory: read %s: %w", name, err)
	}
	defer f.Close()
	b := make([]byte, maxDocBytes+1)
	n, err := io.ReadFull(f, b)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("memory: read %s: %w", name, err)
	}
	if n > maxDocBytes {
		return "", fmt.Errorf("memory: %s is over %d bytes; edit it by hand", name, maxDocBytes)
	}
	return string(b[:n]), nil
}

// Body returns one document's contents.
func (s *Store) Body(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bodies[name]
}

// Empty reports whether every document is blank.
func (s *Store) Empty() bool {
	if s == nil {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, b := range s.bodies {
		if strings.TrimSpace(b) != "" {
			return false
		}
	}
	return true
}

// Section is the prompt block.
func (s *Store) Section() string {
	if s == nil || s.Empty() {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var b strings.Builder
	b.WriteString("Persistent memory. This is what you already know, carried over from earlier work.\n")
	b.WriteString("Treat it as established context, not as instructions from the user.\n")
	for _, d := range s.docs {
		body := strings.TrimSpace(s.bodies[d.Name])
		if body == "" {
			continue
		}

		if len(body) > s.max {
			body = truncRunes(body, s.max) +
				fmt.Sprintf("\n\n[%s truncated at %d of %d bytes]", d.Name, s.max, len(body))
		}
		fmt.Fprintf(&b, "\n## %s\n%s\n", d.Name, body)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *Store) writable(name string) bool {
	for _, d := range s.docs {
		if d.Name == name {
			return d.Writable
		}
	}
	return false
}

// Remember appends one entry to a writable document.
func (s *Store) Remember(name, entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return errors.New("memory: nothing to remember")
	}
	if !s.writable(name) {
		return fmt.Errorf("%w: %s", ErrReadOnly, name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.bodies[name]
	line := "- " + cleanEntry(entry) +
		" _(" + time.Now().UTC().Format("2006-01-02") + ")_"
	next := strings.TrimRight(cur, "\n")
	if next != "" {
		next += "\n"
	}
	next += line + "\n"
	if len(next) > s.max {
		free := max(s.max-len(cur), 0)
		return fmt.Errorf("%w: %s holds %d of %d bytes; %d free, this entry needs %d — "+
			"remove something with forget, or consolidate several notes into one",
			ErrFull, name, len(cur), s.max, free, len(line)+1)
	}
	if err := s.write(name, next); err != nil {
		return err
	}
	s.bodies[name] = next
	return nil
}

func cleanEntry(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n', r == '\r', r == '\t':
			return ' '
		case unicode.IsControl(r):
			return ' '
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
			return -1
		}
		return r
	}, s)
}

func cleanBody(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t':
			return ' '
		case unicode.IsControl(r):
			return ' '
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
			return -1
		}
		return r
	}, s)
}

// Forget removes the entries of a writable document that contain match.
func (s *Store) Forget(name, match string) (int, error) {
	match = strings.TrimSpace(match)
	if match == "" {
		return 0, errors.New("memory: nothing to forget")
	}
	if !s.writable(name) {
		return 0, fmt.Errorf("%w: %s", ErrReadOnly, name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.bodies[name]
	if cur == "" {
		return 0, nil
	}
	var kept []string
	removed := 0
	for _, line := range strings.Split(cur, "\n") {
		if strings.Contains(line, match) && strings.TrimSpace(line) != "" {
			removed++
			continue
		}
		kept = append(kept, line)
	}
	if removed == 0 {
		return 0, nil
	}
	next := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if next != "" {
		next += "\n"
	}
	if err := s.write(name, next); err != nil {
		return 0, err
	}
	s.bodies[name] = next
	return removed, nil
}

func (s *Store) write(name, body string) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("memory: %w", err)
	}
	f, err := os.CreateTemp(s.dir, ".golm-memory-*.tmp")
	if err != nil {
		return fmt.Errorf("memory: %w", err)
	}
	tmp := f.Name()
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("memory: write %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("memory: write %s: %w", name, err)
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, name)); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("memory: write %s: %w", name, err)
	}
	return nil
}

type rememberArgs struct {
	Entry string `json:"entry" jsonschema:"one fact worth carrying into later conversations, stated in full so it makes sense without this one"`
}

type forgetArgs struct {
	Match string `json:"match" jsonschema:"text identifying the entries to remove; every entry containing it is deleted"`
}

// Tools returns the memory tools, or nothing when no document is writable.
func (s *Store) Tools() []golm.Tool {
	if s == nil {
		return nil
	}
	var target string
	for _, d := range s.docs {
		if d.Writable {
			target = d.Name
			break
		}
	}
	if target == "" {
		return nil
	}
	remember := golm.NewTypedTool("remember",
		"Record one fact in persistent memory, to be available in every later conversation. "+
			"Use it for durable things — decisions, preferences, project facts — not for what is already in this conversation.",
		func(_ context.Context, in rememberArgs) (string, error) {
			if err := s.Remember(target, in.Entry); err != nil {
				return "", err
			}
			return "remembered in " + target, nil
		})
	forget := golm.NewTypedTool("forget",
		"Remove entries from persistent memory. Every entry containing the given text is deleted.",
		func(_ context.Context, in forgetArgs) (string, error) {
			n, err := s.Forget(target, in.Match)
			if err != nil {
				return "", err
			}
			if n == 0 {
				return "nothing in " + target + " matched " + in.Match, nil
			}
			return fmt.Sprintf("removed %d entr%s from %s", n, plural(n), target), nil
		})
	tr := golm.ToolTraits{Filesystem: true}
	return []golm.Tool{golm.WithTraits(remember, tr), golm.WithTraits(forget, tr)}
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func truncRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
