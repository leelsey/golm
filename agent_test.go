// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeProvider struct {
	responses []Response
	calls     int
	lastReq   Request
}

func (f *fakeProvider) Name() string               { return "fake" }
func (f *fakeProvider) Capabilities() Capabilities { return Capabilities{Tools: true} }

func (f *fakeProvider) Complete(_ context.Context, req Request) (Response, error) {
	f.lastReq = req
	r := f.responses[f.calls]
	f.calls++
	return r, nil
}

func (f *fakeProvider) Stream(ctx context.Context, req Request, fn func(StreamEvent) error) (Response, error) {
	r, err := f.Complete(ctx, req)
	if err != nil {
		return Response{}, err
	}
	_ = fn(StreamEvent{Type: EventDone})
	return r, nil
}

func echoTool() Tool {
	return NewTool("echo", "echoes its input", json.RawMessage(`{"type":"object"}`),
		func(_ context.Context, input json.RawMessage) (string, error) {
			return "echoed:" + string(input), nil
		})
}

func firstText(c []ToolContent) string {
	if len(c) == 0 {
		return ""
	}
	t, _ := c[0].(Text)
	return t.Text
}

func TestAgentReActLoop(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{
			Message: Message{Role: RoleAssistant, Content: []Content{
				ToolUse{ID: "c1", Name: "echo", Input: json.RawMessage(`{"x":1}`)},
			}},
			StopReason: StopToolUse,
		},
		{
			Message:    AssistantText("done"),
			StopReason: StopEndTurn,
		},
	}}

	reg := NewRegistry()
	reg.Register(echoTool())
	sess := NewSession()
	agent := &Agent{Provider: fp, Model: "x", Tools: reg}

	res, err := agent.Run(context.Background(), sess, "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text() != "done" {
		t.Fatalf("final text = %q, want %q", res.Text(), "done")
	}
	if fp.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", fp.calls)
	}
	hist := sess.History()
	if len(hist) != 4 {
		t.Fatalf("history len = %d, want 4", len(hist))
	}
	if hist[2].Role != RoleTool {
		t.Errorf("history[2].Role = %q, want %q", hist[2].Role, RoleTool)
	}
	tr, ok := hist[2].Content[0].(ToolResult)
	if !ok || tr.ToolUseID != "c1" || tr.Text() != `echoed:{"x":1}` {
		t.Errorf("tool result wrong: %+v", hist[2].Content[0])
	}
}

func TestAgentUnknownTool(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{
			Message: Message{Role: RoleAssistant, Content: []Content{
				ToolUse{ID: "c1", Name: "missing", Input: json.RawMessage(`{}`)},
			}},
			StopReason: StopToolUse,
		},
		{Message: AssistantText("ok"), StopReason: StopEndTurn},
	}}
	sess := NewSession()
	agent := &Agent{Provider: fp, Model: "x", Tools: NewRegistry()}
	if _, err := agent.Run(context.Background(), sess, "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	hist := sess.History()
	tr := hist[2].Content[0].(ToolResult)
	if !tr.IsError {
		t.Errorf("expected error tool result for unknown tool")
	}
}

func TestAgentTotalUsage(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{
			Message: Message{Role: RoleAssistant, Content: []Content{
				ToolUse{ID: "c1", Name: "echo", Input: json.RawMessage(`{}`)},
			}},
			StopReason: StopToolUse,
			Usage:      Usage{InputTokens: 10, OutputTokens: 5},
		},
		{
			Message:    AssistantText("done"),
			StopReason: StopEndTurn,
			Usage:      Usage{InputTokens: 20, OutputTokens: 7},
		},
	}}
	reg := NewRegistry()
	reg.Register(echoTool())
	agent := &Agent{Provider: fp, Model: "x", Tools: reg}

	res, err := agent.Run(context.Background(), NewSession(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Usage.InputTokens != 30 {
		t.Errorf("Usage.InputTokens = %d, want 30", res.Usage.InputTokens)
	}
	if res.Usage.OutputTokens != 12 {
		t.Errorf("Usage.OutputTokens = %d, want 12", res.Usage.OutputTokens)
	}
}

func TestAgentMaxSteps(t *testing.T) {
	loop := Response{
		Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "c", Name: "echo", Input: json.RawMessage(`{}`)},
		}},
		StopReason: StopToolUse,
	}
	resps := make([]Response, 10)
	for i := range resps {
		resps[i] = loop
	}
	reg := NewRegistry()
	reg.Register(echoTool())
	sess := NewSession()
	agent := &Agent{Provider: &fakeProvider{responses: resps}, Model: "x", Tools: reg, MaxSteps: 3}
	if _, err := agent.Run(context.Background(), sess, "hi"); err == nil {
		t.Fatalf("expected max-steps error")
	}
	h := sess.History()
	if n := len(h); n == 0 || h[n-1].Role != RoleTool {
		t.Fatalf("session should end with a tool result, not a dangling tool_use")
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

func TestAgentToolPanicRecovered(t *testing.T) {
	panicTool := NewTool("boom", "", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) { panic("kaboom") })
	for _, parallel := range []bool{false, true} {
		fp := &fakeProvider{responses: []Response{
			{Message: Message{Role: RoleAssistant, Content: []Content{
				ToolUse{ID: "c1", Name: "boom", Input: json.RawMessage(`{}`)},
				ToolUse{ID: "c2", Name: "boom", Input: json.RawMessage(`{}`)},
			}}, StopReason: StopToolUse},
			{Message: AssistantText("recovered"), StopReason: StopEndTurn},
		}}
		reg := NewRegistry()
		reg.Register(panicTool)
		a := &Agent{Provider: fp, Model: "x", Tools: reg, ParallelTools: parallel}
		res, err := a.Run(context.Background(), NewSession(), "hi")
		if err != nil {
			t.Fatalf("parallel=%v: Run errored instead of recovering: %v", parallel, err)
		}
		if res.Text() != "recovered" {
			t.Errorf("parallel=%v: res=%q, want recovered", parallel, res.Text())
		}
	}
}
