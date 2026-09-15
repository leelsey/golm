// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestAgentTruncatedToolCallRecovers(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			Text{Text: "calling"},
			ToolUse{ID: "c1", Name: "echo", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopMaxTokens},
		{Message: AssistantText("final"), StopReason: StopEndTurn},
	}}
	reg := NewRegistry()
	reg.Register(echoTool())
	sess := NewSession()
	a := &Agent{Provider: fp, Model: "x", Tools: reg}

	res, err := a.Run(context.Background(), sess, "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text() != "final" {
		t.Errorf("res = %q, want final", res.Text())
	}
	h := sess.History()
	if h[len(h)-1].Role != RoleAssistant {
		t.Errorf("session should end on the final assistant answer")
	}
	uses, results := 0, 0
	for _, m := range h {
		uses += len(m.ToolUses())
		for _, c := range m.Content {
			if _, ok := c.(ToolResult); ok {
				results++
			}
		}
	}
	if uses != results {
		t.Errorf("unbalanced tool_use/tool_result: %d uses, %d results", uses, results)
	}
}

func TestAgentToolUseWithOtherStopNotExecuted(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "c1", Name: "echo", Input: json.RawMessage(`{"x":`)},
		}}, StopReason: StopOther},
	}}
	reg := NewRegistry()
	reg.Register(echoTool())
	sess := NewSession()
	a := &Agent{Provider: fp, Model: "x", Tools: reg}

	res, err := a.Run(context.Background(), sess, "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fp.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (StopOther terminates, no retry)", fp.calls)
	}
	if res.StopReason != StopOther {
		t.Fatalf("StopReason = %q, want other", res.StopReason)
	}
	seen := false
	for _, m := range sess.History() {
		for _, c := range m.Content {
			if r, ok := c.(ToolResult); ok {
				seen = true
				if !r.IsError {
					t.Fatalf("tool result = %q; tool ran on a StopOther response", r.Content)
				}
			}
		}
	}
	if !seen {
		t.Fatal("no tool result answered the dangling tool_use")
	}
}

// TestForcedToolChoiceReturnsAfterOneTurn.
func TestForcedToolChoiceReturnsAfterOneTurn(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "c1", Name: "echo", Input: json.RawMessage(`{"v":1}`)},
		}}, StopReason: StopToolUse},
	}}
	reg := NewRegistry()
	reg.Register(echoTool())
	a := &Agent{Provider: fp, Model: "x", Tools: reg, ToolChoice: "echo"}

	res, err := a.Run(context.Background(), NewSession(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fp.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (forced tool must not loop)", fp.calls)
	}
	uses := res.Message.ToolUses()
	if len(uses) != 1 || uses[0].Name != "echo" {
		t.Fatalf("the result must carry the forced tool_use, got %+v", res.Message)
	}
	if res.StopReason != StopToolUse {
		t.Fatalf("StopReason = %q, want tool_use", res.StopReason)
	}
}

// TestForcedToolChoiceBalancesSession.
func TestForcedToolChoiceBalancesSession(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "c1", Name: "echo", Input: json.RawMessage(`{"v":1}`)},
		}}, StopReason: StopToolUse},
	}}
	reg := NewRegistry()
	reg.Register(echoTool())
	sess := NewSession()
	a := &Agent{Provider: fp, Model: "x", Tools: reg, ToolChoice: "echo"}

	if _, err := a.Run(context.Background(), sess, "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	uses, results := 0, 0
	for _, m := range sess.History() {
		uses += len(m.ToolUses())
		for _, c := range m.Content {
			if _, ok := c.(ToolResult); ok {
				results++
			}
		}
	}
	if uses != results {
		t.Fatalf("forced-tool session unbalanced: %d uses, %d results", uses, results)
	}
}

// TestContextOverflowTerminatesWithoutSpinning.
func TestContextOverflowTerminatesWithoutSpinning(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "c1", Name: "echo", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopContextOverflow},
	}}
	reg := NewRegistry()
	reg.Register(echoTool())
	sess := NewSession()
	a := &Agent{Provider: fp, Model: "x", Tools: reg}

	res, err := a.Run(context.Background(), sess, "hi")
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("Run err = %v, want ErrContextOverflow", err)
	}
	if fp.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (overflow must not spin)", fp.calls)
	}
	if res.StopReason != StopContextOverflow {
		t.Fatalf("StopReason = %q, want context_overflow", res.StopReason)
	}
	uses, results := 0, 0
	for _, m := range sess.History() {
		uses += len(m.ToolUses())
		for _, c := range m.Content {
			if _, ok := c.(ToolResult); ok {
				results++
			}
		}
	}
	if uses != results {
		t.Fatalf("unbalanced tool_use/tool_result: %d uses, %d results", uses, results)
	}
}

// TestPauseAutoResumes: a resumable pause re-sends and finishes on the next turn.
func TestPauseAutoResumes(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{Message: AssistantText("thinking"), StopReason: StopPause},
		{Message: AssistantText("final"), StopReason: StopEndTurn},
	}}
	a := &Agent{Provider: fp, Model: "x"}

	res, err := a.Run(context.Background(), NewSession(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fp.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (pause must resume)", fp.calls)
	}
	if res.Text() != "final" {
		t.Fatalf("res = %q, want final", res.Text())
	}
}

func TestAgentToolUseWithEndTurnStopExecutes(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "c1", Name: "echo", Input: json.RawMessage(`{"x":1}`)},
		}}, StopReason: StopEndTurn},
		{Message: AssistantText("final"), StopReason: StopEndTurn},
	}}
	reg := NewRegistry()
	reg.Register(echoTool())
	sess := NewSession()
	a := &Agent{Provider: fp, Model: "x", Tools: reg}

	res, err := a.Run(context.Background(), sess, "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text() != "final" {
		t.Fatalf("res = %q, want final", res.Text())
	}
	if fp.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (tool must have run)", fp.calls)
	}
	for _, m := range sess.History() {
		for _, c := range m.Content {
			if r, ok := c.(ToolResult); ok {
				if r.IsError {
					t.Fatalf("tool result is an error %q; tools were dropped instead of executed", r.Text())
				}
				if r.Text() != `echoed:{"x":1}` {
					t.Fatalf("tool result = %q, want echoed output", r.Text())
				}
			}
		}
	}
}
