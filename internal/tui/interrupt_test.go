// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

type blockingProvider struct {
	entered chan struct{}
	once    sync.Once
}

func (*blockingProvider) Name() string                    { return "blocking" }
func (*blockingProvider) Capabilities() golm.Capabilities { return golm.Capabilities{} }

func (b *blockingProvider) Complete(ctx context.Context, _ golm.Request) (golm.Response, error) {
	b.once.Do(func() { close(b.entered) })
	<-ctx.Done()
	return golm.Response{}, ctx.Err()
}

func (b *blockingProvider) Stream(ctx context.Context, req golm.Request, _ func(golm.StreamEvent) error) (golm.Response, error) {
	return b.Complete(ctx, req)
}

// Interrupting a turn must abandon the TURN, not the session.
func TestInterruptEndsTheTurnNotTheSession(t *testing.T) {
	prov := &blockingProvider{entered: make(chan struct{})}
	agent := &golm.Agent{Provider: prov, Model: "x"}

	pr, pw := io.Pipe()
	interrupts := make(chan struct{}, 1)
	var out lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{
			Agent: agent, In: pr, Out: &out, Interrupt: interrupts,
		})
	}()

	if _, err := pw.Write([]byte("go on then\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case <-prov.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the provider was never called")
	}
	interrupts <- struct{}{}

	waitFor(t, &out, "interrupted")
	if _, err := pw.Write([]byte("/exit\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the session did not return after /exit — the interrupt took it down")
	}
	pw.Close()

	s := out.String()
	if !strings.Contains(s, "stopping this turn") {
		t.Errorf("no notice that the turn was being abandoned: %q", s)
	}
	if strings.Contains(s, "error:") {
		t.Errorf("an interrupt the user asked for is not an error: %q", s)
	}
}

// Interrupting while idle at the prompt means the ordinary "I want out".
func TestInterruptAtThePromptExits(t *testing.T) {
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	pr, pw := io.Pipe()
	defer pw.Close()
	interrupts := make(chan struct{}, 1)
	var out lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{Agent: agent, In: pr, Out: &out, Interrupt: interrupts})
	}()
	waitFor(t, &out, "you ▸")
	interrupts <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an interrupt at the prompt should end the session")
	}
}

// Without an Interrupt channel nothing changes.
func TestNoInterruptChannelStillRuns(t *testing.T) {
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	var out bytes.Buffer
	if err := Run(context.Background(), Options{
		Agent: agent, In: strings.NewReader("hello\n/exit\n"), Out: &out,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "echo:hello") {
		t.Errorf("turn not echoed: %q", out.String())
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func waitFor(t *testing.T, out *lockedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never saw %q in output:\n%s", want, out.String())
}

// The session must be able to drive a whole orchestration, not only one agent.
func TestSessionDrivesAnOrchestration(t *testing.T) {
	sub := &golm.Agent{Model: "x", Provider: stubProvider{}}
	main := &golm.Agent{Model: "x", Provider: stubProvider{}}
	o := golm.NewOrchestrator(nil)
	o.Add("main", "main", main)
	o.Add("helper", "sub", sub)

	rules, err := golm.RouteRules([]golm.RouteRule{{Agent: "helper", Prefix: "/help"}})
	if err != nil {
		t.Fatalf("RouteRules: %v", err)
	}
	o.Router = rules

	var out bytes.Buffer
	if err := Run(context.Background(), Options{
		Orchestrator: o, In: strings.NewReader("//help me\n/exit\n"), Out: &out,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "echo:/help me") {
		t.Errorf("the routed turn was not answered: %q", out.String())
	}
}

// A routing rule may use a "/command" convention of its own.
func TestDoubleSlashSendsALiteralSlash(t *testing.T) {
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	var out bytes.Buffer
	if err := Run(context.Background(), Options{
		Agent: agent, In: strings.NewReader("//usage please\n/exit\n"), Out: &out,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "echo:/usage please") {
		t.Errorf("the escaped line was not sent as a message: %q", s)
	}
	if strings.Contains(s, "last run:") {
		t.Error("the escaped line ran the /usage command instead")
	}
}

// An unknown command should say how to send it as a message rather than only refusing it.
func TestUnknownCommandSuggestsTheEscape(t *testing.T) {
	agent := &golm.Agent{Provider: stubProvider{}, Model: "x"}
	var out bytes.Buffer
	if err := Run(context.Background(), Options{
		Agent: agent, In: strings.NewReader("/research this\n/exit\n"), Out: &out,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "//research") {
		t.Errorf("no escape suggested: %q", out.String())
	}
}

func TestNoAgentAndNoOrchestratorIsAnError(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), Options{In: strings.NewReader("/exit\n"), Out: &out}); err == nil {
		t.Error("a session with nothing to drive should not start")
	}
}
