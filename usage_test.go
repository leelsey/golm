// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"testing"
)

func TestUsageAddTotal(t *testing.T) {
	var u Usage
	u.Add(Usage{InputTokens: 10, OutputTokens: 5, ThinkingTokens: 2, CacheReadTokens: 4, CacheWriteTokens: 3})
	u.Add(Usage{InputTokens: 1, OutputTokens: 1, ThinkingTokens: 1, CacheReadTokens: 1, CacheWriteTokens: 1})
	want := Usage{InputTokens: 11, OutputTokens: 6, ThinkingTokens: 3, CacheReadTokens: 5, CacheWriteTokens: 4}
	if u != want {
		t.Errorf("Add = %+v, want %+v", u, want)
	}
	if got := u.Total(); got != 20 {
		t.Errorf("Total = %d, want 20", got)
	}
}

func TestAgentSessionAndStepUsage(t *testing.T) {
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
			Usage:      Usage{InputTokens: 20, OutputTokens: 7, CacheReadTokens: 8},
		},
		{
			Message:    AssistantText("again"),
			StopReason: StopEndTurn,
			Usage:      Usage{InputTokens: 40, OutputTokens: 3},
		},
	}}
	reg := NewRegistry()
	reg.Register(echoTool())
	sess := NewSession()
	agent := &Agent{Provider: fp, Model: "x", Tools: reg}

	res, err := agent.Run(context.Background(), sess, "hi")
	if err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	if len(res.StepUsage) != 2 || res.StepUsage[0].InputTokens != 10 || res.StepUsage[1].CacheReadTokens != 8 {
		t.Errorf("StepUsage after run 1 = %+v", res.StepUsage)
	}
	if res.Usage != (Usage{InputTokens: 30, OutputTokens: 12, CacheReadTokens: 8}) {
		t.Errorf("Usage after run 1 = %+v", res.Usage)
	}

	res, err = agent.Run(context.Background(), sess, "more")
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if len(res.StepUsage) != 1 || res.StepUsage[0].InputTokens != 40 {
		t.Errorf("StepUsage after run 2 = %+v", res.StepUsage)
	}
	if res.Usage != (Usage{InputTokens: 40, OutputTokens: 3}) {
		t.Errorf("Usage after run 2 = %+v", res.Usage)
	}
	if sess.Usage() != (Usage{InputTokens: 70, OutputTokens: 15, CacheReadTokens: 8}) {
		t.Errorf("Session.Usage = %+v", sess.Usage())
	}
}
