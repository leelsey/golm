// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package golm

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// A delegation is one tool call that may take a minute.
func TestDelegationIsBracketedInTheStream(t *testing.T) {
	o, events := streamedDelegation(t, false)
	_ = o
	var start, stop int
	for _, ev := range events {
		switch ev.Type {
		case EventAgentStart:
			start++
			if ev.Agent != "sub" || ev.Depth != 1 {
				t.Errorf("agent_start = %+v, want the sub-agent at depth 1", ev)
			}
			if !strings.Contains(ev.Text, "look it up") {
				t.Errorf("agent_start should carry the task: %q", ev.Text)
			}
		case EventAgentStop:
			stop++
		}
	}
	if start != 1 || stop != 1 {
		t.Errorf("%d start / %d stop events, want 1 and 1", start, stop)
	}
}

// Off by default: a consumer.
func TestSubAgentTextIsWithheldByDefault(t *testing.T) {
	_, events := streamedDelegation(t, false)
	for _, ev := range events {
		if ev.Type == EventTextDelta && ev.Agent == "sub" {
			t.Fatalf("sub-agent text reached the stream without StreamDelegates: %q", ev.Text)
		}
	}
}

func TestStreamDelegatesForwardsAttributedText(t *testing.T) {
	_, events := streamedDelegation(t, true)
	var subText, mainText strings.Builder
	for _, ev := range events {
		if ev.Type != EventTextDelta {
			continue
		}
		switch ev.Agent {
		case "sub":
			if ev.Depth != 1 {
				t.Errorf("sub-agent text at depth %d, want 1", ev.Depth)
			}
			subText.WriteString(ev.Text)
		case "main":
			mainText.WriteString(ev.Text)
		default:
			t.Errorf("unattributed text delta %q from %q", ev.Text, ev.Agent)
		}
	}
	if !strings.Contains(subText.String(), "sub working") {
		t.Errorf("sub-agent text not forwarded: %q", subText.String())
	}
	if !strings.Contains(mainText.String(), "final") {
		t.Errorf("main text lost: %q", mainText.String())
	}
}

func streamedDelegation(t *testing.T, forward bool) (*Orchestrator, []StreamEvent) {
	t.Helper()
	mainP := &streamer{answers: []string{"", "final"}, toolCall: 0}
	subP := &streamer{answers: []string{"sub working"}}
	o := NewOrchestrator(nil)
	o.StreamDelegates = forward
	o.Add("main", "main", &Agent{Provider: mainP, Model: "m"})
	o.Add("sub", "sub", &Agent{Provider: subP, Model: "s"})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}
	var mu sync.Mutex
	var events []StreamEvent
	_, err := o.Stream(context.Background(), NewSession(), "go", func(ev StreamEvent) error {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	return o, events
}

type streamer struct {
	answers  []string
	toolCall int
	calls    int
}

func (s *streamer) Name() string               { return "streamer" }
func (s *streamer) Capabilities() Capabilities { return Capabilities{Streaming: true, Tools: true} }

func (s *streamer) Complete(_ context.Context, _ Request) (Response, error) {
	i := s.calls
	s.calls++
	if i == s.toolCall && s.toolCall >= 0 && len(s.answers) > 1 {
		return Response{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "d1", Name: "sub", Input: json.RawMessage(`{"task":"look it up"}`)},
		}}, StopReason: StopToolUse}, nil
	}
	return Response{Message: AssistantText(s.answers[i]), StopReason: StopEndTurn}, nil
}

func (s *streamer) Stream(ctx context.Context, req Request, fn func(StreamEvent) error) (Response, error) {
	resp, err := s.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	if txt := resp.Message.Text(); txt != "" {
		if err := fn(StreamEvent{Type: EventTextDelta, Text: txt}); err != nil {
			return Response{}, err
		}
	}
	return resp, nil
}

// The caller was promised events from one goroutine.
func TestConcurrentDelegationsDoNotRaceTheCallback(t *testing.T) {
	fanOut := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "a", Name: "one", Input: json.RawMessage(`{"task":"x"}`)},
			ToolUse{ID: "b", Name: "two", Input: json.RawMessage(`{"task":"y"}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("done"), StopReason: StopEndTurn},
	}}
	o := NewOrchestrator(nil)
	o.StreamDelegates = true
	o.Add("main", "main", &Agent{Provider: fanOut, Model: "m", ParallelTools: true})
	o.Add("one", "sub", &Agent{Provider: &streamer{answers: []string{strings.Repeat("one ", 50)}, toolCall: -1}, Model: "s"})
	o.Add("two", "sub", &Agent{Provider: &streamer{answers: []string{strings.Repeat("two ", 50)}, toolCall: -1}, Model: "s"})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatalf("WireDelegation: %v", err)
	}

	var seen []string
	_, err := o.Stream(context.Background(), NewSession(), "go", func(ev StreamEvent) error {
		seen = append(seen, ev.Agent)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(seen) == 0 {
		t.Fatal("no events")
	}
}

// With StreamDelegates off, NOTHING of a sub-agent's own work may reach the caller's stream.
func TestSubAgentToolResultsDoNotLeakWhenForwardingIsOff(t *testing.T) {
	subTools := NewRegistry()
	subTools.Register(NewTool("secret_probe", "d", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) { return "SUB-TOOL-OUTPUT", nil }))
	subProv := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "s1", Name: "secret_probe", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("sub done"), StopReason: StopEndTurn},
	}}
	mainProv := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "d1", Name: "sub", Input: json.RawMessage(`{"task":"go"}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("final"), StopReason: StopEndTurn},
	}}
	o := NewOrchestrator(nil)
	o.StreamDelegates = false
	o.Add("main", "main", &Agent{Provider: mainProv, Model: "m"})
	o.Add("sub", "sub", &Agent{Provider: subProv, Model: "s", Tools: subTools})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var events []StreamEvent
	if _, err := o.Stream(context.Background(), NewSession(), "go", func(ev StreamEvent) error {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for _, ev := range events {
		if ev.Type == EventToolResult && ev.ToolName == "secret_probe" {
			t.Fatalf("a sub-agent's tool result reached the caller with StreamDelegates off: %+v", ev)
		}
		if ev.Depth > 0 && ev.Type != EventAgentStart && ev.Type != EventAgentStop {
			t.Errorf("a depth-%d %s event leaked: %+v", ev.Depth, ev.Type, ev)
		}
	}
}

// And with it ON, the sub-agent's tool results DO arrive, attributed.
func TestSubAgentToolResultsArriveWhenForwardingIsOn(t *testing.T) {
	subTools := NewRegistry()
	subTools.Register(NewTool("probe", "d", json.RawMessage(`{"type":"object"}`),
		func(context.Context, json.RawMessage) (string, error) { return "SUB-OUTPUT", nil }))
	subProv := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "s1", Name: "probe", Input: json.RawMessage(`{}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("sub done"), StopReason: StopEndTurn},
	}}
	mainProv := &fakeProvider{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: []Content{
			ToolUse{ID: "d1", Name: "sub", Input: json.RawMessage(`{"task":"go"}`)},
		}}, StopReason: StopToolUse},
		{Message: AssistantText("final"), StopReason: StopEndTurn},
	}}
	o := NewOrchestrator(nil)
	o.StreamDelegates = true
	o.Add("main", "main", &Agent{Provider: mainProv, Model: "m"})
	o.Add("sub", "sub", &Agent{Provider: subProv, Model: "s", Tools: subTools})
	if err := o.WireDelegation(nil); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var found bool
	if _, err := o.Stream(context.Background(), NewSession(), "go", func(ev StreamEvent) error {
		mu.Lock()
		defer mu.Unlock()
		if ev.Type == EventToolResult && ev.ToolName == "probe" {
			found = true
			if ev.Depth != 1 {
				t.Errorf("the sub-agent's tool result is at depth %d, want 1", ev.Depth)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !found {
		t.Error("the sub-agent's tool result never arrived with forwarding on")
	}
}
