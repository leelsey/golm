// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/acp"
)

func acpConfig(t *testing.T) string {
	t.Helper()
	cfg := &golm.Config{
		DefaultAgent: "main",
		Providers:    []golm.ProviderConfig{{Name: "p", Type: "anthropic", APIKeyEnv: "NOPE"}},
		Agents: []golm.PersonaConfig{
			{Name: "main", Provider: "p", Model: "claude-opus-5", Role: "main"},
			{Name: "helper", Provider: "p", Model: "claude-opus-5", Role: "sub", Description: "helps"},
		},
	}
	path := filepath.Join(t.TempDir(), "golm.json")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return path
}

func TestACPAgentAssembly(t *testing.T) {
	path := acpConfig(t)
	bf := &backendFlags{store: t.TempDir()}
	a, closeAll, err := bf.acpAgent(context.Background(), path, false, &strings.Builder{})
	if err != nil {
		t.Fatalf("acpAgent: %v", err)
	}
	defer closeAll()

	if a.Runner == nil {
		t.Fatal("no runner")
	}

	if a.Store == nil {
		t.Error("no session store, so the editor will not offer to resume")
	}
	if a.Orchestrator != nil {
		t.Error("a single-agent run should have no orchestrator")
	}
	if a.Info.Name != "golm" {
		t.Errorf("info = %+v", a.Info)
	}
}

func TestACPOrchestrated(t *testing.T) {
	path := acpConfig(t)
	bf := &backendFlags{store: t.TempDir()}
	a, closeAll, err := bf.acpAgent(context.Background(), path, true, &strings.Builder{})
	if err != nil {
		t.Fatalf("acpAgent: %v", err)
	}
	defer closeAll()
	if a.Orchestrator == nil {
		t.Fatal("--orchestrate produced no orchestrator")
	}

	if a.Runner != golm.Runner(a.Orchestrator) {
		t.Error("the runner should be the orchestration")
	}
}

// Everywhere else a policy that asks has nobody to ask.
func TestACPGatesByDefault(t *testing.T) {
	path := acpConfig(t)
	bf := &backendFlags{store: t.TempDir()}
	a, closeAll, err := bf.acpAgent(context.Background(), path, false, &strings.Builder{})
	if err != nil {
		t.Fatalf("acpAgent: %v", err)
	}
	defer closeAll()
	ag, ok := a.Runner.(*golm.Agent)
	if !ok {
		t.Fatalf("runner is %T", a.Runner)
	}
	if ag.ToolPolicy == nil {
		t.Fatal("no gate; the one surface with a human on it left the tools ungated")
	}

	dangerous := golm.WithTraits(golm.TextTool("run", "d", "x", "y", nil), golm.ToolTraits{Process: true})
	if _, err := ag.ToolPolicy(context.Background(),
		golm.ToolRequest{Tool: dangerous, Call: golm.ToolUse{Name: "run"}}); err == nil {
		t.Error("a process tool was allowed with no session to ask in")
	}
	safe := golm.WithTraits(golm.TextTool("read_file", "d", "x", "y", nil),
		golm.ToolTraits{ReadOnly: true, Filesystem: true})
	if _, err := ag.ToolPolicy(context.Background(),
		golm.ToolRequest{Tool: safe, Call: golm.ToolUse{Name: "read_file"}}); err != nil {
		t.Errorf("a read-only builtin was gated: %v", err)
	}
}

// A configured policy is the deployment's decision and must not be replaced.
func TestACPKeepsAConfiguredPolicy(t *testing.T) {
	cfg := &golm.Config{
		DefaultAgent: "main",
		Providers:    []golm.ProviderConfig{{Name: "p", Type: "anthropic", APIKeyEnv: "NOPE"}},
		Agents:       []golm.PersonaConfig{{Name: "main", Provider: "p", Model: "claude-opus-5"}},
		Policy:       &golm.PolicyConfig{Default: "deny"},
	}
	path := filepath.Join(t.TempDir(), "golm.json")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	bf := &backendFlags{store: t.TempDir()}
	a, closeAll, err := bf.acpAgent(context.Background(), path, false, &strings.Builder{})
	if err != nil {
		t.Fatalf("acpAgent: %v", err)
	}
	defer closeAll()
	ag := a.Runner.(*golm.Agent)

	safe := golm.WithTraits(golm.TextTool("read_file", "d", "x", "y", nil), golm.ToolTraits{ReadOnly: true})
	if _, err := ag.ToolPolicy(context.Background(),
		golm.ToolRequest{Tool: safe, Call: golm.ToolUse{Name: "read_file"}}); err == nil {
		t.Error("the configured deny-by-default policy was replaced")
	}
}

func TestACPVersionIsReported(t *testing.T) {
	if acp.Version != 1 {
		t.Errorf("protocol version = %d", acp.Version)
	}
}

// A run tool under ACP should be the editor's terminal, where a person can watch and stop it.
func TestACPRunAllowListComesFromFlagsOrConfig(t *testing.T) {
	cfg := &golm.Config{
		DefaultAgent: "main",
		Providers:    []golm.ProviderConfig{{Name: "p", Type: "anthropic", APIKeyEnv: "NOPE"}},
		Agents:       []golm.PersonaConfig{{Name: "main", Provider: "p", Model: "claude-opus-5"}},
		Builtin: &golm.BuiltinConfig{
			Run: &golm.RunConfig{Allow: []string{"go", "git"}, DenyArgs: []string{"-c"}},
		},
	}
	path := filepath.Join(t.TempDir(), "golm.json")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	got := (&backendFlags{}).runAllowList(path)
	if len(got) != 2 || got[0] != "go" {
		t.Errorf("allow list = %v", got)
	}
	if deny := (&backendFlags{}).runDenyArgs(path); len(deny) != 1 || deny[0] != "-c" {
		t.Errorf("deny args = %v", deny)
	}

	flagged := (&backendFlags{allowRun: stringList{"ls"}}).runAllowList(path)
	if len(flagged) != 1 || flagged[0] != "ls" {
		t.Errorf("--allow-run did not take precedence: %v", flagged)
	}

	if got := (&backendFlags{}).runAllowList(""); len(got) != 0 {
		t.Errorf("allow list = %v with no config", got)
	}
}
