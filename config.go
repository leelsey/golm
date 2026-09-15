// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is the JSON configuration for a GoLM deployment.
type Config struct {
	DefaultAgent string            `json:"default_agent,omitempty"`
	Providers    []ProviderConfig  `json:"providers"`
	Agents       []PersonaConfig   `json:"agents"`
	MCPServers   []MCPServerConfig `json:"mcp_servers,omitempty"`
	A2AAgents    []A2AAgentConfig  `json:"a2a_agents,omitempty"`
	Bus          string            `json:"bus,omitempty"`
	BusTokenEnv  string            `json:"bus_token_env,omitempty"`

	Skills []string `json:"skills,omitempty"`

	Builtin *BuiltinConfig `json:"builtin,omitempty"`

	Memory string `json:"memory,omitempty"`

	Policy *PolicyConfig `json:"policy,omitempty"`

	Orchestration *OrchestrationConfig `json:"orchestration,omitempty"`
}

// PolicyConfig is the configured form of PolicyRules.
type PolicyConfig struct {
	Default string `json:"default,omitempty"`

	Unreviewed string `json:"unreviewed,omitempty"`

	Deny  []string `json:"deny,omitempty"`
	Ask   []string `json:"ask,omitempty"`
	Allow []string `json:"allow,omitempty"`

	DenyTraits []string `json:"deny_traits,omitempty"`
	AskTraits  []string `json:"ask_traits,omitempty"`
}

// Rules compiles the config into the runtime form.
func (pc *PolicyConfig) Rules() *PolicyRules {
	if pc == nil {
		return nil
	}
	return &PolicyRules{
		Default:    Decision(strings.ToLower(strings.TrimSpace(pc.Default))),
		Unreviewed: Decision(strings.ToLower(strings.TrimSpace(pc.Unreviewed))),
		Deny:       pc.Deny,
		Ask:        pc.Ask,
		Allow:      pc.Allow,
		DenyTraits: pc.DenyTraits,
		AskTraits:  pc.AskTraits,
	}
}

// NeedsApprover reports whether any rule can reach DecisionAsk.
func (pc *PolicyConfig) NeedsApprover() bool {
	if pc == nil {
		return false
	}
	if strings.EqualFold(pc.Default, string(DecisionAsk)) || strings.EqualFold(pc.Unreviewed, string(DecisionAsk)) {
		return true
	}
	return len(pc.Ask) > 0 || len(pc.AskTraits) > 0
}

// Validate checks one policy block.
func (pc *PolicyConfig) Validate(where string) error {
	if pc == nil {
		return nil
	}
	for field, v := range map[string]string{"default": pc.Default, "unreviewed": pc.Unreviewed} {
		if !ValidDecision(v) {
			return fmt.Errorf("golm: %spolicy.%s is %q (want allow, deny or ask)", where, field, v)
		}
	}
	for field, list := range map[string][]string{"deny_traits": pc.DenyTraits, "ask_traits": pc.AskTraits} {
		for _, t := range list {
			if !ValidTrait(t) {
				return fmt.Errorf("golm: %spolicy.%s names unknown trait %q (want read_only, filesystem, network or process)", where, field, t)
			}
		}
	}

	seen := map[string]string{}
	for list, names := range map[string][]string{"deny": pc.Deny, "ask": pc.Ask, "allow": pc.Allow} {
		for _, n := range names {
			if strings.TrimSpace(n) == "" {
				return fmt.Errorf("golm: %spolicy.%s holds an empty entry", where, list)
			}
			if prev, dup := seen[n]; dup {
				return fmt.Errorf("golm: %spolicy names %q in both %s and %s", where, n, prev, list)
			}
			seen[n] = list
		}
	}
	return nil
}

// BuiltinConfig declares which of the built-in tools an agent gets.
type BuiltinConfig struct {
	Workspace string `json:"workspace,omitempty"`

	WorkspaceReadOnly bool `json:"workspace_read_only,omitempty"`

	Fetch *FetchConfig `json:"fetch,omitempty"`

	Run *RunConfig `json:"run,omitempty"`
}

// FetchConfig configures outbound HTTP.
type FetchConfig struct {
	AllowPrivate bool `json:"allow_private,omitempty"`

	Timeout string `json:"timeout,omitempty"`

	MaxBytes int64 `json:"max_bytes,omitempty"`
}

// RunConfig configures process execution.
type RunConfig struct {
	Allow []string `json:"allow"`

	Timeout string `json:"timeout,omitempty"`

	InheritEnv []string `json:"inherit_env,omitempty"`

	DenyArgs []string `json:"deny_args,omitempty"`

	MaxArgs int `json:"max_args,omitempty"`
}

// TimeoutDuration returns the parsed timeout.
func (c *FetchConfig) TimeoutDuration() time.Duration {
	if c == nil {
		return 0
	}
	d, _ := parseOptionalDuration(c.Timeout)
	return d
}

// TimeoutDuration returns the parsed timeout.
func (c *RunConfig) TimeoutDuration() time.Duration {
	if c == nil {
		return 0
	}
	d, _ := parseOptionalDuration(c.Timeout)
	return d
}

// Validate checks the built-in tool block.
func (b *BuiltinConfig) Validate() error {
	if b == nil {
		return nil
	}
	if b.WorkspaceReadOnly && b.Workspace == "" {
		return fmt.Errorf("golm: builtin.workspace_read_only is set with no workspace")
	}
	if b.Fetch != nil {
		if _, err := parseOptionalDuration(b.Fetch.Timeout); err != nil {
			return fmt.Errorf("golm: invalid builtin.fetch.timeout %q: %w", b.Fetch.Timeout, err)
		}
		if b.Fetch.MaxBytes < 0 {
			return fmt.Errorf("golm: builtin.fetch.max_bytes is negative")
		}
	}
	if b.Run != nil {
		if len(b.Run.Allow) == 0 {
			return fmt.Errorf("golm: builtin.run needs a non-empty allow list; there is no default")
		}
		for _, a := range b.Run.Allow {
			if strings.TrimSpace(a) == "" {
				return fmt.Errorf("golm: builtin.run.allow holds an empty entry")
			}
		}
		if _, err := parseOptionalDuration(b.Run.Timeout); err != nil {
			return fmt.Errorf("golm: invalid builtin.run.timeout %q: %w", b.Run.Timeout, err)
		}
		if b.Run.MaxArgs < 0 {
			return fmt.Errorf("golm: builtin.run.max_args is negative")
		}
		for _, a := range b.Run.DenyArgs {
			if strings.TrimSpace(a) == "" {
				return fmt.Errorf("golm: builtin.run.deny_args holds an empty entry, which would block every argument")
			}
		}
	}
	return nil
}

// MCPServerConfig declares an external MCP server to connect to over stdio.
type MCPServerConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`

	InheritEnv []string `json:"inherit_env,omitempty"`
}

// A2AAgentConfig declares a remote A2A agent to call.
type A2AAgentConfig struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`

	TokenEnv string `json:"token_env,omitempty"`
}

// ProviderConfig declares one LLM backend.
type ProviderConfig struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	APIKeyCmd string `json:"api_key_cmd,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`

	ToolTags string `json:"tool_tags,omitempty"`

	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	PromptVia string   `json:"prompt_via,omitempty"`
}

// PersonaConfig declares one agent persona.
type PersonaConfig struct {
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	System      string `json:"system,omitempty"`
	Role        string `json:"role,omitempty"`
	Description string `json:"description,omitempty"`

	Tools     []string `json:"tools,omitempty"`
	MaxTokens int      `json:"max_tokens,omitempty"`

	Temperature    *float64 `json:"temperature,omitempty"`
	Thinking       string   `json:"thinking,omitempty"`
	ThinkingBudget int      `json:"thinking_budget,omitempty"`

	Effort string `json:"effort,omitempty"`

	Safety string `json:"safety,omitempty"`

	ToolChoice string `json:"tool_choice,omitempty"`

	ResponseModalities []string `json:"response_modalities,omitempty"`

	SystemSections []string `json:"system_sections,omitempty"`

	MaxSteps int `json:"max_steps,omitempty"`

	MaxTotalTokens int `json:"max_total_tokens,omitempty"`

	ToolTimeout string `json:"tool_timeout,omitempty"`

	MaxToolResultBytes int `json:"max_tool_result_bytes,omitempty"`

	ParallelTools    *bool `json:"parallel_tools,omitempty"`
	MaxParallelTools int   `json:"max_parallel_tools,omitempty"`

	CachePrompt    bool   `json:"cache_prompt,omitempty"`
	CachePromptTTL string `json:"cache_prompt_ttl,omitempty"`

	Policy *PolicyConfig `json:"policy,omitempty"`

	KeepLast int `json:"keep_last,omitempty"`

	Compaction *CompactionConfig `json:"compaction,omitempty"`
}

// CompactionConfig declares when and how a persona's transcript is compacted.
type CompactionConfig struct {
	AtMessages int `json:"at_messages,omitempty"`

	AtInputTokens int `json:"at_input_tokens,omitempty"`

	KeepLast int `json:"keep_last"`

	Model string `json:"model,omitempty"`

	Prompt string `json:"prompt,omitempty"`

	Timeout string `json:"timeout,omitempty"`
}

// Parallel reports whether the persona asked for concurrent tool execution.
func (p PersonaConfig) Parallel() (on, set bool) {
	if p.ParallelTools == nil {
		return false, false
	}
	return *p.ParallelTools, true
}

// ThinkingConfig converts the persona's thinking settings to a ThinkingConfig.
func (p PersonaConfig) ThinkingConfig() ThinkingConfig {
	switch strings.ToLower(p.Thinking) {
	case "auto":
		return ThinkingConfig{Mode: ThinkingAuto}
	case "budget":
		return ThinkingConfig{Mode: ThinkingBudget, Budget: p.ThinkingBudget}
	case "disabled":
		return ThinkingConfig{Mode: ThinkingDisabled}
	default:
		return ThinkingConfig{Mode: ThinkingOff}
	}
}

var (
	validThinking = map[string]bool{"": true, "off": true, "auto": true, "budget": true, "disabled": true}
	validEffort   = map[string]bool{"": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	validSafety   = map[string]bool{"": true, "low": true, "none": true}
	validCacheTTL = map[string]bool{"": true, "1h": true}
	validToolTags = map[string]bool{"": true, "hermes": true}
)

func parseOptionalDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("must not be negative")
	}
	return d, nil
}

func (cc *CompactionConfig) validate(agent string) error {
	if cc == nil {
		return nil
	}
	if cc.AtMessages < 0 || cc.AtInputTokens < 0 {
		return fmt.Errorf("golm: agent %q has a negative compaction threshold", agent)
	}
	if cc.AtMessages == 0 && cc.AtInputTokens == 0 {
		return fmt.Errorf("golm: agent %q declares compaction with no threshold; set at_messages or at_input_tokens", agent)
	}
	if cc.KeepLast <= 0 {
		return fmt.Errorf("golm: agent %q has compaction.keep_last %d; a compaction that keeps nothing is a destructive trim", agent, cc.KeepLast)
	}
	if cc.AtMessages > 0 && cc.AtMessages <= cc.KeepLast {
		return fmt.Errorf("golm: agent %q has compaction.at_messages %d at or below keep_last %d, so it can never drop anything", agent, cc.AtMessages, cc.KeepLast)
	}
	if _, err := parseOptionalDuration(cc.Timeout); err != nil {
		return fmt.Errorf("golm: agent %q has invalid compaction.timeout %q: %w", agent, cc.Timeout, err)
	}
	return nil
}

// ToolTimeoutDuration and CompactionTimeout return the parsed durations.
func (p PersonaConfig) ToolTimeoutDuration() time.Duration {
	d, _ := parseOptionalDuration(p.ToolTimeout)
	return d
}

// TimeoutDuration returns the parsed timeout.
func (cc *CompactionConfig) TimeoutDuration() time.Duration {
	if cc == nil {
		return 0
	}
	d, _ := parseOptionalDuration(cc.Timeout)
	return d
}

// SystemPrompt turns SystemSections into an ordered prompt with a cache breakpoint between each tier.
func (p PersonaConfig) SystemPrompt() SystemPrompt {
	kept := make([]string, 0, len(p.SystemSections))
	for _, s := range p.SystemSections {
		if s != "" {
			kept = append(kept, s)
		}
	}
	var sp SystemPrompt
	for i, s := range kept {
		sp = sp.Add(s)
		if i < len(kept)-1 {
			sp = sp.Break()
		}
	}
	return sp
}

// LoadConfig reads and validates a JSON config file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("golm: parse config %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks referential integrity of the config.
func (c *Config) Validate() error {
	provs := map[string]bool{}
	provType := map[string]string{}
	for _, p := range c.Providers {
		if p.Name == "" || p.Type == "" {
			return fmt.Errorf("golm: provider missing name or type")
		}
		if provs[p.Name] {
			return fmt.Errorf("golm: duplicate provider %q", p.Name)
		}
		switch p.Type {
		case "cli":
			if p.Command == "" {
				return fmt.Errorf("golm: cli provider %q missing command", p.Name)
			}
			if p.PromptVia != "" && p.PromptVia != "stdin" && p.PromptVia != "arg" {
				return fmt.Errorf("golm: cli provider %q has invalid prompt_via %q (want stdin or arg)", p.Name, p.PromptVia)
			}
		case "anthropic", "openai", "google":
			if p.APIKeyEnv == "" && p.APIKeyCmd == "" && p.BaseURL == "" {
				return fmt.Errorf("golm: provider %q needs api_key_env, api_key_cmd or base_url", p.Name)
			}
			if !validToolTags[strings.ToLower(p.ToolTags)] {
				return fmt.Errorf("golm: provider %q has unknown tool_tags %q (want hermes, or omit for the native tool API)", p.Name, p.ToolTags)
			}
		default:
			return fmt.Errorf("golm: provider %q has unknown type %q", p.Name, p.Type)
		}
		provs[p.Name] = true
		provType[p.Name] = p.Type
	}
	names := map[string]bool{}
	for _, a := range c.Agents {
		if a.Name == "" {
			return fmt.Errorf("golm: agent missing name")
		}
		if names[a.Name] {
			return fmt.Errorf("golm: duplicate agent %q", a.Name)
		}
		names[a.Name] = true
		if !provs[a.Provider] {
			return fmt.Errorf("golm: agent %q references unknown provider %q", a.Name, a.Provider)
		}
		if p, ok := c.Provider(a.Provider); ok && strings.EqualFold(p.ToolTags, "hermes") && a.ToolChoice != "" {
			return fmt.Errorf("golm: agent %q sets tool_choice on provider %q, which uses tool_tags; the tag convention cannot force a call", a.Name, a.Provider)
		}
		if provType[a.Provider] == "cli" && len(a.Tools) > 0 {
			return fmt.Errorf("golm: agent %q uses cli provider %q, which has no tool support; remove its tools", a.Name, a.Provider)
		}
		if !validThinking[strings.ToLower(a.Thinking)] {
			return fmt.Errorf("golm: agent %q has invalid thinking %q (want off|auto|budget|disabled)", a.Name, a.Thinking)
		}
		if !validEffort[strings.ToLower(a.Effort)] {
			return fmt.Errorf("golm: agent %q has invalid effort %q (want low|medium|high|xhigh|max)", a.Name, a.Effort)
		}
		if !validSafety[strings.ToLower(a.Safety)] {
			return fmt.Errorf("golm: agent %q has invalid safety %q (want low|none)", a.Name, a.Safety)
		}
		if !validCacheTTL[strings.ToLower(a.CachePromptTTL)] {
			return fmt.Errorf("golm: agent %q has invalid cache_prompt_ttl %q (want 1h, or omit for the provider default)", a.Name, a.CachePromptTTL)
		}
		for field, v := range map[string]int{
			"max_steps":             a.MaxSteps,
			"max_total_tokens":      a.MaxTotalTokens,
			"max_tool_result_bytes": a.MaxToolResultBytes,
			"max_parallel_tools":    a.MaxParallelTools,
			"keep_last":             a.KeepLast,
		} {
			if v < 0 {
				return fmt.Errorf("golm: agent %q has negative %s (%d)", a.Name, field, v)
			}
		}
		if _, err := parseOptionalDuration(a.ToolTimeout); err != nil {
			return fmt.Errorf("golm: agent %q has invalid tool_timeout %q: %w", a.Name, a.ToolTimeout, err)
		}
		if err := a.Compaction.validate(a.Name); err != nil {
			return err
		}
		if err := a.Policy.Validate("agent " + a.Name + ": "); err != nil {
			return err
		}

		if a.Compaction != nil && a.KeepLast > 0 {
			return fmt.Errorf("golm: agent %q sets both keep_last and compaction; compaction replaces the trim, so set compaction.keep_last instead", a.Name)
		}
	}
	if c.DefaultAgent != "" && !names[c.DefaultAgent] {
		return fmt.Errorf("golm: default_agent %q not defined", c.DefaultAgent)
	}
	if err := c.Builtin.Validate(); err != nil {
		return err
	}
	if err := c.Policy.Validate(""); err != nil {
		return err
	}
	if err := c.Orchestration.Validate(names); err != nil {
		return err
	}
	for _, m := range c.MCPServers {
		if m.Name == "" || m.Command == "" {
			return fmt.Errorf("golm: mcp_server missing name or command")
		}
	}
	a2aNames := map[string]bool{}
	for _, a := range c.A2AAgents {
		if a.Name == "" || a.URL == "" {
			return fmt.Errorf("golm: a2a_agent missing name or url")
		}
		if a2aNames[a.Name] {
			return fmt.Errorf("golm: duplicate a2a_agent %q", a.Name)
		}
		if names[a.Name] {
			return fmt.Errorf("golm: a2a_agent %q collides with an agent name", a.Name)
		}
		a2aNames[a.Name] = true
	}
	return nil
}

// Provider returns the named provider config.
func (c *Config) Provider(name string) (ProviderConfig, bool) {
	for _, p := range c.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return ProviderConfig{}, false
}

// PolicyFor returns the policy block governing the named persona.
func (c *Config) PolicyFor(name string) *PolicyConfig {
	if a, ok := c.Agent(name); ok && a.Policy != nil {
		return a.Policy
	}
	return c.Policy
}

// MainAgent returns the persona an orchestration runs as its entry point.
func (c *Config) MainAgent() (PersonaConfig, bool) {
	if len(c.Agents) == 0 {
		return PersonaConfig{}, false
	}
	for _, a := range c.Agents {
		if a.Role == "main" {
			return a, true
		}
	}
	return c.Agents[0], true
}

// Agent returns the named persona config.
func (c *Config) Agent(name string) (PersonaConfig, bool) {
	for _, a := range c.Agents {
		if a.Name == name {
			return a, true
		}
	}
	return PersonaConfig{}, false
}

// Save validates the config and writes it as indented JSON to path, creating parent directories.
func (c *Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Providers == nil {
		c.Providers = []ProviderConfig{}
	}
	if c.Agents == nil {
		c.Agents = []PersonaConfig{}
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}

	f, err := os.CreateTemp(dir, ".golm-config-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
