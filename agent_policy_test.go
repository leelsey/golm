// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func denyAll(reason string) ToolPolicy {
	return func(ctx context.Context, req ToolRequest) (context.Context, error) {
		return ctx, fmt.Errorf("%w: %s", ErrToolDenied, reason)
	}
}

func assertBalanced(t *testing.T, s *Session) {
	t.Helper()
	uses, results := 0, 0
	for _, m := range s.History() {
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

// A denial is a gate, not a label.
func TestDeniedToolNeverRunsAndStaysBalanced(t *testing.T) {
	var entered atomic.Bool
	reg := NewRegistry()
	reg.Register(NewTool("t", "must not run", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) {
			entered.Store(true)
			return "ran", nil
		}))

	var seen ToolRequest
	sess := NewSession()
	a := &Agent{
		Provider: &fakeProvider{responses: toolCallThen("final")},
		Model:    "x", Name: "gated", Tools: reg,
		ToolPolicy: func(ctx context.Context, req ToolRequest) (context.Context, error) {
			seen = req
			return ctx, ErrToolDenied
		},
	}
	res, err := a.Run(context.Background(), sess, "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if entered.Load() {
		t.Fatal("the denied tool's function was entered")
	}
	if res.Text() != "final" {
		t.Errorf("the loop must continue past a denial, got %q", res.Text())
	}
	if seen.Agent != "gated" || seen.Step != 1 || seen.Tool == nil || seen.Call.ID != "c1" {
		t.Errorf("the policy was given a thin request: %+v", seen)
	}
	assertBalanced(t, sess)
}

// Whatever the policy says is what the model reads.
func TestDenialReasonAndCountsReachTheResult(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewTool("t", "gated", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) { return "ran", nil }))

	var got []ToolResult
	a := &Agent{
		Provider: &fakeProvider{responses: toolCallThen("final")},
		Model:    "x", Tools: reg,
		ToolPolicy:   denyAll("no approval on file"),
		OnToolResult: func(r ToolResult) { got = append(got, r) },
	}
	res, err := a.Run(context.Background(), NewSession(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want one result, got %d", len(got))
	}
	if !got[0].IsError {
		t.Error("a denial must reach the model as a failed call")
	}
	if got[0].ToolUseID != "c1" || got[0].Name != "t" {
		t.Errorf("denial not attributed to the call: %+v", got[0])
	}
	if !strings.Contains(got[0].Text(), "no approval on file") {
		t.Errorf("the policy's reason did not reach the model, got %q", got[0].Text())
	}
	if res.ToolDenials != 1 {
		t.Errorf("ToolDenials = %d, want 1", res.ToolDenials)
	}
	if res.ToolErrors != 1 {
		t.Errorf("ToolErrors = %d, want 1", res.ToolErrors)
	}
}

// THE decision-point test. A policy may block on a human.
func TestSlowPolicyDoesNotSpendTheToolsTimeout(t *testing.T) {
	const toolTimeout = 250 * time.Millisecond
	const policyDelay = 400 * time.Millisecond
	var headroom atomic.Int64

	reg := NewRegistry()
	reg.Register(NewTool("t", "needs its full timeout", json.RawMessage(`{"type":"object"}`),
		func(ctx context.Context, _ json.RawMessage) (string, error) {
			dl, ok := ctx.Deadline()
			if !ok {
				return "", errors.New("tool ran without a deadline")
			}
			headroom.Store(int64(time.Until(dl)))
			select {
			case <-time.After(50 * time.Millisecond):
				return "ran", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}))

	var got []ToolResult
	a := &Agent{
		Provider: &fakeProvider{responses: toolCallThen("final")},
		Model:    "x", Tools: reg,
		ToolTimeout: toolTimeout,
		ToolPolicy: func(ctx context.Context, _ ToolRequest) (context.Context, error) {
			time.Sleep(policyDelay)
			return ctx, nil
		},
		OnToolResult: func(r ToolResult) { got = append(got, r) },
	}
	if _, err := a.Run(context.Background(), NewSession(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want one result, got %d", len(got))
	}
	if got[0].IsError {
		t.Fatalf("the approved tool failed; the decision was put on its clock: %q", got[0].Text())
	}
	if got[0].Text() != "ran" {
		t.Errorf("result = %q, want %q", got[0].Text(), "ran")
	}
	if left := time.Duration(headroom.Load()); left < toolTimeout*4/5 {
		t.Errorf("tool was handed %v of a %v timeout; the policy spent the rest", left, toolTimeout)
	}
}

func TestPanickingPolicyDenies(t *testing.T) {
	var entered atomic.Bool
	reg := NewRegistry()
	reg.Register(NewTool("t", "must not run", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) {
			entered.Store(true)
			return "ran", nil
		}))

	sess := NewSession()
	var got []ToolResult
	a := &Agent{
		Provider: &fakeProvider{responses: toolCallThen("final")},
		Model:    "x", Tools: reg,
		ToolPolicy: func(context.Context, ToolRequest) (context.Context, error) {
			panic("policy bug")
		},
		OnToolResult: func(r ToolResult) { got = append(got, r) },
	}
	res, err := a.Run(context.Background(), sess, "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if entered.Load() {
		t.Fatal("a gate that fails open is not a gate: the tool ran after the policy panicked")
	}
	if len(got) != 1 || !got[0].IsError {
		t.Fatalf("want one error result, got %+v", got)
	}
	if res.ToolDenials != 1 {
		t.Errorf("ToolDenials = %d, want 1", res.ToolDenials)
	}
	assertBalanced(t, sess)
}

type policyCtxKey struct{}

// The context a policy returns is where a sandbox handle or a capability token travels.
func TestPolicyContextReachesTheTool(t *testing.T) {
	var seen atomic.Value
	reg := NewRegistry()
	reg.Register(NewTool("t", "reads its context", json.RawMessage(`{"type":"object"}`),
		func(ctx context.Context, _ json.RawMessage) (string, error) {
			if v, ok := ctx.Value(policyCtxKey{}).(string); ok {
				seen.Store(v)
			}
			return "ran", nil
		}))

	a := &Agent{
		Provider: &fakeProvider{responses: toolCallThen("final")},
		Model:    "x", Tools: reg,
		ToolPolicy: func(ctx context.Context, _ ToolRequest) (context.Context, error) {
			return context.WithValue(ctx, policyCtxKey{}, "sandbox-1"), nil
		},
	}
	if _, err := a.Run(context.Background(), NewSession(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, _ := seen.Load().(string); got != "sandbox-1" {
		t.Errorf("tool context value = %q, want %q", got, "sandbox-1")
	}
}

// A denied call is not dispatched.
func TestDeniedCallTakesNoParallelSlot(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewTool("t", "runs when allowed", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) { return "ran", nil }))

	sess := NewSession()
	a := &Agent{
		Provider: &fakeProvider{responses: []Response{
			{Message: Message{Role: RoleAssistant, Content: []Content{
				ToolUse{ID: "a", Name: "t", Input: json.RawMessage(`{}`)},
				ToolUse{ID: "b", Name: "t", Input: json.RawMessage(`{}`)},
			}}, StopReason: StopToolUse},
			{Message: AssistantText("final"), StopReason: StopEndTurn},
		}},
		Model: "x", Tools: reg,
		ParallelTools: true, MaxParallelTools: 1,
		ToolTimeout: 200 * time.Millisecond,
		ToolPolicy: func(ctx context.Context, req ToolRequest) (context.Context, error) {
			if req.Call.ID == "a" {
				return ctx, ErrToolDenied
			}
			return ctx, nil
		},
	}
	if _, err := a.Run(context.Background(), sess, "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	toolMsg := sess.History()[2]
	if len(toolMsg.Content) != 2 {
		t.Fatalf("results = %d, want 2", len(toolMsg.Content))
	}
	allowed := toolMsg.Content[1].(ToolResult)
	if allowed.ToolUseID != "b" {
		t.Fatalf("result order not preserved: %+v", toolMsg.Content)
	}
	if allowed.IsError || allowed.Text() != "ran" {
		t.Errorf("the allowed call did not run; the denied one held the only slot: %+v", allowed)
	}
}

// The loop answers whatever it never got a result for.
func TestDenialKeepsItsReasonBesideATimeout(t *testing.T) {
	unblock := make(chan struct{})
	defer close(unblock)

	reg := NewRegistry()
	reg.Register(NewTool("t", "hangs", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) { <-unblock; return "late", nil }))

	sess := NewSession()
	a := &Agent{
		Provider: &fakeProvider{responses: []Response{
			{Message: Message{Role: RoleAssistant, Content: []Content{
				ToolUse{ID: "a", Name: "t", Input: json.RawMessage(`{}`)},
				ToolUse{ID: "b", Name: "t", Input: json.RawMessage(`{}`)},
			}}, StopReason: StopToolUse},
			{Message: AssistantText("final"), StopReason: StopEndTurn},
		}},
		Model: "x", Tools: reg,
		ToolTimeout: 20 * time.Millisecond,
		ToolPolicy: func(ctx context.Context, req ToolRequest) (context.Context, error) {
			if req.Call.ID == "a" {
				return ctx, fmt.Errorf("%w: sandbox unavailable", ErrToolDenied)
			}
			return ctx, nil
		},
	}
	res, err := a.Run(context.Background(), sess, "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	denied := sess.History()[2].Content[0].(ToolResult)
	if denied.ToolUseID != "a" {
		t.Fatalf("result order not preserved: %+v", sess.History()[2].Content)
	}
	if !strings.Contains(denied.Text(), "sandbox unavailable") {
		t.Errorf("the denial was relabelled: %q", denied.Text())
	}
	if strings.Contains(denied.Text(), "deadline") {
		t.Errorf("a denied call was reported as a timeout: %q", denied.Text())
	}
	if res.ToolDenials != 1 {
		t.Errorf("ToolDenials = %d, want 1", res.ToolDenials)
	}
	assertBalanced(t, sess)
}

func countingPolicy(n *atomic.Int32) ToolPolicy {
	return func(ctx context.Context, _ ToolRequest) (context.Context, error) {
		n.Add(1)
		return ctx, nil
	}
}

// Forced tool choice is structured output.
func TestPolicyNotConsultedForForcedToolChoice(t *testing.T) {
	var asked atomic.Int32
	var entered atomic.Bool
	reg := NewRegistry()
	reg.Register(NewTool("t", "structured output", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) {
			entered.Store(true)
			return "ran", nil
		}))

	a := &Agent{
		Provider: &fakeProvider{responses: []Response{
			{Message: Message{Role: RoleAssistant, Content: []Content{
				ToolUse{ID: "c1", Name: "t", Input: json.RawMessage(`{"answer":1}`)},
			}}, StopReason: StopToolUse},
		}},
		Model: "x", Tools: reg,
		ToolChoice: "t",
		ToolPolicy: countingPolicy(&asked),
	}
	if _, err := a.Run(context.Background(), NewSession(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if entered.Load() {
		t.Fatal("the structured-output tool was executed")
	}
	if n := asked.Load(); n != 0 {
		t.Errorf("policy consulted %d times for a call that never runs", n)
	}
}

// A truncated tool call is answered with an error and never dispatched.
func TestPolicyNotConsultedForTruncatedToolCalls(t *testing.T) {
	var asked atomic.Int32
	var entered atomic.Bool
	reg := NewRegistry()
	reg.Register(NewTool("t", "never reached", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) {
			entered.Store(true)
			return "ran", nil
		}))

	sess := NewSession()
	a := &Agent{
		Provider: &fakeProvider{responses: []Response{
			{Message: Message{Role: RoleAssistant, Content: []Content{
				ToolUse{ID: "c1", Name: "t", Input: json.RawMessage(`{"half":`)},
			}}, StopReason: StopMaxTokens},
			{Message: AssistantText("final"), StopReason: StopEndTurn},
		}},
		Model: "x", Tools: reg,
		ToolPolicy: countingPolicy(&asked),
	}
	res, err := a.Run(context.Background(), sess, "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text() != "final" {
		t.Errorf("final = %q", res.Text())
	}
	if entered.Load() {
		t.Fatal("a truncated tool call was executed")
	}
	if n := asked.Load(); n != 0 {
		t.Errorf("policy consulted %d times for a call that never runs", n)
	}
	if res.ToolDenials != 0 {
		t.Errorf("ToolDenials = %d; a truncated call is not a denial", res.ToolDenials)
	}
	assertBalanced(t, sess)
}
