// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAgentDetailedEvents(t *testing.T) {
	fp := &fakeProvider{responses: []Response{
		{
			Message: Message{Role: RoleAssistant, Content: []Content{
				ToolUse{ID: "c1", Name: "echo", Input: json.RawMessage(`{"x":1}`)},
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
	bus := NewBus()
	events, unsub := bus.Subscribe("*")
	defer unsub()
	agent := &Agent{Provider: fp, Model: "x", Tools: reg, Bus: bus, Name: "a"}

	if _, err := agent.Run(context.Background(), NewSession(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	bus.Close()

	var kinds []string
	byKind := map[string][]Event{}
	for ev := range events {
		kinds = append(kinds, ev.Kind)
		byKind[ev.Kind] = append(byKind[ev.Kind], ev)
	}
	want := []string{"start", "step", "message", "tool", "tool_result", "step", "message", "done"}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}

	s1 := byKind["step"][0].Data.(StepEvent)
	if s1.Step != 1 || s1.StopReason != StopToolUse || s1.Usage.InputTokens != 10 {
		t.Errorf("step 1 = %+v", s1)
	}
	s2 := byKind["step"][1].Data.(StepEvent)
	if s2.Step != 2 || s2.StopReason != StopEndTurn || s2.Usage.OutputTokens != 7 {
		t.Errorf("step 2 = %+v", s2)
	}
	tc := byKind["tool"][0].Data.(ToolCallEvent)
	if tc.Name != "echo" || tc.ID != "c1" || string(tc.Input) != `{"x":1}` {
		t.Errorf("tool call = %+v", tc)
	}
	tr := byKind["tool_result"][0].Data.(ToolResultEvent)
	if tr.Name != "echo" || tr.ID != "c1" || tr.IsError || tr.Content != `echoed:{"x":1}` {
		t.Errorf("tool result = %+v", tr)
	}
}
