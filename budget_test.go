// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type costly struct {
	resp  Response
	per   Usage
	calls int
}

func (c *costly) Name() string               { return "costly" }
func (c *costly) Capabilities() Capabilities { return Capabilities{Tools: true} }

func (c *costly) Complete(_ context.Context, _ Request) (Response, error) {
	c.calls++
	r := c.resp
	r.Usage = c.per
	return r, nil
}

func (c *costly) Stream(ctx context.Context, req Request, fn func(StreamEvent) error) (Response, error) {
	return c.Complete(ctx, req)
}

func TestBudgetStopsRunBeforeTheNextCall(t *testing.T) {
	p := &costly{
		resp: Response{Message: AssistantText("ok"), StopReason: StopEndTurn},
		per:  Usage{InputTokens: 60, OutputTokens: 40},
	}
	b := NewBudget(150)
	a := &Agent{Provider: p, Model: "m", Budget: b}

	if _, err := a.Run(context.Background(), NewSession(), "one"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := b.Spent(); got != 100 {
		t.Fatalf("spent = %d, want 100", got)
	}

	if _, err := a.Run(context.Background(), NewSession(), "two"); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := b.Spent(); got != 200 {
		t.Fatalf("spent = %d, want 200", got)
	}

	before := p.calls
	_, err := a.Run(context.Background(), NewSession(), "three")
	if !errors.Is(err, ErrTokenBudget) {
		t.Fatalf("third run err = %v, want ErrTokenBudget", err)
	}
	if p.calls != before {
		t.Errorf("provider called %d times past the ceiling", p.calls-before)
	}
}

// The hole this closes: a delegation is a tool call.
func TestBudgetCoversDelegatedSubAgents(t *testing.T) {
	mainProv := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "d1", Name: "sub", Input: json.RawMessage(`{"task":"spend"}`)},
		}}, StopReason: StopToolUse, Usage: Usage{InputTokens: 10, OutputTokens: 10}},
		{Message: AssistantText("done"), StopReason: StopEndTurn, Usage: Usage{InputTokens: 10, OutputTokens: 10}},
	}}
	subProv := &costly{
		resp: Response{Message: AssistantText("sub done"), StopReason: StopEndTurn},
		per:  Usage{InputTokens: 500, OutputTokens: 500},
	}

	o := NewOrchestrator(nil)
	o.Budget = NewBudget(300)
	o.Add("main", "main", &Agent{Provider: mainProv, Model: "m"})
	o.Add("sub", "sub", &Agent{Provider: subProv, Model: "s"})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}

	res, err := o.Run(context.Background(), NewSession(), "delegate")

	if !errors.Is(err, ErrTokenBudget) {
		t.Fatalf("err = %v, want ErrTokenBudget once the sub-agent overspent", err)
	}
	if o.Budget.Spent() < 1000 {
		t.Errorf("shared budget saw %d tokens, want the sub-agent's 1000 counted", o.Budget.Spent())
	}
	if res.Steps == 0 {
		t.Error("Result should still report the steps the run took")
	}
}

func TestBudgetHandsDownToAgentsWithoutOne(t *testing.T) {
	o := NewOrchestrator(nil)
	o.Budget = NewBudget(99)
	own := NewBudget(7)
	o.Add("a", "main", &Agent{Provider: &fakeProvider{}, Model: "m"})
	o.Add("b", "sub", &Agent{Provider: &fakeProvider{}, Model: "m", Budget: own})

	a, _ := o.Get("a")
	if a.Budget != o.Budget {
		t.Error("an agent without a budget should inherit the orchestrator's")
	}
	b, _ := o.Get("b")
	if b.Budget != own {
		t.Error("an agent with its own budget must keep it")
	}
}

func TestBudgetNilIsUnbounded(t *testing.T) {
	var b *Budget
	b.Spend(Usage{InputTokens: 10})
	if b.Exhausted() || b.Spent() != 0 || b.Remaining() != -1 {
		t.Error("a nil budget must be inert and unbounded")
	}
	if !strings.Contains((&Budget{}).String(), "no ceiling") {
		t.Error("a zero budget is unbounded and should say so")
	}
}

// An explicit parallel_tools decision must survive delegation wiring.
func TestWireDelegationKeepsAnExplicitParallelChoice(t *testing.T) {
	o := NewOrchestrator(nil)
	o.Add("main", "main", &Agent{Provider: &fakeProvider{}, Model: "m", ParallelTools: false})
	o.Add("sub", "sub", &Agent{Provider: &fakeProvider{}, Model: "s"})
	o.KeepParallelTools()
	if err := o.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}
	if main := o.Main(); main.ParallelTools {
		t.Error("WireDelegation overruled an explicit serial choice")
	}
}

func TestWireDelegationDefaultsToParallel(t *testing.T) {
	o := NewOrchestrator(nil)
	o.Add("main", "main", &Agent{Provider: &fakeProvider{}, Model: "m"})
	o.Add("sub", "sub", &Agent{Provider: &fakeProvider{}, Model: "s"})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}
	if main := o.Main(); !main.ParallelTools {
		t.Error("delegation should run concurrently unless told otherwise")
	}
}

// WireDelegation only gives the main agent delegation tools.
func TestDelegationDepthIsBounded(t *testing.T) {
	answerWithDelegation := func(to string) Response {
		return Response{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "d", Name: to, Input: json.RawMessage(`{"task":"again"}`)},
		}}, StopReason: StopToolUse}
	}

	provA := &loopProvider{next: func() Response { return answerWithDelegation("b") }}
	provB := &loopProvider{next: func() Response { return answerWithDelegation("a") }}

	o := NewOrchestrator(nil)
	o.MaxDepth = 2
	agentA := &Agent{Provider: provA, Model: "a", MaxSteps: 3, Tools: NewRegistry()}
	agentB := &Agent{Provider: provB, Model: "b", MaxSteps: 3, Tools: NewRegistry()}
	o.Add("a", "main", agentA)
	o.Add("b", "sub", agentB)
	agentA.Tools.Register(o.AsTool("b", "delegate to b"))
	agentB.Tools.Register(o.AsTool("a", "delegate back to a"))

	res, err := o.Run(context.Background(), NewSession(), "start looping")

	_ = err
	if res.Steps == 0 {
		t.Fatal("the run made no steps at all")
	}
	if provA.calls > 50 || provB.calls > 50 {
		t.Fatalf("delegation recursed unboundedly: %d + %d provider calls", provA.calls, provB.calls)
	}
	if !provB.sawDepthRefusal && !provA.sawDepthRefusal {
		t.Error("nothing was ever refused for nesting; the guard did not engage")
	}
}

type loopProvider struct {
	next            func() Response
	calls           int
	sawDepthRefusal bool
}

func (l *loopProvider) Name() string               { return "loop" }
func (l *loopProvider) Capabilities() Capabilities { return Capabilities{Tools: true} }

func (l *loopProvider) Complete(_ context.Context, req Request) (Response, error) {
	l.calls++
	for _, m := range req.Messages {
		if m.Role != RoleTool {
			continue
		}

		for _, c := range m.Content {
			tr, ok := c.(ToolResult)
			if ok && strings.Contains(tr.Text(), "past the limit of") {
				l.sawDepthRefusal = true
				return Response{Message: AssistantText("stopping"), StopReason: StopEndTurn}, nil
			}
		}
	}
	if l.calls > 40 {
		return Response{Message: AssistantText("giving up"), StopReason: StopEndTurn}, nil
	}
	return l.next(), nil
}

func (l *loopProvider) Stream(ctx context.Context, req Request, fn func(StreamEvent) error) (Response, error) {
	return l.Complete(ctx, req)
}

// Assigning the field after Add changes the orchestrator's idea of its ceiling and nothing else.
func TestSetBudgetReachesAgentsAlreadyAdded(t *testing.T) {
	o := NewOrchestrator(nil)
	o.Add("main", "main", &Agent{Provider: &fakeProvider{}, Model: "m"})
	o.Add("sub", "sub", &Agent{Provider: &fakeProvider{}, Model: "s"})

	o.Budget = NewBudget(10)
	if a, _ := o.Get("main"); a.Budget != nil {
		t.Fatal("the field reached an agent added before it; this test is checking the wrong thing")
	}

	b := NewBudget(99)
	o.SetBudget(b)
	for _, name := range []string{"main", "sub"} {
		a, _ := o.Get(name)
		if a.Budget != b {
			t.Errorf("%s did not receive the budget", name)
		}
	}
	if o.Budget != b {
		t.Error("the orchestrator's own field was not updated")
	}
}

func TestSetBudgetLeavesAnAgentsOwnAlone(t *testing.T) {
	own := NewBudget(5)
	o := NewOrchestrator(nil)
	o.Add("special", "sub", &Agent{Provider: &fakeProvider{}, Model: "s", Budget: own})
	o.SetBudget(NewBudget(99))
	a, _ := o.Get("special")
	if a.Budget != own {
		t.Error("an agent's own budget was overwritten")
	}
}

// The end-to-end property: a ceiling set after construction stops a delegating run.
func TestSetBudgetStopsADelegatingRun(t *testing.T) {
	mainProv := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "d1", Name: "sub", Input: json.RawMessage(`{"task":"spend"}`)},
		}}, StopReason: StopToolUse, Usage: Usage{InputTokens: 10, OutputTokens: 10}},
		{Message: AssistantText("done"), StopReason: StopEndTurn, Usage: Usage{InputTokens: 10, OutputTokens: 10}},
	}}
	o := NewOrchestrator(nil)
	o.Add("main", "main", &Agent{Provider: mainProv, Model: "m"})
	o.Add("sub", "sub", &Agent{Provider: &costly{
		resp: Response{Message: AssistantText("sub"), StopReason: StopEndTurn},
		per:  Usage{InputTokens: 400, OutputTokens: 400},
	}, Model: "s"})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}

	o.SetBudget(NewBudget(300))

	if _, err := o.Run(context.Background(), NewSession(), "go"); !errors.Is(err, ErrTokenBudget) {
		t.Fatalf("err = %v, want ErrTokenBudget", err)
	}
}

func TestSetToolPolicyReachesAgentsAlreadyAdded(t *testing.T) {
	o := NewOrchestrator(nil)
	o.Add("main", "main", &Agent{Provider: &fakeProvider{}, Model: "m"})
	var called bool
	o.SetToolPolicy(func(ctx context.Context, _ ToolRequest) (context.Context, error) {
		called = true
		return ctx, nil
	})
	a, _ := o.Get("main")
	if a.ToolPolicy == nil {
		t.Fatal("the gate did not reach an agent added before it")
	}
	_, _ = a.ToolPolicy(context.Background(), ToolRequest{})
	if !called {
		t.Error("the installed policy was not the one handed down")
	}
}

func TestBudgetReporting(t *testing.T) {
	b := NewBudget(100)
	if b.Limit() != 100 || b.Remaining() != 100 || b.Calls() != 0 || b.LostCompletions() != 0 {
		t.Fatalf("fresh budget: limit %d remaining %d calls %d lost %d",
			b.Limit(), b.Remaining(), b.Calls(), b.LostCompletions())
	}
	b.Spend(Usage{InputTokens: 30, OutputTokens: 10})
	if b.Spent() != 40 || b.Remaining() != 60 || b.Calls() != 1 {
		t.Errorf("after one call: spent %d remaining %d calls %d", b.Spent(), b.Remaining(), b.Calls())
	}

	b.Spend(Usage{InputTokens: 200})
	if b.Remaining() != 0 {
		t.Errorf("Remaining = %d, want 0 once over", b.Remaining())
	}
	if !b.Exhausted() {
		t.Error("not reported exhausted")
	}
	if s := b.String(); !strings.Contains(s, "240/100") || !strings.Contains(s, "2 call") {
		t.Errorf("String = %q", s)
	}
}

// Lost completions were billed but their token counts were in the response that never arrived.
func TestBudgetReportsLostCompletionsSeparately(t *testing.T) {
	b := NewBudget(1000)
	b.Spend(Usage{InputTokens: 10, OutputTokens: 5, LostCompletions: 2})
	if b.LostCompletions() != 2 {
		t.Errorf("lost = %d, want 2", b.LostCompletions())
	}
	if b.Spent() != 15 {
		t.Errorf("spent = %d; a lost completion's tokens are unknown, not zero-then-counted", b.Spent())
	}
	s := b.String()
	if !strings.Contains(s, "2 billed response(s) lost") {
		t.Errorf("String = %q, should say the total reads lower than the bill", s)
	}
}

func TestUnboundedBudgetReporting(t *testing.T) {
	b := NewBudget(0)
	b.Spend(Usage{InputTokens: 7})
	if b.Limit() != 0 || b.Remaining() != -1 || b.Exhausted() {
		t.Errorf("unbounded budget: limit %d remaining %d exhausted %v", b.Limit(), b.Remaining(), b.Exhausted())
	}
	if s := b.String(); !strings.Contains(s, "no ceiling") {
		t.Errorf("String = %q", s)
	}
	var nilB *Budget
	if nilB.Limit() != 0 || nilB.Calls() != 0 || nilB.LostCompletions() != 0 || nilB.String() != "no budget" {
		t.Error("a nil budget must be inert and say so")
	}
}
