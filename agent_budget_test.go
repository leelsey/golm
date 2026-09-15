// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type budgetProvider struct {
	perCall int
	calls   int
}

func (p *budgetProvider) Name() string               { return "budget" }
func (p *budgetProvider) Capabilities() Capabilities { return Capabilities{Tools: true} }
func (p *budgetProvider) Complete(context.Context, Request) (Response, error) {
	p.calls++
	return Response{
		Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "c1", Name: "echo", Input: json.RawMessage(`{}`)},
		}},
		StopReason: StopToolUse,
		Usage:      Usage{InputTokens: p.perCall},
	}, nil
}
func (p *budgetProvider) Stream(ctx context.Context, r Request, _ func(StreamEvent) error) (Response, error) {
	return p.Complete(ctx, r)
}

func TestMaxTotalTokensStopsTheLoop(t *testing.T) {
	fp := &budgetProvider{perCall: 100}
	reg := NewRegistry()
	reg.Register(echoTool())
	a := &Agent{
		Provider: fp, Model: "x", Tools: reg,
		MaxSteps:       50,
		MaxTotalTokens: 250,
	}

	res, err := a.Run(context.Background(), NewSession(), "hi")
	if !errors.Is(err, ErrTokenBudget) {
		t.Fatalf("err = %v, want ErrTokenBudget", err)
	}

	if fp.calls != 3 {
		t.Errorf("provider calls = %d, want 3", fp.calls)
	}

	if res.Usage.Total() != 300 {
		t.Errorf("Usage = %d, want 300 (the overshoot must still be booked)", res.Usage.Total())
	}
	if !errors.Is(err, ErrTokenBudget) || res.Steps == 0 {
		t.Errorf("Steps = %d, want the step count preserved", res.Steps)
	}
}

func TestZeroBudgetIsUnbounded(t *testing.T) {
	fp := &budgetProvider{perCall: 1000}
	reg := NewRegistry()
	reg.Register(echoTool())
	a := &Agent{
		Provider: fp, Model: "x", Tools: reg,
		MaxSteps: 3,
	}

	if _, err := a.Run(context.Background(), NewSession(), "hi"); !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps — an unset budget must not bound anything", err)
	}
	if fp.calls != 3 {
		t.Errorf("provider calls = %d, want 3", fp.calls)
	}
}

// Session.Usage accumulates across runs, a Result's Usage does not.
func TestBudgetIsPerRunNotPerSession(t *testing.T) {
	fp := &budgetProvider{perCall: 100}
	reg := NewRegistry()
	reg.Register(echoTool())
	sess := NewSession()
	a := &Agent{
		Provider: fp, Model: "x", Tools: reg,
		MaxSteps: 2, MaxTotalTokens: 10_000,
	}
	var res Result
	for run := 1; run <= 2; run++ {
		var err error
		res, err = a.Run(context.Background(), sess, "hi")
		if !errors.Is(err, ErrMaxSteps) {
			t.Fatalf("run %d: err = %v, want ErrMaxSteps", run, err)
		}
	}
	if sess.Usage().Total() != 400 {
		t.Errorf("Session.Usage = %d, want 400 across both runs", sess.Usage().Total())
	}
	if res.Usage.Total() != 200 {
		t.Errorf("Result.Usage = %d, want 200 for the last run alone", res.Usage.Total())
	}
}
