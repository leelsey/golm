// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/leelsey/golm"
)

const manifestMaxBytes = 4 << 20

// Remote is a configured MCP server whose tools are available to a model without the server running.
type Remote struct {
	Name    string
	Command string
	Args    []string

	CacheDir string

	Stderr io.Writer

	Env     []string
	Inherit []string

	mu     sync.Mutex
	client *Client
	closed bool
	log    *tailBuffer

	tools map[string]bool
}

// ErrRemoteClosed is a call on a Remote that has been closed.
var ErrRemoteClosed = errors.New("mcp: remote is closed")

type manifest struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Tools   []Tool   `json:"tools"`
}

func (s *Remote) fingerprint() string {
	h := sha256.New()
	h.Write([]byte(s.Command))
	for _, a := range s.Args {
		h.Write([]byte{0})
		h.Write([]byte(a))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func (s *Remote) manifestPath() string {
	if s.CacheDir == "" {
		return ""
	}
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s.Name)
	if safe == "" {
		safe = "server"
	}
	return filepath.Join(s.CacheDir, safe+"-"+s.fingerprint()+".json")
}

func (s *Remote) readManifest() (*manifest, bool) {
	p := s.manifestPath()
	if p == "" {
		return nil, false
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var m manifest
	dec := json.NewDecoder(&boundedReader{r: f, left: manifestMaxBytes})
	if err := dec.Decode(&m); err != nil {
		return nil, false
	}

	if m.Command != s.Command || len(m.Args) != len(s.Args) {
		return nil, false
	}
	for i := range m.Args {
		if m.Args[i] != s.Args[i] {
			return nil, false
		}
	}
	return &m, len(m.Tools) > 0
}

func (s *Remote) writeManifest(list []Tool) error {
	p := s.manifestPath()
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(manifest{Command: s.Command, Args: s.Args, Tools: list})
	if err != nil {
		return err
	}

	f, err := os.CreateTemp(filepath.Dir(p), ".mcp-manifest-*.tmp")
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
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Tools returns the server's tools.
func (s *Remote) Tools(ctx context.Context) ([]golm.Tool, error) {
	if m, ok := s.readManifest(); ok {
		return s.wrap(m.Tools), nil
	}
	list, err := s.discover(ctx)
	if err != nil {
		return nil, err
	}
	return s.wrap(list), nil
}

func (s *Remote) discover(ctx context.Context) ([]Tool, error) {
	c, err := s.connect(ctx)
	if err != nil {
		return nil, err
	}
	list, err := c.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp: list tools from %q: %w", s.Name, err)
	}
	s.remember(list)

	_ = s.writeManifest(list)
	return list, nil
}

func (s *Remote) remember(list []Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = make(map[string]bool, len(list))
	for _, t := range list {
		s.tools[t.Name] = true
	}
}

func (s *Remote) connect(ctx context.Context) (*Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("%w: %q", ErrRemoteClosed, s.Name)
	}
	if s.client != nil {
		if s.client.Live() {
			return s.client, nil
		}

		_ = s.client.Close()
		s.client, s.tools = nil, nil
	}
	if s.log == nil {
		s.log = &tailBuffer{}
	}
	errOut := s.Stderr
	if errOut == nil {
		errOut = s.log
	}
	c, err := Dialer{Stderr: errOut, Env: s.Env, Inherit: s.Inherit}.Dial(ctx, s.Command, s.Args...)
	if err != nil {
		return nil, s.wrapErr("dial", err)
	}
	if err := c.Initialize(ctx, "golm"); err != nil {
		_ = c.Close()
		return nil, s.wrapErr("initialise", err)
	}
	s.client = c
	return c, nil
}

func (s *Remote) wrapErr(what string, err error) error {
	if tail := s.log.String(); tail != "" {
		return fmt.Errorf("mcp: %s %q: %w: %s", what, s.Name, err, tail)
	}
	return fmt.Errorf("mcp: %s %q: %w", what, s.Name, err)
}

func (s *Remote) live(ctx context.Context) (*Client, error) {
	c, err := s.connect(ctx)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	known := s.tools != nil
	s.mu.Unlock()
	if !known {
		list, err := c.ListTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("mcp: list tools from %q: %w", s.Name, err)
		}
		s.remember(list)
		_ = s.writeManifest(list)
	}
	return c, nil
}

func (s *Remote) wrap(list []Tool) []golm.Tool {
	out := make([]golm.Tool, 0, len(list))
	for _, t := range list {
		name := t.Name
		out = append(out, golm.ToolFunc{
			NameVal:        name,
			DescriptionVal: t.Description,
			SchemaVal:      t.InputSchema,
			Fn: func(ctx context.Context, input json.RawMessage) ([]golm.ToolContent, error) {
				c, err := s.live(ctx)
				if err != nil {
					return nil, err
				}
				s.mu.Lock()
				known := s.tools[name]
				s.mu.Unlock()
				if !known {
					return nil, fmt.Errorf("mcp: server %q no longer offers tool %q", s.Name, name)
				}
				text, isErr, err := c.CallTool(ctx, name, input)
				if err != nil {
					return nil, err
				}
				if isErr {
					return nil, errors.New(text)
				}
				return golm.ToolText(text), nil
			},
		})
	}
	return out
}

// Connected reports whether the subprocess has been started.
func (s *Remote) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client != nil
}

// Close shuts the connection if one was ever opened.
func (s *Remote) Close() error {
	s.mu.Lock()
	c := s.client
	s.client, s.closed = nil, true
	s.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.Close()
}

type boundedReader struct {
	r    interface{ Read([]byte) (int, error) }
	left int64
}

func (b *boundedReader) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, fs.ErrInvalid
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.r.Read(p)
	b.left -= int64(n)
	return n, err
}

const tailLogBytes = 8 << 10

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailLogBytes {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-tailLogBytes:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

var _ io.Writer = (*tailBuffer)(nil)
