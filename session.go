// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"crypto/rand"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// Session is one conversation.
type Session struct {
	mu      sync.Mutex
	id      string
	parent  string
	title   string
	created time.Time
	updated time.Time

	rev     uint64
	history []Message
	state   map[string]json.RawMessage
	usage   Usage
	running atomic.Bool
}

// NewSession returns an empty Session with a fresh id.
func NewSession() *Session {
	now := time.Now()
	return &Session{id: rand.Text(), created: now, updated: now}
}

func (s *Session) idLocked() string {
	if s.id == "" {
		s.id = rand.Text()
		if s.created.IsZero() {
			s.created = time.Now()
		}
	}
	return s.id
}

// ID returns the session's identity, minting one on first use.
func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idLocked()
}

// Parent is the id of the archive this session was last compacted from, or empty if it never was.
func (s *Session) Parent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parent
}

// Title is the session's human label.
func (s *Session) Title() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title
}

// SetTitle sets the human label.
func (s *Session) SetTitle(title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.title, s.updated = title, time.Now()
}

// Meta returns identity, provenance and the current message count without copying the transcript.
func (s *Session) Meta() SessionMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionMeta{
		ID: s.idLocked(), Parent: s.parent, Title: s.title,
		Created: s.created, Updated: s.updated, Messages: len(s.history),
	}
}

// Len returns the number of messages without copying any of them.
func (s *Session) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.history)
}

func cloneMessage(m Message) Message {
	if len(m.Content) == 0 {
		return m
	}
	out := m
	out.Content = make([]Content, len(m.Content))
	for i, c := range m.Content {
		out.Content[i] = cloneContent(c)
	}
	return out
}

func cloneContent(c Content) Content {
	switch v := c.(type) {
	case Image:
		v.Data = bytesClone(v.Data)
		return v
	case Audio:
		v.Data = bytesClone(v.Data)
		return v
	case ToolUse:
		v.Input = bytesClone(v.Input)
		return v
	case ToolResult:
		if len(v.Content) == 0 {
			return v
		}
		inner := make([]ToolContent, len(v.Content))
		for i, p := range v.Content {
			inner[i], _ = cloneContent(p).(ToolContent)
		}
		v.Content = inner
		return v
	}
	return c
}

func bytesClone[T ~[]byte](b T) T {
	if b == nil {
		return nil
	}
	return append(T(nil), b...)
}

// Append adds messages to the transcript.
func (s *Session) Append(msgs ...Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range msgs {
		s.history = append(s.history, cloneMessage(m))
	}
	s.updated = time.Now()
}

// History returns a deep copy of the transcript.
func (s *Session) History() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneHistory(s.history)
}

func cloneHistory(h []Message) []Message {
	out := make([]Message, len(h))
	for i, m := range h {
		out[i] = cloneMessage(m)
	}
	return out
}

func (s *Session) view() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.history))
	for i, m := range s.history {
		out[i] = m
		if len(m.Content) > 0 {
			out[i].Content = append([]Content(nil), m.Content...)
		}
	}
	return out
}

func userBoundary(h []Message, keepLast int) int {
	start := 0
	if len(h) > keepLast {
		start = len(h) - keepLast
	}
	for start > 0 && h[start].Role != RoleUser {
		start--
	}
	for start < len(h) && h[start].Role != RoleUser {
		start++
	}
	if start == 0 || start >= len(h) {
		return -1
	}
	return start
}

// Trim keeps roughly the last keepLast messages.
func (s *Session) Trim(keepLast int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if keepLast <= 0 {
		s.history, s.rev = nil, s.rev+1
		return
	}
	start := userBoundary(s.history, keepLast)
	if start < 0 {
		return
	}
	s.history = append([]Message(nil), s.history[start:]...)
	s.rev++
}

// DropBefore removes the first n messages from the transcript.
func (s *Session) DropBefore(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 {
		return
	}
	if n >= len(s.history) {
		s.history, s.rev = nil, s.rev+1
		return
	}
	s.history = append([]Message(nil), s.history[n:]...)
	s.rev++
}

func (s *Session) rollback(base int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if base < 0 || len(s.history) != base+1 {
		return
	}
	s.history[base] = Message{}
	s.history = s.history[:base:base]
	s.updated, s.rev = time.Now(), s.rev+1
}

// SetState stores v under key, marshalled at once.
func (s *Session) SetState(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = make(map[string]json.RawMessage)
	}
	s.state[key] = b
	s.updated, s.rev = time.Now(), s.rev+1
	return nil
}

// GetState decodes the state stored under key into T, reporting whether the key was present.
func GetState[T any](s *Session, key string) (T, bool, error) {
	var v T
	s.mu.Lock()
	b, ok := s.state[key]
	s.mu.Unlock()
	if !ok {
		return v, false, nil
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, true, err
	}
	return v, true, nil
}

// DeleteState removes key.
func (s *Session) DeleteState(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.state[key]; !ok {
		return
	}
	delete(s.state, key)
	s.updated, s.rev = time.Now(), s.rev+1
}

// StateKeys returns the keys currently set, in no particular order.
func (s *Session) StateKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.state))
	for k := range s.state {
		out = append(out, k)
	}
	return out
}

// Usage returns the cumulative provider usage billed to this conversation, across every run it has carried.
func (s *Session) Usage() Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage
}

func (s *Session) addUsage(u Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage.Add(u)
}

func (s *Session) acquire() bool { return s.running.CompareAndSwap(false, true) }

func (s *Session) release() { s.running.Store(false) }

// SessionData is the canonical JSON form of a session and the whole of the store contract.
type SessionData struct {
	ID      string                     `json:"id"`
	Parent  string                     `json:"parent,omitempty"`
	Title   string                     `json:"title,omitempty"`
	Created time.Time                  `json:"created"`
	Updated time.Time                  `json:"updated"`
	History []Message                  `json:"history"`
	State   map[string]json.RawMessage `json:"state,omitempty"`
	Usage   Usage                      `json:"usage,omitzero"`
}

// Snapshot returns the session's serialisable form, deep-copied under the lock.
func (s *Session) Snapshot() SessionData {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := SessionData{
		ID: s.idLocked(), Parent: s.parent, Title: s.title,
		Created: s.created, Updated: s.updated,
		History: cloneHistory(s.history), Usage: s.usage,
	}
	if len(s.state) > 0 {
		d.State = make(map[string]json.RawMessage, len(s.state))
		for k, v := range s.state {
			d.State[k] = bytesClone(v)
		}
	}
	return d
}

// Session rebuilds a live Session from d, minting an id if d carries none.
func (d SessionData) Session() *Session {
	s := &Session{
		id: d.ID, parent: d.Parent, title: d.Title,
		created: d.Created, updated: d.Updated,
		history: cloneHistory(d.History), usage: d.Usage,
	}
	if s.id == "" {
		s.id = rand.Text()
	}
	if s.created.IsZero() {
		s.created = time.Now()
	}
	if len(d.State) > 0 {
		s.state = make(map[string]json.RawMessage, len(d.State))
		for k, v := range d.State {
			s.state[k] = bytesClone(v)
		}
	}
	return s
}
