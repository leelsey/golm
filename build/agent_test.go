// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package build

import (
	"context"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

var parallelOn = true

func cliCfg(p golm.PersonaConfig) *golm.Config {
	return &golm.Config{
		Providers: []golm.ProviderConfig{{Name: "local", Type: "cli", Command: "cat"}},
		Agents:    []golm.PersonaConfig{p},
	}
}

// Every persona field has to reach the Agent.
func TestAgentCarriesEveryPersonaField(t *testing.T) {
	temp := 0.0
	cfg := cliCfg(golm.PersonaConfig{
		Name: "main", Provider: "local", Model: "cli",
		System:             "lead",
		SystemSections:     []string{"identity", "project"},
		MaxTokens:          2048,
		Temperature:        &temp,
		Thinking:           "auto",
		Effort:             "HIGH",
		Safety:             "None",
		ToolChoice:         "",
		ResponseModalities: []string{"audio"},
		MaxSteps:           12,
		MaxTotalTokens:     99000,
		ToolTimeout:        "45s",
		MaxToolResultBytes: 4096,
		ParallelTools:      &parallelOn,
		MaxParallelTools:   3,
		CachePrompt:        true,
		CachePromptTTL:     "1h",
		KeepLast:           30,
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	ag, err := Agent(context.Background(), cfg, "main", nil, nil)
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}
	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"System", ag.System, "lead"},
		{"SystemPrompt sections", len(ag.SystemPrompt.Sections()), 2},
		{"MaxTokens", ag.MaxTokens, 2048},
		{"Temperature", *ag.Temperature, 0.0},
		{"Thinking.Mode", ag.Thinking.Mode, golm.ThinkingAuto},
		{"Effort", ag.Effort, golm.EffortHigh},
		{"Safety", ag.Safety, golm.SafetyNone},
		{"ResponseModalities", len(ag.ResponseModalities), 1},
		{"MaxSteps", ag.MaxSteps, 12},
		{"MaxTotalTokens", ag.MaxTotalTokens, 99000},
		{"ToolTimeout", ag.ToolTimeout, 45 * time.Second},
		{"MaxToolResultBytes", ag.MaxToolResultBytes, 4096},
		{"ParallelTools", ag.ParallelTools, true},
		{"MaxParallelTools", ag.MaxParallelTools, 3},
		{"CachePrompt", ag.CachePrompt, true},
		{"CachePromptTTL", ag.CachePromptTTL, golm.CacheTTL1h},
		{"KeepLast", ag.KeepLast, 30},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
	if ag.Compaction != nil {
		t.Error("Compaction should be nil when the persona declares none")
	}
}

func TestAgentBuildsCompactionPolicy(t *testing.T) {
	cfg := cliCfg(golm.PersonaConfig{
		Name: "main", Provider: "local", Model: "cli",
		Compaction: &golm.CompactionConfig{AtMessages: 60, AtInputTokens: 120000, KeepLast: 12, Timeout: "90s"},
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	ag, err := Agent(context.Background(), cfg, "main", nil, nil)
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}
	p := ag.Compaction
	if p == nil {
		t.Fatal("Compaction not wired")
	}
	if p.AtMessages != 60 || p.AtInputTokens != 120000 || p.KeepLast != 12 {
		t.Errorf("policy = %+v, want the configured thresholds", p)
	}
	if p.Timeout != 90*time.Second {
		t.Errorf("Timeout = %v, want 90s", p.Timeout)
	}
	if p.Summarise == nil {
		t.Error("Summarise must be built from the persona's provider; a nil one makes Compact an archiving trim")
	}

	if p.Archive != nil {
		t.Error("Archive should be left to the caller")
	}
}

func TestAgentRejectsUnknownToolAndAgent(t *testing.T) {
	cfg := cliCfg(golm.PersonaConfig{Name: "main", Provider: "local", Model: "cli", Tools: []string{"nope"}})
	if _, err := Agent(context.Background(), cfg, "main", golm.NewRegistry(), nil); err == nil {
		t.Error("a persona referencing an unregistered tool should fail to build")
	}
	if _, err := Agent(context.Background(), cfg, "absent", nil, nil); err == nil {
		t.Error("an undefined agent name should fail to build")
	}
	bad := &golm.Config{
		Providers: []golm.ProviderConfig{{Name: "local", Type: "cli", Command: "cat"}},
		Agents:    []golm.PersonaConfig{{Name: "main", Provider: "ghost", Model: "cli"}},
	}
	if _, err := Agent(context.Background(), bad, "main", nil, nil); err == nil {
		t.Error("an undefined provider should fail to build")
	}
}

func TestOrchestratorWiresSubAgentsAsTools(t *testing.T) {
	cfg := &golm.Config{
		Providers: []golm.ProviderConfig{{Name: "local", Type: "cli", Command: "cat"}},
		Agents: []golm.PersonaConfig{
			{Name: "lead", Provider: "local", Model: "cli", Role: "main"},
			{Name: "researcher", Provider: "local", Model: "cli", Role: "sub", Description: "digs things up"},
			{Name: "writer", Provider: "local", Model: "cli", Role: "sub"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	bus := golm.NewBus()
	defer bus.Close()
	o, err := Orchestrator(context.Background(), cfg, bus, golm.NewRegistry(), nil)
	if err != nil {
		t.Fatalf("Orchestrator: %v", err)
	}
	main := o.Main()
	if main == nil {
		t.Fatal("no main agent")
	}
	if main.Name != "lead" {
		t.Errorf("main = %q, want the role:main persona", main.Name)
	}
	got := map[string]string{}
	for _, d := range main.Tools.Defs() {
		got[d.Name] = d.Description
	}
	if len(got) != 2 {
		t.Fatalf("delegation tools = %v, want one per sub-agent", got)
	}
	if got["researcher"] != "digs things up" {
		t.Errorf("researcher description = %q, want the persona's", got["researcher"])
	}
	if got["writer"] == "" {
		t.Error("a sub-agent with no description still needs a blurb")
	}
	if _, ok := got["lead"]; ok {
		t.Error("main must not be wired onto itself")
	}
	if !main.ParallelTools {
		t.Error("delegation wiring should enable concurrent tool execution")
	}

	for _, name := range []string{"lead", "researcher", "writer"} {
		ag, ok := o.Get(name)
		if !ok {
			t.Fatalf("agent %q missing", name)
		}
		if ag.Bus != bus {
			t.Errorf("agent %q not on the bus", name)
		}
	}
}

func TestOrchestratorPropagatesABadPersona(t *testing.T) {
	cfg := &golm.Config{
		Providers: []golm.ProviderConfig{{Name: "local", Type: "cli", Command: "cat"}},
		Agents:    []golm.PersonaConfig{{Name: "lead", Provider: "ghost", Model: "cli"}},
	}
	if _, err := Orchestrator(context.Background(), cfg, golm.NewBus(), nil, nil); err == nil {
		t.Error("a persona that cannot be built should fail the whole orchestrator")
	}
}

func TestProviderRejectsAnUnknownType(t *testing.T) {
	if _, err := Provider(context.Background(), golm.ProviderConfig{Name: "x", Type: "telepathy"}, nil); err == nil {
		t.Error("an unknown provider type should fail")
	}
}

func TestProviderBuildsEachType(t *testing.T) {
	for _, tc := range []struct{ typ, want string }{
		{"anthropic", "anthropic"},
		{"openai", "openai"},
		{"google", "google"},
	} {
		p, err := Provider(context.Background(), golm.ProviderConfig{Name: tc.typ, Type: tc.typ, APIKeyEnv: "GOLM_TEST_ABSENT_KEY", BaseURL: "http://127.0.0.1:1/v1"}, nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.typ, err)
		}
		if p.Name() != tc.want {
			t.Errorf("%s Name() = %q, want %q", tc.typ, p.Name(), tc.want)
		}
	}
	p, err := Provider(context.Background(), golm.ProviderConfig{Name: "local", Type: "cli", Command: "cat", PromptVia: "stdin"}, nil)
	if err != nil {
		t.Fatalf("cli: %v", err)
	}
	if p.Name() != "local" {
		t.Errorf("cli Name() = %q, want the configured name", p.Name())
	}
}

func TestProviderSurfacesAFailingKeyCmd(t *testing.T) {
	_, err := Provider(context.Background(), golm.ProviderConfig{
		Name: "x", Type: "anthropic", APIKeyCmd: "definitely-not-a-command-golm",
	}, nil)
	if err == nil {
		t.Fatal("a key command that cannot run should fail the build, not yield an empty key")
	}
}
