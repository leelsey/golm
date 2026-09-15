// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestOrchestratorDelegation(t *testing.T) {
	mainProv := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "d1", Name: "researcher", Input: json.RawMessage(`{"task":"find X"}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("final answer"), StopReason: StopEndTurn},
	}}
	subProv := &fakeProvider{responses: []Response{
		{Message: AssistantText("sub result"), StopReason: StopEndTurn},
	}}

	o := NewOrchestrator(NewBus())
	o.Add("main", "main", &Agent{Provider: mainProv, Model: "m"})
	o.Add("researcher", "sub", &Agent{Provider: subProv, Model: "s"})
	if err := o.WireDelegation(map[string]string{"researcher": "delegates research"}); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}

	sess := NewSession()
	res, err := o.Run(context.Background(), sess, "do research")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text() != "final answer" {
		t.Errorf("final = %q", res.Text())
	}
	if subProv.calls != 1 {
		t.Errorf("sub calls = %d, want 1", subProv.calls)
	}
	if mainProv.calls != 2 {
		t.Errorf("main calls = %d, want 2", mainProv.calls)
	}

	hist := sess.History()
	if len(hist) < 3 {
		t.Fatalf("history too short: %d", len(hist))
	}
	tr, ok := hist[2].Content[0].(ToolResult)
	if !ok || tr.Text() != "sub result" {
		t.Errorf("delegation result not propagated: %+v", hist[2].Content[0])
	}
}

func TestOrchestratorRequiresMain(t *testing.T) {
	o := NewOrchestrator(nil)
	if _, err := o.Run(context.Background(), NewSession(), "x"); err == nil {
		t.Errorf("expected error with no main agent")
	}
}

func TestAgentParallelTools(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "a", Name: "echo", Input: json.RawMessage(`{"n":1}`)},
			ToolUse{ID: "b", Name: "echo", Input: json.RawMessage(`{"n":2}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("done"), StopReason: StopEndTurn},
	}}
	reg := NewRegistry()
	reg.Register(echoTool())
	sess := NewSession()
	agent := &Agent{Provider: fp, Tools: reg, ParallelTools: true}

	if _, err := agent.Run(context.Background(), sess, "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	toolMsg := sess.History()[2]
	if len(toolMsg.Content) != 2 {
		t.Fatalf("results = %d, want 2", len(toolMsg.Content))
	}
	if toolMsg.Content[0].(ToolResult).ToolUseID != "a" || toolMsg.Content[1].(ToolResult).ToolUseID != "b" {
		t.Errorf("result order not preserved: %+v", toolMsg.Content)
	}
}

func TestBusReceivesAgentLifecycle(t *testing.T) {
	bus := NewBus()
	events, unsub := bus.Subscribe("*")
	defer unsub()

	fp := &fakeProvider{responses: []Response{{Message: AssistantText("hi"), StopReason: StopEndTurn}}}
	agent := &Agent{Provider: fp, Name: "solo", Bus: bus}
	if _, err := agent.Run(context.Background(), NewSession(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	bus.Close()

	kinds := map[string]bool{}
	for ev := range events {
		kinds[ev.Kind] = true
	}
	for _, want := range []string{"start", "message", "done"} {
		if !kinds[want] {
			t.Errorf("missing %q lifecycle event; got %v", want, kinds)
		}
	}
}

func TestOrchestratorUsageCountedOnFailedDelegation(t *testing.T) {
	resp := Response{
		Message:    Message{Role: RoleAssistant, Content: []Content{ToolUse{ID: "c", Name: "echo", Input: json.RawMessage(`{}`)}}},
		StopReason: StopToolUse,
		Usage:      Usage{InputTokens: 10, OutputTokens: 5},
	}
	fp := &fakeProvider{responses: []Response{resp, resp, resp}}
	reg := NewRegistry()
	reg.Register(echoTool())
	sub := &Agent{Provider: fp, Model: "x", Tools: reg, MaxSteps: 3}

	o := NewOrchestrator(nil)
	o.Add("worker", "sub", sub)
	tool := o.AsTool("worker", "d")
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"go"}`)); err == nil {
		t.Fatal("expected max-steps failure from the delegated run")
	}
	if u := o.Usage(); u.InputTokens == 0 {
		t.Fatalf("Usage = %+v; failed delegation's consumed tokens were dropped", u)
	}
}

// A sub-agent that can be registered without the orchestrator's gate is not gated at all.
func TestOrchestratorPropagatesToolPolicy(t *testing.T) {
	o := NewOrchestrator(nil)
	o.ToolPolicy = denyAll("orchestrator")

	inherits := &Agent{Provider: &fakeProvider{}, Model: "s"}
	own := &Agent{Provider: &fakeProvider{}, Model: "s", ToolPolicy: denyAll("own")}
	o.Add("inherits", "sub", inherits)
	o.Add("own", "sub", own)

	if inherits.ToolPolicy == nil {
		t.Fatal("a sub-agent added without a policy was left ungated")
	}
	if _, err := inherits.ToolPolicy(context.Background(), ToolRequest{}); err == nil ||
		!strings.Contains(err.Error(), "orchestrator") {
		t.Errorf("inherited policy = %v, want the orchestrator's", err)
	}
	if _, err := own.ToolPolicy(context.Background(), ToolRequest{}); err == nil ||
		!strings.Contains(err.Error(), "own") {
		t.Errorf("the agent's own policy was replaced: %v", err)
	}
}
