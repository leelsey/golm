// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Orchestrator coordinates a main agent and a set of named sub-agents.
type Orchestrator struct {
	ToolPolicy ToolPolicy

	Budget *Budget

	MaxDepth int

	Scope DelegationScope

	MaxConversations int

	MaxFan      int
	FanParallel int

	StreamDelegates bool

	Router Router

	mu     sync.RWMutex
	agents map[string]*Agent
	roles  map[string]string
	descs  map[string]string
	main   string
	bus    *Bus
	usage  Usage

	convs map[string]*conversation
	order []string

	parallelSet bool
}

// DefaultMaxDelegationDepth is how deep delegation nests before it is refused.
const DefaultMaxDelegationDepth = 3

// ErrDelegationTooDeep is delegation nested past MaxDepth.
var ErrDelegationTooDeep = errors.New("orchestrator: delegation nested too deeply")

type delegationDepthKey struct{}

func delegationDepth(ctx context.Context) int {
	d, _ := ctx.Value(delegationDepthKey{}).(int)
	return d
}

func (o *Orchestrator) maxDepth() int {
	if o.MaxDepth > 0 {
		return o.MaxDepth
	}
	return DefaultMaxDelegationDepth
}

// NewOrchestrator returns an Orchestrator publishing events to bus.
func NewOrchestrator(bus *Bus) *Orchestrator {
	return &Orchestrator{
		agents: map[string]*Agent{}, roles: map[string]string{},
		descs: map[string]string{}, convs: map[string]*conversation{}, bus: bus,
	}
}

func (o *Orchestrator) addUsage(u Usage) {
	o.mu.Lock()
	o.usage.Add(u)
	o.mu.Unlock()
}

// Usage returns the cumulative token usage of all sub-agent delegations run via the delegation tools.
func (o *Orchestrator) Usage() Usage {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.usage
}

// Add registers an agent under name with role "main" or "sub".
func (o *Orchestrator) Add(name, role string, a *Agent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	a.Name = name
	if a.Bus == nil {
		a.Bus = o.bus
	}
	if a.ToolPolicy == nil {
		a.ToolPolicy = o.ToolPolicy
	}
	if a.Budget == nil {
		a.Budget = o.Budget
	}
	o.agents[name] = a
	o.roles[name] = role
	if role == "main" || o.main == "" {
		o.main = name
	}
}

// SetBudget installs b as the orchestration's ceiling, including on agents that have ALREADY been added.
func (o *Orchestrator) SetBudget(b *Budget) {
	o.mu.Lock()
	defer o.mu.Unlock()
	prev := o.Budget
	o.Budget = b
	for _, a := range o.agents {
		if a.Budget == nil || a.Budget == prev {
			a.Budget = b
		}
	}
}

// SetToolPolicy installs p as the gate, including on agents already added.
func (o *Orchestrator) SetToolPolicy(p ToolPolicy) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ToolPolicy = p
	for _, a := range o.agents {
		if a.ToolPolicy == nil {
			a.ToolPolicy = p
		}
	}
}

// Get returns the named agent.
func (o *Orchestrator) Get(name string) (*Agent, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	a, ok := o.agents[name]
	return a, ok
}

// Main returns the entry-point agent, or nil if none is registered.
func (o *Orchestrator) Main() *Agent {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.agents[o.main]
}

// AsTool wraps a named sub-agent as a delegation Tool.
func (o *Orchestrator) AsTool(name, description string) Tool {
	t := TextTool(name, description, "task", "the task to delegate to this agent",
		func(ctx context.Context, task string) (string, error) {
			return o.delegate(ctx, name, task)
		})

	return t
}

// WireDelegation registers every sub-agent as a delegation tool on the main agent.
func (o *Orchestrator) WireDelegation(descriptions map[string]string) error {
	o.mu.RLock()
	main := o.agents[o.main]
	subs := make([]string, 0, len(o.roles))
	for name, role := range o.roles {
		if role == "sub" && name != o.main {
			subs = append(subs, name)
		}
	}
	o.mu.RUnlock()

	if main == nil {
		return fmt.Errorf("orchestrator: no main agent")
	}
	if main.Tools == nil {
		main.Tools = NewRegistry()
	}
	for _, name := range subs {
		desc := descriptions[name]
		if desc == "" {
			desc = "Delegate a task to the " + name + " agent."
		}
		o.Describe(name, desc)
		main.Tools.Register(o.AsTool(name, desc))
	}

	if !o.parallelSet {
		main.ParallelTools = true
	}
	return nil
}

// KeepParallelTools tells WireDelegation to leave ParallelTools exactly as the caller set it.
func (o *Orchestrator) KeepParallelTools() {
	o.mu.Lock()
	o.parallelSet = true
	o.mu.Unlock()
}

// Describe records what an agent is for.
func (o *Orchestrator) Describe(name, description string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.descs == nil {
		o.descs = map[string]string{}
	}
	o.descs[name] = description
}

// Agents lists the registered agents, sorted by name.
func (o *Orchestrator) Agents() []AgentInfo {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]AgentInfo, 0, len(o.agents))
	for name := range o.agents {
		out = append(out, AgentInfo{Name: name, Role: o.roles[name], Description: o.descs[name]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Run executes the conversation s with input, through the Router when one is set.
func (o *Orchestrator) Run(ctx context.Context, s *Session, input string) (Result, error) {
	return o.RunMessage(ctx, s, UserText(input))
}

// RunMessage is Run with a full.
func (o *Orchestrator) RunMessage(ctx context.Context, s *Session, msg Message) (Result, error) {
	if s == nil {
		return Result{}, ErrNoSession
	}
	target, err := o.route(ctx, s, msg)
	if err != nil {
		return Result{}, err
	}
	res, err := target.RunMessage(withConversation(ctx, s.ID()), s, msg)
	o.recordLast(s, target)
	return res, err
}

// Stream is Run with incremental events.
func (o *Orchestrator) Stream(ctx context.Context, s *Session, input string, fn func(StreamEvent) error) (Result, error) {
	return o.StreamMessage(ctx, s, UserText(input), fn)
}

// StreamMessage is Stream with a full.
func (o *Orchestrator) StreamMessage(ctx context.Context, s *Session, msg Message, fn func(StreamEvent) error) (Result, error) {
	if s == nil {
		return Result{}, ErrNoSession
	}
	target, err := o.route(ctx, s, msg)
	if err != nil {
		return Result{}, err
	}
	res, err := target.StreamMessage(withConversation(ctx, s.ID()), s, msg, fn)
	o.recordLast(s, target)
	return res, err
}

func (o *Orchestrator) recordLast(s *Session, target *Agent) {
	if s == nil || target == nil || target.Name == "" {
		return
	}
	_ = s.SetState(lastAgentKey, target.Name)
}
