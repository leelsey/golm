// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package build

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/mcp"
	"github.com/leelsey/golm/memory"
	"github.com/leelsey/golm/skills"
	"github.com/leelsey/golm/tools"
)

// Options is everything New needs to turn a config into a working agent.
type Options struct {
	Config *golm.Config

	Agent string

	HTTP *http.Client

	CacheDir string

	SkillDirs []string

	MemoryDir string

	Builtin *golm.BuiltinConfig

	Policy *golm.PolicyConfig

	Approve golm.Approver

	Audit func(golm.ToolRequest, golm.Decision, error)

	Logger *slog.Logger
}

// Runtime is a built agent and the things that have to be shut down with it.
type Runtime struct {
	Agent *golm.Agent

	Tools *golm.Registry

	Skills *skills.Set

	Remotes []*mcp.Remote

	Workspace *tools.Workspace

	Memory *memory.Store

	closers []func() error
}

// OnClose registers a function to run when the runtime is closed, for a resource an embedder attached to it.
func (r *Runtime) OnClose(f func() error) {
	if r == nil || f == nil {
		return
	}
	r.closers = append(r.closers, f)
}

// Close releases every subprocess and connection the runtime opened.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	var first error
	for _, c := range r.closers {
		if err := c(); err != nil && first == nil {
			first = err
		}
	}
	r.closers = nil
	return first
}

// New builds the agent named by o, with its tools and skills wired in.
func New(ctx context.Context, o Options) (*Runtime, error) {
	if o.Config == nil {
		return nil, fmt.Errorf("build: no config")
	}
	name := o.Agent
	if name == "" {
		name = o.Config.DefaultAgent
	}
	if name == "" {
		return nil, fmt.Errorf("build: no agent named and no default_agent in config")
	}

	if err := o.Config.Validate(); err != nil {
		return nil, err
	}
	if err := o.Policy.Validate(""); err != nil {
		return nil, err
	}
	rt, err := newTools(ctx, o)
	if err != nil {
		return nil, err
	}
	persona, ok := o.Config.Agent(name)
	if !ok {
		return nil, fmt.Errorf("build: agent %q not defined", name)
	}
	ag, err := Agent(ctx, o.Config, name, rt.Tools, o.HTTP)
	if err != nil {
		rt.Close()
		return nil, err
	}
	applyAvailableTools(ag, persona, rt.Tools)
	applyPrelude(ag, rt)
	applyPolicy(ag, policyFor(o, name), o)
	ag.Logger = o.Logger

	if ag.Name == "" {
		ag.Name = name
	}
	rt.Agent = ag
	return rt, nil
}

func policyFor(o Options, agent string) *golm.PolicyConfig {
	if o.Policy != nil {
		return o.Policy
	}
	return o.Config.PolicyFor(agent)
}

func applyPolicy(ag *golm.Agent, pc *golm.PolicyConfig, o Options) {
	if ag == nil {
		return
	}
	if pc == nil && o.Audit == nil {
		return
	}
	r := pc.Rules()
	if r == nil {
		r = &golm.PolicyRules{}
	}
	r.Approve, r.Audit = o.Approve, o.Audit
	ag.ToolPolicy = r.Policy()
}

// NewMany builds several named agents against ONE assembled tool set.
func NewMany(ctx context.Context, o Options, names ...string) (*Runtime, map[string]*golm.Agent, error) {
	if o.Config == nil {
		return nil, nil, fmt.Errorf("build: no config")
	}
	if err := o.Config.Validate(); err != nil {
		return nil, nil, err
	}
	if err := o.Policy.Validate(""); err != nil {
		return nil, nil, err
	}
	if len(names) == 0 {
		for _, p := range o.Config.Agents {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		return nil, nil, fmt.Errorf("build: the config defines no agents")
	}
	rt, err := newTools(ctx, o)
	if err != nil {
		return nil, nil, err
	}
	cache := newProviderCache(o.Config, o.HTTP)
	out := make(map[string]*golm.Agent, len(names))
	for _, name := range names {
		persona, ok := o.Config.Agent(name)
		if !ok {
			rt.Close()
			return nil, nil, fmt.Errorf("build: agent %q not defined", name)
		}
		ag, err := agentWith(ctx, o.Config, name, rt.Tools, cache)
		if err != nil {
			rt.Close()
			return nil, nil, err
		}
		applyAvailableTools(ag, persona, rt.Tools)
		applyPrelude(ag, rt)
		applyPolicy(ag, policyFor(o, name), o)
		ag.Logger = o.Logger
		if ag.Name == "" {
			ag.Name = name
		}
		out[name] = ag
	}
	rt.Agent = out[names[0]]
	return rt, out, nil
}

// NewOrchestrator is New for the multi-agent case.
func NewOrchestrator(ctx context.Context, o Options, bus *golm.Bus) (*Runtime, *golm.Orchestrator, error) {
	if o.Config == nil {
		return nil, nil, fmt.Errorf("build: no config")
	}
	if err := o.Config.Validate(); err != nil {
		return nil, nil, err
	}
	if err := o.Policy.Validate(""); err != nil {
		return nil, nil, err
	}
	rt, err := newTools(ctx, o)
	if err != nil {
		return nil, nil, err
	}
	orc, err := Orchestrator(ctx, o.Config, bus, rt.Tools, o.HTTP)
	if err != nil {
		rt.Close()
		return nil, nil, err
	}
	for _, p := range o.Config.Agents {
		ag, ok := orc.Get(p.Name)
		if !ok {
			continue
		}
		applyAvailableTools(ag, p, rt.Tools)
		applyPrelude(ag, rt)

		applyPolicy(ag, policyFor(o, p.Name), o)
		ag.Logger = o.Logger
	}
	rt.Agent = orc.Main()
	return rt, orc, nil
}

func newTools(ctx context.Context, o Options) (*Runtime, error) {
	rt := &Runtime{Tools: golm.NewRegistry()}

	set, err := skills.Load(append(append([]string{}, o.SkillDirs...), o.Config.Skills...)...)
	if err != nil {
		return nil, fmt.Errorf("build: load skills: %w", err)
	}
	rt.Skills = set
	if set.Len() > 0 {
		rt.Tools.Register(set.Tool())
	}

	for _, ms := range o.Config.MCPServers {
		r := &mcp.Remote{Name: ms.Name, Command: ms.Command, Args: ms.Args,
			CacheDir: o.CacheDir, Inherit: ms.InheritEnv}
		ts, err := r.Tools(ctx)
		if err != nil {
			rt.Close()
			return nil, fmt.Errorf("build: mcp server %q: %w", ms.Name, err)
		}
		rt.Remotes = append(rt.Remotes, r)
		rt.closers = append(rt.closers, r.Close)
		rt.Tools.Register(ts...)
	}

	if dir := cmp.Or(o.MemoryDir, o.Config.Memory); dir != "" {
		m, err := memory.Open(dir, memory.Options{})
		if err != nil {
			rt.Close()
			return nil, err
		}
		rt.Memory = m
		rt.Tools.Register(m.Tools()...)
	}

	rt.Tools.Register(A2ATools(o.Config)...)

	if err := rt.addBuiltin(o); err != nil {
		rt.Close()
		return nil, err
	}
	return rt, nil
}

func (rt *Runtime) addBuiltin(o Options) error {
	b := o.Builtin
	if b == nil {
		b = o.Config.Builtin
	}
	if b == nil {
		return nil
	}

	if err := b.Validate(); err != nil {
		return err
	}
	if b.Workspace != "" {
		w, err := tools.Open(b.Workspace)
		if err != nil {
			return err
		}
		w.ReadOnly = b.WorkspaceReadOnly
		rt.Workspace = w
		rt.closers = append(rt.closers, w.Close)
		rt.Tools.Register(w.Tools()...)
	}
	if b.Fetch != nil {
		rt.Tools.Register((&tools.Fetcher{
			AllowPrivate: b.Fetch.AllowPrivate,
			Timeout:      b.Fetch.TimeoutDuration(),
			MaxBytes:     b.Fetch.MaxBytes,
		}).Tool())
	}
	if b.Run != nil {
		dir := b.Workspace
		r := &tools.Runner{
			Allow:    b.Run.Allow,
			Dir:      dir,
			Timeout:  b.Run.TimeoutDuration(),
			Inherit:  b.Run.InheritEnv,
			DenyArgs: b.Run.DenyArgs,
			MaxArgs:  b.Run.MaxArgs,
		}

		if t := r.Tool(); t != nil {
			rt.Tools.Register(t)
		}
	}
	return nil
}

func applyAvailableTools(ag *golm.Agent, p golm.PersonaConfig, all *golm.Registry) {
	if p.Tools != nil {
		return
	}
	if ag.Tools == nil {
		ag.Tools = golm.NewRegistry()
	}
	ag.Tools.Register(all.List()...)
}

func applyPrelude(ag *golm.Agent, rt *Runtime) {
	if ag == nil || rt == nil {
		return
	}
	var head golm.SystemPrompt
	if idx := rt.Skills.Index(); idx != "" {
		head = head.Add(idx).Break()
	}
	if mem := rt.Memory.Section(); mem != "" {
		head = head.Add(mem).Break()
	}
	if len(head) == 0 {
		return
	}
	ag.SystemPrompt = append(head, ag.SystemPrompt...)
}

// CacheDirFor is the conventional manifest location under a base cache directory.
func CacheDirFor(base string) string {
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/mcp"
}
