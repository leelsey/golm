// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func ws(t *testing.T) (*Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	w, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { w.Close() })
	return w, dir
}

func call(t *testing.T, tools []golm.Tool, name string, args any) (string, error) {
	t.Helper()
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.Name() != name {
			continue
		}
		out, err := tool.Execute(context.Background(), b)
		if err != nil {
			return "", err
		}
		return (golm.ToolResult{Content: out}).Text(), nil
	}
	t.Fatalf("no tool named %q in %v", name, toolNames(tools))
	return "", nil
}

func toolNames(tools []golm.Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name()
	}
	return out
}

func TestWorkspaceConfinesEveryPath(t *testing.T) {
	w, dir := ws(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	tools := w.Tools()

	for _, p := range []string{"../secret.txt", "../../etc/passwd", "escape", outside} {
		if _, err := call(t, tools, "read_file", readArgs{Path: p}); err == nil {
			t.Errorf("read_file(%q) should be refused", p)
		}
	}

	for _, p := range []string{"inside.txt", "./inside.txt", filepath.Join(dir, "inside.txt")} {
		got, err := call(t, tools, "read_file", readArgs{Path: p})
		if err != nil {
			t.Errorf("read_file(%q): %v", p, err)
		}
		if got != "mine" {
			t.Errorf("read_file(%q) = %q", p, got)
		}
	}

	err := errOf(t, tools, "read_file", readArgs{Path: outside})
	if !errors.Is(err, ErrOutsideWorkspace) {
		t.Errorf("read_file(%q) = %v, want it refused as outside the workspace", outside, err)
	}

	if _, err := call(t, tools, "write_file", writeArgs{Path: "../planted.txt", Content: "x"}); err == nil {
		t.Error("write_file outside the workspace should be refused")
	}
	if !errors.Is(errOf(t, tools, "write_file", writeArgs{Path: "../planted.txt", Content: "x"}), ErrOutsideWorkspace) {
		t.Error("the refusal should be matchable with errors.Is")
	}
}

func errOf(t *testing.T, tools []golm.Tool, name string, args any) error {
	t.Helper()
	_, err := call(t, tools, name, args)
	return err
}

func TestWriteCreatesAndRefusesToOverwrite(t *testing.T) {
	w, dir := ws(t)
	tools := w.Tools()

	got, err := call(t, tools, "write_file", writeArgs{Path: "notes/today.md", Content: "hello"})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if !strings.Contains(got, "5 bytes") {
		t.Errorf("result = %q, want the byte count", got)
	}
	b, err := os.ReadFile(filepath.Join(dir, "notes", "today.md"))
	if err != nil {
		t.Fatalf("the file was not created: %v", err)
	}
	if string(b) != "hello" {
		t.Errorf("contents = %q", b)
	}

	_, err = call(t, tools, "write_file", writeArgs{Path: "notes/today.md", Content: "clobbered"})
	if err == nil {
		t.Fatal("write_file over an existing file should be refused")
	}
	if !strings.Contains(err.Error(), "edit_file") {
		t.Errorf("err = %v, want it to point at the right tool", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "notes", "today.md")); string(b) != "hello" {
		t.Errorf("the file was changed anyway: %q", b)
	}
}

func TestEditReplacesExactlyOneOccurrence(t *testing.T) {
	w, dir := ws(t)
	tools := w.Tools()
	path := filepath.Join(dir, "conf.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := call(t, tools, "edit_file", editArgs{Path: "conf.txt", Old: "beta", New: "delta"}); err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "alpha\ndelta\ngamma\n" {
		t.Errorf("contents = %q", b)
	}
}

func TestEditRefusesWhatItCannotDoSafely(t *testing.T) {
	w, dir := ws(t)
	tools := w.Tools()
	path := filepath.Join(dir, "dup.txt")
	original := "x = 1\ny = 2\nx = 1\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := call(t, tools, "edit_file", editArgs{Path: "dup.txt", Old: "x = 1", New: "x = 9"})
	if err == nil {
		t.Fatal("an edit matching twice should be refused")
	}
	if !strings.Contains(err.Error(), "2 times") {
		t.Errorf("err = %v, want the count", err)
	}

	_, err = call(t, tools, "edit_file", editArgs{Path: "dup.txt", Old: "z = 3", New: "z = 4"})
	if err == nil {
		t.Fatal("an edit matching nothing should be refused")
	}
	if !strings.Contains(err.Error(), "read it again") {
		t.Errorf("err = %v, want it to say what to do", err)
	}

	if b, _ := os.ReadFile(path); string(b) != original {
		t.Errorf("the file changed despite the refusals: %q", b)
	}

	if _, err := call(t, tools, "edit_file", editArgs{Path: "dup.txt", Old: "", New: "x"}); err == nil {
		t.Error("an empty old should be refused")
	}
	if _, err := call(t, tools, "edit_file", editArgs{Path: "absent.txt", Old: "a", New: "b"}); err == nil {
		t.Error("editing a file that is not there should be refused")
	}
}

func TestWriteIsAtomic(t *testing.T) {
	w, dir := ws(t)
	tools := w.Tools()
	if _, err := call(t, tools, "write_file", writeArgs{Path: "ok.txt", Content: "content"}); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".golm-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want just the file", len(entries))
	}
}

func TestReadRefusesBinaryAndBoundsSize(t *testing.T) {
	w, dir := ws(t)
	tools := w.Tools()
	if err := os.WriteFile(filepath.Join(dir, "image.bin"), []byte{0x89, 0x50, 0x00, 0x01}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, tools, "read_file", readArgs{Path: "image.bin"}); err == nil {
		t.Error("a binary file should be refused rather than dumped into the context")
	}

	big := strings.Repeat("a", maxReadBytes+2048)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := call(t, tools, "read_file", readArgs{Path: "big.txt"})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if len(got) > maxReadBytes+128 {
		t.Errorf("read %d bytes, want it capped near %d", len(got), maxReadBytes)
	}
	if !strings.Contains(got, "truncated") {
		t.Error("a truncated read must say so")
	}
}

func TestListAndSearch(t *testing.T) {
	w, dir := ws(t)
	tools := w.Tools()
	for name, body := range map[string]string{
		"a.go":      "package main\nfunc main() {}\n",
		"b.go":      "package main\n// a note about tokens\n",
		"README.md": "tokens and more tokens\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}

	list, err := call(t, tools, "list_dir", listArgs{})
	if err != nil {
		t.Fatalf("list_dir: %v", err)
	}
	for _, want := range []string{"a.go", "README.md", "sub/"} {
		if !strings.Contains(list, want) {
			t.Errorf("listing %q missing %q", list, want)
		}
	}

	hits, err := call(t, tools, "search", searchArgs{Query: "tokens"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(hits, "b.go:2:") || !strings.Contains(hits, "README.md:1:") {
		t.Errorf("search results = %q, want file:line for each hit", hits)
	}

	hits, err = call(t, tools, "search", searchArgs{Query: `func \w+\(`, Regex: true})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(hits, "a.go:2:") {
		t.Errorf("regex search = %q", hits)
	}
	if _, err := call(t, tools, "search", searchArgs{Query: "[unclosed", Regex: true}); err == nil {
		t.Error("an invalid regular expression should be reported, not ignored")
	}
	if got, _ := call(t, tools, "search", searchArgs{Query: "nowhere-at-all"}); got != "no matches" {
		t.Errorf("a search with no hits = %q", got)
	}
}

func TestReadOnlyWorkspaceWithholdsTheWriters(t *testing.T) {
	w, _ := ws(t)
	w.ReadOnly = true
	names := toolNames(w.Tools())
	for _, n := range names {
		if n == "write_file" || n == "edit_file" {
			t.Errorf("a read-only workspace must not offer %s", n)
		}
	}
	if len(names) != 3 {
		t.Errorf("tools = %v, want the three readers", names)
	}
}

func TestTraitsAreDeclaredHonestly(t *testing.T) {
	w, _ := ws(t)
	want := map[string]golm.ToolTraits{
		"read_file":  {ReadOnly: true, Filesystem: true},
		"list_dir":   {ReadOnly: true, Filesystem: true},
		"search":     {ReadOnly: true, Filesystem: true},
		"write_file": {Filesystem: true},
		"edit_file":  {Filesystem: true},
	}
	for _, tool := range w.Tools() {
		got, ok := golm.TraitsOf(tool)
		if !ok {
			t.Errorf("%s declares no traits; a policy could not class it", tool.Name())
			continue
		}
		if got != want[tool.Name()] {
			t.Errorf("%s traits = %+v, want %+v", tool.Name(), got, want[tool.Name()])
		}
	}
}

// A rename replaces the destination mode and all.
func TestEditPreservesTheFilesMode(t *testing.T) {
	w, dir := ws(t)
	tools := w.Tools()
	p := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(p, []byte("alpha\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, tools, "edit_file", editArgs{Path: "script.sh", Old: "alpha", New: "beta"}); err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode after edit = %v, want it unchanged at -rwxr-xr-x", fi.Mode().Perm())
	}

	if _, err := call(t, tools, "write_file", writeArgs{Path: "fresh.txt", Content: "x"}); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	fi, _ = os.Stat(filepath.Join(dir, "fresh.txt"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("new file mode = %v, want 0600", fi.Mode().Perm())
	}
}
