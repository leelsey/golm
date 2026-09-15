// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

type repeatReader struct {
	s []byte
	i int
}

func (r *repeatReader) Read(p []byte) (int, error) {
	for n := range p {
		p[n] = r.s[r.i]
		r.i = (r.i + 1) % len(r.s)
	}
	return len(p), nil
}

type failProvider struct{ emitTool bool }

func (failProvider) Name() string                    { return "f" }
func (failProvider) Capabilities() golm.Capabilities { return golm.Capabilities{Tools: true} }
func (failProvider) Complete(context.Context, golm.Request) (golm.Response, error) {
	return golm.Response{}, errors.New("boom")
}
func (p failProvider) Stream(ctx context.Context, _ golm.Request, fn func(golm.StreamEvent) error) (golm.Response, error) {
	if p.emitTool {
		_ = fn(golm.StreamEvent{Type: golm.EventToolStart, ToolName: "search"})
		return golm.Response{Message: golm.AssistantText("done")}, nil
	}
	return golm.Response{}, errors.New("boom")
}

func TestRunExitsOnContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	agent := &golm.Agent{Provider: failProvider{}, Model: "x"}

	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{Agent: agent, In: &repeatReader{s: []byte("hi\n")}, Out: io.Discard}) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit on context done (spun on a dead session)")
	}
}

func TestRunShowsToolActivity(t *testing.T) {
	var out strings.Builder
	agent := &golm.Agent{Provider: failProvider{emitTool: true}, Model: "x"}
	if err := Run(context.Background(), Options{Agent: agent, In: strings.NewReader("hi\n"), Out: &out}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "search") {
		t.Errorf("tool activity not shown in TUI output: %q", out.String())
	}
}
