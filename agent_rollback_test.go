// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

type failFirstProvider struct {
	calls    int
	lastSeen []Message
}

func (p *failFirstProvider) Name() string               { return "failfirst" }
func (p *failFirstProvider) Capabilities() Capabilities { return Capabilities{Tools: true} }
func (p *failFirstProvider) Complete(_ context.Context, req Request) (Response, error) {
	p.calls++
	p.lastSeen = req.Messages
	if p.calls == 1 {
		return Response{}, errors.New("429 rate limited")
	}
	return Response{Message: AssistantText("four"), StopReason: StopEndTurn}, nil
}
func (p *failFirstProvider) Stream(ctx context.Context, req Request, _ func(StreamEvent) error) (Response, error) {
	return p.Complete(ctx, req)
}

func TestFailedRunLeavesNoUnansweredTurn(t *testing.T) {
	fp := &failFirstProvider{}
	a := NewAgent(fp, "x")
	s := NewSession()

	if _, err := a.Run(context.Background(), s, "what is 2+2?"); err == nil {
		t.Fatal("expected the provider error")
	}
	if s.Len() != 0 {
		t.Fatalf("history after a failed first call = %d, want 0: %v", s.Len(), s.History())
	}

	if _, err := a.Run(context.Background(), s, "what is 2+2?"); err != nil {
		t.Fatalf("retry: %v", err)
	}

	if n := len(fp.lastSeen); n != 1 {
		t.Fatalf("retry sent %d messages, want 1: %v", n, fp.lastSeen)
	}
	users := 0
	for _, m := range s.History() {
		if m.Role == RoleUser {
			users++
		}
	}
	if users != 1 {
		t.Errorf("user turns in history = %d, want 1: %v", users, s.History())
	}
}

func TestFailedRunKeepsCompletedSteps(t *testing.T) {
	first := Response{
		Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "t1", Name: "echo", Input: json.RawMessage("{}")},
		}},
		StopReason: StopToolUse,
	}
	reg := NewRegistry()
	reg.Register(echoTool())
	a := &Agent{Provider: &erringProvider{first: first}, Model: "x", Tools: reg}
	s := NewSession()

	if _, err := a.Run(context.Background(), s, "hi"); err == nil {
		t.Fatal("expected the provider error")
	}
	if s.Len() != 3 {
		t.Fatalf("history = %d, want 3 (user, assistant, tool): %v", s.Len(), s.History())
	}
	if s.History()[0].Role != RoleUser {
		t.Errorf("first message = %s, want the opening turn kept", s.History()[0].Role)
	}
}

func TestRollbackDoesNotOverruleAConcurrentAppend(t *testing.T) {
	fp := &failFirstProvider{}
	a := NewAgent(fp, "x")
	s := NewSession()
	s.Append(AssistantText("earlier"))

	appender := &appendDuringCall{s: s}
	a.Provider = appender
	if _, err := a.Run(context.Background(), s, "question"); err == nil {
		t.Fatal("expected the provider error")
	}
	if s.Len() != 3 {
		t.Fatalf("history = %d, want 3 (earlier, question, note): %v", s.Len(), s.History())
	}
}

type appendDuringCall struct{ s *Session }

func (p *appendDuringCall) Name() string               { return "appender" }
func (p *appendDuringCall) Capabilities() Capabilities { return Capabilities{} }
func (p *appendDuringCall) Complete(context.Context, Request) (Response, error) {
	p.s.Append(AssistantText("note"))
	return Response{}, fmt.Errorf("boom")
}
func (p *appendDuringCall) Stream(ctx context.Context, req Request, _ func(StreamEvent) error) (Response, error) {
	return p.Complete(ctx, req)
}
