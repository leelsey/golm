// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leelsey/golm"
)

func readEntries(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line is not JSON: %q", sc.Text())
		}
		out = append(out, m)
	}
	return out
}

func toolReq(name string, tool golm.Tool) golm.ToolRequest {
	return golm.ToolRequest{
		Agent: "worker", Step: 2, Tool: tool,
		Call: golm.ToolUse{ID: "c1", Name: name, Input: json.RawMessage(`{"path":"go.mod"}`)},
	}
}

// The question an audit answers is "what did this agent do".
func TestAuditRecordsAllowedCallsAndRefusals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	log, err := newAuditLog(path)
	if err != nil {
		t.Fatalf("newAuditLog: %v", err)
	}
	reader := golm.WithTraits(golm.TextTool("read_file", "d", "path", "p", nil), golm.ToolTraits{ReadOnly: true, Filesystem: true})
	log.Record(toolReq("read_file", reader), golm.DecisionAllow, nil)
	log.Record(toolReq("run", nil), golm.DecisionDeny, errors.New("denied by name"))
	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries := readEntries(t, path)
	if len(entries) != 2 {
		t.Fatalf("recorded %d entries, want 2", len(entries))
	}
	ok, refused := entries[0], entries[1]
	if ok["decision"] != "allow" || ok["allowed"] != true {
		t.Errorf("allowed entry = %v", ok)
	}
	if ok["tool"] != "read_file" || ok["agent"] != "worker" || ok["call_id"] != "c1" {
		t.Errorf("allowed entry lost its identity: %v", ok)
	}

	if in, _ := json.Marshal(ok["input"]); !strings.Contains(string(in), "go.mod") {
		t.Errorf("arguments not recorded: %v", ok["input"])
	}
	if refused["allowed"] != false || !strings.Contains(refused["reason"].(string), "denied by name") {
		t.Errorf("refusal entry = %v", refused)
	}

	traits, _ := json.Marshal(refused["traits"])
	if !strings.Contains(string(traits), "undeclared") {
		t.Errorf("traits = %s, want the undeclared marker", traits)
	}
}

// The record holds the arguments of every tool call.
func TestAuditFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	log, err := newAuditLog(path)
	if err != nil {
		t.Fatalf("newAuditLog: %v", err)
	}
	defer log.Close()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("audit log mode = %o, want no group or other access", perm)
	}
}

func TestAuditAppendsAcrossRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	for i := 0; i < 2; i++ {
		log, err := newAuditLog(path)
		if err != nil {
			t.Fatalf("newAuditLog: %v", err)
		}
		log.Record(toolReq("read_file", nil), golm.DecisionAllow, nil)
		if err := log.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	if n := len(readEntries(t, path)); n != 2 {
		t.Errorf("%d entries after two runs, want 2 — the second run truncated the first", n)
	}
}

func TestAuditCloseIsIdempotent(t *testing.T) {
	log, err := newAuditLog(filepath.Join(t.TempDir(), "a.jsonl"))
	if err != nil {
		t.Fatalf("newAuditLog: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}

	log.Record(toolReq("x", nil), golm.DecisionAllow, nil)
}

func TestAuditRefusesAnUnopenablePath(t *testing.T) {
	if _, err := newAuditLog(filepath.Join(t.TempDir(), "no", "such", "dir", "a.jsonl")); err == nil {
		t.Error("an unwritable audit path was accepted; the run would have gone unrecorded")
	}
}

// A policy that asks with no terminal to ask at must refuse.
func TestGateWarnsWhenNobodyCanBeAsked(t *testing.T) {
	var warned strings.Builder
	g, err := (&backendFlags{ask: true}).gate(nil, &warned)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	defer g.close()
	if g.policy == nil || !g.policy.NeedsApprover() {
		t.Fatal("--ask produced no gate")
	}
	if g.approve == nil && !strings.Contains(warned.String(), "no terminal") {
		t.Errorf("no approver and no warning: %q", warned.String())
	}
}

func TestGateOpensTheAuditFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	g, err := (&backendFlags{audit: path}).gate(nil, &strings.Builder{})
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if g.audit == nil {
		t.Fatal("--audit produced no sink")
	}
	g.audit(toolReq("read_file", nil), golm.DecisionAllow, nil)
	if err := g.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if n := len(readEntries(t, path)); n != 1 {
		t.Errorf("%d entries recorded, want 1", n)
	}
}

func TestGateReportsABadAuditPath(t *testing.T) {
	_, err := (&backendFlags{audit: filepath.Join(t.TempDir(), "nope", "a.jsonl")}).gate(nil, &strings.Builder{})
	if err == nil {
		t.Error("a run whose audit cannot be written should not start silently")
	}
}

func TestLoggerFlagValidation(t *testing.T) {
	if l, err := (&backendFlags{}).logger(&strings.Builder{}); err != nil || l != nil {
		t.Errorf("no --log should build no logger: %v, %v", l, err)
	}
	if _, err := (&backendFlags{logLevel: "chatty"}).logger(&strings.Builder{}); err == nil {
		t.Error("an unknown level was accepted")
	}
	for _, lvl := range []string{"debug", "INFO", " warn ", "error"} {
		if l, err := (&backendFlags{logLevel: lvl}).logger(&strings.Builder{}); err != nil || l == nil {
			t.Errorf("--log %q: %v", lvl, err)
		}
	}
}

func TestDescribeCallShowsWhatIsBeingApproved(t *testing.T) {
	run := golm.WithTraits(golm.TextTool("run", "d", "cmd", "c", nil), golm.ToolTraits{Process: true, Network: true})
	got := describeCall(toolReq("run", run))
	for _, want := range []string{"run", "worker", "network", "process", "go.mod"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing %q:\n%s", want, got)
		}
	}

	if !strings.Contains(got, "{") {
		t.Errorf("the arguments must be shown verbatim:\n%s", got)
	}
	undeclared := describeCall(toolReq("mystery", golm.TextTool("mystery", "d", "x", "y", nil)))
	if !strings.Contains(undeclared, "undeclared") {
		t.Errorf("a tool that declares nothing should say so:\n%s", undeclared)
	}
}

func TestPrettyInputIsBounded(t *testing.T) {
	huge, err := json.Marshal(map[string]string{"blob": strings.Repeat("한", 5000)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := prettyInput(huge)
	if len([]rune(got)) > maxShownInput+1 {
		t.Errorf("prompt quoted %d runes; an approval that scrolls off the screen is answered blind", len([]rune(got)))
	}
	if prettyInput(nil) != "" {
		t.Error("no arguments should render as nothing")
	}
	if got := prettyInput(json.RawMessage(`not json`)); got == "" {
		t.Error("unparseable arguments must still be shown; they are what the model sent")
	}
}
