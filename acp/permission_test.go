// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package acp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/acp"
)

func gatedAgent(t *testing.T, rules *golm.PolicyRules) (*acp.Agent, *bool) {
	t.Helper()
	ran := false
	reg := golm.NewRegistry()
	reg.Register(golm.WithTraits(
		golm.TextTool("run", "runs a program", "cmd", "the command",
			func(context.Context, string) (string, error) { ran = true; return "output", nil }),
		golm.ToolTraits{Process: true}))
	p := &streamingTools{}
	inner := &golm.Agent{Provider: p, Model: "m", Tools: reg}
	a := acp.NewAgent(inner)
	rules.Approve = a.Approver()
	inner.ToolPolicy = rules.Policy()
	return a, &ran
}

// The gate a deployment already configured, answered by the one surface that has a human on it.
func TestPermissionAllowed(t *testing.T) {
	a, ran := gatedAgent(t, &golm.PolicyRules{AskTraits: []string{"process"}})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	var asked acp.RequestPermissionRequest
	e.mu.Lock()
	e.permission = func(req acp.RequestPermissionRequest) acp.PermissionOutcome {
		asked = req
		return acp.PermissionOutcome{Outcome: acp.OutcomeSelected, OptionID: "allow-once"}
	}
	e.mu.Unlock()

	id := e.newSession(t)
	e.prompt(t, id, "run it")

	if !*ran {
		t.Error("an approved tool did not run")
	}
	if asked.SessionID != id {
		t.Errorf("the request named session %q, want %q", asked.SessionID, id)
	}

	if !strings.Contains(asked.ToolCall.Title, "run") || !strings.Contains(asked.ToolCall.Title, "ls") {
		t.Errorf("title = %q; it should say what is being run", asked.ToolCall.Title)
	}
	if !strings.Contains(asked.ToolCall.Title, "process") {
		t.Errorf("title = %q; it should say what the tool touches", asked.ToolCall.Title)
	}
	if asked.ToolCall.Kind != acp.KindExecute {
		t.Errorf("kind = %q", asked.ToolCall.Kind)
	}
	if len(asked.ToolCall.RawInput) == 0 {
		t.Error("the editor was not given the arguments verbatim")
	}

	kinds := map[string]bool{}
	for _, o := range asked.Options {
		kinds[o.Kind] = true
		if o.OptionID == "" || o.Name == "" {
			t.Errorf("option %+v is missing a field", o)
		}
	}
	for _, want := range []string{acp.PermAllowOnce, acp.PermAllowAlways, acp.PermRejectOnce, acp.PermRejectAlways} {
		if !kinds[want] {
			t.Errorf("option kind %q was not offered", want)
		}
	}
}

func TestPermissionRejected(t *testing.T) {
	a, ran := gatedAgent(t, &golm.PolicyRules{AskTraits: []string{"process"}})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	e.mu.Lock()
	e.permission = func(acp.RequestPermissionRequest) acp.PermissionOutcome {
		return acp.PermissionOutcome{Outcome: acp.OutcomeSelected, OptionID: "reject-once"}
	}
	e.mu.Unlock()
	id := e.newSession(t)
	e.prompt(t, id, "run it")
	if *ran {
		t.Error("a rejected tool ran anyway")
	}
}

// "Always" means always: asking again after being told so is how an operator learns to stop reading the question.
func TestAllowAlwaysIsRemembered(t *testing.T) {
	a, _ := gatedAgent(t, &golm.PolicyRules{AskTraits: []string{"process"}})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	var asks int
	e.mu.Lock()
	e.permission = func(acp.RequestPermissionRequest) acp.PermissionOutcome {
		asks++
		return acp.PermissionOutcome{Outcome: acp.OutcomeSelected, OptionID: "allow-always"}
	}
	e.mu.Unlock()
	id := e.newSession(t)
	for i := 0; i < 3; i++ {
		e.prompt(t, id, "run it")
	}
	if asks != 1 {
		t.Errorf("asked %d times after being told always", asks)
	}
}

func TestRejectAlwaysIsRemembered(t *testing.T) {
	a, ran := gatedAgent(t, &golm.PolicyRules{AskTraits: []string{"process"}})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	var asks int
	e.mu.Lock()
	e.permission = func(acp.RequestPermissionRequest) acp.PermissionOutcome {
		asks++
		return acp.PermissionOutcome{Outcome: acp.OutcomeSelected, OptionID: "reject-always"}
	}
	e.mu.Unlock()
	id := e.newSession(t)
	for i := 0; i < 3; i++ {
		e.prompt(t, id, "run it")
	}
	if asks != 1 {
		t.Errorf("asked %d times after being told never", asks)
	}
	if *ran {
		t.Error("the tool ran despite a standing refusal")
	}
}

// An answer nobody can interpret must refuse.
func TestUnknownOptionRefuses(t *testing.T) {
	a, ran := gatedAgent(t, &golm.PolicyRules{AskTraits: []string{"process"}})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	e.mu.Lock()
	e.permission = func(acp.RequestPermissionRequest) acp.PermissionOutcome {
		return acp.PermissionOutcome{Outcome: acp.OutcomeSelected, OptionID: "something-else"}
	}
	e.mu.Unlock()
	id := e.newSession(t)
	e.prompt(t, id, "run it")
	if *ran {
		t.Error("a tool ran on an answer the agent did not offer")
	}
}

// A turn abandoned while the question was open is not a refusal.
func TestCancelledPermissionIsNotARefusal(t *testing.T) {
	a, ran := gatedAgent(t, &golm.PolicyRules{AskTraits: []string{"process"}})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	e.mu.Lock()
	e.permission = func(acp.RequestPermissionRequest) acp.PermissionOutcome {
		return acp.PermissionOutcome{Outcome: acp.OutcomeCancelled}
	}
	e.mu.Unlock()
	id := e.newSession(t)
	var resp acp.PromptResponse
	if err := e.call(t, acp.MethodPrompt, acp.PromptRequest{
		SessionID: id, Prompt: []acp.ContentBlock{acp.TextBlock("run it")},
	}, &resp); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if *ran {
		t.Error("the tool ran after the question was cancelled")
	}
}

// A tool a rule ALLOWS outright must not reach the editor at all.
func TestAllowedToolIsNeverAskedAbout(t *testing.T) {
	a, ran := gatedAgent(t, &golm.PolicyRules{Allow: []string{"run"}, AskTraits: []string{"process"}})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	var asks int
	e.mu.Lock()
	e.permission = func(acp.RequestPermissionRequest) acp.PermissionOutcome {
		asks++
		return acp.PermissionOutcome{Outcome: acp.OutcomeSelected, OptionID: "allow-once"}
	}
	e.mu.Unlock()
	id := e.newSession(t)
	e.prompt(t, id, "run it")
	if asks != 0 {
		t.Errorf("the editor was asked %d times about a tool the rules allow", asks)
	}
	if !*ran {
		t.Error("an allowed tool did not run")
	}
}

// Outside a turn there is no session to ask in.
func TestApproverOutsideATurnRefuses(t *testing.T) {
	a, _ := gatedAgent(t, &golm.PolicyRules{})
	connect(t, a)
	ok, err := a.Approver()(context.Background(), golm.ToolRequest{Call: golm.ToolUse{Name: "run"}})
	if ok {
		t.Error("approved without a session")
	}
	if err == nil || !strings.Contains(err.Error(), "session") {
		t.Errorf("err = %v", err)
	}
}
