// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func TestConfigShowValidatePath(t *testing.T) {
	p := cliConfig(t)

	var out, errOut bytes.Buffer
	if c := runConfig([]string{"show", "--config", p}, strings.NewReader(""), &out, &errOut); c != 0 {
		t.Fatalf("show: %d %s", c, errOut.String())
	}
	var got golm.Config
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("show did not print JSON: %v (%q)", err, out.String())
	}
	if len(got.Providers) != 1 || got.DefaultAgent != "main" {
		t.Errorf("show printed %+v, want the written config", got)
	}

	out.Reset()
	if c := runConfig([]string{"validate", "--config", p}, strings.NewReader(""), &out, &errOut); c != 0 {
		t.Fatalf("validate: %d %s", c, errOut.String())
	}
	if !strings.Contains(out.String(), "is valid") {
		t.Errorf("validate said %q", out.String())
	}

	out.Reset()
	if c := runConfig([]string{"path", "--config", p}, strings.NewReader(""), &out, &errOut); c != 0 {
		t.Fatalf("path: %d", c)
	}
	if !strings.Contains(out.String(), p) || !strings.Contains(out.String(), "exists") {
		t.Errorf("path printed %q, want the path and its status", out.String())
	}

	out.Reset()
	absent := filepath.Join(t.TempDir(), "none.json")
	if c := runConfig([]string{"path", "--config", absent}, strings.NewReader(""), &out, &errOut); c != 0 {
		t.Fatalf("path (absent): %d", c)
	}
	if !strings.Contains(out.String(), "missing") {
		t.Errorf("path printed %q, want it marked missing", out.String())
	}
	if c := runConfig([]string{"show", "--config", absent}, strings.NewReader(""), io.Discard, &errOut); c != 1 {
		t.Errorf("show of an absent config = %d, want 1", c)
	}
	if c := runConfig([]string{"validate", "--config", absent}, strings.NewReader(""), io.Discard, &errOut); c != 1 {
		t.Errorf("validate of an absent config = %d, want 1", c)
	}
}

func TestConfigSetDefault(t *testing.T) {
	p := cliConfig(t)
	if c := runArgs("config", "add-agent", "--config", p, "--name", "second", "--provider", "local", "--model", "cli"); c != 0 {
		t.Fatalf("add-agent: %d", c)
	}
	var out, errOut bytes.Buffer
	if c := runConfig([]string{"set-default", "--config", p, "second"}, strings.NewReader(""), &out, &errOut); c != 0 {
		t.Fatalf("set-default: %d %s", c, errOut.String())
	}
	cfg, err := golm.LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DefaultAgent != "second" {
		t.Errorf("default_agent = %q, want %q", cfg.DefaultAgent, "second")
	}

	if c := runConfig([]string{"set-default", "--config", p, "ghost"}, strings.NewReader(""), io.Discard, &errOut); c == 0 {
		t.Error("set-default to an undefined agent should fail")
	}
	cfg, _ = golm.LoadConfig(p)
	if cfg.DefaultAgent != "second" {
		t.Errorf("default_agent = %q after a refused change, want it untouched", cfg.DefaultAgent)
	}
	if c := runConfig([]string{"set-default", "--config", p}, strings.NewReader(""), io.Discard, &errOut); c != 2 {
		t.Error("set-default with no agent name should be a usage error")
	}
}

func TestConfigEdit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX no-op editor")
	}
	p := cliConfig(t)

	t.Setenv("EDITOR", "true")
	var out, errOut bytes.Buffer
	if c := runConfig([]string{"edit", "--config", p}, strings.NewReader(""), &out, &errOut); c != 0 {
		t.Fatalf("edit: %d %s", c, errOut.String())
	}
	if !strings.Contains(out.String(), "saved") {
		t.Errorf("edit said %q", out.String())
	}
	if _, err := golm.LoadConfig(p); err != nil {
		t.Fatalf("config broken after a no-op edit: %v", err)
	}

	t.Setenv("EDITOR", "false")
	errOut.Reset()
	if c := runConfig([]string{"edit", "--config", p}, strings.NewReader(""), io.Discard, &errOut); c != 1 {
		t.Errorf("edit with a failing editor = %d, want 1", c)
	}
	if !strings.Contains(errOut.String(), "editor") {
		t.Errorf("stderr = %q, want the editor named", errOut.String())
	}
	if _, err := golm.LoadConfig(p); err != nil {
		t.Fatalf("config damaged by a failed edit: %v", err)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Error("a failed editor left its draft behind")
	}
}

func TestSessionsLineage(t *testing.T) {
	dir := t.TempDir()
	store, err := openStore(dir, true)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}

	archive := golm.SessionData{ID: "archived", History: []golm.Message{golm.UserText("old")}}.Session()
	live := golm.SessionData{ID: "live", Parent: "archived", History: []golm.Message{golm.UserText("new")}}.Session()
	for _, s := range []*golm.Session{archive, live} {
		if err := store.Save(t.Context(), s); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	var out, errOut bytes.Buffer
	if c := runSessions([]string{"lineage", "--store", dir, "live"}, &out, &errOut); c != 0 {
		t.Fatalf("lineage: %d %s", c, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lineage printed %d lines, want 2: %q", len(lines), out.String())
	}
	if !strings.HasPrefix(lines[0], "live") {
		t.Errorf("first line = %q, want the live session first", lines[0])
	}
	if !strings.Contains(lines[1], "archived") {
		t.Errorf("second line = %q, want the archive", lines[1])
	}
	if c := runSessions([]string{"lineage", "--store", dir}, &out, &errOut); c != 2 {
		t.Error("lineage without an id should be a usage error")
	}
}

func TestEventDetail(t *testing.T) {
	cases := []struct {
		name string
		ev   golm.Event
		want string
	}{
		{"step", golm.Event{Kind: "step", Data: golm.StepEvent{Step: 2, StopReason: golm.StopToolUse}}, "2: tool_use"},
		{"tool call", golm.Event{Kind: "tool", Data: golm.ToolCallEvent{Name: "echo", Input: []byte(`{"a":1}`)}}, `echo {"a":1}`},
		{"tool ok", golm.Event{Kind: "tool_result", Data: golm.ToolResultEvent{Name: "echo"}}, "echo ok"},
		{"tool error", golm.Event{Kind: "tool_result", Data: golm.ToolResultEvent{Name: "echo", IsError: true, Content: "boom"}}, "echo error: boom"},
		{"error string", golm.Event{Kind: "error", Data: "it broke"}, ": it broke"},
		{"plain string", golm.Event{Kind: "message", Data: "hello"}, ""},
		{"unknown payload", golm.Event{Kind: "step", Data: 42}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := eventDetail(tc.ev); !strings.Contains(got, tc.want) || (tc.want == "" && got != "") {
				t.Errorf("eventDetail = %q, want %q in it", got, tc.want)
			}
		})
	}
}

func TestTruncCutsOnARuneBoundary(t *testing.T) {
	if got := trunc("abc", 10); got != "abc" {
		t.Errorf("trunc under the cap = %q", got)
	}

	full := strings.Repeat("한", 5)
	if got := trunc(full, 10); got != full {
		t.Errorf("trunc = %q, want %q untouched", got, full)
	}
	got := trunc(strings.Repeat("한", 20), 5)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("trunc = %q, want the cut marked", got)
	}
	if len([]rune(got)) != 6 {
		t.Errorf("trunc kept %d runes, want 5 plus the marker", len([]rune(got)))
	}
}

func TestBuildUserMessageAttachments(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "shot.jpg")
	aud := filepath.Join(dir, "clip.mp3")
	unknown := filepath.Join(dir, "blob.zzz")
	for _, p := range []string{img, aud, unknown} {
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	msg, err := buildUserMessage("look", stringList{img, unknown}, stringList{aud})
	if err != nil {
		t.Fatalf("buildUserMessage: %v", err)
	}
	if len(msg.Content) != 4 {
		t.Fatalf("content blocks = %d, want text + 2 images + 1 audio", len(msg.Content))
	}
	if got := msg.Content[1].(golm.Image).MediaType; got != "image/jpeg" {
		t.Errorf("jpg media type = %q, want image/jpeg", got)
	}

	if got := msg.Content[2].(golm.Image).MediaType; got != "image/png" {
		t.Errorf("unknown extension media type = %q, want the image/png fallback", got)
	}
	if got := msg.Content[3].(golm.Audio).MediaType; got != "audio/mpeg" {
		t.Errorf("mp3 media type = %q, want audio/mpeg", got)
	}

	if _, err := buildUserMessage("x", stringList{filepath.Join(dir, "absent.png")}, nil); err == nil {
		t.Error("an unreadable attachment should fail the message, not be skipped")
	}
}
