// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package clibackend

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

func ask(t *testing.T, cfg Config, text string) (golm.Response, error) {
	t.Helper()
	return New(cfg).Complete(context.Background(), golm.Request{Messages: []golm.Message{golm.UserText(text)}})
}

// PromptViaArg puts the prompt where the placeholder is.
func TestPromptViaArgSubstitutesThePlaceholder(t *testing.T) {
	resp, err := ask(t, Config{
		Name: "echo", Command: "/bin/echo", Via: PromptViaArg,
		Placeholder: "{{prompt}}", Args: []string{"before", "{{prompt}}", "after"},
	}, "carried")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got := resp.Message.Text()
	if !strings.HasPrefix(got, "before ") || !strings.HasSuffix(got, " after") {
		t.Errorf("argv rendered as %q, want the prompt between the surrounding args", got)
	}
	if !strings.Contains(got, "carried") {
		t.Errorf("the prompt never reached the argument: %q", got)
	}
	if strings.Contains(got, "{{prompt}}") {
		t.Errorf("the placeholder survived: %q", got)
	}
}

// The placeholder may be embedded in a larger argument.
func TestPlaceholderIsReplacedWithinAnArgument(t *testing.T) {
	resp, err := ask(t, Config{
		Name: "echo", Command: "/bin/echo", Via: PromptViaArg,
		Placeholder: "{{p}}", Args: []string{"--prompt={{p}}", "--again={{p}}"},
	}, "x")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	got := resp.Message.Text()
	if !strings.HasPrefix(got, "--prompt=") || !strings.Contains(got, " --again=") {
		t.Errorf("got %q, want the placeholder replaced inside each argument", got)
	}
	if strings.Contains(got, "{{p}}") {
		t.Errorf("an occurrence was left unreplaced: %q", got)
	}
}

// Configured for an argument but no argument holds the placeholder.
func TestPromptViaArgFallsBackToStdinWhenNoArgHoldsIt(t *testing.T) {
	resp, err := ask(t, Config{
		Name: "cat", Command: "/bin/cat", Via: PromptViaArg,
		Placeholder: "{{prompt}}", Args: nil,
	}, "delivered anyway")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !strings.Contains(resp.Message.Text(), "delivered anyway") {
		t.Errorf("prompt was lost: %q", resp.Message.Text())
	}
}

// A cancelled run must surface the context's error, not the process's "signal.
func TestCancelledRunSurfacesTheContextError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := New(Config{Name: "sleep", Command: "/bin/sleep", Args: []string{"30"}, Via: PromptViaStdin}).
		Complete(ctx, golm.Request{Messages: []golm.Message{golm.UserText("hi")}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded rather than the signal", err)
	}
	if strings.Contains(err.Error(), "signal:") {
		t.Errorf("the process signal leaked into the error: %v", err)
	}
}

// A command that fails on its own must report what it said on stderr.
func TestFailedCommandCarriesItsStderr(t *testing.T) {
	_, err := ask(t, Config{
		Name: "sh", Command: "/bin/sh", Via: PromptViaStdin,
		Args: []string{"-c", "echo 'the reason' >&2; exit 3"},
	}, "hi")
	if err == nil {
		t.Fatal("a command exiting 3 reported success")
	}
	if !strings.Contains(err.Error(), "the reason") {
		t.Errorf("err = %v, want it to carry stderr", err)
	}
}

// A cancelled STREAM must do the same.
func TestCancelledStreamSurfacesTheContextError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := New(Config{Name: "sleep", Command: "/bin/sleep", Args: []string{"30"}, Via: PromptViaStdin}).
		Stream(ctx, golm.Request{Messages: []golm.Message{golm.UserText("hi")}}, func(golm.StreamEvent) error { return nil })
	if err == nil {
		t.Fatal("a cancelled stream reported success")
	}
	if strings.Contains(err.Error(), "signal:") {
		t.Errorf("the process signal leaked into the error: %v", err)
	}
}

// A callback that fails must stop the stream and not leave the child running.
func TestStreamCallbackErrorStopsTheRun(t *testing.T) {
	want := errors.New("caller stopped")
	_, err := New(Config{Name: "yes", Command: "/bin/sh", Via: PromptViaStdin,
		Args: []string{"-c", "while :; do echo line; done"}}).
		Stream(context.Background(), golm.Request{Messages: []golm.Message{golm.UserText("hi")}},
			func(golm.StreamEvent) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want the callback's error", err)
	}
}

// A missing executable must fail, not hang or report an empty answer.
func TestMissingCommandFails(t *testing.T) {
	if _, err := ask(t, Config{Name: "nope", Command: "/nonexistent/golm-cli-probe"}, "hi"); err == nil {
		t.Fatal("a missing command reported success")
	}
	_, err := New(Config{Name: "nope", Command: "/nonexistent/golm-cli-probe"}).
		Stream(context.Background(), golm.Request{Messages: []golm.Message{golm.UserText("hi")}},
			func(golm.StreamEvent) error { return nil })
	if err == nil {
		t.Fatal("a missing command streamed successfully")
	}
}

// Name and Capabilities are what a host reads to decide what this backend can do.
func TestNameAndCapabilities(t *testing.T) {
	c := New(Config{Name: "claude-cli", Command: "/bin/cat"})
	if c.Name() != "claude-cli" {
		t.Errorf("Name = %q", c.Name())
	}
	caps := c.Capabilities()
	if !caps.Streaming {
		t.Error("Capabilities says it cannot stream, but Stream is implemented")
	}
	if caps.Tools {
		t.Error("Capabilities claims tool support; this backend is text-in/text-out")
	}
}
