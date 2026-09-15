// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package build

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func plainCfg() *golm.Config {
	return &golm.Config{
		Providers: []golm.ProviderConfig{{Name: "local", Type: "cli", Command: "cat"}},
		Agents:    []golm.PersonaConfig{{Name: "main", Provider: "local", Model: "cli"}},
	}
}

// Nothing is on unless a deployment says so.
func TestBuiltinToolsAreOffByDefault(t *testing.T) {
	rt, err := New(context.Background(), Options{Config: plainCfg(), Agent: "main"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	if n := len(rt.Tools.List()); n != 0 {
		t.Errorf("tools = %v, want none without a builtin block", toolNames(rt.Tools))
	}
	if rt.Workspace != nil {
		t.Error("no workspace should be opened")
	}
}

func TestWorkspaceToolsAreConfinedToTheConfiguredDirectory(t *testing.T) {
	dir := t.TempDir()
	cfg := plainCfg()
	cfg.Builtin = &golm.BuiltinConfig{Workspace: dir}

	rt, err := New(context.Background(), Options{Config: cfg, Agent: "main"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	got := toolNames(rt.Agent.Tools)
	for _, want := range []string{"read_file", "write_file", "edit_file", "list_dir", "search"} {
		if !slices.Contains(got, want) {
			t.Errorf("tools = %v, want %q", got, want)
		}
	}
	if rt.Workspace == nil || rt.Workspace.Dir() != dir {
		t.Errorf("workspace = %v, want the configured directory", rt.Workspace)
	}

	if err := rt.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestReadOnlyWorkspaceFromConfig(t *testing.T) {
	cfg := plainCfg()
	cfg.Builtin = &golm.BuiltinConfig{Workspace: t.TempDir(), WorkspaceReadOnly: true}
	rt, err := New(context.Background(), Options{Config: cfg, Agent: "main"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	got := toolNames(rt.Agent.Tools)
	for _, unwanted := range []string{"write_file", "edit_file"} {
		if slices.Contains(got, unwanted) {
			t.Errorf("tools = %v, want %q withheld", got, unwanted)
		}
	}
	if !slices.Contains(got, "read_file") {
		t.Errorf("tools = %v, want the readers kept", got)
	}
}

func TestRunWithoutAnAllowlistIsRejected(t *testing.T) {
	cfg := plainCfg()
	cfg.Builtin = &golm.BuiltinConfig{Run: &golm.RunConfig{}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("a run block with no allowlist should not validate")
	}

	rt, err := New(context.Background(), Options{Config: plainCfg(), Agent: "main",
		Builtin: &golm.BuiltinConfig{Run: &golm.RunConfig{}}})
	if err == nil {
		rt.Close()
		t.Fatal("an empty allowlist should be refused however it arrived")
	}
	if !strings.Contains(err.Error(), "allow") {
		t.Errorf("err = %v, want it to name the allowlist", err)
	}

	rt, err = New(context.Background(), Options{Config: plainCfg(), Agent: "main"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	if slices.Contains(toolNames(rt.Tools), "run") {
		t.Error("no run block means no run tool")
	}
}

func TestFetchAndRunFromConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := plainCfg()
	cfg.Builtin = &golm.BuiltinConfig{
		Workspace: dir,
		Fetch:     &golm.FetchConfig{Timeout: "5s"},
		Run:       &golm.RunConfig{Allow: []string{"echo"}, Timeout: "10s"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	rt, err := New(context.Background(), Options{Config: cfg, Agent: "main"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	got := toolNames(rt.Agent.Tools)
	for _, want := range []string{"fetch", "run"} {
		if !slices.Contains(got, want) {
			t.Errorf("tools = %v, want %q", got, want)
		}
	}
}

// An embedder decides what its agent may touch, whatever the config file says.
func TestOptionsBuiltinOverridesTheConfig(t *testing.T) {
	cfg := plainCfg()
	cfg.Builtin = &golm.BuiltinConfig{Workspace: t.TempDir(), Run: &golm.RunConfig{Allow: []string{"rm"}}}
	host := t.TempDir()

	rt, err := New(context.Background(), Options{Config: cfg, Agent: "main",
		Builtin: &golm.BuiltinConfig{Workspace: host, WorkspaceReadOnly: true}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	if rt.Workspace.Dir() != host {
		t.Errorf("workspace = %q, want the host's %q", rt.Workspace.Dir(), host)
	}
	got := toolNames(rt.Agent.Tools)
	if slices.Contains(got, "run") {
		t.Errorf("tools = %v; the host's block replaced the config's, so run must be gone", got)
	}
	if slices.Contains(got, "write_file") {
		t.Errorf("tools = %v, want the host's read-only choice honoured", got)
	}
}

// The tools reach the model with the workspace the deployment chose.
func TestAgentCanUseTheBuiltinTools(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("the answer"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := plainCfg()
	cfg.Builtin = &golm.BuiltinConfig{Workspace: dir}
	rt, err := New(context.Background(), Options{Config: cfg, Agent: "main"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()

	tool, ok := rt.Agent.Tools.Get("read_file")
	if !ok {
		t.Fatal("read_file missing")
	}
	out, err := tool.Execute(context.Background(), []byte(`{"path":"note.txt"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := (golm.ToolResult{Content: out}).Text(); got != "the answer" {
		t.Errorf("read_file = %q", got)
	}

	if _, err := tool.Execute(context.Background(), []byte(`{"path":"../../etc/passwd"}`)); err == nil {
		t.Error("the agent's file tool must stay inside the workspace")
	}
}
