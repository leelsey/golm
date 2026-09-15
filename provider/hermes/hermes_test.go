// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package hermes_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/provider/hermes"
)

type echoProvider struct {
	answers []string
	chunks  [][]string
	calls   int
	last    golm.Request
}

func (e *echoProvider) Name() string { return "echo" }
func (e *echoProvider) Capabilities() golm.Capabilities {
	return golm.Capabilities{Streaming: true}
}

func (e *echoProvider) Complete(_ context.Context, req golm.Request) (golm.Response, error) {
	e.last = req
	a := e.answers[e.calls]
	e.calls++
	return golm.Response{Message: golm.AssistantText(a), StopReason: golm.StopEndTurn}, nil
}

func (e *echoProvider) Stream(_ context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	e.last = req
	i := e.calls
	e.calls++
	var whole strings.Builder
	for _, c := range e.chunks[i] {
		whole.WriteString(c)
		if err := fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: c}); err != nil {
			return golm.Response{}, err
		}
	}
	return golm.Response{Message: golm.AssistantText(whole.String()), StopReason: golm.StopEndTurn}, nil
}

func defs() []golm.ToolDef {
	return []golm.ToolDef{{
		Name: "read_file", Description: "read a file",
		Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
	}}
}

func TestToolsGoIntoThePromptNotTheAPI(t *testing.T) {
	inner := &echoProvider{answers: []string{"hello"}}
	p := hermes.Wrap(inner)
	_, err := p.Complete(context.Background(), golm.Request{Model: "hermes-4", Tools: defs()})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(inner.last.Tools) != 0 {
		t.Error("tools were still sent to an endpoint that cannot use them")
	}
	sys := inner.last.System.Text()
	if !strings.Contains(sys, "<tools>") || !strings.Contains(sys, "read_file") {
		t.Errorf("tool schemas did not reach the prompt:\n%s", sys)
	}
	if !strings.Contains(sys, "<tool_call>") {
		t.Error("the prompt does not tell the model how to call one")
	}

	secs := inner.last.System.Sections()
	if len(secs) == 0 || !strings.Contains(secs[len(secs)-1].Text, "<tools>") {
		t.Error("the tool block should be its own trailing prompt section")
	}
}

func TestTaggedCallBecomesAToolUse(t *testing.T) {
	answer := "I will look.\n<tool_call>\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"go.mod\"}}\n</tool_call>"
	p := hermes.Wrap(&echoProvider{answers: []string{answer}})
	resp, err := p.Complete(context.Background(), golm.Request{Tools: defs()})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if resp.StopReason != golm.StopToolUse {
		t.Errorf("StopReason = %q, want tool_use", resp.StopReason)
	}
	uses := resp.Message.ToolUses()
	if len(uses) != 1 {
		t.Fatalf("got %d tool uses, want 1", len(uses))
	}
	if uses[0].Name != "read_file" {
		t.Errorf("name = %q", uses[0].Name)
	}
	if !strings.Contains(string(uses[0].Input), `"go.mod"`) {
		t.Errorf("arguments = %s", uses[0].Input)
	}
	if uses[0].ID == "" {
		t.Error("a call needs an id to pair a result with")
	}
	if txt := resp.Message.Text(); !strings.Contains(txt, "I will look") {
		t.Errorf("prose around the call was lost: %q", txt)
	}
	if strings.Contains(resp.Message.Text(), "<tool_call>") {
		t.Error("the tag leaked into the visible text")
	}
}

func TestArgumentsAcceptedAsAQuotedObject(t *testing.T) {
	answer := `<tool_call>{"name": "read_file", "arguments": "{\"path\": \"x\"}"}</tool_call>`
	p := hermes.Wrap(&echoProvider{answers: []string{answer}})
	resp, err := p.Complete(context.Background(), golm.Request{Tools: defs()})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	uses := resp.Message.ToolUses()
	if len(uses) != 1 {
		t.Fatalf("got %d uses", len(uses))
	}
	var got map[string]string
	if err := json.Unmarshal(uses[0].Input, &got); err != nil {
		t.Fatalf("arguments are not an object: %s", uses[0].Input)
	}
	if got["path"] != "x" {
		t.Errorf("arguments = %v", got)
	}
}

// The model wrote something it believed was a call.
func TestMalformedCallSurvivesAsText(t *testing.T) {
	answer := "<tool_call>\nnot json at all\n</tool_call>"
	p := hermes.Wrap(&echoProvider{answers: []string{answer}})
	resp, err := p.Complete(context.Background(), golm.Request{Tools: defs()})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.Message.ToolUses()) != 0 {
		t.Error("unparseable JSON became a tool call")
	}
	if !strings.Contains(resp.Message.Text(), "not json at all") {
		t.Errorf("the attempt was discarded: %q", resp.Message.Text())
	}
	if resp.StopReason != golm.StopEndTurn {
		t.Errorf("StopReason = %q, want the turn to end normally", resp.StopReason)
	}
}

func TestToolResultsGoBackAsTaggedUserTurns(t *testing.T) {
	inner := &echoProvider{answers: []string{"thanks"}}
	p := hermes.Wrap(inner)
	msgs := []golm.Message{
		golm.UserText("read go.mod"),
		{Role: golm.RoleAssistant, Content: []golm.Content{
			golm.Text{Text: "looking"},
			golm.ToolUse{ID: "tag_0", Name: "read_file", Input: json.RawMessage(`{"path":"go.mod"}`)},
		}},
		{Role: golm.RoleTool, Content: []golm.Content{
			golm.ToolResult{ToolUseID: "tag_0", Name: "read_file", Content: golm.ToolText("module x")},
		}},
	}
	if _, err := p.Complete(context.Background(), golm.Request{Tools: defs(), Messages: msgs}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	sent := inner.last.Messages
	if len(sent) != 3 {
		t.Fatalf("sent %d messages, want 3", len(sent))
	}

	if !strings.Contains(sent[1].Text(), "<tool_call>") || !strings.Contains(sent[1].Text(), "read_file") {
		t.Errorf("the assistant turn lost its call: %q", sent[1].Text())
	}
	if sent[2].Role != golm.RoleUser {
		t.Errorf("tool results must come back as a user turn, got %q", sent[2].Role)
	}
	if !strings.Contains(sent[2].Text(), "<tool_response>") || !strings.Contains(sent[2].Text(), "module x") {
		t.Errorf("tool result not tagged: %q", sent[2].Text())
	}
}

func TestErrorResultsAreMarked(t *testing.T) {
	inner := &echoProvider{answers: []string{"ok"}}
	p := hermes.Wrap(inner)
	msgs := []golm.Message{{Role: golm.RoleTool, Content: []golm.Content{
		golm.ToolResult{ToolUseID: "tag_0", Name: "read_file", Content: golm.ToolText("no such file"), IsError: true},
	}}}
	if _, err := p.Complete(context.Background(), golm.Request{Tools: defs(), Messages: msgs}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !strings.Contains(inner.last.Messages[0].Text(), "error:") {
		t.Errorf("a failed tool result reads as a successful one: %q", inner.last.Messages[0].Text())
	}
}

func TestWithoutToolsNothingIsRewritten(t *testing.T) {
	inner := &echoProvider{answers: []string{"plain answer"}}
	p := hermes.Wrap(inner)
	resp, err := p.Complete(context.Background(), golm.Request{System: golm.SystemPrompt{}.Add("be brief")})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if strings.Contains(inner.last.System.Text(), "<tools>") {
		t.Error("a request with no tools grew a tool block")
	}
	if resp.Message.Text() != "plain answer" {
		t.Errorf("answer = %q", resp.Message.Text())
	}
}

func TestCapabilitiesAssertTools(t *testing.T) {
	p := hermes.Wrap(&echoProvider{})
	if !p.Capabilities().Tools {
		t.Error("tool calling is what this layer adds; it must say so")
	}
	if !strings.Contains(p.Name(), "hermes") {
		t.Errorf("Name = %q, should say which layers are in play", p.Name())
	}
}
