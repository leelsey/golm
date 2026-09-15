// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/leelsey/golm"
)

type auditLog struct {
	mu sync.Mutex
	f  *os.File

	enc *json.Encoder
}

type auditEntry struct {
	Time     time.Time       `json:"time"`
	Agent    string          `json:"agent,omitempty"`
	Step     int             `json:"step,omitempty"`
	Tool     string          `json:"tool"`
	CallID   string          `json:"call_id,omitempty"`
	Decision string          `json:"decision"`
	Allowed  bool            `json:"allowed"`
	Reason   string          `json:"reason,omitempty"`
	Traits   []string        `json:"traits,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

func newAuditLog(path string) (*auditLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("golm: open audit log %s: %w", path, err)
	}
	return &auditLog{f: f, enc: json.NewEncoder(f)}, nil
}

// Record writes one decision.
func (a *auditLog) Record(req golm.ToolRequest, d golm.Decision, err error) {
	e := auditEntry{
		Time: time.Now().UTC(), Agent: req.Agent, Step: req.Step,
		Tool: req.Call.Name, CallID: req.Call.ID,
		Decision: string(d), Allowed: err == nil, Input: req.Call.Input,
	}
	if err != nil {
		e.Reason = err.Error()
	}
	if tr, ok := golm.TraitsOf(req.Tool); ok {
		if names := traitList(tr); names != "" {
			e.Traits = splitTraits(names)
		}
	} else {
		e.Traits = []string{"undeclared"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		return
	}
	if werr := a.enc.Encode(e); werr != nil {
		fmt.Fprintf(os.Stderr, "golm: audit write failed, no longer recording: %v\n", werr)
		a.f.Close()
		a.f = nil
	}
}

func splitTraits(s string) []string {
	var out []string
	for _, part := range splitComma(s) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, trimSpace(s[start:i]))
			start = i + 1
		}
	}
	return append(out, trimSpace(s[start:]))
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// Close flushes and closes the record.
func (a *auditLog) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		return nil
	}
	err := a.f.Close()
	a.f = nil
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
