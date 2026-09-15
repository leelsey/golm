// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

// Package build constructs concrete providers and agents from a golm.Config.
package build

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/a2a"
	"github.com/leelsey/golm/internal/proc"
	"github.com/leelsey/golm/mcp"
	"github.com/leelsey/golm/provider/anthropic"
	"github.com/leelsey/golm/provider/clibackend"
	"github.com/leelsey/golm/provider/google"
	"github.com/leelsey/golm/provider/hermes"
	"github.com/leelsey/golm/provider/openai"
)

// MCPTools connects to every configured MCP server, returning their tools plus a closer.
func MCPTools(ctx context.Context, cfg *golm.Config) ([]golm.Tool, func(), error) {
	var tools []golm.Tool
	var clients []*mcp.Client
	closeAll := func() {
		for _, c := range clients {
			_ = c.Close()
		}
	}
	for _, ms := range cfg.MCPServers {
		c, err := mcp.Dialer{Inherit: ms.InheritEnv}.Dial(ctx, ms.Command, ms.Args...)
		if err != nil {
			closeAll()
			return nil, nil, fmt.Errorf("build: dial mcp server %q: %w", ms.Name, err)
		}
		if err := c.Initialize(ctx, "golm"); err != nil {
			_ = c.Close()
			closeAll()
			return nil, nil, fmt.Errorf("build: init mcp server %q: %w", ms.Name, err)
		}
		ts, err := c.Tools(ctx)
		if err != nil {
			_ = c.Close()
			closeAll()
			return nil, nil, fmt.Errorf("build: list tools from mcp server %q: %w", ms.Name, err)
		}
		tools = append(tools, ts...)
		clients = append(clients, c)
	}
	return tools, closeAll, nil
}

// Provider builds a golm.Provider from a ProviderConfig.
func Provider(ctx context.Context, pc golm.ProviderConfig, hc *http.Client) (golm.Provider, error) {
	key := ""
	switch {
	case pc.APIKeyCmd != "":
		k, err := keyFromCmd(ctx, pc.APIKeyCmd)
		if err != nil {
			return nil, fmt.Errorf("build: api_key_cmd for provider %q: %w", pc.Name, err)
		}
		key = k
	case pc.APIKeyEnv != "":
		key = os.Getenv(pc.APIKeyEnv)
	}
	p, err := baseProvider(pc, key, hc)
	if err != nil {
		return nil, err
	}

	if strings.EqualFold(pc.ToolTags, "hermes") {
		return hermes.Wrap(p), nil
	}
	return p, nil
}

func baseProvider(pc golm.ProviderConfig, key string, hc *http.Client) (golm.Provider, error) {
	switch pc.Type {
	case "anthropic":
		c := anthropic.New(key)
		if pc.BaseURL != "" {
			c.WithBaseURL(pc.BaseURL)
		}
		if hc != nil {
			c.WithHTTPClient(hc)
		}
		return c, nil
	case "openai":
		c := openai.New(key)
		if pc.BaseURL != "" {
			c.WithBaseURL(pc.BaseURL)
		}
		if hc != nil {
			c.WithHTTPClient(hc)
		}
		return c, nil
	case "google":
		c := google.New(key)
		if pc.BaseURL != "" {
			c.WithBaseURL(pc.BaseURL)
		}
		if hc != nil {
			c.WithHTTPClient(hc)
		}
		return c, nil
	case "cli":
		return clibackend.New(clibackend.Config{
			Name:    pc.Name,
			Command: pc.Command,
			Args:    pc.Args,
			Via:     clibackend.PromptVia(pc.PromptVia),
		}), nil
	default:
		return nil, fmt.Errorf("build: unknown provider type %q", pc.Type)
	}
}

// SplitArgs splits a command string into argv, honouring quotes.
func SplitArgs(s string) []string {
	var args []string
	var cur strings.Builder
	inSingle, inDouble, started := false, false, false
	flush := func() {
		if started {
			args = append(args, cur.String())
			cur.Reset()
			started = false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(c)
			}
			started = true
		case inDouble:
			switch {
			case c == '"':
				inDouble = false
			case c == '\\' && i+1 < len(s):
				i++
				cur.WriteByte(s[i])
			default:
				cur.WriteByte(c)
			}
			started = true
		case c == '\'':
			inSingle, started = true, true
		case c == '"':
			inDouble, started = true, true
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	flush()
	return args
}

// DefaultKeyCmdTimeout bounds an api_key_cmd that neither finishes nor fails.
const DefaultKeyCmdTimeout = 15 * time.Second

// keyCmdKillGrace bounds how long the wait holds on to a killed helper's pipes.
const keyCmdKillGrace = 2 * time.Second

func keyFromCmd(ctx context.Context, cmd string) (string, error) {
	fields := SplitArgs(cmd)
	if len(fields) == 0 {
		return "", fmt.Errorf("empty command")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultKeyCmdTimeout)
		defer cancel()
	}
	c := exec.CommandContext(ctx, fields[0], fields[1:]...)
	proc.Bound(c, keyCmdKillGrace)
	c.Cancel = func() error { return proc.KillGroup(c) }
	out, err := c.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(trunc(string(ee.Stderr), 512)))
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("%w: %w", ctx.Err(), err)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func trunc(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "… (truncated)"
}

type providerCache struct {
	cfg  *golm.Config
	hc   *http.Client
	made map[string]golm.Provider
}

func newProviderCache(cfg *golm.Config, hc *http.Client) *providerCache {
	return &providerCache{cfg: cfg, hc: hc, made: map[string]golm.Provider{}}
}

func (c *providerCache) get(ctx context.Context, name string) (golm.Provider, error) {
	if p, ok := c.made[name]; ok {
		return p, nil
	}
	pc, ok := c.cfg.Provider(name)
	if !ok {
		return nil, fmt.Errorf("build: provider %q not defined", name)
	}
	p, err := Provider(ctx, pc, c.hc)
	if err != nil {
		return nil, err
	}
	c.made[name] = p
	return p, nil
}

// Orchestrator builds a multi-agent Orchestrator from cfg.
func Orchestrator(ctx context.Context, cfg *golm.Config, bus *golm.Bus, tools *golm.Registry, hc *http.Client) (*golm.Orchestrator, error) {
	o := golm.NewOrchestrator(bus)
	cache := newProviderCache(cfg, hc)

	cfg.Orchestration.Apply(o)
	router, err := cfg.Orchestration.Router()
	if err != nil {
		return nil, err
	}
	o.Router = router

	if main, ok := cfg.MainAgent(); ok && main.MaxTotalTokens > 0 {
		o.Budget = golm.NewBudget(main.MaxTotalTokens)
	}
	descriptions := map[string]string{}
	for _, p := range cfg.Agents {
		ag, err := agentWith(ctx, cfg, p.Name, tools, cache)
		if err != nil {
			return nil, err
		}
		role := p.Role
		if role == "" {
			role = "sub"
		}
		o.Add(p.Name, role, ag)
		if p.Description != "" {
			descriptions[p.Name] = p.Description
		}
	}
	if err := wireFanOut(o, cfg, descriptions); err != nil {
		return nil, err
	}

	if main, ok := cfg.MainAgent(); ok {
		if _, set := main.Parallel(); set {
			o.KeepParallelTools()
		}
	}
	if err := o.WireDelegation(descriptions); err != nil {
		return nil, err
	}
	return o, nil
}

func wireFanOut(o *golm.Orchestrator, cfg *golm.Config, descriptions map[string]string) error {
	if cfg.Orchestration == nil || !cfg.Orchestration.FanOut {
		return nil
	}
	main := o.Main()
	if main == nil {
		return fmt.Errorf("build: orchestration.fan_out is set but there is no main agent")
	}
	if main.Tools == nil {
		main.Tools = golm.NewRegistry()
	}

	for _, a := range o.Agents() {
		if a.Role != "sub" || a.Name == main.Name {
			continue
		}
		desc := ""
		if d := descriptions[a.Name]; d != "" {
			desc = fmt.Sprintf("Hand several INDEPENDENT tasks to the %s agent at once (%s). "+
				"Each is answered separately and cannot see the others.", a.Name, d)
		}
		main.Tools.Register(o.AsFanTool(a.Name, desc))
	}
	return nil
}

// A2ATools wraps each remote agent in cfg.A2AAgents as a delegation golm.Tool.
func A2ATools(cfg *golm.Config) []golm.Tool {
	tools := make([]golm.Tool, 0, len(cfg.A2AAgents))
	for _, a := range cfg.A2AAgents {
		desc := a.Description
		if desc == "" {
			desc = "Delegate a task to the remote A2A agent " + a.Name + "."
		}
		cl := a2a.NewClient(a.URL)
		if a.TokenEnv != "" {
			cl = cl.WithToken(os.Getenv(a.TokenEnv))
		}
		tools = append(tools, cl.AsTool(a.Name, desc))
	}
	return tools
}

// Agent builds a golm.Agent for the named persona, wiring its provider.
func Agent(ctx context.Context, cfg *golm.Config, name string, tools *golm.Registry, hc *http.Client) (*golm.Agent, error) {
	return agentWith(ctx, cfg, name, tools, newProviderCache(cfg, hc))
}

func agentWith(ctx context.Context, cfg *golm.Config, name string, tools *golm.Registry, cache *providerCache) (*golm.Agent, error) {
	persona, ok := cfg.Agent(name)
	if !ok {
		return nil, fmt.Errorf("build: agent %q not defined", name)
	}
	prov, err := cache.get(ctx, persona.Provider)
	if err != nil {
		return nil, err
	}
	agentTools := golm.NewRegistry()
	if tools != nil && len(persona.Tools) > 0 {
		for _, tn := range persona.Tools {
			t, ok := tools.Get(tn)
			if !ok {
				return nil, fmt.Errorf("build: agent %q references unknown tool %q", name, tn)
			}
			agentTools.Register(t)
		}
	}
	ag := &golm.Agent{
		Provider:           prov,
		Model:              persona.Model,
		System:             persona.System,
		SystemPrompt:       persona.SystemPrompt(),
		Tools:              agentTools,
		MaxTokens:          persona.MaxTokens,
		Temperature:        persona.Temperature,
		Thinking:           persona.ThinkingConfig(),
		Effort:             golm.Effort(strings.ToLower(persona.Effort)),
		Safety:             golm.SafetyLevel(strings.ToLower(persona.Safety)),
		ToolChoice:         persona.ToolChoice,
		ResponseModalities: persona.ResponseModalities,
		MaxSteps:           persona.MaxSteps,
		MaxTotalTokens:     persona.MaxTotalTokens,
		ToolTimeout:        persona.ToolTimeoutDuration(),
		MaxToolResultBytes: persona.MaxToolResultBytes,
		MaxParallelTools:   persona.MaxParallelTools,
		CachePrompt:        persona.CachePrompt,
		CachePromptTTL:     golm.CacheTTL(strings.ToLower(persona.CachePromptTTL)),
		KeepLast:           persona.KeepLast,
	}
	ag.ParallelTools, _ = persona.Parallel()

	if cc := persona.Compaction; cc != nil {
		model := cc.Model
		if model == "" {
			model = persona.Model
		}
		ag.Compaction = &golm.CompactPolicy{
			CompactConfig: golm.CompactConfig{
				KeepLast:  cc.KeepLast,
				Summarise: golm.SummariseWith(prov, model, cc.Prompt),
			},
			AtMessages:    cc.AtMessages,
			AtInputTokens: cc.AtInputTokens,
			Timeout:       cc.TimeoutDuration(),
		}
	}
	return ag, nil
}
