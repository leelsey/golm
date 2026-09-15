// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
)

type sessionEchoProvider struct {
	mu    sync.Mutex
	calls int
	usage Usage
}

func (p *sessionEchoProvider) Name() string               { return "session-echo" }
func (p *sessionEchoProvider) Capabilities() Capabilities { return Capabilities{Tools: true} }

func (p *sessionEchoProvider) Complete(_ context.Context, req Request) (Response, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	var last string
	for _, m := range req.Messages {
		if m.Role == RoleUser {
			last = m.Text()
		}
	}
	return Response{Message: AssistantText("reply to " + last), StopReason: StopEndTurn, Usage: p.usage}, nil
}

func (p *sessionEchoProvider) Stream(ctx context.Context, req Request, fn func(StreamEvent) error) (Response, error) {
	r, err := p.Complete(ctx, req)
	if err != nil {
		return Response{}, err
	}
	_ = fn(StreamEvent{Type: EventDone})
	return r, nil
}

func (p *sessionEchoProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type replayStep struct {
	resp Response
	err  error
}

type replayProvider struct {
	mu    sync.Mutex
	calls int
	steps []replayStep
}

func (p *replayProvider) Name() string               { return "replay" }
func (p *replayProvider) Capabilities() Capabilities { return Capabilities{Tools: true} }

func (p *replayProvider) Complete(context.Context, Request) (Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.steps[min(p.calls, len(p.steps)-1)]
	p.calls++
	return s.resp, s.err
}

func (p *replayProvider) Stream(ctx context.Context, req Request, _ func(StreamEvent) error) (Response, error) {
	return p.Complete(ctx, req)
}

func (p *replayProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

var errProviderBoom = errors.New("provider boom")

// One Agent, many Sessions, all at once.
func TestOneAgentServesConcurrentSessions(t *testing.T) {
	const runs = 16
	perCall := Usage{InputTokens: 3, OutputTokens: 2}
	p := &sessionEchoProvider{usage: perCall}
	a := &Agent{Provider: p, Model: "m"}

	sessions := make([]*Session, runs)
	results := make([]Result, runs)
	errs := make([]error, runs)
	var wg sync.WaitGroup
	for i := range sessions {
		sessions[i] = NewSession()
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = a.Run(context.Background(), sessions[i], fmt.Sprintf("q%d", i))
		}(i)
	}
	wg.Wait()

	if got := p.count(); got != runs {
		t.Errorf("provider calls = %d, want %d", got, runs)
	}
	for i := range sessions {
		q, want := fmt.Sprintf("q%d", i), fmt.Sprintf("reply to q%d", i)
		if errs[i] != nil {
			t.Errorf("run %d: %v", i, errs[i])
			continue
		}
		res := results[i]
		if res.Text() != want {
			t.Errorf("run %d: text = %q, want %q — a Result crossed between runs", i, res.Text(), want)
		}
		if res.Steps != 1 || len(res.StepUsage) != 1 {
			t.Errorf("run %d: Steps = %d, StepUsage = %+v, want one of each", i, res.Steps, res.StepUsage)
		}
		if res.Usage != perCall {
			t.Errorf("run %d: Usage = %+v, want %+v", i, res.Usage, perCall)
		}
		h := sessions[i].History()
		if len(h) != 2 || h[0].Text() != q || h[1].Text() != want {
			t.Errorf("run %d: history = %+v, want only its own turn and answer", i, h)
		}
		if got := sessions[i].Usage(); got != perCall {
			t.Errorf("run %d: Session.Usage = %+v, want %+v", i, got, perCall)
		}
	}
}

// Every error exit still has to hand back the accounting.
func TestResultSurvivesEveryErrorExit(t *testing.T) {
	bill := Usage{InputTokens: 100}
	toolTurn := replayStep{resp: Response{
		Message: Message{Role: RoleAssistant, Content: []Content{
			Text{Text: "working"},
			ToolUse{ID: "c1", Name: "fail", Input: json.RawMessage(`{}`)},
		}},
		StopReason: StopToolUse,
		Usage:      bill,
	}}
	terminal := func(r StopReason) replayStep {
		return replayStep{resp: Response{Message: AssistantText("stopped"), StopReason: r, Usage: bill}}
	}

	reg := NewRegistry()
	reg.Register(NewTool("fail", "always fails", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) { return "", errors.New("nope") }))

	for _, tc := range []struct {
		name      string
		steps     []replayStep
		maxSteps  int
		maxTokens int
		wantErr   error
		wantCalls int
		wantUsage int
		wantTools int
		wantStop  StopReason
		wantText  string
	}{
		{
			name:      "token budget",
			steps:     []replayStep{toolTurn},
			maxSteps:  50,
			maxTokens: 150,
			wantErr:   ErrTokenBudget,
			wantCalls: 2, wantUsage: 200, wantTools: 2,
			wantStop: StopToolUse, wantText: "working",
		},
		{
			name:      "step error",
			steps:     []replayStep{toolTurn, {resp: Response{Usage: Usage{InputTokens: 7}}, err: errProviderBoom}},
			maxSteps:  8,
			wantErr:   errProviderBoom,
			wantCalls: 2, wantUsage: 107, wantTools: 1,
			wantStop: StopToolUse, wantText: "working",
		},
		{
			name:      "refusal",
			steps:     []replayStep{toolTurn, terminal(StopRefusal)},
			maxSteps:  8,
			wantErr:   ErrRefused,
			wantCalls: 2, wantUsage: 200, wantTools: 1,
			wantStop: StopRefusal, wantText: "stopped",
		},
		{
			name:      "context overflow",
			steps:     []replayStep{toolTurn, terminal(StopContextOverflow)},
			maxSteps:  8,
			wantErr:   ErrContextOverflow,
			wantCalls: 2, wantUsage: 200, wantTools: 1,
			wantStop: StopContextOverflow, wantText: "stopped",
		},
		{
			name:      "max steps",
			steps:     []replayStep{toolTurn},
			maxSteps:  3,
			wantErr:   ErrMaxSteps,
			wantCalls: 3, wantUsage: 300, wantTools: 3,
			wantStop: StopToolUse, wantText: "working",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &replayProvider{steps: tc.steps}
			sess := NewSession()
			a := &Agent{
				Provider: p, Model: "m", Tools: reg,
				MaxSteps: tc.maxSteps, MaxTotalTokens: tc.maxTokens,
			}

			res, err := a.Run(context.Background(), sess, "go")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got := p.count(); got != tc.wantCalls {
				t.Fatalf("provider calls = %d, want %d", got, tc.wantCalls)
			}
			if res.Steps != tc.wantCalls {
				t.Errorf("Steps = %d, want the %d calls actually made — a run stopped before a call must not report it", res.Steps, tc.wantCalls)
			}
			if len(res.StepUsage) != tc.wantCalls {
				t.Errorf("StepUsage = %+v, want one entry per completed step (%d)", res.StepUsage, tc.wantCalls)
			}
			var summed Usage
			for _, u := range res.StepUsage {
				summed.Add(u)
			}
			if summed != res.Usage {
				t.Errorf("StepUsage sums to %+v but Usage is %+v", summed, res.Usage)
			}
			if res.Usage.Total() != tc.wantUsage {
				t.Errorf("Usage = %d, want %d — the failed run was still billed", res.Usage.Total(), tc.wantUsage)
			}
			if res.ToolErrors != tc.wantTools {
				t.Errorf("ToolErrors = %d, want %d", res.ToolErrors, tc.wantTools)
			}
			if res.StopReason != tc.wantStop || res.Response.StopReason != tc.wantStop {
				t.Errorf("StopReason = %q / Response.StopReason = %q, want %q",
					res.StopReason, res.Response.StopReason, tc.wantStop)
			}
			if res.Text() != tc.wantText {
				t.Errorf("Text = %q, want %q", res.Text(), tc.wantText)
			}
			if got := sess.Usage(); got != res.Usage {
				t.Errorf("Session.Usage = %+v, want the failed run's %+v booked too", got, res.Usage)
			}
		})
	}
}

// The conversation's bill outlives its transcript.
func TestSessionUsageAccumulatesAndSurvivesCompaction(t *testing.T) {
	perCall := Usage{InputTokens: 10, OutputTokens: 5}
	a := &Agent{Provider: &sessionEchoProvider{usage: perCall}, Model: "m"}
	sess := NewSession()
	for i := 0; i < 3; i++ {
		if _, err := a.Run(context.Background(), sess, fmt.Sprintf("q%d", i)); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	want := Usage{InputTokens: 30, OutputTokens: 15}
	if got := sess.Usage(); got != want {
		t.Fatalf("Session.Usage = %+v, want %+v across three runs", got, want)
	}

	store := newMemStore()
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := store.Load(context.Background(), sess.ID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := reloaded.Usage(); got != want {
		t.Errorf("Session.Usage after a store round trip = %+v, want %+v", got, want)
	}

	sess.Trim(2)
	if got := sess.Usage(); got != want {
		t.Errorf("Trim reset the usage to %+v; compacting a conversation does not unbill it", got)
	}
	sess.DropBefore(1)
	if got := sess.Usage(); got != want {
		t.Errorf("DropBefore reset the usage to %+v; compacting a conversation does not unbill it", got)
	}

	if _, err := a.Run(context.Background(), sess, "again"); err != nil {
		t.Fatalf("run after compaction: %v", err)
	}
	want.Add(perCall)
	if got := sess.Usage(); got != want {
		t.Errorf("Session.Usage = %+v, want %+v — a compacted session still accumulates", got, want)
	}
}

func TestNilSessionIsRefused(t *testing.T) {
	a := &Agent{Provider: &sessionEchoProvider{}, Model: "m"}
	drain := func(StreamEvent) error { return nil }
	for _, tc := range []struct {
		name string
		run  func() (Result, error)
	}{
		{"Run", func() (Result, error) { return a.Run(context.Background(), nil, "hi") }},
		{"RunMessage", func() (Result, error) { return a.RunMessage(context.Background(), nil, UserText("hi")) }},
		{"Stream", func() (Result, error) { return a.Stream(context.Background(), nil, "hi", drain) }},
		{"StreamMessage", func() (Result, error) {
			return a.StreamMessage(context.Background(), nil, UserText("hi"), drain)
		}},
	} {
		if _, err := tc.run(); !errors.Is(err, ErrNoSession) {
			t.Errorf("%s with a nil Session: err = %v, want ErrNoSession", tc.name, err)
		}
	}
}

// A refusal must not cost the session.
func TestRefusedRunLeavesTheSessionUsable(t *testing.T) {
	sess := NewSession()

	if _, err := (&Agent{Model: "m"}).Run(context.Background(), sess, "hi"); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("no provider: err = %v, want ErrNoProvider", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	reg := NewRegistry()
	reg.Register(blockingTool(entered, release))
	blocked := &Agent{Model: "m", Tools: reg, Provider: &replayProvider{steps: []replayStep{
		{resp: Response{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "c1", Name: "block", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopToolUse}},
		{resp: Response{Message: AssistantText("final"), StopReason: StopEndTurn}},
	}}}

	done := make(chan error, 1)
	go func() {
		_, err := blocked.Run(context.Background(), sess, "first")
		done <- err
	}()
	<-entered
	if _, err := blocked.Run(context.Background(), sess, "second"); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("busy session: err = %v, want ErrSessionBusy", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("blocking run: %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	a := &Agent{Provider: &sessionEchoProvider{}, Model: "m"}
	if _, err := a.Run(cancelled, sess, "hi"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ctx: err = %v, want context.Canceled", err)
	}

	res, err := a.Run(context.Background(), sess, "after")
	if err != nil {
		t.Fatalf("a refused run left the session locked: %v", err)
	}
	if res.Text() != "reply to after" {
		t.Errorf("text = %q, want the session's own last turn answered", res.Text())
	}
}
