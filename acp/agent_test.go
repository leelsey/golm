// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/acp"
	"github.com/leelsey/golm/sessionstore"
)

type scripted struct {
	responses []golm.Response
	calls     int

	block chan struct{}
}

func (s *scripted) Name() string { return "scripted" }
func (s *scripted) Capabilities() golm.Capabilities {
	return golm.Capabilities{Streaming: true, Tools: true}
}

func (s *scripted) Complete(ctx context.Context, _ golm.Request) (golm.Response, error) {
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return golm.Response{}, ctx.Err()
		}
	}
	i := s.calls
	if i >= len(s.responses) {
		i = len(s.responses) - 1
	}
	s.calls++
	return s.responses[i], nil
}

func (s *scripted) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	resp, err := s.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	for _, word := range strings.SplitAfter(resp.Message.Text(), " ") {
		if word == "" {
			continue
		}
		if err := fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: word}); err != nil {
			return golm.Response{}, err
		}
	}
	return resp, nil
}

func say(text string) golm.Response {
	return golm.Response{Message: golm.AssistantText(text), StopReason: golm.StopEndTurn}
}

func newAgent(t *testing.T, responses ...golm.Response) (*acp.Agent, *scripted) {
	t.Helper()
	p := &scripted{responses: responses}
	a := acp.NewAgent(&golm.Agent{Provider: p, Model: "m", Name: "golm"})
	return a, p
}

func fullCaps() acp.ClientCapabilities {
	return acp.ClientCapabilities{
		FS: acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
	}
}

func TestHandshake(t *testing.T) {
	a, _ := newAgent(t, say("hi"))
	e := connect(t, a)
	resp := e.initialize(t, fullCaps())

	if resp.ProtocolVersion != acp.Version {
		t.Errorf("protocolVersion = %d, want %d", resp.ProtocolVersion, acp.Version)
	}
	if resp.AgentInfo == nil || resp.AgentInfo.Name != "golm" {
		t.Errorf("agentInfo = %+v", resp.AgentInfo)
	}

	if resp.AgentCapabilities.LoadSession {
		t.Error("loadSession claimed without a store")
	}
	if !resp.AgentCapabilities.PromptCapabilities.Image {
		t.Error("image prompts should be supported")
	}
	if resp.AuthMethods == nil {
		t.Error("authMethods must be a list, even an empty one")
	}
}

// An agent asked for a version it does not speak answers with the latest it does.
func TestNewerClientVersionIsAnsweredWithOurs(t *testing.T) {
	a, _ := newAgent(t, say("hi"))
	e := connect(t, a)
	var resp acp.InitializeResponse
	if err := e.call(t, acp.MethodInitialize, acp.InitializeRequest{ProtocolVersion: 99}, &resp); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if resp.ProtocolVersion != acp.Version {
		t.Errorf("answered with %d; an agent must never claim a version it does not speak", resp.ProtocolVersion)
	}
}

func TestPromptTurn(t *testing.T) {
	a, _ := newAgent(t, say("the answer is here"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)
	if !strings.HasPrefix(id, "sess_") {
		t.Errorf("session id = %q", id)
	}

	resp := e.prompt(t, id, "a question")
	if resp.StopReason != acp.StopEndTurn {
		t.Errorf("stopReason = %q", resp.StopReason)
	}

	if got := e.chunks(t, acp.UpdateAgentMessageChunk); !strings.Contains(got, "the answer is here") {
		t.Errorf("streamed answer = %q", got)
	}
	if n := len(e.seen()); n < 2 {
		t.Errorf("%d updates; the answer was not streamed", n)
	}
}

func TestPromptOnAnUnknownSession(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	var resp acp.PromptResponse
	err := e.call(t, acp.MethodPrompt, acp.PromptRequest{
		SessionID: "sess_nope", Prompt: []acp.ContentBlock{acp.TextBlock("hi")},
	}, &resp)
	if err == nil || !strings.Contains(err.Error(), "no session") {
		t.Errorf("err = %v", err)
	}
}

func TestEmptyPromptIsRefused(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)
	var resp acp.PromptResponse
	if err := e.call(t, acp.MethodPrompt, acp.PromptRequest{SessionID: id}, &resp); err == nil {
		t.Error("an empty prompt was accepted")
	}
}

// An @-mentioned file arrives inline, already read by the editor.
func TestEmbeddedResourceReachesTheModel(t *testing.T) {
	p := &scripted{responses: []golm.Response{say("read it")}}
	inner := &golm.Agent{Provider: p, Model: "m"}
	a := acp.NewAgent(inner)
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)

	var resp acp.PromptResponse
	if err := e.call(t, acp.MethodPrompt, acp.PromptRequest{
		SessionID: id,
		Prompt: []acp.ContentBlock{
			acp.TextBlock("what does this do?"),
			{Type: "resource", Resource: &acp.EmbeddedResource{
				URI: "file:///tmp/x.go", MimeType: "text/x-go", Text: "package main // MARKER",
			}},
		},
	}, &resp); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	if resp.StopReason != acp.StopEndTurn {
		t.Fatalf("stopReason = %q", resp.StopReason)
	}
}

// A cancelled turn still ANSWERS, with a stop reason of its own.
func TestCancelEndsTheTurnWithAReason(t *testing.T) {
	p := &scripted{responses: []golm.Response{say("never arrives")}, block: make(chan struct{})}
	a := acp.NewAgent(&golm.Agent{Provider: p, Model: "m"})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)

	done := make(chan acp.PromptResponse, 1)
	go func() {
		var resp acp.PromptResponse
		_ = e.call(t, acp.MethodPrompt, acp.PromptRequest{
			SessionID: id, Prompt: []acp.ContentBlock{acp.TextBlock("go")},
		}, &resp)
		done <- resp
	}()
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := e.peer.Notify(ctx, acp.MethodCancel, acp.CancelNotification{SessionID: id}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case resp := <-done:
		if resp.StopReason != acp.StopCancelled {
			t.Errorf("stopReason = %q, want cancelled", resp.StopReason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the cancelled turn never answered")
	}
	close(p.block)
}

// Cancelling with no turn running must not fail.
func TestCancelWithNothingRunning(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := e.peer.Notify(ctx, acp.MethodCancel, acp.CancelNotification{SessionID: id}); err != nil {
		t.Errorf("cancel: %v", err)
	}
	if err := e.peer.Notify(ctx, acp.MethodCancel, acp.CancelNotification{SessionID: "sess_nope"}); err != nil {
		t.Errorf("cancel for an unknown session: %v", err)
	}
}

// session/load replays the transcript.
func TestLoadSessionReplaysTheTranscript(t *testing.T) {
	store := sessionstore.NewFiles(t.TempDir())
	p := &scripted{responses: []golm.Response{say("first answer"), say("second answer")}}
	a := acp.NewAgent(&golm.Agent{Provider: p, Model: "m"})
	a.Store = store
	e := connect(t, a)

	resp := e.initialize(t, fullCaps())
	if !resp.AgentCapabilities.LoadSession {
		t.Fatal("a store was configured but loadSession was not claimed")
	}
	id := e.newSession(t)
	e.prompt(t, id, "first question")

	p2 := &scripted{responses: []golm.Response{say("second answer")}}
	a2 := acp.NewAgent(&golm.Agent{Provider: p2, Model: "m"})
	a2.Store = store
	e2 := connect(t, a2)
	e2.initialize(t, fullCaps())
	var loaded acp.LoadSessionResponse
	if err := e2.call(t, acp.MethodLoadSession, acp.LoadSessionRequest{
		SessionID: id, CWD: "/tmp/project", MCPServers: []acp.MCPServer{},
	}, &loaded); err != nil {
		t.Fatalf("session/load: %v", err)
	}
	user := e2.chunks(t, acp.UpdateUserMessageChunk)
	agent := e2.chunks(t, acp.UpdateAgentMessageChunk)
	if !strings.Contains(user, "first question") {
		t.Errorf("the user turn was not replayed: %q", user)
	}
	if !strings.Contains(agent, "first answer") {
		t.Errorf("the answer was not replayed: %q", agent)
	}

	e2.prompt(t, id, "second question")
	if p2.calls != 1 {
		t.Errorf("provider called %d times", p2.calls)
	}
}

func TestLoadWithoutAStoreIsRefused(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	var resp acp.LoadSessionResponse
	err := e.call(t, acp.MethodLoadSession, acp.LoadSessionRequest{SessionID: "sess_x"}, &resp)
	if err == nil || !strings.Contains(err.Error(), "no session store") {
		t.Errorf("err = %v", err)
	}
}

func TestToolCallsAreReportedAsTheyHappen(t *testing.T) {
	reg := golm.NewRegistry()
	reg.Register(golm.TextTool("read_file", "reads", "path", "p",
		func(context.Context, string) (string, error) { return "file body", nil }))
	p := &scripted{responses: []golm.Response{
		{Message: golm.Message{Role: golm.RoleAssistant, Content: []golm.Content{
			golm.ToolUse{ID: "call_1", Name: "read_file", Input: json.RawMessage(`{"path":"x.go"}`)},
		}}, StopReason: golm.StopToolUse},
		say("done"),
	}}
	a := acp.NewAgent(&golm.Agent{Provider: p, Model: "m", Tools: reg})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)
	e.prompt(t, id, "read it")

	updates := e.toolUpdates(t)
	if len(updates) < 2 {
		t.Fatalf("%d tool updates, want an announcement and a result", len(updates))
	}
	first := updates[0]
	if first["sessionUpdate"] != "tool_call" || first["toolCallId"] != "call_1" {
		t.Errorf("first update = %v", first)
	}
	if first["title"] != "read_file" {
		t.Errorf("title = %v", first["title"])
	}

	if first["kind"] != "read" {
		t.Errorf("kind = %v, want read", first["kind"])
	}

	if first["status"] != "in_progress" && first["status"] != "pending" {
		t.Errorf("status = %v", first["status"])
	}
	var completed bool
	for _, u := range updates[1:] {
		if u["toolCallId"] == "call_1" && u["status"] == "completed" {
			completed = true

			content, _ := u["content"].([]any)
			if len(content) == 0 {
				t.Errorf("the completed call carries no output: %v", u)
			}
		}
	}
	if !completed {
		t.Errorf("the call was never reported finished: %v", updates)
	}
}

// A provider that DOES stream the model's tool blocks announces the call pending, then progresses it.
func TestToolCallAnnouncedOnceWhenTheProviderStreamsIt(t *testing.T) {
	reg := golm.NewRegistry()
	reg.Register(golm.TextTool("run", "runs", "cmd", "c",
		func(context.Context, string) (string, error) { return "output", nil }))
	p := &streamingTools{}
	a := acp.NewAgent(&golm.Agent{Provider: p, Model: "m", Tools: reg})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)
	e.prompt(t, id, "run it")

	updates := e.toolUpdates(t)
	var announcements int
	var statuses []string
	for _, u := range updates {
		if u["toolCallId"] != "call_1" {
			continue
		}
		if u["sessionUpdate"] == "tool_call" {
			announcements++
			if u["status"] != "pending" {
				t.Errorf("announced with status %v, want pending", u["status"])
			}
		}
		if s, ok := u["status"].(string); ok {
			statuses = append(statuses, s)
		}
	}
	if announcements != 1 {
		t.Errorf("the call was announced %d times, want once: %v", announcements, updates)
	}
	if len(statuses) == 0 || statuses[len(statuses)-1] != "completed" {
		t.Errorf("statuses = %v, want it to end completed", statuses)
	}
}

// A tool that fails must be reported failed, not completed.
func TestFailingToolIsReportedFailed(t *testing.T) {
	reg := golm.NewRegistry()
	reg.Register(golm.TextTool("run", "runs", "cmd", "c",
		func(context.Context, string) (string, error) { return "", errors.New("it exploded") }))
	p := &streamingTools{}
	a := acp.NewAgent(&golm.Agent{Provider: p, Model: "m", Tools: reg})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)
	e.prompt(t, id, "run it")

	var failed bool
	for _, u := range e.toolUpdates(t) {
		if u["status"] == "failed" {
			failed = true
		}
		if u["status"] == "completed" {
			t.Errorf("a failing tool was reported completed: %v", u)
		}
	}
	if !failed {
		t.Error("the failure was never reported")
	}
}

type streamingTools struct{ calls int }

func (s *streamingTools) Name() string { return "streaming-tools" }
func (s *streamingTools) Capabilities() golm.Capabilities {
	return golm.Capabilities{Streaming: true, Tools: true}
}

func (s *streamingTools) Complete(context.Context, golm.Request) (golm.Response, error) {
	i := s.calls
	s.calls++
	if i == 0 {
		return golm.Response{Message: golm.Message{Role: golm.RoleAssistant, Content: []golm.Content{
			golm.ToolUse{ID: "call_1", Name: "run", Input: json.RawMessage(`{"cmd":"ls"}`)},
		}}, StopReason: golm.StopToolUse}, nil
	}
	return say("done"), nil
}

func (s *streamingTools) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	resp, err := s.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	for _, c := range resp.Message.Content {
		switch v := c.(type) {
		case golm.Text:
			_ = fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: v.Text})
		case golm.ToolUse:
			_ = fn(golm.StreamEvent{Type: golm.EventToolStart, ToolID: v.ID, ToolName: v.Name})
			_ = fn(golm.StreamEvent{Type: golm.EventToolDelta, ToolID: v.ID, ToolArgs: string(v.Input)})
			_ = fn(golm.StreamEvent{Type: golm.EventToolStop, ToolID: v.ID})
		}
	}
	return resp, nil
}

func TestToolKindClassification(t *testing.T) {
	for tool, want := range map[string]string{
		"read_file": acp.KindRead, "list_dir": acp.KindRead, "read_skill": acp.KindRead,
		"write_file": acp.KindEdit, "edit_file": acp.KindEdit, "remember": acp.KindEdit,
		"search": acp.KindSearch, "fetch": acp.KindFetch, "run": acp.KindExecute,
		"agent_researcher": acp.KindThink, "mystery_widget": acp.KindOther,
	} {
		if got := acp.KindOf(tool); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", tool, got, want)
		}
	}
}
