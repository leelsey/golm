// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/tools"
)

func ws(t *testing.T) (*tools.Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	w, err := tools.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { w.Close() })
	return w, dir
}

func tool(t *testing.T, w *tools.Workspace, name string) golm.Tool {
	t.Helper()
	for _, tl := range w.Tools() {
		if tl.Name() == name {
			return tl
		}
	}
	t.Fatalf("no %q tool", name)
	return nil
}

func exec(t *testing.T, tl golm.Tool, args string) (string, error) {
	t.Helper()
	out, err := tl.Execute(context.Background(), json.RawMessage(args))
	var b strings.Builder
	for _, c := range out {
		if txt, ok := c.(golm.Text); ok {
			b.WriteString(txt.Text)
		}
	}
	return b.String(), err
}

// atomicWrite goes through a temp file and a rename.
func TestConcurrentEditsLeaveTheFileWhole(t *testing.T) {
	w, dir := ws(t)
	path := filepath.Join(dir, "shared.txt")
	if err := os.WriteFile(path, []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	write := tool(t, w, "write_file")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, _ := json.Marshal(map[string]string{
				"path":    "f" + string(rune('a'+i)) + ".txt",
				"content": strings.Repeat("x", 4096),
			})
			if _, err := write.Execute(context.Background(), body); err != nil {
				t.Errorf("write %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".golm-") {
			t.Errorf("a temporary file survived: %s", e.Name())
		}
	}
	for i := 0; i < 8; i++ {
		b, err := os.ReadFile(filepath.Join(dir, "f"+string(rune('a'+i))+".txt"))
		if err != nil {
			t.Errorf("f%c.txt: %v", 'a'+i, err)
			continue
		}
		if len(b) != 4096 {
			t.Errorf("f%c.txt is %d bytes, want a whole 4096", 'a'+i, len(b))
		}
	}
}

// A refused edit must leave the file exactly as it was.
func TestRefusedEditLeavesTheFileUntouched(t *testing.T) {
	w, dir := ws(t)
	original := "alpha\nbeta\nalpha\n"
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	edit := tool(t, w, "edit_file")
	for _, args := range []string{
		`{"path":"f.txt","old":"alpha","new":"gamma"}`,
		`{"path":"f.txt","old":"missing","new":"x"}`,
		`{"path":"f.txt","old":"alpha","new":"alpha"}`,
	} {
		if _, err := exec(t, edit, args); err == nil {
			t.Errorf("%s was accepted", args)
		}
		b, err := os.ReadFile(filepath.Join(dir, "f.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != original {
			t.Fatalf("a refused edit changed the file: %q", b)
		}
	}
}

// An edit must carry the old mode across.
func TestEditKeepsTheFileMode(t *testing.T) {
	w, dir := ws(t)
	path := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec(t, tool(t, w, "edit_file"),
		`{"path":"run.sh","old":"echo hi","new":"echo bye"}`); err != nil {
		t.Fatalf("edit: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode is now %o; the script stopped being executable", fi.Mode().Perm())
	}
}

// os.Root refuses an escape in the kernel.
func TestEveryEscapeShapeIsRefused(t *testing.T) {
	w, dir := ws(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	read := tool(t, w, "read_file")
	for _, path := range []string{
		"../secret.txt", "../../etc/passwd", outside, "link.txt",
		"a/../../secret.txt", "./../secret.txt",
	} {
		body, _ := json.Marshal(map[string]string{"path": path})
		out, err := read.Execute(context.Background(), body)
		if err == nil {
			var text strings.Builder
			for _, c := range out {
				if t2, ok := c.(golm.Text); ok {
					text.WriteString(t2.Text)
				}
			}
			t.Errorf("%q was read: %q", path, text.String())
		}
	}
}

// A read-only workspace withholds the writing tools entirely.
func TestReadOnlyWithholdsTheWritingTools(t *testing.T) {
	w, _ := ws(t)
	w.ReadOnly = true
	for _, tl := range w.Tools() {
		if tl.Name() == "write_file" || tl.Name() == "edit_file" {
			t.Errorf("a read-only workspace offers %q", tl.Name())
		}
	}
}

// write_file creates. It must never overwrite, whatever the path looks like.
func TestWriteNeverOverwrites(t *testing.T) {
	w, dir := ws(t)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"f.txt", "./f.txt", "a/../f.txt"} {
		body, _ := json.Marshal(map[string]string{"path": path, "content": "replaced"})
		if _, err := tool(t, w, "write_file").Execute(context.Background(), body); err == nil {
			t.Errorf("%q overwrote an existing file", path)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if string(b) != "original" {
		t.Errorf("the file is now %q", b)
	}
}
