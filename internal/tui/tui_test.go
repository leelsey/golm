// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

type stubProvider struct{}

func (stubProvider) Name() string                    { return "stub" }
func (stubProvider) Capabilities() golm.Capabilities { return golm.Capabilities{} }
func (stubProvider) Complete(_ context.Context, req golm.Request) (golm.Response, error) {
	return golm.Response{Message: golm.AssistantText("echo:" + req.Messages[len(req.Messages)-1].Text()), StopReason: golm.StopEndTurn}, nil
}
func (s stubProvider) Stream(ctx context.Context, req golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	last := req.Messages[len(req.Messages)-1].Text()
	_ = fn(golm.StreamEvent{Type: golm.EventTextDelta, Text: "echo:" + last})
	return golm.Response{
		Message:    golm.AssistantText("echo:" + last),
		StopReason: golm.StopEndTurn,
		Usage:      golm.Usage{InputTokens: 3, OutputTokens: 5},
	}, nil
}

func TestRunInteractive(t *testing.T) {
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	in := strings.NewReader("hello\n/reset\nworld\n/exit\nnever\n")
	var out bytes.Buffer

	if err := Run(context.Background(), Options{Agent: agent, In: in, Out: &out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "echo:hello") {
		t.Errorf("first turn not echoed: %q", s)
	}
	if !strings.Contains(s, "session reset") {
		t.Errorf("/reset not handled")
	}
	if !strings.Contains(s, "echo:world") {
		t.Errorf("second turn not echoed")
	}
	if strings.Contains(s, "echo:never") {
		t.Errorf("/exit did not stop the loop")
	}
	if strings.Contains(s, "\033[") {
		t.Errorf("ANSI escapes leaked into non-TTY output")
	}
}

func TestUsageCommand(t *testing.T) {
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	in := strings.NewReader("hello\nworld\n/usage\n/reset\n/usage\n/exit\n")
	var out bytes.Buffer

	if err := Run(context.Background(), Options{Agent: agent, In: in, Out: &out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "(last run: input 3, output 5, total 8)") {
		t.Errorf("/usage last-run line missing: %q", s)
	}
	if !strings.Contains(s, "(session:  input 6, output 10, total 16)") {
		t.Errorf("/usage session line missing: %q", s)
	}
	if !strings.Contains(s, "(session:  input 0, output 0, total 0)") {
		t.Errorf("/reset did not clear usage: %q", s)
	}
}

func TestDebugCommand(t *testing.T) {
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	in := strings.NewReader("quiet\n/debug\ntraced\n/debug\nquiet2\n/exit\n")
	var out bytes.Buffer

	if err := Run(context.Background(), Options{Agent: agent, In: in, Out: &out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "(debug on") || !strings.Contains(s, "(debug off)") {
		t.Errorf("/debug toggle output missing: %q", s)
	}
	if got := strings.Count(s, "(step 1: end_turn — input 3, output 5, total 8)"); got != 1 {
		t.Errorf("trace lines = %d, want exactly 1 (only the debug-on turn): %q", got, s)
	}
	if agent.Bus != nil {
		t.Errorf("tui left its private bus attached to the agent")
	}
}
