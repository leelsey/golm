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

func writeSkill(t *testing.T, dir, name, desc, body string) {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: " + name + "\ndescription: " + desc + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mcpConfig(t *testing.T) *golm.Config {
	t.Helper()
	t.Setenv(mcpServerEnv, "1")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &golm.Config{
		Providers: []golm.ProviderConfig{{Name: "local", Type: "openai", BaseURL: "http://127.0.0.1:9/v1", APIKeyEnv: "GOLM_TEST_ABSENT"}},
		Agents:    []golm.PersonaConfig{{Name: "main", Provider: "local", Model: "m"}},
		MCPServers: []golm.MCPServerConfig{{Name: "probe", Command: self,
			InheritEnv: []string{mcpServerEnv}}},
	}
}

// A persona naming an MCP tool used to be impossible.
func TestPersonaCanNameAnMCPTool(t *testing.T) {
	cfg := mcpConfig(t)
	cfg.Agents[0].Tools = []string{"ping"}

	rt, err := New(context.Background(), Options{Config: cfg, Agent: "main", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	if _, ok := rt.Agent.Tools.Get("ping"); !ok {
		t.Fatalf("agent tools = %v, want the named MCP tool", toolNames(rt.Agent.Tools))
	}
}

func TestPersonaWithoutAToolListGetsEverything(t *testing.T) {
	cfg := mcpConfig(t)
	dir := t.TempDir()
	writeSkill(t, dir, "triage", "sort incoming reports", "the instructions")
	cfg.Skills = []string{dir}

	rt, err := New(context.Background(), Options{Config: cfg, Agent: "main", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	got := toolNames(rt.Agent.Tools)
	for _, want := range []string{"ping", "read_skill"} {
		if !slices.Contains(got, want) {
			t.Errorf("agent tools = %v, want %q among them", got, want)
		}
	}
}

func TestPersonaCanOptOutOfTools(t *testing.T) {
	cfg := mcpConfig(t)
	cfg.Agents[0].Tools = []string{}

	rt, err := New(context.Background(), Options{Config: cfg, Agent: "main", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	if n := len(rt.Agent.Tools.List()); n != 0 {
		t.Errorf("agent tools = %v, want none", toolNames(rt.Agent.Tools))
	}

	if _, ok := rt.Tools.Get("ping"); !ok {
		t.Error("the runtime should still hold the tools the persona declined")
	}
}

// The laziness has to survive the assembly.
func TestConfiguredMCPServerIsNotStartedToBuildAnAgent(t *testing.T) {
	cfg := mcpConfig(t)
	cache := t.TempDir()

	first, err := New(context.Background(), Options{Config: cfg, Agent: "main", CacheDir: cache})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first.Close()

	second, err := New(context.Background(), Options{Config: cfg, Agent: "main", CacheDir: cache})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer second.Close()
	if _, ok := second.Agent.Tools.Get("ping"); !ok {
		t.Fatal("the tool should be offered from the manifest")
	}
	for _, r := range second.Remotes {
		if r.Connected() {
			t.Errorf("server %q was started merely to build an agent", r.Name)
		}
	}
}

func TestSkillsIndexLeadsThePrompt(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "release", "cut a release", "body")
	cfg := &golm.Config{
		Providers: []golm.ProviderConfig{{Name: "local", Type: "cli", Command: "cat"}},
		Agents: []golm.PersonaConfig{{
			Name: "main", Provider: "local", Model: "cli",
			System:         "you are golm",
			SystemSections: []string{"project context", "today is friday"},
		}},
		Skills: []string{dir},
	}
	rt, err := New(context.Background(), Options{Config: cfg, Agent: "main"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()

	secs := rt.Agent.SystemPrompt.Sections()
	if len(secs) != 3 {
		t.Fatalf("sections = %d, want the skills index plus the persona's two: %v", len(secs), secs)
	}
	if !strings.Contains(secs[0].Text, "release: cut a release") {
		t.Errorf("first section = %q, want the skills index", secs[0].Text)
	}

	if !secs[0].Cache {
		t.Error("the skills index should end a cacheable tier")
	}
	if strings.Contains(secs[0].Text, "you are golm") {
		t.Error("System is the agent's to prepend; folding it in here sends it twice")
	}
}

func TestNoSkillsMeansNoPromptChange(t *testing.T) {
	cfg := &golm.Config{
		Providers: []golm.ProviderConfig{{Name: "local", Type: "cli", Command: "cat"}},
		Agents:    []golm.PersonaConfig{{Name: "main", Provider: "local", Model: "cli", System: "plain"}},
	}
	rt, err := New(context.Background(), Options{Config: cfg, Agent: "main"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	if len(rt.Agent.SystemPrompt) != 0 {
		t.Errorf("SystemPrompt = %v, want it untouched when no skills are configured", rt.Agent.SystemPrompt)
	}
	if rt.Agent.System != "plain" {
		t.Errorf("System = %q", rt.Agent.System)
	}
}

func TestNewUsesTheDefaultAgent(t *testing.T) {
	cfg := &golm.Config{
		DefaultAgent: "second",
		Providers:    []golm.ProviderConfig{{Name: "local", Type: "cli", Command: "cat"}},
		Agents: []golm.PersonaConfig{
			{Name: "first", Provider: "local", Model: "cli"},
			{Name: "second", Provider: "local", Model: "m2"},
		},
	}
	rt, err := New(context.Background(), Options{Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer rt.Close()
	if rt.Agent.Model != "m2" {
		t.Errorf("model = %q, want the default agent's", rt.Agent.Model)
	}

	cfg.DefaultAgent = ""
	if _, err := New(context.Background(), Options{Config: cfg}); err == nil {
		t.Error("no agent named and no default should be an error, not a guess")
	}
	if _, err := New(context.Background(), Options{}); err == nil {
		t.Error("no config should be an error")
	}
}

func TestOrchestratorGivesEveryAgentTheToolSet(t *testing.T) {
	cfg := mcpConfig(t)
	cfg.Agents = []golm.PersonaConfig{
		{Name: "lead", Provider: "local", Model: "cli", Role: "main"},
		{Name: "helper", Provider: "local", Model: "cli", Role: "sub"},
	}
	bus := golm.NewBus()
	defer bus.Close()
	rt, orc, err := NewOrchestrator(context.Background(), Options{Config: cfg, CacheDir: t.TempDir()}, bus)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	defer rt.Close()

	helper, ok := orc.Get("helper")
	if !ok {
		t.Fatal("helper missing")
	}
	if _, ok := helper.Tools.Get("ping"); !ok {
		t.Errorf("sub-agent tools = %v, want the MCP tool", toolNames(helper.Tools))
	}

	main := orc.Main()
	if _, ok := main.Tools.Get("helper"); !ok {
		t.Errorf("main tools = %v, want the delegation tool", toolNames(main.Tools))
	}
	if _, ok := main.Tools.Get("ping"); !ok {
		t.Errorf("main tools = %v, want the MCP tool", toolNames(main.Tools))
	}
}

func toolNames(r *golm.Registry) []string {
	if r == nil {
		return nil
	}
	out := []string{}
	for _, t := range r.List() {
		out = append(out, t.Name())
	}
	return out
}
