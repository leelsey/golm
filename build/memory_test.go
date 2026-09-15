// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package build_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/build"
)

func memoryConfig(dir string) *golm.Config {
	return &golm.Config{
		DefaultAgent: "main",
		Memory:       dir,
		Providers:    []golm.ProviderConfig{{Name: "p", Type: "anthropic", APIKeyEnv: "X_KEY"}},
		Agents: []golm.PersonaConfig{{
			Name: "main", Provider: "p", Model: "claude-opus-5",
			SystemSections: []string{"project context that changes often"},
		}},
	}
}

// Memory reaches the prompt, behind the skills index and ahead of the persona's own sections.
func TestMemoryReachesThePromptInTheStableTier(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- the gate is /lint\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	skillDir := filepath.Join(t.TempDir(), "audit")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: audit\ndescription: review a diff\n---\nbody\n"), 0o600); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	rt, err := build.New(context.Background(), build.Options{
		Config:    memoryConfig(dir),
		SkillDirs: []string{filepath.Dir(skillDir)},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()

	secs := rt.Agent.SystemPrompt.Sections()
	if len(secs) < 3 {
		t.Fatalf("got %d prompt sections, want the skills index, memory and the persona's own", len(secs))
	}
	if !strings.Contains(secs[0].Text, "Available skills") {
		t.Errorf("section 0 should be the skills index: %q", first(secs[0].Text))
	}
	if !strings.Contains(secs[1].Text, "the gate is /lint") {
		t.Errorf("section 1 should be memory: %q", first(secs[1].Text))
	}
	if !strings.Contains(secs[2].Text, "changes often") {
		t.Errorf("section 2 should be the persona's own: %q", first(secs[2].Text))
	}

	if !secs[0].Cache || !secs[1].Cache {
		t.Errorf("stable tiers are missing cache breakpoints: %v, %v", secs[0].Cache, secs[1].Cache)
	}
	if rt.Memory == nil {
		t.Error("Runtime should expose the memory store")
	}
}

func TestMemoryToolsAreRegistered(t *testing.T) {
	rt, err := build.New(context.Background(), build.Options{Config: memoryConfig(t.TempDir())})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	for _, want := range []string{"remember", "forget"} {
		if _, ok := rt.Tools.Get(want); !ok {
			t.Errorf("no %q tool in the registry", want)
		}
		if _, ok := rt.Agent.Tools.Get(want); !ok {
			t.Errorf("the agent did not get %q", want)
		}
	}
}

func TestNoMemoryConfiguredAddsNothing(t *testing.T) {
	cfg := memoryConfig("")
	rt, err := build.New(context.Background(), build.Options{Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	if rt.Memory != nil {
		t.Error("memory store built for a config that names no directory")
	}
	if _, ok := rt.Tools.Get("remember"); ok {
		t.Error("memory tools offered without memory configured")
	}
}

// The embedder's directory outranks the file's, like every other override.
func TestMemoryDirOptionOverridesTheConfig(t *testing.T) {
	chosen := t.TempDir()
	if err := os.WriteFile(filepath.Join(chosen, "MEMORY.md"), []byte("- from the embedder\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	rt, err := build.New(context.Background(), build.Options{
		Config: memoryConfig(t.TempDir()), MemoryDir: chosen,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	if rt.Memory.Dir() != chosen {
		t.Errorf("memory dir = %q, want %q", rt.Memory.Dir(), chosen)
	}
}

func first(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Asking for a record of what the agent did and receiving an empty file.
func TestAuditAloneStillProducesAGate(t *testing.T) {
	var seen []string
	rt, err := build.New(context.Background(), build.Options{
		Config: memoryConfig(t.TempDir()),
		Audit: func(r golm.ToolRequest, d golm.Decision, err error) {
			seen = append(seen, r.Call.Name+"="+string(d))
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	if rt.Agent.ToolPolicy == nil {
		t.Fatal("--audit with no rules attached no policy, so nothing is ever recorded")
	}
	tool, ok := rt.Tools.Get("remember")
	if !ok {
		t.Fatal("no tool to exercise")
	}
	ctx, err := rt.Agent.ToolPolicy(context.Background(), golm.ToolRequest{
		Tool: tool, Call: golm.ToolUse{Name: "remember"},
	})
	if err != nil || ctx == nil {
		t.Fatalf("a record-only policy must still allow: %v", err)
	}
	if len(seen) != 1 || seen[0] != "remember=allow" {
		t.Errorf("audit saw %v, want the allowed call recorded", seen)
	}
}

func TestNoPolicyAndNoAuditLeavesToolsUngated(t *testing.T) {
	rt, err := build.New(context.Background(), build.Options{Config: memoryConfig(t.TempDir())})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	if rt.Agent.ToolPolicy != nil {
		t.Error("a deployment that configured no gate should have none")
	}
}
