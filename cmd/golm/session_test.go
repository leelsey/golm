// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/sessionstore"
)

func cliConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.json")
	if c := runArgs("config", "add-provider", "--config", p, "--name", "local", "--type", "cli", "--command", "cat"); c != 0 {
		t.Fatalf("add-provider: %d", c)
	}
	if c := runArgs("config", "add-agent", "--config", p, "--name", "main", "--provider", "local", "--model", "cli", "--default"); c != 0 {
		t.Fatalf("add-agent: %d", c)
	}
	return p
}

func TestNoStoreWithoutASession(t *testing.T) {
	store, err := openStore("", false)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	if store != nil {
		t.Fatal("openStore without --session/--store should return no store at all")
	}

	if store != golm.SessionStore(nil) {
		t.Error("the returned store is a non-nil interface holding a nil pointer")
	}
}

func TestSessionPersistsAcrossRuns(t *testing.T) {
	cfg := cliConfig(t)
	dir := t.TempDir()

	var out, errOut bytes.Buffer
	if c := run([]string{"--config", cfg, "--store", dir, "--session", "chat-1", "--no-stream", "first question"},
		strings.NewReader(""), &out, &errOut); c != 0 {
		t.Fatalf("first run: %d %s", c, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	if c := run([]string{"--config", cfg, "--store", dir, "--session", "chat-1", "--no-stream", "second question"},
		strings.NewReader(""), &out, &errOut); c != 0 {
		t.Fatalf("second run: %d %s", c, errOut.String())
	}
	if !strings.Contains(errOut.String(), "resumed session chat-1") {
		t.Errorf("stderr = %q, want it to say the session was resumed", errOut.String())
	}

	store, err := openStore(dir, true)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	s, err := store.Load(context.Background(), "chat-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.Len() != 4 {
		t.Fatalf("history = %d, want 4: %v", s.Len(), s.History())
	}
	if got := s.History()[2].Text(); got != "second question" {
		t.Errorf("third message = %q, want the second question appended after the first exchange", got)
	}
}

func TestSessionRejectsAnUnsafeIDBeforeRunning(t *testing.T) {
	cfg := cliConfig(t)
	var out, errOut bytes.Buffer
	c := run([]string{"--config", cfg, "--store", t.TempDir(), "--session", "../escape", "--no-stream", "hi"},
		strings.NewReader(""), &out, &errOut)
	if c == 0 {
		t.Fatal("a session id that is not safe as a filename should fail")
	}
	if !strings.Contains(errOut.String(), "invalid session id") {
		t.Errorf("stderr = %q, want it to name the invalid id", errOut.String())
	}
}

func TestSessionsSubcommands(t *testing.T) {
	cfg := cliConfig(t)
	dir := t.TempDir()
	for _, id := range []string{"alpha", "beta"} {
		if c := run([]string{"--config", cfg, "--store", dir, "--session", id, "--no-stream", "about " + id},
			strings.NewReader(""), io.Discard, io.Discard); c != 0 {
			t.Fatalf("seed %s: %d", id, c)
		}
	}

	var out, errOut bytes.Buffer
	if c := runSessions([]string{"list", "--store", dir}, &out, &errOut); c != 0 {
		t.Fatalf("list: %d %s", c, errOut.String())
	}
	for _, want := range []string{"alpha", "beta", "MSGS"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list output %q missing %q", out.String(), want)
		}
	}

	out.Reset()
	if c := runSessions([]string{"show", "--store", dir, "alpha"}, &out, &errOut); c != 0 {
		t.Fatalf("show: %d %s", c, errOut.String())
	}
	if !strings.Contains(out.String(), "about alpha") {
		t.Errorf("show output %q missing the transcript", out.String())
	}

	out.Reset()
	if c := runSessions([]string{"search", "--store", dir, "about beta"}, &out, &errOut); c != 0 {
		t.Fatalf("search: %d %s", c, errOut.String())
	}
	if !strings.Contains(out.String(), "beta") {
		t.Errorf("search output %q missing the hit", out.String())
	}

	out.Reset()
	if c := runSessions([]string{"rm", "--store", dir, "alpha"}, &out, &errOut); c != 0 {
		t.Fatalf("rm: %d %s", c, errOut.String())
	}
	out.Reset()
	if c := runSessions([]string{"list", "--store", dir}, &out, &errOut); c != 0 {
		t.Fatalf("list after rm: %d", c)
	}
	if strings.Contains(out.String(), "alpha") {
		t.Errorf("list after rm still shows the removed session: %q", out.String())
	}
}

func TestSessionsUsageErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	if c := runSessions(nil, &out, &errOut); c != 2 {
		t.Errorf("no subcommand = %d, want 2", c)
	}
	if c := runSessions([]string{"telepathy"}, &out, &errOut); c != 2 {
		t.Errorf("unknown subcommand = %d, want 2", c)
	}
	if c := runSessions([]string{"show", "--store", t.TempDir()}, &out, &errOut); c != 2 {
		t.Errorf("show without an id = %d, want 2", c)
	}
	if c := runSessions([]string{"search", "--store", t.TempDir()}, &out, &errOut); c != 2 {
		t.Errorf("search without a term = %d, want 2", c)
	}
	out.Reset()
	if c := runSessions([]string{"help"}, &out, &errOut); c != 0 {
		t.Errorf("help = %d, want 0", c)
	}
	if !strings.Contains(out.String(), "golm sessions") {
		t.Error("help printed nothing useful")
	}
}

func TestDefaultSessionsDirHonoursTheEnvironment(t *testing.T) {
	t.Setenv("GOLM_SESSIONS", "/tmp/golm-test-sessions")
	d, err := defaultSessionsDir()
	if err != nil {
		t.Fatalf("defaultSessionsDir: %v", err)
	}
	if d != "/tmp/golm-test-sessions" {
		t.Errorf("dir = %q, want the environment's", d)
	}
	t.Setenv("GOLM_SESSIONS", "")
	d, err = defaultSessionsDir()
	if err != nil {
		t.Fatalf("defaultSessionsDir: %v", err)
	}
	if !strings.HasSuffix(d, filepath.Join(".local", "share", "golm", "sessions")) {
		t.Errorf("default dir = %q, want it under ~/.local/share", d)
	}
}

func TestReportResultSurfacesWhatTheTextDoesNot(t *testing.T) {
	cases := []struct {
		name string
		res  golm.Result
		code int
		want []string
	}{
		{"clean", golm.Result{StopReason: golm.StopEndTurn}, 0, nil},
		{"truncated", golm.Result{StopReason: golm.StopMaxTokens}, ExitIncomplete, []string{"cut off"}},
		{"tool failures", golm.Result{StopReason: golm.StopEndTurn, ToolErrors: 3}, 0, []string{"3 tool call(s) failed"}},
		{"denials counted once", golm.Result{StopReason: golm.StopEndTurn, ToolErrors: 2, ToolDenials: 2}, 0, []string{"2 tool call(s) refused"}},
		{"compaction", golm.Result{StopReason: golm.StopEndTurn, CompactError: context.DeadlineExceeded}, 0, []string{"compaction did not run"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var errOut bytes.Buffer
			if got := reportResult(&errOut, tc.res); got != tc.code {
				t.Errorf("exit = %d, want %d", got, tc.code)
			}
			for _, w := range tc.want {
				if !strings.Contains(errOut.String(), w) {
					t.Errorf("stderr = %q, want %q in it", errOut.String(), w)
				}
			}
			if tc.want == nil && errOut.Len() != 0 {
				t.Errorf("a clean result printed %q, want nothing", errOut.String())
			}

			if tc.name == "denials counted once" && strings.Contains(errOut.String(), "failed") {
				t.Errorf("stderr = %q, want denials not double-counted as failures", errOut.String())
			}
		})
	}
}

// The usage block is what people copy.
func TestSessionsUsageFormsAllParse(t *testing.T) {
	dir := t.TempDir()
	store := &sessionstore.Files{Dir: dir}
	s := golm.NewSession()
	s.SetTitle("t")
	s.Append(golm.UserText("hello world"))
	if err := store.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	id := s.ID()

	for _, args := range [][]string{
		{"list", "--store", dir},
		{"list", "--store", dir, "--limit", "5"},
		{"show", "--store", dir, id},
		{"search", "--store", dir, "hello"},
		{"search", "--store", dir, "--limit", "5", "hello"},
		{"lineage", "--store", dir, id},
	} {
		var out, errOut bytes.Buffer
		if code := runSessions(args, &out, &errOut); code != 0 {
			t.Errorf("%v -> exit %d, stderr %q", args, code, errOut.String())
		}
	}

	var out, errOut bytes.Buffer
	if code := runSessions([]string{"rm", "--store", dir, id}, &out, &errOut); code != 0 {
		t.Errorf("rm -> exit %d, stderr %q", code, errOut.String())
	}
}
