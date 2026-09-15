// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/build"
)

const adHocAgent = "ad-hoc"

func cacheDir() string {
	if d := os.Getenv("GOLM_CACHE"); d != "" {
		return build.CacheDirFor(d)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return build.CacheDirFor(filepath.Join(home, ".cache", "golm"))
}

func userSkillDir() string {
	if d := os.Getenv("GOLM_SKILLS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "golm", "skills")
}

func (bf *backendFlags) skillDirs() []string {
	dirs := append([]string(nil), bf.skills...)
	if d := userSkillDir(); d != "" {
		dirs = append(dirs, d)
	}
	return dirs
}

func (bf *backendFlags) resolveConfig(cfgPath string) (*golm.Config, string, error) {
	if cfgPath == "" && bf.agent != "" {
		return nil, "", fmt.Errorf("--agent requires a config (pass --config or run where golm.json is discoverable)")
	}
	if cfgPath != "" {
		cfg, err := golm.LoadConfig(cfgPath)
		if err != nil {
			return nil, "", err
		}
		name := bf.agent
		if name == "" {
			name = cfg.DefaultAgent
		}
		if name == "" {
			return nil, "", fmt.Errorf("--agent required (or set default_agent in config)")
		}
		return cfg, name, nil
	}

	if bf.cli != "" {
		fields := splitArgs(bf.cli)
		if len(fields) == 0 {
			return nil, "", fmt.Errorf("--cli command is empty")
		}
		return &golm.Config{
			Providers: []golm.ProviderConfig{{
				Name: "cli", Type: "cli",
				Command: fields[0], Args: fields[1:], PromptVia: "stdin",
			}},
			Agents: []golm.PersonaConfig{{Name: adHocAgent, Provider: "cli", Model: "cli"}},
		}, adHocAgent, nil
	}

	if bf.model == "" {
		return nil, "", fmt.Errorf("--model is required in ad-hoc mode (or use --config / --cli)")
	}
	prov, err := bf.providerType()
	if err != nil {
		return nil, "", err
	}
	keyEnv := defaultKeyEnv(prov)
	if bf.apiKeyEnv != "" {
		keyEnv = bf.apiKeyEnv
	}
	return &golm.Config{
		Providers: []golm.ProviderConfig{{
			Name: prov, Type: prov,
			APIKeyEnv: keyEnv, BaseURL: bf.baseURL, ToolTags: bf.toolTags,
		}},
		Agents: []golm.PersonaConfig{{Name: adHocAgent, Provider: prov, Model: bf.model}},
	}, adHocAgent, nil
}

func (bf *backendFlags) providerType() (string, error) {
	if bf.provider != "" {
		switch bf.provider {
		case "anthropic", "openai", "google":
			return bf.provider, nil
		default:
			return "", fmt.Errorf("unknown --provider %q (want anthropic, openai or google)", bf.provider)
		}
	}
	if t, ok := golm.ProviderTypeForModel(bf.model); ok {
		return t, nil
	}
	return "", fmt.Errorf("cannot tell which API %q speaks; pass --provider anthropic|openai|google "+
		"(a local OpenAI-compatible server is --provider openai --base-url …)", bf.model)
}

func (bf *backendFlags) runtime(ctx context.Context, cfgPath string, stderr io.Writer) (*build.Runtime, error) {
	cfg, name, err := bf.resolveConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	opts, gate, err := bf.buildOptions(cfg, stderr)
	if err != nil {
		return nil, err
	}
	opts.Agent = name
	rt, err := build.New(ctx, opts)
	if err != nil {
		gate.close()
		return nil, err
	}
	if err := bf.override(rt.Agent); err != nil {
		rt.Close()
		gate.close()
		return nil, err
	}
	rt.OnClose(gate.close)
	return rt, nil
}

func (bf *backendFlags) logger(stderr io.Writer) (*slog.Logger, error) {
	if bf.logLevel == "" {
		return nil, nil
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToUpper(strings.TrimSpace(bf.logLevel)))); err != nil {
		return nil, fmt.Errorf("invalid --log %q (want debug, info, warn or error)", bf.logLevel)
	}
	return slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: lvl})), nil
}

func (bf *backendFlags) buildOptions(cfg *golm.Config, stderr io.Writer) (build.Options, toolGate, error) {
	gate, err := bf.gate(cfg, stderr)
	if err != nil {
		return build.Options{}, toolGate{close: func() error { return nil }}, err
	}
	logger, err := bf.logger(stderr)
	if err != nil {
		gate.close()
		return build.Options{}, toolGate{close: func() error { return nil }}, err
	}
	return build.Options{
		Config:    cfg,
		HTTP:      bf.debugClient(stderr),
		CacheDir:  cacheDir(),
		SkillDirs: bf.skillDirs(),
		MemoryDir: bf.memory,
		Builtin:   bf.builtin(cfg),
		Policy:    gate.policy,
		Approve:   gate.approve,
		Audit:     gate.audit,
		Logger:    logger,
	}, gate, nil
}

func (bf *backendFlags) runtimeFor(ctx context.Context, opts build.Options, name string) (*build.Runtime, error) {
	opts.Agent = name
	return build.New(ctx, opts)
}

func (bf *backendFlags) orchestration(ctx context.Context, cfgPath string, stderr io.Writer) (*build.Runtime, *golm.Orchestrator, error) {
	if cfgPath == "" {
		return nil, nil, fmt.Errorf("--orchestrate requires a config (pass --config or run where golm.json is discoverable)")
	}
	cfg, err := golm.LoadConfig(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	opts, gate, err := bf.buildOptions(cfg, stderr)
	if err != nil {
		return nil, nil, err
	}
	rt, orc, err := bf.orchestrationWith(ctx, opts, cfg, stderr)
	if err != nil {
		gate.close()
		return nil, nil, err
	}
	rt.OnClose(gate.close)
	return rt, orc, nil
}

func (bf *backendFlags) orchestrationWith(ctx context.Context, opts build.Options, cfg *golm.Config, stderr io.Writer) (*build.Runtime, *golm.Orchestrator, error) {
	opts.Agent = ""
	rt, orc, err := build.NewOrchestrator(ctx, opts, nil)
	if err != nil {
		return nil, nil, err
	}
	bf.applyToOrchestration(orc, stderr)
	return rt, orc, nil
}

type toolGate struct {
	policy  *golm.PolicyConfig
	approve golm.Approver
	audit   func(golm.ToolRequest, golm.Decision, error)
	close   func() error
}

func (bf *backendFlags) gate(cfg *golm.Config, stderr io.Writer) (toolGate, error) {
	g := toolGate{close: func() error { return nil }}
	if bf.ask {
		g.policy = askPolicy()
	}
	pc := g.policy
	if pc == nil && cfg != nil {
		pc = cfg.Policy
	}
	if pc.NeedsApprover() {
		if a := newApprover(stderr); a != nil {
			g.approve = a.Approve
		} else {
			fmt.Fprintln(stderr, "golm: warning: the tool policy asks for approval but there is no terminal to ask at; those calls will be refused")
		}
	}
	if bf.audit != "" {
		w, err := newAuditLog(bf.audit)
		if err != nil {
			return toolGate{}, err
		}
		g.audit, g.close = w.Record, w.Close
	}
	return g, nil
}

func (bf *backendFlags) builtin(cfg *golm.Config) *golm.BuiltinConfig {
	if bf.workspace == "" && !bf.fetch && !bf.fetchInternal && len(bf.allowRun) == 0 && !bf.readOnly {
		return nil
	}
	b := &golm.BuiltinConfig{Workspace: bf.workspace, WorkspaceReadOnly: bf.readOnly}

	if cfg != nil && cfg.Builtin != nil {
		c := cfg.Builtin
		if b.Workspace == "" {
			b.Workspace = c.Workspace
		}

		b.WorkspaceReadOnly = b.WorkspaceReadOnly || c.WorkspaceReadOnly

		if c.Fetch != nil {
			f := *c.Fetch
			b.Fetch = &f
		}
		if c.Run != nil {
			r := *c.Run
			r.Allow = append([]string(nil), c.Run.Allow...)
			r.InheritEnv = append([]string(nil), c.Run.InheritEnv...)
			b.Run = &r
		}
	}

	if bf.fetch || bf.fetchInternal {
		if b.Fetch == nil {
			b.Fetch = &golm.FetchConfig{}
		}
		b.Fetch.AllowPrivate = b.Fetch.AllowPrivate || bf.fetchInternal
	}

	if len(bf.allowRun) > 0 {
		if b.Run == nil {
			b.Run = &golm.RunConfig{}
		}
		b.Run.Allow = bf.allowRun
	}
	return b
}

func (bf *backendFlags) override(ag *golm.Agent) error {
	if ag == nil {
		return nil
	}
	if bf.model != "" {
		ag.Model = bf.model
	}
	if bf.system != "" {
		ag.System = bf.system
	}
	if bf.think != "" {
		tc, err := parseThink(bf.think, bf.thinkBudget)
		if err != nil {
			return err
		}
		ag.Thinking = tc
	} else if bf.thinkBudget > 0 {
		return fmt.Errorf("--think-budget needs --think budget")
	}
	if bf.effort != "" {
		e, err := parseEffort(bf.effort)
		if err != nil {
			return err
		}
		ag.Effort = e
	}
	if bf.maxTokens > 0 {
		ag.MaxTokens = bf.maxTokens
	}
	if bf.maxSteps > 0 {
		ag.MaxSteps = bf.maxSteps
	}

	if bf.maxTotalTokens > 0 {
		ag.MaxTotalTokens = bf.maxTotalTokens
	}
	if len(bf.modalities) > 0 {
		ag.ResponseModalities = bf.modalities
	}
	return nil
}

func (bf *backendFlags) applyToOrchestration(o *golm.Orchestrator, stderr io.Writer) {
	if bf.maxTotalTokens > 0 {
		o.SetBudget(golm.NewBudget(bf.maxTotalTokens))
	}
	var dropped []string
	for flag, given := range map[string]bool{
		"--model": bf.model != "", "--system": bf.system != "", "--think": bf.think != "",
		"--effort": bf.effort != "", "--max-tokens": bf.maxTokens > 0,
		"--max-steps": bf.maxSteps > 0, "--modality": len(bf.modalities) > 0,
	} {
		if given {
			dropped = append(dropped, flag)
		}
	}
	if len(dropped) == 0 {
		return
	}
	sort.Strings(dropped)
	fmt.Fprintf(stderr, "golm: warning: %s ignored in --orchestrate mode; each persona declares its own\n",
		strings.Join(dropped, ", "))
}

func parseThink(s string, budget int) (golm.ThinkingConfig, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off":
		return golm.ThinkingConfig{Mode: golm.ThinkingOff}, nil
	case "auto":
		return golm.ThinkingConfig{Mode: golm.ThinkingAuto}, nil
	case "disabled":
		return golm.ThinkingConfig{Mode: golm.ThinkingDisabled}, nil
	case "budget":
		if budget <= 0 {
			budget = defaultThinkBudget
		}
		return golm.ThinkingConfig{Mode: golm.ThinkingBudget, Budget: budget}, nil
	default:
		return golm.ThinkingConfig{}, fmt.Errorf("invalid --think %q (want off, auto, budget or disabled)", s)
	}
}

const defaultThinkBudget = 2048

func parseEffort(s string) (golm.Effort, error) {
	switch e := golm.Effort(strings.ToLower(strings.TrimSpace(s))); e {
	case golm.EffortLow, golm.EffortMedium, golm.EffortHigh, golm.EffortXHigh, golm.EffortMax:
		return e, nil
	default:
		return "", fmt.Errorf("invalid --effort %q (want low, medium, high, xhigh or max)", s)
	}
}
