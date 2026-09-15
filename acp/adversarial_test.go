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
)

// The editor is another program.
func TestHostileRequestsDoNotKillTheConnection(t *testing.T) {
	a, _ := newAgent(t, say("still alive"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)

	bad := map[string]any{
		"initialize with nothing":  nil,
		"prompt with no session":   map[string]any{"prompt": []any{}},
		"prompt with null blocks":  map[string]any{"sessionId": id, "prompt": nil},
		"unknown block type":       map[string]any{"sessionId": id, "prompt": []any{map[string]any{"type": "hologram"}}},
		"image that is not base64": map[string]any{"sessionId": id, "prompt": []any{map[string]any{"type": "image", "data": "!!!not base64!!!", "mimeType": "image/png"}}},
		"resource with no body":    map[string]any{"sessionId": id, "prompt": []any{map[string]any{"type": "resource"}}},
		"wrong types":              map[string]any{"sessionId": 42, "prompt": "not a list"},
	}
	for name, params := range bad {
		t.Run(name, func(t *testing.T) {
			var resp acp.PromptResponse
			_ = e.call(t, acp.MethodPrompt, params, &resp)

			if got := e.prompt(t, id, "are you there?"); got.StopReason != acp.StopEndTurn {
				t.Errorf("the connection stopped working after %s: %q", name, got.StopReason)
			}
		})
	}
}

// A method the agent does not serve must be refused, not answered.
func TestUnknownMethodsAreRefused(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	for _, method := range []string{"session/delete", "terminal/create", "nonsense", ""} {
		var out map[string]any
		if err := e.call(t, method, map[string]any{}, &out); err == nil {
			t.Errorf("method %q was answered", method)
		}
	}
}

// A prompt before initialize.
func TestPromptBeforeInitialize(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)

	var resp acp.PromptResponse
	if err := e.call(t, acp.MethodPrompt, acp.PromptRequest{
		SessionID: "sess_invented", Prompt: []acp.ContentBlock{acp.TextBlock("hi")},
	}, &resp); err == nil {
		t.Error("a prompt on an invented session was accepted")
	}

	if len(a.FileTools()) != 0 {
		t.Error("file tools were offered before the client declared anything")
	}
}

// Initialising twice must not run the setup hook twice.
func TestRepeatedInitializeRunsTheHookOnce(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	var runs int
	a.OnInitialized = func() { runs++ }
	e := connect(t, a)
	for i := 0; i < 3; i++ {
		e.initialize(t, fullCaps())
	}
	if runs != 1 {
		t.Errorf("the setup hook ran %d times", runs)
	}
}

// A very large prompt must be handled or refused, never silently truncated into a different question.
func TestVeryLargePrompt(t *testing.T) {
	a, p := newAgent(t, say("read it"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)
	huge := strings.Repeat("한글과 english mixed content. ", 20000)
	resp := e.prompt(t, id, huge)
	if resp.StopReason != acp.StopEndTurn {
		t.Errorf("stopReason = %q", resp.StopReason)
	}
	if p.calls != 1 {
		t.Errorf("provider called %d times", p.calls)
	}
}

// Cancelling twice, and cancelling after the turn ended, must be harmless.
func TestRepeatedCancel(t *testing.T) {
	a, _ := newAgent(t, say("done"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)
	e.prompt(t, id, "go")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		if err := e.peer.Notify(ctx, acp.MethodCancel, acp.CancelNotification{SessionID: id}); err != nil {
			t.Fatalf("cancel %d: %v", i, err)
		}
	}

	if got := e.prompt(t, id, "again"); got.StopReason != acp.StopEndTurn {
		t.Errorf("stopReason = %q after repeated cancels", got.StopReason)
	}
}

// A tool result far larger than an editor can render must be bounded on the way out, not handed over whole.
func TestHugeToolOutputIsBoundedForTheEditor(t *testing.T) {
	reg := golm.NewRegistry()
	reg.Register(golm.TextTool("dump", "d", "x", "y",
		func(context.Context, string) (string, error) {
			return strings.Repeat("y", 1<<20), nil
		}))
	p := &scripted{responses: []golm.Response{
		{Message: golm.Message{Role: golm.RoleAssistant, Content: []golm.Content{
			golm.ToolUse{ID: "call_1", Name: "dump", Input: json.RawMessage(`{"x":"go"}`)},
		}}, StopReason: golm.StopToolUse},
		say("done"),
	}}
	a := acp.NewAgent(&golm.Agent{Provider: p, Model: "m", Tools: reg})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)
	e.prompt(t, id, "dump it")

	for _, u := range e.toolUpdates(t) {
		content, _ := u["content"].([]any)
		for _, c := range content {
			m, ok := c.(map[string]any)
			if !ok {
				continue
			}
			inner, _ := m["content"].(map[string]any)
			text, _ := inner["text"].(string)
			if len(text) > 64<<10 {
				t.Errorf("a %d-byte tool result was sent to the editor whole", len(text))
			}
		}
	}
}

type failProvider struct{ err error }

func (f *failProvider) Name() string                    { return "fail" }
func (f *failProvider) Capabilities() golm.Capabilities { return golm.Capabilities{Tools: true} }
func (f *failProvider) Complete(context.Context, golm.Request) (golm.Response, error) {
	return golm.Response{}, f.err
}
func (f *failProvider) Stream(ctx context.Context, r golm.Request, _ func(golm.StreamEvent) error) (golm.Response, error) {
	return f.Complete(ctx, r)
}

// A failure whose MESSAGE mentions cancellation is still a failure.
func TestFailureMentioningCancellationIsNotTreatedAsOne(t *testing.T) {
	a := acp.NewAgent(&golm.Agent{
		Provider: &failProvider{err: errors.New("run: the command failed: context canceled")},
		Model:    "m", Name: "golm",
	})
	e := connect(t, a)
	e.initialize(t, fullCaps())
	id := e.newSession(t)

	var resp PromptResult
	err := e.call(t, acp.MethodPrompt, acp.PromptRequest{
		SessionID: id, Prompt: []acp.ContentBlock{{Type: "text", Text: "go"}},
	}, &resp)
	if err == nil && resp.StopReason == string(acp.StopEndTurn) {
		t.Fatalf("a failed turn was reported as %q with nothing said about why", resp.StopReason)
	}
}

// PromptResult is the raw shape.
type PromptResult struct {
	StopReason string `json:"stopReason"`
}
