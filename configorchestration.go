// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"fmt"
	"strings"
)

// OrchestrationConfig is the configured form of an Orchestrator's settings.
type OrchestrationConfig struct {
	Scope string `json:"scope,omitempty"`

	StreamDelegates bool `json:"stream_delegates,omitempty"`

	MaxDepth int `json:"max_depth,omitempty"`

	MaxConversations int `json:"max_conversations,omitempty"`

	FanOut bool `json:"fan_out,omitempty"`

	MaxFan      int `json:"max_fan,omitempty"`
	FanParallel int `json:"fan_parallel,omitempty"`

	Routes []RouteRuleConfig `json:"routes,omitempty"`

	RouteToLast bool `json:"route_to_last,omitempty"`
}

// RouteRuleConfig is one routing rule.
type RouteRuleConfig struct {
	Agent   string   `json:"agent"`
	Any     []string `json:"any,omitempty"`
	All     []string `json:"all,omitempty"`
	Prefix  string   `json:"prefix,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
}

// Rules converts the configured rules to their runtime form.
func (oc *OrchestrationConfig) Rules() []RouteRule {
	if oc == nil {
		return nil
	}
	out := make([]RouteRule, 0, len(oc.Routes))
	for _, r := range oc.Routes {
		out = append(out, RouteRule{
			Agent: r.Agent, Any: r.Any, All: r.All, Prefix: r.Prefix, Pattern: r.Pattern,
		})
	}
	return out
}

// Router compiles the block's routing rules, wrapping them in RouteToLast when asked.
func (oc *OrchestrationConfig) Router() (Router, error) {
	if oc == nil {
		return nil, nil
	}
	var inner Router
	if len(oc.Routes) > 0 {
		r, err := RouteRules(oc.Rules())
		if err != nil {
			return nil, err
		}
		inner = r
	}
	if oc.RouteToLast {
		return RouteToLast(inner), nil
	}
	return inner, nil
}

// DelegationScope is the parsed scope.
func (oc *OrchestrationConfig) DelegationScope() DelegationScope {
	if oc != nil && strings.EqualFold(oc.Scope, string(ScopeCall)) {
		return ScopeCall
	}
	return ScopeConversation
}

var validScopes = map[string]bool{"": true, "conversation": true, "call": true}

// Validate checks the block against the agents that exist.
func (oc *OrchestrationConfig) Validate(agents map[string]bool) error {
	if oc == nil {
		return nil
	}
	if !validScopes[strings.ToLower(strings.TrimSpace(oc.Scope))] {
		return fmt.Errorf("golm: orchestration.scope is %q (want conversation or call)", oc.Scope)
	}
	for field, v := range map[string]int{
		"max_depth": oc.MaxDepth, "max_conversations": oc.MaxConversations,
		"max_fan": oc.MaxFan, "fan_parallel": oc.FanParallel,
	} {
		if v < 0 {
			return fmt.Errorf("golm: orchestration.%s is negative (%d)", field, v)
		}
	}
	for i, r := range oc.Routes {
		if strings.TrimSpace(r.Agent) == "" {
			return fmt.Errorf("golm: orchestration.routes[%d] names no agent", i)
		}
		if !agents[r.Agent] {
			return fmt.Errorf("golm: orchestration.routes[%d] sends turns to %q, which is not a defined agent", i, r.Agent)
		}
	}

	if _, err := RouteRules(oc.Rules()); err != nil {
		return err
	}
	return nil
}

// Apply puts the configured settings on o.
func (oc *OrchestrationConfig) Apply(o *Orchestrator) {
	if oc == nil || o == nil {
		return
	}
	o.Scope = oc.DelegationScope()
	o.StreamDelegates = oc.StreamDelegates
	o.MaxDepth = oc.MaxDepth
	o.MaxConversations = oc.MaxConversations
	o.MaxFan = oc.MaxFan
	o.FanParallel = oc.FanParallel
}
