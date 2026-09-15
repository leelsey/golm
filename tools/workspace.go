// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package tools is the built-in tool set.
package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/leelsey/golm"
)

const (
	maxReadBytes       = 256 << 10
	maxWriteBytes      = 4 << 20
	maxListEntries     = 1000
	maxSearchHits      = 100
	maxSearchFileBytes = 2 << 20
	maxWalkFiles       = 20000
	sniffBytes         = 8000

	maxSearchLineBytes = 300
)

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

// ErrOutsideWorkspace is returned for a path that does not resolve inside the workspace.
var ErrOutsideWorkspace = errors.New("tools: path is outside the workspace")

// Workspace is a directory the filesystem tools may touch.
type Workspace struct {
	root *os.Root
	name string

	abs  string
	real string

	ReadOnly bool
}

// Open confines the filesystem tools to dir.
func Open(dir string) (*Workspace, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("tools: open workspace %s: %w", dir, err)
	}
	w := &Workspace{root: r, name: dir}
	if a, err := filepath.Abs(dir); err == nil {
		w.abs = a
		w.real = a
		if e, err := filepath.EvalSymlinks(a); err == nil {
			w.real = e
		}
	}
	return w, nil
}

// Dir is the workspace root as it was given.
func (w *Workspace) Dir() string { return w.name }

// Close releases the directory handle.
func (w *Workspace) Close() error {
	if w == nil || w.root == nil {
		return nil
	}
	return w.root.Close()
}

func (w *Workspace) resolve(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	if filepath.IsAbs(p) {
		rel, ok := w.relative(filepath.Clean(p))
		if !ok {
			return "", fmt.Errorf("%w: %s (paths are relative to the workspace root)", ErrOutsideWorkspace, p)
		}
		p = rel
	}
	c := path.Clean(filepath.ToSlash(p))
	if c == "." {
		return ".", nil
	}
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("%w: %q", ErrOutsideWorkspace, p)
	}
	return c, nil
}

func (w *Workspace) relative(p string) (string, bool) {
	for _, base := range []string{w.abs, w.real} {
		if base == "" {
			continue
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return rel, true
	}
	return "", false
}

func wrapPathErr(p string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no such file: %s", p)
	}
	if strings.Contains(err.Error(), "escapes from parent") {
		return fmt.Errorf("%w: %s", ErrOutsideWorkspace, p)
	}
	return err
}

func isText(b []byte) bool {
	head := b
	if len(head) > sniffBytes {
		head = head[:sniffBytes]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	return utf8.Valid(head)
}

// Tools returns the filesystem tools this workspace offers.
func (w *Workspace) Tools() []golm.Tool {
	out := []golm.Tool{w.readFile(), w.listDir(), w.search()}
	if !w.ReadOnly {
		out = append(out, w.writeFile(), w.editFile())
	}
	return out
}

type readArgs struct {
	Path string `json:"path" jsonschema:"the file to read, relative to the workspace root"`
}

func (w *Workspace) readFile() golm.Tool {
	t := golm.NewTypedTool("read_file",
		"Read a text file from the workspace. Paths are relative to the workspace root.",
		func(_ context.Context, in readArgs) (string, error) {
			p, err := w.resolve(in.Path)
			if err != nil {
				return "", err
			}
			f, err := w.root.Open(p)
			if err != nil {
				return "", wrapPathErr(in.Path, err)
			}
			defer f.Close()
			st, err := f.Stat()
			if err != nil {
				return "", err
			}
			if st.IsDir() {
				return "", fmt.Errorf("%s is a directory; use list_dir", in.Path)
			}
			b := make([]byte, maxReadBytes+1)
			n, err := io.ReadFull(f, b)
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return "", err
			}
			b = b[:n]
			if !isText(b) {
				return "", fmt.Errorf("%s is not a text file (%d bytes)", in.Path, st.Size())
			}
			if n > maxReadBytes {
				return truncRunes(string(b), maxReadBytes) +
					fmt.Sprintf("\n\n[truncated: %s is %d bytes]", in.Path, st.Size()), nil
			}
			return string(b), nil
		})
	return golm.WithTraits(t, golm.ToolTraits{ReadOnly: true, Filesystem: true})
}

type writeArgs struct {
	Path    string `json:"path" jsonschema:"the file to create, relative to the workspace root"`
	Content string `json:"content" jsonschema:"the full contents of the new file"`
}

func (w *Workspace) writeFile() golm.Tool {
	t := golm.NewTypedTool("write_file",
		"Create a new file in the workspace. Fails if the file already exists — use edit_file to change one.",
		func(_ context.Context, in writeArgs) (string, error) {
			p, err := w.resolve(in.Path)
			if err != nil {
				return "", err
			}
			if len(in.Content) > maxWriteBytes {
				return "", fmt.Errorf("content is %d bytes, over the %d byte limit", len(in.Content), maxWriteBytes)
			}
			if _, err := w.root.Lstat(p); err == nil {
				return "", fmt.Errorf("%s already exists; use edit_file to change it", in.Path)
			}
			if dir := path.Dir(p); dir != "." {
				if err := w.root.MkdirAll(dir, 0o700); err != nil {
					return "", wrapPathErr(in.Path, err)
				}
			}
			if err := w.atomicWrite(p, []byte(in.Content)); err != nil {
				return "", wrapPathErr(in.Path, err)
			}
			return fmt.Sprintf("wrote %s (%d bytes)", in.Path, len(in.Content)), nil
		})
	return golm.WithTraits(t, golm.ToolTraits{Filesystem: true})
}

type editArgs struct {
	Path string `json:"path" jsonschema:"the file to change, relative to the workspace root"`
	Old  string `json:"old" jsonschema:"the exact text to replace; must appear exactly once in the file"`
	New  string `json:"new" jsonschema:"the text to put in its place"`
}

func (w *Workspace) editFile() golm.Tool {
	t := golm.NewTypedTool("edit_file",
		"Replace an exact piece of text in a workspace file. The old text must appear exactly once; include surrounding lines to make it unique.",
		func(_ context.Context, in editArgs) (string, error) {
			p, err := w.resolve(in.Path)
			if err != nil {
				return "", err
			}
			if in.Old == "" {
				return "", fmt.Errorf("old must not be empty; use write_file to create a file")
			}
			if in.Old == in.New {
				return "", fmt.Errorf("old and new are identical; nothing to do")
			}
			b, err := w.root.ReadFile(p)
			if err != nil {
				return "", wrapPathErr(in.Path, err)
			}
			if !isText(b) {
				return "", fmt.Errorf("%s is not a text file", in.Path)
			}
			switch n := strings.Count(string(b), in.Old); n {
			case 0:
				return "", fmt.Errorf("the text to replace is not in %s; read it again", in.Path)
			case 1:
			default:
				return "", fmt.Errorf("the text to replace appears %d times in %s; include more surrounding context so it matches once", n, in.Path)
			}
			out := strings.Replace(string(b), in.Old, in.New, 1)
			if len(out) > maxWriteBytes {
				return "", fmt.Errorf("the result would be %d bytes, over the %d byte limit", len(out), maxWriteBytes)
			}
			if err := w.atomicWrite(p, []byte(out)); err != nil {
				return "", wrapPathErr(in.Path, err)
			}
			return fmt.Sprintf("edited %s (%+d bytes)", in.Path, len(out)-len(b)), nil
		})
	return golm.WithTraits(t, golm.ToolTraits{Filesystem: true})
}

func (w *Workspace) atomicWrite(p string, b []byte) error {
	dir := path.Dir(p)
	tmp := path.Join(dir, ".golm-"+rand.Text()+".tmp")
	f, err := w.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}

	if st, err := w.root.Stat(p); err == nil {
		if err := f.Chmod(st.Mode().Perm()); err != nil {
			f.Close()
			w.root.Remove(tmp)
			return err
		}
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		w.root.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		w.root.Remove(tmp)
		return err
	}
	if err := w.root.Rename(tmp, p); err != nil {
		w.root.Remove(tmp)
		return err
	}
	return nil
}

type listArgs struct {
	Path string `json:"path,omitempty" jsonschema:"the directory to list; omit for the workspace root"`
}

func (w *Workspace) listDir() golm.Tool {
	t := golm.NewTypedTool("list_dir",
		"List the entries of a directory in the workspace.",
		func(_ context.Context, in listArgs) (string, error) {
			p := in.Path
			if strings.TrimSpace(p) == "" {
				p = "."
			}
			c, err := w.resolve(p)
			if err != nil {
				return "", err
			}
			f, err := w.root.Open(c)
			if err != nil {
				return "", wrapPathErr(p, err)
			}
			defer f.Close()
			entries, err := f.ReadDir(maxListEntries + 1)
			if err != nil && !errors.Is(err, io.EOF) {
				return "", wrapPathErr(p, err)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			var b strings.Builder
			shown := entries
			if len(shown) > maxListEntries {
				shown = shown[:maxListEntries]
			}
			for _, e := range shown {
				if e.IsDir() {
					fmt.Fprintf(&b, "%s/\n", e.Name())
					continue
				}
				size := int64(-1)
				if fi, err := e.Info(); err == nil {
					size = fi.Size()
				}
				fmt.Fprintf(&b, "%s\t%d\n", e.Name(), size)
			}
			if len(entries) > maxListEntries {
				fmt.Fprintf(&b, "[truncated at %d entries]\n", maxListEntries)
			}
			if b.Len() == 0 {
				return "(empty directory)", nil
			}
			return b.String(), nil
		})
	return golm.WithTraits(t, golm.ToolTraits{ReadOnly: true, Filesystem: true})
}

type searchArgs struct {
	Query string `json:"query" jsonschema:"text to find, or a regular expression when regex is true"`
	Path  string `json:"path,omitempty" jsonschema:"directory to search under; omit for the whole workspace"`
	Regex bool   `json:"regex,omitempty" jsonschema:"treat query as a regular expression"`
}

func (w *Workspace) search() golm.Tool {
	t := golm.NewTypedTool("search",
		"Search the workspace for a piece of text, returning matching lines with their file and line number.",
		func(ctx context.Context, in searchArgs) (string, error) {
			if strings.TrimSpace(in.Query) == "" {
				return "", fmt.Errorf("query must not be empty")
			}
			base := in.Path
			if strings.TrimSpace(base) == "" {
				base = "."
			}
			c, err := w.resolve(base)
			if err != nil {
				return "", err
			}
			var re *regexp.Regexp
			if in.Regex {
				re, err = regexp.Compile(in.Query)
				if err != nil {
					return "", fmt.Errorf("invalid regular expression: %w", err)
				}
			}
			var b strings.Builder
			hits, walked := 0, 0
			err = fs.WalkDir(w.root.FS(), c, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if d.IsDir() {
					if name := path.Base(p); p != c && (name == ".git" || name == "node_modules" || name == "vendor") {
						return fs.SkipDir
					}
					return nil
				}
				if walked++; walked > maxWalkFiles {
					return fs.SkipAll
				}
				if fi, err := d.Info(); err == nil && fi.Size() > maxSearchFileBytes {
					return nil
				}
				data, err := w.root.ReadFile(p)
				if err != nil || !isText(data) {
					return nil
				}
				for i, line := range strings.Split(string(data), "\n") {
					var found bool
					if re != nil {
						found = re.MatchString(line)
					} else {
						found = strings.Contains(line, in.Query)
					}
					if !found {
						continue
					}
					if hits++; hits > maxSearchHits {
						return fs.SkipAll
					}
					if len(line) > maxSearchLineBytes {
						line = truncRunes(line, maxSearchLineBytes) + "…"
					}
					fmt.Fprintf(&b, "%s:%d: %s\n", p, i+1, strings.TrimRight(line, "\r"))
				}
				return nil
			})
			if err != nil && !errors.Is(err, fs.SkipAll) {
				return "", err
			}
			if hits == 0 {
				return "no matches", nil
			}
			if hits > maxSearchHits {
				fmt.Fprintf(&b, "[truncated at %d matches]\n", maxSearchHits)
			}
			return b.String(), nil
		})
	return golm.WithTraits(t, golm.ToolTraits{ReadOnly: true, Filesystem: true})
}
