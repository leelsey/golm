// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"strings"
	"testing"
)

// A nil block must be inert everywhere.
func TestNilOrchestrationConfigIsInert(t *testing.T) {
	var oc *OrchestrationConfig
	if oc.Rules() != nil {
		t.Error("nil block produced rules")
	}
	r, err := oc.Router()
	if err != nil || r != nil {
		t.Errorf("Router = %v, %v; want nil, nil", r, err)
	}
	if got := oc.DelegationScope(); got != ScopeConversation {
		t.Errorf("scope = %q, want the default", got)
	}
	if err := oc.Validate(nil); err != nil {
		t.Errorf("Validate: %v", err)
	}
	o := NewOrchestrator(nil)
	oc.Apply(o)
	if o.Scope != "" || o.MaxFan != 0 {
		t.Errorf("a nil block changed the orchestrator: scope %q, max fan %d", o.Scope, o.MaxFan)
	}
}

func TestOrchestrationConfigApply(t *testing.T) {
	oc := &OrchestrationConfig{
		Scope: "CALL", StreamDelegates: true, MaxDepth: 2,
		MaxConversations: 5, MaxFan: 6, FanParallel: 3,
	}
	o := NewOrchestrator(nil)
	oc.Apply(o)
	if o.Scope != ScopeCall {
		t.Errorf("scope = %q; the enum must fold case", o.Scope)
	}
	if !o.StreamDelegates || o.MaxDepth != 2 || o.MaxConversations != 5 || o.MaxFan != 6 || o.FanParallel != 3 {
		t.Errorf("settings not applied: stream %v, depth %d, convs %d, fan %d/%d",
			o.StreamDelegates, o.MaxDepth, o.MaxConversations, o.MaxFan, o.FanParallel)
	}
	oc.Apply(nil)
}

// An unknown scope would otherwise fall through to the default with no warning.
func TestUnknownScopeIsRefused(t *testing.T) {
	err := (&OrchestrationConfig{Scope: "forever"}).Validate(nil)
	if err == nil || !strings.Contains(err.Error(), "conversation or call") {
		t.Errorf("err = %v", err)
	}
}

func TestConfiguredRouterCompiles(t *testing.T) {
	oc := &OrchestrationConfig{
		Routes: []RouteRuleConfig{
			{Agent: "scanner", Prefix: "/scan"},
			{Agent: "researcher", All: []string{"look", "up"}},
		},
	}
	if err := oc.Validate(map[string]bool{"scanner": true, "researcher": true}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	r, err := oc.Router()
	if err != nil {
		t.Fatalf("Router: %v", err)
	}
	for text, want := range map[string]string{
		"/scan me":        "scanner",
		"look it up":      "researcher",
		"unrelated thing": "",
	} {
		got, err := r(context.Background(), RouteInput{Message: UserText(text)})
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		if got.Agent != want {
			t.Errorf("%q -> %q, want %q", text, got.Agent, want)
		}
	}
}

func TestRouteToLastWrapsEvenWithNoRules(t *testing.T) {
	oc := &OrchestrationConfig{RouteToLast: true}
	r, err := oc.Router()
	if err != nil {
		t.Fatalf("Router: %v", err)
	}
	if r == nil {
		t.Fatal("route_to_last alone produced no router")
	}

	got, err := r(context.Background(), RouteInput{Session: NewSession(), Message: UserText("x")})
	if err != nil || got.Agent != "" {
		t.Errorf("got %+v, %v; want no opinion", got, err)
	}
}

func TestValidateRejectsRoutesToUndefinedAgents(t *testing.T) {
	oc := &OrchestrationConfig{Routes: []RouteRuleConfig{{Agent: "ghost", Any: []string{"x"}}}}
	err := oc.Validate(map[string]bool{"real": true})
	if err == nil || !strings.Contains(err.Error(), "not a defined agent") {
		t.Errorf("err = %v", err)
	}
	if err := oc.Validate(map[string]bool{"ghost": true}); err != nil {
		t.Errorf("a rule naming a defined agent was refused: %v", err)
	}
}

func TestWithConversationIgnoresAnEmptyID(t *testing.T) {
	ctx := withConversation(context.Background(), "")
	if conversationOf(ctx) != "" {
		t.Error("an empty id became a conversation")
	}
	ctx = withConversation(context.Background(), "CONV")
	if conversationOf(ctx) != "CONV" {
		t.Error("the conversation did not survive")
	}
}
