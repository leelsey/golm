// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"strings"
	"testing"
	"time"
)

func persona(p PersonaConfig) *Config {
	p.Name, p.Provider, p.Model = "a", "cli", "cli"
	return &Config{
		Providers: []ProviderConfig{{Name: "cli", Type: "cli", Command: "cat"}},
		Agents:    []PersonaConfig{p},
	}
}

func TestValidatePersonaBounds(t *testing.T) {
	cases := []struct {
		name string
		p    PersonaConfig
		want string
	}{
		{"negative max_steps", PersonaConfig{MaxSteps: -1}, "max_steps"},
		{"negative max_total_tokens", PersonaConfig{MaxTotalTokens: -5}, "max_total_tokens"},
		{"negative max_tool_result_bytes", PersonaConfig{MaxToolResultBytes: -1}, "max_tool_result_bytes"},
		{"negative keep_last", PersonaConfig{KeepLast: -2}, "keep_last"},
		{"bad tool_timeout", PersonaConfig{ToolTimeout: "half an hour"}, "tool_timeout"},
		{"negative tool_timeout", PersonaConfig{ToolTimeout: "-30s"}, "tool_timeout"},
		{"bad cache ttl", PersonaConfig{CachePromptTTL: "5m"}, "cache_prompt_ttl"},
		{"good bounds", PersonaConfig{MaxSteps: 12, MaxTotalTokens: 100000, ToolTimeout: "45s", CachePromptTTL: "1h", KeepLast: 20}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := persona(tc.p).Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.want == "":
			case err == nil:
				t.Fatalf("Validate() = nil, want an error naming %q", tc.want)
			case !strings.Contains(err.Error(), tc.want):
				t.Errorf("Validate() = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

func TestValidateCompaction(t *testing.T) {
	cases := []struct {
		name string
		p    PersonaConfig
		want string
	}{
		{"no threshold", PersonaConfig{Compaction: &CompactionConfig{KeepLast: 10}}, "no threshold"},
		{"keeps nothing", PersonaConfig{Compaction: &CompactionConfig{AtMessages: 40}}, "keep_last"},
		{"threshold below keep_last", PersonaConfig{Compaction: &CompactionConfig{AtMessages: 10, KeepLast: 10}}, "can never drop anything"},
		{"bad timeout", PersonaConfig{Compaction: &CompactionConfig{AtMessages: 40, KeepLast: 10, Timeout: "soon"}}, "compaction.timeout"},
		{"with keep_last too", PersonaConfig{KeepLast: 20, Compaction: &CompactionConfig{AtMessages: 40, KeepLast: 10}}, "keep_last and compaction"},
		{"by message count", PersonaConfig{Compaction: &CompactionConfig{AtMessages: 40, KeepLast: 10}}, ""},
		{"by input tokens", PersonaConfig{Compaction: &CompactionConfig{AtInputTokens: 120000, KeepLast: 10}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := persona(tc.p).Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.want == "":
			case err == nil:
				t.Fatalf("Validate() = nil, want an error naming %q", tc.want)
			case !strings.Contains(err.Error(), tc.want):
				t.Errorf("Validate() = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

func TestPersonaDurations(t *testing.T) {
	p := PersonaConfig{ToolTimeout: "90s", Compaction: &CompactionConfig{Timeout: "2m"}}
	if got := p.ToolTimeoutDuration(); got != 90*time.Second {
		t.Errorf("ToolTimeoutDuration() = %v, want 90s", got)
	}
	if got := p.Compaction.TimeoutDuration(); got != 2*time.Minute {
		t.Errorf("compaction TimeoutDuration() = %v, want 2m", got)
	}
	var none *CompactionConfig
	if got := none.TimeoutDuration(); got != 0 {
		t.Errorf("nil compaction TimeoutDuration() = %v, want 0", got)
	}
	if got := (PersonaConfig{}).ToolTimeoutDuration(); got != 0 {
		t.Errorf("unset ToolTimeoutDuration() = %v, want 0", got)
	}
}

func TestPersonaSystemPromptTiers(t *testing.T) {
	if sp := (PersonaConfig{System: "only"}).SystemPrompt(); sp != nil {
		t.Errorf("SystemPrompt() with no sections = %v, want nil", sp)
	}

	p := PersonaConfig{System: "lead", SystemSections: []string{"identity", "", "project", "today"}}
	sp := p.SystemPrompt()
	secs := sp.Sections()
	if len(secs) != 3 {
		t.Fatalf("sections = %d, want 3 (the empty one dropped): %v", len(secs), secs)
	}

	if secs[0].Text != "identity" {
		t.Errorf("first section = %q, want %q", secs[0].Text, "identity")
	}

	if !secs[0].Cache || !secs[1].Cache {
		t.Errorf("want a breakpoint after each leading tier, got %v", secs)
	}
	if secs[2].Cache {
		t.Error("want no breakpoint after the final tier")
	}
}

func TestValidateBuiltin(t *testing.T) {
	base := func(b *BuiltinConfig) *Config {
		c := persona(PersonaConfig{})
		c.Builtin = b
		return c
	}
	cases := []struct {
		name string
		b    *BuiltinConfig
		want string
	}{
		{"none", nil, ""},
		{"workspace", &BuiltinConfig{Workspace: "/tmp"}, ""},
		{"read-only without a workspace", &BuiltinConfig{WorkspaceReadOnly: true}, "no workspace"},
		{"run without an allowlist", &BuiltinConfig{Run: &RunConfig{}}, "no default"},
		{"run with an empty entry", &BuiltinConfig{Run: &RunConfig{Allow: []string{"git", " "}}}, "empty entry"},
		{"run with a bad timeout", &BuiltinConfig{Run: &RunConfig{Allow: []string{"git"}, Timeout: "soon"}}, "builtin.run.timeout"},
		{"run", &BuiltinConfig{Run: &RunConfig{Allow: []string{"git"}, Timeout: "30s"}}, ""},
		{"fetch with a bad timeout", &BuiltinConfig{Fetch: &FetchConfig{Timeout: "later"}}, "builtin.fetch.timeout"},
		{"fetch with negative bytes", &BuiltinConfig{Fetch: &FetchConfig{MaxBytes: -1}}, "max_bytes"},
		{"fetch", &BuiltinConfig{Fetch: &FetchConfig{Timeout: "20s", MaxBytes: 1024}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := base(tc.b).Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.want == "":
			case err == nil:
				t.Fatalf("Validate() = nil, want an error naming %q", tc.want)
			case !strings.Contains(err.Error(), tc.want):
				t.Errorf("Validate() = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

func TestBuiltinTimeoutDurations(t *testing.T) {
	f := &FetchConfig{Timeout: "20s"}
	if got := f.TimeoutDuration(); got != 20*time.Second {
		t.Errorf("fetch timeout = %v, want 20s", got)
	}
	r := &RunConfig{Timeout: "90s"}
	if got := r.TimeoutDuration(); got != 90*time.Second {
		t.Errorf("run timeout = %v, want 90s", got)
	}
	var nilFetch *FetchConfig
	var nilRun *RunConfig
	if nilFetch.TimeoutDuration() != 0 || nilRun.TimeoutDuration() != 0 {
		t.Error("a nil block should have no timeout rather than panic")
	}
}
