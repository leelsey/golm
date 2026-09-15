// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leelsey/golm"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "golm.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOrchestrateNeedsAConfig(t *testing.T) {
	var errb strings.Builder
	if code := runOrchestrator(context.Background(), &backendFlags{}, "", "hi", false, true, io.Discard, &errb); code == 0 {
		t.Fatal("--orchestrate without --config succeeded")
	}
	if !strings.Contains(errb.String(), "--config") {
		t.Errorf("the error does not name the missing flag: %q", errb.String())
	}
}

func TestOrchestrateReportsAnUnreadableConfig(t *testing.T) {
	var errb strings.Builder
	code := runOrchestrator(context.Background(), &backendFlags{},
		filepath.Join(t.TempDir(), "absent.json"), "hi", false, true, io.Discard, &errb)
	if code == 0 {
		t.Fatal("a missing config succeeded")
	}
	if errb.Len() == 0 {
		t.Error("nothing was reported")
	}
}

// A bus token named but unset must stop the run rather than bridge to a hub unauthenticated.
func TestOrchestrateRefusesAnEmptyBusToken(t *testing.T) {
	cfg := writeCfg(t, `{
      "providers":[{"name":"cat","type":"cli","command":"/bin/cat"}],
      "agents":[{"name":"main","provider":"cat","model":"cat","role":"main"}],
      "bus":"http://127.0.0.1:1","bus_token_env":"GOLM_TEST_BUS_TOKEN_ABSENT"}`)
	t.Setenv("GOLM_TEST_BUS_TOKEN_ABSENT", "")
	var errb strings.Builder
	code := runOrchestrator(context.Background(), &backendFlags{}, cfg, "hi", false, true, io.Discard, &errb)
	if code == 0 {
		t.Fatal("an empty bus token was accepted")
	}
	if !strings.Contains(errb.String(), "bus_token_env") {
		t.Errorf("the error does not name the variable: %q", errb.String())
	}
}

// A routed turn never reaches the main model.
func TestEventDetailExplainsARoutingDecision(t *testing.T) {
	got := eventDetail(golm.Event{Kind: "route", Data: golm.RouteEvent{Agent: "research", Reason: "prefix \"/r\""}})
	if !strings.Contains(got, "research") || !strings.Contains(got, "prefix") {
		t.Errorf("route detail = %q, want the agent and the reason", got)
	}
}

func TestEventDetailRendersToolOutcomes(t *testing.T) {
	ok := eventDetail(golm.Event{Data: golm.ToolResultEvent{Name: "read_file"}})
	if !strings.Contains(ok, "read_file") || !strings.Contains(ok, "ok") {
		t.Errorf("success detail = %q", ok)
	}
	bad := eventDetail(golm.Event{Data: golm.ToolResultEvent{
		Name: "run", IsError: true, Content: strings.Repeat("x", 500)}})
	if !strings.Contains(bad, "error") {
		t.Errorf("failure detail = %q", bad)
	}
	if len(bad) > 300 {
		t.Errorf("a long failure was not bounded: %d chars", len(bad))
	}
	if e := eventDetail(golm.Event{Kind: "error", Data: "went wrong"}); !strings.Contains(e, "went wrong") {
		t.Errorf("error detail = %q", e)
	}
}

// Two interrupts arriving before the first is read must not queue two stops.
func TestInterruptsCoalesce(t *testing.T) {
	bf := &backendFlags{}
	ctx, interrupts, cancel := bf.sessionContext()
	defer cancel()
	_ = ctx

	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Skip("cannot signal this process")
	}
	for i := 0; i < 3; i++ {
		if err := p.Signal(os.Interrupt); err != nil {
			t.Skip("cannot raise SIGINT here")
		}
	}

	<-interrupts
	time.Sleep(100 * time.Millisecond)
	if n := len(interrupts); n > 1 {
		t.Errorf("interrupts queued %d; one pending stop is the contract", n)
	}
}
