// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package acp_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/leelsey/golm"
	"github.com/leelsey/golm/acp"
)

type term struct {
	command  string
	args     []string
	cwd      string
	output   string
	exitCode int
	signal   string

	truncated bool
	released  bool
	killed    bool
}

func withTerminals(t *testing.T, e *editor, tm *term) {
	t.Helper()
	var mu sync.Mutex
	e.peer.Handle(acp.MethodTerminalCreate, func(_ context.Context, params json.RawMessage) (any, error) {
		var req acp.CreateTerminalRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		tm.command, tm.args, tm.cwd = req.Command, req.Args, req.CWD
		return acp.CreateTerminalResponse{TerminalID: "term_1"}, nil
	})
	e.peer.Handle(acp.MethodTerminalWaitForExit, func(context.Context, json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		return exitStatus(tm), nil
	})
	e.peer.Handle(acp.MethodTerminalOutput, func(context.Context, json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		st := exitStatus(tm)
		return acp.TerminalOutputResponse{Output: tm.output, Truncated: tm.truncated, ExitStatus: &st}, nil
	})
	e.peer.Handle(acp.MethodTerminalRelease, func(context.Context, json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		tm.released = true
		return map[string]any{}, nil
	})
	e.peer.Handle(acp.MethodTerminalKill, func(context.Context, json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		tm.killed = true
		return map[string]any{}, nil
	})
}

func exitStatus(tm *term) acp.TerminalExitStatus {
	st := acp.TerminalExitStatus{}
	if tm.signal != "" {
		s := tm.signal
		st.Signal = &s
		return st
	}
	code := tm.exitCode
	st.ExitCode = &code
	return st
}

func termCaps() acp.ClientCapabilities {
	c := fullCaps()
	c.Terminal = true
	return c
}

func runTerminalTool(t *testing.T, tool golm.Tool, id string, args string) (string, error) {
	t.Helper()
	ctx := golm.WithToolCall(acp.SessionContext(context.Background(), id), "call_1")
	out, err := tool.Execute(ctx, json.RawMessage(args))
	return toolText(out), err
}

// A command runs in the EDITOR's terminal, where a person can watch and stop it.
func TestTerminalRunsThroughTheEditor(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, termCaps())
	tm := &term{output: "42 tests passed\n"}
	withTerminals(t, e, tm)
	id := e.newSession(t)

	tool := a.Terminals(acp.TerminalRunner{Allow: []string{"go"}})
	if tool == nil {
		t.Fatal("no terminal tool")
	}
	out, err := runTerminalTool(t, tool, id, `{"command":"go","args":["test","./..."],"cwd":"/proj"}`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "42 tests passed") {
		t.Errorf("output = %q", out)
	}
	if tm.command != "go" || len(tm.args) != 2 || tm.cwd != "/proj" {
		t.Errorf("the editor was asked to run %q %v in %q", tm.command, tm.args, tm.cwd)
	}

	if !tm.released {
		t.Error("the terminal was never released")
	}

	var attached bool
	for _, u := range e.toolUpdates(t) {
		content, _ := u["content"].([]any)
		for _, c := range content {
			if m, ok := c.(map[string]any); ok && m["type"] == "terminal" && m["terminalId"] == "term_1" {
				attached = true
			}
		}
	}
	if !attached {
		t.Error("the running terminal was not attached to the tool call")
	}
}

// A non-zero exit is information, not a tool failure.
func TestTerminalNonZeroExitIsReportedAsAFailure(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, termCaps())
	tm := &term{output: "FAIL: two tests failed\n", exitCode: 1}
	withTerminals(t, e, tm)
	id := e.newSession(t)

	tool := a.Terminals(acp.TerminalRunner{Allow: []string{"go"}})
	_, err := runTerminalTool(t, tool, id, `{"command":"go","args":["test"]}`)
	if err == nil {
		t.Fatal("a failing command reported success")
	}
	if !strings.Contains(err.Error(), "status 1") {
		t.Errorf("err = %v, should name the exit status", err)
	}

	if !strings.Contains(err.Error(), "two tests failed") {
		t.Errorf("err = %v, should carry the command's own output", err)
	}
}

func TestTerminalSignalIsReported(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, termCaps())
	withTerminals(t, e, &term{output: "", signal: "SIGKILL"})
	id := e.newSession(t)
	tool := a.Terminals(acp.TerminalRunner{Allow: []string{"go"}})
	_, err := runTerminalTool(t, tool, id, `{"command":"go"}`)
	if err == nil || !strings.Contains(err.Error(), "SIGKILL") {
		t.Errorf("err = %v, want the signal named", err)
	}
}

func TestTerminalTruncationIsSaidPlainly(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, termCaps())
	withTerminals(t, e, &term{output: "the tail\n", truncated: true})
	id := e.newSession(t)
	tool := a.Terminals(acp.TerminalRunner{Allow: []string{"go"}})
	out, err := runTerminalTool(t, tool, id, `{"command":"go"}`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "dropped") {
		t.Errorf("output = %q; the model should be told output was lost", out)
	}
}

// Being able to WATCH a command is not the same as having agreed to it.
func TestTerminalHonoursTheAllowlist(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, termCaps())
	withTerminals(t, e, &term{})
	id := e.newSession(t)

	tool := a.Terminals(acp.TerminalRunner{Allow: []string{"go"}, DenyArgs: []string{"-exec"}})
	if _, err := runTerminalTool(t, tool, id, `{"command":"rm","args":["-rf","/"]}`); err == nil {
		t.Error("a command outside the allowlist ran")
	}
	if _, err := runTerminalTool(t, tool, id, `{"command":"go","args":["-exec","evil"]}`); err == nil {
		t.Error("a denied argument was accepted")
	}
	if _, err := runTerminalTool(t, tool, id, "{\"command\":\"go\",\"args\":[\"a\\u0000b\"]}"); err == nil {
		t.Error("a NUL in an argument was accepted")
	}

	if a.Terminals(acp.TerminalRunner{}) != nil {
		t.Error("a runner with no allowlist offered a tool")
	}
}

// Offering a tool that cannot work costs the model a turn to discover.
func TestTerminalNotOfferedWithoutTheCapability(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, fullCaps())
	if a.Terminals(acp.TerminalRunner{Allow: []string{"go"}}) != nil {
		t.Error("a terminal tool was offered to a client that cannot run one")
	}
}

func TestTerminalRefusesOutsideATurn(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, termCaps())
	withTerminals(t, e, &term{})
	tool := a.Terminals(acp.TerminalRunner{Allow: []string{"go"}})
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"go"}`)); err == nil {
		t.Error("the terminal tool ran outside a prompt turn")
	}
}

// It runs a program. Saying so is what puts it behind whatever gate the deployment configured.
func TestTerminalDeclaresItsTraits(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, termCaps())
	tool := a.Terminals(acp.TerminalRunner{Allow: []string{"go"}})
	tr, ok := golm.TraitsOf(tool)
	if !ok || !tr.Process {
		t.Errorf("traits = %+v (declared %v); a run tool must declare Process", tr, ok)
	}
	if tr.ReadOnly {
		t.Error("running a program is not read-only")
	}
}

func TestExitStatusDescription(t *testing.T) {
	zero, one := 0, 1
	sig := "SIGTERM"
	for _, c := range []struct {
		st     *acp.TerminalExitStatus
		want   string
		failed bool
	}{
		{nil, "still running", false},
		{&acp.TerminalExitStatus{ExitCode: &zero}, "exited with status 0", false},
		{&acp.TerminalExitStatus{ExitCode: &one}, "exited with status 1", true},
		{&acp.TerminalExitStatus{Signal: &sig}, "killed by signal SIGTERM", true},
	} {
		if got := c.st.Describe(); got != c.want {
			t.Errorf("Describe() = %q, want %q", got, c.want)
		}
		if got := c.st.Failed(); got != c.failed {
			t.Errorf("Failed() = %v for %q", got, c.want)
		}
	}
}

// The editor keeps the tail when it has to drop output.
func TestTerminalOutputKeepsTheTail(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, termCaps())
	tm := &term{output: strings.Repeat("noise\n", 4000) + "PANIC: the actual failure\n", exitCode: 1}
	withTerminals(t, e, tm)

	tool := a.Terminals(acp.TerminalRunner{Allow: []string{"go"}, MaxOutput: 200})
	_, err := runTerminalTool(t, tool, e.newSession(t), `{"command":"go","args":["test"]}`)
	if err == nil {
		t.Fatal("a non-zero exit must reach the model as an error")
	}
	if !strings.Contains(err.Error(), "PANIC: the actual failure") {
		t.Errorf("the failure was truncated away: %v", err)
	}
	if !strings.Contains(err.Error(), "earlier output dropped") {
		t.Errorf("the model was not told output was dropped: %v", err)
	}
}

// terminal/output need not repeat the exit status.
func TestTerminalFallsBackToWaitForExitStatus(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, termCaps())
	tm := &term{output: "boom\n", exitCode: 2}
	withTerminals(t, e, tm)

	e.peer.Handle(acp.MethodTerminalOutput, func(context.Context, json.RawMessage) (any, error) {
		return acp.TerminalOutputResponse{Output: tm.output}, nil
	})

	tool := a.Terminals(acp.TerminalRunner{Allow: []string{"go"}})
	_, err := runTerminalTool(t, tool, e.newSession(t), `{"command":"go","args":["test"]}`)
	if err == nil {
		t.Fatal("the failure was lost: the command read as still running")
	}
	if !strings.Contains(err.Error(), "exited with status 2") {
		t.Errorf("wrong status: %v", err)
	}
}

// A model that emits ten thousand arguments is not making a request the editor should be asked to honour.
func TestTerminalBoundsArgumentCount(t *testing.T) {
	a, _ := newAgent(t, say("x"))
	e := connect(t, a)
	e.initialize(t, termCaps())
	tm := &term{}
	withTerminals(t, e, tm)

	args, _ := json.Marshal(struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}{"go", make([]string, 300)})

	tool := a.Terminals(acp.TerminalRunner{Allow: []string{"go"}})
	if _, err := runTerminalTool(t, tool, e.newSession(t), string(args)); err == nil {
		t.Fatal("300 arguments were accepted")
	}
	if tm.command != "" {
		t.Error("the editor was asked to run it anyway")
	}
}
