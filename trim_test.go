// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"testing"
)

func TestTrimIsUserFirst(t *testing.T) {
	s := NewSession()
	s.Append(
		UserText("q"),
		Message{Role: RoleAssistant, Content: []Content{ToolUse{ID: "c1", Name: "echo", Input: json.RawMessage(`{}`)}}},
		Message{Role: RoleTool, Content: []Content{ToolResult{ToolUseID: "c1", Content: ToolText("out")}}},
		AssistantText("answer"),
	)
	for _, keep := range []int{1, 2, 3, 4} {
		c := NewSession()
		c.Append(s.History()...)
		c.Trim(keep)
		h := c.History()
		if len(h) == 0 {
			t.Fatalf("Trim(%d) wiped the session; the cut must widen to the enclosing user turn", keep)
		}
		if h[0].Role != RoleUser {
			t.Fatalf("Trim(%d): history starts with role %q, want user-first", keep, h[0].Role)
		}
	}
	{
		c := NewSession()
		c.Append(s.History()...)
		c.Trim(3)
		if got := len(c.History()); got != 4 {
			t.Fatalf("Trim(3) kept %d messages, want 4 (whole enclosing turn)", got)
		}
	}
	s.Trim(4)
	if h := s.History(); len(h) != 4 || h[0].Role != RoleUser {
		t.Fatalf("Trim(4) = %d messages starting %v, want all 4 kept", len(h), h[0].Role)
	}
}

func TestAgentKeepLastTrims(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{Message: AssistantText("one"), StopReason: StopEndTurn},
		{Message: AssistantText("two"), StopReason: StopEndTurn},
	}}
	sess := NewSession()
	a := &Agent{Provider: fp, Model: "x", KeepLast: 2}
	if _, err := a.Run(context.Background(), sess, "first"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := a.Run(context.Background(), sess, "second"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := len(sess.History()); n > 2 {
		t.Fatalf("history length = %d, want <= 2 (KeepLast)", n)
	}
}

func TestTextTool(t *testing.T) {
	tool := TextTool("delegate", "d", "task", "the task", func(_ context.Context, text string) (string, error) {
		return "got:" + text, nil
	})
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"hello","extra":1}`))
	if err != nil || firstText(out) != "got:hello" {
		t.Fatalf("Execute = %q, %v; want got:hello", firstText(out), err)
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil || len(schema.Required) != 1 || schema.Required[0] != "task" {
		t.Fatalf("schema = %s (err %v), want required [task]", tool.Schema(), err)
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"task":42}`)); err == nil {
		t.Fatal("non-string arg accepted")
	}
}
