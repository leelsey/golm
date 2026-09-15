// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stressProvider struct{ calls atomic.Int64 }

func (p *stressProvider) Name() string               { return "stress" }
func (p *stressProvider) Capabilities() Capabilities { return Capabilities{Tools: true} }
func (p *stressProvider) Complete(ctx context.Context, _ Request) (Response, error) {
	p.calls.Add(1)
	select {
	case <-time.After(time.Duration(rand.Intn(300)) * time.Microsecond):
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
	return Response{Message: AssistantText("ok"), StopReason: StopEndTurn,
		Usage: Usage{InputTokens: 1, OutputTokens: 1}}, nil
}
func (p *stressProvider) Stream(ctx context.Context, r Request, _ func(StreamEvent) error) (Response, error) {
	return p.Complete(ctx, r)
}

// "Waiting on a person is never a sync.Mutex".
func TestStressApprovalQueueIsAbandonable(t *testing.T) {
	const runs = 64

	held := make(chan struct{})
	rules := PolicyRules{
		Default: DecisionAsk,
		Approve: func(ctx context.Context, req ToolRequest) (bool, error) {
			select {
			case <-held:
				return true, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		},
	}
	reg := NewRegistry()
	reg.Register(TextTool("gated", "does nothing", "input", "anything",
		func(context.Context, string) (string, error) { return "done", nil }))

	agent := &Agent{Provider: &toolThenStop{}, Model: "m", Tools: reg,
		ToolPolicy: rules.Policy(), MaxSteps: 3}

	var wg sync.WaitGroup
	done := make(chan struct{})
	for i := 0; i < runs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(),
				time.Duration(5+rand.Intn(40))*time.Millisecond)
			defer cancel()
			_, _ = agent.Run(ctx, NewSession(), "go")
		}(i)
	}
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("runs queued for an approver did not leave the queue when their contexts ended")
	}
	close(held)
}

type toolThenStop struct{}

func (toolThenStop) Name() string               { return "toolthenstop" }
func (toolThenStop) Capabilities() Capabilities { return Capabilities{Tools: true} }
func (t toolThenStop) Complete(ctx context.Context, req Request) (Response, error) {
	for _, m := range req.Messages {
		if m.Role == RoleTool {
			return Response{Message: AssistantText("done"), StopReason: StopEndTurn}, nil
		}
	}
	return Response{
		Message:    Message{Role: RoleAssistant, Content: []Content{ToolUse{ID: "1", Name: "gated", Input: []byte(`{"input":"x"}`)}}},
		StopReason: StopToolUse,
	}, nil
}
func (t toolThenStop) Stream(ctx context.Context, r Request, _ func(StreamEvent) error) (Response, error) {
	return t.Complete(ctx, r)
}

// One Agent serves any number of conversations, one Session each.
func TestStressOneAgentManySessionsAndTheBusyGuard(t *testing.T) {
	p := &stressProvider{}
	agent := &Agent{Provider: p, Model: "m", MaxSteps: 2}

	const sessions, perSession = 24, 8
	var wg sync.WaitGroup
	var busy, ok atomic.Int64
	for i := 0; i < sessions; i++ {
		s := NewSession()
		for j := 0; j < perSession; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := agent.Run(context.Background(), s, "hi")
				switch {
				case err == nil:
					ok.Add(1)
				case errors.Is(err, ErrSessionBusy):
					busy.Add(1)
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}()
		}
	}
	wg.Wait()

	if ok.Load() == 0 {
		t.Fatal("no run completed")
	}
	if busy.Load() == 0 {
		t.Log("note: no ErrSessionBusy observed; the guard was not contended this time")
	}

	if ok.Load()+busy.Load() != sessions*perSession {
		t.Errorf("accounted for %d of %d runs", ok.Load()+busy.Load(), sessions*perSession)
	}
}

// A Budget is shared by every agent holding it.
func TestStressSharedBudgetStopsEveryone(t *testing.T) {
	const runs, limit = 48, 40
	b := NewBudget(limit)
	p := &stressProvider{}
	agent := &Agent{Provider: p, Model: "m", Budget: b, MaxSteps: 4}

	var wg sync.WaitGroup
	for i := 0; i < runs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = agent.Run(context.Background(), NewSession(), "hi")
		}()
	}
	wg.Wait()

	if !b.Exhausted() {
		t.Fatalf("budget not exhausted after %d calls: %s", p.calls.Load(), b)
	}

	if spent := b.Spent(); spent > limit+2*runs {
		t.Errorf("spent %d against a %d ceiling: the overshoot grows with concurrency", spent, limit)
	}
}

// "A caller's callback never fails the loop that calls it." Every seam.
func TestStressPanickingCallbacksNeverEscape(t *testing.T) {
	boom := func() { panic("deployment code") }

	t.Run("router", func(t *testing.T) {
		o := NewOrchestrator(nil)
		o.Add("main", "main", &Agent{Provider: &stressProvider{}, Model: "m"})
		o.Router = func(context.Context, RouteInput) (Route, error) { boom(); return Route{}, nil }
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := o.Run(context.Background(), NewSession(), "hi"); err == nil {
					t.Error("a panicking router produced a successful run")
				}
			}()
		}
		wg.Wait()
	})

	t.Run("audit", func(t *testing.T) {
		rules := PolicyRules{Default: DecisionAllow, Audit: func(ToolRequest, Decision, error) { boom() }}
		reg := NewRegistry()
		reg.Register(TextTool("gated", "t", "input", "x",
			func(context.Context, string) (string, error) { return "done", nil }))
		agent := &Agent{Provider: toolThenStop{}, Model: "m", Tools: reg,
			ToolPolicy: rules.Policy(), MaxSteps: 3}
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := agent.Run(context.Background(), NewSession(), "go"); err != nil {
					t.Errorf("a panicking audit failed the run: %v", err)
				}
			}()
		}
		wg.Wait()
	})

	t.Run("approver denies", func(t *testing.T) {
		var ran atomic.Int64
		rules := PolicyRules{Default: DecisionAsk,
			Approve: func(context.Context, ToolRequest) (bool, error) { boom(); return true, nil }}
		reg := NewRegistry()
		reg.Register(TextTool("gated", "t", "input", "x",
			func(context.Context, string) (string, error) { ran.Add(1); return "RAN", nil }))
		agent := &Agent{Provider: toolThenStop{}, Model: "m", Tools: reg,
			ToolPolicy: rules.Policy(), MaxSteps: 3}
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = agent.Run(context.Background(), NewSession(), "go")
			}()
		}
		wg.Wait()
		if n := ran.Load(); n != 0 {
			t.Errorf("a panicking approver let the tool run %d time(s): the gate failed open", n)
		}
	})
}

// Calls to the SAME sub-agent in one conversation are serialised.
func TestStressDelegationSerialisesPerAgent(t *testing.T) {
	o := NewOrchestrator(nil)
	o.Add("main", "main", &Agent{Provider: &stressProvider{}, Model: "m"})
	for _, n := range []string{"a", "b", "c"} {
		o.Add(n, "sub", &Agent{Provider: &stressProvider{}, Model: "m"})
	}

	conv := NewSession()
	ctx := withConversation(context.Background(), conv.ID())

	var wg sync.WaitGroup
	done := make(chan struct{})
	for i := 0; i < 48; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := []string{"a", "b", "c"}[i%3]
			cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if _, err := o.delegate(cctx, name, fmt.Sprintf("task %d", i)); err != nil &&
				!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				t.Errorf("delegate(%s): %v", name, err)
			}
		}(i)
	}
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent delegations deadlocked")
	}
}
