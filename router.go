// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// AgentInfo is what a Router is told about one registered agent.
type AgentInfo struct {
	Name        string
	Role        string
	Description string
}

// RouteInput is one routing question.
type RouteInput struct {
	Session *Session

	Message Message

	Agents []AgentInfo
}

// Text is the routed turn's text.
func (in RouteInput) Text() string { return in.Message.Text() }

// Route is a routing decision.
type Route struct {
	Agent string

	Reason string
}

// Router picks which agent handles a turn, WITHOUT asking a model.
type Router func(ctx context.Context, in RouteInput) (Route, error)

func (o *Orchestrator) route(ctx context.Context, s *Session, msg Message) (*Agent, error) {
	main := o.Main()
	if o.Router == nil {
		if main == nil {
			return nil, fmt.Errorf("orchestrator: no main agent")
		}
		return main, nil
	}
	r, err := o.ask(ctx, RouteInput{Session: s, Message: msg, Agents: o.Agents()})
	if err != nil {
		return nil, fmt.Errorf("orchestrator: routing: %w", err)
	}
	if r.Agent == "" {
		if main == nil {
			return nil, fmt.Errorf("orchestrator: no main agent and the router named none")
		}
		return main, nil
	}
	target, ok := o.Get(r.Agent)
	if !ok {
		return nil, fmt.Errorf("orchestrator: router chose %q, which is not registered", r.Agent)
	}
	if o.bus != nil {
		o.bus.Publish(ctx, Event{Topic: r.Agent, Agent: r.Agent, Kind: "route",
			Data: RouteEvent(r)})
	}
	return target, nil
}

func (o *Orchestrator) ask(ctx context.Context, in RouteInput) (r Route, err error) {
	defer func() {
		if p := recover(); p != nil {
			r, err = Route{}, fmt.Errorf("the router panicked: %v", p)
		}
	}()
	return o.Router(ctx, in)
}

// RouteEvent is the Data of a "route" event, published.
type RouteEvent struct {
	Agent  string
	Reason string
}

// RouteRule matches a turn and names the agent that should handle it.
type RouteRule struct {
	Agent string

	Any []string

	All []string

	Prefix string

	Pattern string

	re *regexp.Regexp
}

// RouteRules compiles rules into a Router, first match wins.
func RouteRules(rules []RouteRule) (Router, error) {
	compiled := make([]RouteRule, len(rules))
	for i, r := range rules {
		if strings.TrimSpace(r.Agent) == "" {
			return nil, fmt.Errorf("golm: route rule %d names no agent", i)
		}
		if r.Pattern != "" {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("golm: route rule %d has an invalid pattern %q: %w", i, r.Pattern, err)
			}
			r.re = re
		}
		if len(r.Any) == 0 && len(r.All) == 0 && r.Prefix == "" && r.Pattern == "" {
			return nil, fmt.Errorf("golm: route rule %d for %q has no condition, so it would take every turn", i, r.Agent)
		}
		compiled[i] = r
	}
	return func(_ context.Context, in RouteInput) (Route, error) {
		text := in.Text()
		lower := strings.ToLower(text)
		for _, r := range compiled {
			if r.matches(text, lower) {
				return Route{Agent: r.Agent, Reason: r.reason()}, nil
			}
		}
		return Route{}, nil
	}, nil
}

func (r RouteRule) matches(text, lower string) bool {
	if r.Prefix != "" && !strings.HasPrefix(lower, strings.ToLower(r.Prefix)) {
		return false
	}
	if r.re != nil && !r.re.MatchString(text) {
		return false
	}
	for _, want := range r.All {
		if !strings.Contains(lower, strings.ToLower(want)) {
			return false
		}
	}
	if len(r.Any) > 0 {
		var hit bool
		for _, want := range r.Any {
			if strings.Contains(lower, strings.ToLower(want)) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

func (r RouteRule) reason() string {
	switch {
	case r.Prefix != "":
		return fmt.Sprintf("prefix %q", r.Prefix)
	case r.Pattern != "":
		return fmt.Sprintf("pattern %q", r.Pattern)
	case len(r.All) > 0:
		return "all of " + strings.Join(r.All, ", ")
	case len(r.Any) > 0:
		return "any of " + strings.Join(r.Any, ", ")
	}
	return "rule matched"
}

// RouteToLast keeps a conversation with whoever answered it last, falling through to next for a turn.
func RouteToLast(next Router) Router {
	return func(ctx context.Context, in RouteInput) (Route, error) {
		if in.Session != nil {
			if last, ok, _ := GetState[string](in.Session, lastAgentKey); ok && registered(last, in.Agents) {
				return Route{Agent: last, Reason: "continuing with the agent that answered last"}, nil
			}
		}
		if next == nil {
			return Route{}, nil
		}
		return next(ctx, in)
	}
}

func registered(name string, agents []AgentInfo) bool {
	if name == "" {
		return false
	}
	if len(agents) == 0 {
		return true
	}
	for _, a := range agents {
		if a.Name == name {
			return true
		}
	}
	return false
}

const lastAgentKey = "golm.last_agent"
