// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package acp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/leelsey/golm"
)

// Terminal method names. All of them run the other way.
const (
	MethodTerminalCreate      = "terminal/create"
	MethodTerminalOutput      = "terminal/output"
	MethodTerminalWaitForExit = "terminal/wait_for_exit"
	MethodTerminalKill        = "terminal/kill"
	MethodTerminalRelease     = "terminal/release"
)

// CreateTerminalRequest asks the editor to start a command.
type CreateTerminalRequest struct {
	SessionID string   `json:"sessionId"`
	Command   string   `json:"command"`
	Args      []string `json:"args,omitempty"`
	Env       []EnvVar `json:"env,omitempty"`
	CWD       string   `json:"cwd,omitempty"`

	OutputByteLimit int64 `json:"outputByteLimit,omitempty"`
}

// CreateTerminalResponse names the terminal the agent then waits on, reads and releases.
type CreateTerminalResponse struct {
	TerminalID string `json:"terminalId"`
}

// TerminalRef addresses a terminal for every call after creation.
type TerminalRef struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

// TerminalOutputResponse is what the command has produced so far.
type TerminalOutputResponse struct {
	Output     string              `json:"output"`
	Truncated  bool                `json:"truncated"`
	ExitStatus *TerminalExitStatus `json:"exitStatus,omitempty"`
}

// TerminalExitStatus is how a command ended.
type TerminalExitStatus struct {
	ExitCode *int    `json:"exitCode"`
	Signal   *string `json:"signal"`
}

// Ended reports whether the command has finished.
func (t *TerminalOutputResponse) Ended() bool { return t != nil && t.ExitStatus != nil }

// Describe renders an exit status for the model.
func (e *TerminalExitStatus) Describe() string {
	switch {
	case e == nil:
		return "still running"
	case e.Signal != nil && *e.Signal != "":
		return "killed by signal " + *e.Signal
	case e.ExitCode != nil:
		return fmt.Sprintf("exited with status %d", *e.ExitCode)
	}
	return "ended"
}

// Failed reports whether the command ended badly.
func (e *TerminalExitStatus) Failed() bool {
	if e == nil {
		return false
	}
	if e.Signal != nil && *e.Signal != "" {
		return true
	}
	return e.ExitCode != nil && *e.ExitCode != 0
}

// TerminalRunner runs commands THROUGH the editor.
type TerminalRunner struct {
	Allow []string

	DenyArgs []string

	OutputLimit int64

	MaxOutput int

	MaxArgs int

	agent *Agent
}

const (
	defaultTerminalOutput int64 = 1 << 20

	defaultTerminalToModel = 16 << 10

	defaultTerminalArgs = 256
)

// Terminals returns the terminal-backed run tool, or nil when nothing is allowed or the client cannot run one.
func (a *Agent) Terminals(r TerminalRunner) golm.Tool {
	if len(r.Allow) == 0 || !a.clientTerminal() {
		return nil
	}
	r.agent = a
	return r.tool()
}

func (a *Agent) clientTerminal() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.clientCaps.Terminal
}

type runArgs struct {
	Command string   `json:"command" jsonschema:"the program to run; must be one the operator allowed"`
	Args    []string `json:"args,omitempty" jsonschema:"arguments, one per element; no shell is involved"`
	CWD     string   `json:"cwd,omitempty" jsonschema:"working directory; omit for the session's own"`
}

func (r TerminalRunner) tool() golm.Tool {
	desc := "Run one of these programs in the editor's terminal, where it can be watched and stopped: " +
		strings.Join(r.Allow, ", ") + ". Arguments are passed directly, without a shell."
	t := golm.NewTypedTool("run", desc, r.run)
	return golm.WithTraits(t, golm.ToolTraits{Process: true, Filesystem: true, Network: true})
}

func (r TerminalRunner) run(ctx context.Context, in runArgs) (string, error) {
	cmd := strings.TrimSpace(in.Command)
	if cmd == "" {
		return "", errors.New("no command given")
	}
	if !slices.Contains(r.Allow, cmd) {
		return "", fmt.Errorf("%q is not allowed here; allowed: %s", cmd, strings.Join(r.Allow, ", "))
	}
	maxArgs := r.MaxArgs
	if maxArgs <= 0 {
		maxArgs = defaultTerminalArgs
	}
	if len(in.Args) > maxArgs {
		return "", fmt.Errorf("%d arguments is over the limit of %d", len(in.Args), maxArgs)
	}
	for _, arg := range in.Args {
		if strings.ContainsRune(arg, 0) {
			return "", errors.New("arguments must not contain NUL bytes")
		}
		for _, bad := range r.DenyArgs {
			if bad != "" && strings.Contains(strings.ToLower(arg), strings.ToLower(bad)) {
				return "", fmt.Errorf("argument %q contains %q, which is not allowed here", truncate(arg, 80), bad)
			}
		}
	}
	sessionID := SessionIDOf(ctx)
	if sessionID == "" {
		return "", errors.New("acp: no session; this tool only works inside a prompt turn")
	}

	limit := r.OutputLimit
	if limit <= 0 {
		limit = defaultTerminalOutput
	}
	var created CreateTerminalResponse
	if err := r.agent.peer.Call(ctx, MethodTerminalCreate, CreateTerminalRequest{
		SessionID: sessionID, Command: cmd, Args: in.Args, CWD: in.CWD, OutputByteLimit: limit,
	}, &created); err != nil {
		return "", fmt.Errorf("the editor could not start %s: %w", cmd, err)
	}
	ref := TerminalRef{SessionID: sessionID, TerminalID: created.TerminalID}

	defer func() {
		rctx, cancel := releaseContext(ctx)
		defer cancel()
		_ = r.agent.peer.Call(rctx, MethodTerminalRelease, ref, nil)
	}()

	if id := golm.ToolCallIDOf(ctx); id != "" {
		r.agent.notify(ctx, sessionID, ToolCallProgress{
			SessionUpdate: UpdateToolCallUpdate,
			ToolCallID:    id,
			Status:        StatusInProgress,
			Content:       []ToolCallContent{{Type: "terminal", TerminalID: created.TerminalID}},
		})
	}

	var waited TerminalExitStatus
	if err := r.agent.peer.Call(ctx, MethodTerminalWaitForExit, ref, &waited); err != nil {
		kctx, cancel := releaseContext(ctx)
		defer cancel()
		_ = r.agent.peer.Call(kctx, MethodTerminalKill, ref, nil)
		return "", fmt.Errorf("%s was stopped: %w", cmd, err)
	}

	var out TerminalOutputResponse
	if err := r.agent.peer.Call(ctx, MethodTerminalOutput, ref, &out); err != nil {
		return "", fmt.Errorf("could not read %s's output: %w", cmd, err)
	}

	status := out.ExitStatus
	if status == nil && (waited.ExitCode != nil || waited.Signal != nil) {
		status = &waited
	}

	maxOut := r.MaxOutput
	if maxOut <= 0 {
		maxOut = defaultTerminalToModel
	}

	body, cut := tail(out.Output, maxOut)
	if out.Truncated || cut {
		body = "[earlier output dropped]\n" + body
	}
	if status.Failed() {
		return "", fmt.Errorf("%s %s\n%s", cmd, status.Describe(), body)
	}
	if strings.TrimSpace(body) == "" {
		return cmd + " " + status.Describe() + " with no output", nil
	}
	return body, nil
}

func tail(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	r := []rune(s)
	if len(r) <= n {
		return s, false
	}
	return string(r[len(r)-n:]), true
}

func releaseContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), terminalCleanupTimeout)
}

const terminalCleanupTimeout = 5 * time.Second
