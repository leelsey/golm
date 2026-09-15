// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func routerSetup(t *testing.T) (*Orchestrator, *fakeProvider, *fakeProvider) {
	t.Helper()
	answer := func(text string) []Response {
		return []Response{{Message: AssistantText(text), StopReason: StopEndTurn}}
	}
	mainP := &fakeProvider{responses: answer("main answered")}
	subP := &fakeProvider{responses: answer("researcher answered")}
	o := NewOrchestrator(NewBus())
	o.Add("main", "main", &Agent{Provider: mainP, Model: "m"})
	o.Add("researcher", "sub", &Agent{Provider: subP, Model: "s"})
	o.Describe("researcher", "looks things up")
	return o, mainP, subP
}

// The point of the seam: a turn routed by a rule never reaches the main model at all.
func TestRouterSkipsTheMainAgentEntirely(t *testing.T) {
	o, mainP, subP := routerSetup(t)
	r, err := RouteRules([]RouteRule{{Agent: "researcher", Any: []string{"research", "look up"}}})
	if err != nil {
		t.Fatalf("RouteRules: %v", err)
	}
	o.Router = r

	res, err := o.Run(context.Background(), NewSession(), "Please RESEARCH the protocol")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text() != "researcher answered" {
		t.Errorf("answer = %q, want the routed agent's", res.Text())
	}
	if mainP.calls != 0 {
		t.Errorf("the main model was called %d times; routing should have spent nothing", mainP.calls)
	}
	if subP.calls != 1 {
		t.Errorf("routed agent called %d times", subP.calls)
	}
}

// A rule that does not match must change nothing.
func TestRouterFallsThroughToMain(t *testing.T) {
	o, mainP, subP := routerSetup(t)
	r, _ := RouteRules([]RouteRule{{Agent: "researcher", Any: []string{"research"}}})
	o.Router = r
	res, err := o.Run(context.Background(), NewSession(), "how are you")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text() != "main answered" || mainP.calls != 1 || subP.calls != 0 {
		t.Errorf("unmatched turn went to %q (main %d, sub %d)", res.Text(), mainP.calls, subP.calls)
	}
}

func TestRouteRuleConditions(t *testing.T) {
	cases := []struct {
		name  string
		rule  RouteRule
		text  string
		match bool
	}{
		{"any matches one", RouteRule{Agent: "a", Any: []string{"x", "find"}}, "please FIND it", true},
		{"any matches none", RouteRule{Agent: "a", Any: []string{"x", "y"}}, "nothing here", false},
		{"all needs every term", RouteRule{Agent: "a", All: []string{"scan", "port"}}, "scan the ports", true},
		{"all missing one", RouteRule{Agent: "a", All: []string{"scan", "port"}}, "scan the host", false},
		{"prefix", RouteRule{Agent: "a", Prefix: "/research"}, "/RESEARCH this", true},
		{"prefix elsewhere", RouteRule{Agent: "a", Prefix: "/research"}, "please /research", false},
		{"pattern", RouteRule{Agent: "a", Pattern: `CVE-\d{4}-\d+`}, "check CVE-2026-1234", true},
		{"conditions are ANDed", RouteRule{Agent: "a", Prefix: "/scan", Any: []string{"port"}}, "/scan the host", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := RouteRules([]RouteRule{c.rule})
			if err != nil {
				t.Fatalf("RouteRules: %v", err)
			}
			got, err := r(context.Background(), RouteInput{Message: UserText(c.text)})
			if err != nil {
				t.Fatalf("route: %v", err)
			}
			if (got.Agent != "") != c.match {
				t.Errorf("matched=%v (agent %q), want %v", got.Agent != "", got.Agent, c.match)
			}
			if c.match && got.Reason == "" {
				t.Error("a routing decision nobody can explain is one nobody can fix")
			}
		})
	}
}

func TestRouteRulesRefuseNonsense(t *testing.T) {
	bad := []struct {
		name string
		rule RouteRule
		want string
	}{
		{"no agent", RouteRule{Any: []string{"x"}}, "names no agent"},
		{"no condition", RouteRule{Agent: "a"}, "no condition"},
		{"bad pattern", RouteRule{Agent: "a", Pattern: "("}, "invalid pattern"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			_, err := RouteRules([]RouteRule{c.rule})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one mentioning %q", err, c.want)
			}
		})
	}
}

// A router naming an agent that is not registered is a configuration error.
func TestRouterToAnUnknownAgentIsAnError(t *testing.T) {
	o, _, _ := routerSetup(t)
	o.Router = func(context.Context, RouteInput) (Route, error) {
		return Route{Agent: "nobody"}, nil
	}
	_, err := o.Run(context.Background(), NewSession(), "x")
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Errorf("err = %v, want one naming the unregistered agent", err)
	}
}

// A follow-up carries none of the words that routed the first turn.
func TestRouteToLastKeepsAFollowUpWithTheSameAgent(t *testing.T) {
	o, mainP, subP := routerSetup(t)
	keywords, _ := RouteRules([]RouteRule{{Agent: "researcher", Any: []string{"research"}}})
	o.Router = RouteToLast(keywords)

	sess := NewSession()
	if _, err := o.Run(context.Background(), sess, "research the protocol"); err != nil {
		t.Fatalf("first: %v", err)
	}

	subP.responses = append(subP.responses, Response{Message: AssistantText("still the researcher"), StopReason: StopEndTurn})
	res, err := o.Run(context.Background(), sess, "and the second one?")
	if err != nil {
		t.Fatalf("follow-up: %v", err)
	}
	if res.Text() != "still the researcher" {
		t.Errorf("follow-up answered by %q", res.Text())
	}
	if mainP.calls != 0 {
		t.Errorf("the follow-up reached the main model %d times", mainP.calls)
	}
}

func TestRouterSeesTheRegisteredAgents(t *testing.T) {
	o, _, _ := routerSetup(t)
	var saw []AgentInfo
	o.Router = func(_ context.Context, in RouteInput) (Route, error) {
		saw = in.Agents
		return Route{}, nil
	}
	if _, err := o.Run(context.Background(), NewSession(), "x"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(saw) != 2 {
		t.Fatalf("router saw %d agents, want 2", len(saw))
	}

	if saw[0].Name != "main" || saw[1].Name != "researcher" {
		t.Errorf("agents = %+v, want them sorted by name", saw)
	}
	if saw[1].Description != "looks things up" || saw[1].Role != "sub" {
		t.Errorf("researcher info = %+v", saw[1])
	}
}

func TestRouterErrorStopsTheRun(t *testing.T) {
	o, mainP, _ := routerSetup(t)
	o.Router = func(context.Context, RouteInput) (Route, error) {
		return Route{}, context.DeadlineExceeded
	}
	if _, err := o.Run(context.Background(), NewSession(), "x"); err == nil {
		t.Fatal("a failing router did not stop the run")
	}
	if mainP.calls != 0 {
		t.Error("the run proceeded after the router failed")
	}
}

// Routing must not disturb what already works.
func TestRouterCoexistsWithDelegation(t *testing.T) {
	subP := &fakeProvider{responses: []Response{{Message: AssistantText("sub done"), StopReason: StopEndTurn}}}
	mainP := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "d1", Name: "sub", Input: json.RawMessage(`{"task":"x"}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("main done"), StopReason: StopEndTurn},
	}}
	o := NewOrchestrator(nil)
	o.Add("main", "main", &Agent{Provider: mainP, Model: "m"})
	o.Add("sub", "sub", &Agent{Provider: subP, Model: "s"})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}
	o.Router, _ = RouteRules([]RouteRule{{Agent: "sub", Prefix: "/sub"}})
	res, err := o.Run(context.Background(), NewSession(), "do it")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text() != "main done" || subP.calls != 1 {
		t.Errorf("answer %q, sub calls %d", res.Text(), subP.calls)
	}
}

// A session outlives the configuration.
func TestRouteToLastFallsThroughWhenTheAgentIsGone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		last   string
		agents []AgentInfo
		want   string
	}{
		{"still registered", "research", []AgentInfo{{Name: "research"}}, "research"},
		{"removed", "retired", []AgentInfo{{Name: "research"}}, "fallback"},

		{"no agents listed", "research", nil, "research"},
		{"never routed", "", []AgentInfo{{Name: "research"}}, "fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSession()
			if tc.last != "" {
				if err := s.SetState(lastAgentKey, tc.last); err != nil {
					t.Fatal(err)
				}
			}
			r := RouteToLast(func(context.Context, RouteInput) (Route, error) {
				return Route{Agent: "fallback"}, nil
			})
			got, err := r(context.Background(), RouteInput{Session: s, Agents: tc.agents})
			if err != nil {
				t.Fatal(err)
			}
			if got.Agent != tc.want {
				t.Errorf("routed to %q, want %q", got.Agent, tc.want)
			}
		})
	}
}
