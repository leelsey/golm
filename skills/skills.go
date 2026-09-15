// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package skills loads instruction bundles from disk.
package skills

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/leelsey/golm"
)

// File is the document a skill directory must contain.
const File = "SKILL.md"

const maxBodyBytes = 256 << 10

const maxFieldBytes = 1 << 10

const maxDepth = 4

// ErrNotFound is returned by Read for a name no loaded skill has.
var ErrNotFound = errors.New("skills: no such skill")

// Skill is one bundle: what the index says about it.
type Skill struct {
	Name string

	Description string

	Path string
}

// Set is a loaded library, addressable by name.
type Set struct {
	list   []Skill
	byName map[string]Skill
}

// Load reads every skill under the given directories, in order.
func Load(dirs ...string) (*Set, error) {
	s := &Set{byName: map[string]Skill{}}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if err := s.loadDir(dir); err != nil {
			return nil, err
		}
	}
	sort.Slice(s.list, func(i, j int) bool { return s.list[i].Name < s.list[j].Name })
	return s, nil
}

func (s *Set) loadDir(dir string) error {
	root, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if !root.IsDir() {
		return fmt.Errorf("skills: %s is not a directory", dir)
	}
	base := filepath.Clean(dir)
	return filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if depth(base, p) > maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != File {
			return nil
		}
		sk, err := parse(p)
		if err != nil {
			return err
		}

		if _, taken := s.byName[sk.Name]; taken {
			return nil
		}
		s.byName[sk.Name] = sk
		s.list = append(s.list, sk)
		return nil
	})
}

func depth(base, p string) int {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return maxDepth + 1
	}
	if rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

func parse(path string) (Skill, error) {
	f, err := os.Open(path)
	if err != nil {
		return Skill{}, err
	}
	defer f.Close()
	sk := Skill{Path: path, Name: filepath.Base(filepath.Dir(path))}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	if !sc.Scan() {
		return sk, nil
	}
	if strings.TrimSpace(sc.Text()) != "---" {
		return sk, nil
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		value = cleanField(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			if value != "" {
				sk.Name = value
			}
		case "description":
			sk.Description = value
		}
	}
	if err := sc.Err(); err != nil {
		return Skill{}, fmt.Errorf("skills: read %s: %w", path, err)
	}
	return sk, nil
}

func cleanField(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(truncRunes(s, maxFieldBytes))
}

// List returns the loaded skills, by name.
func (s *Set) List() []Skill {
	if s == nil {
		return nil
	}
	out := make([]Skill, len(s.list))
	copy(out, s.list)
	return out
}

// Len is how many skills were loaded.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.list)
}

// Index is the prompt section.
func (s *Set) Index() string {
	if s.Len() == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Available skills. Each is a set of instructions for a particular kind of task.\n")
	b.WriteString("Read one with the read_skill tool before starting work it covers; the descriptions below are all you have until you do.\n\n")
	for _, sk := range s.list {
		if sk.Description == "" {
			fmt.Fprintf(&b, "- %s\n", sk.Name)
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", sk.Name, sk.Description)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Read returns one skill's body, without its front matter.
func (s *Set) Read(name string) (string, error) {
	if s == nil {
		return "", ErrNotFound
	}
	sk, ok := s.byName[name]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	f, err := os.Open(sk.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	b := make([]byte, maxBodyBytes+1)
	n, err := io.ReadFull(f, b)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	body := string(b[:n])
	if n > maxBodyBytes {
		body = truncRunes(body, maxBodyBytes) + "\n\n[skill truncated]"
	}
	return strings.TrimSpace(stripFrontMatter(body)), nil
}

func stripFrontMatter(s string) string {
	rest, ok := strings.CutPrefix(s, "---")
	if !ok {
		return s
	}
	if i := strings.Index(rest, "\n---"); i >= 0 {
		return rest[i+len("\n---"):]
	}
	return s
}

// Tool is how a model reads a body.
func (s *Set) Tool() golm.Tool {
	t := golm.TextTool("read_skill",
		"Read the full instructions for one of the available skills, named exactly as the skills index lists it.",
		"name", "the skill to read",
		func(_ context.Context, name string) (string, error) {
			body, err := s.Read(strings.TrimSpace(name))
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return "", fmt.Errorf("%w; available: %s", err, strings.Join(s.names(), ", "))
				}
				return "", err
			}
			return body, nil
		})
	return golm.WithTraits(t, golm.ToolTraits{ReadOnly: true, Filesystem: true})
}

func (s *Set) names() []string {
	out := make([]string, 0, len(s.list))
	for _, sk := range s.list {
		out = append(out, sk.Name)
	}
	return out
}

func truncRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
